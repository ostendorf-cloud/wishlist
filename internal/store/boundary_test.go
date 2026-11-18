// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package store

// The blindness of a reservation is a property of the queries, so it is
// tested on the source and needs no database: it runs everywhere.
//
// Only guest.go may name the reservations table. Every other file in this
// package is read through the Go syntax tree and fails when a string in it
// names the table. Comments are fine. The embedded migrations are fine: a
// schema has to create the table.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	guestFile      = "guest.go"
	guestTable     = "reservations"
	migrationsDir  = "migrations"
	boundaryReason = "only guest.go may name the reservations table"
)

// scan is what a boundary check looked at and found.
type scan struct {
	// files are the files that were read, relative to the root.
	files []string
	// violations are readable descriptions, one per offence.
	violations []string
}

// checkBoundary reads every file under root except the guest file, the test
// files and the migrations directory. It reports every string that names the
// guests' table, however it is spelled:
//
//   - in any case, bare, quoted or schema qualified: the check looks for the
//     table name as a substring of the lowercased string;
//   - in a raw or an interpreted string, with escapes decoded;
//   - split by a concatenation: constant "+" chains are folded;
//   - split across constants: the literals of all scanned files are also
//     joined into one text and searched;
//   - in a .sql file outside the migrations directory.
//
// It cannot see a name that is computed at run time, for example by
// fmt.Sprintf from a variable. That is the limit of any static check.
func checkBoundary(root string) (scan, error) {
	var result scan
	var joined strings.Builder

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if rel == migrationsDir {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == guestFile || strings.HasSuffix(rel, "_test.go") {
			return nil
		}

		switch filepath.Ext(rel) {
		case ".go":
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), rel, source, 0)
			if err != nil {
				return err
			}
			result.files = append(result.files, rel)
			for _, text := range goStrings(parsed) {
				joined.WriteString(text)
				if namesGuestTable(text) {
					result.violations = append(result.violations,
						rel+": a string names the "+guestTable+" table: "+firstLine(text))
				}
			}
		case ".sql":
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			result.files = append(result.files, rel)
			if namesGuestTable(string(source)) {
				result.violations = append(result.violations,
					rel+": a SQL file outside "+migrationsDir+" names the "+guestTable+" table")
			}
		}
		return nil
	})
	if err != nil {
		return scan{}, err
	}
	if namesGuestTable(joined.String()) && len(result.violations) == 0 {
		result.violations = append(result.violations,
			"the string constants of the scanned files add up to the "+guestTable+" table name when joined")
	}
	slices.Sort(result.files)
	return result, nil
}

// goStrings returns every string literal of a file, decoded, and every
// constant concatenation folded into one string.
func goStrings(file *ast.File) []string {
	var out []string
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.BasicLit:
			if n.Kind == token.STRING {
				out = append(out, unquote(n.Value))
			}
		case *ast.BinaryExpr:
			if folded, ok := fold(n); ok {
				out = append(out, folded)
			}
		}
		return true
	})
	return out
}

// fold evaluates a chain of "+" over string literals.
func fold(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			return unquote(e.Value), true
		}
	case *ast.ParenExpr:
		return fold(e.X)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		left, lok := fold(e.X)
		right, rok := fold(e.Y)
		return left + right, lok && rok
	}
	return "", false
}

func unquote(literal string) string {
	if value, err := strconv.Unquote(literal); err == nil {
		return value
	}
	return literal
}

func namesGuestTable(text string) bool {
	return strings.Contains(strings.ToLower(text), guestTable)
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return line
}

// TestOnlyTheGuestFileNamesTheReservationsTable is the boundary itself,
// run against this package's real files.
func TestOnlyTheGuestFileNamesTheReservationsTable(t *testing.T) {
	t.Parallel()

	result, err := checkBoundary(".")
	if err != nil {
		t.Fatalf("check boundary: %v", err)
	}

	// A check that scanned nothing proves nothing: the directory could be
	// wrong or the files renamed.
	for _, want := range []string{"owner.go", "store.go", "model.go", "migrate.go"} {
		if !slices.Contains(result.files, want) {
			t.Errorf("the boundary check did not read %s, it read %v", want, result.files)
		}
	}
	if slices.Contains(result.files, guestFile) {
		t.Errorf("the boundary check read %s, which is the one file allowed to name the table", guestFile)
	}
	for _, violation := range result.violations {
		t.Errorf("%s: %s", boundaryReason, violation)
	}
}

