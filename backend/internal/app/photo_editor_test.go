package app

import (
	"bytes"
	"image"
	"image/png"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPortalPersonPhotoTogether(t *testing.T) {
	s, _, _ := fixture(t)
	cookie, csrf := login(t, s)
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 70, 90))); err != nil {
		t.Fatal(err)
	}
	submit := func(path string, data []byte, token string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("csrf", token)
		_ = writer.WriteField("first_name", "Example")
		_ = writer.WriteField("email", "example@example.invalid")
		if data != nil {
			part, err := writer.CreateFormFile("photo", "portrait.png")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = part.Write(data)
		}
		_ = writer.Close()
		r := httptest.NewRequest("POST", path, &body)
		r.Header.Set("Content-Type", writer.FormDataContentType())
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	want(t, submit("/admin/people/new", picture.Bytes(), "wrong"), 403)
	if len(s.store.snapshot().People) != 0 {
		t.Fatal("CSRF request wrote a record")
	}
	w := submit("/admin/people/new", []byte("not an image"), csrf)
	if !strings.Contains(w.Body.String(), "Upload a JPEG or PNG") {
		t.Fatal("missing upload error")
	}
	if len(s.store.snapshot().People) != 0 {
		t.Fatal("invalid image created a partial record")
	}
	w = submit("/admin/people/new", picture.Bytes(), csrf)
	want(t, w, 303)
	path := strings.Split(w.Header().Get("Location"), "?")[0]
	id := strings.TrimPrefix(path, "/admin/people/")
	db := s.store.snapshot()
	if !db.People[id].HasPhoto || len(db.Photos[id].Data) == 0 {
		t.Fatal("missing atomic photo save")
	}
	before := append([]byte(nil), db.Photos[id].Data...)
	want(t, submit(path, nil, csrf), 303)
	if !bytes.Equal(before, s.store.snapshot().Photos[id].Data) {
		t.Fatal("no-upload edit lost photo")
	}
	_ = submit(path, []byte("invalid replacement"), csrf)
	if !bytes.Equal(before, s.store.snapshot().Photos[id].Data) {
		t.Fatal("invalid upload replaced photo")
	}
	r := httptest.NewRequest("GET", "/admin/people/new", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "data-photo-controls") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatal("missing crop editor")
	}
	want(t, call(s, "GET", "/admin/assets/photo-editor.js", "", false), 303)
}
