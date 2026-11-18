// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package store

// Everything the guest side reads and writes: the list behind a share token,
// the reservations, and the comment thread. This is the one file that may name
// the reservations table. boundary_test.go fails when another file in this
// package does, which is what keeps a reservation blind to the owner
// (docs/adr/0001-blind-reservations.md).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrClosed is returned when a guest tries to take a wish on a list whose
// owner has closed reservations.
var ErrClosed = errors.New("list closed for reservations")

// ErrTaken is returned when other guests already hold so much of the wish that
// the wanted quantity is used up. It is a conflict, not a bad input.
var ErrTaken = errors.New("wish already taken")

// ErrInvalidQuantity is returned for a quantity below 1, above maxQuantity, or
// above what the wish asks for in total.
var ErrInvalidQuantity = errors.New("invalid quantity")

// ErrInvalidAmount is returned for a pledge that is not between 1 and
// maxAmountCents.
var ErrInvalidAmount = errors.New("invalid amount")

// ErrGuestKeyRequired is returned when a guest write comes without the
// guest's key: the key is what lets a guest undo their own entry later.
var ErrGuestKeyRequired = errors.New("guest key is required")

// Input limits, far above any real wish and far below what PostgreSQL's
// integer columns hold, so the database never answers with a raw check or
// overflow error for a plausible mistake.
const (
	maxQuantity    = 10_000
	maxAmountCents = int64(1_000_000_000_00)
)

// Reservation states.
const (
	StateReserved = "reserved"
	StateBought   = "bought"
)

