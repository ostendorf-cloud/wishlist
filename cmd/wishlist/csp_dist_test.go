// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"html"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/wishlist/internal/accounts"
	"github.com/ostendorf-cloud/wishlist/internal/config"
	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner/ownertest"
)

// The public handler sends govue's default policy: same origin files, and an
// inline script or style only with the response's nonce. A static file in the
// build cannot carry a nonce, so the build must hold none of those. This test
// reads every built HTML and CSS file and fails when one would be blocked, so
// a later change to the build, a new govue release or a Vue feature that writes
// inline content cannot break the site silently. The theme boot script is not
// in the files: once it exists it is injected at serve time with the nonce.

// wantPolicy is govue v0.5.0's DefaultPolicy with the nonce stripped.
const wantPolicy = "default-src 'none'; script-src 'self' 'nonce-X'; style-src 'self' 'nonce-X'; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; form-action 'self'; " +
	"frame-ancestors 'none'; base-uri 'none'; object-src 'none'"

var nonceRe = regexp.MustCompile(`'nonce-[A-Za-z0-9+/=_-]{16,}'`)

var (
	tagRe       = regexp.MustCompile(`(?is)<([a-z][a-z0-9-]*)\b([^>]*)>`)
	attrRe      = regexp.MustCompile(`(?is)([^\s=/"'>]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+)))?`)
	styleTagRe  = regexp.MustCompile(`(?is)<style\b`)
	remoteURLRe = regexp.MustCompile(`(?i)^(?:https?:)?//`)

	cssURLRe      = regexp.MustCompile(`(?is)url\(\s*(?:"([^"]*)"|'([^']*)'|([^)]*?))\s*\)`)
	cssImportRe   = regexp.MustCompile(`(?is)@import\s+(?:url\(\s*)?["']?\s*([^"')\s;]+)`)
	cssFontFaceRe = regexp.MustCompile(`(?is)@font-face\s*\{[^}]*\}`)
	fontDataRe    = regexp.MustCompile(`(?i)^data:\s*(?:font/|application/(?:x-)?font|application/vnd\.ms-fontobject|application/octet-stream)`)
)

// finding is one thing the policy would block.
type finding struct{ what, fix, where string }

// urlAttrs are the attributes that load or navigate to a URL.
var urlAttrs = map[string]bool{
	"src": true, "href": true, "data": true, "action": true, "formaction": true,
	"poster": true, "xlink:href": true,
}

// cleanURL decodes HTML entities and drops the whitespace and control
// characters a browser ignores inside a scheme.
func cleanURL(v string) string {
	v = html.UnescapeString(v)
	return strings.Map(func(r rune) rune {
		if r <= ' ' || r == 0x7f {
			return -1
		}
		return r
	}, v)
}

// scanHTML returns what in one HTML document the policy would block.
func scanHTML(doc string) []finding {
	var out []finding
	if loc := styleTagRe.FindString(doc); loc != "" {
		out = append(out, finding{"a style element", "extract the CSS into a file under assets/", loc})
	}
	for _, m := range tagRe.FindAllStringSubmatch(doc, -1) {
		tag, name, rest := m[0], strings.ToLower(m[1]), m[2]
		attrs := map[string]string{}
		for _, a := range attrRe.FindAllStringSubmatch(rest, -1) {
			val := a[2] + a[3] + a[4]
			attrs[strings.ToLower(a[1])] = val
		}
		if name == "script" {
			if _, ok := attrs["src"]; !ok {
				out = append(out, finding{
					"an inline script (no src)",
					"load it from a file under assets/, or inject it at serve time with the response nonce", tag,
				})
			}
		}
		for attr, val := range attrs {
			switch {
			case attr == "style":
				out = append(out, finding{"a style attribute", "use a class, or set the property from script through the CSSOM", tag})
			case strings.HasPrefix(attr, "on"):
				out = append(out, finding{"an inline event handler", "attach the listener from script", tag})
			}
			if urlAttrs[attr] && strings.HasPrefix(strings.ToLower(cleanURL(val)), "javascript:") {
				out = append(out, finding{"a javascript: URL", "use a real link or a button", tag})
			}
		}
		if name == "a" {
			continue // a link navigates, the policy does not restrict it
		}
		if name == "link" && metadataRel(attrs["rel"]) {
			continue // a canonical or alternate link is metadata, never fetched
		}
		remote := false
		for attr, val := range attrs {
			if urlAttrs[attr] && remoteURLRe.MatchString(cleanURL(val)) {
				remote = true
			}
			if attr == "srcset" {
				for _, cand := range strings.Split(html.UnescapeString(val), ",") {
					if f := strings.Fields(cand); len(f) > 0 && remoteURLRe.MatchString(f[0]) {
						remote = true
					}
				}
			}
		}
		if remote {
			out = append(out, finding{"a third party origin", "the policy allows same origin only", tag})
		}
	}
	return out
}

