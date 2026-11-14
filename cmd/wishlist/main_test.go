// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ostendorf-cloud/govue/health"
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

	if _, err := buildHandler(); err != nil {
		t.Fatalf("buildHandler() error = %v", err)
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
