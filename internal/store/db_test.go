// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package store

// These tests run against a real PostgreSQL server named by
// WISHLIST_TEST_DATABASE_URL, see internal/dbtest. Without it they skip
// loudly, and in CI they fail instead of skipping.

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ostendorf-cloud/wishlist/internal/dbtest"
)

// newCore opens a store on a fresh database and applies the schema.
func newCore(t *testing.T) *Core {
	t.Helper()

	c, err := Open(t.Context(), dbtest.Database(t))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(c.Close)
	if err := c.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return c
}

// fixture is one owner with one shared list holding one wish.
type fixture struct {
	core  *Core
	owner Owner
	list  List
	wish  Wish
}

func (f fixture) owners() *OwnerStore { return f.core.Owners() }
func (f fixture) guests() *GuestStore { return f.core.Guests() }

func seed(t *testing.T, c *Core) fixture {
	t.Helper()

	o := c.Owners()
	owner, err := o.UpsertOwner(t.Context(), "subject-1", "alice", "Alice", "alice@example.invalid")
	if err != nil {
		t.Fatalf("UpsertOwner() error = %v", err)
	}
	list, err := o.CreateList(t.Context(), owner.ID, ListInput{Title: "Birthday", ReserveOpen: true})
	if err != nil {
		t.Fatalf("CreateList() error = %v", err)
	}
	if err := o.SetShareToken(t.Context(), owner.ID, list.ID, "token-1"); err != nil {
		t.Fatalf("SetShareToken() error = %v", err)
	}
	wish, err := o.CreateWish(t.Context(), owner.ID, list.ID, WishInput{Title: "Book", Quantity: 1, Priority: 1})
	if err != nil {
		t.Fatalf("CreateWish() error = %v", err)
	}
	return fixture{core: c, owner: owner, list: list, wish: wish}
}

