package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDemoRejectsUnmatchedIdentityAndClearsPreviousMatch(t *testing.T) {
	s, _, _ := fixture(t)
	p := create(t, s)
	key := hash("demo-test")
	s.demoSessions[key] = demoSession{Passport: p.PassportNumber, PersonID: p.ID, Verified: true, Challenge: "old", Expires: time.Now().Add(time.Hour)}
	post := func(path string, values url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "demo_session", Value: "demo-test"})
		w := httptest.NewRecorder()
		s.demoFlow(w, r)
		return w
	}
	want(t, post("/gov/date-of-birth", url.Values{"date_of_birth": {"2000-01-01"}}), 422)
	se := s.demoSessions[key]
	if se.PersonID != "" || se.Verified || se.Challenge != "" {
		t.Fatal("previous identity was retained")
	}
	w := post("/gov/security-code-delivery", nil)
	want(t, w, 303)
	if w.Header().Get("Location") != "/gov/passport" {
		t.Fatal("missing identity should restart flow")
	}
	want(t, post("/gov/date-of-birth", url.Values{"date_of_birth": {p.DateOfBirth}}), 303)
	want(t, post("/gov/security-code-delivery", url.Values{"delivery": {"sms"}}), 422)
	if len(s.store.snapshot().Outbox) != 0 {
		t.Fatal("missing phone must not create an email")
	}
}

func TestDemoIdentityMismatchPage(t *testing.T) {
	s, _, _ := fixture(t)
	p := create(t, s)
	for _, tc := range []struct{ name, id, dob, display string }{
		{"wrong ID", "UNKNOWN", p.DateOfBirth, "09 March 1990"},
		{"wrong DOB", p.PassportNumber, "2001-01-21", "21 January 2001"},
		{"both wrong", "UNKNOWN", "2001-01-21", "21 January 2001"},
		{"escaped input", "<script>alert(1)</script>", "bad<date>", "bad&lt;date&gt;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.demoSessions[hash("mismatch-test")] = demoSession{Passport: tc.id, PersonID: p.ID, Challenge: "old", Verified: true, Expires: time.Now().Add(time.Hour)}
			r := httptest.NewRequest(http.MethodPost, "/gov/date-of-birth", strings.NewReader(url.Values{"date_of_birth": {tc.dob}}.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.AddCookie(&http.Cookie{Name: "demo_session", Value: "mismatch-test"})
			w := httptest.NewRecorder()
			s.demoFlow(w, r)
			want(t, w, http.StatusUnprocessableEntity)
			body := w.Body.String()
			for _, expected := range []string{"The details entered do not match our records", tc.display, `href="/gov/passport"`, "Try again"} {
				if !strings.Contains(body, expected) {
					t.Fatalf("missing %q", expected)
				}
			}
			if strings.Contains(body, "<script>alert(1)</script>") || strings.Contains(body, "120382978") || strings.Contains(body, "/auth/realms/") {
				t.Fatal("unsafe or copied identity content")
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("identity page must not be cached")
			}
			se := s.demoSessions[hash("mismatch-test")]
			if se.PersonID != "" || se.Challenge != "" || se.Verified {
				t.Fatal("previous verification retained")
			}
			if len(s.store.snapshot().Outbox) != 0 {
				t.Fatal("mismatch created an email")
			}
		})
	}
}

func TestRenamedFlowTemplatesAndCookie(t *testing.T) {
	s, _, _ := fixture(t)
	old := call(s, "GET", "/demo/passport", "", false)
	want(t, old, 308)
	if old.Header().Get("Location") != "/gov/passport" {
		t.Fatal("legacy redirect missing")
	}
	first := call(s, "GET", "/gov/passport", "", false)
	want(t, first, 200)
	if !strings.Contains(first.Body.String(), "documentNumber") {
		t.Fatal("passport template missing")
	}
	cookies := first.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Path != "/gov" {
		t.Fatal("session cookie has wrong path")
	}
	r := httptest.NewRequest("GET", "/gov/date-of-birth", nil)
	r.AddCookie(cookies[0])
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	want(t, w, 200)
	if !strings.Contains(w.Body.String(), "birth") {
		t.Fatal("date template missing")
	}
}

func TestDemoBothOptionsSendOnlyToLinkedPhone(t *testing.T) {
	for _, choice := range []string{"email", "sms"} {
		t.Run(choice, func(t *testing.T) {
			s, _, _ := fixture(t)
			p := create(t, s)
			if err := s.store.update(func(d *database) error {
				p.PhoneNumber = "+44 7700 900123"
				p.Email = ""
				d.People[p.ID] = p
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			var code string
			sends := 0
			s.demoOTPSend = func(_ context.Context, phone, value string) error {
				sends++
				if phone != "+447700900123" || len(value) != 6 {
					t.Fatal("unexpected SMS recipient or code length")
				}
				code = value
				return nil
			}
			seed := httptest.NewRecorder()
			s.ServeHTTP(seed, httptest.NewRequest("GET", "/gov/passport", nil))
			cookie := seed.Result().Cookies()[0]
			request := func(method, path string, values url.Values) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				r.AddCookie(cookie)
				w := httptest.NewRecorder()
				s.ServeHTTP(w, r)
				return w
			}
			want(t, request("POST", "/gov/passport", url.Values{"documentNumber": {p.PassportNumber}}), 303)
			want(t, request("POST", "/gov/date-of-birth", url.Values{"date_of_birth": {p.DateOfBirth}}), 303)
			options := request("GET", "/gov/security-code-delivery", nil)
			want(t, options, 200)
			if !strings.Contains(options.Body.String(), "ending in 0123") || strings.Contains(options.Body.String(), "+447700900123") {
				t.Fatal("expected only a masked phone number")
			}
			form := url.Values{"delivery": {choice}, "phone": {"+447700900999"}}
			sender := s.demoOTPSend
			s.demoOTPSend = nil
			want(t, request("POST", "/gov/security-code-delivery", form), 503)
			if s.demoSessions[hash(cookie.Value)].Challenge != "" {
				t.Fatal("unavailable delivery must not issue a challenge")
			}
			s.demoOTPSend = sender
			want(t, request("POST", "/gov/security-code-delivery", form), 303)
			want(t, request("POST", "/gov/security-code-delivery", form), 429)
			w := request("POST", "/gov/security-code", url.Values{"verificationCode": {code}})
			want(t, w, 303)
			if w.Header().Get("Location") != "/gov/status" || sends != 1 || len(s.store.snapshot().Outbox) != 0 {
				t.Fatal("expected one SMS, verified status and no email")
			}
			if s.demoSessions[hash(cookie.Value)].Challenge != "" {
				t.Fatal("accepted code should be consumed")
			}
		})
	}
}
