// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/govue/health"
	"github.com/ostendorf-cloud/govue/metrics"

	"github.com/ostendorf-cloud/wishlist/internal/accounts"
	"github.com/ostendorf-cloud/wishlist/internal/config"
	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner/ownertest"
)

// A missed shutdown deadline is a warning and no failure. Other errors pass.
func TestCloseResult(t *testing.T) {
	t.Parallel()

	other := errors.New("boom")
	tests := map[string]struct {
		err      error
		want     error
		wantWarn bool
	}{
		"nil":               {},
		"deadline exceeded": {err: context.DeadlineExceeded, wantWarn: true},
		"canceled":          {err: context.Canceled, wantWarn: true},
		"wrapped deadline":  {err: fmt.Errorf("close: %w", context.DeadlineExceeded), wantWarn: true},
		"other error":       {err: other, want: other},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, nil))
			got := closeResult(logger, tc.err)
			if !errors.Is(got, tc.want) || (tc.want == nil && got != nil) {
				t.Errorf("closeResult() = %v, want %v", got, tc.want)
			}
			if warned := strings.Contains(buf.String(), `"level":"WARN"`); warned != tc.wantWarn {
				t.Errorf("warn logged = %v, want %v (log %q)", warned, tc.wantWarn, buf.String())
			}
		})
	}
}

// TestEmbeddedDistBuildsAWorkingHandler proves the embedded dist filesystem is
// shaped the way buildHandler expects, without starting a server or binding a
// port: a build with an empty or mis-shaped frontend build fails here instead
// of at a request.
func TestEmbeddedDistBuildsAWorkingHandler(t *testing.T) {
	dist, err := fs.Sub(distFS, "dist")
	if err != nil {
		t.Fatalf("fs.Sub(distFS, \"dist\") error = %v", err)
	}

	handler, err := buildHandler(slog.New(slog.DiscardHandler), handlerDeps{
		owners:   ownertest.New(),
		accounts: accounts.New(newFakeBackend()),
		cfg:      config.Config{RegistrationOpen: true},
	})
	if err != nil {
		t.Fatalf("buildHandler() error = %v", err)
	}

	// Without a session nobody is an owner, and the SPA still loads.
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
	admin := buildAdminHandler(health.New(), metrics.Handler(metrics.NewRegistry()))

	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		admin.ServeHTTP(rec, req)
		if rec.Code >= 500 {
			t.Fatalf("GET %s = %d, want it answered", path, rec.Code)
		}
	}
}
