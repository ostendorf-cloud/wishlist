// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package store

// The way in of an owner: login, password hash and TOTP secret, in
// owner_credentials beside the owners row. internal/accounts adapts this to
// govue's account.Store and runs govue's conformance suite against it.
//
// Every write that guards a security property is one conditional statement:
// the comparison is in the WHERE clause, never read in Go and written later.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrLoginTaken is what CreateAccount returns for a login that exists.
var ErrLoginTaken = errors.New("login already taken")

// uniqueViolation is PostgreSQL's SQLSTATE for a unique index conflict.
const uniqueViolation = "23505"

// AccountStore is the owners' credentials. It holds the pool and nothing else.
type AccountStore struct {
	pool *pgxpool.Pool
}

// Accounts returns the credentials side.
func (c *Core) Accounts() *AccountStore { return &AccountStore{pool: c.pool} }

// Account is one row of owner_credentials. ID is the owners row id.
type Account struct {
	ID           int64
	Login        string
	PasswordHash string
	TOTPSecret   string
	TOTPCounter  int64
}

// FindAccount returns the account with this folded login key, or ErrNotFound.
// A disabled account is not found.
func (s *AccountStore) FindAccount(ctx context.Context, loginKey string) (Account, error) {
	var a Account
	err := s.pool.QueryRow(ctx, `
		select owner_id, login, password_hash, totp_secret, totp_counter
		from owner_credentials
		where login_key = $1 and not disabled`,
		loginKey,
	).Scan(&a.ID, &a.Login, &a.PasswordHash, &a.TOTPSecret, &a.TOTPCounter)
	if err != nil {
		return Account{}, fmt.Errorf("find account: %w", notFound(err))
	}
	return a, nil
}

// UnconfirmedTTL is how long an account may stay without a confirmed TOTP
// secret before the next signup deletes it. The SQL below spells the same 24
// hours.
const UnconfirmedTTL = 24 * time.Hour

// CreateAccount adds an owner row and its credentials in one transaction and
// returns the new account. The counter is always stored as 0, so the TOTP
// secret starts unconfirmed. The owner row gets a subject and a username of
// its own that hold no login and no address: the guest side shows the username
// when there is no display name, and a guest must never see the login. A login
// that exists is ErrLoginTaken and leaves no owner row behind.
//
// First the transaction deletes stale unconfirmed accounts, so a login that
// was registered and never confirmed is free again after 24 hours.
func (s *AccountStore) CreateAccount(ctx context.Context, login, loginKey, passwordHash, totpSecret string) (Account, error) {
	var a Account
	err := runTx(ctx, s.pool, func(tx pgx.Tx) error {
		// An owner is deleted only when it came from a local registration, its
		// secret was never confirmed, it is older than 24 hours and no list
		// points at it. The credentials go with it (on delete cascade). Lists
		// are the only other table that references owners.
		//
		// Two statements, not one delete with an exists subquery: on READ
		// COMMITTED that subquery could still see the old totp_counter of a
		// confirm that commits meanwhile, and delete a fresh owner. A select
		// for update waits for that confirm and re-checks totp_counter = 0 on
		// the newest row version. The ids are ordered so that two cleanups lock
		// in the same order and cannot deadlock.
		rows, err := tx.Query(ctx, `
			select c.owner_id
			from owner_credentials c
			join owners o on o.id = c.owner_id
			where c.totp_counter = 0
			  and o.subject like 'local:%'
			  and o.created_at < now() - interval '24 hours'
			  and not exists (select 1 from lists l where l.owner_id = o.id)
			order by c.owner_id
			for update of c`)
		if err != nil {
			return fmt.Errorf("select unconfirmed owners: %w", err)
		}
		stale, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return fmt.Errorf("select unconfirmed owners: %w", err)
		}
		if len(stale) > 0 {
			if _, err := tx.Exec(ctx, `delete from owners where id = any($1)`, stale); err != nil {
				return fmt.Errorf("delete unconfirmed owners: %w", err)
			}
		}
		var id int64
		if err := tx.QueryRow(ctx, `
			insert into owners (subject, username, display_name, email)
			values ('local:' || gen_random_uuid()::text, '', '', '')
			returning id`,
		).Scan(&id); err != nil {
			return fmt.Errorf("insert owner: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			update owners set username = 'owner-' || id::text where id = $1`,
			id,
		); err != nil {
			return fmt.Errorf("name owner: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			insert into owner_credentials (owner_id, login, login_key, password_hash, totp_secret, totp_counter)
			values ($1, $2, $3, $4, $5, 0)`,
			id, login, loginKey, passwordHash, totpSecret,
		); err != nil {
			return err
		}
		a = Account{ID: id, Login: login, PasswordHash: passwordHash, TOTPSecret: totpSecret}
		return nil
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return Account{}, ErrLoginTaken
		}
		return Account{}, fmt.Errorf("create account: %w", err)
	}
	return a, nil
}

// affected turns a conditional write into ErrNotFound when no row changed.
func affected(tag pgconn.CommandTag, err error, what string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AdvanceTOTPCounter moves the counter forward. It writes only when the stored
// counter is above zero (a confirmed secret) and lower than counter, so a
// replayed code changes nothing.
func (s *AccountStore) AdvanceTOTPCounter(ctx context.Context, id, counter int64) error {
	tag, err := s.pool.Exec(ctx, `
		update owner_credentials set totp_counter = $2
		where owner_id = $1 and totp_counter > 0 and totp_counter < $2`,
		id, counter)
	return affected(tag, err, "advance totp counter")
}

// ConfirmTOTP confirms the secret. It writes only while exactly this secret is
// stored, the counter is zero and the new counter is above zero.
func (s *AccountStore) ConfirmTOTP(ctx context.Context, id int64, secret string, counter int64) error {
	tag, err := s.pool.Exec(ctx, `
		update owner_credentials set totp_counter = $3
		where owner_id = $1 and totp_secret = $2 and totp_secret <> ''
		  and totp_counter = 0 and $3 > 0`,
		id, secret, counter)
	return affected(tag, err, "confirm totp")
}

// ReplaceTOTPSecret swaps a secret that is not confirmed yet.
func (s *AccountStore) ReplaceTOTPSecret(ctx context.Context, id int64, secret string) error {
	tag, err := s.pool.Exec(ctx, `
		update owner_credentials set totp_secret = $2
		where owner_id = $1 and totp_counter = 0`,
		id, secret)
	return affected(tag, err, "replace totp secret")
}

// SetPasswordHash is a compare and set: it writes only when a hash is stored
// and equals oldHash.
func (s *AccountStore) SetPasswordHash(ctx context.Context, id int64, oldHash, newHash string) error {
	tag, err := s.pool.Exec(ctx, `
		update owner_credentials set password_hash = $3
		where owner_id = $1 and password_hash <> '' and password_hash = $2`,
		id, oldHash, newHash)
	return affected(tag, err, "set password hash")
}
