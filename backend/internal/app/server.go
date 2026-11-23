package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const Disclaimer = "Private, unverified record. Not a government service, immigration decision, or proof of immigration status."

var errNotFound = errors.New("record not found")

type Config struct {
	EmailLogoURL   string
	PublicURL      string
	SMTP           SMTPConfig
	AdminUsername  string
	AdminEmail     string
	AdminPassword  string
	APIToken       string
	SecureCookies  bool
	LocalOTPOutbox bool
}
type session struct {
	CSRF    string
	Expires time.Time
}
type Server struct {
	mailMu       sync.Mutex
	mailSend     func(SMTPConfig, Message) error
	store        *Store
	cfg          Config
	mux          *http.ServeMux
	mu           sync.Mutex
	sessions     map[string]session
	failures     int
	blockedUntil time.Time
	demoMu       sync.Mutex
	demoSessions map[string]demoSession
	demoSMSNext  map[string]time.Time
	demoOTPSend  func(context.Context, string, string) error
}

func New(store *Store, cfg Config) (*Server, error) {
	if len(cfg.AdminPassword) < 12 || len(cfg.APIToken) < 32 {
		return nil, errors.New("admin password must be at least 12 characters and API token at least 32")
	}
	if strings.TrimSpace(cfg.AdminUsername) == "" {
		cfg.AdminUsername = "admin"
	}
	cfg.AdminUsername = strings.TrimSpace(cfg.AdminUsername)
	cfg.AdminEmail = strings.TrimSpace(cfg.AdminEmail)
	if err := validateMailConfig(&cfg); err != nil {
		return nil, err
	}
	s := &Server{mailSend: sendSMTP, store: store, cfg: cfg, mux: http.NewServeMux(), sessions: map[string]session{}, demoSessions: map[string]demoSession{}, demoSMSNext: map[string]time.Time{}, demoOTPSend: msg91Sender()}
	s.routes()
	return s, nil
}
func randomToken(prefix string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}
func hash(v string) string { b := sha256.Sum256([]byte(v)); return hex.EncodeToString(b[:]) }
func equal(a, b string) bool {
	x := sha256.Sum256([]byte(a))
	y := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Discard transport metadata before routing, persistence, or provider calls.
	r = r.Clone(r.Context())
	r.RemoteAddr = ""
	for _, header := range []string{"Forwarded", "X-Forwarded-For", "X-Real-IP", "CF-Connecting-IP", "True-Client-IP", "Client-IP", "X-Client-IP", "Fastly-Client-IP"} {
		r.Header.Del(header)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'; connect-src 'self'")
	if r.URL.Path != "/healthz" && !strings.HasPrefix(r.URL.Path, "/assets/") {
		if err := s.store.refresh(r.Context()); err != nil {
			fail(w, http.StatusServiceUnavailable, "Saved records are temporarily unavailable")
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}
func send(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, message string) {
	send(w, status, map[string]string{"error": message})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		fail(w, 415, "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		fail(w, 400, "Invalid JSON or unknown fields")
		return false
	}
	if d.Decode(new(any)) != io.EOF {
		fail(w, 400, "Expected one JSON object")
		return false
	}
	return true
}
func (s *Server) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || !equal(token, s.cfg.APIToken) {
			fail(w, 401, "Admin bearer token required")
			return
		}
		next(w, r)
	}
}
func result(w http.ResponseWriter, err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, errNotFound) {
		fail(w, 404, "Record not found")
	} else {
		fail(w, 500, "Could not save changes")
	}
	return false
}
func (s *Server) routes() {
	s.websiteRoutes()
	s.mux.HandleFunc("GET /api/v1/reports", s.admin(func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, makeReport(s.store.snapshot(), time.Now(), reportMonths(r.URL.Query())))
	}))
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, map[string]string{"status": "ok", "service": "Private Records"})
	})
	s.mux.HandleFunc("GET /api/v1/people", s.admin(s.listPeople))
	s.mux.HandleFunc("POST /api/v1/people", s.admin(s.createPerson))
	s.mux.HandleFunc("POST /api/v1/people/lookup", s.admin(s.lookupPerson))
	s.mux.HandleFunc("GET /api/v1/people/{id}", s.admin(s.getPerson))
	s.mux.HandleFunc("PATCH /api/v1/people/{id}", s.admin(s.updatePerson))
	s.mux.HandleFunc("DELETE /api/v1/people/{id}", s.admin(s.deletePerson))
	s.mux.HandleFunc("PUT /api/v1/people/{id}/photo", s.admin(s.putPhoto))
	s.mux.HandleFunc("GET /api/v1/people/{id}/photo", s.admin(s.getPhoto))
	s.mux.HandleFunc("DELETE /api/v1/people/{id}/photo", s.admin(s.deletePhoto))
	s.mux.HandleFunc("POST /api/v1/people/{id}/shares", s.admin(s.createShare))
	s.mux.HandleFunc("GET /api/v1/people/{id}/shares", s.admin(s.listShares))
	s.mux.HandleFunc("GET /api/v1/shares/{id}", s.admin(s.getShare))
	s.mux.HandleFunc("POST /api/v1/shares/{id}/revoke", s.admin(s.revokeShare))
	s.mux.HandleFunc("POST /api/v1/shares/{id}/messages", s.admin(s.shareMessage))
	s.mux.HandleFunc("DELETE /api/v1/shares/{id}", s.admin(s.deleteShare))
	s.mux.HandleFunc("GET /api/v1/shared/{token}", s.resolveShare)
	s.mux.HandleFunc("POST /api/v1/people/{id}/email-verifications", s.admin(s.requestVerification))
	s.mux.HandleFunc("POST /api/v1/email-verifications/confirm", s.confirmVerification)
	s.mux.HandleFunc("POST /api/v1/outbox/{id}/send", s.admin(s.sendOutbox))
	s.mux.HandleFunc("GET /api/v1/shared/{token}/pdf", s.sharedPDF)
	s.mux.HandleFunc("GET /api/v1/outbox", s.admin(s.listOutbox))
	s.mux.HandleFunc("DELETE /api/v1/outbox/{id}", s.admin(s.deleteOutbox))
	s.mux.HandleFunc("GET /api/v1/audit", s.admin(s.listAudit))
	s.portalRoutes()
}
