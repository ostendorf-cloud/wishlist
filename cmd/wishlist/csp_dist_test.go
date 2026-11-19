// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
// reads every built HTML file and fails when one would be blocked, so a later
// change to the build, a new govue release or a Vue feature that writes inline
// content cannot break the site silently. The theme boot script is not in the
// files: it is injected at serve time with the nonce.
var blockedByThePolicy = []struct {
	what, fix string
	re        *regexp.Regexp
}{
	{
		"an inline script (no src)", "load it from a file under assets/, or inject it at serve time with the response nonce",
		regexp.MustCompile(`(?is)<script\b(?:[^>]*?)>`),
	},
	{
		"a style attribute", "use a class, or set the property from script through the CSSOM",
		regexp.MustCompile(`(?is)<[a-z][^>]*\sstyle\s*=`),
	},
	{
		"a style element", "extract the CSS into a file under assets/",
		regexp.MustCompile(`(?is)<style\b`),
	},
	{
		"an inline event handler", "attach the listener from script",
		regexp.MustCompile(`(?is)<[a-z][^>]*\son[a-z]+\s*=`),
	},
	{
		"a javascript: URL", "use a real link or a button",
		regexp.MustCompile(`(?is)\s(?:href|src|action|formaction)\s*=\s*["']?\s*javascript:`),
	},
}

var (
	scriptTag  = regexp.MustCompile(`(?is)<script\b([^>]*)>`)
	srcAttr    = regexp.MustCompile(`(?is)\ssrc\s*=\s*["']?([^"'\s>]+)`)
	remoteRef  = regexp.MustCompile(`(?is)<(?:script|link|img|iframe|source|video|audio|embed|object)\b[^>]*\s(?:src|href|data)\s*=\s*["']?(https?:)?//`)
	linkCanon  = regexp.MustCompile(`(?is)<link\b[^>]*\srel\s*=\s*["']?(?:canonical|alternate)\b`)
	remoteScan = regexp.MustCompile(`(?is)<[a-z][^>]*>`)
)

func TestTheBuiltSPAFitsThePolicy(t *testing.T) {
	dist, err := fs.Sub(distFS, "dist")
	if err != nil {
		t.Fatal(err)
	}
	pages := 0
	err = fs.WalkDir(dist, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(name, ".html") {
			return err
		}
		pages++
		raw, err := fs.ReadFile(dist, name)
		if err != nil {
			return err
		}
		html := string(raw)
		for _, m := range scriptTag.FindAllStringSubmatch(html, -1) {
			if !srcAttr.MatchString(m[1]) {
				t.Errorf("dist/%s has %s: %s\nThe policy only runs inline script with a per response nonce; %s.",
					name, blockedByThePolicy[0].what, m[0], blockedByThePolicy[0].fix)
			}
		}
		for _, b := range blockedByThePolicy[1:] {
			if loc := b.re.FindString(html); loc != "" {
				t.Errorf("dist/%s has %s: %.120s\nThe policy would block it; %s.", name, b.what, loc, b.fix)
			}
		}
		for _, tag := range remoteScan.FindAllString(html, -1) {
			if linkCanon.MatchString(tag) {
				continue // a canonical or alternate link is metadata, never fetched
			}
			if remoteRef.MatchString(tag) {
				t.Errorf("dist/%s loads a third party origin: %.120s\nThe policy allows same origin only.", name, tag)
			}
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
}

// The scanner itself has to notice each thing the policy blocks.
func TestTheDistScanNoticesWhatThePolicyBlocks(t *testing.T) {
	for name, html := range map[string]string{
		"inline script":  `<script>window.x = 1</script>`,
		"style attr":     `<div style="color: red">x</div>`,
		"style element":  `<style>a{}</style>`,
		"event handler":  `<button onclick="go()">x</button>`,
		"javascript url": `<a href="javascript:go()">x</a>`,
		"remote script":  `<script src="https://cdn.example.invalid/a.js"></script>`,
		"remote css":     `<link rel="stylesheet" href="//cdn.example.invalid/a.css">`,
	} {
		t.Run(name, func(t *testing.T) {
			hit := false
			for _, m := range scriptTag.FindAllStringSubmatch(html, -1) {
				hit = hit || !srcAttr.MatchString(m[1])
			}
			for _, b := range blockedByThePolicy[1:] {
				hit = hit || b.re.MatchString(html)
			}
			for _, tag := range remoteScan.FindAllString(html, -1) {
				hit = hit || remoteRef.MatchString(tag)
			}
			if !hit {
				t.Errorf("the scan misses %s", html)
			}
		})
	}
}

// The embedded build, served by the real handler, comes with the policy that
// the scan above assumes.
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
		if rec.Code != http.StatusOK || !strings.Contains(csp, "script-src 'self' 'nonce-") || strings.Contains(csp, "unsafe-inline") {
			t.Errorf("GET %s = %d with CSP %q", target, rec.Code, csp)
		}
	}
}