func TestMigrateAppliesTheSchemaOnceAndMakesTheStoreReady(t *testing.T) {
	c, err := Open(t.Context(), dbtest.Database(t))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer c.Close()

	if err := c.Ping(t.Context()); !errors.Is(err, ErrNotMigrated) {
		t.Fatalf("Ping() before Migrate = %v, want ErrNotMigrated", err)
	}
	for range 2 {
		if err := c.Migrate(t.Context()); err != nil {
			t.Fatalf("Migrate() error = %v", err)
		}
	}
	if err := c.Ping(t.Context()); err != nil {
		t.Errorf("Ping() after Migrate = %v, want nil", err)
	}

	var applied int
	if err := c.pool.QueryRow(t.Context(), "select count(*) from schema_migrations").Scan(&applied); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	files, _ := loadMigrations()
	if applied != len(files) {
		t.Errorf("schema_migrations has %d rows, want %d: a second Migrate must not apply twice", applied, len(files))
	}

	for _, table := range []string{"owners", "lists", "wishes", "reservations", "comments"} {
		var exists bool
		if err := c.pool.QueryRow(t.Context(), "select to_regclass($1) is not null", table).Scan(&exists); err != nil || !exists {
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
			c, err := Open(t.Context(), cfg)
			if err != nil {
				errs <- err
				return
			}
			defer c.Close()
			errs <- c.Migrate(t.Context())
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
	c := newCore(t)
	f := seed(t, c)

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
			_, err := c.pool.Exec(t.Context(), tc.sql, tc.args...)
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
		if _, err := c.pool.Exec(t.Context(), insert, f.wish.ID); err != nil {
			t.Fatalf("first insert: %v", err)
		}
		_, err := c.pool.Exec(t.Context(), insert, f.wish.ID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "reservations_guest_idx" {
			t.Errorf("error = %v, want a violation of reservations_guest_idx", err)
		}
	})
}

func TestDeletingAListCascadesToEverythingBelowIt(t *testing.T) {
	c := newCore(t)
	f := seed(t, c)

	if err := f.guests().Reserve(t.Context(), f.list.ID, f.wish.ID, "guest-1", "Bob", StateReserved, 1, nil); err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if err := f.guests().AddGuestComment(t.Context(), f.list.ID, f.wish.ID, "guest-1", "Bob", "Which size?", VisibleToOwner); err != nil {
		t.Fatalf("AddGuestComment() error = %v", err)
	}
	if _, err := f.owners().DeleteList(t.Context(), f.owner.ID, f.list.ID); err != nil {
		t.Fatalf("DeleteList() error = %v", err)
	}

	for _, table := range []string{"lists", "wishes", "reservations", "comments"} {
		var n int
		if err := c.pool.QueryRow(t.Context(), "select count(*) from "+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s has %d rows after the list was deleted, want 0", table, n)
		}
	}
}

// Every owner write and read of somebody else's data is ErrNotFound, and
// leaves the data as it was.
func TestOwnerQueriesAreScopedToTheOwner(t *testing.T) {
	c := newCore(t)
	f := seed(t, c)
	o := f.owners()

	other, err := o.UpsertOwner(t.Context(), "subject-2", "mallory", "", "")
	if err != nil {
		t.Fatalf("UpsertOwner() error = %v", err)
	}

	// A comment to answer, so AddOwnerReply has a target to be refused on.
	if err := f.guests().AddGuestComment(t.Context(), f.list.ID, f.wish.ID, "guest-1", "Bob", "Question", VisibleToOwner); err != nil {
		t.Fatalf("AddGuestComment() error = %v", err)
	}

	cases := []struct {
		name string
		run  func() error
	}{
		{"List", func() error { _, err := o.List(t.Context(), other.ID, f.list.ID); return err }},
		{"UpdateList", func() error { return o.UpdateList(t.Context(), other.ID, f.list.ID, ListInput{Title: "x"}) }},
		{"SetListArchived", func() error { return o.SetListArchived(t.Context(), other.ID, f.list.ID, true) }},
		{"SetShareToken", func() error { return o.SetShareToken(t.Context(), other.ID, f.list.ID, "stolen") }},
		{"DeleteList", func() error { _, err := o.DeleteList(t.Context(), other.ID, f.list.ID); return err }},
		{"Wish", func() error { _, err := o.Wish(t.Context(), other.ID, f.wish.ID); return err }},
		{"CreateWish", func() error {
			_, err := o.CreateWish(t.Context(), other.ID, f.list.ID, WishInput{Title: "x"})
			return err
		}},
		{"UpdateWish", func() error { return o.UpdateWish(t.Context(), other.ID, f.wish.ID, WishInput{Title: "x"}) }},
		{"SetWishArchived", func() error { return o.SetWishArchived(t.Context(), other.ID, f.wish.ID, true) }},
		{"DeleteWish", func() error { _, err := o.DeleteWish(t.Context(), other.ID, f.wish.ID); return err }},
		{"MoveWish", func() error { return o.MoveWish(t.Context(), other.ID, f.wish.ID, 1) }},
		{"AddOwnerReply", func() error { return o.AddOwnerReply(t.Context(), other.ID, f.wish.ID, "reply") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, ErrNotFound) {
				t.Errorf("%s as somebody else = %v, want ErrNotFound", tc.name, err)
			}
		})
	}

	// The two list reads answer with nothing, not with an error.
	t.Run("Lists and Wishes and OwnerQuestions are empty", func(t *testing.T) {
		if lists, err := o.Lists(t.Context(), other.ID, true); err != nil || len(lists) != 0 {
			t.Errorf("Lists() = %v, %v, want none", lists, err)
		}
		if wishes, err := o.Wishes(t.Context(), other.ID, f.list.ID, true); err != nil || len(wishes) != 0 {
			t.Errorf("Wishes() = %v, %v, want none", wishes, err)
		}
		if qs, err := o.OwnerQuestions(t.Context(), other.ID, f.list.ID); err != nil || len(qs) != 0 {
			t.Errorf("OwnerQuestions() = %v, %v, want none", qs, err)
		}
	})

	// Nothing of it took effect.
	t.Run("the data is untouched", func(t *testing.T) {
		list, err := o.List(t.Context(), f.owner.ID, f.list.ID)
		if err != nil || list.Archived || list.ShareToken != "token-1" || list.Title != "Birthday" {
			t.Errorf("list = %+v, %v, want it unchanged", list, err)
		}
		wish, err := o.Wish(t.Context(), f.owner.ID, f.wish.ID)
		if err != nil || wish.Archived || wish.Title != "Book" {
			t.Errorf("wish = %+v, %v, want it unchanged", wish, err)
		}
		qs, err := o.OwnerQuestions(t.Context(), f.owner.ID, f.list.ID)
		if err != nil || len(qs[f.wish.ID]) != 1 {
			t.Errorf("OwnerQuestions() = %v, %v, want the one question and no reply", qs, err)
		}
	})
}

