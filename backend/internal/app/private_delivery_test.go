package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func codeFor(t *testing.T, m Message) string {
	t.Helper()
	match := regexp.MustCompile(`security code: ([0-9]{6})`).FindStringSubmatch(m.Body)
	if len(match) != 2 {
		t.Fatal("missing code")
	}
	return match[1]
}
func TestPrivateEmailCodesAndTemplates(t *testing.T) {
	s, _, _ := fixture(t)
	p, err := s.savePerson("", map[string]string{"first_name": "Example <script>", "last_name": "Person", "email": "example@example.invalid", "passport_number": "TEST12345678"})
	if err != nil {
		t.Fatal(err)
	}
	mid, err := s.verification(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := s.store.snapshot().Outbox[mid]
	if !strings.Contains(m.HTML, "5678") || strings.Contains(m.HTML, p.PassportNumber) || strings.Contains(m.HTML, "<script>") || !strings.Contains(m.HTML, "&lt;script&gt;") || !strings.Contains(m.HTML, "not proof of immigration status") {
		t.Fatal("unsafe or missing template data")
	}
	code := codeFor(t, m)
	if err = s.verifyCode(m.ChallengeID, code); err != nil {
		t.Fatal(err)
	}
	if !s.store.snapshot().People[p.ID].Unverified {
		t.Fatal("email verified an immigration record")
	}
	if err = s.verifyCode(m.ChallengeID, code); err == nil {
		t.Fatal("code reused")
	}
	mid, _ = s.verification(p.ID)
	m = s.store.snapshot().Outbox[mid]
	code = codeFor(t, m)
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}
	for i := 0; i < 5; i++ {
		if s.verifyCode(m.ChallengeID, wrong) == nil {
			t.Fatal("wrong code accepted")
		}
	}
	if s.verifyCode(m.ChallengeID, code) == nil {
		t.Fatal("attempt limit bypassed")
	}
	mid, _ = s.verification(p.ID)
	m = s.store.snapshot().Outbox[mid]
	code = codeFor(t, m)
	_ = s.store.update(func(d *database) error {
		for k, v := range d.Verifications {
			v.ExpiresAt = time.Now().Add(-time.Second)
			d.Verifications[k] = v
		}
		return nil
	})
	if s.verifyCode(m.ChallengeID, code) == nil {
		t.Fatal("expired code accepted")
	}
	mid, _ = s.verification(p.ID)
	m = s.store.snapshot().Outbox[mid]
	code = codeFor(t, m)
	_, _ = s.verification(p.ID)
	if s.verifyCode(m.ChallengeID, code) == nil {
		t.Fatal("superseded code accepted")
	}
	if passportEnding("1234") != "" {
		t.Fatal("short passport exposed in full")
	}
}
func TestPrivateEmailDeliveryAndMIME(t *testing.T) {
	s, _, _ := fixture(t)
	p := create(t, s)
	mid, err := s.verification(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if s.deliverMessage(mid) == nil {
		t.Fatal("unconfigured SMTP claimed delivery")
	}
	m := s.store.snapshot().Outbox[mid]
	raw, err := mimeEmail("sender@example.invalid", m)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	kind, params, _ := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if kind != "multipart/alternative" {
		t.Fatal(kind)
	}
	reader := multipart.NewReader(parsed.Body, params["boundary"])
	for _, wantType := range []string{"text/plain", "text/html"} {
		part, e := reader.NextPart()
		if e != nil || !strings.HasPrefix(part.Header.Get("Content-Type"), wantType) {
			t.Fatal("missing MIME part")
		}
	}
	calls := 0
	s.cfg.SMTP.Host = "test.invalid"
	s.mailSend = func(SMTPConfig, Message) error { calls++; return errors.New("test failure") }
	if s.deliverMessage(mid) == nil || s.store.snapshot().Outbox[mid].SentAt != nil {
		t.Fatal("failed delivery marked sent")
	}
	s.mailSend = func(SMTPConfig, Message) error { calls++; return nil }
	if err = s.deliverMessage(mid); err != nil {
		t.Fatal(err)
	}
	if s.store.snapshot().Outbox[mid].SentAt == nil {
		t.Fatal("missing sent timestamp")
	}
	if s.deliverMessage(mid) == nil || calls != 2 {
		t.Fatal("duplicate send")
	}
	mid, _ = s.verification(p.ID)
	_, _ = s.verification(p.ID)
	if s.deliverMessage(mid) == nil || calls != 2 {
		t.Fatal("sent superseded verification")
	}
}
func TestPrivateSharedPDFAndRevocation(t *testing.T) {
	s, _, _ := fixture(t)
	p := create(t, s)
	sh, token, err := s.newShare(p.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	want(t, call(s, "GET", "/shared/wrong/pdf", "", false), 404)
	w := call(s, "GET", "/shared/"+token+"/pdf", "", false)
	want(t, w, 200)
	if !bytes.HasPrefix(w.Body.Bytes(), []byte("%PDF-")) || w.Header().Get("Content-Type") != "application/pdf" || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("invalid PDF download")
	}
	if err = s.changeShare(sh.ID, false); err != nil {
		t.Fatal(err)
	}
	want(t, call(s, "GET", "/shared/"+token+"/pdf", "", false), 404)
	// Optional fictional-only artifacts for visual QA; no runtime database is read.
	if directory := os.Getenv("PRIVATE_RECORDS_PREVIEW_DIR"); directory != "" {
		if err = os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		data := map[string]any{"first_name": "Example", "last_name": "Person", "nationality": "Example nationality", "recorded_status": "User-entered sample - unverified", "valid_from": "2026-01-01", "valid_until": "2026-12-31", "expires_at": time.Now().Add(time.Hour), "internal_reference": "internal_SAMPLE_ONLY_NOT_A_REAL_TOKEN", "private_link": "http://localhost:8081/shared/internal_SAMPLE_ONLY_NOT_A_REAL_TOKEN"}
		pdf, e := privateRecordPDF(data)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(directory, "private-record-sample.pdf"), pdf, 0600); e != nil {
			t.Fatal(e)
		}
		_, html, e := renderEmail(emailData{Subject: "Private Records: your email verification code", Heading: "Verify your email", Name: "Example Person", Intro: "You requested email verification for your Private Records account.", PassportEnding: "1234", Code: "000000", Explanation: "Sample message only. This code is not active.", URL: "http://localhost:8081/", Action: "Open Private Records", Closing: "If you did not request this, ignore this message."})
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(directory, "verification-email-sample.html"), []byte(html), 0600); e != nil {
			t.Fatal(e)
		}
	}
}
func TestCodeAPI(t *testing.T) {
	s, _, _ := fixture(t)
	p := create(t, s)
	w := call(s, "POST", "/api/v1/people/"+p.ID+"/email-verifications", "", true)
	want(t, w, 202)
	var data struct {
		ID        string `json:"outbox_message_id"`
		Challenge string `json:"challenge_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	m := s.store.snapshot().Outbox[data.ID]
	want(t, call(s, "POST", "/api/v1/email-verifications/confirm", fmt.Sprintf(`{"challenge_id":%q,"code":%q}`, data.Challenge, codeFor(t, m)), false), 200)
}

func TestPrivateMailConfiguration(t *testing.T) {
	for _, origin := range []string{"javascript:alert(1)", "https://name:password@example.com", "http://remote.example.com", "https://example.com/?x=1"} {
		cfg := Config{PublicURL: origin}
		if validateMailConfig(&cfg) == nil {
			t.Fatalf("accepted invalid origin %q", origin)
		}
	}
	cfg := Config{PublicURL: "https://records.example.com", SMTP: SMTPConfig{Host: "smtp.example.com", Username: "user", Password: "test", From: "sender@example.com"}}
	if err := validateMailConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.SMTP.Port != "587" {
		t.Fatal("missing default SMTP port")
	}
	if _, err := mimeEmail("sender@example.invalid", Message{To: "person@example.com\r\nBcc: other@example.com"}); err == nil {
		t.Fatal("header injection accepted")
	}
}
