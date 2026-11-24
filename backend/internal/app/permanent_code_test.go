package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPermanentCodePersistsAndCanBeReplaced(t *testing.T) {
	s, path, key := fixture(t)
	p := create(t, s)
	if err := s.setAdminCode(p.ID, "001234"); err != nil {
		t.Fatal(err)
	}
	otp := s.store.snapshot().AdminOTPs[p.ID]
	if !otp.Permanent || !otp.ExpiresAt.IsZero() || otp.Hash == "001234" {
		t.Fatal("code was not stored as a permanent hash")
	}
	for i := 0; i < 2; i++ {
		if ok, err := s.verifyAdminOTP(p.ID, "001234"); !ok || err != nil {
			t.Fatalf("repeat sign-in rejected: %v", err)
		}
	}
	if ok, _ := s.verifyAdminOTP("another-person", "001234"); ok {
		t.Fatal("code crossed records")
	}
	loaded, err := OpenStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	s.store = loaded
	if s.store.snapshot().AdminOTPs[p.ID].DisplayCode != "001234" {
		t.Fatal("display code did not persist")
	}
	if ok, _ := s.verifyAdminOTP(p.ID, "001234"); !ok {
		t.Fatal("code did not persist")
	}
	for _, invalid := range []string{"", "12345", "1234567", "abc123", " 123456"} {
		if err := s.setAdminCode(p.ID, invalid); err == nil {
			t.Fatalf("accepted invalid code %q", invalid)
		}
	}
	if err := s.setAdminCode(p.ID, "987654"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.verifyAdminOTP(p.ID, "001234"); ok {
		t.Fatal("old code remains valid")
	}
	if ok, _ := s.verifyAdminOTP(p.ID, "987654"); !ok {
		t.Fatal("replacement rejected")
	}
	for i := 0; i < 5; i++ {
		s.verifyAdminOTP(p.ID, "000000")
	}
	if ok, _ := s.verifyAdminOTP(p.ID, "987654"); ok {
		t.Fatal("lockout bypassed")
	}
	if !s.hasAdminOTP(p.ID) {
		t.Fatal("lockout deleted code")
	}
	if err := s.store.update(func(d *database) error {
		otp := d.AdminOTPs[p.ID]
		otp.LockedUntil = time.Now().Add(-time.Second)
		d.AdminOTPs[p.ID] = otp
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.verifyAdminOTP(p.ID, "987654"); !ok {
		t.Fatal("code failed after lockout")
	}
	if err := s.removePerson(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, exists := s.store.snapshot().AdminOTPs[p.ID]; exists {
		t.Fatal("deleted person's code retained")
	}
}

func TestAdminSetsCodeAndRepeatedSessionsVerify(t *testing.T) {
	s, _, _ := fixture(t)
	p := create(t, s)
	cookie, csrf := login(t, s)
	path := "/admin/people/" + p.ID + "/sign-in-code"
	post := func(path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	form := url.Values{"csrf": {csrf}, "security_code": {"012345"}}
	post(path, form, nil)
	if s.hasAdminOTP(p.ID) {
		t.Fatal("unauthenticated update accepted")
	}
	post(path, url.Values{"security_code": {"012345"}}, cookie)
	if s.hasAdminOTP(p.ID) {
		t.Fatal("CSRF protection bypassed")
	}
	saved := post(path, form, cookie)
	want(t, saved, 200)
	if !strings.Contains(saved.Body.String(), "Security code saved") || !strings.Contains(saved.Body.String(), "<code>012345</code>") {
		t.Fatal("missing confirmation or saved code display")
	}
	// A permanent code takes priority even if a delivery provider is configured.
	s.cfg.SMTP.Host = "unused.example.test"
	for _, token := range []string{"first-sign-in", "second-sign-in"} {
		s.demoSessions[hash(token)] = demoSession{PersonID: p.ID, Expires: time.Now().Add(time.Hour)}
		c := &http.Cookie{Name: "demo_session", Value: token}
		optionsRequest := httptest.NewRequest("GET", "/gov/security-code-delivery", nil)
		optionsRequest.AddCookie(c)
		options := httptest.NewRecorder()
		s.ServeHTTP(options, optionsRequest)
		want(t, options, 200)
		for _, text := range []string{"Sign in", "Receive a security code", "Continue to enter the security code for", "Problems signing in", "recover your account"} {
			if !strings.Contains(options.Body.String(), text) {
				t.Fatalf("missing selection text: %s", text)
			}
		}
		next := post("/gov/security-code-delivery", url.Values{"delivery": {"email"}}, c)
		want(t, next, 303)
		if s.demoSessions[hash(token)].CodeChannel != "admin" {
			t.Fatal("saved code did not take priority")
		}
		r := httptest.NewRequest("GET", "/gov/security-code", nil)
		r.AddCookie(c)
		page := httptest.NewRecorder()
		s.ServeHTTP(page, r)
		want(t, page, 200)
		if strings.Contains(page.Body.String(), "012345") {
			t.Fatal("code leaked onto public entry page")
		}
		for _, text := range []string{"Enter your security code", "Enter your 6-digit security code for:", maskDemoOTPRecipient("email", p.Email)} {
			if !strings.Contains(page.Body.String(), text) {
				t.Fatalf("missing entry text: %s", text)
			}
		}
		for _, text := range []string{"administrator-provided", "expire 24 hours", "single-use, 6-digit"} {
			if strings.Contains(page.Body.String(), text) {
				t.Fatalf("unwanted text: %s", text)
			}
		}
		verified := post("/gov/security-code", url.Values{"verificationCode": {"012345"}}, c)
		want(t, verified, 303)
		if verified.Header().Get("Location") != "/gov/status" {
			t.Fatal("sign-in failed")
		}
	}
}

func TestSavedCodeDisplayIsAdminOnly(t *testing.T) {
	s, path, _ := fixture(t)
	p := create(t, s)
	if err := s.setAdminCode(p.ID, "654321"); err != nil {
		t.Fatal(err)
	}
	cookie, _ := login(t, s)
	getPage := func(auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/admin/people/"+p.ID, nil)
		if auth {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	page := getPage(true)
	want(t, page, 200)
	if !strings.Contains(page.Body.String(), "<code>654321</code>") {
		t.Fatal("code missing on reopened admin page")
	}
	if page.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("code page is cacheable")
	}
	if strings.Contains(getPage(false).Body.String(), "654321") {
		t.Fatal("unauthenticated code exposure")
	}
	if strings.Contains(call(s, "GET", "/api/v1/people/"+p.ID, "", true).Body.String(), "654321") {
		t.Fatal("code leaked into person API")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("654321")) {
		t.Fatal("code stored without encryption")
	}
	if err := s.setAdminCode(p.ID, "123456"); err != nil {
		t.Fatal(err)
	}
	page = getPage(true)
	if !strings.Contains(page.Body.String(), "<code>123456</code>") || strings.Contains(page.Body.String(), "654321") {
		t.Fatal("replacement display incorrect")
	}
	if err := s.store.update(func(d *database) error {
		otp := d.AdminOTPs[p.ID]
		otp.DisplayCode = ""
		d.AdminOTPs[p.ID] = otp
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(getPage(true).Body.String(), "Save the code again below") {
		t.Fatal("legacy hash-only code has no explanation")
	}
	if ok, _ := s.verifyAdminOTP(p.ID, "123456"); !ok {
		t.Fatal("legacy code stopped working")
	}
}
