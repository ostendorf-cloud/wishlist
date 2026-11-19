// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package store

// Against a real PostgreSQL, like db_test.go: what a registration writes into
// the owner row, and which unconfirmed accounts the next signup deletes.

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestARegistrationStoresNoLoginInTheOwnerRow(t *testing.T) {
	c := newCore(t)
	ctx := t.Context()
	const login = "alice@example.invalid"

	a, err := c.Accounts().CreateAccount(ctx, login, login, "hash", "SECRET")
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}
	var subject, username, display, email string
	if err := c.pool.QueryRow(ctx,
		`select subject, username, display_name, email from owners where id = $1`, a.ID,
	).Scan(&subject, &username, &display, &email); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("owner-%d", a.ID); username != want {
		t.Errorf("username = %q, want %q", username, want)
	}
	if display != "" || email != "" || !strings.HasPrefix(subject, "local:") {
		t.Errorf("owner row = %q %q %q, want empty display name and email, local subject", subject, display, email)
	}

	// The guest side greets with the display name or the username: neither is the login.
	list, err := c.Owners().CreateList(ctx, a.ID, ListInput{Title: "Birthday", ReserveOpen: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Owners().SetShareToken(ctx, a.ID, list.ID, "token-a"); err != nil {
		t.Fatal(err)
	}
	got, err := c.Guests().ListByToken(ctx, "token-a")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(got.OwnerName), "alice") || strings.Contains(got.OwnerName, "@") {
		t.Errorf("the guest sees the owner as %q: the login leaked", got.OwnerName)
	}
}

func TestASignupDeletesStaleUnconfirmedAccountsOnly(t *testing.T) {
	c := newCore(t)
	ctx := t.Context()
	acc := c.Accounts()

	create := func(login string) Account {
		t.Helper()
		a, err := acc.CreateAccount(ctx, login, login, "hash", "SECRET")
		if err != nil {
			t.Fatalf("CreateAccount(%s) error = %v", login, err)
		}
		return a
	}
	age := func(a Account, hours int) {
		t.Helper()
		if _, err := c.pool.Exec(ctx,
			`update owners set created_at = now() - make_interval(hours => $2) where id = $1`,
			a.ID, hours); err != nil {
			t.Fatal(err)
		}
	}

	stale := create("stale@example.invalid")
	young := create("young@example.invalid")
	confirmed := create("confirmed@example.invalid")
	withData := create("data@example.invalid")
	imported, err := c.Owners().UpsertOwner(ctx, "oidc-subject", "oidc", "Oidc", "")
	if err != nil {
		t.Fatal(err)
	}
	// An imported owner that also has a credentials row with counter 0: only
	// the local subject makes an account deletable.
	importedWithCreds, err := c.Owners().UpsertOwner(ctx, "oidc-subject-2", "oidc2", "Oidc Two", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.pool.Exec(ctx, `
		insert into owner_credentials (owner_id, login, login_key, password_hash, totp_secret, totp_counter)
		values ($1, 'imported@example.invalid', 'imported@example.invalid', 'hash', 'SECRET', 0)`,
		importedWithCreds.ID); err != nil {
		t.Fatal(err)
	}
	if err := acc.ConfirmTOTP(ctx, confirmed.ID, "SECRET", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Owners().CreateList(ctx, withData.ID, ListInput{Title: "Mine", ReserveOpen: true}); err != nil {
		t.Fatal(err)
	}
	age(stale, 25)
	age(young, 23)
	age(confirmed, 500)
	age(withData, 500)
	if _, err := c.pool.Exec(ctx, `update owners set created_at = now() - interval '500 hours' where id = $1`, imported.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := c.pool.Exec(ctx, `update owners set created_at = now() - interval '500 hours' where id = $1`, importedWithCreds.ID); err != nil {
		t.Fatal(err)
	}

	create("next@example.invalid")

	if _, err := acc.FindAccount(ctx, "stale@example.invalid"); !errors.Is(err, ErrNotFound) {
		t.Errorf("the stale unconfirmed account is still there: %v", err)
	}
	var n int
	if err := c.pool.QueryRow(ctx, `select count(*) from owners where id = $1`, stale.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("the stale owner row count = %d, %v, want 0", n, err)
	}
	for _, login := range []string{"young@example.invalid", "confirmed@example.invalid", "data@example.invalid"} {
		if _, err := acc.FindAccount(ctx, login); err != nil {
			t.Errorf("%s was deleted: %v", login, err)
		}
	}
	if err := c.pool.QueryRow(ctx, `select count(*) from owners where id = $1`, imported.ID).Scan(&n); err != nil || n != 1 {
		t.Errorf("an owner without local credentials was deleted: %d, %v", n, err)
	}
	if err := c.pool.QueryRow(ctx, `select count(*) from owners where id = $1`, importedWithCreds.ID).Scan(&n); err != nil || n != 1 {
		t.Errorf("an old unconfirmed owner with a non local subject was deleted: %d, %v", n, err)
	}
	if _, err := acc.FindAccount(ctx, "imported@example.invalid"); err != nil {
		t.Errorf("the credentials of the non local owner are gone: %v", err)
	}
	// The login of the deleted account can register again.
	if _, err := acc.CreateAccount(ctx, "stale@example.invalid", "stale@example.invalid", "hash", "SECRET"); err != nil {
		t.Errorf("registering the freed login failed: %v", err)
	}
}
