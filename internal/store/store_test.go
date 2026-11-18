// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/wishlist/internal/config"
	"github.com/ostendorf-cloud/wishlist/internal/dbtest"
)

const dummyPassword = dbtest.UnreachablePassword

// unreachable returns settings for a port nothing listens on: the real
// client against a real refusal, not a fake. This is the error path of the
// client and needs no server.
func unreachable(t *testing.T) config.Database {
	t.Helper()
	return dbtest.Unreachable(t)
}

func TestDSNCarriesNoPassword(t *testing.T) {
	t.Parallel()

	cfg := config.Database{
		Host: "db.example.invalid", Port: 5432, Name: "wishlist", User: "wishlist",
		Password: "p@ss:w/ord%", SSLMode: "require",
	}
	got := dsn(cfg)

	if strings.Contains(got, "p@ss") || strings.Contains(got, "ord%") {
		t.Fatalf("DSN contains the password: %s", got)
	}
	for _, want := range []string{"postgres://wishlist@db.example.invalid:5432/wishlist", "sslmode=require"} {
		if !strings.Contains(got, want) {
			t.Errorf("DSN %s is missing %s", got, want)
		}
	}
}

// The password lands on the parsed config, whole, however odd its characters.
func TestPoolConfigCarriesTheSettings(t *testing.T) {
	t.Parallel()

	cfg := config.Database{
		Host: "db.example.invalid", Port: 6432, Name: "wishlist", User: "wishlist",
		Password: "p@ss:w/ord%", SSLMode: "require", MaxConns: 7, ConnectTimeout: 3 * time.Second,
	}
	pc, err := poolConfig(cfg)
	if err != nil {
		t.Fatalf("poolConfig() error = %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"host", pc.ConnConfig.Host, "db.example.invalid"},
		{"port", pc.ConnConfig.Port, uint16(6432)},
		{"database", pc.ConnConfig.Database, "wishlist"},
		{"user", pc.ConnConfig.User, "wishlist"},
		{"password", pc.ConnConfig.Password, "p@ss:w/ord%"},
		{"connect timeout", pc.ConnConfig.ConnectTimeout, 3 * time.Second},
		{"max conns", pc.MaxConns, int32(7)},
		{"max conn lifetime", pc.MaxConnLifetime, maxConnLifetime},
		{"max conn idle time", pc.MaxConnIdleTime, maxConnIdleTime},
		{"health check period", pc.HealthCheckPeriod, healthCheckPeriod},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if maxConnLifetime <= 0 || maxConnIdleTime <= 0 || healthCheckPeriod <= 0 {
		t.Error("a pool lifetime constant is not positive, so connections would never be recycled")
	}
}

func TestLoadMigrations(t *testing.T) {
	t.Parallel()

	files, err := loadMigrations()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no migration is embedded")
	}
	for i, m := range files {
		if m.version != i+1 {
			t.Errorf("migration %s has version %d at place %d", m.name, m.version, i+1)
		}
		if strings.TrimSpace(m.sql) == "" {
			t.Errorf("migration %s is empty", m.name)
		}
	}
}

// Every table the issue names has to be in the first migration.
func TestTheSchemaCreatesEveryTable(t *testing.T) {
	t.Parallel()

	files, err := loadMigrations()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var all strings.Builder
	for _, m := range files {
		all.WriteString(strings.ToLower(m.sql))
	}
	for _, table := range []string{"owners", "lists", "wishes", "reservations", "comments"} {
		if !strings.Contains(all.String(), "create table "+table+" ") {
			t.Errorf("no migration creates the %s table", table)
		}
	}
}

// The constraints the schema carries. This reads the SQL text and needs no
// server, so deleting one fails here at once. db_test.go proves each one
// against a real PostgreSQL.
func TestTheSchemaDeclaresItsConstraints(t *testing.T) {
	t.Parallel()

	files, err := loadMigrations()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var sb strings.Builder
	for _, m := range files {
		sb.WriteString(strings.ToLower(m.sql))
	}
	// Whitespace collapsed, so the column alignment in the SQL does not matter.
	schema := strings.Join(strings.Fields(sb.String()), " ")

	for _, name := range []string{
		"lists_title_not_empty",
		"wishes_title_not_empty", "wishes_priority_range", "wishes_quantity_positive",
		"wishes_price_not_negative", "wishes_currency_code",
		"reservations_state", "reservations_quantity_positive", "reservations_amount_positive",
		"reservations_guest_idx",
		"comments_author_kind", "comments_visibility", "comments_body_not_empty",
		"comments_owner_replies_visibly",
	} {
		if !strings.Contains(schema, name) {
			t.Errorf("the schema no longer declares %s", name)
		}
	}

	for _, fk := range []string{
		"owner_id bigint not null references owners (id) on delete cascade",
		"list_id bigint not null references lists (id) on delete cascade",
		"wish_id bigint not null references wishes (id) on delete cascade",
	} {
		// wishes' reservations and comments both reference wishes.
		want := 1
		if strings.HasPrefix(fk, "wish_id") {
			want = 2
		}
		if got := strings.Count(schema, fk); got != want {
			t.Errorf("foreign key %q appears %d times, want %d", fk, got, want)
		}
	}
	for _, unique := range []string{"subject text not null unique", "share_token text unique"} {
		if !strings.Contains(schema, unique) {
			t.Errorf("the schema no longer declares the unique column %q", unique)
		}
	}
}

func TestOpenDoesNotConnect(t *testing.T) {
	t.Parallel()

	s, err := Open(t.Context(), unreachable(t))
	if err != nil {
		t.Fatalf("Open() error = %v, want nil: the pool connects lazily", err)
	}
	s.Close()
}

// Readiness depends on Ping: an unreachable database has to be an error, and
// the error must not carry the password.
func TestPingFailsWhileTheDatabaseIsUnreachable(t *testing.T) {
	t.Parallel()

	s, err := Open(t.Context(), unreachable(t))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err = s.Ping(ctx)
	if err == nil {
		t.Fatal("Ping() error = nil, want an error for an unreachable database")
	}
	if strings.Contains(err.Error(), dummyPassword) {
		t.Errorf("Ping() error leaks the password: %v", err)
	}
}

func TestMigrateFailsWithoutALeakWhileTheDatabaseIsUnreachable(t *testing.T) {
	t.Parallel()

	s, err := Open(t.Context(), unreachable(t))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err = s.Migrate(ctx)
	if err == nil {
		t.Fatal("Migrate() error = nil, want an error for an unreachable database")
	}
	if strings.Contains(err.Error(), dummyPassword) {
		t.Errorf("Migrate() error leaks the password: %v", err)
	}
	if s.migrated.Load() {
		t.Error("the store claims to be migrated after a failed Migrate")
	}
}

// MigrateUntilReady keeps retrying and stops with the context.
func TestMigrateUntilReadyStopsWithItsContext(t *testing.T) {
	t.Parallel()

	s, err := Open(t.Context(), unreachable(t))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	logger := slog.New(slog.DiscardHandler)

	done := make(chan error, 1)
	go func() { done <- s.MigrateUntilReady(ctx, logger) }()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("MigrateUntilReady() error = %v, want the context's deadline", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("MigrateUntilReady() did not stop when its context ended")
	}
}

// A port number is only a number to the config, but the DSN has to join it
// the IPv6 safe way.
func TestDSNJoinsAnIPv6HostSafely(t *testing.T) {
	t.Parallel()

	got := dsn(config.Database{Host: "::1", Port: 5432, Name: "w", User: "u", SSLMode: "disable"})
	if want := "[::1]:" + strconv.Itoa(5432); !strings.Contains(got, want) {
		t.Errorf("DSN %s does not contain %s", got, want)
	}
}
