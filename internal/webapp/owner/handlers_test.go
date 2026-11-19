// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package owner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/govue/api"

	"github.com/ostendorf-cloud/wishlist/internal/store"
	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner/dto"
	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner/ownertest"
)

// ownerHeader is how the fake resolver of these tests names the owner. The
// production resolver reads a session; nothing here reads a body or a query.
const ownerHeader = "X-Test-Owner"

func fakeResolver(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.Header.Get(ownerHeader), 10, 64)
	return id, err == nil
}

// world is two owners: alice (1) with two lists and three wishes, mallory (2)
// with one list and one wish.
type world struct {
	h                      http.Handler
	fake                   *ownertest.Fake
	aliceList, aliceOther  int64
	aliceWish1, aliceWish2 int64
	aliceArchived          int64
	malloryList            int64
	malloryWish            int64
}

const (
	alice   = 1
	mallory = 2
)

func newWorld(t *testing.T) *world {
	t.Helper()
	fake := ownertest.New()
	a, err := New(fake, fakeResolver, nil)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a.Register(mux, func(h http.Handler) http.Handler { return h })

	ctx := context.Background()
	w := &world{h: mux, fake: fake}
	l, _ := fake.CreateList(ctx, alice, store.ListInput{Title: "Birthday", ReserveOpen: false})
	w.aliceList = l.ID
	l, _ = fake.CreateList(ctx, alice, store.ListInput{Title: "Christmas", ReserveOpen: true})
	w.aliceOther = l.ID
	l, _ = fake.CreateList(ctx, mallory, store.ListInput{Title: "Secret", ReserveOpen: true})
	w.malloryList = l.ID
	in := func(title string) store.WishInput {
		return store.WishInput{Title: title, Currency: "EUR", Priority: 1, Quantity: 1}
	}
	x, _ := fake.CreateWish(ctx, alice, w.aliceList, in("Book"))
	w.aliceWish1 = x.ID
	x, _ = fake.CreateWish(ctx, alice, w.aliceList, in("Lamp"))
	w.aliceWish2 = x.ID
	x, _ = fake.CreateWish(ctx, alice, w.aliceList, in("Old"))
	w.aliceArchived = x.ID
	_ = fake.SetWishArchived(ctx, alice, x.ID, true)
	x, _ = fake.CreateWish(ctx, mallory, w.malloryList, in("Private"))
	w.malloryWish = x.ID
	return w
}

type call struct {
	method, path string
	owner        int // 0 sends no owner
	body         string
	contentType  string // default application/json when there is a body
}

func (w *world) do(t *testing.T, c call) *httptest.ResponseRecorder {
	t.Helper()
	var body *strings.Reader
	if c.body != "" {
		body = strings.NewReader(c.body)
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequestWithContext(t.Context(), c.method, Prefix+c.path, body)
	if c.owner != 0 {
		req.Header.Set(ownerHeader, strconv.Itoa(c.owner))
	}
	switch {
	case c.contentType != "":
		req.Header.Set("Content-Type", c.contentType)
	case c.body != "":
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	w.h.ServeHTTP(rec, req)
	return rec
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("body %q is not the expected JSON: %v", rec.Body.String(), err)
	}
	return v
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) (string, map[string]any) {
	t.Helper()
	var e struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("body %q is not an error: %v", rec.Body.String(), err)
	}
	return e.Error.Code, e.Error.Details
}

