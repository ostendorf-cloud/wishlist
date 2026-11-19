// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/ostendorf-cloud/govue/health"
	"github.com/ostendorf-cloud/govue/httpserver"
	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/metrics"

	"github.com/ostendorf-cloud/wishlist/internal/config"
	"github.com/ostendorf-cloud/wishlist/internal/webapp"
	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	logger := logging.New(os.Stdout, cfg.LogLevel)

	checks := health.New()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The pool is built at once and connects on first use. The schema is
	// applied in the background, retrying until the database is there, and
	// /readyz stays unready until it is.
	db, waitMigration, err := startDatabase(ctx, cfg.Database, logger, checks)
	if err != nil {
		return err
	}
	defer func() {
		stop()
		waitMigration()
		db.Close()
	}()

	// The owner API gets the owner's side of the store and nothing else.
	owners := db.Owners()
	handler, err := buildHandler(logger, owners, resolveOwner(cfg.DevOwner, owners, logger))
	if err != nil {
		return err
	}

	return httpserver.RunAdmin(ctx, httpserver.Config{
		Addr:    cfg.Addr,
		Handler: handler,
		Logger:  logger,
		// Failing the readiness probe before the drain delay starts is what
		// lets a load balancer stop sending new requests before the server
		// stops accepting connections at all.
		BeforeShutdown: checks.Drain,
	}, httpserver.AdminConfig{
		Addr:    cfg.AdminAddr,
		Handler: buildAdminHandler(checks),
	})
}

// buildHandler serves the owner API and the embedded SPA bundle, see
// internal/webapp. Health, readiness and metrics never live here; see
// buildAdminHandler.
func buildHandler(logger *slog.Logger, owners owner.Store, resolve owner.Resolver) (http.Handler, error) {
	dist, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, fmt.Errorf("wishlist: %w", err)
	}

	handler, err := webapp.Handler(webapp.Config{
		Store: owners,
		Owner: resolve,
		// No session exists before the owner sign in (issue 4), so there is
		// no token to derive: only the protector's origin check applies.
		// Issue 4 supplies the function that reads the session and returns
		// csrf.Token(key, session.ID).
		CSRFExpected: nil,
		Dist:         dist,
		Logger:       logger,
	})
	if err != nil {
		return nil, fmt.Errorf("wishlist: %w", err)
	}
	return handler, nil
}

// buildAdminHandler serves liveness, readiness and a Prometheus scrape on
// the second, administrative listener httpserver.RunAdmin opens: never on
// the public listener buildHandler builds, and never routed by the Helm
// chart's own Ingress or HTTPRoute.
func buildAdminHandler(checks *health.Registry) http.Handler {
	mux := http.NewServeMux()
	checks.Handle(mux)
	mux.Handle("GET /metrics", metrics.Handler(metrics.NewRegistry()))
	return mux
}
