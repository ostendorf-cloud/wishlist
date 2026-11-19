// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package owner

// The response layer half of the blindness guarantee
// (docs/adr/0001-blind-reservations.md).
//
// The mechanism is reflection over the response types the route table names.
// Every handler returns a typed value that one adapter writes, so the table is
// the list of what can go out. The walk follows struct fields (embedded ones
// too), pointers, slices, arrays and maps, and fails on:
//
//   - any named type of internal/store, which also covers Reservation,
//     GuestWish and GuestList and anything later added to guest.go,
//   - a type or field named like a reservation, in Go or in its JSON tag,
//   - an interface, function or channel, where the walk cannot see what goes
//     out (an any field is itself a violation),
//   - a type with its own JSON or text encoding, which hides its fields.

import (
	"encoding"
	"encoding/json"
	"go/ast"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/wishlist/internal/store"
	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner/dto"
	"github.com/ostendorf-cloud/wishlist/internal/webapp/owner/ownertest"
)

// storePath is the import path of the store, whose types never go out.
const storePath = "github.com/ostendorf-cloud/wishlist/internal/store"

// blindName matches a name that speaks of what guests did.
var blindName = regexp.MustCompile(`(?i)reserv|guest|taken|bought|pledge`)

// allowedNames are the fields that match blindName and are harmless: the
// owner's own setting of whether guests may still take wishes. A value, not
// data about what was taken. Keep this list short and reviewed.
var allowedNames = map[string]bool{
	"dto.List.ReserveOpen": true,
}

var (
	jsonMarshaler = reflect.TypeFor[json.Marshaler]()
	textMarshaler = reflect.TypeFor[encoding.TextMarshaler]()
	timeType      = reflect.TypeFor[time.Time]()
)

// violations returns what makes root unsafe for an owner response, one line
// each, or nothing.
func violations(root reflect.Type) []string {
	w := &walker{seen: map[reflect.Type]bool{}}
	w.walk(root, root.String())
	return w.found
}

type walker struct {
	seen  map[reflect.Type]bool
	found []string
}

func (w *walker) add(path, why string) { w.found = append(w.found, path+": "+why) }

func (w *walker) walk(t reflect.Type, path string) {
	if t == timeType || w.seen[t] {
		return
	}
	w.seen[t] = true

	if t.Name() != "" {
		if strings.HasPrefix(t.PkgPath(), storePath) {
			w.add(path, "type of internal/store: "+t.String())
			return
		}
		if blindName.MatchString(t.Name()) {
			w.add(path, "type named like a reservation: "+t.String())
		}
	}
	if t.Implements(jsonMarshaler) || reflect.PointerTo(t).Implements(jsonMarshaler) ||
		t.Implements(textMarshaler) || reflect.PointerTo(t).Implements(textMarshaler) {
		w.add(path, "own encoding hides the fields: "+t.String())
		return
	}

	switch t.Kind() {
	case reflect.Struct:
		for f := range t.Fields() {
			if !f.IsExported() && !f.Anonymous {
				continue
			}
			jsonName, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if jsonName == "-" {
				continue
			}
			key := shortPkg(t) + "." + t.Name() + "." + f.Name
			if !allowedNames[key] && (blindName.MatchString(f.Name) || blindName.MatchString(jsonName)) {
				w.add(path+"."+f.Name, "field named like a reservation")
			}
			w.walk(f.Type, path+"."+f.Name)
		}
	case reflect.Pointer, reflect.Slice, reflect.Array:
		w.walk(t.Elem(), path+"[]")
	case reflect.Map:
		w.walk(t.Key(), path+"[key]")
		w.walk(t.Elem(), path+"[value]")
	case reflect.Interface:
		w.add(path, "interface: the walk cannot see what goes out")
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		w.add(path, "kind "+t.Kind().String()+" cannot be checked")
	default:
		// Basic kinds carry no structure.
	}
}

func shortPkg(t reflect.Type) string {
	p := t.PkgPath()
	return p[strings.LastIndex(p, "/")+1:]
}