// Behaviour of the guest side, and the other half of the blindness: what a
// guest does is invisible in everything the owner's queries return.
func TestReservationsWorkForGuestsAndStayInvisibleToTheOwner(t *testing.T) {
	c := newCore(t)
	f := seed(t, c)
	ctx := t.Context()
	g := f.guests()

	guest, err := g.ListByToken(ctx, "token-1")
	if err != nil || guest.ID != f.list.ID || guest.OwnerName != "Alice" {
		t.Fatalf("ListByToken() = %+v, %v", guest, err)
	}
	if _, err := g.ListByToken(ctx, "unknown"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ListByToken(unknown) = %v, want ErrNotFound", err)
	}

	if err := g.Reserve(ctx, f.list.ID, f.wish.ID, "guest-1", "Bob", StateReserved, 1, nil); err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	// The same guest again edits the entry instead of adding one.
	if err := g.Reserve(ctx, f.list.ID, f.wish.ID, "guest-1", "Bob", StateBought, 1, nil); err != nil {
		t.Fatalf("second Reserve() error = %v", err)
	}
	// Another guest finds the single copy taken.
	if err := g.Reserve(ctx, f.list.ID, f.wish.ID, "guest-2", "Eve", StateReserved, 1, nil); !errors.Is(err, ErrTaken) {
		t.Errorf("Reserve() on a taken wish = %v, want ErrTaken", err)
	}
	// Asking for more than the wish wants, alone, is a bad input and not a conflict.
	if err := g.Reserve(ctx, f.list.ID, f.wish.ID, "guest-3", "Mia", StateReserved, 2, nil); !errors.Is(err, ErrInvalidQuantity) {
		t.Errorf("Reserve() above the wanted quantity = %v, want ErrInvalidQuantity", err)
	}

	wishes, err := g.GuestWishes(ctx, f.list.ID, "guest-1")
	if err != nil || len(wishes) != 1 {
		t.Fatalf("GuestWishes() = %v, %v", wishes, err)
	}
	if !wishes[0].Taken() || wishes[0].Mine == nil || wishes[0].Mine.State != StateBought || len(wishes[0].Reservations) != 1 {
		t.Errorf("guest view = %+v, want one bought reservation marked mine", wishes[0])
	}
	other, _ := g.GuestWishes(ctx, f.list.ID, "guest-2")
	if other[0].Mine != nil {
		t.Error("another guest sees the reservation as theirs")
	}

	// Somebody else cannot release it, the guest can.
	if err := g.Release(ctx, f.list.ID, f.wish.ID, "guest-2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Release() by another guest = %v, want ErrNotFound", err)
	}
	// The right guest on the wrong list cannot either.
	if err := g.Release(ctx, f.list.ID+999, f.wish.ID, "guest-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Release() with the wrong list id = %v, want ErrNotFound", err)
	}
	if still, _ := g.GuestWishes(ctx, f.list.ID, "guest-1"); len(still) != 1 || still[0].Mine == nil {
		t.Error("a refused Release removed the reservation")
	}

	// The owner's side: nothing of it shows.
	ownerWishes, err := f.owners().Wishes(ctx, f.owner.ID, f.list.ID, true)
	if err != nil || len(ownerWishes) != 1 || ownerWishes[0].ID != f.wish.ID {
		t.Fatalf("Wishes() = %v, %v", ownerWishes, err)
	}

	if err := g.Release(ctx, f.list.ID, f.wish.ID, "guest-1"); err != nil {
		t.Errorf("Release() by the owner of the entry = %v", err)
	}
}

// Two guests reaching for the last unit at the same moment: exactly one gets it.
func TestConcurrentReservesOfTheLastUnitHaveOneWinner(t *testing.T) {
	c := newCore(t)
	f := seed(t, c)

	const guests = 8
	results := make(chan error, guests)
	var wg sync.WaitGroup
	for i := range guests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("guest-%d", i)
			results <- f.guests().Reserve(t.Context(), f.list.ID, f.wish.ID, key, key, StateReserved, 1, nil)
		}()
	}
	wg.Wait()
	close(results)

	wins, taken := 0, 0
	for err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrTaken):
			taken++
		default:
			t.Errorf("Reserve() error = %v, want nil or ErrTaken", err)
		}
	}
	if wins != 1 || taken != guests-1 {
		t.Errorf("%d winners and %d refused, want 1 and %d", wins, taken, guests-1)
	}
	var rows int
	if err := c.pool.QueryRow(t.Context(), "select count(*) from reservations").Scan(&rows); err != nil || rows != 1 {
		t.Errorf("%d reservation rows, %v, want exactly 1", rows, err)
	}
}

