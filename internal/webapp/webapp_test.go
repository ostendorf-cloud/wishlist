// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package webapp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner"
	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner/ownertest"
)

const sessionToken = "token-of-the-session"

func newHandler(t *testing.T, signedIn bool, expected func(*http.Request) (string, bool)) http.Handler {
	t.Helper()
	h, err := Handler(Config{
		Store: ownertest.New(),
		Owner: func(*http.Request) (int64, bool) { return 1, signedIn },
		Dist: fstest.MapFS{
			"index.html":    {Data: []byte("<html>spa</html>")},
			"assets/app.js": {Data: []byte("js")},
		},
		CSRFExpected: expected,
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func serve(t *testing.T, h http.Handler, method, target, body string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCrossSiteWritesAreRefused(t *testing.T) {
	h := newHandler(t, true, nil)
	json := map[string]string{"Content-Type": "application/json"}

	cases := []struct {
		name   string
		header map[string]string
		want   int
	}{
		{"cross site fetch", map[string]string{"Content-Type": "application/json", "Sec-Fetch-Site": "cross-site"}, 403},
		{"foreign origin", map[string]string{"Content-Type": "application/json", "Origin": "https://evil.example.invalid"}, 403},
		{"same origin fetch", map[string]string{"Content-Type": "application/json", "Sec-Fetch-Site": "same-origin"}, 201},
		{"no browser headers", json, 201},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := serve(t, h, "POST", owner.Prefix+"/lists", `{"title":"x"}`, tc.header)
			if rec.Code != tc.want {
				t.Errorf("= %d %s, want %d", rec.Code, rec.Body, tc.want)
			}
		})
	}
}

// Once the sign in supplies a session, the token has to match as well.
func TestAWriteNeedsTheSessionsTokenWhenThereIsASession(t *testing.T) {
	h := newHandler(t, true, func(*http.Request) (string, bool) { return sessionToken, true })
	body := `{"title":"x"}`
	for name, tc := range map[string]struct {
		token string
		want  int
	}{
		"missing": {"", 403},
		"wrong":   {"other", 403},
		"right":   {sessionToken, 201},
	} {
		t.Run(name, func(t *testing.T) {
			header := map[string]string{"Content-Type": "application/json"}
			if tc.token != "" {
				header["X-CSRF-Token"] = tc.token
			}
			if rec := serve(t, h, "POST", owner.Prefix+"/lists", body, header); rec.Code != tc.want {
				t.Errorf("= %d %s, want %d", rec.Code, rec.Body, tc.want)
			}
		})
	}
	if rec := serve(t, h, "GET", owner.Prefix+"/lists", "", nil); rec.Code != 200 {
		t.Errorf("a read needs no token, got %d", rec.Code)
	}
}

func TestWithoutAnOwnerTheAPIAnswers401InTheErrorShape(t *testing.T) {
	h := newHandler(t, false, nil)
	rec := serve(t, h, "GET", owner.Prefix+"/lists", "", nil)
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), `"unauthenticated"`) {
		t.Errorf("= %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("X-Request-Id") == "" && !strings.Contains(rec.Body.String(), "request_id") {
		t.Error("the request id middleware is not in the chain")
	}
}

func TestUnknownAPIPathsAreJSONAndNeverTheSPA(t *testing.T) {
	h := newHandler(t, true, nil)
	for _, target := range []string{"/api/nothing", "/api/v1/owner/nothing"} {
		rec := serve(t, h, "GET", target, "", nil)
		if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"not_found"`) {
			t.Errorf("%s = %d %s", target, rec.Code, rec.Body)
		}
	}
}

func TestTheSPAIsServedForItsRoutesOnly(t *testing.T) {
	h := newHandler(t, true, nil)
	cases := []struct {
		name   string
		method string
		target string
		want   int
		body   string // a fragment the body must hold, "" for none
		not    string // a fragment the body must not hold
	}{
		{"the root", "GET", "/", 200, "spa", ""},
		{"a list of the SPA", "GET", "/lists/3", 200, "spa", ""},
		{"a new list", "GET", "/lists/new", 200, "spa", ""},
		{"a wish form", "GET", "/wishes/4/edit", 200, "spa", ""},
		{"HEAD of an SPA route", "HEAD", "/lists/3", 200, "", "spa"},
		// vue-router matches case insensitively, so the server does too.
		{"an SPA route in upper case", "GET", "/LISTS/3", 200, "spa", ""},
		{"an SPA route in mixed case", "GET", "/Wishes/4/Edit", 200, "spa", ""},
		{"login in upper case", "GET", "/LOGIN", 200, "spa", ""},
		{"a built file is case sensitive", "GET", "/Assets/app.js", 404, "", "spa"},
		{"upper case does not make a prefix a route", "GET", "/LISTSX", 404, "", "spa"},
		{"an API path in upper case is not the SPA", "GET", "/API/v1/owner/lists", 404, "", "spa"},
		{"a built file", "GET", "/assets/app.js", 200, "js", ""},
		{"a missing asset", "GET", "/assets/x.js", 404, "", "spa"},
		{"a missing file in an SPA route", "GET", "/lists/3.js", 404, "", "spa"},
		{"metrics belong to the admin listener", "GET", "/metrics", 404, "", "spa"},
		{"healthz", "GET", "/healthz", 404, "", "spa"},
		{"readyz", "GET", "/readyz", 404, "", "spa"},
		{"an unknown path", "GET", "/nothing/here", 404, "", "spa"},
		// The mux redirects to the cleaned path, which is then a 404 itself.
		{"dot segments are cleaned by the mux", "GET", "/lists/../metrics", 307, "", "spa"},
		{"a prefix is not a route", "GET", "/listsx", 404, "", "spa"},
		{"a write to an SPA route", "POST", "/lists/3", 404, "", "spa"},
		{"unknown API path is JSON", "GET", "/api/v1/nothing", 404, `"not_found"`, "spa"},
		{"the bare API prefix is JSON", "GET", "/api/", 404, `"not_found"`, "spa"},
		{"an API path with an extension is JSON", "GET", "/api/v1/x.js", 404, `"not_found"`, "spa"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := serve(t, h, tc.method, tc.target, "", nil)
			if rec.Code != tc.want || !strings.Contains(rec.Body.String(), tc.body) ||
				(tc.not != "" && strings.Contains(rec.Body.String(), tc.not)) {
				t.Errorf("%s %s = %d %q", tc.method, tc.target, rec.Code, rec.Body)
			}
		})
	}
	rec := serve(t, h, "GET", "/api/v1/nothing", "", nil)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("unknown API path Content-Type = %q, want JSON", ct)
	}
}

// What scripts/smoke-test-image.sh pins on the public listener, over a real
// listener: /metrics is not here, / is the shell, /index.html is canonicalised.
func TestThePublicListenerAnswersWhatTheSmokeTestExpects(t *testing.T) {
	srv := httptest.NewServer(newHandler(t, false, nil))
	t.Cleanup(srv.Close)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	for path, want := range map[string]int{
		"/metrics":    http.StatusNotFound,
		"/":           http.StatusOK,
		"/index.html": http.StatusMovedPermanently,
	} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", path, resp.StatusCode, want)
		}
	}
}

// Every first path segment of the router in the SPA has to be in spaRoutes,
// or a deep link to it would be a 404 from the server.
func TestSPARoutesCoverTheRouter(t *testing.T) {
	src, err := os.ReadFile("../../web/src/router.ts")
	if err != nil {
		t.Fatal(err)
	}
	found := regexp.MustCompile(`path: '(/[^':]*)`).FindAllStringSubmatch(string(src), -1)
	if len(found) < 5 {
		t.Fatalf("found %d paths in router.ts, the pattern is stale", len(found))
	}
	for _, m := range found {
		route := strings.TrimSuffix(m[1], "/")
		if route == "" {
			continue // the root is served as a file
		}
		if !isSPARoute(route) && !isSPARoute(route+"/x") {
			t.Errorf("router path %q is not in spaRoutes", m[1])
		}
	}
}

// The legal pages are wired through the real handler: redirects, pages from
// the build, and everything around them still a 404.
func TestTheLegalPagesAreWiredOutsideTheRouteChain(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":            {Data: []byte("<html>spa</html>")},
		"de/imprint/index.html": {Data: []byte("<html>de imprint</html>")},
		"de/privacy/index.html": {Data: []byte("<html>de privacy</html>")},
		"en/imprint/index.html": {Data: []byte("<html>en imprint</html>")},
		"en/privacy/index.html": {Data: []byte("<html>en privacy</html>")},
		"de/other/index.html":   {Data: []byte("<html>other</html>")},
		"sitemap.xml":           {Data: []byte("<urlset/>")},
	}
	h, err := Handler(Config{
		Store: ownertest.New(),
		Owner: func(*http.Request) (int64, bool) { return 1, false },
		Dist:  dist,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		target, location, body string
		want                   int
	}{
		{"/imprint", "/de/imprint", "", 302},
		{"/impressum", "/de/imprint", "", 302},
		{"/datenschutz", "/de/privacy", "", 302},
		{"/datenschutzerklaerung", "/de/privacy", "", 302},
		{"/de/imprint", "", "de imprint", 200},
		{"/en/privacy", "", "en privacy", 200},
		{"/sitemap.xml", "", "urlset", 200},
		{"/terms", "", "", 404},
		{"/agb", "", "", 404},
		{"/de/terms", "", "", 404},
		{"/de/other", "", "", 404},
		{"/de/imprint/", "", "", 404},
		{"/metrics", "", "", 404},
		{"/nothing", "", "", 404},
	} {
		rec := serve(t, h, "GET", tc.target, "", nil)
		if rec.Code != tc.want || rec.Header().Get("Location") != tc.location ||
			!strings.Contains(rec.Body.String(), tc.body) {
			t.Errorf("GET %s = %d %q %q", tc.target, rec.Code, rec.Header().Get("Location"), rec.Body)
		}
	}
}

func TestHandlerRequiresItsParts(t *testing.T) {
	if _, err := Handler(Config{Store: ownertest.New(), Owner: func(*http.Request) (int64, bool) { return 1, true }}); err == nil {
		t.Error("Handler without a dist succeeded")
	}
	if _, err := Handler(Config{Dist: fstest.MapFS{}, Store: ownertest.New()}); err == nil {
		t.Error("Handler without an owner resolver succeeded")
	}
}
