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
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"path/filepath"
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

// blindName matches a name that speaks of what guests did. The name check is a
// heuristic: a clever name gets past it. The typed walk (no store type, no
// interface, no opaque encoding, no free form bytes or string map) is the
// real guard, and review of every new DTO field is the last one.
var blindName = regexp.MustCompile(
	`(?i)reserv|guest|taken|bought|pledge|claim|purchas|book|contrib|donor|amount_?cents`)

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
			// Free form content cannot be walked: a byte slice or a string
			// map can carry anything. Allow one only by name, reviewed.
			if !allowedNames[key] && freeForm(f.Type) {
				w.add(path+"."+f.Name, "free form field ([]byte or map[string]string)")
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

func freeForm(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Slice:
		return t.Elem().Kind() == reflect.Uint8
	case reflect.Map:
		return t.Key().Kind() == reflect.String && t.Elem().Kind() == reflect.String
	default:
		return false
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
	claimed     struct{ Claimed bool }
	purchased   struct{ PurchasedBy string }
	booked      struct{ Booking int }
	contributor struct{ Contributors []string }
	donated     struct{ AmountCents int64 }
	rawBytes    struct{ Blob []byte }
	stringMap   struct{ Labels map[string]string }
	fine        struct {
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
		{"field claimed", reflect.TypeFor[claimed](), true},
		{"field purchased", reflect.TypeFor[purchased](), true},
		{"field booking", reflect.TypeFor[booked](), true},
		{"field contributors", reflect.TypeFor[contributor](), true},
		{"field amount cents", reflect.TypeFor[donated](), true},
		{"raw bytes", reflect.TypeFor[rawBytes](), true},
		{"string map", reflect.TypeFor[stringMap](), true},
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

// writers are the selectors that put bytes on a response.
var writers = []string{
	"Write", "WriteHeader", "WriteString", "NoContent", "NewEncoder", "Marshal",
	"MarshalIndent", "Fprint", "Fprintf", "Fprintln", "WithDetails",
}

// The adapter in owner/owner.go is the only place a body is written; the
// route table names its type, and the reflection walk reads that type. This
// keeps anything under internal/webapp from writing around it. Allowed apart
// from the adapter: WithDetails in fieldProblem (validate.go), which holds the
// details of an error to field names and codes, and in webapp.go only the
// refusal of the CSRF protector (api.WriteError) and the SPA, which writes no
// bytes itself.
func TestOnlyTheAdapterWritesAResponse(t *testing.T) {
	files := webappFiles(t)
	for path, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if x.Sel.Name == "WithDetails" && path == "validate.go" {
					return true
				}
				if slices.Contains(writers, x.Sel.Name) && path != "owner.go" {
					t.Errorf("%s: %s writes around the typed adapter", path, x.Sel.Name)
				}
				if id, ok := x.X.(*ast.Ident); ok && id.Name == "http" && x.Sel.Name == "Error" {
					t.Errorf("%s: http.Error writes around the typed adapter", path)
				}
			case *ast.Ident:
				// A handler returns a value. Only the adapter, the CSRF refusal
				// and the SPA in webapp.go touch a ResponseWriter.
				if x.Name == "ResponseWriter" && path != "owner.go" && path != filepath.Join("..", "webapp.go") {
					t.Errorf("%s: handlers must return a value, not take a ResponseWriter", path)
				}
			}
			return true
		})
	}
}

// Every route under the owner prefix comes from Routes(): the only calls that
// register a handler are Register's, from the table, and the two catch alls in
// webapp.go. An endpoint added by hand next to them would skip both the
// resolver and the response walk.
func TestEveryRegisteredPatternComesFromTheRouteTable(t *testing.T) {
	registered := 0
	for path, file := range webappFiles(t) {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
				return true
			}
			// api.Handle is govue's error adapter, not a registration.
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "api" {
				return true
			}
			registered++
			if len(call.Args) == 0 {
				t.Errorf("%s: %s without a pattern", path, sel.Sel.Name)
				return true
			}
			switch arg := call.Args[0].(type) {
			case *ast.BasicLit:
				if path == filepath.Join("..", "webapp.go") && (arg.Value == `"/api/"` || arg.Value == `"/"`) {
					return true
				}
			case *ast.CallExpr:
				if inner, ok := arg.Fun.(*ast.SelectorExpr); ok && inner.Sel.Name == "Pattern" && path == "owner.go" {
					return true
				}
			}
			t.Errorf("%s: a handler is registered by hand, not from Routes()", path)
			return true
		})
	}
	if registered != 3 {
		t.Errorf("found %d registrations, want the one in Register and the two catch alls", registered)
	}
}

// webappFiles parses every non test file under internal/webapp, keyed by its
// path relative to this package: owner.go, ../webapp.go, dto/dto.go.
func webappFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	out := map[string]*ast.File{}
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		out[strings.TrimPrefix(path, "../owner/")] = file
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