func TestReserveRefusesAClosedList(t *testing.T) {
	c := newCore(t)
	f := seed(t, c)

	in := ListInput{Title: f.list.Title, ReserveOpen: false}
	if err := f.owners().UpdateList(t.Context(), f.owner.ID, f.list.ID, in); err != nil {
		t.Fatalf("UpdateList() error = %v", err)
	}
	if err := f.guests().Reserve(t.Context(), f.list.ID, f.wish.ID, "guest-1", "Bob", StateReserved, 1, nil); !errors.Is(err, ErrClosed) {
		t.Errorf("Reserve() on a closed list = %v, want ErrClosed", err)
	}
}

func TestGroupGiftsAcceptSeveralPledges(t *testing.T) {
	c := newCore(t)
	f := seed(t, c)
	ctx := t.Context()

	price := int64(10000)
	if err := f.owners().UpdateWish(ctx, f.owner.ID, f.wish.ID, WishInput{
		Title: "Bike", PriceCents: &price, Quantity: 1, Priority: 1, GroupGift: true,
	}); err != nil {
		t.Fatalf("UpdateWish() error = %v", err)
	}
	for i, key := range []string{"guest-1", "guest-2"} {
		pledge := int64(2500 * (i + 1))
		if err := f.guests().Reserve(ctx, f.list.ID, f.wish.ID, key, key, StateReserved, 1, &pledge); err != nil {
			t.Fatalf("Reserve() by %s error = %v", key, err)
		}
	}
	wish, err := f.guests().GuestWish(ctx, f.list.ID, f.wish.ID, "guest-1")
	if err != nil {
		t.Fatalf("GuestWish() error = %v", err)
	}
	if wish.PledgedCents != 7500 || wish.OpenCents() != 2500 {
		t.Errorf("pledged = %d, open = %d, want 7500 and 2500", wish.PledgedCents, wish.OpenCents())
	}
}

func TestCommentsReachTheRightSide(t *testing.T) {
	c := newCore(t)
	f := seed(t, c)
	ctx := t.Context()
	g, o := f.guests(), f.owners()

	if err := g.AddGuestComment(ctx, f.list.ID, f.wish.ID, "guest-1", "Bob", "Among us", VisibleToGuests); err != nil {
		t.Fatalf("AddGuestComment(guests) error = %v", err)
	}
	if err := g.AddGuestComment(ctx, f.list.ID, f.wish.ID, "guest-1", "Bob", "For the owner", VisibleToOwner); err != nil {
		t.Fatalf("AddGuestComment(owner) error = %v", err)
	}
	if err := o.AddOwnerReply(ctx, f.owner.ID, f.wish.ID, "An answer"); err != nil {
		t.Fatalf("AddOwnerReply() error = %v", err)
	}

	questions, err := o.OwnerQuestions(ctx, f.owner.ID, f.list.ID)
	if err != nil {
		t.Fatalf("OwnerQuestions() error = %v", err)
	}
	if got := questions[f.wish.ID]; len(got) != 2 || got[0].Body != "For the owner" || !got[1].FromOwner() {
		t.Errorf("owner sees %+v, want the question and the answer only", got)
	}

	comments, err := g.GuestComments(ctx, f.list.ID, "guest-1")
	if err != nil {
		t.Fatalf("GuestComments() error = %v", err)
	}
	thread := comments[f.wish.ID]
	if len(thread) != 3 || !thread[0].Mine {
		t.Fatalf("guest sees %+v, want all three comments with their own marked", thread)
	}

	// Another guest cannot delete it.
	if err := g.DeleteComment(ctx, f.list.ID, thread[0].ID, "guest-2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteComment() by another guest = %v, want ErrNotFound", err)
	}
	// The owner's reply cannot be deleted by a guest, whatever key they send.
	reply := thread[2]
	if !reply.FromOwner() {
		t.Fatalf("the third comment is not the owner's reply: %+v", reply)
	}
	for _, key := range []string{"guest-1", "guest-2", ""} {
		if err := g.DeleteComment(ctx, f.list.ID, reply.ID, key); !errors.Is(err, ErrNotFound) {
			t.Errorf("DeleteComment() of the owner's reply with key %q = %v, want ErrNotFound", key, err)
		}
	}
	// The wrong list does not reach it either.
	if err := g.DeleteComment(ctx, f.list.ID+999, thread[0].ID, "guest-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteComment() with the wrong list id = %v, want ErrNotFound", err)
	}
	// The author can.
	if err := g.DeleteComment(ctx, f.list.ID, thread[0].ID, "guest-1"); err != nil {
		t.Fatalf("DeleteComment() by the author = %v", err)
	}
	after, _ := g.GuestComments(ctx, f.list.ID, "guest-1")
	if got := after[f.wish.ID]; len(got) != 2 {
		t.Errorf("%d comments after the delete, want 2", len(got))
	}
}