func TestEndpointsAnswerTheOwnerWhoOwnsTheRows(t *testing.T) {
	w := newWorld(t)
	id := func(n int64) string { return strconv.FormatInt(n, 10) }

	t.Run("overview counts live wishes and hides archived lists", func(t *testing.T) {
		rec := w.do(t, call{method: "GET", path: "/lists", owner: alice})
		if rec.Code != 200 {
			t.Fatalf("status = %d", rec.Code)
		}
		got := decodeBody[dto.ListOverview](t, rec).Lists
		if len(got) != 2 || got[0].WishCount != 2 || got[0].Shared {
			t.Errorf("lists = %+v, want two, the first with 2 live wishes", got)
		}
		if got[0].ReserveOpen {
			t.Error("the stored reserve_open false must survive")
		}
	})

	t.Run("create list defaults reserve_open to true", func(t *testing.T) {
		rec := w.do(t, call{
			method: "POST", path: "/lists", owner: alice,
			body: `{"title":"  Wedding ","event_date":"2030-05-01"}`,
		})
		if rec.Code != 201 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		got := decodeBody[dto.List](t, rec)
		if got.Title != "Wedding" || !got.ReserveOpen || got.EventDate == nil || *got.EventDate != "2030-05-01" {
			t.Errorf("list = %+v", got)
		}
	})

	t.Run("get list hides archived wishes unless asked", func(t *testing.T) {
		rec := w.do(t, call{method: "GET", path: "/lists/" + id(w.aliceList), owner: alice})
		if got := decodeBody[dto.ListDetail](t, rec); len(got.Wishes) != 2 {
			t.Errorf("wishes = %d, want the 2 live ones", len(got.Wishes))
		}
		rec = w.do(t, call{method: "GET", path: "/lists/" + id(w.aliceList) + "?archived=true", owner: alice})
		if got := decodeBody[dto.ListDetail](t, rec); len(got.Wishes) != 3 {
			t.Errorf("wishes = %d, want all 3", len(got.Wishes))
		}
	})

	t.Run("update list keeps reserve_open when absent", func(t *testing.T) {
		rec := w.do(t, call{method: "PUT", path: "/lists/" + id(w.aliceList), owner: alice, body: `{"title":"Birthday 2"}`})
		got := decodeBody[dto.List](t, rec)
		if rec.Code != 200 || got.Title != "Birthday 2" || got.ReserveOpen {
			t.Errorf("status %d list %+v, want the title changed and reserve_open still false", rec.Code, got)
		}
	})

	t.Run("archive and unarchive list", func(t *testing.T) {
		rec := w.do(t, call{method: "PUT", path: "/lists/" + id(w.aliceOther) + "/archived", owner: alice, body: `{"archived":true}`})
		if got := decodeBody[dto.List](t, rec); rec.Code != 200 || !got.Archived {
			t.Fatalf("status %d list %+v", rec.Code, got)
		}
		rec = w.do(t, call{method: "GET", path: "/lists", owner: alice})
		if got := decodeBody[dto.ListOverview](t, rec).Lists; len(got) != 2 {
			t.Errorf("default overview has %d lists, want 2 with the archived one hidden", len(got))
		}
		rec = w.do(t, call{method: "GET", path: "/lists?archived=true", owner: alice})
		if got := decodeBody[dto.ListOverview](t, rec).Lists; len(got) != 3 {
			t.Errorf("archive filter has %d lists, want all 3", len(got))
		}
		rec = w.do(t, call{method: "PUT", path: "/lists/" + id(w.aliceOther) + "/archived", owner: alice, body: `{"archived":false}`})
		if got := decodeBody[dto.List](t, rec); got.Archived {
			t.Error("unarchive did not clear the flag")
		}
	})

	t.Run("create, read, update, archive and delete a wish", func(t *testing.T) {
		rec := w.do(t, call{
			method: "POST", path: "/lists/" + id(w.aliceOther) + "/wishes", owner: alice,
			body: `{"title":"Bike","url":"https://shop.example.invalid/bike","price_cents":19900,"priority":2,"quantity":2,"currency":"usd","group_gift":true}`,
		})
		if rec.Code != 201 {
			t.Fatalf("create status = %d, body %s", rec.Code, rec.Body)
		}
		wish := decodeBody[dto.Wish](t, rec)
		if wish.Currency != "USD" || wish.Priority != 2 || wish.Quantity != 2 || !wish.GroupGift ||
			wish.PriceCents == nil || *wish.PriceCents != 19900 || wish.ListID != w.aliceOther {
			t.Errorf("wish = %+v", wish)
		}
		path := "/wishes/" + id(wish.ID)

		rec = w.do(t, call{method: "GET", path: path, owner: alice})
		if got := decodeBody[dto.Wish](t, rec); rec.Code != 200 || got.Title != "Bike" {
			t.Errorf("get = %d %+v", rec.Code, got)
		}
		rec = w.do(t, call{method: "PUT", path: path, owner: alice, body: `{"title":"E-Bike"}`})
		got := decodeBody[dto.Wish](t, rec)
		if rec.Code != 200 || got.Title != "E-Bike" || got.Priority != 1 || got.PriceCents != nil {
			t.Errorf("update = %d %+v, want a full replacement with defaults", rec.Code, got)
		}
		rec = w.do(t, call{method: "PUT", path: path + "/archived", owner: alice, body: `{"archived":true}`})
		if got := decodeBody[dto.Wish](t, rec); !got.Archived {
			t.Error("archive did not set the flag")
		}
		rec = w.do(t, call{method: "DELETE", path: path, owner: alice})
		if rec.Code != 204 || rec.Body.Len() != 0 {
			t.Errorf("delete = %d %q", rec.Code, rec.Body)
		}
		if rec = w.do(t, call{method: "GET", path: path, owner: alice}); rec.Code != 404 {
			t.Errorf("get after delete = %d, want 404", rec.Code)
		}
	})

	t.Run("move a wish up and down", func(t *testing.T) {
		order := func() []int64 {
			rec := w.do(t, call{method: "GET", path: "/lists/" + id(w.aliceList), owner: alice})
			var ids []int64
			for _, x := range decodeBody[dto.ListDetail](t, rec).Wishes {
				ids = append(ids, x.ID)
			}
			return ids
		}
		if got := order(); !reflect.DeepEqual(got, []int64{w.aliceWish1, w.aliceWish2}) {
			t.Fatalf("order = %v", got)
		}
		rec := w.do(t, call{method: "POST", path: "/wishes/" + id(w.aliceWish2) + "/move", owner: alice, body: `{"direction":"up"}`})
		if rec.Code != 204 {
			t.Fatalf("move = %d %s", rec.Code, rec.Body)
		}
		if got := order(); !reflect.DeepEqual(got, []int64{w.aliceWish2, w.aliceWish1}) {
			t.Errorf("after up = %v", got)
		}
		w.do(t, call{method: "POST", path: "/wishes/" + id(w.aliceWish2) + "/move", owner: alice, body: `{"direction":"down"}`})
		if got := order(); !reflect.DeepEqual(got, []int64{w.aliceWish1, w.aliceWish2}) {
			t.Errorf("after down = %v", got)
		}
	})

	t.Run("move a wish to another own list", func(t *testing.T) {
		rec := w.do(t, call{
			method: "PUT", path: "/wishes/" + id(w.aliceWish1) + "/list", owner: alice,
			body: fmt.Sprintf(`{"list_id":%d}`, w.aliceOther),
		})
		if got := decodeBody[dto.Wish](t, rec); rec.Code != 200 || got.ListID != w.aliceOther {
			t.Errorf("relocate = %d %+v", rec.Code, got)
		}
	})

	t.Run("delete a list takes its wishes with it", func(t *testing.T) {
		rec := w.do(t, call{method: "DELETE", path: "/lists/" + id(w.aliceList), owner: alice})
		if rec.Code != 204 {
			t.Fatalf("delete = %d", rec.Code)
		}
		if rec = w.do(t, call{method: "GET", path: "/wishes/" + id(w.aliceWish2), owner: alice}); rec.Code != 404 {
			t.Errorf("wish of a deleted list = %d, want 404", rec.Code)
		}
	})
}

