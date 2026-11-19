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
	"path"
	"strings"
	"time"

	"github.com/ostendorf-cloud/govue/api"
	"github.com/ostendorf-cloud/govue/clientip"
	"github.com/ostendorf-cloud/govue/csrf"
	"github.com/ostendorf-cloud/govue/middleware"
	"github.com/ostendorf-cloud/govue/ratelimit"

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
	// Owner says who is signed in. The sign in (issue 4) supplies it.
	Owner owner.Resolver
	// CSRFExpected derives the token a request must carry from its session.
	// It returns false while there is no session to protect, in which case
	// only the origin check of the protector applies. The sign in (issue 4)
	// supplies the real one.
	CSRFExpected csrf.Expected
	// Dist is the built SPA.
	Dist   fs.FS
	Logger *slog.Logger
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

	ips := clientip.NewResolver(nil)
	limit, err := middleware.NewRateLimit(middleware.RateLimitConfig{
		Limiter:  ratelimit.New(rateBurst, ratePer, maxRateKeys),
		ClientIP: ips,
	})
	if err != nil {
		return nil, err
	}

	route := func(h http.Handler) http.Handler {
		return middleware.Chain(h, limit.Middleware, protector.Middleware)
	}

	mux := http.NewServeMux()
	ownerAPI.Register(mux, route)
	// Anything else under /api is an unknown endpoint in the error shape,
	// never the SPA's index page.
	mux.Handle("/api/", route(api.NotFoundHandler()))
	mux.Handle("/", spa(cfg.Dist))

	return middleware.Chain(mux,
		middleware.Recover(logger),
		middleware.RequestID(logger),
		middleware.AccessLog{ClientIP: ips}.Middleware,
	), nil
}

// spaRoutes are the first path segments of the routes in web/src/router.ts.
// Only these get index.html for a deep link. Every other path that is not a
// built file is a plain 404, so the public port never answers /metrics,
// /healthz or an unknown path with an HTML page (the smoke test pins that).
// TestSPARoutesCoverTheRouter keeps this list and the router in step.
var spaRoutes = []string{"/lists", "/wishes"}

func isSPARoute(p string) bool {
	if path.Ext(p) != "" {
		return false
	}
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
func spa(dist fs.FS) http.Handler {
	files := http.FileServerFS(dist)
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
		if info, err := fs.Stat(dist, name); err == nil && !info.IsDir() || name == "." {
			files.ServeHTTP(w, r)
			return
		}
		if !isSPARoute(clean) {
			http.NotFound(w, r)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		files.ServeHTTP(w, r2)
	})
}
