// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package store

// Everything the owner of a list reads and writes.
//
// No query in this file may touch the guests' table: the owner never learns
// what somebody has taken. boundary_test.go reads this file and fails when a
// string in it names that table, so the rule cannot be lost in a later change.
// The guests' side, including every guest comment query, is in guest.go.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// UpsertOwner records the account behind an OIDC subject and returns it. The
// subject is the key: a rename at the identity provider changes the username,
// not the rows.
func (s *OwnerStore) UpsertOwner(ctx context.Context, subject, username, displayName, email string) (Owner, error) {
	var owner Owner
	err := s.pool.QueryRow(ctx, `
		insert into owners (subject, username, display_name, email)
		values ($1, $2, $3, $4)
		on conflict (subject) do update
		set username = excluded.username,
		    display_name = excluded.display_name,
		    email = excluded.email,
		    last_seen_at = now()
		returning id, subject, username, display_name, email, created_at, last_seen_at`,
		subject, username, displayName, email,
	).Scan(&owner.ID, &owner.Subject, &owner.Username, &owner.DisplayName, &owner.Email,
		&owner.CreatedAt, &owner.LastSeenAt)
	if err != nil {
		return Owner{}, fmt.Errorf("upsert owner: %w", err)
	}
	return owner, nil
}

// scanListsWithCount reads rows of listColumns plus one trailing wish count.
func scanListsWithCount(rows pgx.Rows) ([]List, error) {
	defer rows.Close()

	var out []List
	for rows.Next() {
		var list List
		if err := rows.Scan(&list.ID, &list.OwnerID, &list.Title, &list.Occasion, &list.EventDate,
			&list.Greeting, &list.ShareToken, &list.ReserveOpen, &list.Archived,
			&list.CreatedAt, &list.UpdatedAt, &list.WishCount); err != nil {
			return nil, err
		}
		out = append(out, list)
	}
	return out, rows.Err()
}

// Lists are the owner's lists, newest first, with the number of wishes on each.
func (s *OwnerStore) Lists(ctx context.Context, ownerID int64, includeArchived bool) ([]List, error) {
	rows, err := s.pool.Query(ctx, `
		select `+listColumns+`,
		       (select count(*) from wishes w where w.list_id = l.id and not w.archived)
		from lists l
		where owner_id = $1 and ($2 or not archived)
		order by archived, created_at desc`,
		ownerID, includeArchived)
	if err != nil {
		return nil, fmt.Errorf("lists: %w", err)
	}
	out, err := scanListsWithCount(rows)
	if err != nil {
		return nil, fmt.Errorf("lists: %w", err)
	}
	return out, nil
}

// List is one of the owner's lists. A list of somebody else is ErrNotFound.
func (s *OwnerStore) List(ctx context.Context, ownerID, listID int64) (List, error) {
	var list List
	err := scanList(s.pool.QueryRow(ctx,
		`select `+listColumns+` from lists where id = $1 and owner_id = $2`, listID, ownerID), &list)
	if err != nil {
		return List{}, fmt.Errorf("list: %w", notFound(err))
	}
	return list, nil
}

// CreateList adds a list. The guest link is created separately, so a list can
// be filled before anybody can see it.
func (s *OwnerStore) CreateList(ctx context.Context, ownerID int64, in ListInput) (List, error) {
	var list List
	err := scanList(s.pool.QueryRow(ctx, `
		insert into lists (owner_id, title, occasion, event_date, greeting, reserve_open)
		values ($1, $2, $3, $4, $5, $6)
		returning `+listColumns,
		ownerID, in.Title, in.Occasion, in.EventDate, in.Greeting, in.ReserveOpen), &list)
	if err != nil {
		return List{}, fmt.Errorf("create list: %w", err)
	}
	return list, nil
}

