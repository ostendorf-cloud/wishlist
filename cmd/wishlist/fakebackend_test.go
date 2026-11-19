// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"sync"
	"time"

	"github.com/ostendorf-cloud/wishlist/internal/accounts"
	"github.com/ostendorf-cloud/wishlist/internal/store"
)

// fakeBackend is the credentials side of the store in memory, with the same
// guards as the SQL: it lets the sign in tests run without a database. The SQL
// itself is proven by the conformance suite in internal/accounts, which needs
// PostgreSQL.
type fakeBackend struct {
	mu    sync.Mutex
	next  int64
	byID  map[int64]*store.Account
	byKey map[string]int64

	// now is the clock of the 24 hour cleanup. Nil is time.Now.
	now     func() time.Time
	created map[int64]time.Time
	// hasData marks an owner that has lists: it is never cleaned up.
	hasData map[int64]bool
}

var _ accounts.Backend = (*fakeBackend)(nil)

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		byID:    map[int64]*store.Account{},
		byKey:   map[string]int64{},
		created: map[int64]time.Time{},
		hasData: map[int64]bool{},
	}
}

func (f *fakeBackend) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

func (f *fakeBackend) FindAccount(_ context.Context, key string) (store.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.byKey[key]
	if !ok {
		return store.Account{}, store.ErrNotFound
	}
	return *f.byID[id], nil
}

func (f *fakeBackend) CreateAccount(_ context.Context, login, key, hash, secret string) (store.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// The rule of the SQL: unconfirmed, older than the TTL, no data.
	for id, a := range f.byID {
		if a.TOTPCounter == 0 && !f.hasData[id] && f.clock().Sub(f.created[id]) > store.UnconfirmedTTL {
			delete(f.byKey, accounts.FoldLogin(a.Login))
			delete(f.byID, id)
			delete(f.created, id)
		}
	}
	if _, taken := f.byKey[key]; taken {
		return store.Account{}, store.ErrLoginTaken
	}
	f.next++
	a := &store.Account{ID: f.next, Login: login, PasswordHash: hash, TOTPSecret: secret}
	f.byID[a.ID] = a
	f.byKey[key] = a.ID
	f.created[a.ID] = f.clock()
	return *a, nil
}

func (f *fakeBackend) change(id int64, fn func(*store.Account) bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.byID[id]
	if !ok || !fn(a) {
		return store.ErrNotFound
	}
	return nil
}

func (f *fakeBackend) AdvanceTOTPCounter(_ context.Context, id, counter int64) error {
	return f.change(id, func(a *store.Account) bool {
		if a.TOTPCounter <= 0 || a.TOTPCounter >= counter {
			return false
		}
		a.TOTPCounter = counter
		return true
	})
}

func (f *fakeBackend) ConfirmTOTP(_ context.Context, id int64, secret string, counter int64) error {
	return f.change(id, func(a *store.Account) bool {
		if a.TOTPSecret == "" || a.TOTPSecret != secret || a.TOTPCounter != 0 || counter <= 0 {
			return false
		}
		a.TOTPCounter = counter
		return true
	})
}

func (f *fakeBackend) ReplaceTOTPSecret(_ context.Context, id int64, secret string) error {
	return f.change(id, func(a *store.Account) bool {
		if a.TOTPCounter != 0 {
			return false
		}
		a.TOTPSecret = secret
		return true
	})
}

func (f *fakeBackend) SetPasswordHash(_ context.Context, id int64, oldHash, newHash string) error {
	return f.change(id, func(a *store.Account) bool {
		if a.PasswordHash == "" || a.PasswordHash != oldHash {
			return false
		}
		a.PasswordHash = newHash
		return true
	})
}
