package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
)

var internationalSMSPhone = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

func smsPhone(value string) string {
	phone := strings.NewReplacer(" ", "", "(", "", ")", "", "-", "", ".", "").Replace(strings.TrimSpace(value))
	if !internationalSMSPhone.MatchString(phone) {
		return ""
	}
	return phone
}

// sendDemoOTP issues a challenge only after the supplied sender accepts the code.
func sendDemoOTP(ctx context.Context, send func(context.Context, string, string) error, session demoSession, key, phone string) (demoSession, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return session, err
	}
	code := fmt.Sprintf("%06d", value.Int64())
	if err := send(ctx, phone, code); err != nil {
		return session, err
	}
	session.Challenge = hash(key + code)
	session.CodeExpires = time.Now().Add(10 * time.Minute)
	session.CodeAttempts = 0
	session.CodePhone = phone
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
