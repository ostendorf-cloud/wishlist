// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package ownertest is an in memory owner store for handler tests. It scopes
// every call by owner id the way the SQL does and answers store.ErrNotFound for
// a row of somebody else. The queries themselves are tested against a real
// PostgreSQL in internal/store.
package ownertest

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/ostendorf-cloud/wishlist/internal/store"
)

// Fake implements owner.Store.
type Fake struct {
	mu     sync.Mutex
	nextID int64
	lists  []store.List
	wishes []store.Wish
	// Err, when set, is returned by every call: it drives the 500 path.
	Err error
}

// New returns an empty fake.
func New() *Fake { return &Fake{} }

func (f *Fake) id() int64 {
	f.nextID++
	return f.nextID
}

func (f *Fake) list(ownerID, listID int64) (*store.List, error) {
	for i := range f.lists {
		if f.lists[i].ID == listID && f.lists[i].OwnerID == ownerID {
			return &f.lists[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *Fake) wish(ownerID, wishID int64) (*store.Wish, error) {
	for i := range f.wishes {
		if f.wishes[i].ID != wishID {
			continue
		}
		if _, err := f.list(ownerID, f.wishes[i].ListID); err != nil {
			return nil, err
		}
		return &f.wishes[i], nil
	}
	return nil, store.ErrNotFound
}

// Lists implements owner.Store.
func (f *Fake) Lists(_ context.Context, ownerID int64, includeArchived bool) ([]store.List, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return nil, f.Err
	}
	var out []store.List
	for _, l := range f.lists {
		if l.OwnerID != ownerID || (l.Archived && !includeArchived) {
			continue
		}
		for _, w := range f.wishes {
			if w.ListID == l.ID && !w.Archived {
				l.WishCount++
			}
		}
		out = append(out, l)
	}
	return out, nil
}

// List implements owner.Store.
func (f *Fake) List(_ context.Context, ownerID, listID int64) (store.List, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return store.List{}, f.Err
	}
	l, err := f.list(ownerID, listID)
	if err != nil {
		return store.List{}, err
	}
	return *l, nil
}

// CreateList implements owner.Store.
func (f *Fake) CreateList(_ context.Context, ownerID int64, in store.ListInput) (store.List, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return store.List{}, f.Err
	}
	now := time.Now().UTC()
	l := store.List{
		ID: f.id(), OwnerID: ownerID, Title: in.Title, Occasion: in.Occasion,
		Greeting: in.Greeting, EventDate: in.EventDate, ReserveOpen: in.ReserveOpen,
		CreatedAt: now, UpdatedAt: now,
	}
	f.lists = append(f.lists, l)
	return l, nil
}

// UpdateList implements owner.Store.
func (f *Fake) UpdateList(_ context.Context, ownerID, listID int64, in store.ListInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	l, err := f.list(ownerID, listID)
	if err != nil {
		return err
	}
	l.Title, l.Occasion, l.Greeting = in.Title, in.Occasion, in.Greeting
	l.EventDate, l.ReserveOpen = in.EventDate, in.ReserveOpen
	return nil
}

// SetListArchived implements owner.Store.
func (f *Fake) SetListArchived(_ context.Context, ownerID, listID int64, archived bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	l, err := f.list(ownerID, listID)
	if err != nil {
		return err
	}
	l.Archived = archived
	return nil
}

// DeleteList implements owner.Store.
func (f *Fake) DeleteList(_ context.Context, ownerID, listID int64) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return nil, f.Err
	}
	if _, err := f.list(ownerID, listID); err != nil {
		return nil, err
	}
	f.lists = slices.DeleteFunc(f.lists, func(l store.List) bool { return l.ID == listID })
	f.wishes = slices.DeleteFunc(f.wishes, func(w store.Wish) bool { return w.ListID == listID })
	return nil, nil
}

