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

// accountNames are the only identifiers of the store that internal/auth and
// internal/accounts may use: the credentials side, which touches no list, wish
// or guest table.
var accountNames = map[string]bool{
	"Account":       true,
	"AccountStore":  true,
	"ErrNotFound":   true,
	"ErrLoginTaken": true,
}

// storeViolations returns what one parsed file does against the rules: an
// aliased store import, the dto package importing the store, a forbidden
// selector and a store name outside allowed.
func storeViolations(where string, file *ast.File, allowed map[string]bool, isDTO bool) []string {
	var out []string
	imports := false
	for _, imp := range file.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if p != storePath {
			continue
		}
		imports = true
		if imp.Name != nil {
			// An alias would hide the package from the check below.
			out = append(out, where+": import the store without an alias")
		}
	}
	if isDTO && imports {
		out = append(out, where+": the dto package must not import the store")
	}
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if forbiddenSelectors[sel.Sel.Name] {
			out = append(out, where+": "+sel.Sel.Name+" reaches beyond the owner's side")
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == "store" && imports && !allowed[sel.Sel.Name] {
			out = append(out, where+": store."+sel.Sel.Name+" is not on the allow list")
		}
		return true
	})
	return out
}

// scanStoreNames applies storeViolations to every non test file under root.
func scanStoreNames(t *testing.T, root string, allowed map[string]bool) int {
	t.Helper()
	scanned := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		for name, file := range parseNonTest(t, path) {
			scanned++
			where := filepath.Join(path, name)
			for _, v := range storeViolations(where, file, allowed, strings.HasSuffix(path, "dto")) {
				t.Error(v)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return scanned
}

func TestNothingUnderWebappReachesTheGuestSide(t *testing.T) {
	if n := scanStoreNames(t, "..", storeNames); n < 5 {
		t.Fatalf("scanned %d files, the walk is pointing at the wrong place", n)
	}
}

// The sign in packages hold the credentials only: the account side of the
// store and nothing else.
func TestTheAccountPackagesReachOnlyTheAccountSide(t *testing.T) {
	for _, root := range []string{filepath.Join("..", "..", "auth"), filepath.Join("..", "..", "accounts")} {
		if n := scanStoreNames(t, root, accountNames); n < 1 {
			t.Fatalf("scanned %d files under %s, the walk is pointing at the wrong place", n, root)
		}
	}
}

func parseSource(t *testing.T, src string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

// Each rule is proven by a fixture that breaks it, so a weakened check fails
// here and not only in review.
func TestTheStoreNameRulesCatchEachBreach(t *testing.T) {
	const head = "package x\nimport \"github.com/ostendorf-cloud/wishlist/internal/store\"\n"
	tests := []struct {
		name    string
		src     string
		allowed map[string]bool
		isDTO   bool
	}{
		{"alias", "package x\nimport s \"github.com/ostendorf-cloud/wishlist/internal/store\"\nvar _ s.List\n", storeNames, false},
		{"dto imports the store", head + "var _ store.List\n", storeNames, true},
		{"guest store", head + "var _ store.GuestStore\n", storeNames, false},
		{"guests accessor", head + "func f(c interface{ Guests() int }) { c.Guests() }\n", storeNames, false},
		{"owners accessor", head + "func f(c interface{ Owners() int }) { c.Owners() }\n", accountNames, false},
		{"core", head + "var _ store.Core\n", accountNames, false},
		{"owner store in the account packages", head + "var _ store.OwnerStore\n", accountNames, false},
		{"guest store in the account packages", head + "var _ store.GuestStore\n", accountNames, false},
		{"wish in the account packages", head + "var _ store.Wish\n", accountNames, false},
		{"account in the owner api", head + "var _ store.Account\n", storeNames, false},
		{"account store in the owner api", head + "var _ store.AccountStore\n", storeNames, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := storeViolations("fixture.go", parseSource(t, tt.src), tt.allowed, tt.isDTO); len(got) == 0 {
				t.Errorf("the rule did not notice: %s", tt.src)
			}
		})
	}
	for _, ok := range []struct {
		src     string
		allowed map[string]bool
	}{
		{head + "var _ store.AccountStore\nvar _ = store.ErrLoginTaken\nvar _ store.Account\nvar _ = store.ErrNotFound\n", accountNames},
		{head + "var _ store.OwnerStore\nvar _ store.List\n", storeNames},
	} {
		if got := storeViolations("fixture.go", parseSource(t, ok.src), ok.allowed, false); len(got) != 0 {
			t.Errorf("a clean fixture failed: %v", got)
		}
	}
}
