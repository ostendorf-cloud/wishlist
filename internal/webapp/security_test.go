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

// wantPolicy is govue v0.5.0's DefaultPolicy with the nonce stripped. The
// whole policy is compared, so any added source or directive fails.
const wantPolicy = "default-src 'none'; script-src 'self' 'nonce-X'; style-src 'self' 'nonce-X'; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; form-action 'self'; " +
	"frame-ancestors 'none'; base-uri 'none'; object-src 'none'"

const wantHSTS = "max-age=31536000"

var nonceRe = regexp.MustCompile(`'nonce-[A-Za-z0-9+/=_-]{16,}'`)

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

// stripNonce replaces every nonce of a policy with X.
func stripNonce(csp string) string { return nonceRe.ReplaceAllString(csp, "'nonce-X'") }

// variants are the two ways a request reaches the handler. The HSTS variant
// takes the secure branch of the middleware, and the headers outside HSTS must
// be the same on both.
var variants = []struct {
	name     string
	trusted  []string
	remote   string
	proto    string
	wantHSTS string
}{
	{"plain HTTP", nil, "192.0.2.1:1", "", ""},
	{"trusted proxy over https", []string{"10.0.0.0/8"}, "10.1.2.3:1", "https", wantHSTS},
}

func do(t *testing.T, h http.Handler, method, target, remote, proto string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	req.RemoteAddr = remote
	if proto != "" {
		req.Header.Set("X-Forwarded-Proto", proto)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestEveryAnswerCarriesTheHardeningHeaders(t *testing.T) {
	for _, v := range variants {
		h := securityHandler(t, v.trusted...)
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
			t.Run(v.name+"/"+tc.name, func(t *testing.T) {
				rec := do(t, h, "GET", tc.target, v.remote, v.proto)
				if rec.Code != tc.want {
					t.Fatalf("GET %s = %d, want %d", tc.target, rec.Code, tc.want)
				}
				hdr := rec.Header()
				for name, want := range map[string]string{
					"X-Content-Type-Options":    "nosniff",
					"X-Frame-Options":           "DENY",
					"Referrer-Policy":           "strict-origin-when-cross-origin",
					"Strict-Transport-Security": v.wantHSTS,
				} {
					if got := hdr.Get(name); got != want {
						t.Errorf("%s = %q, want %q", name, got, want)
					}
				}
				if got := hdr.Values("Strict-Transport-Security"); len(got) > 1 {
					t.Errorf("HSTS sent %d times: %q", len(got), got)
				}
				csp := hdr.Get("Content-Security-Policy")
				if got := stripNonce(csp); got != wantPolicy {
					t.Errorf("CSP = %q, want %q", got, wantPolicy)
				}
				if got := strings.Join(directive(csp, "frame-ancestors"), " "); got != "'none'" {
					t.Errorf("frame-ancestors = %q in %q", got, csp)
				}
			})
		}
	}
}

func TestThePolicyAllowsNoInlineScriptAndNoThirdParty(t *testing.T) {
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			h := securityHandler(t, v.trusted...)
			csp := do(t, h, "GET", "/", v.remote, v.proto).Header().Get("Content-Security-Policy")
			if csp == "" {
				t.Fatal("no Content-Security-Policy")
			}
			// The whole policy is an allowlist: nothing else may appear.
			if got := stripNonce(csp); got != wantPolicy {
				t.Errorf("CSP = %q, want %q", got, wantPolicy)
			}
			for _, name := range []string{"script-src", "script-src-elem", "default-src"} {
				for _, src := range directive(csp, name) {
					if src == "'unsafe-inline'" || src == "'unsafe-eval'" {
						t.Errorf("%s allows %s: %q", name, src, csp)
					}
				}
			}
			// Inline script is only possible through a nonce, and the nonce is fresh.
			again := do(t, h, "GET", "/", v.remote, v.proto).Header().Get("Content-Security-Policy")
			if again == csp {
				t.Error("two responses carry the same nonce")
			}
		})
	}
}

func TestHSTSOnlyWhereTheRequestArrivedOverTLS(t *testing.T) {
	const hsts = wantHSTS
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
		{"last of two header lines wins, https", []string{"10.0.0.0/8"}, "10.1.2.3:1", false, []string{"http", "https"}, hsts},
		{"last of two header lines wins, http", []string{"10.0.0.0/8"}, "10.1.2.3:1", false, []string{"https", "http"}, ""},
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
			if got := rec.Header().Values("Strict-Transport-Security"); len(got) > 1 {
				t.Errorf("HSTS sent %d times: %q", len(got), got)
			}
		})
	}
}