// routeCalls lists every route as a call by alice on her own rows, with the
// foreign or missing id swapped in by the tests below.
func routeCalls(w *world, listID, wishID int64) []call {
	l := strconv.FormatInt(listID, 10)
	x := strconv.FormatInt(wishID, 10)
	return []call{
		{method: "GET", path: "/lists/" + l},
		{method: "PUT", path: "/lists/" + l, body: `{"title":"x"}`},
		{method: "PUT", path: "/lists/" + l + "/archived", body: `{"archived":true}`},
		{method: "DELETE", path: "/lists/" + l},
		{method: "POST", path: "/lists/" + l + "/wishes", body: `{"title":"x"}`},
		{method: "GET", path: "/wishes/" + x},
		{method: "PUT", path: "/wishes/" + x, body: `{"title":"x"}`},
		{method: "PUT", path: "/wishes/" + x + "/archived", body: `{"archived":true}`},
		{method: "DELETE", path: "/wishes/" + x},
		{method: "POST", path: "/wishes/" + x + "/move", body: `{"direction":"up"}`},
		{method: "PUT", path: "/wishes/" + x + "/list", body: `{"list_id":` + strconv.FormatInt(w.aliceOther, 10) + `}`},
	}
}

// Nobody signed in gets 401 on every route, before anything else is looked at.
func TestEveryRouteRefusesWithoutAnOwner(t *testing.T) {
	w := newWorld(t)
	calls := append(routeCalls(w, w.aliceList, w.aliceWish1),
		call{method: "GET", path: "/lists"}, call{method: "POST", path: "/lists", body: `{"title":"x"}`})
	if len(calls) != len(w.routes(t)) {
		t.Fatalf("%d calls for %d routes: a route has no test", len(calls), len(w.routes(t)))
	}
	for _, c := range calls {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			rec := w.do(t, c)
			if code, _ := errorCode(t, rec); rec.Code != 401 || code != "unauthenticated" {
				t.Errorf("= %d %s, want 401 unauthenticated", rec.Code, code)
			}
		})
	}
	// A stale or forged header is no owner either.
	req := httptest.NewRequestWithContext(t.Context(), "GET", Prefix+"/lists", nil)
	req.Header.Set(ownerHeader, "abc")
	rec := httptest.NewRecorder()
	w.h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("unparseable owner = %d, want 401", rec.Code)
	}
}

