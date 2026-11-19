// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package owner

import (
	"github.com/ostendorf-cloud/wishlist/internal/store"
	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner/dto"
)

const dateLayout = "2006-01-02"

// listDTO copies field by field: a store struct is never sent as it is.
func listDTO(l store.List) dto.List {
	out := dto.List{
		ID:          l.ID,
		Title:       l.Title,
		Occasion:    l.Occasion,
		Greeting:    l.Greeting,
		ReserveOpen: l.ReserveOpen,
		Archived:    l.Archived,
		Shared:      l.Shared(),
		WishCount:   l.WishCount,
		CreatedAt:   l.CreatedAt,
		UpdatedAt:   l.UpdatedAt,
	}
	if l.EventDate != nil {
		day := l.EventDate.Format(dateLayout)
		out.EventDate = &day
	}
	return out
}

func wishDTO(w store.Wish) dto.Wish {
	return dto.Wish{
		ID:          w.ID,
		ListID:      w.ListID,
		Title:       w.Title,
		Description: w.Description,
		URL:         w.URL,
		Shop:        w.Shop,
		PriceCents:  w.PriceCents,
		Currency:    w.Currency,
		Priority:    int(w.Priority),
		Quantity:    w.Quantity,
		GroupGift:   w.GroupGift,
		Position:    w.Position,
		Archived:    w.Archived,
		CreatedAt:   w.CreatedAt,
		UpdatedAt:   w.UpdatedAt,
	}
}

func listDTOs(in []store.List) []dto.List {
	out := make([]dto.List, 0, len(in))
	for _, l := range in {
		out = append(out, listDTO(l))
	}
	return out
}

func wishDTOs(in []store.Wish) []dto.Wish {
	out := make([]dto.Wish, 0, len(in))
	for _, w := range in {
		out = append(out, wishDTO(w))
	}
	return out
}
