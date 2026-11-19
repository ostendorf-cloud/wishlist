// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package accounts_test

// TestStoreConformance runs govue's conformance suite against a real
// PostgreSQL, see internal/dbtest: it skips locally without a server and fails
// in CI without one. The other tests need no database.

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/account/accounttest"

	"github.com/ostendorf-cloud/wishlist/internal/accounts"
	"github.com/ostendorf-cloud/wishlist/internal/dbtest"
	"github.com/ostendorf-cloud/wishlist/internal/store"
)

func TestStoreConformance(t *testing.T) {
	cfg := dbtest.Database(t)
	core, err := store.Open(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(core.Close)
	if err := core.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	dsn := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Path:     "/" + cfg.Name,
		RawQuery: url.Values{"sslmode": {cfg.SSLMode}}.Encode(),
	}
	conn, err := pgx.Connect(t.Context(), dsn.String())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })

	accounttest.Run(t, accounttest.Harness{
		NewStore:  func(*testing.T) account.Store { return accounts.New(core.Accounts()) },
		FoldsCase: true,
		Disable: func(t *testing.T, _ account.Store, id string) {
			t.Helper()
			n, ok := accounts.OwnerID(id)
			if !ok {
				t.Fatalf("id %q is not an owner id", id)
			}
			if _, err := conn.Exec(t.Context(),
				"update owner_credentials set disabled = true where owner_id = $1", n); err != nil {
				t.Fatalf("disable: %v", err)
			}
		},
	})
}

func TestOwnerIDOnlyAcceptsPlainPositiveNumbers(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"1", "42"} {
		if _, ok := accounts.OwnerID(id); !ok {
			t.Errorf("OwnerID(%q) is not an id", id)
		}
	}
	for _, id := range []string{
		"", "0", "-1", "+1", "01", "1 ", " 1", "local:1", "alice@example.com", "9223372036854775808",
	} {
		if _, ok := accounts.OwnerID(id); ok {
			t.Errorf("OwnerID(%q) is an id", id)
		}
	}
	if got := accounts.ID(7); got != "7" {
		t.Errorf("ID(7) = %q", got)
	}
}

func TestFoldLoginIsCaseOnly(t *testing.T) {
	t.Parallel()
	if accounts.FoldLogin("Alice@Example.COM") != "alice@example.com" {
		t.Error("case is not folded")
	}
	if accounts.FoldLogin(" a") == accounts.FoldLogin("a") {
		t.Error("padding must not fold away")
	}
}

type failing struct{ accounts.Backend }

func (failing) FindAccount(context.Context, string) (store.Account, error) {
	return store.Account{}, errors.New("database down")
}

func (failing) AdvanceTOTPCounter(context.Context, int64, int64) error { return store.ErrNotFound }

// A store failure is not "no such account": the service must see the
// difference, or an outage looks like a wrong password.
func TestAStoreFailureIsNotANotFound(t *testing.T) {
	t.Parallel()
	s := accounts.New(failing{})
	if _, err := s.FindByLogin(t.Context(), "a@example.com"); err == nil || errors.Is(err, account.ErrNotFound) {
		t.Errorf("FindByLogin error = %v, want a failure that is not ErrNotFound", err)
	}
	if err := s.AdvanceTOTPCounter(t.Context(), "1", 5); !errors.Is(err, account.ErrNotFound) {
		t.Errorf("a refused conditional write = %v, want ErrNotFound", err)
	}
	if err := s.AdvanceTOTPCounter(t.Context(), "not-an-id", 5); !errors.Is(err, account.ErrNotFound) {
		t.Errorf("a bad id = %v, want ErrNotFound", err)
	}
}