// Wishes implements owner.Store.
func (f *Fake) Wishes(_ context.Context, ownerID, listID int64, includeArchived bool) ([]store.Wish, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return nil, f.Err
	}
	if _, err := f.list(ownerID, listID); err != nil {
		return nil, nil
	}
	var out []store.Wish
	for _, w := range f.wishes {
		if w.ListID == listID && (includeArchived || !w.Archived) {
			out = append(out, w)
		}
	}
	slices.SortStableFunc(out, func(a, b store.Wish) int { return a.Position - b.Position })
	return out, nil
}

// Wish implements owner.Store.
func (f *Fake) Wish(_ context.Context, ownerID, wishID int64) (store.Wish, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return store.Wish{}, f.Err
	}
	w, err := f.wish(ownerID, wishID)
	if err != nil {
		return store.Wish{}, err
	}
	return *w, nil
}

func (f *Fake) end(listID int64) int {
	next := 0
	for _, w := range f.wishes {
		if w.ListID == listID && w.Position >= next {
			next = w.Position + 1
		}
	}
	return next
}

// CreateWish implements owner.Store.
func (f *Fake) CreateWish(_ context.Context, ownerID, listID int64, in store.WishInput) (store.Wish, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return store.Wish{}, f.Err
	}
	if _, err := f.list(ownerID, listID); err != nil {
		return store.Wish{}, err
	}
	now := time.Now().UTC()
	w := store.Wish{
		ID: f.id(), ListID: listID, Title: in.Title, Description: in.Description, URL: in.URL,
		Shop: in.Shop, PriceCents: in.PriceCents, Currency: in.Currency, Priority: in.Priority,
		Quantity: in.Quantity, GroupGift: in.GroupGift, Position: f.end(listID),
		CreatedAt: now, UpdatedAt: now,
	}
	f.wishes = append(f.wishes, w)
	return w, nil
}

// UpdateWish implements owner.Store.
func (f *Fake) UpdateWish(_ context.Context, ownerID, wishID int64, in store.WishInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	w, err := f.wish(ownerID, wishID)
	if err != nil {
		return err
	}
	w.Title, w.Description, w.URL, w.Shop = in.Title, in.Description, in.URL, in.Shop
	w.PriceCents, w.Currency, w.Priority = in.PriceCents, in.Currency, in.Priority
	w.Quantity, w.GroupGift = in.Quantity, in.GroupGift
	return nil
}

// SetWishArchived implements owner.Store.
func (f *Fake) SetWishArchived(_ context.Context, ownerID, wishID int64, archived bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	w, err := f.wish(ownerID, wishID)
	if err != nil {
		return err
	}
	w.Archived = archived
	return nil
}

// DeleteWish implements owner.Store.
func (f *Fake) DeleteWish(_ context.Context, ownerID, wishID int64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return "", f.Err
	}
	if _, err := f.wish(ownerID, wishID); err != nil {
		return "", err
	}
	f.wishes = slices.DeleteFunc(f.wishes, func(w store.Wish) bool { return w.ID == wishID })
	return "", nil
}

// MoveWish implements owner.Store.
func (f *Fake) MoveWish(_ context.Context, ownerID, wishID int64, direction int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	if direction != -1 && direction != 1 {
		return errors.New("direction is -1 or 1")
	}
	w, err := f.wish(ownerID, wishID)
	if err != nil {
		return err
	}
	var neighbour *store.Wish
	for i := range f.wishes {
		o := &f.wishes[i]
		if o.ListID != w.ListID || o.ID == w.ID || o.Archived != w.Archived {
			continue
		}
		if direction == 1 && o.Position > w.Position && (neighbour == nil || o.Position < neighbour.Position) {
			neighbour = o
		}
		if direction == -1 && o.Position < w.Position && (neighbour == nil || o.Position > neighbour.Position) {
			neighbour = o
		}
	}
	if neighbour != nil {
		w.Position, neighbour.Position = neighbour.Position, w.Position
	}
	return nil
}

// MoveWishToList implements owner.Store.
func (f *Fake) MoveWishToList(_ context.Context, ownerID, wishID, listID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	w, err := f.wish(ownerID, wishID)
	if err != nil {
		return err
	}
	if _, err := f.list(ownerID, listID); err != nil {
		return err
	}
	position := f.end(listID)
	w.ListID = listID
	w.Position = position
	return nil
}
