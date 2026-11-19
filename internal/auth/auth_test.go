// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ostendorf-cloud/govue/clientip"

	"github.com/ostendorf-cloud/wishlist/internal/accounts"
	"github.com/ostendorf-cloud/wishlist/internal/auth"
)

func TestNewRequiresItsParts(t *testing.T) {
	t.Parallel()
	if _, err := auth.New(auth.Config{ClientIPs: clientip.NewResolver(nil)}); err == nil {
		t.Error("New without an account store succeeded")
	}
	if _, err := auth.New(auth.Config{Accounts: accounts.New(nil)}); err == nil {
		t.Error("New without a client address resolver succeeded")
	}
}

// The resolver reads the session the middleware put in the context. A request
// that never went through it is nobody: forgetting the middleware fails closed.
func TestARequestWithoutASessionIsNobody(t *testing.T) {
	t.Parallel()
	a, err := auth.New(auth.Config{Accounts: accounts.New(nil), ClientIPs: clientip.NewResolver(nil)})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	if id, ok := a.OwnerID(req); ok || id != 0 {
		t.Errorf("OwnerID() = %d, %v, want nobody", id, ok)
	}
	if _, ok := a.CSRFExpected()(req); ok {
		t.Error("a request without a session has an expected CSRF token")
	}
}
