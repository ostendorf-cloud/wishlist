// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package store is the whole database access of the service: the schema, the
// owner's queries and the guest's queries.
//
// The two sides are separated on purpose. Only guest.go may name the
// reservations table, because the owner of a list must not learn what was
// taken (docs/adr/0001-blind-reservations.md). boundary_test.go holds every
// other file to it. Everything the owner reads or writes is in owner.go.
package store

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ostendorf-cloud/wishlist/internal/config"
)

// Pool lifetimes. A connection the pool keeps for hours survives a failover
// of the cluster only as a broken socket, so connections are recycled.
const (
	maxConnLifetime   = 30 * time.Minute
	maxConnIdleTime   = 5 * time.Minute
	healthCheckPeriod = time.Minute
)

// ErrNotFound is what every lookup returns for a row that is not there, or for
// a row of another owner: a list somebody else owns is indistinguishable from
// one that does not exist.
var ErrNotFound = errors.New("not found")

// ErrNotMigrated is what Ping returns while the schema is not applied yet.
var ErrNotMigrated = errors.New("schema not applied yet")

// Store holds the pool. Every method takes a context and is safe for
// concurrent use, as several pods and several requests share one database.
type Store struct {
	pool     *pgxpool.Pool
	migrated atomic.Bool
}

// dsn is the connection string without the password, which is set on the
// parsed config instead: a password with a %, @ or : in it would otherwise
// have to be escaped correctly in two places, and a DSN is the thing that
// gets logged.
func dsn(cfg config.Database) string {
	q := url.Values{}
	q.Set("sslmode", cfg.SSLMode)
	u := url.URL{
		Scheme:   "postgres",
		User:     url.User(cfg.User),
		Host:     net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Path:     "/" + cfg.Name,
		RawQuery: q.Encode(),
	}
	return u.String()
}

// poolConfig turns the validated settings into the pool's configuration.
func poolConfig(cfg config.Database) (*pgxpool.Config, error) {
	pc, err := pgxpool.ParseConfig(dsn(cfg))
	if err != nil {
		return nil, fmt.Errorf("database config: %w", err)
	}
	pc.ConnConfig.Password = cfg.Password
	pc.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	pc.MaxConns = cfg.MaxConns
	pc.MaxConnLifetime = maxConnLifetime
	pc.MaxConnIdleTime = maxConnIdleTime
	pc.HealthCheckPeriod = healthCheckPeriod
	return pc, nil
}

// Open builds the pool. It does not connect: the first query does, so a
// database that is still starting does not stop the pod. Readiness reports
// the gap through Ping.
func Open(ctx context.Context, cfg config.Database) (*Store, error) {
	pc, err := poolConfig(cfg)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("database pool: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close waits for the pool's connections to be returned and closes them.
func (s *Store) Close() {
	s.pool.Close()
}

// Ping is what the readiness probe answers from: a pod that cannot reach its
// database, or whose schema is not applied yet, serves nothing useful.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	if !s.migrated.Load() {
		return ErrNotMigrated
	}
	return nil
}

// tx runs fn in a transaction and rolls back on any error or panic.
func (s *Store) tx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() {
		// The rollback of a committed transaction is a no-op, so this needs no
		// flag; an error here is only interesting when fn already failed.
		_ = tx.Rollback(ctx)
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// notFound maps pgx' no rows to the package's own error, so callers never
// import pgx.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
