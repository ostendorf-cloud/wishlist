// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package store

// These tests run against a real PostgreSQL server named by
// WISHLIST_TEST_DATABASE_URL, see internal/dbtest. Without it they skip
// loudly, and in CI they fail instead of skipping.

import (
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ostendorf-cloud/wishlist/internal/dbtest"
)

// newStore opens a store on a fresh database and applies the schema.
func newStore(t *testing.T) *Store {
	t.Helper()

	s, err := Open(t.Context(), dbtest.Database(t))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return s
}

// fixture is one owner with one shared list holding one wish.
type fixture struct {
	owner Owner
	list  List
	wish  Wish
}

func seed(t *testing.T, s *Store) fixture {
	t.Helper()

	owner, err := s.UpsertOwner(t.Context(), "subject-1", "alice", "Alice", "alice@example.invalid")
	if err != nil {
		t.Fatalf("UpsertOwner() error = %v", err)
	}
	list, err := s.CreateList(t.Context(), owner.ID, ListInput{Title: "Birthday", ReserveOpen: true})
	if err != nil {
		t.Fatalf("CreateList() error = %v", err)
	}
	if err := s.SetShareToken(t.Context(), owner.ID, list.ID, "token-1"); err != nil {
		t.Fatalf("SetShareToken() error = %v", err)
	}
	wish, err := s.CreateWish(t.Context(), owner.ID, list.ID, WishInput{Title: "Book", Quantity: 1, Priority: 1})
	if err != nil {
		t.Fatalf("CreateWish() error = %v", err)
	}
	return fixture{owner: owner, list: list, wish: wish}
}

func TestMigrateAppliesTheSchemaOnceAndMakesTheStoreReady(t *testing.T) {
	s, err := Open(t.Context(), dbtest.Database(t))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer s.Close()

	if err := s.Ping(t.Context()); !errors.Is(err, ErrNotMigrated) {
		t.Fatalf("Ping() before Migrate = %v, want ErrNotMigrated", err)
	}
	for range 2 {
		if err := s.Migrate(t.Context()); err != nil {
			t.Fatalf("Migrate() error = %v", err)
		}
	}
	if err := s.Ping(t.Context()); err != nil {
		t.Errorf("Ping() after Migrate = %v, want nil", err)
	}

	var applied int
	if err := s.pool.QueryRow(t.Context(), "select count(*) from schema_migrations").Scan(&applied); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	files, _ := loadMigrations()
	if applied != len(files) {
		t.Errorf("schema_migrations has %d rows, want %d: a second Migrate must not apply twice", applied, len(files))
	}

	for _, table := range []string{"owners", "lists", "wishes", "reservations", "comments"} {
		var exists bool
		if err := s.pool.QueryRow(t.Context(), "select to_regclass($1) is not null", table).Scan(&exists); err != nil || !exists {
			t.Errorf("table %s does not exist after Migrate (err %v)", table, err)
		}
	}
}

// Two pods starting together both run Migrate.
func TestMigrateIsSafeToRunConcurrently(t *testing.T) {
	cfg := dbtest.Database(t)

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := Open(t.Context(), cfg)
			if err != nil {
				errs <- err
				return
			}
			defer s.Close()
			errs <- s.Migrate(t.Context())
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Migrate() error = %v", err)
		}
	}
}

