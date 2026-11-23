package app

import (
	"crypto/rand"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestSplitNamesCRUDSearchAndSharing(t *testing.T) {
	s, _, _ := fixture(t)
	want(t, call(s, "POST", "/api/v1/people", `{"last_name":"Record"}`, true), 422)
	want(t, call(s, "POST", "/api/v1/people", `{"name":"Legacy input"}`, true), 422)
	w := call(s, "POST", "/api/v1/people", `{"first_name":"  Example  ","last_name":"  Multi Part  "}`, true)
	want(t, w, 201)
	var p Person
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.FirstName != "Example" || p.LastName != "Multi Part" || p.FullName() != "Example Multi Part" {
		t.Fatal("incorrect split names", p)
	}
	var payload map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &payload)
	if _, found := payload["name"]; found {
		t.Fatal("legacy name returned in API")
	}
	for _, query := range []string{"example", "MULTI PART", "Example Multi"} {
		w = call(s, "GET", "/api/v1/people?q="+url.QueryEscape(query), "", true)
		want(t, w, 200)
		if !strings.Contains(w.Body.String(), p.ID) {
			t.Fatal("search did not match", query)
		}
	}
	want(t, call(s, "PATCH", "/api/v1/people/"+p.ID, `{"first_name":"Updated"}`, true), 200)
	current := s.store.snapshot().People[p.ID]
	if current.FirstName != "Updated" || current.LastName != "Multi Part" {
		t.Fatal("PATCH changed omitted last name")
	}
	want(t, call(s, "PATCH", "/api/v1/people/"+p.ID, `{"first_name":" "}`, true), 422)
	want(t, call(s, "PATCH", "/api/v1/people/"+p.ID, `{"name":"Old"}`, true), 422)
	_, token, err := s.newShare(p.ID, 24)
	if err != nil {
		t.Fatal(err)
	}
	w = call(s, "GET", "/api/v1/shared/"+token, "", false)
	want(t, w, 200)
	payload = map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &payload)
	if payload["first_name"] != "Updated" || payload["last_name"] != "Multi Part" {
		t.Fatal("share did not use split fields")
	}
	if _, found := payload["name"]; found {
		t.Fatal("share returned legacy field")
	}
	want(t, call(s, "PATCH", "/api/v1/people/"+p.ID, `{"last_name":""}`, true), 200)
	current = s.store.snapshot().People[p.ID]
	if current.FullName() != "Updated" {
		t.Fatal("single-name display has extra whitespace")
	}
}

func TestLegacyEncryptedNameMigration(t *testing.T) {
	s, path, key := fixture(t)
	legacy := `{"people":{"person_legacy":{"id":"person_legacy","name":"Example Multi Part","email":"example@example.invalid","passport_number":"DEMO-ONLY","unverified":true}},"photos":{},"shares":{},"share_hashes":{},"verifications":{},"outbox":{},"audit":[]}`
	nonce := make([]byte, s.store.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	encrypted := s.store.aead.Seal(nonce, nonce, []byte(legacy), []byte("private-records-v1"))
	if err := os.WriteFile(path, encrypted, 0600); err != nil {
		t.Fatal(err)
	}
	migrated, err := OpenStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	p := migrated.snapshot().People["person_legacy"]
	if p.FirstName != "Example Multi Part" || p.LastName != "" || p.Email != "example@example.invalid" || p.PassportNumber != "DEMO-ONLY" {
		t.Fatal("legacy data was lost or names guessed")
	}
	s.store = migrated
	if _, err = s.savePerson(p.ID, map[string]string{"first_name": "Example", "last_name": "Multi Part"}); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	p = reopened.snapshot().People[p.ID]
	if p.FirstName != "Example" || p.LastName != "Multi Part" {
		t.Fatal("new names not persisted")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := s.store.aead.Open(nil, data[:s.store.aead.NonceSize()], data[s.store.aead.NonceSize():], []byte("private-records-v1"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), `"name":`) {
		t.Fatal("legacy field persisted after migration")
	}
}

func TestExplicitSplitNamesTakePrecedence(t *testing.T) {
	for _, input := range []string{`{"name":"Legacy","first_name":"Current","last_name":"Record"}`, `{"name":"Legacy","first_name":"","last_name":"Record"}`} {
		var p Person
		if err := json.Unmarshal([]byte(input), &p); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(p.FullName(), "Legacy") {
			t.Fatal("legacy name overrode explicit fields")
		}
	}
}

func TestPortalSplitNameFields(t *testing.T) {
	s, _, _ := fixture(t)
	cookie, csrf := login(t, s)
	r := httptest.NewRequest("GET", "/admin/people/new", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	want(t, w, 200)
	for _, label := range []string{`for="first_name">First name`, `for="last_name">Last name`} {
		if !strings.Contains(w.Body.String(), label) {
			t.Fatal("missing portal field", label)
		}
	}
	form := url.Values{"first_name": {"Example"}, "last_name": {"Record"}, "csrf": {csrf}}
	r = httptest.NewRequest("POST", "/admin/people/new", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	want(t, w, 303)
	personPath := w.Header().Get("Location")
	for _, path := range []string{personPath, personPath + "/delete", "/admin/people?q=Example+Record"} {
		r = httptest.NewRequest("GET", path, nil)
		r.AddCookie(cookie)
		w = httptest.NewRecorder()
		s.ServeHTTP(w, r)
		want(t, w, 200)
		if !strings.Contains(w.Body.String(), "Example Record") {
			t.Fatal("full name missing on portal page", path)
		}
	}
}
