// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package owner

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/ostendorf-cloud/govue/api"

	"github.com/ostendorf-cloud/wishlist/internal/store"
	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner/dto"
)

// refusal is the one answer for a row that is not the caller's and a row that
// does not exist, and for an id that is not a number. It is a single value, so
// the bytes cannot differ.
var refusal = api.NotFound("not found")

// fail maps a store error. ErrNotFound becomes the refusal and nothing else
// does: the 500 path never describes the cause to the client.
func fail(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return refusal
	}
	return err
}

// pathID reads the {id} of the path. A malformed id is the refusal as well.
func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, refusal
	}
	return id, nil
}

func (a *API) listLists(r *http.Request, owner int64) (dto.ListOverview, error) {
	lists, err := a.store.Lists(r.Context(), owner, r.URL.Query().Get("archived") == "true")
	if err != nil {
		return dto.ListOverview{}, err
	}
	return dto.ListOverview{Lists: listDTOs(lists)}, nil
}

func (a *API) createList(r *http.Request, owner int64) (dto.List, error) {
	var req dto.ListRequest
	if err := decode(r, &req); err != nil {
		return dto.List{}, err
	}
	in, err := listInput(req)
	if err != nil {
		return dto.List{}, err
	}
	list, err := a.store.CreateList(r.Context(), owner, in)
	if err != nil {
		return dto.List{}, err
	}
	return listDTO(list), nil
}

func (a *API) getList(r *http.Request, owner int64) (dto.ListDetail, error) {
	id, err := pathID(r)
	if err != nil {
		return dto.ListDetail{}, err
	}
	list, err := a.store.List(r.Context(), owner, id)
	if err != nil {
		return dto.ListDetail{}, fail(err)
	}
	wishes, err := a.store.Wishes(r.Context(), owner, id, r.URL.Query().Get("archived") == "true")
	if err != nil {
		return dto.ListDetail{}, fail(err)
	}
	return dto.ListDetail{List: listDTO(list), Wishes: wishDTOs(wishes)}, nil
}

func (a *API) updateList(r *http.Request, owner int64) (dto.List, error) {
	id, err := pathID(r)
	if err != nil {
		return dto.List{}, err
	}
	var req dto.ListRequest
	if err := decode(r, &req); err != nil {
		return dto.List{}, err
	}
	in, err := listInput(req)
	if err != nil {
		return dto.List{}, err
	}
	if req.ReserveOpen == nil {
		current, err := a.store.List(r.Context(), owner, id)
		if err != nil {
			return dto.List{}, fail(err)
		}
		in.ReserveOpen = current.ReserveOpen
	}
	if err := a.store.UpdateList(r.Context(), owner, id, in); err != nil {
		return dto.List{}, fail(err)
	}
	list, err := a.store.List(r.Context(), owner, id)
	if err != nil {
		return dto.List{}, fail(err)
	}
	return listDTO(list), nil
}

func (a *API) archiveList(r *http.Request, owner int64) (dto.List, error) {
	id, err := pathID(r)
	if err != nil {
		return dto.List{}, err
	}
	var req dto.ArchiveRequest
	if err := decode(r, &req); err != nil {
		return dto.List{}, err
	}
	if err := a.store.SetListArchived(r.Context(), owner, id, req.Archived); err != nil {
		return dto.List{}, fail(err)
	}
	list, err := a.store.List(r.Context(), owner, id)
	if err != nil {
		return dto.List{}, fail(err)
	}
	return listDTO(list), nil
}

func (a *API) deleteList(r *http.Request, owner int64) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	keys, err := a.store.DeleteList(r.Context(), owner, id)
	if err != nil {
		return fail(err)
	}
	// Pictures are out of scope here: the keys are counted, not dropped
	// silently. The image milestone deletes the objects.
	a.logger.DebugContext(r.Context(), "list deleted, picture objects left to the image milestone",
		"pictures", len(keys))
	return nil
}

func (a *API) createWish(r *http.Request, owner int64) (dto.Wish, error) {
	listID, err := pathID(r)
	if err != nil {
		return dto.Wish{}, err
	}
	var req dto.WishRequest
	if err := decode(r, &req); err != nil {
		return dto.Wish{}, err
	}
	in, err := wishInput(req)
	if err != nil {
		return dto.Wish{}, err
	}
	wish, err := a.store.CreateWish(r.Context(), owner, listID, in)
	if err != nil {
		return dto.Wish{}, fail(err)
	}
	return wishDTO(wish), nil
}

func (a *API) getWish(r *http.Request, owner int64) (dto.Wish, error) {
	id, err := pathID(r)
	if err != nil {
		return dto.Wish{}, err
	}
	wish, err := a.store.Wish(r.Context(), owner, id)
	if err != nil {
		return dto.Wish{}, fail(err)
	}
	return wishDTO(wish), nil
}

func (a *API) updateWish(r *http.Request, owner int64) (dto.Wish, error) {
	id, err := pathID(r)
	if err != nil {
		return dto.Wish{}, err
	}
	var req dto.WishRequest
	if err := decode(r, &req); err != nil {
		return dto.Wish{}, err
	}
	in, err := wishInput(req)
	if err != nil {
		return dto.Wish{}, err
	}
	if err := a.store.UpdateWish(r.Context(), owner, id, in); err != nil {
		return dto.Wish{}, fail(err)
	}
	wish, err := a.store.Wish(r.Context(), owner, id)
	if err != nil {
		return dto.Wish{}, fail(err)
	}
	return wishDTO(wish), nil
}

func (a *API) archiveWish(r *http.Request, owner int64) (dto.Wish, error) {
	id, err := pathID(r)
	if err != nil {
		return dto.Wish{}, err
	}
	var req dto.ArchiveRequest
	if err := decode(r, &req); err != nil {
		return dto.Wish{}, err
	}
	if err := a.store.SetWishArchived(r.Context(), owner, id, req.Archived); err != nil {
		return dto.Wish{}, fail(err)
	}
	wish, err := a.store.Wish(r.Context(), owner, id)
	if err != nil {
		return dto.Wish{}, fail(err)
	}
	return wishDTO(wish), nil
}

func (a *API) deleteWish(r *http.Request, owner int64) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	key, err := a.store.DeleteWish(r.Context(), owner, id)
	if err != nil {
		return fail(err)
	}
	a.logger.DebugContext(r.Context(), "wish deleted, picture object left to the image milestone",
		"has_picture", key != "")
	return nil
}

func (a *API) moveWish(r *http.Request, owner int64) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var req dto.MoveRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	var direction int
	switch req.Direction {
	case "up":
		direction = -1
	case "down":
		direction = 1
	default:
		return api.Invalid("direction must be up or down").
			WithDetails(map[string]any{"direction": codeInvalid})
	}
	return fail(a.store.MoveWish(r.Context(), owner, id, direction))
}

func (a *API) relocateWish(r *http.Request, owner int64) (dto.Wish, error) {
	id, err := pathID(r)
	if err != nil {
		return dto.Wish{}, err
	}
	var req dto.RelocateRequest
	if err := decode(r, &req); err != nil {
		return dto.Wish{}, err
	}
	if req.ListID < 1 {
		return dto.Wish{}, api.Invalid("list_id is required").
			WithDetails(map[string]any{"list_id": codeRequired})
	}
	if err := a.store.MoveWishToList(r.Context(), owner, id, req.ListID); err != nil {
		return dto.Wish{}, fail(err)
	}
	wish, err := a.store.Wish(r.Context(), owner, id)
	if err != nil {
		return dto.Wish{}, fail(err)
	}
	return wishDTO(wish), nil
}
