// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ostendorf-cloud/govue/clientip"
	"github.com/ostendorf-cloud/govue/session"

	"github.com/ostendorf-cloud/wishlist/internal/accounts"
)

// A session of another kind (a later OIDC login) shares the session store but
// is not an own account: its subject has no local: prefix, so it is nobody,
// even when the subject looks like a row id.
func TestOwnerIDRefusesASubjectWithoutTheLocalPrefix(t *testing.T) {
	t.Parallel()
	a, err := New(Config{Accounts: accounts.New(nil), ClientIPs: clientip.NewResolver(nil), AllowPlainHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(subject string) (int64, bool) {
		t.Helper()
		start := httptest.NewRecorder()
		sreq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
		if _, err := a.sessions.Start(sreq.Context(), start, session.Session{Subject: subject, Username: "x"}); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		for _, c := range start.Result().Cookies() {
			req.AddCookie(c)
		}
		var (
			id int64
			ok bool
		)
		a.SessionMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			id, ok = a.OwnerID(r)
		})).ServeHTTP(httptest.NewRecorder(), req)
		return id, ok
	}

	if id, ok := resolve("local:7"); !ok || id != 7 {
		t.Fatalf("OwnerID(local:7) = %d, %v, want 7, true: the harness does not reach the session", id, ok)
	}
	for _, subject := range []string{"7", "oidc:7", "https://idp.example/7", "Local:7", " local:7"} {
		if id, ok := resolve(subject); ok || id != 0 {
			t.Errorf("OwnerID(%q) = %d, %v, want nobody", subject, id, ok)
		}
	}
}
