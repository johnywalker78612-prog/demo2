package app

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestAdminRecordLookup(t *testing.T) {
	s, _, _ := fixture(t)
	p := create(t, s)
	body := fmt.Sprintf(`{"id":%q,"date_of_birth":%q}`, p.ID, p.DateOfBirth)
	want(t, call(s, "POST", "/api/v1/people/lookup", body, false), 401)
	w := call(s, "POST", "/api/v1/people/lookup", body, true)
	want(t, w, 200)
	var got Person
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != p.ID || got.Email != p.Email {
		t.Fatal("wrong record returned")
	}
	for _, input := range []string{`{}`, `{"id":"x"}`, `{"date_of_birth":"1990-03-09"}`, `{"id":"x","date_of_birth":"2024-02-30"}`, `{"id":"x","date_of_birth":"2999-01-01"}`} {
		want(t, call(s, "POST", "/api/v1/people/lookup", input, true), 422)
	}
	wrong := call(s, "POST", "/api/v1/people/lookup", fmt.Sprintf(`{"id":%q,"date_of_birth":"1991-03-09"}`, p.ID), true)
	missing := call(s, "POST", "/api/v1/people/lookup", `{"id":"missing","date_of_birth":"1990-03-09"}`, true)
	want(t, wrong, 404)
	want(t, missing, 404)
	if wrong.Body.String() != missing.Body.String() {
		t.Fatal("mismatch should not disclose record existence")
	}
	want(t, call(s, "DELETE", "/api/v1/people/"+p.ID, "", true), 204)
	want(t, call(s, "POST", "/api/v1/people/lookup", body, true), 404)
}
