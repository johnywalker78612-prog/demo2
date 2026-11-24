package app

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"
)

var fields = []string{"first_name", "last_name", "email", "phone_number", "date_of_birth", "nationality", "passport_number", "recorded_status", "accompanied_by", "valid_from", "valid_until", "entries_allowed", "national_insurance_number", "share_code"}

var shareCodeInput = regexp.MustCompile(`^(?:[A-Za-z0-9]{9}|[A-Za-z0-9]{3}-[A-Za-z0-9]{3}-[A-Za-z0-9]{3})$`)

func apply(p *Person, input map[string]string) error {
	for key, v := range input {
		v = strings.TrimSpace(v)
		limit := 250
		if key == "accompanied_by" {
			limit = 2000
		}
		if len(v) > limit {
			return fmt.Errorf("Each field must be at most %d bytes", limit)
		}
		switch key {
		case "share_code":
			if v == "" || v == p.ShareCode {
				p.ShareCode = v
				continue
			}
			if !shareCodeInput.MatchString(v) {
				return errors.New("Enter a share code with nine letters or digits, for example GHY-76H-67A")
			}
			code := strings.ToUpper(strings.ReplaceAll(v, "-", ""))
			p.ShareCode = code[:3] + "-" + code[3:6] + "-" + code[6:]
		case "first_name":
			p.FirstName = v
		case "last_name":
			p.LastName = v
		case "email":
			if p.Email != v {
				p.EmailVerifiedAt = nil
			}
			p.Email = v
		case "phone_number":
			p.PhoneNumber = v
		case "date_of_birth":
			p.DateOfBirth = v
		case "nationality":
			p.Nationality = v
		case "passport_number":
			p.PassportNumber = v
		case "recorded_status":
			p.RecordedStatus = v
		case "accompanied_by":
			p.AccompaniedBy = v
		case "valid_from":
			p.ValidFrom = v
		case "valid_until":
			p.ValidUntil = v
		case "entries_allowed":
			p.EntriesAllowed = v
		case "national_insurance_number":
			p.NationalInsuranceNumber = v
		default:
			return errors.New("Unknown or read-only field: " + key)
		}
	}
	if p.FirstName == "" {
		return errors.New("First name is required")
	}
	if p.Email != "" {
		a, err := mail.ParseAddress(p.Email)
		if err != nil || a.Address != p.Email {
			return errors.New("Email must be a valid address without a display name")
		}
	}
	if p.PhoneNumber != "" {
		digits := phoneDigits(p.PhoneNumber)
		if len(digits) < 7 || len(digits) > 15 || strings.Trim(p.PhoneNumber, "+0123456789 ()-.") != "" {
			return errors.New("Phone number must contain 7–15 digits; use +, spaces, parentheses, dots or dashes for formatting")
		}
	}
	if p.Nationality != "" {
		p.Nationality = strings.ToUpper(p.Nationality)
		if len(p.Nationality) < 3 || len(p.Nationality) > 4 || strings.Trim(p.Nationality, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
			return errors.New("Nationality must be a 3 or 4 letter code, such as IND or USA")
		}
	}
	if p.EntriesAllowed != "" {
		if strings.EqualFold(p.EntriesAllowed, "Multiple") {
			p.EntriesAllowed = "Multiple"
		} else if strings.Trim(p.EntriesAllowed, "0123456789") != "" {
			return errors.New("Entries allowed must contain digits only or Multiple")
		}
	}
	if p.NationalInsuranceNumber != "" && !validInsuranceNumber(p.NationalInsuranceNumber) {
		return errors.New("National Insurance number must use letters, numbers, spaces, / or -, or be N/A")
	}
	for _, v := range []string{p.DateOfBirth, p.ValidFrom, p.ValidUntil} {
		if v != "" {
			if _, err := time.Parse("2006-01-02", v); err != nil {
				return errors.New("Dates must be real calendar dates in YYYY-MM-DD format")
			}
		}
	}
	if p.DateOfBirth > time.Now().UTC().Format("2006-01-02") {
		return errors.New("Date of birth cannot be in the future")
	}
	if p.ValidFrom != "" && p.ValidUntil != "" && p.ValidUntil < p.ValidFrom {
		return errors.New("Valid until must not precede valid from")
	}
	p.Unverified = true
	p.UpdatedAt = time.Now().UTC()
	return nil
}