func (w *world) routes(t *testing.T) []Route {
	t.Helper()
	a, err := New(w.fake, fakeResolver, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a.Routes()
}

// A row of somebody else and a row that does not exist are the same answer:
// status, headers and body, for every route that takes an id.
func TestForeignAndMissingRowsAreRefusedIdentically(t *testing.T) {
	w := newWorld(t)
	const missing = 9999
	foreign := routeCalls(w, w.malloryList, w.malloryWish)
	absent := routeCalls(w, missing, missing)

	for i := range foreign {
		c := foreign[i]
		t.Run(c.method+" "+strings.SplitN(c.path, "/", 3)[1], func(t *testing.T) {
			c.owner, absent[i].owner = alice, alice
			f := w.do(t, c)
			m := w.do(t, absent[i])
			if f.Code != http.StatusNotFound {
				t.Fatalf("foreign = %d %s, want 404", f.Code, f.Body)
			}
			if f.Code != m.Code || f.Body.String() != m.Body.String() || !reflect.DeepEqual(f.Header(), m.Header()) {
				t.Errorf("foreign %d %v %q differs from missing %d %v %q",
					f.Code, f.Header(), f.Body, m.Code, m.Header(), m.Body)
			}
		})
	}

	// A malformed id is the same refusal too.
	bad := w.do(t, call{method: "GET", path: "/lists/abc", owner: alice})
	ref := w.do(t, absent[0])
	if bad.Code != 404 || bad.Body.String() != ref.Body.String() {
		t.Errorf("malformed id = %d %q, want the refusal", bad.Code, bad.Body)
	}

	// A bad body does not tell either: the check of the body comes first and
	// does not look at the row.
	for _, id := range []int64{w.malloryList, missing} {
		rec := w.do(t, call{method: "PUT", path: "/lists/" + strconv.FormatInt(id, 10), owner: alice, body: `{"title":""}`})
		if rec.Code != 400 {
			t.Errorf("empty title on list %d = %d, want 400 whether or not it exists", id, rec.Code)
		}
	}

	// Nothing of mallory's changed.
	list, _ := w.fake.List(t.Context(), mallory, w.malloryList)
	wish, _ := w.fake.Wish(t.Context(), mallory, w.malloryWish)
	if list.Title != "Secret" || list.Archived || wish.Title != "Private" || wish.Archived || wish.ListID != w.malloryList {
		t.Errorf("a refused call changed foreign data: %+v %+v", list, wish)
	}
}

func TestAWishCannotBeMovedToOrFromAForeignList(t *testing.T) {
	w := newWorld(t)
	to := w.do(t, call{
		method: "PUT", path: "/wishes/" + strconv.FormatInt(w.aliceWish1, 10) + "/list", owner: alice,
		body: fmt.Sprintf(`{"list_id":%d}`, w.malloryList),
	})
	if to.Code != 404 {
		t.Errorf("move into a foreign list = %d, want 404", to.Code)
	}
	from := w.do(t, call{
		method: "PUT", path: "/wishes/" + strconv.FormatInt(w.malloryWish, 10) + "/list", owner: alice,
		body: fmt.Sprintf(`{"list_id":%d}`, w.aliceList),
	})
	if from.Code != 404 {
		t.Errorf("move out of a foreign list = %d, want 404", from.Code)
	}
	gone := w.do(t, call{
		method: "PUT", path: "/wishes/" + strconv.FormatInt(w.aliceWish1, 10) + "/list", owner: alice,
		body: `{"list_id":9999}`,
	})
	if gone.Code != 404 || gone.Body.String() != to.Body.String() {
		t.Errorf("move into a missing list = %d %q, want the same refusal", gone.Code, gone.Body)
	}
	if x, _ := w.fake.Wish(t.Context(), alice, w.aliceWish1); x.ListID != w.aliceList {
		t.Error("the wish left its list")
	}
}

// The owner is whoever the resolver says, never what the request claims.
func TestTheOwnerNeverComesFromTheRequestData(t *testing.T) {
	w := newWorld(t)

	for _, body := range []string{
		`{"title":"x","owner_id":2}`,
		`{"title":"x","ownerId":2}`,
	} {
		rec := w.do(t, call{method: "POST", path: "/lists", owner: alice, body: body})
		if rec.Code != 400 {
			t.Errorf("body %s = %d, want unknown fields refused", body, rec.Code)
		}
	}

	rec := w.do(t, call{method: "GET", path: "/lists?owner_id=2&owner=2", owner: alice})
	for _, l := range decodeBody[dto.ListOverview](t, rec).Lists {
		if l.ID == w.malloryList {
			t.Error("a query parameter switched the owner")
		}
	}

	rec = w.do(t, call{method: "POST", path: "/lists", owner: alice, body: `{"title":"mine"}`})
	created := decodeBody[dto.List](t, rec)
	if _, err := w.fake.List(t.Context(), mallory, created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Error("a list alice created belongs to somebody else")
	}
}

func TestRequestsAreRefusedBeforeTheyReachTheStore(t *testing.T) {
	w := newWorld(t)
	list := "/lists/" + strconv.FormatInt(w.aliceList, 10)
	cases := []struct {
		name string
		call call
		want int
		code string
	}{
		{"wrong content type", call{method: "POST", path: "/lists", body: `{"title":"x"}`, contentType: "text/plain"}, 415, "unsupported_media_type"},
		{"form content type", call{method: "POST", path: "/lists", body: `title=x`, contentType: "application/x-www-form-urlencoded"}, 415, "unsupported_media_type"},
		{"no content type", call{method: "PUT", path: list, body: `{"title":"x"}`, contentType: "none"}, 415, "unsupported_media_type"},
		{"json with charset", call{method: "POST", path: "/lists", body: `{"title":"x"}`, contentType: "application/json; charset=utf-8"}, 201, ""},
		{"bad json", call{method: "POST", path: "/lists", body: `{"title":`}, 400, "invalid_argument"},
		{"empty body", call{method: "POST", path: "/lists", body: ` `}, 400, "invalid_argument"},
		{"unknown field", call{method: "POST", path: "/lists", body: `{"title":"x","nope":1}`}, 400, "invalid_argument"},
		{"second value", call{method: "POST", path: "/lists", body: `{"title":"x"}{"title":"y"}`}, 400, "invalid_argument"},
		{"wrong type", call{method: "POST", path: "/lists", body: `{"title":5}`}, 400, "invalid_argument"},
		{"body too large", call{method: "POST", path: "/lists", body: `{"title":"` + strings.Repeat("a", maxBody) + `"}`}, 413, "request_too_large"},
		{"title missing", call{method: "POST", path: "/lists", body: `{}`}, 400, "invalid_argument"},
		{"title blank", call{method: "POST", path: "/lists", body: `{"title":"   "}`}, 400, "invalid_argument"},
		{"title too long", call{method: "POST", path: "/lists", body: `{"title":"` + strings.Repeat("ä", maxListTitle+1) + `"}`}, 400, "invalid_argument"},
		{"title at the limit counts runes", call{method: "POST", path: "/lists", body: `{"title":"` + strings.Repeat("ä", maxListTitle) + `"}`}, 201, ""},
		{"bad date", call{method: "POST", path: "/lists", body: `{"title":"x","event_date":"01.05.2030"}`}, 400, "invalid_argument"},
		{"move direction", call{method: "POST", path: "/wishes/1/move", body: `{"direction":"sideways"}`}, 400, "invalid_argument"},
		{"relocate without list", call{method: "PUT", path: "/wishes/1/list", body: `{}`}, 400, "invalid_argument"},
		{"method not allowed", call{method: "PATCH", path: "/lists"}, 405, ""},
		{"unknown route", call{method: "GET", path: "/nothing"}, 404, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.call
			c.owner = alice
			if c.contentType == "none" {
				c.contentType = ""
				req := httptest.NewRequestWithContext(t.Context(), c.method, Prefix+c.path, strings.NewReader(c.body))
				req.Header.Set(ownerHeader, "1")
				rec := httptest.NewRecorder()
				w.h.ServeHTTP(rec, req)
				if code, _ := errorCode(t, rec); rec.Code != tc.want || code != tc.code {
					t.Errorf("= %d %s, want %d %s", rec.Code, code, tc.want, tc.code)
				}
				return
			}
			rec := w.do(t, c)
			if rec.Code != tc.want {
				t.Fatalf("= %d %s, want %d", rec.Code, rec.Body, tc.want)
			}
			if tc.code != "" {
				if code, _ := errorCode(t, rec); code != tc.code {
					t.Errorf("code = %s, want %s", code, tc.code)
				}
			}
		})
	}
}

