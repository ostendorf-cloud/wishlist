// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package dto holds every type the owner API sends or receives.
//
// The response types are explicit and never a store struct passed through: a
// field added to a store type later does not reach the wire until somebody adds
// it here. The blindness test in the parent package walks every response type
// by reflection and fails when one can reach a reservation, a guest wish or a
// guest list (docs/adr/0001-blind-reservations.md). Nothing in this package
// may import internal/store.
package dto

import "time"

// List is one list of the owner.
type List struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Occasion string `json:"occasion"`
	Greeting string `json:"greeting"`
	// EventDate is YYYY-MM-DD, or null.
	EventDate *string `json:"event_date"`
	// ReserveOpen is the owner's own switch: whether guests may still take
	// wishes. It is a setting, not data about what was taken.
	ReserveOpen bool `json:"reserve_open"`
	Archived    bool `json:"archived"`
	// Shared says whether a guest link exists. The link itself is not part of
	// this API yet.
	Shared bool `json:"shared"`
	// WishCount counts the live wishes, filled by the overview only.
	WishCount int       `json:"wish_count"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Wish is one wish of a list.
type Wish struct {
	ID          int64     `json:"id"`
	ListID      int64     `json:"list_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	URL         string    `json:"url"`
	Shop        string    `json:"shop"`
	PriceCents  *int64    `json:"price_cents"`
	Currency    string    `json:"currency"`
	Priority    int       `json:"priority"`
	Quantity    int       `json:"quantity"`
	GroupGift   bool      `json:"group_gift"`
	Position    int       `json:"position"`
	Archived    bool      `json:"archived"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ListOverview answers GET /lists.
type ListOverview struct {
	Lists []List `json:"lists"`
}

// ListDetail answers GET /lists/{id}: the list and its wishes in order.
type ListDetail struct {
	List   List   `json:"list"`
	Wishes []Wish `json:"wishes"`
}

// ListRequest creates or replaces a list. An absent reserve_open is true on
// create and unchanged on update.
type ListRequest struct {
	Title       string `json:"title"`
	Occasion    string `json:"occasion"`
	Greeting    string `json:"greeting"`
	EventDate   string `json:"event_date"`
	ReserveOpen *bool  `json:"reserve_open"`
}

// WishRequest creates or replaces a wish. Absent currency, priority and
// quantity mean EUR, 1 and 1.
type WishRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Shop        string `json:"shop"`
	PriceCents  *int64 `json:"price_cents"`
	Currency    string `json:"currency"`
	Priority    *int   `json:"priority"`
	Quantity    *int   `json:"quantity"`
	GroupGift   bool   `json:"group_gift"`
}

// ArchiveRequest sets or clears the archived flag.
type ArchiveRequest struct {
	Archived bool `json:"archived"`
}

// MoveRequest swaps a wish with its neighbour: direction is "up" or "down".
type MoveRequest struct {
	Direction string `json:"direction"`
}

// RelocateRequest moves a wish to the end of another list of the same owner.
type RelocateRequest struct {
	ListID int64 `json:"list_id"`
}
