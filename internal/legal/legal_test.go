// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package legal

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func newMux(t *testing.T) *http.ServeMux {
	t.Helper()
	dist := fstest.MapFS{}
	for _, lang := range Languages {
		for _, page := range Pages {
			dist[lang+"/"+page+"/index.html"] = &fstest.MapFile{Data: []byte("<html>" + lang + " " + page + "</html>")}
		}
	}
	mux := http.NewServeMux()
	if err := Register(mux, dist); err != nil {
		t.Fatal(err)
	}
	// The site's catch all, so a path the set does not own is visible as 404.
	mux.Handle("/", http.NotFoundHandler())
	return mux
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
	if rec := get(t, mux, http.MethodHead, "/de/imprint", nil); rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("HEAD = %d with %d bytes", rec.Code, rec.Body.Len())
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
