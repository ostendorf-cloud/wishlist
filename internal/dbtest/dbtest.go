// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package dbtest gives a test its own database on a real PostgreSQL server.
//
// The server's address comes from the environment variable named by EnvURL.
// Tests that need a server call Database. Locally, where no server is
// configured, they skip with a message naming the missing variable. In CI,
// where the variable must be set, a missing value fails the test instead, so
// the suite can never go green by skipping everything.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ostendorf-cloud/wishlist/internal/config"
)

// EnvURL names the variable holding the test server's URL, for example
// postgres://user:password@localhost:5432/postgres?sslmode=disable. The role
// needs the CREATEDB privilege: every test gets a database of its own.
const EnvURL = "WISHLIST_TEST_DATABASE_URL"

// Database creates an empty database on the configured server and returns
// the settings that reach it. The database is dropped when the test ends.
//
// With no server configured the test is skipped, loudly, unless the CI
// environment variable is set, in which case it fails.
func Database(t *testing.T) config.Database {
	t.Helper()

	raw := os.Getenv(EnvURL)
	if raw == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s is not set in CI: the PostgreSQL service container is missing from the workflow", EnvURL)
		}
		t.Skipf("SKIPPED, NOT RUN: %s is not set, so this test of the real PostgreSQL store did not run. "+
			"Start a server (docker compose up -d postgres) and set it, see AGENTS.md.", EnvURL)
	}

	admin := parse(t, raw)
	name := "wishlist_test_" + randomSuffix(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatalf("connect to the test server named by %s: %v", EnvURL, err)
	}
	defer func() { _ = conn.Close(ctx) }()

	if _, err := conn.Exec(ctx, "create database "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c, err := pgx.Connect(cleanup, raw)
		if err != nil {
			t.Errorf("connect to drop %s: %v", name, err)
			return
		}
		defer func() { _ = c.Close(cleanup) }()
		if _, err := c.Exec(cleanup, "drop database if exists "+pgx.Identifier{name}.Sanitize()+" with (force)"); err != nil {
			t.Errorf("drop test database %s: %v", name, err)
		}
	})

	admin.Name = name
	return admin
}

// Unreachable returns settings for a port nothing listens on: the real client
// meets a real refusal, with no server and no fake. It is for the error paths
// of the client and runs everywhere. The password is an obvious dummy.
func Unreachable(t *testing.T) config.Database {
	t.Helper()

	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address is a %T, want a TCP address", l.Addr())
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return config.Database{
		Host: "127.0.0.1", Port: addr.Port, Name: "wishlist", User: "wishlist",
		Password: UnreachablePassword, SSLMode: "disable", MaxConns: 3, ConnectTimeout: time.Second,
	}
}

// UnreachablePassword is the dummy password of Unreachable, for tests that
// prove it never shows up in an error or a response.
const UnreachablePassword = "dummy-password-not-real"

func parse(t *testing.T, raw string) config.Database {
	t.Helper()

	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User == nil {
		t.Fatalf("%s must be a postgres:// URL with a user, a host and a password", EnvURL)
	}
	port := 5432
	if p := u.Port(); p != "" {
		if port, err = strconv.Atoi(p); err != nil {
			t.Fatalf("%s has a port that is not a number", EnvURL)
		}
	}
	password, _ := u.User.Password()
	sslmode := u.Query().Get("sslmode")
	if sslmode == "" {
		sslmode = "prefer"
	}
	return config.Database{
		Host:           u.Hostname(),
		Port:           port,
		User:           u.User.Username(),
		Password:       password,
		SSLMode:        sslmode,
		MaxConns:       4,
		ConnectTimeout: 5 * time.Second,
	}
}

func randomSuffix(t *testing.T) string {
	t.Helper()

	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random suffix: %v", err)
	}
	return hex.EncodeToString(b)
}