// The schema's own constraints, one violation each. The first column is the
// statement, the second the constraint or column PostgreSQL has to name.
func TestSchemaConstraints(t *testing.T) {
	s := newStore(t)
	f := seed(t, s)

	// A second owner's list and wish exist for the unique cases.
	cases := []struct {
		name       string
		sql        string
		args       []any
		code       string
		constraint string
	}{
		{"owner subject unique", "insert into owners (subject, username) values ('subject-1', 'x')", nil, "23505", "owners_subject_key"},
		{"owner subject required", "insert into owners (subject, username) values (null, 'x')", nil, "23502", ""},
		{"list needs an owner", "insert into lists (owner_id, title) values (999999, 't')", nil, "23503", "lists_owner_id_fkey"},
		{"list title not blank", "insert into lists (owner_id, title) values ($1, '   ')", []any{f.owner.ID}, "23514", "lists_title_not_empty"},
		{"share token unique", "insert into lists (owner_id, title, share_token) values ($1, 't', 'token-1')", []any{f.owner.ID}, "23505", "lists_share_token_key"},
		{"wish needs a list", "insert into wishes (list_id, title) values (999999, 't')", nil, "23503", "wishes_list_id_fkey"},
		{"wish title not blank", "insert into wishes (list_id, title) values ($1, ' ')", []any{f.list.ID}, "23514", "wishes_title_not_empty"},
		{"wish priority range", "insert into wishes (list_id, title, priority) values ($1, 't', 3)", []any{f.list.ID}, "23514", "wishes_priority_range"},
		{"wish quantity positive", "insert into wishes (list_id, title, quantity) values ($1, 't', 0)", []any{f.list.ID}, "23514", "wishes_quantity_positive"},
		{"wish price not negative", "insert into wishes (list_id, title, price_cents) values ($1, 't', -1)", []any{f.list.ID}, "23514", "wishes_price_not_negative"},
		{"wish currency code", "insert into wishes (list_id, title, currency) values ($1, 't', 'eur')", []any{f.list.ID}, "23514", "wishes_currency_code"},
		{"reservation needs a wish", "insert into reservations (wish_id, guest_key) values (999999, 'k')", nil, "23503", "reservations_wish_id_fkey"},
		{"reservation state", "insert into reservations (wish_id, guest_key, state) values ($1, 'k', 'stolen')", []any{f.wish.ID}, "23514", "reservations_state"},
		{"reservation quantity positive", "insert into reservations (wish_id, guest_key, quantity) values ($1, 'k', 0)", []any{f.wish.ID}, "23514", "reservations_quantity_positive"},
		{"reservation amount positive", "insert into reservations (wish_id, guest_key, amount_cents) values ($1, 'k', 0)", []any{f.wish.ID}, "23514", "reservations_amount_positive"},
		{"comment needs a wish", "insert into comments (wish_id, author_kind, body) values (999999, 'guest', 'b')", nil, "23503", "comments_wish_id_fkey"},
		{"comment author kind", "insert into comments (wish_id, author_kind, body) values ($1, 'stranger', 'b')", []any{f.wish.ID}, "23514", "comments_author_kind"},
		{"comment visibility", "insert into comments (wish_id, author_kind, body, visibility) values ($1, 'guest', 'b', 'world')", []any{f.wish.ID}, "23514", "comments_visibility"},
		{"comment body not blank", "insert into comments (wish_id, author_kind, body) values ($1, 'guest', '  ')", []any{f.wish.ID}, "23514", "comments_body_not_empty"},
		{"owner reply visible", "insert into comments (wish_id, author_kind, body, visibility) values ($1, 'owner', 'b', 'guests')", []any{f.wish.ID}, "23514", "comments_owner_replies_visibly"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.pool.Exec(t.Context(), tc.sql, tc.args...)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) {
				t.Fatalf("error = %v, want a PostgreSQL error %s", err, tc.code)
			}
			if pgErr.Code != tc.code {
				t.Errorf("code = %s (%s), want %s", pgErr.Code, pgErr.Message, tc.code)
			}
			if tc.constraint != "" && pgErr.ConstraintName != tc.constraint {
				t.Errorf("constraint = %q, want %q", pgErr.ConstraintName, tc.constraint)
			}
		})
	}

	t.Run("one reservation per guest and wish", func(t *testing.T) {
		const insert = "insert into reservations (wish_id, guest_key) values ($1, 'same-guest')"
		if _, err := s.pool.Exec(t.Context(), insert, f.wish.ID); err != nil {
			t.Fatalf("first insert: %v", err)
		}
		_, err := s.pool.Exec(t.Context(), insert, f.wish.ID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "reservations_guest_idx" {
			t.Errorf("error = %v, want a violation of reservations_guest_idx", err)
		}
	})
}

