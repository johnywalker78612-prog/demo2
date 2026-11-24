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

func TestDemoUsesSavedContactForSelectedChannel(t *testing.T) {
	for _, tc := range []struct {
		name, choice, email, phone string
		local                      bool
	}{
		{"email only", "email", "example@example.test", "", false},
		{"phone only in UK local format", "sms", "", "07700 900123", false},
		{"email with UK personal number", "email", "example@example.test", "07000 900123", false},
		{"both choose email", "email", "example@example.test", "+44 7700 900123", false},
		{"both choose SMS", "sms", "example@example.test", "+44 7700 900123", false},
		{"local email outbox", "email", "example@example.test", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			choice := tc.choice
			s, _, _ := fixture(t)
			p := create(t, s)
			if err := s.store.update(func(d *database) error {
				p.PhoneNumber = tc.phone
				p.Email = tc.email
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
			s.mailSend = func(_ SMTPConfig, message Message) error {
				sends++
				if message.To != tc.email {
					t.Fatal("unexpected email recipient")
				}
				code = strings.TrimSuffix(strings.Fields(message.Body)[4], ".")
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
			body := options.Body.String()
			if strings.Contains(body, "phone ending in 0123") != (tc.phone != "" && !strings.HasPrefix(tc.phone, "070")) ||
				strings.Contains(body, "ex***le@example.test") != (tc.email != "") ||
				strings.Contains(body, "+447700900123") || strings.Contains(body, "example@example.test") {
				t.Fatal("expected only the saved, masked options")
			}
			form := url.Values{"delivery": {choice}, "phone": {"+447700900999"}}
			sender := s.demoOTPSend
			s.demoOTPSend = nil
			want(t, request("POST", "/gov/security-code-delivery", form), 303)
			if s.demoSessions[hash(cookie.Value)].Challenge != "" {
				t.Fatal("unavailable delivery must not issue a challenge")
			}
			s.demoOTPSend = sender
			if tc.local {
				s.cfg.LocalOTPOutbox = true
				form = nil // Reproduce the empty local delivery POST.
				page := request("GET", "/gov/security-code-delivery", nil).Body.String()
				if !strings.Contains(page, `value="email" required checked`) {
					t.Fatal("email should be preselected in local mode")
				}
			} else {
				s.cfg.SMTP.Host = "mock.example.test"
			}
			want(t, request("POST", "/gov/security-code-delivery", form), 303)
			want(t, request("POST", "/gov/security-code-delivery", form), 429)
			outbox := s.store.snapshot().Outbox
			if tc.local {
				if len(outbox) != 1 || sends != 0 ||
					!strings.Contains(request("GET", "/gov/security-code", nil).Body.String(), "No email was sent") {
					t.Fatal("expected local outbox delivery")
				}
				for _, message := range outbox {
					if !message.LocalOnly || message.To != tc.email {
						t.Fatal("unexpected local message")
					}
					code = strings.TrimSuffix(strings.Fields(message.Body)[5], ".")
				}
			} else if sends != 1 || len(outbox) != 0 {
				t.Fatal("expected one external delivery and no outbox message")
			}
			w := request("POST", "/gov/security-code", url.Values{"verificationCode": {code}})
			want(t, w, 303)
			if w.Header().Get("Location") != "/gov/status" {
				t.Fatal("expected successful verification and a redirect to status")
			}
			if s.demoSessions[hash(cookie.Value)].Challenge != "" {
				t.Fatal("accepted code should be consumed")
			}
		})
	}
}

func TestAdminOTPIsRecordBoundAndSingleUse(t *testing.T) {
	s, _, _ := fixture(t)
	person := create(t, s)
	code, err := s.issueAdminOTP(person.ID)
	if err != nil {
		t.Fatal(err)
	}
	otp := s.store.snapshot().AdminOTPs[person.ID]
	if remaining := time.Until(otp.ExpiresAt); remaining > 24*time.Hour || remaining < 24*time.Hour-time.Minute {
		t.Fatalf("unexpected lifetime: %s", remaining)
	}
	if ok, _ := s.verifyAdminOTP("other-record", code); ok {
		t.Fatal("code accepted for another record")
	}
	if ok, err := s.verifyAdminOTP(person.ID, code); !ok || err != nil {
		t.Fatalf("issued code rejected: %v", err)
	}
	if ok, _ := s.verifyAdminOTP(person.ID, code); ok {
		t.Fatal("code accepted twice")
	}
}

func TestAdminCodeCanBeSelectedFromEmailCodeScreen(t *testing.T) {
	s, _, _ := fixture(t)
	s.cfg.LocalOTPOutbox = true
	p := create(t, s)
	code, err := s.issueAdminOTP(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	const token = "admin-code-test-session"
	emailSession := demoSession{
		PersonID: p.ID, Expires: time.Now().Add(time.Minute),
		CodeChannel: "email", CodeRecipient: p.Email,
		Challenge: "email challenge", CodeExpires: time.Now().Add(time.Minute),
	}
	s.demoSessions[hash(token)] = emailSession
	request := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "demo_session", Value: token})
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	options := request("GET", "/gov/security-code-delivery", nil)
	want(t, options, 200)
	if !strings.Contains(options.Body.String(), `value="email" required checked`) {
		t.Fatal("saved email should be preselected")
	}
	page := request("GET", "/gov/security-code", nil)
	want(t, page, 200)
	if !strings.Contains(page.Body.String(), "Use a code from your administrator") {
		t.Fatal("email screen has no switch")
	}
	direct := request("POST", "/gov/security-code", url.Values{"verificationCode": {code}})
	want(t, direct, 303)
	if direct.Header().Get("Location") != "/gov/status" {
		t.Fatal("administrator code did not verify directly on the local email screen")
	}
	if _, ok := s.store.snapshot().AdminOTPs[p.ID]; ok {
		t.Fatal("email screen did not consume administrator code")
	}
	s.demoSessions[hash(token)] = emailSession
	code, err = s.issueAdminOTP(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	want(t, request("POST", "/gov/security-code-delivery", url.Values{"delivery": {"admin"}}), 303)
	verified := request("POST", "/gov/security-code", url.Values{"verificationCode": {code}})
	want(t, verified, 303)
	if verified.Header().Get("Location") != "/gov/status" {
		t.Fatal("administrator code did not verify")
	}
}

func TestManualCodeWithoutDeliveryProvider(t *testing.T) {
	s, _, _ := fixture(t)
	p := create(t, s)
	const token = "manual-code-session"
	s.demoSessions[hash(token)] = demoSession{PersonID: p.ID, Expires: time.Now().Add(time.Hour)}
	request := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "demo_session", Value: token})
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	options := request("GET", "/gov/security-code-delivery", nil)
	want(t, options, 200)
	if !strings.Contains(options.Body.String(), `value="email" required checked`) || strings.Contains(options.Body.String(), "delivery unavailable") || strings.Contains(options.Body.String(), p.Email) {
		t.Fatal("expected selectable masked email before admin generates code")
	}
	next := request("POST", "/gov/security-code-delivery", nil)
	want(t, next, 303)
	if next.Header().Get("Location") != "/gov/security-code" {
		t.Fatal("wrong destination")
	}
	page := request("GET", "/gov/security-code", nil)
	want(t, page, 200)
	if !strings.Contains(page.Body.String(), maskDemoOTPRecipient("email", p.Email)) || strings.Contains(page.Body.String(), "administrator-provided") || strings.Contains(page.Body.String(), "Administrator codes expire") {
		t.Fatal("missing masked address or manual instructions")
	}
	request("POST", "/gov/security-code", url.Values{"verificationCode": {"123456"}})
	if s.demoSessions[hash(token)].Verified {
		t.Fatal("accepted code before issuance")
	}
	code, err := s.issueAdminOTP(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	verified := request("POST", "/gov/security-code", url.Values{"verificationCode": {code}})
	want(t, verified, 303)
	if verified.Header().Get("Location") != "/gov/status" || !s.demoSessions[hash(token)].Verified {
		t.Fatal("manual code did not verify")
	}
	if ok, _ := s.verifyAdminOTP(p.ID, code); ok {
		t.Fatal("code reused")
	}
	if len(s.store.snapshot().Outbox) != 0 {
		t.Fatal("manual mode created email")
	}
}
