// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ostendorf-cloud/wishlist/internal/store"
)

type fakeUpserter struct {
	calls   int
	subject string
	fail    error
}

func (f *fakeUpserter) UpsertOwner(_ context.Context, subject, username, _, _ string) (store.Owner, error) {
	f.calls++
	f.subject = subject
	if f.fail != nil {
		return store.Owner{}, f.fail
	}
	return store.Owner{ID: 42, Subject: subject, Username: username}, nil
}

func request(t *testing.T) *http.Request {
	t.Helper()
	return httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
}

func TestWithoutADevelopmentOwnerNobodyIsAnOwner(t *testing.T) {
	up := &fakeUpserter{}
	resolve := resolveOwner("", up, slog.New(slog.DiscardHandler))

	r := request(t)
	// Nothing a request carries makes it an owner.
	r.Header.Set("X-Owner", "1")
	r.AddCookie(&http.Cookie{Name: "owner", Value: "1"})
	if id, ok := resolve(r); ok || id != 0 {
		t.Errorf("resolve() = %d, %v, want no owner", id, ok)
	}
	if up.calls != 0 {
		t.Error("the store was asked for an account")
	}
}

func TestTheDevelopmentOwnerIsCreatedOnceAndRetriedWhileTheDatabaseIsDown(t *testing.T) {
	up := &fakeUpserter{fail: errors.New("connection refused")}
	resolve := resolveOwner("dev", up, slog.New(slog.DiscardHandler))

	if id, ok := resolve(request(t)); ok || id != 0 {
		t.Fatalf("resolve() with the database down = %d, %v, want no owner", id, ok)
	}
	up.fail = nil
	for range 3 {
		if id, ok := resolve(request(t)); !ok || id != 42 {
			t.Fatalf("resolve() = %d, %v, want 42", id, ok)
		}
	}
	if up.calls != 2 || up.subject != "dev:dev" {
		t.Errorf("upserts = %d for %q, want one failed and one successful for dev:dev", up.calls, up.subject)
	}
}