func TestDeletingAListCascadesToEverythingBelowIt(t *testing.T) {
	s := newStore(t)
	f := seed(t, s)

	if err := s.Reserve(t.Context(), f.list.ID, f.wish.ID, "guest-1", "Bob", StateReserved, 1, nil); err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if err := s.AddGuestComment(t.Context(), f.list.ID, f.wish.ID, "guest-1", "Bob", "Which size?", VisibleToOwner); err != nil {
		t.Fatalf("AddGuestComment() error = %v", err)
	}
	if _, err := s.DeleteList(t.Context(), f.owner.ID, f.list.ID); err != nil {
		t.Fatalf("DeleteList() error = %v", err)
	}

	for _, table := range []string{"lists", "wishes", "reservations", "comments"} {
		var n int
		if err := s.pool.QueryRow(t.Context(), "select count(*) from "+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s has %d rows after the list was deleted, want 0", table, n)
		}
	}
}

func TestOwnerQueriesAreScopedToTheOwner(t *testing.T) {
	s := newStore(t)
	f := seed(t, s)

	other, err := s.UpsertOwner(t.Context(), "subject-2", "mallory", "", "")
	if err != nil {
		t.Fatalf("UpsertOwner() error = %v", err)
	}
	if _, err := s.List(t.Context(), other.ID, f.list.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("List() of somebody else's list = %v, want ErrNotFound", err)
	}
	if _, err := s.Wish(t.Context(), other.ID, f.wish.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Wish() of somebody else's wish = %v, want ErrNotFound", err)
	}
	if err := s.UpdateWish(t.Context(), other.ID, f.wish.ID, WishInput{Title: "x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateWish() of somebody else's wish = %v, want ErrNotFound", err)
	}
	if _, err := s.DeleteList(t.Context(), other.ID, f.list.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteList() of somebody else's list = %v, want ErrNotFound", err)
	}
}

// Behaviour of the guest side, and the other half of the blindness: what a
// guest does is invisible in everything the owner's queries return.
func TestReservationsWorkForGuestsAndStayInvisibleToTheOwner(t *testing.T) {
	s := newStore(t)
	f := seed(t, s)
	ctx := t.Context()

	guest, err := s.ListByToken(ctx, "token-1")
	if err != nil || guest.ID != f.list.ID || guest.OwnerName != "Alice" {
		t.Fatalf("ListByToken() = %+v, %v", guest, err)
	}
	if _, err := s.ListByToken(ctx, "unknown"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ListByToken(unknown) = %v, want ErrNotFound", err)
	}

	if err := s.Reserve(ctx, f.list.ID, f.wish.ID, "guest-1", "Bob", StateReserved, 1, nil); err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	// The same guest again edits the entry instead of adding one.
	if err := s.Reserve(ctx, f.list.ID, f.wish.ID, "guest-1", "Bob", StateBought, 1, nil); err != nil {
		t.Fatalf("second Reserve() error = %v", err)
	}
	// Another guest finds the single copy taken.
	if err := s.Reserve(ctx, f.list.ID, f.wish.ID, "guest-2", "Eve", StateReserved, 1, nil); !errors.Is(err, ErrTaken) {
		t.Errorf("Reserve() on a taken wish = %v, want ErrTaken", err)
	}

	wishes, err := s.GuestWishes(ctx, f.list.ID, "guest-1")
	if err != nil || len(wishes) != 1 {
		t.Fatalf("GuestWishes() = %v, %v", wishes, err)
	}
	if !wishes[0].Taken() || wishes[0].Mine == nil || wishes[0].Mine.State != StateBought || len(wishes[0].Reservations) != 1 {
		t.Errorf("guest view = %+v, want one bought reservation marked mine", wishes[0])
	}
	other, _ := s.GuestWishes(ctx, f.list.ID, "guest-2")
	if other[0].Mine != nil {
		t.Error("another guest sees the reservation as theirs")
	}

	// Somebody else cannot release it, the guest can.
	if err := s.Release(ctx, f.list.ID, f.wish.ID, "guest-2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Release() by another guest = %v, want ErrNotFound", err)
	}

	// The owner's side: nothing of it shows.
	ownerWishes, err := s.Wishes(ctx, f.owner.ID, f.list.ID, true)
	if err != nil || len(ownerWishes) != 1 || ownerWishes[0].ID != f.wish.ID {
		t.Fatalf("Wishes() = %v, %v", ownerWishes, err)
	}

	if err := s.Release(ctx, f.list.ID, f.wish.ID, "guest-1"); err != nil {
		t.Errorf("Release() by the owner of the entry = %v", err)
	}
}

