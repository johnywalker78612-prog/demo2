package app

import (
	"bytes"
	"html/template"
	"net/http"
	"private-records/web"
	"regexp"
	"strings"
	"time"
)

type demoSession struct {
	Passport, PersonID, Challenge, LookupNonce string
	Verified                                   bool
	CodeExpires                                time.Time
	CodeAttempts                               int
	CodePhone                                  string
	Expires                                    time.Time
}

func (s *Server) demoFlow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	c, _ := r.Cookie("demo_session")
	key := ""
	if c != nil {
		key = hash(c.Value)
	}
	s.demoMu.Lock()
	defer s.demoMu.Unlock()
	se, ok := s.demoSessions[key]
	if !ok || !time.Now().Before(se.Expires) {
		if r.URL.Path != "/gov/passport" {
			http.Redirect(w, r, "/gov/passport", 303)
			return
		}
		t := randomToken("")
		key = hash(t)
		se = demoSession{Expires: time.Now().Add(20 * time.Minute)}
		s.demoSessions[key] = se
		http.SetCookie(w, &http.Cookie{Name: "demo_session", Value: t, Path: "/gov", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.cfg.SecureCookies, MaxAge: 1200})
	}
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
	}
	demoError := ""
	displayPhone := ""
	switch r.URL.Path {
	case "/gov/status/pdf", "/gov/share-preview/pdf", "/gov/share-result/pdf":
		if !se.Verified || se.PersonID == "" {
			http.Redirect(w, r, "/gov/passport", http.StatusSeeOther)
			return
		}
		p, exists := s.store.snapshot().People[se.PersonID]
		if !exists {
			http.Redirect(w, r, "/gov/passport", http.StatusSeeOther)
			return
		}
		var pdf []byte
		var err error
		filename := "status.pdf"
		if r.URL.Path == "/gov/share-result/pdf" {
			pdf, err = demoSharePDF(p)
			filename = "share-code.pdf"
		} else {
			var photo *Photo
			if stored, hasPhoto := s.store.snapshot().Photos[p.ID]; hasPhoto {
				photo = &stored
			}
			pdf, err = demoStatusPDF(p, photo)
		}
		if err != nil {
			http.Error(w, "Could not create PDF", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(pdf)
		return
	case "/gov/passport":
		if r.Method == http.MethodPost {
			se.Passport = strings.TrimSpace(r.FormValue("documentNumber"))
			se.PersonID = ""
			se.Challenge = ""
			se.Verified = false
			s.demoSessions[key] = se
			http.Redirect(w, r, "/gov/date-of-birth", 303)
			return
		}
	case "/gov/date-of-birth":
		if r.Method == http.MethodPost {
			id := strings.TrimSpace(r.FormValue("documentNumber"))
			if id == "" {
				id = se.Passport
			}
			se.PersonID = ""
			se.Challenge = ""
			se.Verified = false
			dob := strings.TrimSpace(r.FormValue("date_of_birth"))
			for pid, p := range s.store.snapshot().People {
				if p.DateOfBirth == dob && (strings.EqualFold(p.PassportNumber, id) || p.ID == id) {
					se.PersonID = pid
					se.LookupNonce = randomToken("")
					break
				}
			}
			s.demoSessions[key] = se
			if se.PersonID == "" {
				renderDemoIdentityMismatch(w, id, dob)
				return
			}
			http.Redirect(w, r, "/gov/security-code-delivery", 303)
			return
		}
	case "/gov/security-code-delivery":
		p, exists := s.store.snapshot().People[se.PersonID]
		if !exists {
			http.Redirect(w, r, "/gov/passport", http.StatusSeeOther)
			return
		}
		phone := smsPhone(p.PhoneNumber)
		if phone == "" {
			http.Error(w, "This record needs a linked mobile number in international format.", http.StatusUnprocessableEntity)
			return
		}
		displayPhone = phone
		if r.Method == http.MethodPost {
			if choice := r.PostFormValue("delivery"); choice != "email" && choice != "sms" {
				http.Error(w, "Choose how to request a security code.", http.StatusBadRequest)
				return
			}
			now := time.Now()
			if now.Before(s.demoSMSNext[p.ID]) {
				w.Header().Set("Retry-After", "60")
				http.Error(w, "Please wait a minute before requesting another security code.", http.StatusTooManyRequests)
				return
			}
			if s.demoOTPSend == nil {
				http.Error(w, "Text message delivery is temporarily unavailable.", http.StatusServiceUnavailable)
				return
			}
			s.demoSMSNext[p.ID] = now.Add(time.Minute)
			s.demoMu.Unlock()
			nextSession, err := sendDemoOTP(r.Context(), s.demoOTPSend, se, key, phone)
			s.demoMu.Lock()
			if err != nil {
				http.Error(w, "The security code could not be sent. Please try again later.", http.StatusBadGateway)
				return
			}
			if s.demoSessions[key] != se {
				http.Redirect(w, r, "/gov/passport", http.StatusSeeOther)
				return
			}
			se = nextSession
			s.demoSessions[key] = se
			http.Redirect(w, r, "/gov/security-code", http.StatusSeeOther)
			return
		}
	case "/gov/security-code":
		if se.Verified {
			http.Redirect(w, r, "/gov/status", http.StatusSeeOther)
			return
		}
		p, exists := s.store.snapshot().People[se.PersonID]
		if !exists || se.CodePhone == "" || se.CodePhone != smsPhone(p.PhoneNumber) {
			http.Redirect(w, r, "/gov/passport", http.StatusSeeOther)
			return
		}
		if se.Challenge == "" || !time.Now().Before(se.CodeExpires) {
			http.Redirect(w, r, "/gov/security-code-delivery", http.StatusSeeOther)
			return
		}
		displayPhone = se.CodePhone
		if r.Method == http.MethodPost {
			if !se.verifyDemoOTP() {
				if se.CodeAttempts >= 5 {
					demoError = "Too many incorrect attempts. Use the resend button to request a new code."
				} else {
					demoError = "Invalid code entered, please check your code and enter it again."
				}
				s.demoSessions[key] = se
				break
			}
			s.demoSessions[key] = se
			http.Redirect(w, r, "/gov/status", http.StatusSeeOther)
			return
		}
	case "/gov/photo":
		if !se.Verified {
			http.NotFound(w, r)
			return
		}
		r.SetPathValue("id", se.PersonID)
		s.getPhoto(w, r)
		return
	}
	content, _ := web.Files.ReadFile("pages/demo/" + strings.TrimPrefix(r.URL.Path, "/gov/") + ".html")
	// The original static preview forbids all network requests. This flow now
	// posts the existing form through the same-origin endpoint, so relax only
	// those two CSP directives in the response; no visual markup is changed.
	content = []byte(strings.ReplaceAll(string(content), "connect-src 'none'", "connect-src 'self'"))
	content = []byte(strings.ReplaceAll(string(content), "form-action 'none'", "form-action 'self'"))
	if se.Verified && se.PersonID != "" {
		p, exists := s.store.snapshot().People[se.PersonID]
		if exists {
			content = populateDemo(content, p)
			if r.URL.Path == "/gov/status" || r.URL.Path == "/gov/share-preview" || r.URL.Path == "/gov/share-result" {
				pdfPath := "/gov/status/pdf"
				if r.URL.Path == "/gov/share-result" {
					pdfPath = "/gov/share-result/pdf"
				}
				content = wireDemoPDFLinks(content, pdfPath)
			}
		}
	}
	if displayPhone != "" {
		masked := "ending in " + displayPhone[len(displayPhone)-4:]
		content = bytes.ReplaceAll(content, []byte("LINKED_PHONE_SUFFIX"), []byte(masked))
	}
	if demoError != "" && r.URL.Path == "/gov/security-code" {
		content = addDemoCodeError(content, demoError)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Keep the original demo page styling and scripts available. The global
	// server policy is intentionally stricter for API responses, but these
	// pages already depend on the bundled GOV.UK assets and their existing JS.
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; form-action 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'")
	w.WriteHeader(200)
	if r.Method != http.MethodHead {
		_, _ = w.Write(content)
	}
}

func addDemoCodeError(content []byte, message string) []byte {
	errorHTML := `<div class="osmuk-error-summary" role="alert" tabindex="-1" style="border:5px solid #d4351c;padding:20px;margin:0 0 30px;color:#d4351c"><h2 class="osmuk-heading-m" style="margin:0 0 10px">There is a problem</h2><p style="margin:0"><a href="#verificationCode" style="color:#d4351c">` + template.HTMLEscapeString(message) + `</a></p></div>`
	marker := `<div id="kc-content-wrapper" class="">`
	return []byte(strings.Replace(string(content), marker, marker+errorHTML, 1))
}

func populateDemo(content []byte, p Person) []byte {
	name := template.HTMLEscapeString(p.FullName())
	content = []byte(strings.ReplaceAll(string(content), "Example Person", name))
	content = replaceDemoSummary(content, "Nationality", p.Nationality)
	content = replaceDemoSummary(content, "Status", p.RecordedStatus)
	content = replaceDemoSummary(content, "Accompanied by", p.AccompaniedBy)
	content = replaceDemoSummary(content, "Valid from", formatDemoDate(p.ValidFrom))
	content = replaceDemoSummary(content, "Valid until", formatDemoDate(p.ValidUntil))
	content = replaceDemoSummary(content, "Number of entries allowed", p.EntriesAllowed)
	if p.ShareCode != "" {
		content = []byte(strings.ReplaceAll(string(content), "DEMO-NOT-VALID", template.HTMLEscapeString(p.ShareCode)))
	}
	if p.DateOfBirth != "" {
		content = replaceDemoSummary(content, "Date of birth", formatDemoDate(p.DateOfBirth))
	}
	if p.HasPhoto {
		content = []byte(strings.ReplaceAll(string(content), "/assets/images/demo-portrait.svg", "/gov/photo"))
	}
	return content
}

func wireDemoPDFLinks(content []byte, pdfPath string) []byte {
	s := string(content)
	s = regexp.MustCompile(`(?s)(<a[^>]*aria-label="Print this page"[^>]*href=")[^"]*(")`).ReplaceAllString(s, `${1}#print-page${2}`)
	s = strings.Replace(s, `aria-label="Print this page"`, `aria-label="Print this page" onclick="window.print(); return false;"`, 1)
	s = regexp.MustCompile(`(?s)(<a[^>]*aria-label="Print this page"[^>]*) data-local-unavailable="link"`).ReplaceAllString(s, `${1}`)
	s = regexp.MustCompile(`(?s)(<a[^>]*aria-label="Download PDF"[^>]*href=")[^"]*(")`).ReplaceAllString(s, `${1}`+pdfPath+`${2}`)
	s = regexp.MustCompile(`(?s)(<a[^>]*aria-label="Download PDF"[^>]*) data-local-unavailable="link"`).ReplaceAllString(s, `${1}`)
	if !strings.Contains(s, "aria-label=\"Download PDF\"") {
		marker := "</main>"
		s = strings.Replace(s, marker, `<p class="osmuk-body"><a class="osmuk-link" aria-label="Print this page" href="#print-page" onclick="window.print(); return false;">Print this page</a> &nbsp; <a class="osmuk-link" aria-label="Download PDF" href="`+pdfPath+`">Download PDF</a></p>`+marker, 1)
	}
	return []byte(s)
}

func formatDemoDate(value string) string {
	d, err := time.Parse("2006-01-02", value)
	if err != nil {
		return ""
	}
	return d.Format("02 January 2006")
}

func replaceDemoSummary(content []byte, label, value string) []byte {
	if value == "" {
		return content
	}
	pattern := regexp.MustCompile(`(?s)(<dt[^>]*>\s*` + regexp.QuoteMeta(label) + `\s*</dt>\s*<dd[^>]*>).*?(</dd>)`)
	return pattern.ReplaceAll(content, []byte(`${1}`+template.HTMLEscapeString(value)+`${2}`))
}

// Render only the submitted values, never details from another stored record.
func renderDemoIdentityMismatch(w http.ResponseWriter, documentNumber, dob string) {
	content, err := web.Files.ReadFile("pages/demo/identity-mismatch.html")
	if err != nil {
		http.Error(w, "Unable to display the error page", http.StatusInternalServerError)
		return
	}
	page, err := template.New("identity-mismatch").Parse(string(content))
	var body bytes.Buffer
	if err == nil {
		displayDOB := formatDemoDate(dob)
		if displayDOB == "" {
			displayDOB = dob
		}
		err = page.Execute(&body, struct{ DocumentNumber, DateOfBirth string }{documentNumber, displayDOB})
	}
	if err != nil {
		http.Error(w, "Unable to display the error page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; form-action 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_, _ = w.Write(body.Bytes())
}
