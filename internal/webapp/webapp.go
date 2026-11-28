// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package webapp builds the public handler: the owner API behind govue's
// middleware chain, and the SPA behind it. It follows the chain order of
// govue's example application: one outer chain around the mux, one route
// chain on every handler the mux dispatches to, so a refusal by the rate
// limiter or the CSRF protector carries the real route in the log.
//
// The owner API takes the owner's side of the store as owner.Store and nothing
// else; boundary_test.go in the owner package fails when anything under this
// directory reaches the guest side.
package webapp

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"path"
	"strings"
	"time"

	"github.com/ostendorf-cloud/govue/analytics"
	"github.com/ostendorf-cloud/govue/api"
	"github.com/ostendorf-cloud/govue/clientip"
	"github.com/ostendorf-cloud/govue/csrf"
	"github.com/ostendorf-cloud/govue/middleware"
	"github.com/ostendorf-cloud/govue/ratelimit"
	"github.com/ostendorf-cloud/govue/themeboot"

	"github.com/ostendorf-cloud/wishlist/internal/legal"
	"github.com/ostendorf-cloud/wishlist/internal/secheaders"
	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner"
)

// Rate limit of the API per client address. The limiter keys by the address
// of the connection until trusted proxies are configured, so behind an ingress
// all callers share one bucket: the numbers are generous for that reason.
const (
	rateBurst   = 300
	ratePer     = time.Minute
	maxRateKeys = 100_000
)

// Config is what the handler needs from the wiring.
type Config struct {
	// Store is the owner's side of the store.
	Store owner.Store
	// Owner says who is signed in. It reads what Auth.SessionMiddleware put
	// in the request context.
	Owner owner.Resolver
	// CSRFExpected derives the token a request must carry from its session.
	// It returns false for a request without a session, which carries no
	// authority to borrow, and then only the origin check applies.
	CSRFExpected csrf.Expected
	// Auth is the sign in. Nil mounts no account route and reads no session,
	// which only tests do.
	Auth Auth
	// ClientIPs resolves the client address of the limits and the log. Nil
	// trusts no proxy header.
	ClientIPs *clientip.Resolver
	// Views counts page views, see NewViews. Nil counts nothing.
	Views *analytics.Client
	// TrustedProxies are the peers whose X-Forwarded-Proto is believed when
	// the handler decides whether to send HSTS. Empty trusts no header.
	TrustedProxies []netip.Prefix
	// Dist is the built SPA.
	Dist   fs.FS
	Logger *slog.Logger
}

// Auth is what the public handler needs from the sign in, see internal/auth.
type Auth interface {
	// SessionMiddleware reads the session into the request context.
	SessionMiddleware(http.Handler) http.Handler
	// ClientLimit is the stricter per address limit of the password routes.
	ClientLimit(http.Handler) http.Handler
	// Mount registers the account routes.
	Mount(mux *http.ServeMux, route, checked func(http.Handler) http.Handler)
}

// NewViews builds the page view counter. It is the one place the privacy
// relevant choices are made: the client address is resolved by ips, built
// from the configured trusted proxies only, and it is masked, never sent
// whole (FullIP stays false), and no title is sent. hostname is the bare
// host of the site. client may be nil for govue's default client, a test
// passes one with a fake transport. onDrop may be nil.
func NewViews(cfg analytics.Config, hostname string, ips *clientip.Resolver, logger *slog.Logger, client *http.Client, onDrop func()) (*analytics.Client, error) {
	return analytics.New(cfg, analytics.Options{
		Hostname:   hostname,
		Logger:     logger,
		HTTPClient: client,
		ClientIP:   ips,
		OnDrop:     onDrop,
	})
}

