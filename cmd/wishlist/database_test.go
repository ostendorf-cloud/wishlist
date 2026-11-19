// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ostendorf-cloud/govue/health"

	"github.com/ostendorf-cloud/wishlist/internal/dbtest"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// syncBuffer is a log sink several goroutines write to and a test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func readyz(t *testing.T, checks *health.Registry) (int, string) {
	t.Helper()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil)
	buildAdminHandler(checks).ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// TestReadinessIsUnreadyWhileTheDatabaseIsUnreachable runs everywhere: the
// real client meets a port nothing listens on.
func TestReadinessIsUnreadyWhileTheDatabaseIsUnreachable(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	checks := health.New()
	logs := &syncBuffer{}
	db, wait, err := startDatabase(ctx, dbtest.Unreachable(t), slog.New(slog.NewTextHandler(logs, nil)), checks)
	if err != nil {
		t.Fatalf("startDatabase() error = %v", err)
	}
	defer db.Close()

	// Startup has to try to apply the schema. The database is unreachable, so
	// the attempt fails and says so.
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(logs.String(), "schema not applied yet") {
		if time.Now().After(deadline) {
			t.Fatalf("startup never tried to apply the schema, log:\n%s", logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if strings.Contains(logs.String(), dbtest.UnreachablePassword) {
		t.Errorf("the log leaks the password:\n%s", logs.String())
	}

	code, body := readyz(t, checks)
	if code != http.StatusServiceUnavailable {
		t.Errorf("GET /readyz = %d, want 503 while the database is unreachable", code)
	}
	if !strings.Contains(body, "database") {
		t.Errorf("body %q does not name the failing check", body)
	}
	if strings.Contains(body, dbtest.UnreachablePassword) {
		t.Errorf("body %q leaks the password", body)
	}

	// Liveness must not depend on the database.
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	buildAdminHandler(checks).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /healthz = %d, want 200 while the database is unreachable", rec.Code)
	}

	cancel()
	finished := make(chan struct{})
	go func() { wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("the background migration did not stop when its context was cancelled")
	}
}

// TestStartupAppliesTheSchemaAndBecomesReady needs a real server: see
// internal/dbtest.
func TestStartupAppliesTheSchemaAndBecomesReady(t *testing.T) {
	cfg := dbtest.Database(t)

	ctx, cancel := context.WithCancel(t.Context())
	checks := health.New()
	db, wait, err := startDatabase(ctx, cfg, discardLogger(), checks)
	if err != nil {
		t.Fatalf("startDatabase() error = %v", err)
	}
	defer func() {
		cancel()
		wait()
		db.Close()
	}()

	deadline := time.Now().Add(20 * time.Second)
	for {
		code, body := readyz(t, checks)
		if code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET /readyz = %d %s, want 200 once the schema is applied", code, body)
		}
		time.Sleep(100 * time.Millisecond)
	}

	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Path:     "/" + cfg.Name,
		RawQuery: "sslmode=" + cfg.SSLMode,
	}
	conn, err := pgx.Connect(t.Context(), u.String())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	var tables int
	if err := conn.QueryRow(t.Context(), `
		select count(*) from pg_tables
		where schemaname = 'public'
		  and tablename in ('owners', 'lists', 'wishes', 'reservations', 'comments', 'owner_credentials')`).Scan(&tables); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	if tables != 6 {
		t.Errorf("found %d of the 6 application tables after startup", tables)
	}
}
