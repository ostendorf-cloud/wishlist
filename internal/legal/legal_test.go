// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package legal

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ostendorf-cloud/govue/middleware"
	"github.com/ostendorf-cloud/govue/themeboot"
)

// newMux mounts the pages behind govue's security middleware, which puts the
// nonce of the response into the request context like the public handler does.
func newMux(t *testing.T) http.Handler {
	t.Helper()
	dist := fstest.MapFS{}
	for _, lang := range Languages {
		for _, page := range Pages {
			dist[lang+"/"+page+"/index.html"] = &fstest.MapFile{Data: []byte("<!doctype html><html><head><title>t</title></head><body>" + lang + " " + page + "</body></html>")}
		}
	}
	mux := http.NewServeMux()
	if err := Register(mux, dist); err != nil {
		t.Fatal(err)
	}
	// The site's catch all, so a path the set does not own is visible as 404.
	mux.Handle("/", http.NotFoundHandler())
	return middleware.DefaultSecurity().Middleware(mux)
}

func get(t *testing.T, mux http.Handler, method, target string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestRedirects(t *testing.T) {
	mux := newMux(t)
	german := map[string]string{"Accept-Language": "en", "Cookie": "govue-language=en"}
	cases := []struct {
		path   string
		header map[string]string
		want   string
	}{
		{"/imprint", map[string]string{"Accept-Language": "en"}, "/en/imprint"},
		{"/privacy", map[string]string{"Accept-Language": "de"}, "/de/privacy"},
		{"/privacy", nil, "/de/privacy"},
		{"/impressum", german, "/de/imprint"},
		{"/datenschutz", german, "/de/privacy"},
		{"/datenschutzerklaerung", german, "/de/privacy"},
	}
	for _, tc := range cases {
		rec := get(t, mux, http.MethodGet, tc.path, tc.header)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != tc.want {
			t.Errorf("%s = %d %q, want 302 %q", tc.path, rec.Code, rec.Header().Get("Location"), tc.want)
		}
	}
}

func TestThereAreNoTermsRoutes(t *testing.T) {
	mux := newMux(t)
	for _, path := range []string{"/terms", "/agb", "/de/terms", "/en/terms"} {
		if rec := get(t, mux, http.MethodGet, path, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, rec.Code)
		}
	}
}

func TestThePagesAreServedFromTheBuild(t *testing.T) {
	mux := newMux(t)
	for _, lang := range Languages {
		for _, page := range Pages {
			rec := get(t, mux, http.MethodGet, "/"+lang+"/"+page, nil)
			want := lang + " " + page
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
				t.Errorf("/%s/%s = %d %q", lang, page, rec.Code, rec.Body)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Errorf("/%s/%s Content-Type = %q", lang, page, ct)
			}
		}
	}
	rec := get(t, mux, http.MethodHead, "/de/imprint", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("HEAD = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("HEAD Cache-Control = %q, want no-store", got)
	}
}

var nonceInCSP = regexp.MustCompile(`script-src [^;]*'nonce-([A-Za-z0-9+/=_-]{16,})'`)

func TestEveryPageCarriesTheBootScriptWithTheNonceOfItsResponse(t *testing.T) {
	mux := newMux(t)
	seen := map[string]bool{}
	for _, lang := range Languages {
		for _, page := range Pages {
			target := "/" + lang + "/" + page
			rec := get(t, mux, http.MethodGet, target, nil)
			m := nonceInCSP.FindStringSubmatch(rec.Header().Get("Content-Security-Policy"))
			if m == nil {
				t.Fatalf("%s: no script nonce in %q", target, rec.Header().Get("Content-Security-Policy"))
			}
			nonce := m[1]
			if seen[nonce] {
				t.Errorf("%s: nonce %q repeats", target, nonce)
			}
			seen[nonce] = true
			body := rec.Body.String()
			if want := `<script nonce="` + nonce + `">`; !strings.Contains(body, want) {
				t.Errorf("%s: no %s in %q", target, want, body)
			}
			if !strings.Contains(body, themeboot.StorageKey) {
				t.Errorf("%s: the boot script does not read %q", target, themeboot.StorageKey)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("%s: Cache-Control = %q, want no-store", target, got)
			}
		}
	}
}

func TestOnlyTheExactPagesExist(t *testing.T) {
	mux := newMux(t)
	for _, path := range []string{
		"/de/imprint/", "/de/imprint/index.html", "/de", "/de/", "/fr/imprint", "/de/Imprint",
		"/de/impressum", "/de/privacy/x", "/en/../de/imprint/x",
	} {
		if rec := get(t, mux, http.MethodGet, path, nil); rec.Code == http.StatusOK {
			t.Errorf("%s = 200, want no page", path)
		}
	}
	if rec := get(t, mux, http.MethodPost, "/de/imprint", nil); rec.Code != http.StatusNotFound {
		t.Errorf("POST = %d, want 404", rec.Code)
	}
}

func TestAMissingBuildFileIsA404(t *testing.T) {
	mux := http.NewServeMux()
	if err := Register(mux, fstest.MapFS{}); err != nil {
		t.Fatal(err)
	}
	mux.Handle("/", http.NotFoundHandler())
	if rec := get(t, mux, http.MethodGet, "/de/imprint", nil); rec.Code != http.StatusNotFound {
		t.Errorf("= %d, want 404", rec.Code)
	}
}
