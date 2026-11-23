package app

import (
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"private-records/web"
	"strings"
)

// PageRoutes is the single routing registry for the static demo. Add future
// pages here and place their files below web/pages. HTML routes are GET-only;
// the separate /api/v1 endpoints continue to handle private-records operations.
var PageRoutes = []struct{ Path, File string }{
	{"/", "home.html"},
	{"/visa", "visa.html"},
	{"/evisa/view-evisa-get-share-code-prove-immigration-status", "evisa-share-code.html"},
	{"/gov/sign-in", "demo/sign-in.html"},
	{"/gov/passport", "demo/passport.html"},
	{"/gov/date-of-birth", "demo/date-of-birth.html"},
	{"/gov/security-code-delivery", "demo/security-code-delivery.html"},
	{"/gov/security-code", "demo/security-code.html"},
	{"/gov/status", "demo/status.html"},
	{"/gov/security-code-options", "demo/security-code-options.html"},
	{"/gov/share-introduction", "demo/share-introduction.html"},
	{"/gov/share-purpose", "demo/share-purpose.html"},
	{"/gov/share-preview", "demo/share-preview.html"},
	{"/gov/share-result", "demo/share-result.html"},
	{"/gov/new/verification", "demo/new-verification.html"},
	{"/gov/new/status", "demo/new-status.html"},
	{"/gov/new/share-introduction", "demo/new-share-introduction.html"},
	{"/gov/new/share-purpose", "demo/new-share-purpose.html"},
	{"/gov/new/share-preview", "demo/new-share-preview.html"},
	{"/gov/new/share-result", "demo/new-share-result.html"},
}

const demoCSP = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'none'; form-action 'none'; base-uri 'none'; object-src 'none'; frame-src 'none'; frame-ancestors 'none'"

func (s *Server) websiteRoutes() {
	s.mux.HandleFunc("/gov.uk/", func(w http.ResponseWriter, r *http.Request) {
		target := "/gov/" + strings.TrimPrefix(r.URL.Path, "/gov.uk/")
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})
	s.mux.HandleFunc("/demo/", func(w http.ResponseWriter, r *http.Request) {
		target := "/gov/" + strings.TrimPrefix(r.URL.Path, "/demo/")
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})
	for _, path := range []string{"/gov/passport", "/gov/date-of-birth", "/gov/security-code-delivery", "/gov/security-code", "/gov/status", "/gov/share-preview", "/gov/share-result", "/gov/photo", "/gov/status/pdf", "/gov/share-preview/pdf", "/gov/share-result/pdf"} {
		s.mux.HandleFunc("GET "+path, s.demoFlow)
		if path != "/gov/photo" && !strings.HasSuffix(path, "/pdf") {
			s.mux.HandleFunc("POST "+path, s.demoFlow)
		}
	}
	for i, page := range PageRoutes {
		if pathIsDemoFlow(page.Path) {
			continue
		}
		content, err := web.Files.ReadFile("pages/" + page.File)
		if err != nil {
			panic(fmt.Sprintf("missing website page %s: %v", page.File, err))
		}
		pattern := "GET " + page.Path
		if page.Path == "/" {
			pattern = "GET /{$}"
		}
		s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Security-Policy", demoCSP)
			w.Header().Set("X-Robots-Tag", "noindex, nofollow")
			w.WriteHeader(http.StatusOK)
			if r.Method != http.MethodHead {
				_, _ = w.Write(content)
			}
		})
		// Preserve old bookmarks, but discard any historical form/query values.
		target := page.Path
		for _, old := range []string{fmt.Sprintf("/pages/page%d.html", i+1), fmt.Sprintf("/page%d.html", i+1)} {
			s.mux.HandleFunc("GET "+old, func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target, http.StatusPermanentRedirect)
			})
		}
		if page.Path != "/" {
			s.mux.HandleFunc("GET "+page.Path+"/{$}", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target, http.StatusPermanentRedirect)
			})
		}
	}
	s.mux.HandleFunc("GET /extra/after_enter_code_1.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/gov/status", http.StatusPermanentRedirect)
	})
	s.mux.HandleFunc("GET /extra/page8.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/gov/security-code-options", http.StatusPermanentRedirect)
	})
	s.mux.HandleFunc("GET /extra/code_opt_1.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/gov/security-code-options", http.StatusPermanentRedirect)
	})
	s.mux.HandleFunc("GET /extra/generate_share_Code3.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/gov/share-introduction", http.StatusPermanentRedirect)
	})
	s.mux.HandleFunc("GET /extra/prove_status_4.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/gov/share-purpose", http.StatusPermanentRedirect)
	})
	s.mux.HandleFunc("GET /extra/page5_det_photo.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/gov/share-preview", http.StatusPermanentRedirect)
	})
	s.mux.HandleFunc("GET /extra/final.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/gov/share-result", http.StatusPermanentRedirect)
	})
	s.mux.HandleFunc("GET /new_pages/1_verification_code_conf.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/gov/new/verification", http.StatusPermanentRedirect)
	})
	s.mux.HandleFunc("GET /new_pages/2_after_code.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/gov/new/status", http.StatusPermanentRedirect)
	})
	s.mux.HandleFunc("GET /new_pages/3_get_share_code_s1.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/gov/new/share-introduction", http.StatusPermanentRedirect)
	})
	s.mux.HandleFunc("GET /new_pages/4_prove.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/gov/new/share-purpose", http.StatusPermanentRedirect)
	})
	s.mux.HandleFunc("GET /new_pages/5_prove_photo.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/gov/new/share-preview", http.StatusPermanentRedirect)
	})
	s.mux.HandleFunc("GET /new_pages/6_final_code_gen.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/gov/new/share-result", http.StatusPermanentRedirect)
	})
	s.mux.HandleFunc("GET /index.html", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/", http.StatusPermanentRedirect) })
	assets, err := fs.Sub(web.Files, "assets")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix("/assets/", http.FileServer(http.FS(assets)))
	s.mux.HandleFunc("GET /assets/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/assets/")
		if name == "" || !fs.ValidPath(name) || path.Clean(name) != name {
			http.NotFound(w, r)
			return
		}
		info, err := fs.Stat(assets, name)
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		// Revalidate unversioned assets rather than retaining stale CSS after deploys.
		w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
		files.ServeHTTP(w, r)
	})
}

func pathIsDemoFlow(path string) bool {
	for _, p := range []string{"/gov/passport", "/gov/date-of-birth", "/gov/security-code-delivery", "/gov/security-code", "/gov/status", "/gov/share-preview", "/gov/share-result"} {
		if path == p {
			return true
		}
	}
	return false
}