// Handler builds the public handler.
func Handler(cfg Config) (http.Handler, error) {
	if cfg.Dist == nil {
		return nil, errors.New("webapp: dist is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	expected := cfg.CSRFExpected
	if expected == nil {
		expected = func(*http.Request) (string, bool) { return "", false }
	}

	ownerAPI, err := owner.New(cfg.Store, cfg.Owner, logger)
	if err != nil {
		return nil, err
	}
	protector, err := csrf.New(expected)
	if err != nil {
		return nil, err
	}
	// The refusal uses the one error shape, like every other answer.
	protector.OnRefusal(func(w http.ResponseWriter, r *http.Request, _ error) {
		api.WriteError(w, r, api.PermissionDenied("cross origin or stale request, please reload"))
	})

	ips := cfg.ClientIPs
	if ips == nil {
		ips = clientip.NewResolver(nil)
	}
	limit, err := middleware.NewRateLimit(middleware.RateLimitConfig{
		Limiter:  ratelimit.New(rateBurst, ratePer, maxRateKeys),
		ClientIP: ips,
	})
	if err != nil {
		return nil, err
	}

	// The session middleware sits after the protector, so a request is checked
	// before its cookie is read.
	after := []middleware.Middleware{protector.Middleware}
	if cfg.Auth != nil {
		after = append(after, cfg.Auth.SessionMiddleware)
	}
	route := func(h http.Handler) http.Handler {
		return middleware.Chain(h, append([]middleware.Middleware{limit.Middleware}, after...)...)
	}

	mux := http.NewServeMux()
	ownerAPI.Register(mux, route)
	if cfg.Auth != nil {
		// The stricter limit of the routes that check a password sits next
		// to the general one, ahead of the protector and the session.
		checked := func(h http.Handler) http.Handler {
			return middleware.Chain(h, append([]middleware.Middleware{limit.Middleware, cfg.Auth.ClientLimit}, after...)...)
		}
		cfg.Auth.Mount(mux, route, checked)
	}
	// Anything else under /api is an unknown endpoint in the error shape,
	// never the SPA's index page.
	mux.Handle("/api/", route(api.NotFoundHandler()))
	// The legal pages need no session or CSRF check, so they sit outside the
	// route chain. The outer chain below still applies. They are exact
	// patterns, so spaRoutes needs no entry for /de or /en.
	if err := legal.Register(mux, cfg.Dist); err != nil {
		return nil, err
	}
	mux.Handle("/", spa(cfg.Dist))

	chain := []middleware.Middleware{
		middleware.Recover(logger),
		middleware.RequestID(logger),
		secheaders.Middleware(cfg.TrustedProxies),
		middleware.AccessLog{ClientIP: ips}.Middleware,
	}
	// Innermost, directly around the mux, because it reads r.Pattern and
	// RequestID replaces the request. A view is counted by its route pattern
	// only, so no id, token or query string of a request is ever reported.
	// The owner API answers JSON and the SPA files are no HTML page with a
	// pattern of their own, so neither is counted. It is the mux itself when
	// counting is off.
	if cfg.Views != nil {
		chain = append(chain, cfg.Views.Middleware)
	}
	return middleware.Chain(mux, chain...), nil
}

// spaRoutes are the first path segments of the routes in web/src/router.ts.
// Only these get index.html for a deep link. Every other path that is not a
// built file is a plain 404, so the public port never answers /metrics,
// /healthz or an unknown path with an HTML page (the smoke test pins that).
// TestSPARoutesCoverTheRouter keeps this list and the router in step.
var spaRoutes = []string{"/lists", "/wishes", "/login", "/signup"}

// isSPARoute compares without regard to case, because the router in
// web/src/router.ts does: vue-router matches paths case insensitively unless a
// route sets sensitive, and none does. A reload of /LISTS/3 then shows the
// same page as a click on a link to it. Built files stay case sensitive, they
// are looked up before this runs.
func isSPARoute(p string) bool {
	if path.Ext(p) != "" {
		return false
	}
	p = strings.ToLower(p)
	for _, prefix := range spaRoutes {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

// spa serves the built files and, for GET and HEAD of a route of the SPA,
// index.html so the router in the browser can take a deep link. Everything
// else is a plain 404.
//
// The shell carries govue's theme boot script, injected here with the nonce of
// the response, so a stored colour mode shows at first paint. The static file
// cannot hold the script: the policy runs a script with the nonce only.
// themeboot.Shell marks the response no-store, because a page with a nonce
// must never be served again from a cache.
func spa(dist fs.FS) http.Handler {
	files := http.FileServerFS(dist)
	var shell http.Handler
	if page, err := fs.ReadFile(dist, "index.html"); err == nil {
		shell = themeboot.Shell(page)
	}
	// serveShell sends GET and HEAD through the same handler, so a HEAD answer
	// carries the headers and the length of the GET answer. net/http drops the
	// body of a HEAD answer.
	serveShell := func(w http.ResponseWriter, r *http.Request) {
		if shell != nil {
			shell.ServeHTTP(w, r)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		files.ServeHTTP(w, r2)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		clean := path.Clean("/" + r.URL.Path)
		name := strings.TrimPrefix(clean, "/")
		if name == "" {
			name = "."
		}
		if name == "." {
			serveShell(w, r)
			return
		}
		if info, err := fs.Stat(dist, name); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		if !isSPARoute(clean) {
			http.NotFound(w, r)
			return
		}
		serveShell(w, r)
	})
}
