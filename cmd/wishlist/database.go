// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ostendorf-cloud/govue/health"

	"github.com/ostendorf-cloud/wishlist/internal/config"
	"github.com/ostendorf-cloud/wishlist/internal/store"
)

// databaseCheckTimeout bounds one readiness probe of the database.
const databaseCheckTimeout = 2 * time.Second

// startDatabase builds the pool, registers the readiness check and applies the
// schema in the background. The check reports unready while the database is
// unreachable and until the schema is applied. Liveness is not tied to the
// database: restarting a pod does not bring a database back.
//
// The returned wait function blocks until the background migration has
// returned, which main calls after cancelling ctx so the pool is not closed
// under a running migration.
func startDatabase(ctx context.Context, cfg config.Database, logger *slog.Logger, checks *health.Registry) (db *store.Store, wait func(), err error) {
	db, err = store.Open(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("wishlist: %w", err)
	}

	checks.Add("database", databaseCheckTimeout, db.Ping)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := db.MigrateUntilReady(ctx, logger); err != nil && !errors.Is(err, context.Canceled) {
			logger.ErrorContext(ctx, "schema migration stopped", "error", err.Error())
		}
	}()
	return db, func() { <-done }, nil
}
