package app

import (
	"bytes"
	"crypto/tls"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

//go:embed templates/email.html
var emailHTML string
var emailTemplate = template.Must(template.New("email").Parse(emailHTML))

type emailData struct{ Subject, Heading, Name, Intro, PassportEnding, Code, Explanation, URL, Action, Closing, LogoURL string }
type SMTPConfig struct {
	Host, Port, Username, Password, From string
	ImplicitTLS                          bool
	BrevoAPIKey, FromEmail, FromName     string
}

func (c SMTPConfig) enabled() bool {
	return c.BrevoAPIKey != "" || c.Host != ""
}
func (c SMTPConfig) senderEmail() string {
	if c.FromEmail != "" {
		return c.FromEmail
	}
	return c.From
}

func (s *Server) mailURL(path string) string { return strings.TrimRight(s.cfg.PublicURL, "/") + path }
func passportEnding(value string) string {
	r := []rune(strings.TrimSpace(value))
	if len(r) <= 4 {
		return ""
	}
	return string(r[len(r)-4:])
}
func renderEmail(d emailData) (string, string, error) {
	var b bytes.Buffer
	if err := emailTemplate.Execute(&b, d); err != nil {
		return "", "", err
	}
	text := "Private Records\n\n" + d.Heading + "\n\nDear " + d.Name + ",\n\n" + d.Intro + "\n"
	if d.PassportEnding != "" {
		text += "Passport ending: ****" + d.PassportEnding + "\n"
	}
	if d.Code != "" {
		text += "\nYour Private Records security code: " + d.Code + "\nExpires in 10 minutes. Single use. Do not share it.\n"
	}
	text += "\n" + d.Explanation + "\n\n" + d.Action + ": " + d.URL + "\n\n" + d.Closing + "\n\nRegards,\nPrivate Records\n\nUnverified private record - not proof of immigration status.\n" + Disclaimer
	return text, b.String(), nil
}
func validateMailConfig(cfg *Config) error {
	if cfg.PublicURL == "" {
		cfg.PublicURL = "http://localhost:8081"
	}
	u, err := url.Parse(cfg.PublicURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || (u.Scheme != "https" && !(u.Scheme == "http" && u.Hostname() == "localhost")) {
		return errors.New("RECORDS_PUBLIC_URL must be an HTTPS origin, or HTTP localhost")
	}
	if cfg.EmailLogoURL != "" {
		logo, err := url.Parse(cfg.EmailLogoURL)
		if err != nil || logo.Host == "" || logo.User != nil || (logo.Scheme != "https" && !(logo.Scheme == "http" && logo.Hostname() == "localhost")) {
			return errors.New("RECORDS_EMAIL_LOGO_URL must be an HTTPS image URL, or HTTP localhost")
		}
	}
	if cfg.SMTP.BrevoAPIKey != "" {
		from := cfg.SMTP.senderEmail()
		a, err := mail.ParseAddress(from)
		if err != nil || a.Address != from || strings.ContainsAny(from, "\r\n") {
			return errors.New("email API requires a valid FROM_EMAIL sender address")
		}
		return nil
	}
	if cfg.SMTP.Host == "" {
		return nil
	}
	if cfg.SMTP.Port == "" {
		cfg.SMTP.Port = "587"
	}
	a, err := mail.ParseAddress(cfg.SMTP.From)
	if err != nil || a.Address != cfg.SMTP.From || strings.ContainsAny(cfg.SMTP.From, "\r\n") {
		return errors.New("SMTP requires a valid sender address")
	}
	if cfg.SMTP.Username == "" || cfg.SMTP.Password == "" {
		return errors.New("SMTP requires username and password")
	}
	return nil
}
func mimeEmail(from string, m Message) ([]byte, error) {
	a, err := mail.ParseAddress(m.To)
	if err != nil || a.Address != m.To || strings.ContainsAny(m.To, "\r\n") {
		return nil, errors.New("invalid recipient")
	}
	var body bytes.Buffer
	multi := multipart.NewWriter(&body)
	for _, part := range []struct{ kind, value string }{{"text/plain", m.Body}, {"text/html", m.HTML}} {
		h := textproto.MIMEHeader{}
		h.Set("Content-Type", part.kind+"; charset=UTF-8")
		h.Set("Content-Transfer-Encoding", "quoted-printable")
		w, e := multi.CreatePart(h)
		if e != nil {
			return nil, e
		}
		q := quotedprintable.NewWriter(w)
		if _, e = q.Write([]byte(part.value)); e != nil {
			return nil, e
		}
		if e = q.Close(); e != nil {
			return nil, e
		}
	}
	if err = multi.Close(); err != nil {
		return nil, err
	}
	headers := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=%q\r\n\r\n", (&mail.Address{Name: "Private Records", Address: from}).String(), a.String(), mime.QEncoding.Encode("UTF-8", strings.ReplaceAll(strings.ReplaceAll(m.Subject, "\r", ""), "\n", "")), time.Now().UTC().Format(time.RFC1123Z), multi.Boundary())
	return append([]byte(headers), body.Bytes()...), nil
}
func sendSMTP(cfg SMTPConfig, m Message) error {
	raw, err := mimeEmail(cfg.From, m)
	if err != nil {
		return err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	address := net.JoinHostPort(cfg.Host, cfg.Port)
	tlsCfg := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	if cfg.ImplicitTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, tlsCfg)
	} else {
		conn, err = dialer.Dial("tcp", address)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return err
	}
	defer client.Close()
	if !cfg.ImplicitTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server must support STARTTLS")
		}
		if err = client.StartTLS(tlsCfg); err != nil {
			return err
		}
	}
	if err = client.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
		return err
	}
	if err = client.Mail(cfg.From); err != nil {
		return err
	}
	if err = client.Rcpt(m.To); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = w.Write(raw); err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	// Acceptance is successful even if the peer closes before QUIT completes.
	_ = client.Quit()
	return nil
}

