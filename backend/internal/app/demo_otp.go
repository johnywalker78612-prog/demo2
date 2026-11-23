package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"html/template"
	"math/big"
	"net/mail"
	"regexp"
	"strings"
	"time"
)

var ukNationalMobile = regexp.MustCompile(`^07[1-57-9][0-9]{8}$`)

var internationalSMSPhone = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

func smsPhone(value string) string {
	phone := strings.NewReplacer(" ", "", "(", "", ")", "", "-", "", ".", "").Replace(strings.TrimSpace(value))
	if strings.HasPrefix(phone, "00") {
		phone = "+" + phone[2:]
	}
	if ukNationalMobile.MatchString(phone) {
		phone = "+44" + phone[1:]
	}
	if strings.HasPrefix(phone, "+4470") || !internationalSMSPhone.MatchString(phone) {
		return ""
	}
	return phone
}

func newDemoOTP() (string, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", value.Int64()), nil
}

// sendDemoOTP issues a challenge only after the supplied sender accepts the code.
func sendDemoOTP(ctx context.Context, send func(context.Context, string, string) error, session demoSession, key, recipient string) (demoSession, error) {
	code, err := newDemoOTP()
	if err != nil {
		return session, err
	}
	if err := send(ctx, recipient, code); err != nil {
		return session, err
	}
	session.Challenge = hash(key + code)
	session.CodeExpires = time.Now().Add(10 * time.Minute)
	session.CodeAttempts = 0
	session.CodeRecipient = recipient
	session.Verified = false
	return session, nil
}

func (session *demoSession) verifyDemoOTP(key, code string, now time.Time) bool {
	if session.Challenge == "" || !now.Before(session.CodeExpires) {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(hash(key+code)), []byte(session.Challenge)) != 1 {
		session.CodeAttempts++
		if session.CodeAttempts >= 5 {
			session.Challenge = ""
		}
		return false
	}
	session.Challenge = ""
	session.Verified = true
	return true
}

func demoOTPRecipient(p Person, channel string) string {
	switch channel {
	case "sms":
		return smsPhone(p.PhoneNumber)
	case "email":
		address := strings.TrimSpace(p.Email)
		parsed, err := mail.ParseAddress(address)
		if err == nil && parsed.Address == address {
			return address
		}
	}
	return ""
}

func maskDemoOTPRecipient(channel, recipient string) string {
	if channel == "sms" {
		return "phone ending in " + recipient[len(recipient)-4:]
	}
	return "***" + recipient[strings.LastIndex(recipient, "@"):]
}

func demoOTPOption(channel, recipient string, available, selected bool) string {
	destination := template.HTMLEscapeString(maskDemoOTPRecipient(channel, recipient))
	state := ""
	if selected {
		state = " checked"
	}
	if !available {
		state = " disabled"
		destination += " (delivery unavailable)"
	}
	return `<div class="osmuk-radios__item"><input class="osmuk-radios__input" type="radio" id="delivery-` + channel +
		`" name="delivery" value="` + channel + `" required` + state + `><label class="osmuk-label osmuk-radios__label" for="delivery-` +
		channel + `">` + destination + `</label></div>`
}

func (s *Server) demoOTPSender(channel string) func(context.Context, string, string) error {
	if channel == "sms" {
		return s.demoOTPSend
	}
	if channel != "email" {
		return nil
	}
	if !s.cfg.SMTP.enabled() {
		if !s.cfg.LocalOTPOutbox {
			return nil
		}
		return func(_ context.Context, recipient, code string) error {
			return s.store.update(func(d *database) error {
				now := time.Now().UTC()
				for id, message := range d.Outbox {
					if message.LocalOnly && !now.Before(message.ExpiresAt) {
						delete(d.Outbox, id)
					}
				}
				id := randomToken("otp_")
				d.Outbox[id] = Message{
					ID: id, To: recipient, Subject: "Local test sign-in code",
					Body:      "Your local sign-in code is " + code + ". No email was sent.",
					CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute), LocalOnly: true,
				}
				return nil
			})
		}
	}
	return func(_ context.Context, recipient, code string) error {
		return s.mailSend(s.cfg.SMTP, Message{
			ID: randomToken("otp_"), To: recipient, Subject: "Your Private Records sign-in code",
			Body: "Your sign-in code is " + code + ". It expires in 10 minutes.",
		})
	}
}

type adminOTP struct {
	Hash, Salt string
	ExpiresAt  time.Time
	Attempts   int
}

func (s *Server) issueAdminOTP(personID string) (string, error) {
	code, err := newDemoOTP()
	if err != nil {
		return "", err
	}
	salt := randomToken("")
	now := time.Now()
	err = s.store.update(func(d *database) error {
		if _, ok := d.People[personID]; !ok {
			return errNotFound
		}
		if d.AdminOTPs == nil {
			d.AdminOTPs = make(map[string]adminOTP)
		}
		for id, otp := range d.AdminOTPs {
			if !now.Before(otp.ExpiresAt) {
				delete(d.AdminOTPs, id)
			}
		}
		d.AdminOTPs[personID] = adminOTP{Hash: hash(salt + code), Salt: salt, ExpiresAt: now.Add(24 * time.Hour)}
		return nil
	})
	return code, err
}

func (s *Server) verifyAdminOTP(personID, code string) (bool, error) {
	verified := false
	err := s.store.update(func(d *database) error {
		otp, ok := d.AdminOTPs[personID]
		if _, exists := d.People[personID]; !exists || !ok || !time.Now().Before(otp.ExpiresAt) {
			return errNotFound
		}
		verified = subtle.ConstantTimeCompare([]byte(hash(otp.Salt+code)), []byte(otp.Hash)) == 1
		otp.Attempts++
		if verified || otp.Attempts >= 5 {
			delete(d.AdminOTPs, personID)
		} else {
			d.AdminOTPs[personID] = otp
		}
		return nil
	})
	return verified && err == nil, err
}

func (s *Server) hasAdminOTP(personID string) bool {
	return s.cfg.LocalOTPOutbox && time.Now().Before(s.store.snapshot().AdminOTPs[personID].ExpiresAt)
}
