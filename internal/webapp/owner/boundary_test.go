// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package owner

// The handler layer half of ADR 0001: nothing under internal/webapp may reach
// the guest side of the store. Read it for what it is, like the store's own
// boundary test: a guard against accidents, not against a determined author.
// It sees names, not reflection or generated code.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// storeNames are the only identifiers of the store package that code under
// internal/webapp may use: the owner's side and the types it passes in.
var storeNames = map[string]bool{
	"OwnerStore":  true,
	"ErrNotFound": true,
	"List":        true,
	"Wish":        true,
	"ListInput":   true,
	"WishInput":   true,
}

// forbiddenSelectors reach the guest side or the part that hands it out.
var forbiddenSelectors = map[string]bool{"Guests": true, "Owners": true, "Core": true, "GuestStore": true}

func parseNonTest(t *testing.T, dir string) map[string]*ast.File {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*ast.File{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = file
	}
	return out
}

func TestNothingUnderWebappReachesTheGuestSide(t *testing.T) {
	scanned := 0
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		for name, file := range parseNonTest(t, path) {
			scanned++
			where := filepath.Join(path, name)
			storeName := ""
			for _, imp := range file.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				if p != storePath {
					continue
				}
				storeName = "store"
				if imp.Name != nil {
					// An alias would hide the package from the check below.
					t.Errorf("%s: import the store without an alias", where)
				}
			}
			if strings.HasSuffix(path, "dto") && storeName != "" {
				t.Errorf("%s: the dto package must not import the store", where)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if forbiddenSelectors[sel.Sel.Name] {
					t.Errorf("%s: %s reaches beyond the owner's side", where, sel.Sel.Name)
				}
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "store" && storeName != "" && !storeNames[sel.Sel.Name] {
					t.Errorf("%s: store.%s is not on the owner allow list", where, sel.Sel.Name)
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 5 {
		t.Fatalf("scanned %d files, the walk is pointing at the wrong place", scanned)
	}
}
