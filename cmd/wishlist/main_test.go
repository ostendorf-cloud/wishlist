// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ostendorf-cloud/govue/health"

	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner/ownertest"
)

// TestEmbeddedDistBuildsAWorkingHandler proves the embedded dist filesystem is
// shaped the way buildHandler expects, without starting a server or binding a
// port: a build with an empty or mis-shaped frontend build fails here instead
// of at a request.
func TestEmbeddedDistBuildsAWorkingHandler(t *testing.T) {
	dist, err := fs.Sub(distFS, "dist")
	if err != nil {
		t.Fatalf("fs.Sub(distFS, \"dist\") error = %v", err)
	}

	handler, err := buildHandler(slog.New(slog.DiscardHandler), ownertest.New(), resolveOwner("", nil, slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("buildHandler() error = %v", err)
	}

	// Until the sign in exists nobody is an owner, and the SPA still loads.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/owner/lists", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/owner/lists = %d, want 401", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET / = %d, want 200", rec.Code)
	}

	if _, err := fs.Stat(dist, "index.html"); err != nil {
		t.Fatalf("embedded dist has no index.html: %v", err)
	}
}

// TestAdminHandlerAnswersHealthAndMetrics proves the admin handler actually
// wires govue's health registry and a Prometheus scrape, without binding a
// port: a request straight through the handler is enough to prove the
// routes exist and answer.
func TestAdminHandlerAnswersHealthAndMetrics(t *testing.T) {
	admin := buildAdminHandler(health.New())

	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		admin.ServeHTTP(rec, req)
		if rec.Code >= 500 {
			t.Fatalf("GET %s = %d, want it answered", path, rec.Code)
		}
	}
}
