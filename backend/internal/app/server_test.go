package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

const testPassword = "test-password-only-123"
const testToken = "test-api-token-not-for-real-use-123456789"

func fixture(t *testing.T) (*Server, string, []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "records.enc")
	key := bytes.Repeat([]byte{7}, 32)
	store, err := OpenStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(store, Config{AdminPassword: testPassword, APIToken: testToken})
	if err != nil {
		t.Fatal(err)
	}
	return s, path, key
}
func call(s *Server, method, path, body string, auth bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if auth {
		r.Header.Set("Authorization", "Bearer "+testToken)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func want(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d want %d: %s", w.Code, status, w.Body.String())
	}
}
func create(t *testing.T, s *Server) Person {
	t.Helper()
	w := call(s, "POST", "/api/v1/people", `{"first_name":"Example","last_name":"Record","email":"example@example.invalid","date_of_birth":"1990-03-09","passport_number":"DEMO-ONLY","recorded_status":"User supplied example","national_insurance_number":"DEMO-ONLY","valid_from":"2026-01-01","valid_until":"2026-12-31"}`, true)
	want(t, w, 201)
	var p Person
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestPeopleAuthenticationValidationPersistence(t *testing.T) {
	s, path, key := fixture(t)
	want(t, call(s, "GET", "/api/v1/people", "", false), 401)
	want(t, call(s, "POST", "/api/v1/people", `{"first_name":"Example","unverified":false}`, true), 400)
	want(t, call(s, "POST", "/api/v1/people", `{"first_name":"Example","date_of_birth":"2024-02-30"}`, true), 422)
	want(t, call(s, "POST", "/api/v1/people", `{"first_name":"Example","valid_from":"2026-02-01","valid_until":"2025-01-01"}`, true), 422)
	p := create(t, s)
	if !p.Unverified {
		t.Fatal("records must remain unverified")
	}
	want(t, call(s, "PATCH", "/api/v1/people/"+p.ID, `{"nationality":"IND"}`, true), 200)
	w := call(s, "GET", "/api/v1/people/"+p.ID, "", true)
	want(t, w, 200)
	if !strings.Contains(w.Body.String(), "IND") {
		t.Fatal(w.Body.String())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{p.FirstName, p.Email, p.PassportNumber} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("plaintext leaked to disk")
		}
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("database permissions", info.Mode())
	}
	loaded, err := OpenStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.snapshot().People[p.ID].FirstName != p.FirstName {
		t.Fatal("record lost after reopening")
	}
	if _, err = OpenStore(path, bytes.Repeat([]byte{3}, 32)); err == nil {
		t.Fatal("wrong key accepted")
	}
	want(t, call(s, "DELETE", "/api/v1/people/"+p.ID, "", true), 204)
	want(t, call(s, "GET", "/api/v1/people/"+p.ID, "", true), 404)
}
func TestPhotos(t *testing.T) {
	s, _, _ := fixture(t)
	p := create(t, s)
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 30, 40))); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("PUT", "/api/v1/people/"+p.ID+"/photo", bytes.NewReader(raw.Bytes()))
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	want(t, w, 204)
	w = call(s, "GET", "/api/v1/people/"+p.ID+"/photo", "", true)
	want(t, w, 200)
	if w.Header().Get("Content-Type") != "image/png" {
		t.Fatal("bad image type")
	}
	if _, err := png.Decode(bytes.NewReader(w.Body.Bytes())); err != nil {
		t.Fatal(err)
	}
	want(t, call(s, "GET", "/api/v1/people/"+p.ID+"/photo", "", false), 401)
	want(t, call(s, "PUT", "/api/v1/people/"+p.ID+"/photo", "not an image", true), 422)
	want(t, call(s, "DELETE", "/api/v1/people/"+p.ID+"/photo", "", true), 204)
	want(t, call(s, "GET", "/api/v1/people/"+p.ID+"/photo", "", true), 404)
}
func TestSharingScopeExpiryRevocationAndCascade(t *testing.T) {
	s, _, _ := fixture(t)
	p := create(t, s)
	w := call(s, "POST", "/api/v1/people/"+p.ID+"/shares", `{"expires_in_hours":24}`, true)
	want(t, w, 201)
	var response struct {
		Share Share
		Token string
		Path  string
	}
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	if !strings.HasPrefix(response.Token, "internal_") {
		t.Fatal("ambiguous token")
	}
	w = call(s, "GET", "/api/v1/shared/"+response.Token, "", false)
	want(t, w, 200)
	for _, field := range []string{"passport_number", "national_insurance_number", "date_of_birth", "email"} {
		if strings.Contains(w.Body.String(), field) {
			t.Fatal("private field shared", field)
		}
	}
	if !strings.Contains(w.Body.String(), "unverified") {
		t.Fatal("missing disclaimer")
	}
	list := call(s, "GET", "/api/v1/people/"+p.ID+"/shares", "", true)
	want(t, list, 200)
	if strings.Contains(list.Body.String(), response.Token) {
		t.Fatal("token disclosed after creation")
	}
	want(t, call(s, "POST", "/api/v1/shares/"+response.Share.ID+"/revoke", "", true), 204)
	want(t, call(s, "GET", "/api/v1/shared/"+response.Token, "", false), 404)
	sh, token, err := s.newShare(p.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.store.update(func(d *database) error {
		x := d.Shares[sh.ID]
		x.ExpiresAt = time.Now().Add(-time.Minute)
		d.Shares[sh.ID] = x
		return nil
	})
	want(t, call(s, "GET", "/api/v1/shared/"+token, "", false), 404)
	_, token, err = s.newShare(p.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.verification(p.ID); err != nil {
		t.Fatal(err)
	}
	want(t, call(s, "DELETE", "/api/v1/people/"+p.ID, "", true), 204)
	want(t, call(s, "GET", "/api/v1/shared/"+token, "", false), 404)
	d := s.store.snapshot()
	if len(d.Shares)+len(d.ShareHashes)+len(d.Verifications)+len(d.Outbox) != 0 {
		t.Fatal("cascade left dependent records")
	}
}
func verificationToken(t *testing.T, s *Server, mid string) string {
	t.Helper()
	body := s.store.snapshot().Outbox[mid].Body
	matches := regexp.MustCompile(`/verify-email/(verify_[A-Za-z0-9_-]+)`).FindStringSubmatch(body)
	if len(matches) != 2 {
		t.Fatal("no development token")
	}
	return matches[1]
}
func TestEmailVerification(t *testing.T) {
	s, _, _ := fixture(t)
	p := create(t, s)
	w := call(s, "POST", "/api/v1/people/"+p.ID+"/email-verifications", "", true)
	want(t, w, 202)
	if !strings.Contains(w.Body.String(), `"email_sent":false`) {
		t.Fatal("must not claim delivery")
	}
	var out struct {
		ID string `json:"outbox_message_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	token := verificationToken(t, s, out.ID)
	want(t, call(s, "POST", "/api/v1/email-verifications/confirm", fmt.Sprintf(`{"token":%q}`, token), false), 200)
	want(t, call(s, "POST", "/api/v1/email-verifications/confirm", fmt.Sprintf(`{"token":%q}`, token), false), 404)
	if s.store.snapshot().People[p.ID].EmailVerifiedAt == nil {
		t.Fatal("email not verified")
	}
	want(t, call(s, "PATCH", "/api/v1/people/"+p.ID, `{"email":"changed@example.invalid"}`, true), 200)
	if s.store.snapshot().People[p.ID].EmailVerifiedAt != nil {
		t.Fatal("email change retained verification")
	}
	mid, err := s.verification(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	token = verificationToken(t, s, mid)
	_ = s.store.update(func(d *database) error {
		v := d.Verifications[hash(token)]
		v.ExpiresAt = time.Now().Add(-time.Second)
		d.Verifications[hash(token)] = v
		return nil
	})
	want(t, call(s, "POST", "/api/v1/email-verifications/confirm", fmt.Sprintf(`{"token":%q}`, token), false), 404)
}
func login(t *testing.T, s *Server) (*http.Cookie, string) {
	t.Helper()
	w := call(s, "GET", "/admin/login", "", false)
	want(t, w, 200)
	nonce := w.Result().Cookies()[0]
	form := url.Values{"identifier": {"admin"}, "password": {testPassword}, "csrf": {nonce.Value}}
	r := httptest.NewRequest("POST", "/admin/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(nonce)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	want(t, w, 303)
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "private_records_session" {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("missing secure session attributes")
	}
	r = httptest.NewRequest("GET", "/admin", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	want(t, w, 200)
	re := regexp.MustCompile(`name="csrf" value="([^"]+)"`)
	matches := re.FindStringSubmatch(w.Body.String())
	if len(matches) != 2 {
		t.Fatal("missing CSRF")
	}
	return cookie, matches[1]
}
func TestPortalCSRFAndEscaping(t *testing.T) {
	s, _, _ := fixture(t)
	cookie, csrf := login(t, s)
	r := httptest.NewRequest("POST", "/admin/people/new", strings.NewReader("name=Example"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	want(t, w, 403)
	form := url.Values{"first_name": {"<script>alert(1)</script>"}, "csrf": {csrf}}
	r = httptest.NewRequest("POST", "/admin/people/new", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	want(t, w, 303)
	r = httptest.NewRequest("GET", w.Header().Get("Location"), nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	want(t, w, 200)
	if strings.Contains(w.Body.String(), "<script>alert(1)</script>") {
		t.Fatal("unescaped name")
	}
	if !strings.Contains(w.Body.String(), "Not a government service") {
		t.Fatal("missing branding disclaimer")
	}
}
func TestLoginRateLimit(t *testing.T) {
	s, _, _ := fixture(t)
	w := call(s, "GET", "/admin/login", "", false)
	nonce := w.Result().Cookies()[0]
	for i := 0; i < 6; i++ {
		form := url.Values{"identifier": {"admin"}, "password": {"wrong"}, "csrf": {nonce.Value}}
		r := httptest.NewRequest("POST", "/admin/login", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(nonce)
		w = httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if i < 5 {
			want(t, w, 401)
		} else {
			want(t, w, 429)
		}
	}
}
func TestConcurrentWrites(t *testing.T) {
	s, _, _ := fixture(t)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.savePerson("", map[string]string{"first_name": fmt.Sprintf("Example %d", i)})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if len(s.store.snapshot().People) != 12 {
		t.Fatal("lost concurrent updates")
	}
}
func TestFailedPersistenceRollsBack(t *testing.T) {
	s, _, _ := fixture(t)
	block := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(block, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	s.store.path = filepath.Join(block, "impossible.enc")
	if _, err := s.savePerson("", map[string]string{"first_name": "Example"}); err == nil {
		t.Fatal("expected persistence failure")
	}
	if len(s.store.snapshot().People) != 0 {
		t.Fatal("failed write changed memory state")
	}
}

func TestShareMessagesAndEmailChangeInvalidateTokens(t *testing.T) {
	s, _, _ := fixture(t)
	p := create(t, s)
	sh, token, err := s.newShare(p.ID, 24)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"token":%q,"recipient":"recipient@example.invalid"}`, token)
	w := call(s, "POST", "/api/v1/shares/"+sh.ID+"/messages", body, true)
	want(t, w, 202)
	if !strings.Contains(w.Body.String(), `"email_sent":false`) {
		t.Fatal("incorrect delivery claim")
	}
	want(t, call(s, "POST", "/api/v1/shares/"+sh.ID+"/messages", `{"token":"invalid","recipient":"recipient@example.invalid"}`, true), 404)
	want(t, call(s, "POST", "/api/v1/shares/"+sh.ID+"/messages", fmt.Sprintf(`{"token":%q,"recipient":"invalid"}`, token), true), 422)
	mid, err := s.verification(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	verification := verificationToken(t, s, mid)
	want(t, call(s, "PATCH", "/api/v1/people/"+p.ID, `{"email":"different@example.invalid"}`, true), 200)
	want(t, call(s, "POST", "/api/v1/email-verifications/confirm", fmt.Sprintf(`{"token":%q}`, verification), false), 404)
	want(t, call(s, "DELETE", "/api/v1/shares/"+sh.ID, "", true), 204)
	want(t, call(s, "GET", "/api/v1/shared/"+token, "", false), 404)
	want(t, call(s, "GET", "/api/v1/outbox", "", false), 401)
}
