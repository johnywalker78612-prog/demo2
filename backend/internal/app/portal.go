package app

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type field struct {
	Key, Label, Value, Type string
	Textarea                bool
	MaxLength               int
	Placeholder             string
}
type portalData struct {
	AdminOTPEnabled                        bool
	AdminOTPCode                           string
	HideEntriesAllowed                     bool
	EmailEnabled                           bool
	Title, CSRF, Message, SharePath, Token string
	Person                                 Person
	People                                 []Person
	Fields                                 []field
	Shares                                 []Share
	Outbox                                 []Message
	Audit                                  []AuditEvent
	Shared                                 map[string]any
	View                                   string
	List                                   PeopleList
	Report                                 Report
}

//go:embed templates/portal.html
var portalHTML string

//go:embed templates/portal.css
var portalCSS string

//go:embed templates/photo-editor.js
var photoEditorJS string
var portal = template.Must(template.New("portal").Funcs(template.FuncMap{
	"date": func(t time.Time) string { return t.In(reportLocation).Format("02 Jan 2006") },
	"initial": func(p Person) string {
		r := []rune(p.FirstName)
		if len(r) == 0 {
			return "?"
		}
		return strings.ToUpper(string(r[0]))
	},
	"fieldMax": func(f field) int {
		if f.MaxLength > 0 {
			return f.MaxLength
		}
		return 250
	},
}).Parse(strings.Replace(portalHTML, "/*ADMIN_CSS*/", portalCSS, 1)))