// wishesOf creates n wishes on the list, after the fixture's own, and returns
// the ids in list order, the fixture's wish first.
func wishesOf(t *testing.T, f fixture, n int) []int64 {
	t.Helper()

	ids := []int64{f.wish.ID}
	for i := range n - 1 {
		w, err := f.owners().CreateWish(t.Context(), f.owner.ID, f.list.ID, WishInput{Title: fmt.Sprintf("Wish %d", i+2)})
		if err != nil {
			t.Fatalf("CreateWish() error = %v", err)
		}
		ids = append(ids, w.ID)
	}
	return ids
}

func orderOf(t *testing.T, f fixture, includeArchived bool) []int64 {
	t.Helper()

	wishes, err := f.owners().Wishes(t.Context(), f.owner.ID, f.list.ID, includeArchived)
	if err != nil {
		t.Fatalf("Wishes() error = %v", err)
	}
	var ids []int64
	for _, w := range wishes {
		ids = append(ids, w.ID)
	}
	return ids
}

func TestMoveWishToList(t *testing.T) {
	f := seed(t, newCore(t))
	o := f.owners()
	ctx := t.Context()

	target, err := o.CreateList(ctx, f.owner.ID, ListInput{Title: "Christmas"})
	if err != nil {
		t.Fatalf("CreateList() error = %v", err)
	}
	first, err := o.CreateWish(ctx, f.owner.ID, target.ID, WishInput{Title: "Existing"})
	if err != nil {
		t.Fatalf("CreateWish() error = %v", err)
	}
	if err := o.SetWishArchived(ctx, f.owner.ID, f.wish.ID, true); err != nil {
		t.Fatalf("SetWishArchived() error = %v", err)
	}

	other, err := o.UpsertOwner(ctx, "subject-3", "mallory", "", "")
	if err != nil {
		t.Fatalf("UpsertOwner() error = %v", err)
	}
	foreign, err := o.CreateList(ctx, other.ID, ListInput{Title: "Theirs"})
	if err != nil {
		t.Fatalf("CreateList() error = %v", err)
	}

	t.Run("a foreign list, a foreign wish and a missing list are all ErrNotFound", func(t *testing.T) {
		for name, err := range map[string]error{
			"foreign list":                          o.MoveWishToList(ctx, f.owner.ID, f.wish.ID, foreign.ID),
			"foreign wish":                          o.MoveWishToList(ctx, other.ID, f.wish.ID, foreign.ID),
			"missing list":                          o.MoveWishToList(ctx, f.owner.ID, f.wish.ID, 999999),
			"missing wish":                          o.MoveWishToList(ctx, f.owner.ID, 999999, target.ID),
			"foreign owner, foreign wish, own list": o.MoveWishToList(ctx, other.ID, f.wish.ID, target.ID),
		} {
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("%s = %v, want ErrNotFound", name, err)
			}
		}
		if w, err := o.Wish(ctx, f.owner.ID, f.wish.ID); err != nil || w.ListID != f.list.ID {
			t.Errorf("wish = %+v, %v, want it still on its list", w, err)
		}
	})

	t.Run("the wish goes to the end of the other list and keeps its flag", func(t *testing.T) {
		if err := o.MoveWishToList(ctx, f.owner.ID, f.wish.ID, target.ID); err != nil {
			t.Fatalf("MoveWishToList() error = %v", err)
		}
		w, err := o.Wish(ctx, f.owner.ID, f.wish.ID)
		if err != nil || w.ListID != target.ID || w.Position <= first.Position || !w.Archived {
			t.Errorf("wish = %+v, %v, want it on the target list after %d, still archived", w, err, first.Position)
		}
	})
}

