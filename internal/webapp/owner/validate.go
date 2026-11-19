// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package owner

import (
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ostendorf-cloud/govue/api"

	"github.com/ostendorf-cloud/wishlist/internal/store"
	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner/dto"
)

// Limits, in runes where a person types the text. They match what
// old_wishlist's forms allowed.
const (
	maxBody        = 64 << 10
	maxListTitle   = 200
	maxOccasion    = 200
	maxGreeting    = 2000
	maxWishTitle   = 300
	maxDescription = 4000
	maxURL         = 2000
	maxShop        = 200
	maxPriceCents  = 1_000_000_000
	maxQuantity    = 99
)

// Field error codes. They are keys, not text: the SPA maps them to words.
const (
	codeRequired = "required"
	codeTooLong  = "too_long"
	codeInvalid  = "invalid"
)

// fieldErrors collects the problems of one request.
type fieldErrors map[string]any

func (f fieldErrors) err() error {
	if len(f) == 0 {
		return nil
	}
	return api.Invalid("some fields are not valid").WithDetails(f)
}

// decode reads a JSON body. Unlike api.Decode it requires the content type:
// a missing header is refused too, so a form post cannot reach a handler.
func decode(r *http.Request, into any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return api.New(http.StatusUnsupportedMediaType, api.CodeUnsupportedMediaType,
			"content type must be application/json")
	}
	return api.DecodeLimit(r, into, maxBody)
}

// text trims a field and checks its length.
func text(f fieldErrors, field, value string, limit int, required bool) string {
	value = strings.TrimSpace(value)
	switch {
	case value == "" && required:
		f[field] = codeRequired
	case utf8.RuneCountInString(value) > limit:
		f[field] = codeTooLong
	}
	return value
}

// listInput validates a list request. It never reads the store, so the answer
// to a bad body cannot depend on whether the list exists. An absent
// reserve_open is true here; an update replaces it with the stored value.
func listInput(req dto.ListRequest) (store.ListInput, error) {
	f := fieldErrors{}
	in := store.ListInput{
		Title:    text(f, "title", req.Title, maxListTitle, true),
		Occasion: text(f, "occasion", req.Occasion, maxOccasion, false),
		Greeting: text(f, "greeting", req.Greeting, maxGreeting, false),
	}
	if raw := strings.TrimSpace(req.EventDate); raw != "" {
		day, err := time.Parse(dateLayout, raw)
		if err != nil {
			f["event_date"] = codeInvalid
		}
		in.EventDate = &day
	}
	in.ReserveOpen = req.ReserveOpen == nil || *req.ReserveOpen
	return in, f.err()
}

// wishInput validates a wish request.
func wishInput(req dto.WishRequest) (store.WishInput, error) {
	f := fieldErrors{}
	in := store.WishInput{
		Title:       text(f, "title", req.Title, maxWishTitle, true),
		Description: text(f, "description", req.Description, maxDescription, false),
		URL:         text(f, "url", req.URL, maxURL, false),
		Shop:        text(f, "shop", req.Shop, maxShop, false),
		GroupGift:   req.GroupGift,
		Currency:    "EUR",
		Priority:    1,
		Quantity:    1,
	}
	if _, bad := f["url"]; !bad && in.URL != "" && !webLink(in.URL) {
		f["url"] = codeInvalid
	}
	if req.PriceCents != nil {
		if *req.PriceCents < 0 || *req.PriceCents > maxPriceCents {
			f["price_cents"] = codeInvalid
		}
		in.PriceCents = req.PriceCents
	}
	if raw := strings.ToUpper(strings.TrimSpace(req.Currency)); raw != "" {
		if !currencyCode(raw) {
			f["currency"] = codeInvalid
		}
		in.Currency = raw
	}
	if req.Priority != nil {
		if *req.Priority < 0 || *req.Priority > 2 {
			f["priority"] = codeInvalid
		}
		in.Priority = int16(max(0, min(*req.Priority, 2)))
	}
	if req.Quantity != nil {
		if *req.Quantity < 1 || *req.Quantity > maxQuantity {
			f["quantity"] = codeInvalid
		}
		in.Quantity = max(1, min(*req.Quantity, maxQuantity))
	}
	return in, f.err()
}

// webLink accepts an absolute http or https URL with a host and no
// credentials. Anything else, javascript: above all, is refused.
func webLink(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func currencyCode(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, c := range s {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}
