// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The migrations are plain SQL files, applied in order by the small runner
// below, the way the predecessor service did. A migration library would add a
// dependency and a second dialect for a schema that is a handful of files.
//
//go:embed migrations/*.sql
var migrations embed.FS

// migrationLock is an arbitrary but fixed key. Every pod runs Migrate at
// start, and the advisory lock makes the second one wait instead of applying
// the same file twice.
const migrationLock = 8153_2026

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads the embedded files and checks their numbering: the
// versions start at 1 and have no gap and no repeat.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var out []migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		// 0001_init.sql: the number orders them, the rest is for the reader.
		number, _, found := strings.Cut(strings.TrimSuffix(entry.Name(), ".sql"), "_")
		if !found {
			return nil, fmt.Errorf("migration %s is not named <version>_<name>.sql", entry.Name())
		}
		version, err := strconv.Atoi(number)
		if err != nil || version < 1 {
			return nil, fmt.Errorf("migration %s has no version number", entry.Name())
		}
		body, err := migrations.ReadFile(path.Join("migrations", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		out = append(out, migration{version: version, name: entry.Name(), sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf("migration versions have a gap or a repeat at %s", m.name)
		}
	}
	return out, nil
}

// Migrate applies every migration that is not recorded yet, each in its own
// transaction together with its bookkeeping row: a file that fails leaves
// nothing half applied. After it returns nil, Ping stops reporting
// ErrNotMigrated.
func (s *Store) Migrate(ctx context.Context) error {
	files, err := loadMigrations()
	if err != nil {
		return err
	}

	// Inside the lock as well: two pods starting together would otherwise race
	// on create table if not exists, which PostgreSQL answers with a duplicate
	// key error on its catalogue rather than with a no-op.
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", migrationLock); err != nil {
			return fmt.Errorf("lock: %w", err)
		}
		_, err := tx.Exec(ctx, `
			create table if not exists schema_migrations (
				version    integer     primary key,
				name       text        not null,
				applied_at timestamptz not null default now()
			)`)
		return err
	})
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	for _, m := range files {
		if err := s.applyMigration(ctx, m); err != nil {
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
	}
	s.migrated.Store(true)
	return nil
}

func (s *Store) applyMigration(ctx context.Context, m migration) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		// Held until the transaction ends, so two pods starting together apply
		// the file once.
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", migrationLock); err != nil {
			return fmt.Errorf("lock: %w", err)
		}
		var applied bool
		if err := tx.QueryRow(ctx,
			"select exists (select 1 from schema_migrations where version = $1)", m.version,
		).Scan(&applied); err != nil {
			return fmt.Errorf("check: %w", err)
		}
		if applied {
			return nil
		}
		if _, err := tx.Exec(ctx, m.sql); err != nil {
			return fmt.Errorf("apply: %w", err)
		}
		if _, err := tx.Exec(ctx,
			"insert into schema_migrations (version, name) values ($1, $2)", m.version, m.name,
		); err != nil {
			return fmt.Errorf("record: %w", err)
		}
		return nil
	})
}

// MigrateUntilReady applies the schema at startup, retrying with a growing
// pause while the database is not there yet, and returns once it is applied
// or ctx ends. It never gives up on its own: a database that starts after the
// pod does is the normal case in a cluster, and readiness reports the gap.
func (s *Store) MigrateUntilReady(ctx context.Context, logger *slog.Logger) error {
	const maxWait = 30 * time.Second
	wait := time.Second
	for {
		attempt, cancel := context.WithTimeout(ctx, time.Minute)
		err := s.Migrate(attempt)
		cancel()
		if err == nil {
			logger.InfoContext(ctx, "schema applied")
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("migrate: %w", ctx.Err())
		}
		logger.WarnContext(ctx, "schema not applied yet", "error", err.Error(), "retry_in", wait.String())
		select {
		case <-ctx.Done():
			return fmt.Errorf("migrate: %w", ctx.Err())
		case <-time.After(wait):
		}
		wait = min(wait*2, maxWait)
	}
}
