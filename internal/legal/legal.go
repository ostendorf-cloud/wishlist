// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package legal serves the legal pages of the site: govue's redirects from
// the short and German paths, and the four prerendered pages they lead to.
//
// It lives outside internal/webapp on purpose: the handler layer tests of the
// owner API forbid any other writer of a response body under that directory.
package legal

import (
	"fmt"
	"io/fs"
	"net/http"

	"github.com/ostendorf-cloud/govue/language"
)

// Languages and Pages are the canonical set /<language>/<page>. The site has
// no terms, so there is no terms page and no /terms or /agb route.
var (
	Languages = []string{"de", "en"}
	Pages     = []string{"imprint", "privacy"}
)

// Register mounts the redirects and the pages on mux. dist is the built SPA,
// whose <language>/<page>/index.html files the prerender step writes.
//
// The redirects need no session and no CSRF check, so the caller mounts them
// outside the route chain. mux needs a catch all without a method, see
// language.HandleLegal.
func Register(mux *http.ServeMux, dist fs.FS) error {
	if err := language.HandleLegal(mux, language.LegalConfig{}, nil); err != nil {
		return fmt.Errorf("legal: %w", err)
	}
	for _, lang := range Languages {
		for _, page := range Pages {
			mux.Handle("GET /"+lang+"/"+page, pageHandler(dist, lang+"/"+page+"/index.html"))
		}
	}
	return nil
}

// pageHandler serves one prerendered file, or a 404 when the build did not
// write it. The path comes from the fixed set above, never from the request.
func pageHandler(dist fs.FS, name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if info, err := fs.Stat(dist, name); err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		http.ServeFileFS(w, r, dist, name)
	})
}