func TestWishFieldsAreValidated(t *testing.T) {
	w := newWorld(t)
	path := "/lists/" + strconv.FormatInt(w.aliceList, 10) + "/wishes"
	long := strings.Repeat("a", 5000)
	cases := []struct {
		name, body, field, code string
	}{
		{"no title", `{}`, "title", "required"},
		{"title too long", `{"title":"` + strings.Repeat("a", maxWishTitle+1) + `"}`, "title", "too_long"},
		{"description too long", `{"title":"x","description":"` + long + `"}`, "description", "too_long"},
		{"shop too long", `{"title":"x","shop":"` + long + `"}`, "shop", "too_long"},
		{"url too long", `{"title":"x","url":"https://a.example.invalid/` + long + `"}`, "url", "too_long"},
		{"javascript url", `{"title":"x","url":"javascript:alert(1)"}`, "url", "invalid"},
		{"ftp url", `{"title":"x","url":"ftp://a.example.invalid/f"}`, "url", "invalid"},
		{"relative url", `{"title":"x","url":"/shop"}`, "url", "invalid"},
		{"credentials in url", `{"title":"x","url":"https://user:pw@a.example.invalid/"}`, "url", "invalid"},
		{"negative price", `{"title":"x","price_cents":-1}`, "price_cents", "invalid"},
		{"huge price", `{"title":"x","price_cents":99999999999999}`, "price_cents", "invalid"},
		{"currency", `{"title":"x","currency":"EU1"}`, "currency", "invalid"},
		{"priority 3", `{"title":"x","priority":3}`, "priority", "invalid"},
		{"priority -1", `{"title":"x","priority":-1}`, "priority", "invalid"},
		{"quantity 0", `{"title":"x","quantity":0}`, "quantity", "invalid"},
		{"quantity 100", `{"title":"x","quantity":100}`, "quantity", "invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := w.do(t, call{method: "POST", path: path, owner: alice, body: tc.body})
			if rec.Code != 400 {
				t.Fatalf("= %d %s, want 400", rec.Code, rec.Body)
			}
			if _, details := errorCode(t, rec); details[tc.field] != tc.code {
				t.Errorf("details = %v, want %s: %s", details, tc.field, tc.code)
			}
		})
	}

	t.Run("the edges are accepted", func(t *testing.T) {
		for _, body := range []string{
			`{"title":"x","priority":0,"quantity":99,"price_cents":0,"url":"http://a.example.invalid"}`,
			`{"title":"x","price_cents":1000000000}`,
		} {
			if rec := w.do(t, call{method: "POST", path: path, owner: alice, body: body}); rec.Code != 201 {
				t.Errorf("%s = %d %s", body, rec.Code, rec.Body)
			}
		}
	})
}