func render(w http.ResponseWriter, status int, d portalData) {
	if d.View == "person" || d.View == "shared" {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'; connect-src 'self'")
	}
	var buffer bytes.Buffer
	if err := portal.Execute(&buffer, d); err != nil {
		fail(w, 500, "Could not render page")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buffer.WriteTo(w)
}
func (s *Server) current(r *http.Request) (session, bool) {
	c, err := r.Cookie("private_records_session")
	if err != nil {
		return session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.sessions[hash(c.Value)]
	return v, ok && time.Now().Before(v.Expires)
}
func (s *Server) portalAuth(next func(http.ResponseWriter, *http.Request, session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.current(r)
		if !ok {
			http.Redirect(w, r, "/admin/login", 303)
			return
		}
		if r.Method != "GET" {
			r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				if err := r.ParseMultipartForm(6 << 20); err != nil {
					fail(w, 400, "Invalid upload")
					return
				}
				if r.MultipartForm != nil {
					defer r.MultipartForm.RemoveAll()
				}
			} else {
				if err := r.ParseForm(); err != nil {
					fail(w, 400, "Invalid form")
					return
				}
			}
			if !equal(r.FormValue("csrf"), sess.CSRF) {
				fail(w, 403, "Invalid form token")
				return
			}
		}
		next(w, r, sess)
	}
}
func (s *Server) portalRoutes() {
	s.mux.HandleFunc("GET /shared-assets/print.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = io.WriteString(w, `document.querySelector('[data-print-record]').addEventListener('click',function(){window.print()});`)
	})
	s.mux.HandleFunc("GET /shared/{token}/pdf", s.sharedPDF)
	s.mux.HandleFunc("GET /admin/outbox/{id}/preview", s.portalAuth(func(w http.ResponseWriter, r *http.Request, _ session) { s.mailPreview(w, r) }))
	s.mux.HandleFunc("POST /admin/outbox/{id}/send", s.portalAuth(func(w http.ResponseWriter, r *http.Request, _ session) {
		if err := s.deliverMessage(r.PathValue("id")); err != nil {
			render(w, 422, portalData{Title: "Email not sent", Message: err.Error()})
			return
		}
		http.Redirect(w, r, "/admin/outbox", 303)
	}))
	s.mux.HandleFunc("POST /admin/share-email", s.portalAuth(func(w http.ResponseWriter, r *http.Request, _ session) {
		token := strings.TrimPrefix(r.FormValue("share_path"), "/shared/")
		id := s.store.snapshot().ShareHashes[hash(token)]
		if _, err := s.createShareMessage(id, token, r.FormValue("recipient")); err != nil {
			fail(w, 422, err.Error())
			return
		}
		http.Redirect(w, r, "/admin/outbox", 303)
	}))
	s.mux.HandleFunc("GET /verify-code/{challenge}", func(w http.ResponseWriter, r *http.Request) {
		render(w, 200, portalData{Title: "Private Records email verification", View: "verify-code", Token: r.PathValue("challenge")})
	})
	s.mux.HandleFunc("POST /verify-code/{challenge}", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil {
			fail(w, 400, "Invalid form")
			return
		}
		if err := s.verifyCode(r.PathValue("challenge"), r.FormValue("code")); err != nil {
			render(w, 400, portalData{Title: "Private Records email verification", View: "verify-code", Token: r.PathValue("challenge"), Message: "Code is incorrect, expired, or already used. After five failed attempts, request a new code."})
			return
		}
		render(w, 200, portalData{Title: "Email confirmed", Message: "Email ownership confirmed for Private Records. The record remains unverified."})
	})
	s.mux.HandleFunc("GET /admin/assets/photo-editor.js", s.portalAuth(func(w http.ResponseWriter, r *http.Request, _ session) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = io.WriteString(w, photoEditorJS)
	}))
	s.mux.HandleFunc("GET /admin/login", func(w http.ResponseWriter, r *http.Request) {
		nonce := randomToken("")
		http.SetCookie(w, &http.Cookie{Name: "login_nonce", Value: nonce, Path: "/admin/login", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.cfg.SecureCookies, MaxAge: 600})
		render(w, 200, portalData{Title: "Admin sign in", View: "login", CSRF: nonce})
	})
	s.mux.HandleFunc("POST /admin/login", s.login)
	s.mux.HandleFunc("POST /admin/logout", s.portalAuth(func(w http.ResponseWriter, r *http.Request, _ session) {
		c, _ := r.Cookie("private_records_session")
		s.mu.Lock()
		delete(s.sessions, hash(c.Value))
		s.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "private_records_session", Value: "", Path: "/admin", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.cfg.SecureCookies, MaxAge: -1})
		http.Redirect(w, r, "/admin/login", 303)
	}))
	s.mux.HandleFunc("GET /admin", s.portalAuth(func(w http.ResponseWriter, r *http.Request, se session) {
		d := s.store.snapshot()
		list := queryPeople(d, nil)
		if len(list.Items) > 5 {
			list.Items = list.Items[:5]
		}
		render(w, 200, portalData{Title: "Overview", View: "dashboard", CSRF: se.CSRF, People: list.Items, Report: makeReport(d, time.Now(), reportMonths(r.URL.Query()))})
	}))
	s.mux.HandleFunc("GET /admin/people", s.portalAuth(func(w http.ResponseWriter, r *http.Request, se session) {
		list := queryPeople(s.store.snapshot(), r.URL.Query())
		render(w, 200, portalData{Title: "People", View: "list", CSRF: se.CSRF, People: list.Items, List: list})
	}))
	s.mux.HandleFunc("GET /admin/people/new", s.portalAuth(func(w http.ResponseWriter, r *http.Request, se session) { s.renderPerson(w, se, Person{}, "", "") }))
	s.mux.HandleFunc("POST /admin/people/new", s.portalAuth(s.portalSave))
	s.mux.HandleFunc("GET /admin/people/{id}", s.portalAuth(func(w http.ResponseWriter, r *http.Request, se session) {
		p, ok := s.store.snapshot().People[r.PathValue("id")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		message := ""
		if r.URL.Query().Get("saved") == "1" {
			message = "Record saved successfully."
		}
		s.renderPerson(w, se, p, message, "")
	}))
	s.mux.HandleFunc("POST /admin/people/{id}", s.portalAuth(s.portalSave))
	s.mux.HandleFunc("GET /admin/people/{id}/photo", s.portalAuth(func(w http.ResponseWriter, r *http.Request, _ session) { s.getPhoto(w, r) }))
	s.mux.HandleFunc("POST /admin/people/{id}/photo", s.portalAuth(func(w http.ResponseWriter, r *http.Request, se session) {
		file, _, err := r.FormFile("photo")
		if err != nil {
			fail(w, 400, "Select a photo")
			return
		}
		defer file.Close()
		raw, err := io.ReadAll(io.LimitReader(file, (5<<20)+1))
		if err != nil || len(raw) > 5<<20 {
			fail(w, 413, "Photo exceeds 5 MB")
			return
		}
		photo, err := normalizePhoto(raw)
		if err != nil {
			fail(w, 422, err.Error())
			return
		}
		if result(w, s.savePhoto(r.PathValue("id"), photo)) {
			http.Redirect(w, r, "/admin/people/"+r.PathValue("id"), 303)
		}
	}))
	s.mux.HandleFunc("POST /admin/people/{id}/photo/delete", s.portalAuth(func(w http.ResponseWriter, r *http.Request, _ session) {
		if result(w, s.removePhoto(r.PathValue("id"))) {
			http.Redirect(w, r, "/admin/people/"+r.PathValue("id"), 303)
		}
	}))
	s.mux.HandleFunc("GET /admin/people/{id}/delete", s.portalAuth(func(w http.ResponseWriter, r *http.Request, se session) {
		p, ok := s.store.snapshot().People[r.PathValue("id")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		render(w, 200, portalData{Title: "Delete record", View: "delete", Person: p, CSRF: se.CSRF})
	}))
	s.mux.HandleFunc("POST /admin/people/{id}/delete", s.portalAuth(func(w http.ResponseWriter, r *http.Request, _ session) {
		if result(w, s.removePerson(r.PathValue("id"))) {
			http.Redirect(w, r, "/admin", 303)
		}
	}))
	s.mux.HandleFunc("POST /admin/people/{id}/shares", s.portalAuth(func(w http.ResponseWriter, r *http.Request, se session) {
		hours, err := strconv.Atoi(r.FormValue("hours"))
		if err != nil || hours < 1 || hours > 168 {
			fail(w, 422, "Expiry must be 1–168 hours")
			return
		}
		_, token, err := s.newShare(r.PathValue("id"), hours)
		if result(w, err) {
			s.renderPerson(w, se, s.store.snapshot().People[r.PathValue("id")], "Internal link created.", "/shared/"+token)
		}
	}))
	for _, action := range []string{"revoke", "delete"} {
		s.mux.HandleFunc("POST /admin/shares/{id}/"+action, s.portalAuth(func(w http.ResponseWriter, r *http.Request, _ session) {
			sh, ok := s.store.snapshot().Shares[r.PathValue("id")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			if result(w, s.changeShare(sh.ID, action == "delete")) {
				http.Redirect(w, r, "/admin/people/"+sh.PersonID, 303)
			}
		}))
	}
	s.mux.HandleFunc("POST /admin/people/{id}/sign-in-code", s.portalAuth(func(w http.ResponseWriter, r *http.Request, se session) {
		person, exists := s.store.snapshot().People[r.PathValue("id")]
		if !exists {
			http.NotFound(w, r)
			return
		}
		code, err := s.issueAdminOTP(person.ID)
		if err != nil {
			s.renderPerson(w, se, person, "Could not generate a sign-in code.", "")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		s.renderPersonDetails(w, se, person, portalData{AdminOTPCode: code})
	}))
	s.mux.HandleFunc("POST /admin/people/{id}/verify-email", s.portalAuth(func(w http.ResponseWriter, r *http.Request, se session) {
		_, err := s.verification(r.PathValue("id"))
		if err != nil {
			fail(w, 422, "Add a valid email address first")
			return
		}
		http.Redirect(w, r, "/admin/outbox", 303)
	}))
	s.mux.HandleFunc("GET /admin/outbox", s.portalAuth(func(w http.ResponseWriter, r *http.Request, se session) {
		items := []Message{}
		for _, m := range s.store.snapshot().Outbox {
			items = append(items, m)
		}
		sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
		render(w, 200, portalData{Title: "Email outbox", View: "outbox", Outbox: items, CSRF: se.CSRF, EmailEnabled: s.cfg.SMTP.enabled()})
	}))
	s.mux.HandleFunc("POST /admin/outbox/{id}/delete", s.portalAuth(func(w http.ResponseWriter, r *http.Request, _ session) {
		if result(w, s.store.update(func(d *database) error {
			if _, ok := d.Outbox[r.PathValue("id")]; !ok {
				return errNotFound
			}
			delete(d.Outbox, r.PathValue("id"))
			event(d, "outbox.deleted", r.PathValue("id"))
			return nil
		})) {
			http.Redirect(w, r, "/admin/outbox", 303)
		}
	}))
	s.mux.HandleFunc("GET /admin/audit", s.portalAuth(func(w http.ResponseWriter, r *http.Request, se session) {
		render(w, 200, portalData{Title: "Audit events", View: "audit", Audit: s.store.snapshot().Audit, CSRF: se.CSRF})
	}))
	s.mux.HandleFunc("GET /shared/{token}", func(w http.ResponseWriter, r *http.Request) {
		data, ok := s.shared(r.PathValue("token"))
		if !ok {
			render(w, 404, portalData{Title: "Link unavailable", Message: "This link is invalid, expired, or revoked."})
			return
		}
		render(w, 200, portalData{Title: "Shared private record", View: "shared", Shared: data, Token: r.PathValue("token")})
	})
	s.mux.HandleFunc("GET /verify-email/{token}", func(w http.ResponseWriter, r *http.Request) {
		render(w, 200, portalData{Title: "Confirm your email", View: "verify", Token: r.PathValue("token")})
	})
	s.mux.HandleFunc("POST /verify-email/{token}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.verify(r.PathValue("token")); err != nil {
			render(w, 404, portalData{Title: "Verification unavailable", Message: "This link is invalid, expired, or already used."})
			return
		}
		render(w, 200, portalData{Title: "Email confirmed", Message: "Email ownership confirmed for this private service. Record details remain unverified."})
	})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		fail(w, 400, "Invalid form")
		return
	}
	c, err := r.Cookie("login_nonce")
	if err != nil || !equal(c.Value, r.FormValue("csrf")) {
		fail(w, 403, "Reload the login page and try again")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Now().Before(s.blockedUntil) {
		w.Header().Set("Retry-After", "60")
		render(w, 429, portalData{Title: "Welcome back", View: "login", CSRF: c.Value, Message: "Too many sign-in attempts. Please wait one minute before trying again."})
		return
	}
	identifier := strings.ToLower(strings.TrimSpace(r.FormValue("identifier")))
	validIdentifier := equal(identifier, strings.ToLower(s.cfg.AdminUsername))
	if s.cfg.AdminEmail != "" && equal(identifier, strings.ToLower(s.cfg.AdminEmail)) {
		validIdentifier = true
	}
	if !equal(r.FormValue("password"), s.cfg.AdminPassword) || !validIdentifier {
		s.failures++
		if s.failures >= 5 {
			s.blockedUntil = time.Now().Add(time.Minute)
			s.failures = 0
		}
		render(w, 401, portalData{Title: "Welcome back", View: "login", CSRF: c.Value, Message: "The username, email or password is incorrect. Please try again."})
		return
	}
	s.failures = 0
	for id, se := range s.sessions {
		if !time.Now().Before(se.Expires) {
			delete(s.sessions, id)
		}
	}
	if len(s.sessions) >= 128 {
		fail(w, 429, "Session limit reached")
		return
	}
	token := randomToken("")
	s.sessions[hash(token)] = session{CSRF: randomToken(""), Expires: time.Now().Add(12 * time.Hour)}
	http.SetCookie(w, &http.Cookie{Name: "private_records_session", Value: token, Path: "/admin", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.cfg.SecureCookies, MaxAge: 12 * 60 * 60})
	http.SetCookie(w, &http.Cookie{Name: "login_nonce", Value: "", Path: "/admin/login", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.cfg.SecureCookies, MaxAge: -1})
	http.Redirect(w, r, "/admin", 303)
}
func (s *Server) portalSave(w http.ResponseWriter, r *http.Request, se session) {
	input := map[string]string{}
	for _, f := range fields {
		input[f] = r.FormValue(f)
	}
	id := r.PathValue("id")
	person := Person{}
	if id != "" {
		var ok bool
		person, ok = s.store.snapshot().People[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
	}
	if err := apply(&person, input); err != nil {
		s.renderPerson(w, se, person, err.Error(), "", input)
		return
	}

	var photo *Photo
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		file, header, uploadErr := r.FormFile("photo")
		if uploadErr == nil {
			defer file.Close()
			raw, readErr := io.ReadAll(io.LimitReader(file, (5<<20)+1))
			if readErr != nil || header.Size > 5<<20 || len(raw) > 5<<20 {
				s.renderPerson(w, se, person, "Photo must be no larger than 5 MB. Select it again after correcting the error.", "", input)
				return
			}
			normalized, err := normalizePhoto(raw)
			if err != nil {
				s.renderPerson(w, se, person, err.Error()+". Select the photo again.", "", input)
				return
			}
			photo = &normalized
		} else if !errors.Is(uploadErr, http.ErrMissingFile) {
			s.renderPerson(w, se, person, "Could not read the photo. Select it again.", "", input)
			return
		}
	}
	person, err := s.savePersonWithPhoto(id, input, photo)
	if err != nil {
		s.renderPerson(w, se, person, "Could not save changes. Please try again.", "", input)
		return
	}
	http.Redirect(w, r, "/admin/people/"+person.ID+"?saved=1", 303)
}
func (s *Server) renderPerson(w http.ResponseWriter, se session, p Person, message, path string, draft ...map[string]string) {
	s.renderPersonDetails(w, se, p, portalData{Message: message, SharePath: path}, draft...)
}

func (s *Server) renderPersonDetails(w http.ResponseWriter, se session, p Person, data portalData, draft ...map[string]string) {
	labels := []string{"First name", "Last name", "Email", "Phone number", "Date of birth", "Nationality", "Passport number", "Recorded status (unverified)", "Accompanied by", "Valid from", "Valid until", "Entries allowed", "National Insurance number", "Share code (user supplied)"}
	raw, _ := json.Marshal(p)
	var values map[string]any
	_ = json.Unmarshal(raw, &values)
	recordedStatus := p.RecordedStatus
	if len(draft) > 0 {
		recordedStatus = draft[0]["recorded_status"]
	}
	f := []field{}
	for i, k := range fields {
		v, _ := values[k].(string)
		if len(draft) > 0 {
			v = draft[0][k]
		}
		kind := "text"
		textarea := false
		maxLength := 250
		placeholder := ""
		if k == "phone_number" {
			kind = "tel"
		}
		if k == "email" {
			kind = "email"
		}
		if k == "date_of_birth" || k == "valid_from" || k == "valid_until" {
			kind = "date"
		}
		if k == "entries_allowed" {
			kind = "text"
			placeholder = "For example, 2 or Multiple"
		}
		if k == "nationality" {
			maxLength = 4
			placeholder = "For example, IND"
		}
		if k == "national_insurance_number" {
			placeholder = "N/A if not applicable"
		}
		if k == "accompanied_by" {
			textarea = true
			maxLength = 2000
			placeholder = "Add any accompanying-person details"
		}
		f = append(f, field{Key: k, Label: labels[i], Value: v, Type: kind, Textarea: textarea, MaxLength: maxLength, Placeholder: placeholder})
	}
	shares := []Share{}
	for _, sh := range s.store.snapshot().Shares {
		if sh.PersonID == p.ID {
			shares = append(shares, sh)
		}
	}
	sort.Slice(shares, func(i, j int) bool { return shares[i].CreatedAt.Before(shares[j].CreatedAt) })
	title := "Add person"
	if p.ID != "" {
		title = fmt.Sprintf("Record: %s", p.FullName())
	}
	data.Title = title
	data.View = "person"
	data.Person = p
	data.AdminOTPEnabled = p.ID != ""
	data.HideEntriesAllowed = isSkilledWorkerStatus(recordedStatus)
	data.Fields = f
	data.Shares = shares
	data.CSRF = se.CSRF
	render(w, 200, data)

}