// The guest file has to keep reading and writing the table: a boundary that
// holds because the feature disappeared proves nothing.
func TestTheGuestFileDoesQueryReservations(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile(guestFile)
	if err != nil {
		t.Fatalf("read %s: %v", guestFile, err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), guestFile, source, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", guestFile, err)
	}
	var reads, writes, deletes bool
	for _, text := range goStrings(parsed) {
		lowered := strings.ToLower(text)
		reads = reads || strings.Contains(lowered, "from "+guestTable)
		writes = writes || strings.Contains(lowered, "into "+guestTable)
		deletes = deletes || strings.Contains(lowered, "delete from "+guestTable)
	}
	if !reads || !writes || !deletes {
		t.Errorf("%s no longer reads (%t), writes (%t) and deletes (%t) the table, so the boundary proves nothing",
			guestFile, reads, writes, deletes)
	}
}

// The schema has to create the table the whole rule is about, under that
// name, in a migration, which the boundary allows.
func TestTheMigrationsCreateTheReservationsTable(t *testing.T) {
	t.Parallel()

	files, err := loadMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	found := false
	for _, m := range files {
		if strings.Contains(strings.ToLower(m.sql), "create table "+guestTable) {
			found = true
		}
	}
	if !found {
		t.Error("no migration creates the reservations table, so the boundary is about nothing")
	}
}

// TestBoundaryCatchesEverySpelling proves the checker itself on fixture
// directories: each offending file has to be reported, each innocent one not.
func TestBoundaryCatchesEverySpelling(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		files map[string]string
		want  int // number of violations, at least
	}{
		{"bare select", map[string]string{"a.go": "package s\nvar q = `select * from reservations`\n"}, 1},
		{"upper case", map[string]string{"a.go": "package s\nvar q = \"SELECT * FROM RESERVATIONS\"\n"}, 1},
		{"mixed case", map[string]string{"a.go": "package s\nvar q = \"select * from Reservations\"\n"}, 1},
		{"quoted identifier", map[string]string{"a.go": "package s\nvar q = `select * from \"reservations\"`\n"}, 1},
		{"schema qualified", map[string]string{"a.go": "package s\nvar q = \"delete from public.reservations\"\n"}, 1},
		{"comma join", map[string]string{"a.go": "package s\nvar q = \"select 1 from wishes w, reservations r\"\n"}, 1},
		{"raw string over lines", map[string]string{"a.go": "package s\nvar q = `\nselect *\nfrom\n\treservations`\n"}, 1},
		{"concatenation", map[string]string{"a.go": "package s\nvar q = \"select * from res\" + \"ervations\"\n"}, 1},
		{"parenthesised concatenation", map[string]string{"a.go": "package s\nvar q = \"from \" + (\"reserv\" + \"ations\")\n"}, 1},
		{"split across constants", map[string]string{"a.go": "package s\nconst a = \"reserv\"\nconst b = \"ations\"\nvar q = \"select * from \" + a + b\n"}, 1},
		{"constants in two files", map[string]string{
			"a.go": "package s\nconst a = \"reserv\"\n",
			"b.go": "package s\nconst b = \"ations\"\n",
		}, 1},
		{"unicode escape", map[string]string{"a.go": "package s\nvar q = \"select * from \\u0072eservations\"\n"}, 1},
		{"hex escape", map[string]string{"a.go": "package s\nvar q = \"select * from \\x72eservations\"\n"}, 1},
		{"bare name in a constant", map[string]string{"a.go": "package s\nconst table = \"reservations\"\n"}, 1},
		{"sql file outside migrations", map[string]string{"queries/x.sql": "select * from reservations;\n"}, 1},
		{"nested directory", map[string]string{"sub/a.go": "package s\nvar q = \"select * from reservations\"\n"}, 1},
		{"in migrations is fine", map[string]string{
			"migrations/0001.sql": "create table reservations (id int);\n",
			"a.go":                "package s\nvar q = \"select 1\"\n",
		}, 0},
		{"comment is fine", map[string]string{"a.go": "package s\n// reservations are the guests' business\nvar q = \"select 1\"\n"}, 0},
		{"guest file is exempt", map[string]string{
			guestFile: "package s\nvar q = \"select * from reservations\"\n",
			"a.go":    "package s\nvar q = \"select 1\"\n",
		}, 0},
		{"test file is skipped", map[string]string{
			"a_test.go": "package s\nvar q = \"select * from reservations\"\n",
			"a.go":      "package s\nvar q = \"select 1\"\n",
		}, 0},
		{"clean file", map[string]string{"a.go": "package s\nvar q = \"select * from wishes\"\n"}, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			for name, content := range tc.files {
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatalf("write fixture: %v", err)
				}
			}

			result, err := checkBoundary(root)
			if err != nil {
				t.Fatalf("check boundary: %v", err)
			}
			switch {
			case tc.want == 0 && len(result.violations) != 0:
				t.Errorf("violations = %v, want none", result.violations)
			case tc.want > 0 && len(result.violations) < tc.want:
				t.Errorf("violations = %v, want at least %d", result.violations, tc.want)
			}
		})
	}
}
