package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestDiscardsClientNetworkMetadata(t *testing.T) {
	headers := []string{"Forwarded", "X-Forwarded-For", "X-Real-IP", "CF-Connecting-IP", "True-Client-IP", "Client-IP", "X-Client-IP", "Fastly-Client-IP"}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.RemoteAddr != "" {
			t.Error("peer address reached the handler")
		}
		for _, header := range headers {
			if r.Header.Get(header) != "" {
				t.Errorf("%s reached the handler", header)
			}
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("authentication header was lost")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s := &Server{mux: mux}
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.RemoteAddr = "192.0.2.1:1234"
	for _, header := range headers {
		r.Header.Set(header, "192.0.2.1")
	}
	r.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("unexpected response or missing referrer protection")
	}
}