// UpdateList writes the editable fields of a list.
func (s *OwnerStore) UpdateList(ctx context.Context, ownerID, listID int64, in ListInput) error {
	tag, err := s.pool.Exec(ctx, `
		update lists
		set title = $3, occasion = $4, event_date = $5, greeting = $6, reserve_open = $7,
		    updated_at = now()
		where id = $1 and owner_id = $2`,
		listID, ownerID, in.Title, in.Occasion, in.EventDate, in.Greeting, in.ReserveOpen)
	if err != nil {
		return fmt.Errorf("update list: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetListArchived hides a list from the overview and from its guest link
// without deleting anything.
func (s *OwnerStore) SetListArchived(ctx context.Context, ownerID, listID int64, archived bool) error {
	tag, err := s.pool.Exec(ctx,
		`update lists set archived = $3, updated_at = now() where id = $1 and owner_id = $2`,
		listID, ownerID, archived)
	if err != nil {
		return fmt.Errorf("archive list: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetShareToken sets or rotates the guest link, or turns it off with an empty
// token. Rotating invalidates every link already handed out, which is the point.
func (s *OwnerStore) SetShareToken(ctx context.Context, ownerID, listID int64, token string) error {
	var value *string
	if token != "" {
		value = &token
	}
	tag, err := s.pool.Exec(ctx,
		`update lists set share_token = $3, updated_at = now() where id = $1 and owner_id = $2`,
		listID, ownerID, value)
	if err != nil {
		return fmt.Errorf("share token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteList removes the list and, through the foreign keys, everything that
// hangs off it. It returns the object keys of the pictures, which the caller
// deletes from the object store afterwards.
func (s *OwnerStore) DeleteList(ctx context.Context, ownerID, listID int64) ([]string, error) {
	var keys []string
	err := runTx(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			select image_key from wishes
			where list_id = (select id from lists where id = $1 and owner_id = $2)
			  and image_key <> ''`, listID, ownerID)
		if err != nil {
			return fmt.Errorf("pictures: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				return fmt.Errorf("pictures: %w", err)
			}
			keys = append(keys, key)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("pictures: %w", err)
		}
		rows.Close()

		tag, err := tx.Exec(ctx, `delete from lists where id = $1 and owner_id = $2`, listID, ownerID)
		if err != nil {
			return fmt.Errorf("delete list: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}

// Wishes are the wishes of one of the owner's lists, in the list's order. What
// guests did with them is not part of the answer.
func (s *OwnerStore) Wishes(ctx context.Context, ownerID, listID int64, includeArchived bool) ([]Wish, error) {
	rows, err := s.pool.Query(ctx, `
		select `+wishColumns+`
		from wishes
		where list_id = (select id from lists where id = $1 and owner_id = $2)
		  and ($3 or not archived)
		order by archived, position, id`,
		listID, ownerID, includeArchived)
	if err != nil {
		return nil, fmt.Errorf("wishes: %w", err)
	}
	defer rows.Close()

	var out []Wish
	for rows.Next() {
		var wish Wish
		if err := scanWish(rows, &wish); err != nil {
			return nil, fmt.Errorf("wishes: %w", err)
		}
		out = append(out, wish)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("wishes: %w", err)
	}
	return out, nil
}

// Wish is one wish of the owner, whichever list it is on.
func (s *OwnerStore) Wish(ctx context.Context, ownerID, wishID int64) (Wish, error) {
	var wish Wish
	err := scanWish(s.pool.QueryRow(ctx, `
		select `+wishColumns+`
		from wishes
		where id = $1 and list_id in (select id from lists where owner_id = $2)`,
		wishID, ownerID), &wish)
	if err != nil {
		return Wish{}, fmt.Errorf("wish: %w", notFound(err))
	}
	return wish, nil
}

// CreateWish appends a wish to the list, at the end of its order.
func (s *OwnerStore) CreateWish(ctx context.Context, ownerID, listID int64, in WishInput) (Wish, error) {
	var wish Wish
	err := scanWish(s.pool.QueryRow(ctx, `
		insert into wishes (list_id, title, description, url, shop, price_cents, currency,
		                    priority, quantity, group_gift, position)
		select l.id, $3, $4, $5, $6, $7, $8, $9, $10, $11,
		       coalesce((select max(position) + 1 from wishes w where w.list_id = l.id), 0)
		from lists l
		where l.id = $1 and l.owner_id = $2
		returning `+wishColumns,
		listID, ownerID, in.Title, in.Description, in.URL, in.Shop, in.PriceCents,
		currencyOrDefault(in.Currency), in.Priority, quantityOrOne(in.Quantity), in.GroupGift), &wish)
	if err != nil {
		return Wish{}, fmt.Errorf("create wish: %w", notFound(err))
	}
	return wish, nil
}

// UpdateWish writes the editable fields.
func (s *OwnerStore) UpdateWish(ctx context.Context, ownerID, wishID int64, in WishInput) error {
	tag, err := s.pool.Exec(ctx, `
		update wishes
		set title = $3, description = $4, url = $5, shop = $6, price_cents = $7, currency = $8,
		    priority = $9, quantity = $10, group_gift = $11, updated_at = now()
		where id = $1 and list_id in (select id from lists where owner_id = $2)`,
		wishID, ownerID, in.Title, in.Description, in.URL, in.Shop, in.PriceCents,
		currencyOrDefault(in.Currency), in.Priority, quantityOrOne(in.Quantity), in.GroupGift)
	if err != nil {
		return fmt.Errorf("update wish: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetWishArchived takes a wish off the guest's view without deleting it, for a
// wish that was fulfilled elsewhere.
func (s *OwnerStore) SetWishArchived(ctx context.Context, ownerID, wishID int64, archived bool) error {
	tag, err := s.pool.Exec(ctx, `
		update wishes set archived = $3, updated_at = now()
		where id = $1 and list_id in (select id from lists where owner_id = $2)`,
		wishID, ownerID, archived)
	if err != nil {
		return fmt.Errorf("archive wish: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteWish removes a wish and returns its picture's object key.
func (s *OwnerStore) DeleteWish(ctx context.Context, ownerID, wishID int64) (string, error) {
	var key string
	err := s.pool.QueryRow(ctx, `
		delete from wishes
		where id = $1 and list_id in (select id from lists where owner_id = $2)
		returning image_key`, wishID, ownerID).Scan(&key)
	if err != nil {
		return "", fmt.Errorf("delete wish: %w", notFound(err))
	}
	return key, nil
}

// MoveWish swaps a wish with its neighbour, up for -1 and down for 1: the whole
// ordering the owner needs, without a drag and drop protocol.
func (s *OwnerStore) MoveWish(ctx context.Context, ownerID, wishID int64, direction int) error {
	if direction != -1 && direction != 1 {
		return errors.New("direction is -1 or 1")
	}
	return runTx(ctx, s.pool, func(tx pgx.Tx) error {
		// The list row is locked first, so every move inside one list runs one
		// after the other and always takes its locks in the same order: list,
		// then wishes. Locking only the moved wish let two moves of adjacent
		// wishes wait on each other (deadlock, 40P01) or write a stale
		// position. "no key update" does not block a wish being added to the
		// list, which only needs a key share lock.
		var listID int64
		err := tx.QueryRow(ctx, `
			select id from lists
			where id = (select w.list_id from wishes w
			            join lists o on o.id = w.list_id
			            where w.id = $1 and o.owner_id = $2)
			for no key update`, wishID, ownerID).Scan(&listID)
		if err != nil {
			return fmt.Errorf("move wish: %w", notFound(err))
		}

		// Read again under the lock: the position may have changed while this
		// move waited for it. "no key update" is enough, only non key columns
		// change. A guest's comment, which takes a key share lock on the wish,
		// goes through meanwhile. A guest's Reserve takes "for no key update" on
		// the same wish row, so one waits for the other. Reserve needs only that
		// row before its insert, so no lock cycle is expected, but no test runs
		// the two together.
		var position int
		var archived bool
		err = tx.QueryRow(ctx, `
			select position, archived from wishes
			where id = $1 and list_id = $2
			for no key update`, wishID, listID).Scan(&position, &archived)
		if err != nil {
			return fmt.Errorf("move wish: %w", notFound(err))
		}

		// The neighbour by order, not by position value: positions may repeat
		// after imports, and the tie breaker is the id. Archived and live wishes
		// are moved among their own kind, or moving an archived one would
		// reorder the guest page for no visible reason.
		var neighbourID int64
		var neighbourPosition int
		query := `
			select id, position from wishes
			where list_id = $1 and archived = $4 and (position, id) < ($2, $3)
			order by position desc, id desc limit 1`
		if direction == 1 {
			query = `
			select id, position from wishes
			where list_id = $1 and archived = $4 and (position, id) > ($2, $3)
			order by position, id limit 1`
		}
		err = tx.QueryRow(ctx, query, listID, position, wishID, archived).Scan(&neighbourID, &neighbourPosition)
		if errors.Is(err, pgx.ErrNoRows) {
			// Already at the end; moving further is not an error.
			return nil
		}
		if err != nil {
			return fmt.Errorf("move wish: %w", err)
		}
		if neighbourPosition == position {
			// Same position value: give the moved wish the neighbour's place by
			// stepping it, the ids then order them the other way round.
			neighbourPosition = position + direction
		}
		if _, err := tx.Exec(ctx,
			`update wishes set position = $2, updated_at = now() where id = $1`,
			wishID, neighbourPosition); err != nil {
			return fmt.Errorf("move wish: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`update wishes set position = $2, updated_at = now() where id = $1`,
			neighbourID, position); err != nil {
			return fmt.Errorf("move wish: %w", err)
		}
		return nil
	})
}

// foreignKeyViolation is PostgreSQL's SQLSTATE for a row whose parent is gone.
const foreignKeyViolation = "23503"

// MoveWishToList puts a wish at the end of another list. Both the wish and the
// list have to be the owner's: a foreign wish and a foreign list are alike
// ErrNotFound, so the caller cannot tell them from rows that do not exist. The
// wish keeps its archived flag. Moving a wish into the list it is already on
// puts it last. Two moves into one list at the same moment may pick the same
// position, which is harmless: the id breaks the tie when the list is read.
// A list deleted between the check and the write is a foreign key violation
// (23503) and is ErrNotFound as well. The reservations of the wish are not
// touched and move along with it. The guest milestone decides whether that
// stays, see AGENTS.md "Refusals".
func (s *OwnerStore) MoveWishToList(ctx context.Context, ownerID, wishID, listID int64) error {
	tag, err := s.pool.Exec(ctx, `
		update wishes w
		set list_id = l.id,
		    position = coalesce((select max(x.position) + 1 from wishes x where x.list_id = l.id), 0),
		    updated_at = now()
		from lists l
		where w.id = $1
		  and w.list_id in (select id from lists where owner_id = $2)
		  and l.id = $3 and l.owner_id = $2`,
		wishID, ownerID, listID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
			return ErrNotFound
		}
		return fmt.Errorf("move wish to list: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// EventsSoon are the owner's lists whose date is within the given duration,
// for a reminder.
func (s *OwnerStore) EventsSoon(ctx context.Context, ownerID int64, within time.Duration) ([]List, error) {
	rows, err := s.pool.Query(ctx, `
		select `+listColumns+`, 0
		from lists l
		where owner_id = $1 and not archived and event_date is not null
		  and event_date between current_date and current_date + make_interval(days => $2)
		order by event_date`,
		ownerID, int(within.Hours()/24))
	if err != nil {
		return nil, fmt.Errorf("events: %w", err)
	}
	out, err := scanListsWithCount(rows)
	if err != nil {
		return nil, fmt.Errorf("events: %w", err)
	}
	return out, nil
}

// OwnerQuestions are the comments guests addressed to the owner, per wish, with
// the owner's own answers next to them. Guest-only comments are not part of
// the answer.
func (s *OwnerStore) OwnerQuestions(ctx context.Context, ownerID, listID int64) (map[int64][]Comment, error) {
	rows, err := s.pool.Query(ctx, `
		select c.id, c.wish_id, c.author_kind, c.author, c.body, c.visibility, false, c.created_at
		from comments c
		join wishes w on w.id = c.wish_id
		where w.list_id = (select id from lists where id = $1 and owner_id = $2)
		  and c.visibility = 'owner'
		order by c.created_at, c.id`, listID, ownerID)
	if err != nil {
		return nil, fmt.Errorf("questions: %w", err)
	}
	out, err := scanComments(rows)
	if err != nil {
		return nil, fmt.Errorf("questions: %w", err)
	}
	return out, nil
}

// AddOwnerReply answers a question guests asked. visibility owner is what the
// schema forces on an owner's comment, and it means both sides read it: the
// owner through OwnerQuestions, the guests through GuestComments, which
// filters nothing. An answer only the owner could see would be pointless.
func (s *OwnerStore) AddOwnerReply(ctx context.Context, ownerID, wishID int64, body string) error {
	tag, err := s.pool.Exec(ctx, `
		insert into comments (wish_id, author_kind, author, body, visibility)
		select w.id, $3, '', $4, $5
		from wishes w
		where w.id = $1 and w.list_id in (select id from lists where owner_id = $2)`,
		wishID, ownerID, AuthorOwner, body, VisibleToOwner)
	if err != nil {
		return fmt.Errorf("reply: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// scanComments reads rows of the eight comment columns, grouped by wish.
func scanComments(rows pgx.Rows) (map[int64][]Comment, error) {
	defer rows.Close()

	out := map[int64][]Comment{}
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.WishID, &c.AuthorKind, &c.Author, &c.Body, &c.Visibility,
			&c.Mine, &c.CreatedAt); err != nil {
			return nil, err
		}
		out[c.WishID] = append(out[c.WishID], c)
	}
	return out, rows.Err()
}

func currencyOrDefault(currency string) string {
	if currency == "" {
		return "EUR"
	}
	return currency
}

func quantityOrOne(quantity int) int {
	if quantity < 1 {
		return 1
	}
	return quantity
}