// metadataRel is true only when every token of rel is canonical or alternate:
// "alternate stylesheet" loads a stylesheet.
func metadataRel(rel string) bool {
	tokens := strings.Fields(strings.ToLower(html.UnescapeString(rel)))
	if len(tokens) == 0 {
		return false
	}
	for _, tok := range tokens {
		if tok != "canonical" && tok != "alternate" {
			return false
		}
	}
	return true
}

// scanCSS returns what in one stylesheet the policy would block: a remote
// origin, an import, or a font that is not a same origin file (font-src is
// 'self', so a data: font fails).
func scanCSS(css string) []finding {
	var out []finding
	blocked := func(raw string) bool {
		u := cleanURL(raw)
		return remoteURLRe.MatchString(u) || fontDataRe.MatchString(u)
	}
	for _, m := range cssURLRe.FindAllStringSubmatch(css, -1) {
		if v := m[1] + m[2] + m[3]; blocked(v) {
			out = append(out, finding{"a remote or font data URL in CSS", "ship the file under assets/ and reference it by a same origin path", m[0]})
		}
	}
	for _, m := range cssImportRe.FindAllStringSubmatch(css, -1) {
		if blocked(m[1]) || strings.HasPrefix(strings.ToLower(cleanURL(m[1])), "data:") {
			out = append(out, finding{"a remote @import", "bundle the stylesheet instead", m[0]})
		}
	}
	for _, face := range cssFontFaceRe.FindAllString(css, -1) {
		for _, m := range cssURLRe.FindAllStringSubmatch(face, -1) {
			u := strings.ToLower(cleanURL(m[1] + m[2] + m[3]))
			if remoteURLRe.MatchString(u) || strings.HasPrefix(u, "data:") {
				out = append(out, finding{"an @font-face with a source that is not a same origin file", "font-src is 'self'", face})
			}
		}
	}
	return out
}