func TestMoveWish(t *testing.T) {
	t.Run("up and down", func(t *testing.T) {
		f := seed(t, newCore(t))
		ids := wishesOf(t, f, 3)
		o := f.owners()

		if err := o.MoveWish(t.Context(), f.owner.ID, ids[2], -1); err != nil {
			t.Fatalf("MoveWish(up) error = %v", err)
		}
		if got, want := orderOf(t, f, false), []int64{ids[0], ids[2], ids[1]}; !slices.Equal(got, want) {
			t.Errorf("order after moving up = %v, want %v", got, want)
		}
		if err := o.MoveWish(t.Context(), f.owner.ID, ids[0], 1); err != nil {
			t.Fatalf("MoveWish(down) error = %v", err)
		}
		if got, want := orderOf(t, f, false), []int64{ids[2], ids[0], ids[1]}; !slices.Equal(got, want) {
			t.Errorf("order after moving down = %v, want %v", got, want)
		}
	})

	t.Run("at the end is no error and no change", func(t *testing.T) {
		f := seed(t, newCore(t))
		ids := wishesOf(t, f, 2)
		o := f.owners()

		if err := o.MoveWish(t.Context(), f.owner.ID, ids[0], -1); err != nil {
			t.Errorf("MoveWish(up at the top) error = %v", err)
		}
		if err := o.MoveWish(t.Context(), f.owner.ID, ids[1], 1); err != nil {
			t.Errorf("MoveWish(down at the bottom) error = %v", err)
		}
		if got := orderOf(t, f, false); !slices.Equal(got, ids) {
			t.Errorf("order = %v, want it unchanged %v", got, ids)
		}
	})

	t.Run("a wrong direction is refused", func(t *testing.T) {
		f := seed(t, newCore(t))
		if err := f.owners().MoveWish(t.Context(), f.owner.ID, f.wish.ID, 2); err == nil {
			t.Error("MoveWish(2) error = nil, want an error")
		}
	})

	t.Run("on a tie in position", func(t *testing.T) {
		f := seed(t, newCore(t))
		ids := wishesOf(t, f, 2)
		if _, err := f.core.pool.Exec(t.Context(), "update wishes set position = 5"); err != nil {
			t.Fatalf("make a tie: %v", err)
		}
		// Ties are ordered by id: ids[0] before ids[1].
		if got := orderOf(t, f, false); !slices.Equal(got, ids) {
			t.Fatalf("order on a tie = %v, want by id %v", got, ids)
		}
		if err := f.owners().MoveWish(t.Context(), f.owner.ID, ids[1], -1); err != nil {
			t.Fatalf("MoveWish() error = %v", err)
		}
		if got, want := orderOf(t, f, false), []int64{ids[1], ids[0]}; !slices.Equal(got, want) {
			t.Errorf("order after moving up on a tie = %v, want %v", got, want)
		}
	})

	t.Run("an archived wish moves among the archived only", func(t *testing.T) {
		f := seed(t, newCore(t))
		ids := wishesOf(t, f, 4) // live, live, archived, archived by the step below
		o := f.owners()
		for _, id := range ids[2:] {
			if err := o.SetWishArchived(t.Context(), f.owner.ID, id, true); err != nil {
				t.Fatalf("SetWishArchived() error = %v", err)
			}
		}
		// The archived ones sit behind the live ones. Moving the first archived
		// one up finds no archived neighbour above it and changes nothing.
		if err := o.MoveWish(t.Context(), f.owner.ID, ids[2], -1); err != nil {
			t.Fatalf("MoveWish(archived up) error = %v", err)
		}
		if got := orderOf(t, f, true); !slices.Equal(got, ids) {
			t.Errorf("order = %v, want it unchanged %v", got, ids)
		}
		if err := o.MoveWish(t.Context(), f.owner.ID, ids[3], -1); err != nil {
			t.Fatalf("MoveWish(archived swap) error = %v", err)
		}
		if got, want := orderOf(t, f, true), []int64{ids[0], ids[1], ids[3], ids[2]}; !slices.Equal(got, want) {
			t.Errorf("order = %v, want %v: the live wishes must stay where they were", got, want)
		}
	})

	// Moves of adjacent wishes at the same moment used to wait on each other
	// (deadlock, 40P01) or write a stale position. The positions must stay a
	// permutation of what they were, and no move may fail.
	t.Run("concurrent moves of adjacent wishes", func(t *testing.T) {
		f := seed(t, newCore(t))
		ids := wishesOf(t, f, 6)
		o := f.owners()

		var wg sync.WaitGroup
		errs := make(chan error, 60)
		for round := range 10 {
			for i := range ids {
				wg.Add(1)
				go func() {
					defer wg.Done()
					direction := 1
					if (round+i)%2 == 0 {
						direction = -1
					}
					errs <- o.MoveWish(t.Context(), f.owner.ID, ids[i], direction)
				}()
			}
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Errorf("MoveWish() error = %v", err)
			}
		}

		wishes, err := o.Wishes(t.Context(), f.owner.ID, f.list.ID, true)
		if err != nil {
			t.Fatalf("Wishes() error = %v", err)
		}
		var positions []int
		for _, w := range wishes {
			positions = append(positions, w.Position)
		}
		slices.Sort(positions)
		if want := []int{0, 1, 2, 3, 4, 5}; !slices.Equal(positions, want) {
			t.Errorf("positions after the moves = %v, want %v: a swap lost or duplicated one", positions, want)
		}
	})
}

