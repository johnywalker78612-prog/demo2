package app

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
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
	at := strings.LastIndex(recipient, "@")
	if at <= 0 {
		return "***"
	}
	local := []rune(recipient[:at])
	domain := recipient[at:]
	// Keep short addresses masked rather than exposing their entire local part.
	if len(local) <= 4 {
		return string(local[:1]) + "***" + domain
	}
	return string(local[:2]) + "***" + string(local[len(local)-2:]) + domain
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
		channel + `">Use a security code for<br><strong>` + destination + `</strong></label></div>`
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
	// DisplayCode is persisted inside the encrypted store and rendered only to authenticated admins.
	DisplayCode string `json:"display_code,omitempty"`
	Hash, Salt  string
	ExpiresAt   time.Time
	Attempts    int
	Permanent   bool
	LockedUntil time.Time
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
			if !otp.Permanent && !now.Before(otp.ExpiresAt) {
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
		if _, exists := d.People[personID]; !exists || !ok || (!otp.Permanent && !time.Now().Before(otp.ExpiresAt)) {
			return errNotFound
		}
		if otp.Permanent {
			now := time.Now()
			if now.Before(otp.LockedUntil) {
				return nil
			}
			if !otp.LockedUntil.IsZero() {
				otp.Attempts = 0
				otp.LockedUntil = time.Time{}
			}
			candidate := ""
			if sixDigitSecurityCode.MatchString(code) {
				candidate, _ = permanentCodeHash(otp.Salt, code)
			}
			verified = subtle.ConstantTimeCompare([]byte(candidate), []byte(otp.Hash)) == 1
			if verified {
				otp.Attempts = 0
			} else {
				otp.Attempts++
				if otp.Attempts >= 5 {
					otp.LockedUntil = now.Add(15 * time.Minute)
				}
			}
			d.AdminOTPs[personID] = otp
			return nil
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

// Permanent codes remain available during a temporary retry lockout.
func (s *Server) hasAdminOTP(personID string) bool {
	otp, exists := s.store.snapshot().AdminOTPs[personID]
	return exists && otp.Hash != "" && (otp.Permanent || time.Now().Before(otp.ExpiresAt))
}

var sixDigitSecurityCode = regexp.MustCompile(`^[0-9]{6}$`)

func permanentCodeHash(salt, code string) (string, error) {
	key, err := pbkdf2.Key(sha256.New, code, []byte(salt), 600000, 32)
	return hex.EncodeToString(key), err
}

func (s *Server) setAdminCode(personID, code string) error {
	if !sixDigitSecurityCode.MatchString(code) {
		return errors.New("Enter exactly six digits.")
	}
	salt := randomToken("")
	digest, err := permanentCodeHash(salt, code)
	if err != nil {
		return err
	}
	return s.store.update(func(d *database) error {
		if _, exists := d.People[personID]; !exists {
			return errNotFound
		}
		if d.AdminOTPs == nil {
			d.AdminOTPs = make(map[string]adminOTP)
		}
		d.AdminOTPs[personID] = adminOTP{Hash: digest, Salt: salt, Permanent: true, DisplayCode: code}
		return nil
	})
}
