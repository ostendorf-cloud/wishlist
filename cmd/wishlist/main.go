// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/ostendorf-cloud/govue/health"
	"github.com/ostendorf-cloud/govue/httpserver"
	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/metrics"

	"github.com/ostendorf-cloud/wishlist/internal/config"
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

	handler, err := buildHandler()
	if err != nil {
		return err
	}

	checks := health.New()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

// buildHandler serves the embedded SPA bundle. A real application replaces
// this with its own handler-building package, adding the application's own
// API routes and whatever middleware govue pieces it needs. Health,
// readiness and metrics never live here; see buildAdminHandler.
func buildHandler() (http.Handler, error) {
	dist, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, fmt.Errorf("wishlist: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServerFS(dist))
	return mux, nil
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
