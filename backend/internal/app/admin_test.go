package app

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestReportingCalendarBoundariesAndDeletedPeople(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, reportLocation)
	boundary := time.Date(2026, 11, 1, 0, 0, 0, 0, reportLocation)
	d := database{People: map[string]Person{"legacy": {ID: "legacy", CreatedAt: boundary.AddDate(0, -2, 0)}, "current": {ID: "current", CreatedAt: boundary}}}
	d.Audit = []AuditEvent{
		{Action: "person.created", ResourceID: "current", At: boundary},
		{Action: "person.created", ResourceID: "current", At: boundary}, // Duplicate event must not inflate additions.
		{Action: "person.created", ResourceID: "deleted", At: boundary.Add(-time.Second)},
		{Action: "person.deleted", ResourceID: "deleted", At: boundary.Add(time.Hour)},
		{Action: "person.updated", ResourceID: "current", At: now},
		{Action: "person.created", ResourceID: "older", At: boundary.AddDate(0, -5, 0)},
		{Action: "person.created", ResourceID: "year", At: boundary.AddDate(0, -11, 0)},
		{Action: "person.created", ResourceID: "excluded", At: boundary.AddDate(0, -12, 0)},
		{Action: "person.created", ResourceID: "future", At: now.Add(time.Hour)},
	}
	r := makeReport(d, now, 3)
	if r.CurrentTotal != 2 || r.ThisMonth != 1 || r.ThreeMonths != 3 || r.SixMonths != 4 || r.TwelveMonths != 5 || r.SelectedAdded != 3 || r.SelectedDeleted != 1 || len(r.Monthly) != 3 {
		t.Fatalf("incorrect report: %+v", r)
	}
	if r.Monthly[1].Added != 1 || r.Monthly[2].Added != 1 || r.Monthly[2].Deleted != 1 {
		t.Fatalf("incorrect boundary: %+v", r.Monthly)
	}
	if r.Timezone != "Asia/Kolkata" || r.From != "01 Sep 2026" {
		t.Fatalf("wrong calendar: %+v", r)
	}
	// January must include December of the previous year.
	r = makeReport(d, time.Date(2027, 1, 2, 0, 0, 0, 0, reportLocation), 3)
	if r.From != "01 Nov 2026" || r.Monthly[1].Label != "Dec 2026" {
		t.Fatal(r)
	}
}
func TestPeopleFilteringSortingAndPaging(t *testing.T) {
	d := database{People: map[string]Person{}}
	for i := 0; i < 31; i++ {
		id := fmt.Sprintf("p%02d", i)
		d.People[id] = Person{ID: id, FirstName: fmt.Sprintf("Example %02d", i), Email: fmt.Sprintf("example%02d@example.invalid", i), PassportNumber: fmt.Sprintf("TEST%02d", i), PhoneNumber: fmt.Sprintf("+1 (202) 555-%04d", 100+i), Nationality: "Example", CreatedAt: time.Now().Add(time.Duration(i) * time.Second)}
	}
	for _, query := range []string{"q=EXAMPLE00%40example.invalid&field=email", "q=TEST00&field=passport", "q=12025550100&field=phone", "q=Example+00&field=name"} {
		v, _ := url.ParseQuery(query)
		l := queryPeople(d, v)
		if l.Total != 1 || l.Items[0].ID != "p00" {
			t.Fatalf("search failed %s: %+v", query, l)
		}
	}
	v, _ := url.ParseQuery("sort=name&order=asc&page=2&nationality=Example")
	l := queryPeople(d, v)
	if l.Total != 31 || len(l.Items) != 6 || l.Start != 26 || l.Items[0].ID != "p25" || l.Previous == "" || l.Next != "" {
		t.Fatalf("pagination: %+v", l)
	}
	v.Set("page", "99999")
	if queryPeople(d, v).Page != 2 {
		t.Fatal("page not clamped")
	}
	v.Set("q", "no matches")
	l = queryPeople(d, v)
	if l.Total != 0 || l.Start != 0 || l.Page != 1 {
		t.Fatalf("empty results: %+v", l)
	}
	v = url.Values{"sort": {"name"}, "order": {"desc"}, "verification": {"verified"}}
	if queryPeople(d, v).Total != 0 {
		t.Fatal("email verification filter")
	}
	v.Del("verification")
	if queryPeople(d, v).Items[0].ID != "p30" {
		t.Fatal("descending order")
	}
}
func TestPhoneCRUDAndPrivateReports(t *testing.T) {
	s, _, _ := fixture(t)
	p := create(t, s)
	want(t, call(s, "PATCH", "/api/v1/people/"+p.ID, `{"phone_number":"invalid"}`, true), 422)
	want(t, call(s, "PATCH", "/api/v1/people/"+p.ID, `{"phone_number":"+1 (202) 555-0101"}`, true), 200)
	w := call(s, "GET", "/api/v1/people?q=12025550101&field=phone", "", true)
	want(t, w, 200)
	if !strings.Contains(w.Body.String(), p.ID) {
		t.Fatal("phone search not wired")
	}
	w = call(s, "GET", "/api/v1/people/"+p.ID, "", true)
	var got Person
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.PhoneNumber != "+1 (202) 555-0101" {
		t.Fatal(got.PhoneNumber)
	}
	want(t, call(s, "GET", "/api/v1/reports", "", false), 401)
	w = call(s, "GET", "/api/v1/reports?months=6", "", true)
	want(t, w, 200)
	var report Report
	_ = json.Unmarshal(w.Body.Bytes(), &report)
	if report.CurrentTotal != 1 || report.SelectedMonths != 6 {
		t.Fatal(report)
	}
	_, token, err := s.newShare(p.ID, 24)
	if err != nil {
		t.Fatal(err)
	}
	w = call(s, "GET", "/api/v1/shared/"+token, "", false)
	if strings.Contains(w.Body.String(), "phone_number") {
		t.Fatal("phone leaked in public share")
	}
}
func TestAdminIdentifierAndLogout(t *testing.T) {
	s, _, _ := fixture(t)
	s.cfg.AdminEmail = "owner@example.invalid"
	for _, identifier := range []string{"ADMIN", "owner@EXAMPLE.invalid", "wrong@example.invalid", ""} {
		w := call(s, "GET", "/admin/login", "", false)
		nonce := w.Result().Cookies()[0]
		form := url.Values{"identifier": {identifier}, "password": {testPassword}, "csrf": {nonce.Value}}
		r := httptest.NewRequest("POST", "/admin/login", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(nonce)
		w = httptest.NewRecorder()
		s.ServeHTTP(w, r)
		expected := 303
		if identifier == "wrong@example.invalid" || identifier == "" {
			expected = 401
		}
		want(t, w, expected)
	}
	cookie, csrf := login(t, s)
	for _, path := range []string{"/admin", "/admin/people", "/admin/people/new", "/admin/audit", "/admin/outbox"} {
		r := httptest.NewRequest("GET", path, nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		want(t, w, 200)
	}
	r := httptest.NewRequest("POST", "/admin/logout", strings.NewReader(url.Values{"csrf": {csrf}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	want(t, w, 303)
	r = httptest.NewRequest("GET", "/admin/people", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	want(t, w, 303)
	if w.Header().Get("Location") != "/admin/login" {
		t.Fatal("session remained valid")
	}
}

func TestInvalidPortalEditPreservesDraft(t *testing.T) {
	s, _, _ := fixture(t)
	p := create(t, s)
	cookie, csrf := login(t, s)
	form := url.Values{"csrf": {csrf}, "first_name": {"Edited Example"}, "last_name": {"Draft"}, "email": {"draft@example.invalid"}, "phone_number": {"bad phone"}, "nationality": {"Draft nationality"}}
	r := httptest.NewRequest("POST", "/admin/people/"+p.ID, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	want(t, w, 200)
	for _, value := range []string{"Edited Example", "draft@example.invalid", "bad phone", "Draft nationality"} {
		if !strings.Contains(w.Body.String(), `value="`+value+`"`) {
			t.Fatalf("draft lost %s", value)
		}
	}
	if got := s.store.snapshot().People[p.ID]; got.FirstName != p.FirstName || got.PhoneNumber != "" {
		t.Fatal("invalid draft was saved")
	}
}