// A second list, of the same owner and of another one, each with a wish.
func otherLists(t *testing.T, f fixture) (sameOwner, otherOwner List, wishSame, wishOther Wish) {
	t.Helper()

	o := f.owners()
	var err error
	sameOwner, err = o.CreateList(t.Context(), f.owner.ID, ListInput{Title: "Second", ReserveOpen: true})
	if err != nil {
		t.Fatalf("CreateList() error = %v", err)
	}
	wishSame, err = o.CreateWish(t.Context(), f.owner.ID, sameOwner.ID, WishInput{Title: "Second wish", Quantity: 1})
	if err != nil {
		t.Fatalf("CreateWish() error = %v", err)
	}
	stranger, err := o.UpsertOwner(t.Context(), "subject-9", "carol", "", "")
	if err != nil {
		t.Fatalf("UpsertOwner() error = %v", err)
	}
	otherOwner, err = o.CreateList(t.Context(), stranger.ID, ListInput{Title: "Theirs", ReserveOpen: true})
	if err != nil {
		t.Fatalf("CreateList() error = %v", err)
	}
	wishOther, err = o.CreateWish(t.Context(), stranger.ID, otherOwner.ID, WishInput{Title: "Their wish", Quantity: 1})
	if err != nil {
		t.Fatalf("CreateWish() error = %v", err)
	}
	return sameOwner, otherOwner, wishSame, wishOther
}

// A guest holding one list's link must not reach a wish of another list by
// passing that wish's id with their own list id.
func TestGuestWritesRefuseAWishFromAnotherList(t *testing.T) {
	c := newCore(t)
	f := seed(t, c)
	g := f.guests()
	_, _, wishSame, wishOther := otherLists(t, f)

	for name, wishID := range map[string]int64{"same owner's other list": wishSame.ID, "another owner's list": wishOther.ID} {
		t.Run(name, func(t *testing.T) {
			err := g.Reserve(t.Context(), f.list.ID, wishID, "guest-1", "Bob", StateReserved, 1, nil)
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("Reserve() = %v, want ErrNotFound", err)
			}
			err = g.AddGuestComment(t.Context(), f.list.ID, wishID, "guest-1", "Bob", "hello", VisibleToGuests)
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("AddGuestComment() = %v, want ErrNotFound", err)
			}
			if _, err := g.WishOnList(t.Context(), f.list.ID, wishID); !errors.Is(err, ErrNotFound) {
				t.Errorf("WishOnList() = %v, want ErrNotFound", err)
			}
			if _, err := g.GuestWish(t.Context(), f.list.ID, wishID, "guest-1"); !errors.Is(err, ErrNotFound) {
				t.Errorf("GuestWish() = %v, want ErrNotFound", err)
			}
		})
	}

	var rows int
	if err := c.pool.QueryRow(t.Context(), "select (select count(*) from reservations) + (select count(*) from comments)").
		Scan(&rows); err != nil || rows != 0 {
		t.Errorf("%d reservation and comment rows after the refused writes, %v, want none", rows, err)
	}
}

