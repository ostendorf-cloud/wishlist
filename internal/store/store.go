// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package store is the whole database access of the service: the schema, the
// owner's queries and the guest's queries.
//
// The two sides are separate types. OwnerStore, in owner.go, has every query
// the owner of a list may run. GuestStore, in guest.go, has the guests' queries
// and is the only code that names the reservations table. Both are views on one
// Core, which owns the pool and the lifecycle (Open, Close, Ping, Migrate). A
// handler for the owner is given an OwnerStore and nothing else, so it cannot
// call a guest method: the compiler enforces it. boundary_test.go enforces the
// file side of the same rule (docs/adr/0001-blind-reservations.md).
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

// Core holds the pool and the lifecycle. It has no query. Owners and Guests
// hand out the two sides, which share the pool and nothing else.
type Core struct {
	pool     *pgxpool.Pool
	migrated atomic.Bool
	// pause is the first wait between two migration attempts. Zero means the
	// default. Tests set it.
	pause time.Duration
}

// OwnerStore is what the owner of a list may run. It never touches the
// reservations table.
type OwnerStore struct {
	pool *pgxpool.Pool
}

// GuestStore is what a guest may run, reservations included.
type GuestStore struct {
	pool *pgxpool.Pool
}

// Owners returns the owner's side.
func (c *Core) Owners() *OwnerStore { return &OwnerStore{pool: c.pool} }

// Guests returns the guest's side.
func (c *Core) Guests() *GuestStore { return &GuestStore{pool: c.pool} }

// dsn is the connection string without the password, which is set on the
// parsed config instead: a password with a %, @ or : in it would otherwise
// have to be escaped correctly in two places, and a DSN is the thing that
// gets logged.
func dsn(cfg config.Database) string {
	q := url.Values{}
	q.Set("sslmode", cfg.SSLMode)
	if cfg.SSLRootCert != "" {
		q.Set("sslrootcert", cfg.SSLRootCert)
	}
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
func Open(ctx context.Context, cfg config.Database) (*Core, error) {
	pc, err := poolConfig(cfg)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("database pool: %w", err)
	}
	return &Core{pool: pool}, nil
}

// Close waits for the pool's connections to be returned and closes them.
func (c *Core) Close() {
	c.pool.Close()
}

// Ping is what the readiness probe answers from: a pod that cannot reach its
// database, or whose schema is not applied yet, serves nothing useful.
func (c *Core) Ping(ctx context.Context) error {
	if err := c.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	if !c.migrated.Load() {
		return ErrNotMigrated
	}
	return nil
}

// runTx runs fn in a transaction and rolls back on any error or panic.
func runTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
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

func (c *Core) tx(ctx context.Context, fn func(pgx.Tx) error) error {
	return runTx(ctx, c.pool, fn)
}

// notFound maps pgx' no rows to the package's own error, so callers never
// import pgx.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