func TestAStoreFailureIsAnInternalErrorWithoutDetail(t *testing.T) {
	w := newWorld(t)
	w.fake.Err = errors.New("pq: connection to 10.1.2.3 refused, relation wishes")
	for _, c := range routeCalls(w, w.aliceList, w.aliceWish1) {
		c.owner = alice
		rec := w.do(t, c)
		if rec.Code != 500 && rec.Code != 400 {
			t.Errorf("%s %s = %d, want 500", c.method, c.path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "10.1.2.3") || strings.Contains(rec.Body.String(), "relation") {
			t.Errorf("%s %s leaks the cause: %s", c.method, c.path, rec.Body)
		}
	}
}

// Error details are held to field names and codes: anything else fails closed
// as a 500, so a fact added to a details map never reaches the client.
func TestErrorDetailsCarryOnlyFieldCodes(t *testing.T) {
	cases := []struct {
		name    string
		details map[string]string
		wantErr bool
	}{
		{"a field and a code", map[string]string{"title": "required"}, false},
		{"an unknown key", map[string]string{"direction": "invalid", "reserved_by": "invalid"}, true},
		{"an unknown code", map[string]string{"title": "aunt"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := fieldProblem("x", tc.details)
			var apiErr *api.Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v, want an api error", err)
			}
			if got := apiErr.Status == http.StatusInternalServerError; got != tc.wantErr {
				t.Errorf("status = %d, want internal error: %v", apiErr.Status, tc.wantErr)
			}
		})
	}

	w := newWorld(t)
	rec := w.do(t, call{method: "POST", path: "/wishes/1/move", owner: alice, body: `{"direction":"sideways"}`})
	if _, details := errorCode(t, rec); rec.Code != 400 || len(details) != 1 || details["direction"] != "invalid" {
		t.Errorf("move = %d %v, want 400 with exactly the direction", rec.Code, details)
	}
}

func TestResponsesAreNotCached(t *testing.T) {
	w := newWorld(t)
	rec := w.do(t, call{method: "GET", path: "/lists", owner: alice})
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestNewRequiresItsDependencies(t *testing.T) {
	if _, err := New(nil, fakeResolver, nil); err == nil {
		t.Error("New without a store succeeded")
	}
	if _, err := New(ownertest.New(), nil, nil); err == nil {
		t.Error("New without a resolver succeeded")
	}
}
