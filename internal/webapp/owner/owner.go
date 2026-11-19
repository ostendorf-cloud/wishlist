// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package owner is the HTTP API of the owner of a list: lists and wishes.
//
// Three rules shape it, all of them kept honest by tests in this package.
//
//   - Who the owner is comes from the Resolver the wiring supplies and from
//     nowhere else: never from the body, the query or the path.
//   - A row of somebody else and a row that does not exist get the same answer,
//     byte for byte (refusal).
//   - No response type can reach a reservation (blindness_test.go). Every
//     handler returns a typed value that one adapter writes, and the route
//     table names the type, so the test sees exactly what goes out.
//
// The package takes the owner's side of the store and nothing else:
// boundary_test.go fails on any reference to the guest side.
package owner

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"reflect"

	"github.com/ostendorf-cloud/govue/api"

	"github.com/ostendorf-cloud/wishlist/internal/store"
)

// Prefix is where the owner API lives.
const Prefix = "/api/v1/owner"

// Resolver says who the owner of a request is. The wiring supplies it: the
// session of the owner's sign in (issue 4) once it exists. It returns false
// for a request without a signed in owner, which the handlers answer with 401.
type Resolver func(r *http.Request) (ownerID int64, ok bool)

// Store is the owner's side of the store, as far as this API uses it.
// *store.OwnerStore satisfies it, and the wiring passes nothing else: the
// interface has no method of the guest side.
type Store interface {
	Lists(ctx context.Context, ownerID int64, includeArchived bool) ([]store.List, error)
	List(ctx context.Context, ownerID, listID int64) (store.List, error)
	CreateList(ctx context.Context, ownerID int64, in store.ListInput) (store.List, error)
	UpdateList(ctx context.Context, ownerID, listID int64, in store.ListInput) error
	SetListArchived(ctx context.Context, ownerID, listID int64, archived bool) error
	DeleteList(ctx context.Context, ownerID, listID int64) ([]string, error)
	Wishes(ctx context.Context, ownerID, listID int64, includeArchived bool) ([]store.Wish, error)
	Wish(ctx context.Context, ownerID, wishID int64) (store.Wish, error)
	CreateWish(ctx context.Context, ownerID, listID int64, in store.WishInput) (store.Wish, error)
	UpdateWish(ctx context.Context, ownerID, wishID int64, in store.WishInput) error
	SetWishArchived(ctx context.Context, ownerID, wishID int64, archived bool) error
	DeleteWish(ctx context.Context, ownerID, wishID int64) (string, error)
	MoveWish(ctx context.Context, ownerID, wishID int64, direction int) error
	MoveWishToList(ctx context.Context, ownerID, wishID, listID int64) error
}

var _ Store = (*store.OwnerStore)(nil)

// API is the owner API.
type API struct {
	store  Store
	owner  Resolver
	logger *slog.Logger
}

// New builds the API. Both the store and the resolver are required: without a
// resolver nobody could be told apart from anybody else.
func New(s Store, resolve Resolver, logger *slog.Logger) (*API, error) {
	if s == nil {
		return nil, errors.New("owner api: store is required")
	}
	if resolve == nil {
		return nil, errors.New("owner api: resolver is required")
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &API{store: s, owner: resolve, logger: logger}, nil
}

// Route is one endpoint. Response is the type the handler returns, which is
// what the blindness test walks; it is nil for an answer without a body.
type Route struct {
	Method   string
	Path     string
	Response reflect.Type
	handler  http.Handler
}

// Pattern is the ServeMux pattern of the route.
func (r Route) Pattern() string { return r.Method + " " + Prefix + r.Path }

// Routes lists every endpoint.
func (a *API) Routes() []Route {
	return []Route{
		endpoint(a, http.MethodGet, "/lists", http.StatusOK, a.listLists),
		endpoint(a, http.MethodPost, "/lists", http.StatusCreated, a.createList),
		endpoint(a, http.MethodGet, "/lists/{id}", http.StatusOK, a.getList),
		endpoint(a, http.MethodPut, "/lists/{id}", http.StatusOK, a.updateList),
		endpoint(a, http.MethodPut, "/lists/{id}/archived", http.StatusOK, a.archiveList),
		action(a, http.MethodDelete, "/lists/{id}", a.deleteList),
		endpoint(a, http.MethodPost, "/lists/{id}/wishes", http.StatusCreated, a.createWish),
		endpoint(a, http.MethodGet, "/wishes/{id}", http.StatusOK, a.getWish),
		endpoint(a, http.MethodPut, "/wishes/{id}", http.StatusOK, a.updateWish),
		endpoint(a, http.MethodPut, "/wishes/{id}/archived", http.StatusOK, a.archiveWish),
		action(a, http.MethodDelete, "/wishes/{id}", a.deleteWish),
		action(a, http.MethodPost, "/wishes/{id}/move", a.moveWish),
		endpoint(a, http.MethodPut, "/wishes/{id}/list", http.StatusOK, a.relocateWish),
	}
}

// Register mounts every route on mux. wrap is the route chain (rate limit,
// CSRF) the wiring owns.
func (a *API) Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	for _, route := range a.Routes() {
		mux.Handle(route.Pattern(), wrap(route.handler))
	}
}

// authorize resolves the owner or refuses with 401.
func (a *API) authorize(r *http.Request) (int64, error) {
	id, ok := a.owner(r)
	if !ok {
		return 0, api.Unauthenticated("log in first")
	}
	return id, nil
}

// endpoint wraps a handler that answers with a value. The one place a body is
// written: handlers return Resp and never touch the ResponseWriter.
func endpoint[Resp any](a *API, method, path string, status int, h func(r *http.Request, owner int64) (Resp, error)) Route {
	return Route{
		Method:   method,
		Path:     path,
		Response: reflect.TypeFor[Resp](),
		handler: api.Handle(func(w http.ResponseWriter, r *http.Request) error {
			id, err := a.authorize(r)
			if err != nil {
				return err
			}
			body, err := h(r, id)
			if err != nil {
				return err
			}
			return api.Write(w, status, body)
		}),
	}
}

// action wraps a handler that answers 204.
func action(a *API, method, path string, h func(r *http.Request, owner int64) error) Route {
	return Route{
		Method: method,
		Path:   path,
		handler: api.Handle(func(w http.ResponseWriter, r *http.Request) error {
			id, err := a.authorize(r)
			if err != nil {
				return err
			}
			if err := h(r, id); err != nil {
				return err
			}
			api.NoContent(w)
			return nil
		}),
	}
}
