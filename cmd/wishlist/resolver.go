// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"

	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner"
)

// identity is what the owner resolver needs from the sign in.
type identity interface {
	OwnerID(r *http.Request) (int64, bool)
}

// resolveOwner is the one source of the owner id of the owner API: the owners
// row id of the session the request carries, and nobody for a request without
// a signed in account. There is no other way to be an owner: the development
// owner of the earlier milestone is gone, so no setting signs requests in
// without a password.
func resolveOwner(who identity) owner.Resolver {
	return who.OwnerID
}