func validInsuranceNumber(v string) bool {
	if strings.EqualFold(strings.TrimSpace(v), "n/a") {
		return true
	}
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == ' ' || r == '/' || r == '-' {
			continue
		}
		return false
	}
	return true
}
func (s *Server) savePerson(id string, input map[string]string) (Person, error) {
	return s.savePersonWithPhoto(id, input, nil)
}

// savePersonWithPhoto commits the record and optional photo in one store transaction.
func (s *Server) savePersonWithPhoto(id string, input map[string]string, photo *Photo) (Person, error) {
	var person Person
	err := s.store.update(func(d *database) error {
		if id == "" {
			person = Person{ID: randomToken("person_"), CreatedAt: time.Now().UTC()}
		} else {
			var ok bool
			person, ok = d.People[id]
			if !ok {
				return errNotFound
			}
		}
		oldEmail := person.Email
		if err := apply(&person, input); err != nil {
			return err
		}
		if oldEmail != person.Email {
			for h, v := range d.Verifications {
				if v.PersonID == person.ID {
					delete(d.Verifications, h)
				}
			}
		}
		if photo != nil {
			d.Photos[person.ID] = *photo
			person.HasPhoto = true
			event(d, "photo.updated", person.ID)
		}
		d.People[person.ID] = person
		action := "person.updated"
		if id == "" {
			action = "person.created"
		}
		event(d, action, person.ID)
		return nil
	})
	return person, err
}
func (s *Server) listPeople(w http.ResponseWriter, r *http.Request) {
	list := queryPeople(s.store.snapshot(), r.URL.Query())
	send(w, 200, map[string]any{"items": list.Items, "total": list.Total, "page": list.Page, "page_size": list.PageSize, "disclaimer": Disclaimer})
}
func (s *Server) createPerson(w http.ResponseWriter, r *http.Request) {
	var input map[string]string
	if !decode(w, r, &input) {
		return
	}
	probe := Person{}
	if err := apply(&probe, input); err != nil {
		fail(w, 422, err.Error())
		return
	}
	p, err := s.savePerson("", input)
	if result(w, err) {
		w.Header().Set("Location", "/api/v1/people/"+p.ID)
		send(w, 201, p)
	}
}