func TestReserveRefusesAClosedList(t *testing.T) {
	s := newStore(t)
	f := seed(t, s)

	in := ListInput{Title: f.list.Title, ReserveOpen: false}
	if err := s.UpdateList(t.Context(), f.owner.ID, f.list.ID, in); err != nil {
		t.Fatalf("UpdateList() error = %v", err)
	}
	if err := s.Reserve(t.Context(), f.list.ID, f.wish.ID, "guest-1", "Bob", StateReserved, 1, nil); !errors.Is(err, ErrClosed) {
		t.Errorf("Reserve() on a closed list = %v, want ErrClosed", err)
	}
}

func TestGroupGiftsAcceptSeveralPledges(t *testing.T) {
	s := newStore(t)
	f := seed(t, s)
	ctx := t.Context()

	price := int64(10000)
	if err := s.UpdateWish(ctx, f.owner.ID, f.wish.ID, WishInput{
		Title: "Bike", PriceCents: &price, Quantity: 1, Priority: 1, GroupGift: true,
	}); err != nil {
		t.Fatalf("UpdateWish() error = %v", err)
	}
	for i, key := range []string{"guest-1", "guest-2"} {
		pledge := int64(2500 * (i + 1))
		if err := s.Reserve(ctx, f.list.ID, f.wish.ID, key, key, StateReserved, 1, &pledge); err != nil {
			t.Fatalf("Reserve() by %s error = %v", key, err)
		}
	}
	wish, err := s.GuestWish(ctx, f.list.ID, f.wish.ID, "guest-1")
	if err != nil {
		t.Fatalf("GuestWish() error = %v", err)
	}
	if wish.PledgedCents != 7500 || wish.OpenCents() != 2500 {
		t.Errorf("pledged = %d, open = %d, want 7500 and 2500", wish.PledgedCents, wish.OpenCents())
	}
}

func TestCommentsReachTheRightSide(t *testing.T) {
	s := newStore(t)
	f := seed(t, s)
	ctx := t.Context()

	if err := s.AddGuestComment(ctx, f.list.ID, f.wish.ID, "guest-1", "Bob", "Among us", VisibleToGuests); err != nil {
		t.Fatalf("AddGuestComment(guests) error = %v", err)
	}
	if err := s.AddGuestComment(ctx, f.list.ID, f.wish.ID, "guest-1", "Bob", "For the owner", VisibleToOwner); err != nil {
		t.Fatalf("AddGuestComment(owner) error = %v", err)
	}
	if err := s.AddOwnerReply(ctx, f.owner.ID, f.wish.ID, "An answer"); err != nil {
		t.Fatalf("AddOwnerReply() error = %v", err)
	}

	questions, err := s.OwnerQuestions(ctx, f.owner.ID, f.list.ID)
	if err != nil {
		t.Fatalf("OwnerQuestions() error = %v", err)
	}
	if got := questions[f.wish.ID]; len(got) != 2 || got[0].Body != "For the owner" || !got[1].FromOwner() {
		t.Errorf("owner sees %+v, want the question and the answer only", got)
	}

	comments, err := s.GuestComments(ctx, f.list.ID, "guest-1")
	if err != nil {
		t.Fatalf("GuestComments() error = %v", err)
	}
	if got := comments[f.wish.ID]; len(got) != 3 || !got[0].Mine {
		t.Errorf("guest sees %+v, want all three comments with their own marked", got)
	}

	if err := s.DeleteComment(ctx, f.list.ID, comments[f.wish.ID][0].ID, "guest-2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteComment() by another guest = %v, want ErrNotFound", err)
	}
}

func TestMoveWishSwapsNeighbours(t *testing.T) {
	s := newStore(t)
	f := seed(t, s)
	ctx := t.Context()

	second, err := s.CreateWish(ctx, f.owner.ID, f.list.ID, WishInput{Title: "Second"})
	if err != nil {
		t.Fatalf("CreateWish() error = %v", err)
	}
	if err := s.MoveWish(ctx, f.owner.ID, second.ID, -1); err != nil {
		t.Fatalf("MoveWish() error = %v", err)
	}
	wishes, err := s.Wishes(ctx, f.owner.ID, f.list.ID, false)
	if err != nil || len(wishes) != 2 || wishes[0].ID != second.ID {
		t.Errorf("order after moving up = %v, %v, want the second wish first", wishes, err)
	}
}