func TestWishOnList(t *testing.T) {
	c := newCore(t)
	f := seed(t, c)
	g := f.guests()

	wish, err := g.WishOnList(t.Context(), f.list.ID, f.wish.ID)
	if err != nil || wish.ID != f.wish.ID || wish.Title != "Book" {
		t.Fatalf("WishOnList() = %+v, %v, want the wish", wish, err)
	}
	if _, err := g.WishOnList(t.Context(), f.list.ID, f.wish.ID+999); !errors.Is(err, ErrNotFound) {
		t.Errorf("WishOnList(unknown wish) = %v, want ErrNotFound", err)
	}
	if _, err := g.WishOnList(t.Context(), f.list.ID+999, f.wish.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("WishOnList(wrong list) = %v, want ErrNotFound", err)
	}
	// An archived wish is not served to guests.
	if err := f.owners().SetWishArchived(t.Context(), f.owner.ID, f.wish.ID, true); err != nil {
		t.Fatalf("SetWishArchived() error = %v", err)
	}
	if _, err := g.WishOnList(t.Context(), f.list.ID, f.wish.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("WishOnList(archived wish) = %v, want ErrNotFound", err)
	}
}

func TestGuestCommentsHideCommentsOnArchivedWishes(t *testing.T) {
	c := newCore(t)
	f := seed(t, c)
	ctx := t.Context()
	g, o := f.guests(), f.owners()

	second, err := o.CreateWish(ctx, f.owner.ID, f.list.ID, WishInput{Title: "Second", Quantity: 1})
	if err != nil {
		t.Fatalf("CreateWish() error = %v", err)
	}
	for _, id := range []int64{f.wish.ID, second.ID} {
		if err := g.AddGuestComment(ctx, f.list.ID, id, "guest-1", "Bob", fmt.Sprintf("on %d", id), VisibleToGuests); err != nil {
			t.Fatalf("AddGuestComment() error = %v", err)
		}
	}
	if err := o.SetWishArchived(ctx, f.owner.ID, second.ID, true); err != nil {
		t.Fatalf("SetWishArchived() error = %v", err)
	}

	comments, err := g.GuestComments(ctx, f.list.ID, "guest-1")
	if err != nil {
		t.Fatalf("GuestComments() error = %v", err)
	}
	if len(comments[f.wish.ID]) != 1 {
		t.Errorf("comments on the live wish = %v, want one", comments[f.wish.ID])
	}
	if got := comments[second.ID]; len(got) != 0 {
		t.Errorf("comments on the archived wish = %v, want none", got)
	}
	// A new comment on the archived wish is refused too.
	if err := g.AddGuestComment(ctx, f.list.ID, second.ID, "guest-1", "Bob", "late", VisibleToGuests); !errors.Is(err, ErrNotFound) {
		t.Errorf("AddGuestComment() on an archived wish = %v, want ErrNotFound", err)
	}
}

func TestEventsSoon(t *testing.T) {
	c := newCore(t)
	f := seed(t, c)
	ctx := t.Context()
	o := f.owners()

	day := func(offset int) *time.Time {
		d := time.Now().UTC().AddDate(0, 0, offset)
		return &d
	}
	addList := func(ownerID int64, title string, date *time.Time) List {
		t.Helper()
		l, err := o.CreateList(ctx, ownerID, ListInput{Title: title, EventDate: date, ReserveOpen: true})
		if err != nil {
			t.Fatalf("CreateList(%s) error = %v", title, err)
		}
		return l
	}
	soon := addList(f.owner.ID, "soon", day(2))
	addList(f.owner.ID, "far", day(40))
	addList(f.owner.ID, "past", day(-3))
	archived := addList(f.owner.ID, "archived", day(1))
	if err := o.SetListArchived(ctx, f.owner.ID, archived.ID, true); err != nil {
		t.Fatalf("SetListArchived() error = %v", err)
	}
	other, err := o.UpsertOwner(ctx, "subject-2", "mallory", "", "")
	if err != nil {
		t.Fatalf("UpsertOwner() error = %v", err)
	}
	addList(other.ID, "somebody else's", day(1))

	got, err := o.EventsSoon(ctx, f.owner.ID, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("EventsSoon() error = %v", err)
	}
	if len(got) != 1 || got[0].ID != soon.ID {
		var titles []string
		for _, l := range got {
			titles = append(titles, l.Title)
		}
		t.Errorf("EventsSoon() = %v, want only \"soon\"", titles)
	}
}
