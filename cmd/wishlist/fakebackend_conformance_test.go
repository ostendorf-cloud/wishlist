// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/account/accounttest"

	"github.com/ostendorf-cloud/wishlist/internal/accounts"
	"github.com/ostendorf-cloud/wishlist/internal/store"
)

// The sign in tests trust fakeBackend, so the fake has to pass the same suite
// as the SQL store (TestStoreConformance in internal/accounts).
func TestTheFakeBackendPassesTheConformanceSuite(t *testing.T) {
	t.Parallel()
	fake := newFakeBackend()
	accounttest.Run(t, accounttest.Harness{
		NewStore:  func(*testing.T) account.Store { return accounts.New(fake) },
		FoldsCase: true,
		Disable: func(t *testing.T, _ account.Store, id string) {
			t.Helper()
			n, ok := accounts.OwnerID(id)
			if !ok {
				t.Fatalf("id %q is not an owner id", id)
			}
			// The fake has no disabled flag: an empty hash is the contract's
			// other way to refuse a login.
			if err := fake.change(n, func(a *store.Account) bool { a.PasswordHash = ""; return true }); err != nil {
				t.Fatal(err)
			}
		},
	})
}