func TestNoOwnerResponseCanReachAReservation(t *testing.T) {
	a, err := New(ownertest.New(), func(*http.Request) (int64, bool) { return 1, true }, nil)
	if err != nil {
		t.Fatal(err)
	}
	routes := a.Routes()
	if len(routes) < 13 {
		t.Fatalf("route table has %d routes, want at least 13", len(routes))
	}
	typed := 0
	for _, r := range routes {
		if r.Response == nil {
			continue
		}
		typed++
		if found := violations(r.Response); len(found) > 0 {
			t.Errorf("%s: %s", r.Pattern(), strings.Join(found, "; "))
		}
	}
	if typed == 0 {
		t.Fatal("no route names a response type: the test would pass over nothing")
	}
}

// The mutation proof of the walk, one case per way a reservation can get out.
type (
	withStore   struct{ R store.Reservation }
	withGuest   struct{ W *store.GuestWish }
	withList    struct{ L []store.GuestList }
	withStoreOK struct{ W store.Wish }
	namedField  struct{ Reserved bool }
	taggedField struct {
		Open bool `json:"reservations"`
	}
	anyField     struct{ Extra any }
	mapAny       struct{ M map[string]any }
	mapValue     struct{ M map[string][]withStore }
	funcField    struct{ F func() }
	hiddenJSON   struct{ H custom }
	embeddedBad  struct{ namedField }
	deepPointer  struct{ P **[2]withGuest }
	Reservation  struct{ N int }
	typeNamed    struct{ R Reservation }
	ignoredField struct {
		Hidden store.Reservation `json:"-"`
	}
	fine struct {
		A int
		T time.Time
		P *int64
		S []string
	}
)

type custom struct{ N int }

func (custom) MarshalJSON() ([]byte, error) { return []byte("1"), nil }

func TestTheWalkCatchesEveryWayAReservationCanGetOut(t *testing.T) {
	cases := []struct {
		name string
		typ  reflect.Type
		want bool
	}{
		{"store reservation", reflect.TypeFor[withStore](), true},
		{"pointer to guest wish", reflect.TypeFor[withGuest](), true},
		{"slice of guest list", reflect.TypeFor[withList](), true},
		{"a store wish passed through", reflect.TypeFor[withStoreOK](), true},
		{"field named reserved", reflect.TypeFor[namedField](), true},
		{"json name reservations", reflect.TypeFor[taggedField](), true},
		{"any field", reflect.TypeFor[anyField](), true},
		{"map of any", reflect.TypeFor[mapAny](), true},
		{"map value", reflect.TypeFor[mapValue](), true},
		{"func field", reflect.TypeFor[funcField](), true},
		{"own marshaller", reflect.TypeFor[hiddenJSON](), true},
		{"embedded", reflect.TypeFor[embeddedBad](), true},
		{"deep pointer and array", reflect.TypeFor[deepPointer](), true},
		{"type named Reservation", reflect.TypeFor[typeNamed](), true},
		{"json dash hides the field from the wire", reflect.TypeFor[ignoredField](), false},
		{"plain fields and time", reflect.TypeFor[fine](), false},
		{"the real list", reflect.TypeFor[dto.ListDetail](), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := violations(tc.typ)
			if (len(got) > 0) != tc.want {
				t.Errorf("violations(%s) = %v, want violation: %v", tc.typ, got, tc.want)
			}
		})
	}
}

// The adapter is the only place a body is written, and the route table names
// its type. This keeps a handler from writing around it.
func TestOnlyTheAdapterWritesAResponse(t *testing.T) {
	for name, file := range parseNonTest(t, ".") {
		if name == "owner.go" {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				if x.Name == "ResponseWriter" {
					t.Errorf("%s: handlers must return a value, not take a ResponseWriter", name)
				}
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok {
					call := id.Name + "." + x.Sel.Name
					if slices.Contains([]string{"api.Write", "api.NoContent", "json.NewEncoder", "http.Error", "json.Marshal"}, call) {
						t.Errorf("%s: %s writes around the typed adapter", name, call)
					}
				}
			}
			return true
		})
	}
}