func TestTheBuiltSPAFitsThePolicy(t *testing.T) {
	dist, err := fs.Sub(distFS, "dist")
	if err != nil {
		t.Fatal(err)
	}
	pages, sheets := 0, 0
	err = fs.WalkDir(dist, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		var scan func(string) []finding
		switch path.Ext(name) {
		case ".html":
			pages++
			scan = scanHTML
		case ".css":
			sheets++
			scan = scanCSS
		default:
			return nil
		}
		raw, err := fs.ReadFile(dist, name)
		if err != nil {
			return err
		}
		for _, f := range scan(string(raw)) {
			t.Errorf("dist/%s has %s: %.120s\nThe policy would block it; %s.", name, f.what, f.where, f.fix)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The shell and the four legal pages at least; fewer means the prerender did not run.
	if pages < 5 {
		t.Fatalf("scanned %d built HTML files, want at least 5: did the web build and prerender run?", pages)
	}
	if sheets < 1 {
		t.Fatal("scanned no built CSS file: did the web build run?")
	}
}

// The scanner itself has to notice each thing the policy blocks, and let the
// harmless cases pass.
func TestTheDistScanNoticesWhatThePolicyBlocks(t *testing.T) {
	for name, doc := range map[string]string{
		"inline script":              `<script>window.x = 1</script>`,
		"style attr":                 `<div style="color: red">x</div>`,
		"style element":              `<style>a{}</style>`,
		"event handler":              `<button onclick="go()">x</button>`,
		"javascript url":             `<a href="javascript:go()">x</a>`,
		"javascript url entity":      `<a href="java&#115;cript:go()">x</a>`,
		"javascript url named":       `<a href="java&Tab;script:go()">x</a>`,
		"javascript xlink":           `<svg><a xlink:href="javascript:go()"><text>x</text></a></svg>`,
		"javascript xlink entity":    `<svg><a xlink:href="java&#115;cript:go()"><text>x</text></a></svg>`,
		"remote script":              `<script src="https://cdn.example.invalid/a.js"></script>`,
		"remote css":                 `<link rel="stylesheet" href="//cdn.example.invalid/a.css">`,
		"remote srcset":              `<img src="/a.png" srcset="/a.png 1x, https://cdn.example.invalid/b.png 2x">`,
		"remote form action":         `<form action="https://evil.example.invalid/x"><input></form>`,
		"alternate stylesheet":       `<link rel="alternate stylesheet" href="https://cdn.example.invalid/a.css">`,
		"canonical plus stylesheet":  `<link rel="canonical stylesheet" href="https://cdn.example.invalid/a.css">`,
		"remote iframe, entity URL":  `<iframe src="&#104;ttps://cdn.example.invalid/"></iframe>`,
		"remote source in a picture": `<picture><source srcset="//cdn.example.invalid/a.webp"></picture>`,
	} {
		t.Run(name, func(t *testing.T) {
			if len(scanHTML(doc)) == 0 {
				t.Errorf("the scan misses %s", doc)
			}
		})
	}
	for name, doc := range map[string]string{
		"script file":     `<script type="module" src="/assets/index.js"></script>`,
		"canonical":       `<link rel="canonical" href="https://example.invalid/de/imprint">`,
		"alternate":       `<link rel="alternate" hreflang="en" href="https://example.invalid/en/imprint">`,
		"external anchor": `<a href="https://example.org/" target="_blank" rel="noopener">x</a>`,
		"same origin css": `<link rel="stylesheet" href="/assets/index.css">`,
		"escaped text":    `<p>&lt;script&gt;alert(1)&lt;/script&gt; style="x"</p>`,
	} {
		t.Run("clean "+name, func(t *testing.T) {
			if got := scanHTML(doc); len(got) != 0 {
				t.Errorf("false positive on %s: %v", doc, got)
			}
		})
	}
}

func TestTheCSSScanNoticesWhatThePolicyBlocks(t *testing.T) {
	for name, css := range map[string]string{
		"remote url":         `a{background:url(https://cdn.example.invalid/a.png)}`,
		"protocol relative":  `a{background:url("//cdn.example.invalid/a.png")}`,
		"remote import":      `@import url("https://fonts.example.invalid/a.css");`,
		"remote import bare": `@import "https://fonts.example.invalid/a.css";`,
		"data font":          `@font-face{font-family:x;src:url(data:font/woff2;base64,AAAA)}`,
		"data font octet":    `a{src:url('data:application/octet-stream;base64,AAAA')}`,
		"font face remote":   `@font-face{font-family:x;src:url(https://fonts.example.invalid/x.woff2)}`,
		"font face data any": `@font-face{font-family:x;src:url(data:image/svg+xml;base64,AAAA)}`,
	} {
		t.Run(name, func(t *testing.T) {
			if len(scanCSS(css)) == 0 {
				t.Errorf("the scan misses %s", css)
			}
		})
	}
	for name, css := range map[string]string{
		"same origin font":  `@font-face{font-family:x;src:url(/assets/x.woff2) format("woff2")}`,
		"data image":        `a{background:url("data:image/svg+xml;base64,AAAA")}`,
		"same origin image": `a{background:url(/assets/a.png)}`,
	} {
		t.Run("clean "+name, func(t *testing.T) {
			if got := scanCSS(css); len(got) != 0 {
				t.Errorf("false positive on %s: %v", css, got)
			}
		})
	}
}

// The embedded build, served by the real handler, comes with the policy that
// the scan above assumes: the whole default policy and nothing else.
func TestTheEmbeddedSPAIsServedWithThePolicy(t *testing.T) {
	handler, err := buildHandler(slog.New(slog.DiscardHandler), handlerDeps{
		owners:   ownertest.New(),
		accounts: accounts.New(newFakeBackend()),
		cfg:      config.Config{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/", "/login", "/de/imprint", "/en/privacy"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil))
		csp := rec.Header().Get("Content-Security-Policy")
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d", target, rec.Code)
		}
		if got := nonceRe.ReplaceAllString(csp, "'nonce-X'"); got != wantPolicy {
			t.Errorf("GET %s: CSP = %q, want %q", target, got, wantPolicy)
		}
	}
}
