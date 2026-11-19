// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package secheaders sends the security headers of the public handler. It sits
// outside internal/webapp because the handler layer tests of the owner API
// allow no other code there to take a response writer.
package secheaders

import (
	"net/http"
	"net/netip"
	"strings"

	"github.com/ostendorf-cloud/govue/middleware"
)

// Middleware sends govue's default security headers on every answer of the
// public handler, error answers and redirects included.
//
// The policy is govue's default, unchanged: same origin script, style, image,
// font and fetch, no framing, no base tag, and a per response nonce for inline
// script and style. The built SPA needs nothing more, because the build
// writes one module script and one stylesheet as files. The nonce is what lets
// govue's theme boot script run, which the handler injects at serve time.
// TestTheBuiltSPAFitsThePolicy in cmd/wishlist checks the built files.
//
// Strict-Transport-Security is the one change. The default sends it on every
// answer, so a browser that reached the site over plain HTTP would be told to
// upgrade. It is sent only when the request arrived over TLS or a trusted
// proxy says it did, see viaTLS.
func Middleware(trusted []netip.Prefix) middleware.Middleware {
	withHSTS := middleware.DefaultSecurity()
	withoutHSTS := middleware.DefaultSecurity()
	withoutHSTS.HSTS = 0

	return func(next http.Handler) http.Handler {
		secure := withHSTS.Middleware(next)
		plain := withoutHSTS.Middleware(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if viaTLS(r, trusted) {
				secure.ServeHTTP(w, r)
				return
			}
			plain.ServeHTTP(w, r)
		})
	}
}

// viaTLS says whether the client reached the site over TLS: the connection
// itself is TLS, or the TCP peer is a trusted proxy (TRUSTED_PROXIES) whose
// X-Forwarded-Proto names https. The header of any other peer is a claim by
// the client and is ignored. A proxy chain appends, so the last entry is the
// one the nearest trusted proxy wrote.
func viaTLS(r *http.Request, trusted []netip.Prefix) bool {
	if r.TLS != nil {
		return true
	}
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	addr := peer.Addr().Unmap()
	for _, prefix := range trusted {
		if !prefix.Contains(addr) {
			continue
		}
		values := r.Header.Values("X-Forwarded-Proto")
		if len(values) == 0 {
			return false
		}
		parts := strings.Split(values[len(values)-1], ",")
		return strings.EqualFold(strings.TrimSpace(parts[len(parts)-1]), "https")
	}
	return false
}
