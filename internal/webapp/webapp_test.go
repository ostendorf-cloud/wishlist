// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package webapp

import (
	"net/http"
	"net/http/httptest"
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

func TestTheSPAIsServedWithAFallbackForDeepLinks(t *testing.T) {
	h := newHandler(t, true, nil)
	cases := []struct {
		target string
		want   int
		body   string
	}{
		{"/", 200, "spa"},
		{"/lists/12", 200, "spa"},
		{"/assets/app.js", 200, "js"},
		{"/assets/missing.js", 404, ""},
	}
	for _, tc := range cases {
		rec := serve(t, h, "GET", tc.target, "", nil)
		if rec.Code != tc.want || !strings.Contains(rec.Body.String(), tc.body) {
			t.Errorf("%s = %d %q", tc.target, rec.Code, rec.Body)
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
