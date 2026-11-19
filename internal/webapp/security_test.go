// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package webapp

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner"
	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner/ownertest"
)

func securityHandler(t *testing.T, trusted ...string) http.Handler {
	t.Helper()
	var prefixes []netip.Prefix
	for _, p := range trusted {
		prefixes = append(prefixes, netip.MustParsePrefix(p))
	}
	h, err := Handler(Config{
		Store: ownertest.New(),
		Owner: func(*http.Request) (int64, bool) { return 1, false },
		Dist: fstest.MapFS{
			"index.html":            {Data: []byte("<html>spa</html>")},
			"de/imprint/index.html": {Data: []byte("<html>de imprint</html>")},
		},
		TrustedProxies: prefixes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// directive returns the sources of one CSP directive, or nil when it is absent.
func directive(csp, name string) []string {
	for _, part := range strings.Split(csp, ";") {
		fields := strings.Fields(part)
		if len(fields) > 0 && fields[0] == name {
			return fields[1:]
		}
	}
	return nil
}

func TestEveryAnswerCarriesTheHardeningHeaders(t *testing.T) {
	h := securityHandler(t)
	for _, tc := range []struct {
		name, target string
		want         int
	}{
		{"SPA shell", "/", 200},
		{"SPA deep link", "/lists", 200},
		{"legal page", "/de/imprint", 200},
		{"legal redirect", "/imprint", 302},
		{"API answer", owner.Prefix + "/lists", 401},
		{"unknown API path", "/api/nothing", 404},
		{"404", "/nothing", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := serve(t, h, "GET", tc.target, "", nil)
			if rec.Code != tc.want {
				t.Fatalf("GET %s = %d, want %d", tc.target, rec.Code, tc.want)
			}
			hdr := rec.Header()
			for name, want := range map[string]string{
				"X-Content-Type-Options": "nosniff",
				"X-Frame-Options":        "DENY",
				"Referrer-Policy":        "strict-origin-when-cross-origin",
			} {
				if got := hdr.Get(name); got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
			csp := hdr.Get("Content-Security-Policy")
			for name, want := range map[string]string{
				"default-src":     "'none'",
				"frame-ancestors": "'none'",
				"base-uri":        "'none'",
				"object-src":      "'none'",
				"form-action":     "'self'",
			} {
				if got := strings.Join(directive(csp, name), " "); got != want {
					t.Errorf("CSP %s = %q, want %q in %q", name, got, want, csp)
				}
			}
			if hdr.Get("Strict-Transport-Security") != "" {
				t.Errorf("HSTS sent over plain HTTP: %q", hdr.Get("Strict-Transport-Security"))
			}
		})
	}
}

func TestThePolicyAllowsNoInlineScriptAndNoThirdParty(t *testing.T) {
	rec := serve(t, securityHandler(t), "GET", "/", "", nil)
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("no Content-Security-Policy")
	}
	for _, name := range []string{"script-src", "script-src-elem", "default-src"} {
		for _, src := range directive(csp, name) {
			if src == "'unsafe-inline'" || src == "'unsafe-eval'" {
				t.Errorf("%s allows %s: %q", name, src, csp)
			}
		}
	}
	if len(directive(csp, "script-src")) == 0 {
		t.Errorf("no script-src in %q", csp)
	}
	// Inline script is only possible through a nonce, and the nonce is fresh.
	if !regexp.MustCompile(`script-src 'self' 'nonce-[A-Za-z0-9+/]{20,}'`).MatchString(csp) {
		t.Errorf("script-src is not self plus a nonce: %q", csp)
	}
	again := serve(t, securityHandler(t), "GET", "/", "", nil).Header().Get("Content-Security-Policy")
	if again == csp {
		t.Error("two responses carry the same nonce")
	}
	for _, src := range strings.Fields(strings.NewReplacer(";", " ").Replace(csp)) {
		if strings.Contains(src, "://") || strings.HasPrefix(src, "*") || strings.Contains(src, ".") {
			t.Errorf("the policy names an origin or wildcard %q: %q", src, csp)
		}
	}
}

func TestHSTSOnlyWhereTheRequestArrivedOverTLS(t *testing.T) {
	const hsts = "max-age=31536000; includeSubDomains; preload"
	for _, tc := range []struct {
		name    string
		trusted []string
		remote  string
		tls     bool
		proto   []string
		want    string
	}{
		{"plain HTTP", nil, "192.0.2.1:1", false, nil, ""},
		{"TLS connection", nil, "192.0.2.1:1", true, nil, hsts},
		{"untrusted peer claims https", nil, "192.0.2.1:1", false, []string{"https"}, ""},
		{"peer outside the trusted range", []string{"10.0.0.0/8"}, "192.0.2.1:1", false, []string{"https"}, ""},
		{"trusted proxy says https", []string{"10.0.0.0/8"}, "10.1.2.3:1", false, []string{"https"}, hsts},
		{"trusted proxy says https, v4 in v6", []string{"10.0.0.0/8"}, "[::ffff:10.1.2.3]:1", false, []string{"https"}, hsts},
		{"trusted proxy says http", []string{"10.0.0.0/8"}, "10.1.2.3:1", false, []string{"http"}, ""},
		{"trusted proxy says nothing", []string{"10.0.0.0/8"}, "10.1.2.3:1", false, nil, ""},
		{"nearest proxy wins", []string{"10.0.0.0/8"}, "10.1.2.3:1", false, []string{"https, http"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)
			req.RemoteAddr = tc.remote
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			for _, v := range tc.proto {
				req.Header.Add("X-Forwarded-Proto", v)
			}
			rec := httptest.NewRecorder()
			securityHandler(t, tc.trusted...).ServeHTTP(rec, req)
			if got := rec.Header().Get("Strict-Transport-Security"); got != tc.want {
				t.Errorf("HSTS = %q, want %q", got, tc.want)
			}
		})
	}
}
