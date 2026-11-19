// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/analytics"
	"github.com/ostendorf-cloud/govue/clientip"
	"github.com/ostendorf-cloud/govue/health"
	"github.com/ostendorf-cloud/govue/httpserver"
	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/metrics"

	"github.com/ostendorf-cloud/wishlist/internal/accounts"
	"github.com/ostendorf-cloud/wishlist/internal/auth"
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

	registry := metrics.NewRegistry()
	ips := clientip.NewResolver(cfg.TrustedProxies)
	views, err := webapp.NewViews(cfg.Umami, cfg.SiteHostname(), ips, logger, nil,
		metrics.NewCounter(registry, "analytics_views_dropped_total",
			"Page views lost to a full queue or a shutdown before they reached Umami."))
	if err != nil {
		return fmt.Errorf("wishlist: %w", err)
	}
	// govue logs only failures, so the one startup line is ours. It names no
	// URL, which an operator may have put a secret into.
	if views.Enabled() {
		logger.Info("page view counting is on", "site_host", cfg.SiteHostname())
	} else {
		logger.Info("page view counting is off: UMAMI_URL and UMAMI_WEBSITE_ID are not set")
	}
	// The server has stopped by the time this runs: send the views still
	// queued, within a bound.
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), analyticsCloseTimeout)
		defer cancel()
		if err := closeResult(logger, views.Close(closeCtx)); err != nil {
			logger.Warn("closing the page view client failed", "error", err)
		}
	}()

	// The owner API gets the owner's side of the store and nothing else. The
	// sign in gets the credentials side.
	handler, err := buildHandler(logger, handlerDeps{
		owners:   db.Owners(),
		accounts: accounts.New(db.Accounts()),
		cfg:      cfg,
		ips:      ips,
		views:    views,
	})
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
		Handler: buildAdminHandler(checks, metrics.Handler(registry)),
	})
}

// analyticsCloseTimeout bounds how long shutdown waits for queued page views
// to reach Umami.
const analyticsCloseTimeout = 5 * time.Second

// closeResult turns a missed shutdown deadline into a warning: views that
// did not reach Umami in time are lost, which is no failure. Other errors
// pass through to the caller.
func closeResult(logger *slog.Logger, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		logger.Warn("page views still queued at shutdown were not sent", "error", err)
		return nil
	}
	return err
}

// handlerDeps is what the public handler is built from.
type handlerDeps struct {
	owners   owner.Store
	accounts account.Store
	cfg      config.Config
	// ips resolves the client address. Nil builds it from cfg.TrustedProxies.
	ips *clientip.Resolver
	// views counts page views. Nil counts nothing.
	views *analytics.Client
	// tune lets a test change the sign in's limits and clock. Nil in main.
	tune func(*auth.Config)
}

// webappConfig connects the sign in to the public handler. Tests build the
// config here and weaken one part to prove a check notices.
func webappConfig(logger *slog.Logger, d handlerDeps) (webapp.Config, error) {
	dist, err := fs.Sub(distFS, "dist")
	if err != nil {
		return webapp.Config{}, fmt.Errorf("wishlist: %w", err)
	}

	ips := d.ips
	if ips == nil {
		ips = clientip.NewResolver(d.cfg.TrustedProxies)
	}
	authCfg := auth.Config{
		Accounts:         d.accounts,
		ClientIPs:        ips,
		Logger:           logger,
		RegistrationOpen: d.cfg.RegistrationOpen,
		AllowPlainHTTP:   d.cfg.AllowPlainHTTP,
	}
	if d.tune != nil {
		d.tune(&authCfg)
	}
	signIn, err := auth.New(authCfg)
	if err != nil {
		return webapp.Config{}, fmt.Errorf("wishlist: %w", err)
	}

	return webapp.Config{
		Store: d.owners,
		Owner: resolveOwner(signIn),
		// Every unsafe request of a signed in session needs that session's
		// token. A request without a session carries no authority, and the
		// protector's origin check still applies to it.
		CSRFExpected: signIn.CSRFExpected(),
		Auth:         signIn,
		ClientIPs:    ips,
		Views:        d.views,
		Dist:         dist,
		Logger:       logger,
	}, nil
}

// buildHandler serves the owner API, the account routes and the embedded SPA
// bundle, see internal/webapp. Health, readiness and metrics never live here;
// see buildAdminHandler.
func buildHandler(logger *slog.Logger, d handlerDeps) (http.Handler, error) {
	cfg, err := webappConfig(logger, d)
	if err != nil {
		return nil, err
	}
	handler, err := webapp.Handler(cfg)
	if err != nil {
		return nil, fmt.Errorf("wishlist: %w", err)
	}
	return handler, nil
}

// buildAdminHandler serves liveness, readiness and a Prometheus scrape on
// the second, administrative listener httpserver.RunAdmin opens: never on
// the public listener buildHandler builds, and never routed by the Helm
// chart's own Ingress or HTTPRoute.
func buildAdminHandler(checks *health.Registry, scrape http.Handler) http.Handler {
	mux := http.NewServeMux()
	checks.Handle(mux)
	mux.Handle("GET /metrics", scrape)
	return mux
}
