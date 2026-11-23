package app

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/mail"
	"sort"
	"strings"
	"time"
)

func (s *Server) newShare(personID string, hours int) (Share, string, error) {
	if hours == 0 {
		hours = 24
	}
	if hours < 1 || hours > 168 {
		return Share{}, "", errors.New("Expiry must be between 1 and 168 hours")
	}
	token := randomToken("internal_")
	sh := Share{ID: randomToken("share_"), PersonID: personID, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Duration(hours) * time.Hour)}
	err := s.store.update(func(d *database) error {
		if _, ok := d.People[personID]; !ok {
			return errNotFound
		}
		d.Shares[sh.ID] = sh
		d.ShareHashes[hash(token)] = sh.ID
		event(d, "share.created", sh.ID)
		return nil
	})
	return sh, token, err
}
func (s *Server) createShare(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ExpiresInHours int `json:"expires_in_hours"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.ExpiresInHours < 0 || input.ExpiresInHours > 168 {
		fail(w, 422, "Expiry must be between 1 and 168 hours")
		return
	}
	sh, token, err := s.newShare(r.PathValue("id"), input.ExpiresInHours)
	if result(w, err) {
		send(w, 201, map[string]any{"share": sh, "token": token, "path": "/shared/" + token, "api_path": "/api/v1/shared/" + token, "disclaimer": Disclaimer})
	}
}
func (s *Server) listShares(w http.ResponseWriter, r *http.Request) {
	d := s.store.snapshot()
	if _, ok := d.People[r.PathValue("id")]; !ok {
		fail(w, 404, "Record not found")
		return
	}
	shares := []Share{}
	for _, sh := range d.Shares {
		if sh.PersonID == r.PathValue("id") {
			shares = append(shares, sh)
		}
	}
	sort.Slice(shares, func(i, j int) bool { return shares[i].CreatedAt.Before(shares[j].CreatedAt) })
	send(w, 200, map[string]any{"items": shares})
}
func (s *Server) getShare(w http.ResponseWriter, r *http.Request) {
	sh, ok := s.store.snapshot().Shares[r.PathValue("id")]
	if !ok {
		fail(w, 404, "Share not found")
		return
	}
	send(w, 200, sh)
}
func (s *Server) changeShare(id string, remove bool) error {
	return s.store.update(func(d *database) error {
		sh, ok := d.Shares[id]
		if !ok {
			return errNotFound
		}
		action := "share.revoked"
		if remove {
			delete(d.Shares, id)
			action = "share.deleted"
		} else {
			now := time.Now().UTC()
			sh.RevokedAt = &now
			d.Shares[id] = sh
		}
		for h, sid := range d.ShareHashes {
			if sid == id {
				delete(d.ShareHashes, h)
			}
		}
		event(d, action, id)
		return nil
	})
}
func (s *Server) revokeShare(w http.ResponseWriter, r *http.Request) {
	if result(w, s.changeShare(r.PathValue("id"), false)) {
		w.WriteHeader(204)
	}
}
func (s *Server) deleteShare(w http.ResponseWriter, r *http.Request) {
	if result(w, s.changeShare(r.PathValue("id"), true)) {
		w.WriteHeader(204)
	}
}
func (s *Server) shared(token string) (map[string]any, bool) {
	d := s.store.snapshot()
	id, ok := d.ShareHashes[hash(token)]
	if !ok {
		return nil, false
	}
	sh, ok := d.Shares[id]
	if !ok || sh.RevokedAt != nil || !time.Now().Before(sh.ExpiresAt) {
		return nil, false
	}
	p, ok := d.People[sh.PersonID]
	if !ok {
		return nil, false
	}
	return map[string]any{"first_name": p.FirstName, "last_name": p.LastName, "nationality": p.Nationality, "recorded_status": p.RecordedStatus, "valid_from": p.ValidFrom, "valid_until": p.ValidUntil, "unverified": true, "expires_at": sh.ExpiresAt, "disclaimer": Disclaimer}, true
}
func (s *Server) resolveShare(w http.ResponseWriter, r *http.Request) {
	data, ok := s.shared(r.PathValue("token"))
	if !ok {
		fail(w, 404, "Link is invalid, expired, or revoked")
		return
	}
	send(w, 200, data)
}
func (s *Server) verification(id string, preserveExisting ...bool) (string, error) {
	token := randomToken("verify_")
	number, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	code := fmt.Sprintf("%06d", number.Int64())
	challenge := randomToken("challenge_")
	var mid string
	err = s.store.update(func(d *database) error {
		p, ok := d.People[id]
		if !ok {
			return errNotFound
		}
		if p.Email == "" {
			return errors.New("Add an email address first")
		}
		if len(preserveExisting) == 0 || !preserveExisting[0] {
			for h, v := range d.Verifications {
				if v.PersonID == id {
					delete(d.Verifications, h)
				}
			}
		}
		expiry := time.Now().UTC().Add(10 * time.Minute)
		d.Verifications[hash(token)] = Verification{ID: challenge, CodeHash: hash(challenge + ":" + code), PersonID: id, Email: p.Email, ExpiresAt: expiry}
		mid = randomToken("message_")
		subject := "Private Records: your email verification code"
		body, html, err := renderEmail(emailData{LogoURL: s.cfg.EmailLogoURL, Subject: subject, Heading: "Verify your email", Name: p.FullName(), Intro: "You requested email verification for your Private Records account.", PassportEnding: passportEnding(p.PassportNumber), Code: code, Explanation: "Enter this code on the Private Records verification page. This only confirms access to your inbox.", URL: s.mailURL("/verify-code/" + challenge), Action: "Enter verification code", Closing: "If you did not request this, ignore this message. Your record remains unverified."})
		if err != nil {
			return err
		}
		body += "\nAlternative single-use confirmation link: " + s.mailURL("/verify-email/"+token)
		d.Outbox[mid] = Message{PersonID: id, ID: mid, To: p.Email, Subject: subject, Body: body, HTML: html, ChallengeID: challenge, ExpiresAt: expiry, CreatedAt: time.Now().UTC()}
		event(d, "email.verification_requested", id)
		return nil
	})
	return mid, err
}
func (s *Server) requestVerification(w http.ResponseWriter, r *http.Request) {
	p, ok := s.store.snapshot().People[r.PathValue("id")]
	if !ok {
		fail(w, 404, "Record not found")
		return
	}
	if p.Email == "" {
		fail(w, 422, "Add an email address first")
		return
	}
	id, err := s.verification(p.ID)
	if result(w, err) {
		send(w, 202, map[string]any{"outbox_message_id": id, "challenge_id": s.store.snapshot().Outbox[id].ChallengeID, "delivery": "development_outbox", "email_sent": false})
	}
}
func (s *Server) verify(token string) error {
	return s.store.update(func(d *database) error {
		h := hash(token)
		v, ok := d.Verifications[h]
		if !ok || !time.Now().Before(v.ExpiresAt) {
			return errNotFound
		}
		p, ok := d.People[v.PersonID]
		if !ok || p.Email != v.Email {
			return errNotFound
		}
		now := time.Now().UTC()
		p.EmailVerifiedAt = &now
		p.UpdatedAt = now
		d.People[p.ID] = p
		delete(d.Verifications, h)
		event(d, "email.verified", p.ID)
		return nil
	})
}
func (s *Server) confirmVerification(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token       string `json:"token"`
		ChallengeID string `json:"challenge_id"`
		Code        string `json:"code"`
	}
	if !decode(w, r, &input) {
		return
	}
	var err error
	if input.Token != "" {
		err = s.verify(input.Token)
	} else {
		err = s.verifyCode(input.ChallengeID, input.Code)
	}
	if result(w, err) {
		send(w, 200, map[string]bool{"email_verified": true})
	}
}
func (s *Server) listOutbox(w http.ResponseWriter, r *http.Request) {
	items := []Message{}
	for _, m := range s.store.snapshot().Outbox {
		items = append(items, m)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	send(w, 200, map[string]any{"delivery": "development_outbox", "items": items})
}
func (s *Server) deleteOutbox(w http.ResponseWriter, r *http.Request) {
	if result(w, s.store.update(func(d *database) error {
		if _, ok := d.Outbox[r.PathValue("id")]; !ok {
			return errNotFound
		}
		delete(d.Outbox, r.PathValue("id"))
		event(d, "outbox.deleted", r.PathValue("id"))
		return nil
	})) {
		w.WriteHeader(204)
	}
}
func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	send(w, 200, map[string]any{"items": s.store.snapshot().Audit})
}

// Create a development message for an existing active internal link. Its plaintext
// token must be supplied because only a hash was retained at creation time.
var errRecipient = errors.New("Provide a valid recipient email")

func (s *Server) shareMessage(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token     string `json:"token"`
		Recipient string `json:"recipient"`
	}
	if !decode(w, r, &input) {
		return
	}
	mid, err := s.createShareMessage(r.PathValue("id"), input.Token, input.Recipient)
	if errors.Is(err, errRecipient) {
		fail(w, 422, err.Error())
		return
	}
	if result(w, err) {
		send(w, 202, map[string]any{"outbox_message_id": mid, "delivery": "development_outbox", "email_sent": false})
	}
}
func (s *Server) createShareMessage(shareID, token, recipient string) (string, error) {
	address, err := mail.ParseAddress(recipient)
	if err != nil || address.Address != recipient || len(recipient) > 250 {
		return "", errRecipient
	}
	mid := randomToken("message_")
	err = s.store.update(func(d *database) error {
		id, ok := d.ShareHashes[hash(token)]
		sh, exists := d.Shares[id]
		if !ok || !exists || id != shareID || sh.RevokedAt != nil || !time.Now().Before(sh.ExpiresAt) {
			return errNotFound
		}
		p := d.People[sh.PersonID]
		subject := "Private Records: a private record was shared with you"
		body, html, err := renderEmail(emailData{LogoURL: s.cfg.EmailLogoURL, Subject: subject, Heading: "View a shared private record", Name: "recipient", Intro: p.FullName() + " has a private record shared with you.", Explanation: "This link expires at " + sh.ExpiresAt.Format(time.RFC1123) + ". Anyone with the link can view the limited shared information until it expires or is revoked.", URL: s.mailURL("/shared/" + token), Action: "View private record", Closing: "Treat this link as confidential. The record is user supplied and does not confirm immigration status."})
		if err != nil {
			return err
		}
		d.Outbox[mid] = Message{ID: mid, PersonID: sh.PersonID, To: recipient, Subject: subject, Body: body, HTML: html, ShareID: sh.ID, ExpiresAt: sh.ExpiresAt, CreatedAt: time.Now().UTC()}
		event(d, "share.message_created", id)
		return nil
	})
	return mid, err
}

// Failed attempts are committed before returning the generic rejection.
func (s *Server) verifyCode(id, code string) error {
	if !strings.HasPrefix(id, "challenge_") || len(code) != 6 {
		return errNotFound
	}
	accepted := false
	err := s.store.update(func(d *database) error {
		for key, v := range d.Verifications {
			if v.ID != id {
				continue
			}
			if !time.Now().Before(v.ExpiresAt) || v.Attempts >= 5 {
				delete(d.Verifications, key)
				return nil
			}
			if !equal(v.CodeHash, hash(id+":"+code)) {
				v.Attempts++
				if v.Attempts >= 5 {
					delete(d.Verifications, key)
				} else {
					d.Verifications[key] = v
				}
				return nil
			}
			p, ok := d.People[v.PersonID]
			if !ok || p.Email != v.Email {
				delete(d.Verifications, key)
				return nil
			}
			now := time.Now().UTC()
			p.EmailVerifiedAt = &now
			p.UpdatedAt = now
			d.People[p.ID] = p
			delete(d.Verifications, key)
			event(d, "email.verified", p.ID)
			accepted = true
			return nil
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !accepted {
		return errNotFound
	}
	return nil
}
