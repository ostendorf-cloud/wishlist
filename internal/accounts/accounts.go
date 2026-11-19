// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package accounts adapts the credentials in internal/store to govue's
// account.Store. The account id is the owners row id, as a decimal string.
// It is never an email address and never a provider subject.
package accounts

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ostendorf-cloud/govue/account"

	"github.com/ostendorf-cloud/wishlist/internal/store"
)

// Backend is the credentials side of the store. *store.AccountStore satisfies it.
type Backend interface {
	FindAccount(ctx context.Context, loginKey string) (store.Account, error)
	CreateAccount(ctx context.Context, login, loginKey, passwordHash, totpSecret string) (store.Account, error)
	AdvanceTOTPCounter(ctx context.Context, id, counter int64) error
	ConfirmTOTP(ctx context.Context, id int64, secret string, counter int64) error
	ReplaceTOTPSecret(ctx context.Context, id int64, secret string) error
	SetPasswordHash(ctx context.Context, id int64, oldHash, newHash string) error
}

var _ Backend = (*store.AccountStore)(nil)

// Store implements account.Store.
type Store struct {
	backend Backend
}

var _ account.Store = (*Store)(nil)

// New returns the adapter over a backend.
func New(b Backend) *Store { return &Store{backend: b} }

// FoldLogin is the one rule that decides when two logins are the same account:
// lower case, no trimming (the service refuses padded logins). The store key,
// and the rate limit per login, both use it.
func FoldLogin(login string) string { return strings.ToLower(login) }

// ID renders an owner id as the account id of the contract.
func ID(ownerID int64) string { return strconv.FormatInt(ownerID, 10) }

// OwnerID parses an account id. A value that is not a positive decimal number
// is not an id.
func OwnerID(id string) (int64, bool) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n <= 0 || strconv.FormatInt(n, 10) != id {
		return 0, false
	}
	return n, true
}

func record(a store.Account) account.Record {
	return account.Record{
		ID:           ID(a.ID),
		Login:        a.Login,
		PasswordHash: a.PasswordHash,
		TOTPSecret:   a.TOTPSecret,
		TOTPCounter:  a.TOTPCounter,
	}
}

// FindByLogin implements account.Store.
func (s *Store) FindByLogin(ctx context.Context, login string) (account.Record, error) {
	a, err := s.backend.FindAccount(ctx, FoldLogin(login))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return account.Record{}, account.ErrNotFound
		}
		return account.Record{}, fmt.Errorf("accounts: find: %w", err)
	}
	return record(a), nil
}

// Create implements account.Store. The id and the counter of rec are ignored.
func (s *Store) Create(ctx context.Context, rec account.Record) (account.Record, error) {
	a, err := s.backend.CreateAccount(ctx, rec.Login, FoldLogin(rec.Login), rec.PasswordHash, rec.TOTPSecret)
	if err != nil {
		if errors.Is(err, store.ErrLoginTaken) {
			return account.Record{}, account.ErrConflict
		}
		return account.Record{}, fmt.Errorf("accounts: create: %w", err)
	}
	return record(a), nil
}

// write maps the result of a conditional write. An id that is no owner id
// changes nothing, which is a refusal like any other.
func write(err error, what string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return account.ErrNotFound
	}
	return fmt.Errorf("accounts: %s: %w", what, err)
}

// AdvanceTOTPCounter implements account.Store.
func (s *Store) AdvanceTOTPCounter(ctx context.Context, id string, counter int64) error {
	n, ok := OwnerID(id)
	if !ok {
		return account.ErrNotFound
	}
	return write(s.backend.AdvanceTOTPCounter(ctx, n, counter), "advance counter")
}

// ConfirmTOTP implements account.Store.
func (s *Store) ConfirmTOTP(ctx context.Context, id, secret string, counter int64) error {
	n, ok := OwnerID(id)
	if !ok {
		return account.ErrNotFound
	}
	return write(s.backend.ConfirmTOTP(ctx, n, secret, counter), "confirm totp")
}

// ReplaceTOTPSecret implements account.Store.
func (s *Store) ReplaceTOTPSecret(ctx context.Context, id, secret string) error {
	n, ok := OwnerID(id)
	if !ok {
		return account.ErrNotFound
	}
	return write(s.backend.ReplaceTOTPSecret(ctx, n, secret), "replace totp secret")
}

// SetPasswordHash implements account.Store.
func (s *Store) SetPasswordHash(ctx context.Context, id, oldHash, newHash string) error {
	n, ok := OwnerID(id)
	if !ok {
		return account.ErrNotFound
	}
	return write(s.backend.SetPasswordHash(ctx, n, oldHash, newHash), "set password hash")
}
