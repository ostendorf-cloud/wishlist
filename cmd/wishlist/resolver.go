// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/ostendorf-cloud/wishlist/internal/store"
	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner"
)

// ownerUpserter is the one store call the development owner needs.
type ownerUpserter interface {
	UpsertOwner(ctx context.Context, subject, username, displayName, email string) (store.Owner, error)
}

// resolveOwner is the place the owner's sign in plugs in.
//
// Until issue 4 lands, no request has an owner: the resolver answers false and
// every owner endpoint answers 401. Issue 4 replaces the first return below
// with a resolver that reads its session (govue's session package: the session
// of the request, then UpsertOwner by subject, then the owner id), and passes
// the matching csrf.Expected to webapp.Config.CSRFExpected in buildHandler.
//
// With devOwner set the resolver instead signs every request in as that one
// account, created on first use. config.Load has already refused the setting
// unless the listener is loopback, or the operator vouched for it.
func resolveOwner(devOwner string, owners ownerUpserter, logger *slog.Logger) owner.Resolver {
	if devOwner == "" {
		return func(*http.Request) (int64, bool) { return 0, false }
	}
	logger.Warn("development owner is on: every request is signed in as one account, there is no password",
		"owner", devOwner)

	var id atomic.Int64
	return func(r *http.Request) (int64, bool) {
		if known := id.Load(); known != 0 {
			return known, true
		}
		// The database may still be starting: the next request tries again.
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		account, err := owners.UpsertOwner(ctx, "dev:"+devOwner, devOwner, devOwner, "")
		if err != nil {
			logger.DebugContext(r.Context(), "development owner not ready", "error", err.Error())
			return 0, false
		}
		id.Store(account.ID)
		return account.ID, true
	}
}