func sendBrevo(cfg SMTPConfig, m Message) error {
	senderEmail := cfg.FromEmail
	if senderEmail == "" {
		senderEmail = cfg.From
	}
	if cfg.BrevoAPIKey == "" || senderEmail == "" {
		return errors.New("Brevo sender configuration is incomplete")
	}
	payload := struct {
		Sender struct {
			Email string `json:"email"`
			Name  string `json:"name,omitempty"`
		} `json:"sender"`
		To []struct {
			Email string `json:"email"`
		} `json:"to"`
		Subject     string `json:"subject"`
		HTMLContent string `json:"htmlContent"`
		TextContent string `json:"textContent"`
	}{}
	payload.Sender.Email, payload.Sender.Name = senderEmail, cfg.FromName
	payload.To = []struct {
		Email string `json:"email"`
	}{{Email: m.To}}
	payload.Subject, payload.HTMLContent, payload.TextContent = m.Subject, m.HTML, m.Body
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.brevo.com/v3/smtp/email", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("api-key", cfg.BrevoAPIKey)
	req.Header.Set("content-type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("Brevo API returned HTTP %d", resp.StatusCode)
	}
	return nil
}
func (s *Server) deliverMessage(id string) error {
	if !s.cfg.SMTP.enabled() {
		return errors.New("email delivery is not configured; message remains in the development outbox")
	}
	s.mailMu.Lock()
	defer s.mailMu.Unlock()
	d := s.store.snapshot()
	m, ok := d.Outbox[id]
	if !ok {
		return errNotFound
	}
	if m.SentAt != nil {
		return errors.New("message has already been sent")
	}
	if !m.ExpiresAt.IsZero() && !time.Now().Before(m.ExpiresAt) {
		return errors.New("message expired; create a new one")
	}
	if m.ChallengeID != "" {
		active := false
		for _, v := range d.Verifications {
			if v.ID == m.ChallengeID {
				active = true
				break
			}
		}
		if !active {
			return errors.New("verification is no longer active")
		}
	}
	if m.ShareID != "" {
		sh, ok := d.Shares[m.ShareID]
		if !ok || sh.RevokedAt != nil || !time.Now().Before(sh.ExpiresAt) {
			return errors.New("sharing link is no longer active")
		}
	}
	var sendErr error
	if s.cfg.SMTP.BrevoAPIKey != "" {
		sendErr = sendBrevo(s.cfg.SMTP, m)
	} else {
		sendErr = s.mailSend(s.cfg.SMTP, m)
	}
	if sendErr != nil {
		return errors.New("email delivery failed; message remains unsent. Check the email provider configuration before retrying")
	}
	return s.store.update(func(d *database) error {
		current, ok := d.Outbox[id]
		if !ok {
			return errNotFound
		}
		now := time.Now().UTC()
		current.SentAt = &now
		d.Outbox[id] = current
		event(d, "email.sent", id)
		return nil
	})
}
func (s *Server) sendOutbox(w http.ResponseWriter, r *http.Request) {
	if err := s.deliverMessage(r.PathValue("id")); err != nil {
		if errors.Is(err, errNotFound) {
			fail(w, 404, "Message not found")
		} else {
			fail(w, 422, err.Error())
		}
		return
	}
	send(w, 200, map[string]bool{"email_sent": true})
}
func (s *Server) mailPreview(w http.ResponseWriter, r *http.Request) {
	m, ok := s.store.snapshot().Outbox[r.PathValue("id")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src https: http://localhost:*; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; sandbox")
	_, _ = w.Write([]byte(m.HTML))
}
