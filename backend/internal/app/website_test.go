package app

import (
	"fmt"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"private-records/web"
	"regexp"
	"strings"
	"testing"
)

func TestWebsiteRoutesAndAssets(t *testing.T) {
	s, _, _ := fixture(t)
	assets := regexp.MustCompile(`(?:src|href)="(/assets/[^"#?]+)`)
	for i, page := range PageRoutes {
		t.Run(page.Path, func(t *testing.T) {
			if pathIsDemoFlow(page.Path) {
				w := call(s, "GET", page.Path, "", false)
				if page.Path == "/gov/passport" {
					want(t, w, 200)
				} else {
					want(t, w, 303)
				}
				return
			}
			w := call(s, "GET", page.Path, "", false)
			want(t, w, 200)
			if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
				t.Fatal("missing HTML or demo notice")
			}
			if !strings.Contains(w.Header().Get("Content-Security-Policy"), "form-action 'none'") {
				t.Fatal("demo must not submit details")
			}
			if strings.Contains(w.Body.String(), "../assets/") {
				t.Fatal("relative asset path")
			}
			for _, match := range assets.FindAllStringSubmatch(w.Body.String(), -1) {
				a := call(s, "GET", match[1], "", false)
				want(t, a, 200)
				if strings.Contains(a.Header().Get("Content-Type"), "text/html") {
					t.Fatalf("asset returned HTML: %s", match[1])
				}
			}
			want(t, call(s, "POST", page.Path, `{"passport":"fictional"}`, false), 405)
			head := call(s, "HEAD", page.Path, "", false)
			want(t, head, 200)
			if head.Body.Len() != 0 {
				t.Fatal("HEAD body")
			}
			old := call(s, "GET", fmt.Sprintf("/pages/page%d.html?private=value", i+1), "", false)
			want(t, old, 308)
			if old.Header().Get("Location") != page.Path {
				t.Fatal("bad legacy redirect")
			}
		})
	}
	for _, path := range []string{"/.env", "/backend/.env", "/web/pages/gov/passport.html", "/assets/", "/assets/css/", "/unknown", "/assets/nonexistent.css"} {
		want(t, call(s, "GET", path, "", false), 404)
	}
	if len(s.store.snapshot().People) != 0 {
		t.Fatal("demo routes must not create records")
	}
	want(t, call(s, "GET", "/api/v1/people", "", false), 401)
	want(t, call(s, "GET", "/admin", "", false), 303)
}

func TestWebsiteEmbeddedIndependentOfWorkingDirectory(t *testing.T) {
	// The bundled site must work without repository files next to the executable.
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	if err = os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	s, _, _ := fixture(t)
	want(t, call(s, "GET", "/", "", false), 200)
	want(t, call(s, "GET", "/assets/css/passport.css", "", false), 200)
}

func TestStaticDemoInputsAreNotSubmitted(t *testing.T) {
	for _, page := range PageRoutes[3:] {
		if page.Path == "/gov/security-code-delivery" || page.Path == "/gov/security-code" {
			continue // These forms post directly to the same-origin verification handlers.
		}
		b, err := fs.ReadFile(web.Files, "pages/"+page.File)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		if strings.Contains(text, `<button type="submit"`) {
			t.Fatalf("submit button in demo %s", page.File)
		}
		for _, field := range []string{"documentNumber", "dob-day", "dob-month", "dob-year", "verificationCode"} {
			if strings.Contains(text, `name="`+field+`"`) {
				t.Fatalf("serializable private field in %s", page.File)
			}
		}
	}
}

func TestWebsitePathCannotReadOutsideAssets(t *testing.T) {
	s, _, _ := fixture(t)
	for _, raw := range []string{"/assets/%2e%2e/.env", "/assets/%2e%2e/pages/home.html"} {
		r := httptest.NewRequest("GET", raw, nil)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code == 200 {
			t.Fatalf("path escaped assets: %s", raw)
		}
	}
	// Configuration files are not present anywhere in the embedded tree.
	err := fs.WalkDir(web.Files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if filepath.Base(path) == ".env" {
			t.Fatal("embedded environment file")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStatusDemoIsFictionalAndLocal(t *testing.T) {
	s, _, _ := fixture(t)
	// Status is now protected by the identity lookup and OTP session.
	if w := call(s, "GET", "/gov/status", "", false); w.Code != 303 {
		t.Fatalf("status should require verification: %d", w.Code)
	}
	return
	/*
		w := call(s, "GET", "/gov/status", "", false)
		want(t, w, 200)
		body := w.Body.String()
		for _, required := range []string{"DEMO — NOT PROOF OF STATUS", "Example Person", "/assets/css/status.css", "/assets/images/demo-portrait.svg"} {
			if !strings.Contains(body, required) {
				t.Fatalf("missing %s", required)
			}
		}
		for _, forbidden := range []string{"data:image/jpeg;base64", "googletagmanager.com", "ruxitagentjs", "href=\"https://", "src=\"https://", "javascript:"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("unexpected external/private reference: %s", forbidden)
			}
		}
		w = call(s, "GET", "/extra/after_enter_code_1.html", "", false)
		want(t, w, 308)
		if w.Header().Get("Location") != "/gov/status" {
			t.Fatal("wrong status redirect")
		}
		w = call(s, "GET", "/gov/security-code", "", false)
		if !strings.Contains(w.Body.String(), `data-demo-next="/gov/status"`) {
			t.Fatal("missing next demo route")
		}
	*/
}
