// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"time"

	"github.com/jackc/pgx/v5"
)

// The types both sides share. Nothing here carries a reservation: the types
// only the guest side may see (Reservation, GuestWish, GuestList) are in
// guest.go.

// Owner is one account at the identity provider that collects wishes.
type Owner struct {
	ID          int64
	Subject     string
	Username    string
	DisplayName string
	Email       string
	CreatedAt   time.Time
	LastSeenAt  time.Time
}

// Name is what the guest side greets with: the display name if the identity
// provider gave one, the username otherwise.
func (o Owner) Name() string {
	if o.DisplayName != "" {
		return o.DisplayName
	}
	return o.Username
}

// List is one occasion, for example a birthday, with its guest link.
type List struct {
	ID          int64
	OwnerID     int64
	Title       string
	Occasion    string
	Greeting    string
	EventDate   *time.Time
	ShareToken  string
	ReserveOpen bool
	Archived    bool
	CreatedAt   time.Time
	UpdatedAt   time.Time

	// WishCount is filled by the owner's overview only.
	WishCount int
}

// Shared says whether a guest link exists at all.
func (l List) Shared() bool { return l.ShareToken != "" }

// ListInput is the editable part of a list.
type ListInput struct {
	Title       string
	Occasion    string
	Greeting    string
	EventDate   *time.Time
	ReserveOpen bool
}

// Wish is one entry of a list.
type Wish struct {
	ID          int64
	ListID      int64
	Title       string
	Description string
	URL         string
	Shop        string
	PriceCents  *int64
	Currency    string
	ImageKey    string
	ImageURL    string
	Priority    int16
	Quantity    int
	GroupGift   bool
	Position    int
	Archived    bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// HasImage is true when either a picture was uploaded or a shop picture is
// known.
func (w Wish) HasImage() bool { return w.ImageKey != "" || w.ImageURL != "" }

// WishInput is the editable part of a wish. The picture is not part of it:
// it has its own write, which belongs to the image milestone.
type WishInput struct {
	Title       string
	Description string
	URL         string
	Shop        string
	PriceCents  *int64
	Currency    string
	Priority    int16
	Quantity    int
	GroupGift   bool
}

// Comment is one message on a wish.
type Comment struct {
	ID         int64
	WishID     int64
	AuthorKind string
	Author     string
	Body       string
	Visibility string
	// Mine is true for the viewing guest's own comments. Always false on the
	// owner's side.
	Mine      bool
	CreatedAt time.Time
}

// Comment author kinds and visibilities.
const (
	AuthorGuest     = "guest"
	AuthorOwner     = "owner"
	VisibleToGuests = "guests"
	VisibleToOwner  = "owner"
)

// FromOwner says whether the owner wrote it, which the UI labels differently.
func (c Comment) FromOwner() bool { return c.AuthorKind == AuthorOwner }

const listColumns = `id, owner_id, title, occasion, event_date, greeting,
	coalesce(share_token, ''), reserve_open, archived, created_at, updated_at`

func scanList(row pgx.Row, list *List) error {
	return row.Scan(&list.ID, &list.OwnerID, &list.Title, &list.Occasion, &list.EventDate,
		&list.Greeting, &list.ShareToken, &list.ReserveOpen, &list.Archived,
		&list.CreatedAt, &list.UpdatedAt)
}

const wishColumns = `id, list_id, title, description, url, shop, price_cents, currency,
	image_key, image_url, priority, quantity, group_gift, position, archived,
	created_at, updated_at`

func scanWish(row pgx.Row, wish *Wish) error {
	return row.Scan(&wish.ID, &wish.ListID, &wish.Title, &wish.Description, &wish.URL, &wish.Shop,
		&wish.PriceCents, &wish.Currency, &wish.ImageKey, &wish.ImageURL, &wish.Priority,
		&wish.Quantity, &wish.GroupGift, &wish.Position, &wish.Archived,
		&wish.CreatedAt, &wish.UpdatedAt)
}
