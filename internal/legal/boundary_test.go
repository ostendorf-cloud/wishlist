// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package legal

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// This package writes responses on the public mux, outside the owner API and
// its response walk (internal/webapp/owner/blindness_test.go). It stays safe
// because it cannot reach the store or anything else of this module: it may
// import the standard library and govue's language and themeboot packages
// only.
var allowedModuleImports = map[string]bool{
	"github.com/ostendorf-cloud/govue/language":  true,
	"github.com/ostendorf-cloud/govue/themeboot": true,
}

func TestThePackageImportsNothingOfThisModule(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			first, _, _ := strings.Cut(path, "/")
			standard := !strings.Contains(first, ".")
			if !standard && !allowedModuleImports[path] {
				t.Errorf("%s imports %q: only the standard library and govue's language and themeboot packages are allowed", name, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no source file found, the test is stale")
	}
}