// Reservation is a guest's claim on a wish. Nothing the owner ever sees.
type Reservation struct {
	ID          int64
	WishID      int64
	GuestName   string
	Quantity    int
	State       string
	AmountCents *int64
	Mine        bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// GuestList is a list as a guest sees it, with the name of the person whose
// wishes they are.
type GuestList struct {
	List
	OwnerName string
}

// GuestWish is a wish as a guest sees it: with who took it and how much of a
// group gift is covered.
type GuestWish struct {
	Wish
	Reservations  []Reservation
	TakenQuantity int
	PledgedCents  int64
	Mine          *Reservation
}

// Open is how many of the wanted quantity nobody has taken yet.
func (g GuestWish) Open() int {
	return max(g.Quantity-g.TakenQuantity, 0)
}

// Taken is true when the wish needs nobody else.
func (g GuestWish) Taken() bool { return g.Open() == 0 }

// OpenCents is what is missing on a group gift, 0 when there is no price or it
// is covered.
func (g GuestWish) OpenCents() int64 {
	if g.PriceCents == nil {
		return 0
	}
	return max(*g.PriceCents-g.PledgedCents, 0)
}

// ListByToken resolves a share token. An archived list and an unknown token are
// the same answer, so a guest cannot tell one from the other.
func (s *GuestStore) ListByToken(ctx context.Context, token string) (GuestList, error) {
	if token == "" {
		return GuestList{}, ErrNotFound
	}
	var list GuestList
	err := s.pool.QueryRow(ctx, `
		select l.id, l.owner_id, l.title, l.occasion, l.event_date, l.greeting,
		       coalesce(l.share_token, ''), l.reserve_open, l.archived, l.created_at, l.updated_at,
		       case when o.display_name <> '' then o.display_name else o.username end
		from lists l
		join owners o on o.id = l.owner_id
		where l.share_token = $1 and not l.archived`, token,
	).Scan(&list.ID, &list.OwnerID, &list.Title, &list.Occasion, &list.EventDate, &list.Greeting,
		&list.ShareToken, &list.ReserveOpen, &list.Archived, &list.CreatedAt, &list.UpdatedAt,
		&list.OwnerName)
	if err != nil {
		return GuestList{}, fmt.Errorf("share token: %w", notFound(err))
	}
	return list, nil
}

// WishOnList resolves a wish id against a list: the proof that the wish
// belongs to that token, without any reservation.
func (s *GuestStore) WishOnList(ctx context.Context, listID, wishID int64) (Wish, error) {
	var wish Wish
	err := scanWish(s.pool.QueryRow(ctx,
		`select `+wishColumns+` from wishes where id = $1 and list_id = $2 and not archived`,
		wishID, listID), &wish)
	if err != nil {
		return Wish{}, fmt.Errorf("wish on list: %w", notFound(err))
	}
	return wish, nil
}

// GuestWishes are the wishes of a list with every reservation on them. guestKey
// marks the viewer's own entries, which are the only ones they may undo.
func (s *GuestStore) GuestWishes(ctx context.Context, listID int64, guestKey string) ([]GuestWish, error) {
	rows, err := s.pool.Query(ctx, `
		select `+wishColumns+`
		from wishes
		where list_id = $1 and not archived
		order by priority desc, position, id`, listID)
	if err != nil {
		return nil, fmt.Errorf("guest wishes: %w", err)
	}
	defer rows.Close()

	wishes := make([]GuestWish, 0, 16)
	index := map[int64]int{}
	for rows.Next() {
		var wish GuestWish
		if err := scanWish(rows, &wish.Wish); err != nil {
			return nil, fmt.Errorf("guest wishes: %w", err)
		}
		index[wish.ID] = len(wishes)
		wishes = append(wishes, wish)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("guest wishes: %w", err)
	}
	if len(wishes) == 0 {
		return wishes, nil
	}

	reservations, err := s.pool.Query(ctx, `
		select r.id, r.wish_id, r.guest_name, r.quantity, r.state, r.amount_cents,
		       r.guest_key = $2, r.created_at, r.updated_at
		from reservations r
		join wishes w on w.id = r.wish_id
		where w.list_id = $1
		order by r.created_at, r.id`, listID, guestKey)
	if err != nil {
		return nil, fmt.Errorf("reservations: %w", err)
	}
	defer reservations.Close()

	for reservations.Next() {
		var r Reservation
		if err := reservations.Scan(&r.ID, &r.WishID, &r.GuestName, &r.Quantity, &r.State,
			&r.AmountCents, &r.Mine, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("reservations: %w", err)
		}
		at, ok := index[r.WishID]
		if !ok {
			// An archived wish keeps its reservations; it is simply not shown.
			continue
		}
		wish := &wishes[at]
		wish.Reservations = append(wish.Reservations, r)
		wish.TakenQuantity += r.Quantity
		if r.AmountCents != nil {
			wish.PledgedCents += *r.AmountCents
		}
		if r.Mine {
			mine := r
			wish.Mine = &mine
		}
	}
	if err := reservations.Err(); err != nil {
		return nil, fmt.Errorf("reservations: %w", err)
	}
	return wishes, nil
}

// GuestWish is one wish of a list, for the pages that show a single one.
func (s *GuestStore) GuestWish(ctx context.Context, listID, wishID int64, guestKey string) (GuestWish, error) {
	wishes, err := s.GuestWishes(ctx, listID, guestKey)
	if err != nil {
		return GuestWish{}, err
	}
	for _, wish := range wishes {
		if wish.ID == wishID {
			return wish, nil
		}
	}
	return GuestWish{}, ErrNotFound
}

// Reserve is what a guest does with a wish: take it, or change what they took.
// One row per guest and wish, so a reload cannot take the same wish twice.
//
// state is reserved or bought, amountCents a pledge towards a group gift or
// nil. The list has to be open and the wish has to be on it, both checked in
// the transaction rather than beforehand: two guests click at the same time.
func (s *GuestStore) Reserve(ctx context.Context, listID, wishID int64, guestKey, guestName, state string, quantity int, amountCents *int64) error {
	if guestKey == "" {
		return ErrGuestKeyRequired
	}
	if state != StateReserved && state != StateBought {
		return fmt.Errorf("state %q is not reserved or bought", state)
	}
	if quantity < 1 || quantity > maxQuantity {
		return fmt.Errorf("%w: %d is not between 1 and %d", ErrInvalidQuantity, quantity, maxQuantity)
	}
	if amountCents != nil && (*amountCents < 1 || *amountCents > maxAmountCents) {
		return fmt.Errorf("%w: %d cents is not between 1 and %d", ErrInvalidAmount, *amountCents, maxAmountCents)
	}

	return runTx(ctx, s.pool, func(tx pgx.Tx) error {
		var open, groupGift bool
		var wanted int
		err := tx.QueryRow(ctx, `
			select l.reserve_open, w.group_gift, w.quantity
			from wishes w
			join lists l on l.id = w.list_id
			where w.id = $1 and w.list_id = $2 and not w.archived and not l.archived
			for no key update of w`, wishID, listID).Scan(&open, &groupGift, &wanted)
		if err != nil {
			return notFound(err)
		}
		if !open {
			return ErrClosed
		}
		if !groupGift {
			// A pledge only means something when the wish is a group gift.
			amountCents = nil
		}

		// How much others have taken, without this guest's own row.
		var taken int
		if err := tx.QueryRow(ctx, `
			select coalesce(sum(quantity), 0) from reservations
			where wish_id = $1 and guest_key <> $2`, wishID, guestKey).Scan(&taken); err != nil {
			return fmt.Errorf("taken: %w", err)
		}
		// A group gift is never full: several people pay towards one present.
		if !groupGift {
			if quantity > wanted {
				// Nobody else is involved: the request alone asks for too much.
				return fmt.Errorf("%w: %d asked, %d wanted", ErrInvalidQuantity, quantity, wanted)
			}
			if taken+quantity > wanted {
				return ErrTaken
			}
		}

		_, err = tx.Exec(ctx, `
			insert into reservations (wish_id, guest_key, guest_name, quantity, state, amount_cents)
			values ($1, $2, $3, $4, $5, $6)
			on conflict (wish_id, guest_key) do update
			set guest_name = excluded.guest_name,
			    quantity = excluded.quantity,
			    state = excluded.state,
			    amount_cents = excluded.amount_cents,
			    updated_at = now()`,
			wishID, guestKey, guestName, quantity, state, amountCents)
		if err != nil {
			return fmt.Errorf("reserve: %w", err)
		}
		return nil
	})
}

// Release undoes a guest's own reservation. Somebody else's is ErrNotFound: the
// cookie is what identifies a guest.
func (s *GuestStore) Release(ctx context.Context, listID, wishID int64, guestKey string) error {
	if guestKey == "" {
		return ErrNotFound
	}
	tag, err := s.pool.Exec(ctx, `
		delete from reservations
		where wish_id = $1 and guest_key = $2
		  and wish_id in (select id from wishes where list_id = $3)`,
		wishID, guestKey, listID)
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GuestComments are every comment on a wish, whatever its visibility: the
// guests see each other's messages, the ones somebody addressed to the owner
// (labelled as such on the page), and the owner's answers. The lack of a
// visibility filter here is deliberate. Adding one would hide the owner's
// answers, which carry visibility owner because that is what lets the owner
// read their own thread.
func (s *GuestStore) GuestComments(ctx context.Context, listID int64, guestKey string) (map[int64][]Comment, error) {
	rows, err := s.pool.Query(ctx, `
		select c.id, c.wish_id, c.author_kind, c.author, c.body, c.visibility,
		       c.guest_key <> '' and c.guest_key = $2, c.created_at
		from comments c
		join wishes w on w.id = c.wish_id
		where w.list_id = $1 and not w.archived
		order by c.created_at, c.id`, listID, guestKey)
	if err != nil {
		return nil, fmt.Errorf("comments: %w", err)
	}
	out, err := scanComments(rows)
	if err != nil {
		return nil, fmt.Errorf("comments: %w", err)
	}
	return out, nil
}

// AddGuestComment writes a guest's message. visibility guests keeps it among
// the guests, visibility owner asks the owner, who then sees it.
func (s *GuestStore) AddGuestComment(ctx context.Context, listID, wishID int64, guestKey, author, body, visibility string) error {
	if guestKey == "" {
		return ErrGuestKeyRequired
	}
	if visibility != VisibleToGuests && visibility != VisibleToOwner {
		return fmt.Errorf("visibility %q is not guests or owner", visibility)
	}
	tag, err := s.pool.Exec(ctx, `
		insert into comments (wish_id, author_kind, guest_key, author, body, visibility)
		select w.id, $3, $4, $5, $6, $7
		from wishes w
		join lists l on l.id = w.list_id
		where w.id = $1 and w.list_id = $2 and not w.archived and not l.archived`,
		wishID, listID, AuthorGuest, guestKey, author, body, visibility)
	if err != nil {
		return fmt.Errorf("add comment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteComment removes a guest's own comment, identified by their cookie.
func (s *GuestStore) DeleteComment(ctx context.Context, listID, commentID int64, guestKey string) error {
	if guestKey == "" {
		return ErrNotFound
	}
	tag, err := s.pool.Exec(ctx, `
		delete from comments
		where id = $1 and guest_key = $2
		  and wish_id in (select id from wishes where list_id = $3)`,
		commentID, guestKey, listID)
	if err != nil {
		return fmt.Errorf("delete comment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