// lookupPerson requires administrator authorization; matching a birth date is
// a record filter, not authentication. Keep personal lookup values out of URLs.
func (s *Server) lookupPerson(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID          string `json:"id"`
		DateOfBirth string `json:"date_of_birth"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.ID = strings.TrimSpace(input.ID)
	input.DateOfBirth = strings.TrimSpace(input.DateOfBirth)
	date, err := time.Parse("2006-01-02", input.DateOfBirth)
	if input.ID == "" || len(input.ID) > 250 || err != nil || date.Format("2006-01-02") != input.DateOfBirth || input.DateOfBirth > time.Now().UTC().Format("2006-01-02") {
		fail(w, 422, "Provide a record id and a valid date_of_birth in YYYY-MM-DD format")
		return
	}
	person, ok := s.store.snapshot().People[input.ID]
	if !ok || person.DateOfBirth != input.DateOfBirth {
		fail(w, 404, "Record not found")
		return
	}
	send(w, 200, person)
}

func (s *Server) getPerson(w http.ResponseWriter, r *http.Request) {
	p, ok := s.store.snapshot().People[r.PathValue("id")]
	if !ok {
		fail(w, 404, "Record not found")
		return
	}
	send(w, 200, p)
}
func (s *Server) updatePerson(w http.ResponseWriter, r *http.Request) {
	var input map[string]string
	if !decode(w, r, &input) {
		return
	}
	probe, ok := s.store.snapshot().People[r.PathValue("id")]
	if !ok {
		fail(w, 404, "Record not found")
		return
	}
	if err := apply(&probe, input); err != nil {
		fail(w, 422, err.Error())
		return
	}
	p, err := s.savePerson(r.PathValue("id"), input)
	if result(w, err) {
		send(w, 200, p)
	}
}
func (s *Server) removePerson(id string) error {
	return s.store.update(func(d *database) error {
		if _, ok := d.People[id]; !ok {
			return errNotFound
		}
		delete(d.People, id)
		delete(d.AdminOTPs, id)
		delete(d.Photos, id)
		for sid, sh := range d.Shares {
			if sh.PersonID == id {
				delete(d.Shares, sid)
				for h, v := range d.ShareHashes {
					if v == sid {
						delete(d.ShareHashes, h)
					}
				}
			}
		}
		for h, v := range d.Verifications {
			if v.PersonID == id {
				delete(d.Verifications, h)
			}
		}
		// Outbox recipients and tokens belong to the deleted person too.
		for mid, m := range d.Outbox {
			if m.PersonID == id {
				delete(d.Outbox, mid)
			}
		}
		event(d, "person.deleted", id)
		return nil
	})
}
func (s *Server) deletePerson(w http.ResponseWriter, r *http.Request) {
	if result(w, s.removePerson(r.PathValue("id"))) {
		w.WriteHeader(204)
	}
}
func normalizePhoto(raw []byte) (Photo, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "jpeg" && format != "png") {
		return Photo{}, errors.New("Upload a JPEG or PNG photo")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 4000 || cfg.Height > 4000 {
		return Photo{}, errors.New("Photo dimensions must not exceed 4000 × 4000")
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return Photo{}, errors.New("Invalid photo")
	}
	var b bytes.Buffer
	ct := "image/jpeg"
	if format == "png" {
		err = png.Encode(&b, img)
		ct = "image/png"
	} else {
		err = jpeg.Encode(&b, img, &jpeg.Options{Quality: 90})
	}
	if err != nil {
		return Photo{}, err
	}
	if b.Len() > 5<<20 {
		return Photo{}, errors.New("Photo exceeds the 5 MB limit")
	}
	return Photo{Data: b.Bytes(), ContentType: ct}, nil
}
func (s *Server) savePhoto(id string, photo Photo) error {
	return s.store.update(func(d *database) error {
		p, ok := d.People[id]
		if !ok {
			return errNotFound
		}
		d.Photos[id] = photo
		p.HasPhoto = true
		p.UpdatedAt = time.Now().UTC()
		d.People[id] = p
		event(d, "photo.updated", id)
		return nil
	})
}
func (s *Server) putPhoto(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		fail(w, 413, "Photo exceeds the 5 MB limit")
		return
	}
	photo, err := normalizePhoto(raw)
	if err != nil {
		fail(w, 422, err.Error())
		return
	}
	if result(w, s.savePhoto(r.PathValue("id"), photo)) {
		w.WriteHeader(204)
	}
}
func (s *Server) getPhoto(w http.ResponseWriter, r *http.Request) {
	photo, ok := s.store.snapshot().Photos[r.PathValue("id")]
	if !ok {
		fail(w, 404, "Photo not found")
		return
	}
	w.Header().Set("Content-Type", photo.ContentType)
	w.Header().Set("Content-Disposition", "inline")
	w.Write(photo.Data)
}
func (s *Server) removePhoto(id string) error {
	return s.store.update(func(d *database) error {
		p, ok := d.People[id]
		if !ok {
			return errNotFound
		}
		delete(d.Photos, id)
		p.HasPhoto = false
		p.UpdatedAt = time.Now().UTC()
		d.People[id] = p
		event(d, "photo.deleted", id)
		return nil
	})
}
func (s *Server) deletePhoto(w http.ResponseWriter, r *http.Request) {
	if result(w, s.removePhoto(r.PathValue("id"))) {
		w.WriteHeader(204)
	}
}
