// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package store

// The blindness of a reservation is a property of the queries, so it is
// tested on the source and needs no database: it runs everywhere.
//
// Two layers hold it. The types are the first: OwnerStore and GuestStore are
// separate, so a handler given an OwnerStore cannot call a guest method, and
// TestTheTwoSidesShareNoMethod and TestMethodsStayInTheirFiles pin that down.
// The files are the second: only guest.go may name the reservations table,
// and checkBoundary below reads everything else.
//
// What checkBoundary catches, exactly:
//
//   - every constant string expression of the non guest .go files, evaluated
//     with go/types: literals, named constants, constants defined in another
//     file or in guest.go, and any constant "+" expression of them, however far
//     apart the parts are. Package level string variables with an initialiser
//     that is such an expression count the same, wherever they are used, also
//     when they are declared in guest.go. Escapes are decoded, raw strings are
//     read as they are. The match is case insensitive and on the substring, so
//     quoted, schema qualified and prefixed spellings fall under it.
//   - the SQL identifier spelling U&"..." in those strings, which can write the
//     name with escapes.
//   - catalog and metadata queries in those strings (pg_stat, pg_class,
//     pg_catalog, information_schema and a few more), and the functions that run
//     a query given as a string (query_to_xml, cursor_to_xml, execute, do $),
//     which can find or build the table name inside the SQL.
//   - imports: a non guest file may import only the standard library, pgx and
//     internal/config, and never "C". A constant of any other package cannot be
//     evaluated here, and a cgo preamble is C source, so the import itself is
//     the violation.
//   - files are attributed by their real name: a //line directive naming guest.go
//     does not exempt a file.
//   - every other file in the store tree, of any extension and any case, outside
//     migrations/: scanned as text for the name and for the same things. A _test.go
//     file is skipped unless a go:embed directive names it.
//   - migrations/: only files named NNNN_name.sql may be there. Every statement
//     that mentions the table has to be its own create table, an index on it or
//     an alter table of it that does not rename it, move it to another schema,
//     make it inherit or attach a partition. A view, function, rule, trigger,
//     policy, or another table's foreign key over the table is a violation, and
//     so is dynamic SQL (do, execute) and a catalog query, which cannot be read.
//     Statements are cut and comments removed with a reader that knows single
//     quoted, E, double quoted and dollar quoted text, so "--" or "/*" inside a
//     string hides nothing.
//   - sides: no non guest file except store.go mentions GuestStore, Guests or a
//     guest method, and guest.go never mentions OwnerStore or Owners.
//
// This is a guard against accidents by a contributor, not against a determined
// one. The real defence is the review of the handler layer and code review.
//
// What it does NOT catch:
//
//   - a name built when the program runs: fmt.Sprintf, strings.ReplaceAll, a
//     rune slice, a function result, a package level variable initialised by a
//     call, a struct tag read through reflect.
//   - a name built from local variables: x := "reserv"; x + "ations", x +=
//     "ations" in a function, or a package variable += in init(). Only package
//     level declarations are evaluated.
//   - a name split inside an embedded template, or in a .s file.
//   - a go:embed pattern that is quoted and contains a space.
//   - a constant of an allowlisted package that has been changed to spell the
//     name: the allowlist is the review point.
//   - a name an owner query finds through the database in a way the markers
//     above do not list, for example alter table reservations set (...), owner
//     to, create publication ... for all tables, or pg_sequences (which leaks
//     only the global reservation count).
//   - checkSides gaps: an unexported guest method, a type alias for GuestStore
//     declared in store.go, a call by reflection on the method name. (A
//     package level function in guest.go is a violation, so that gap is closed.)
//   - a migration object that does not mention the table and is harmless or not.
//
// Review of the handler layer and code review cover what is left. The type split
// keeps an owner handler away from guest methods only once the wiring passes an
// OwnerStore: the handler layer test (docs/adr/0001-blind-reservations.md) must
// forbid any reference to GuestStore, Core.Guests and Core in an owner handler.

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	guestFile     = "guest.go"
	ownerFile     = "owner.go"
	guestTable    = "reservations"
	migrationsDir = "migrations"
)

// catalogMarkers are fragments of queries that look at the database's own
// catalog. The owner side has no reason to run one, and one can find the
// guests' table without naming it ("relname like 'reserv%'").
var catalogMarkers = []string{
	"pg_stat", "pg_class", "pg_catalog", "information_schema", "pg_tables", "pg_depend",
	"pg_attribute", "pg_namespace", "pg_views", "pg_rewrite", "pg_proc", "pg_trigger",
	"pg_policy", "pg_get_", "regclass",
}

// dynamicMarkers are the ways to run, or build, a query inside a query: the
// table name can then be assembled in SQL where no Go constant shows it.
var dynamicMarkers = []string{
	"query_to_xml", "query_to_xml_and_xmlschema", "cursor_to_xml", "table_to_xml", "schema_to_xml",
	"database_to_xml", "dblink", "ts_stat", "xpath_table", "execute", "do $", "do$",
}

// forbiddenAlter are the parts of an alter table that change what the table is
// called, where it lives or what it is part of. After one of them a later
// query can reach the table without the name.
var forbiddenAlter = []string{"rename", "set schema", "inherit", "attach partition", "detach partition"}

// allowedImports says whether a non guest file may import a package: the
// standard library (no dot in the first path element), pgx and the config.
func allowedImport(path string) bool {
	if path == "C" {
		// A cgo preamble is C source: a #define there is a string no check reads.
		return false
	}
	first, _, _ := strings.Cut(path, "/")
	if !strings.Contains(first, ".") {
		return true
	}
	return strings.HasPrefix(path, "github.com/jackc/pgx/v5") ||
		path == "github.com/ostendorf-cloud/wishlist/internal/config"
}

// migrationName is the only shape a file in migrations/ may have.
var migrationName = regexp.MustCompile(`^[0-9]{4}_[a-z0-9_]+\.sql$`)

// allowedTableStatements are the statements that may mention the table in a
// migration: its own creation, an index on it, an alter of it.
var allowedTableStatements = []*regexp.Regexp{
	regexp.MustCompile(`^create table reservations \(`),
	regexp.MustCompile(`^create (unique )?index [a-z0-9_]+ on reservations( using [a-z]+)? \(`),
	regexp.MustCompile(`^alter table reservations `),
}

// scan is what a boundary check looked at and found.
type scan struct {
	// files are the files that were read, relative to the root.
	files []string
	// violations are readable descriptions, one per offence.
	violations []string
}

// forbiddenText says what is wrong with a string outside guest.go and
// migrations/, or an empty list.
func forbiddenText(text string) []string {
	// Whitespace collapsed, so "do   $$" and a line break do not hide a marker.
	lowered := strings.Join(strings.Fields(strings.ToLower(text)), " ")
	var reasons []string
	if strings.Contains(lowered, guestTable) {
		reasons = append(reasons, "names the "+guestTable+" table")
	}
	if strings.Contains(lowered, `u&"`) {
		reasons = append(reasons, `uses a U&" identifier, which can spell the table name with escapes`)
	}
	for _, marker := range catalogMarkers {
		if strings.Contains(lowered, marker) {
			reasons = append(reasons, "queries the catalog ("+marker+"), which can find the table without naming it")
		}
	}
	for _, marker := range dynamicMarkers {
		if strings.Contains(lowered, marker) {
			reasons = append(reasons, "runs or builds SQL inside SQL ("+marker+"), which can assemble the table name")
		}
	}
	return reasons
}

// checkBoundary reads the store tree under root, see the file comment.
func checkBoundary(root string) (scan, error) {
	var result scan
	goFiles := map[string][]string{} // directory to its non test .go files
	var testFiles []string

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if rel != migrationsDir {
				return nil
			}
			found, err := checkMigrations(path)
			if err != nil {
				return err
			}
			result.files = append(result.files, migrationsDir+"/")
			result.violations = append(result.violations, found...)
			return filepath.SkipDir
		}
		if strings.HasSuffix(rel, "_test.go") {
			// Skipped for now: it is read below when a go:embed names it.
			testFiles = append(testFiles, path)
			return nil
		}
		if filepath.Ext(rel) == ".go" {
			goFiles[filepath.Dir(path)] = append(goFiles[filepath.Dir(path)], path)
			return nil
		}

		// Any other file, whatever its extension or its case: embedded or not,
		// it is text a query could be read from.
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result.files = append(result.files, rel)
		for _, reason := range forbiddenText(string(source)) {
			result.violations = append(result.violations, rel+": "+reason)
		}
		return nil
	})
	if err != nil {
		return scan{}, err
	}

	// A test file is not compiled into the binary, unless a go:embed names it:
	// then its text ships and can be read as a query.
	embedded, err := embeddedFiles(goFiles)
	if err != nil {
		return scan{}, err
	}
	for _, path := range testFiles {
		if !embedded[path] {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return scan{}, err
		}
		rel, _ := filepath.Rel(root, path)
		result.files = append(result.files, rel)
		for _, reason := range forbiddenText(string(source)) {
			result.violations = append(result.violations, rel+": embedded test file "+reason)
		}
	}

	for dir, paths := range goFiles {
		found, err := checkGoDir(root, paths)
		if err != nil {
			return scan{}, fmt.Errorf("%s: %w", dir, err)
		}
		for _, p := range paths {
			rel, _ := filepath.Rel(root, p)
			result.files = append(result.files, rel)
		}
		result.violations = append(result.violations, found...)
	}

	slices.Sort(result.files)
	slices.Sort(result.violations)
	return result, nil
}

var embedDirective = regexp.MustCompile(`(?m)^//go:embed[ \t]+(.+)$`)

// embeddedFiles returns every file a go:embed directive of the given Go files
// names, directories expanded, as a set of paths.
func embeddedFiles(goFiles map[string][]string) (map[string]bool, error) {
	out := map[string]bool{}
	for dir, paths := range goFiles {
		for _, p := range paths {
			source, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			for _, m := range embedDirective.FindAllStringSubmatch(string(source), -1) {
				for _, pattern := range strings.Fields(m[1]) {
					pattern = strings.Trim(strings.TrimPrefix(pattern, "all:"), "\"`")
					matches, err := filepath.Glob(filepath.Join(dir, pattern))
					if err != nil {
						return nil, err
					}
					for _, match := range matches {
						err := filepath.WalkDir(match, func(path string, entry fs.DirEntry, err error) error {
							if err == nil && !entry.IsDir() {
								out[path] = true
							}
							return err
						})
						if err != nil {
							return nil, err
						}
					}
				}
			}
		}
	}
	return out, nil
}

// stubImporter gives every import an empty package. The checker then reports
// errors for everything taken from one, which checkGoDir ignores: it only
// needs the constants, and those do not depend on imports.
type stubImporter struct{}

func (stubImporter) Import(path string) (*types.Package, error) {
	pkg := types.NewPackage(path, filepath.Base(path))
	pkg.MarkComplete()
	return pkg, nil
}

var _ types.Importer = stubImporter{}

// checkGoDir type checks the files of one directory as one package and tests
// every constant string, which is how a name split over non adjacent
// constants, or over two files, is rebuilt.
func checkGoDir(root string, paths []string) ([]string, error) {
	fset := token.NewFileSet()
	var files []*ast.File
	var out []string
	for _, p := range paths {
		parsed, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return nil, err
		}
		files = append(files, parsed)

		// A constant of a package that is not type checked here cannot be
		// evaluated, so a package outside the allowlist is the violation.
		if rel, err := filepath.Rel(root, p); err == nil && rel != guestFile {
			for _, imp := range parsed.Imports {
				if path := strings.Trim(imp.Path.Value, "\"`"); !allowedImport(path) {
					out = append(out, fmt.Sprintf("%s: imports %s: its constants cannot be evaluated, "+
						"review it and add it to allowedImport", rel, path))
				}
			}
		}
	}

	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
		Defs:  map[*ast.Ident]types.Object{},
		Uses:  map[*ast.Ident]types.Object{},
	}
	conf := types.Config{
		Importer:    stubImporter{},
		FakeImportC: true,
		Error:       func(error) {},
	}
	// The errors are expected, see stubImporter. A real syntax problem already
	// failed in ParseFile.
	_, _ = conf.Check("store", fset, files, info)

	// Package level string variables with an initialiser made of constants and
	// other such variables count as constants: they are what a guest.go "var"
	// holding the table name looks like. A variable assigned somewhere else is
	// still read as its initialiser, which errs on the strict side.
	varValue := map[types.Object]string{}
	var eval func(e ast.Expr, depth int) (string, bool)
	eval = func(e ast.Expr, depth int) (string, bool) {
		if depth > 8 {
			return "", false
		}
		if tv, ok := info.Types[e]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
			return constant.StringVal(tv.Value), true
		}
		switch x := e.(type) {
		case *ast.ParenExpr:
			return eval(x.X, depth+1)
		case *ast.Ident:
			v, ok := varValue[info.Uses[x]]
			return v, ok
		case *ast.BinaryExpr:
			if x.Op != token.ADD {
				return "", false
			}
			left, lok := eval(x.X, depth+1)
			right, rok := eval(x.Y, depth+1)
			return left + right, lok && rok
		}
		return "", false
	}
	for range 6 { // a variable may depend on one declared later
		for _, f := range files {
			for _, decl := range f.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.VAR {
					continue
				}
				for _, spec := range gen.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, name := range vs.Names {
						if i >= len(vs.Values) {
							continue
						}
						if v, ok := eval(vs.Values[i], 0); ok {
							varValue[info.Defs[name]] = v
						}
					}
				}
			}
		}
	}

	seen := map[string]bool{}
	report := func(pos token.Pos, value string) {
		// Not Position: that one applies //line directives, and a directive
		// naming guest.go would make this file look like the exempt one.
		position := fset.PositionFor(pos, false)
		rel, err := filepath.Rel(root, position.Filename)
		if err != nil || rel == guestFile {
			return
		}
		for _, reason := range forbiddenText(value) {
			key := rel + "|" + reason
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, fmt.Sprintf("%s:%d: a string %s: %s", rel, position.Line, reason, firstLine(value)))
		}
	}
	for expr, tv := range info.Types {
		if tv.Value != nil && tv.Value.Kind() == constant.String {
			report(expr.Pos(), constant.StringVal(tv.Value))
			continue
		}
		if value, ok := eval(expr, 0); ok {
			report(expr.Pos(), value)
		}
	}
	for ident, obj := range info.Defs {
		if c, ok := obj.(*types.Const); ok && c.Val().Kind() == constant.String {
			report(ident.Pos(), constant.StringVal(c.Val()))
		}
	}
	return out, nil
}

// checkMigrations reads migrations/ under the rules in the file comment.
func checkMigrations(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		name := migrationsDir + "/" + entry.Name()
		if entry.IsDir() || !migrationName.MatchString(entry.Name()) {
			out = append(out, name+": only files named NNNN_name.sql may be in "+migrationsDir)
			continue
		}
		source, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		for _, reason := range migrationViolations(string(source)) {
			out = append(out, name+": "+reason)
		}
	}
	return out, nil
}

func isIdentChar(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// splitSQL cuts a script into statements at the semicolons that are not inside
// a string, an identifier or a dollar quote, and replaces comments with a
// space. "--" and "/*" inside a string are text, not a comment: stripping them
// blindly would let a statement hide its tail ("select '--', wish_id from
// reservations") from the rules. It knows single quotes with ” (and E” with
// backslash escapes), double quoted identifiers, dollar quotes with a tag, line
// comments and nested block comments.
func splitSQL(sql string) []string {
	var statements []string
	var cur strings.Builder
	flush := func() {
		statements = append(statements, cur.String())
		cur.Reset()
	}

	for i := 0; i < len(sql); {
		c := sql[i]
		switch {
		case c == '-' && strings.HasPrefix(sql[i:], "--"):
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			cur.WriteByte(' ')
		case c == '/' && strings.HasPrefix(sql[i:], "/*"):
			depth := 0
			for i < len(sql) {
				switch {
				case strings.HasPrefix(sql[i:], "/*"):
					depth++
					i += 2
				case strings.HasPrefix(sql[i:], "*/"):
					depth--
					i += 2
				default:
					i++
				}
				if depth == 0 {
					break
				}
			}
			cur.WriteByte(' ')
		case c == '\'':
			escapes := i > 0 && (sql[i-1] == 'e' || sql[i-1] == 'E') && (i < 2 || !isIdentChar(sql[i-2]))
			cur.WriteByte(c)
			i++
			for i < len(sql) {
				ch := sql[i]
				cur.WriteByte(ch)
				i++
				if escapes && ch == '\\' && i < len(sql) {
					cur.WriteByte(sql[i])
					i++
					continue
				}
				if ch == '\'' {
					if i < len(sql) && sql[i] == '\'' { // '' is a quote inside the string
						cur.WriteByte('\'')
						i++
						continue
					}
					break
				}
			}
		case c == '"':
			cur.WriteByte(c)
			i++
			for i < len(sql) {
				ch := sql[i]
				cur.WriteByte(ch)
				i++
				if ch == '"' {
					if i < len(sql) && sql[i] == '"' {
						cur.WriteByte('"')
						i++
						continue
					}
					break
				}
			}
		case c == '$' && (i == 0 || !isIdentChar(sql[i-1])):
			// A dollar quote opens with $tag$, the tag being empty or an identifier
			// that does not start with a digit.
			end := i + 1
			for end < len(sql) && isIdentChar(sql[end]) {
				end++
			}
			tag := sql[i:min(end+1, len(sql))]
			if end < len(sql) && sql[end] == '$' && (end == i+1 || sql[i+1] < '0' || sql[i+1] > '9') {
				offset := strings.Index(sql[end+1:], tag)
				if offset < 0 { // never closed: the rest is the string
					cur.WriteString(sql[i:])
					i = len(sql)
				} else {
					stop := end + 1 + offset + len(tag)
					cur.WriteString(sql[i:stop])
					i = stop
				}
				break
			}
			cur.WriteByte(c)
			i++
		case c == ';':
			flush()
			i++
		default:
			cur.WriteByte(c)
			i++
		}
	}
	flush()
	return statements
}

// migrationViolations reads one migration's statements. It is a text rule, not
// a SQL parser: it errs on the strict side, and a statement it cannot place is
// reported rather than guessed at.
func migrationViolations(sql string) []string {
	var out []string
	for _, statement := range splitSQL(sql) {
		statement = strings.Join(strings.Fields(strings.ToLower(statement)), " ")
		if statement == "" {
			continue
		}
		unquoted := strings.ReplaceAll(statement, `"`, "")
		switch {
		case strings.HasPrefix(statement, "do ") || strings.Contains(statement, "execute "):
			out = append(out, "dynamic SQL (do, execute) cannot be checked: "+clip(statement))
		case strings.Contains(statement, `u&"`):
			out = append(out, `a U&" identifier can spell the table name with escapes: `+clip(statement))
		case mentionsAny(statement, catalogMarkers) || mentionsAny(statement, dynamicMarkers):
			out = append(out, "a catalog query or a query built inside SQL can find the table without naming it: "+
				clip(statement))
		case strings.Contains(unquoted, guestTable) && !allowedTableStatement(statement):
			out = append(out, "a statement other than the table's own create table, an index on it or an alter table of it "+
				"mentions it: "+clip(statement))
		}
	}
	return out
}

func mentionsAny(text string, markers []string) bool {
	return slices.ContainsFunc(markers, func(m string) bool { return strings.Contains(text, m) })
}

func allowedTableStatement(statement string) bool {
	if strings.HasPrefix(statement, "alter table reservations ") && mentionsAny(statement, forbiddenAlter) {
		// A rename or a move makes the table reachable under another name.
		return false
	}
	return slices.ContainsFunc(allowedTableStatements, func(re *regexp.Regexp) bool { return re.MatchString(statement) })
}

func clip(text string) string {
	if len(text) > 80 {
		return text[:80] + "..."
	}
	return text
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
	for _, want := range []string{ownerFile, "store.go", "model.go", "migrate.go", migrationsDir + "/"} {
		if !slices.Contains(result.files, want) {
			t.Errorf("the boundary check did not read %s, it read %v", want, result.files)
		}
	}
	for _, violation := range result.violations {
		t.Errorf("only guest.go may name the reservations table: %s", violation)
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
	text := strings.ToLower(string(source))
	var reads, writes, deletes bool
	reads = strings.Contains(text, "from "+guestTable)
	writes = strings.Contains(text, "into "+guestTable)
	deletes = strings.Contains(text, "delete from "+guestTable)
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

// exportedMethods lists a type's exported method names.
func exportedMethods(t reflect.Type) []string {
	var names []string
	for i := range t.NumMethod() {
		names = append(names, t.Method(i).Name)
	}
	return names
}

// The two sides share no method, and the owner's side has no lifecycle
// either, so an OwnerStore can never be turned into a GuestStore or a Core.
func TestTheTwoSidesShareNoMethod(t *testing.T) {
	t.Parallel()

	owner := exportedMethods(reflect.TypeFor[*OwnerStore]())
	guest := exportedMethods(reflect.TypeFor[*GuestStore]())
	core := exportedMethods(reflect.TypeFor[*Core]())

	if len(owner) == 0 || len(guest) == 0 {
		t.Fatalf("a side has no methods (owner %v, guest %v), the check proves nothing", owner, guest)
	}
	for _, name := range owner {
		if slices.Contains(guest, name) {
			t.Errorf("OwnerStore and GuestStore both have %s", name)
		}
		if slices.Contains(core, name) {
			t.Errorf("OwnerStore has %s, which is Core's", name)
		}
	}
	for _, name := range guest {
		if slices.Contains(core, name) {
			t.Errorf("GuestStore has %s, which is Core's", name)
		}
	}
	for _, name := range []string{"Owners", "Guests"} {
		if !slices.Contains(core, name) {
			t.Errorf("Core has no %s", name)
		}
	}

	// A promoted method would show up above, but an embedded field is the way
	// it would sneak in: the sides hold the pool and nothing else.
	for _, typ := range []reflect.Type{reflect.TypeFor[OwnerStore](), reflect.TypeFor[GuestStore]()} {
		for field := range typ.Fields() {
			if field.Anonymous {
				t.Errorf("%s embeds %s: embedding promotes methods across the boundary", typ.Name(), field.Name)
			}
		}
	}
}

// The methods of each side are in that side's file and nowhere else.
func TestMethodsStayInTheirFiles(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	owners, guests := 0, 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
				continue
			}
			recv := receiverName(fn.Recv.List[0].Type)
			switch recv {
			case "OwnerStore":
				owners++
				if name != ownerFile {
					t.Errorf("%s declares %s on OwnerStore: owner methods belong in %s", name, fn.Name.Name, ownerFile)
				}
			case "GuestStore":
				guests++
				if name != guestFile {
					t.Errorf("%s declares %s on GuestStore: guest methods belong in %s", name, fn.Name.Name, guestFile)
				}
			case "Core":
				if name == ownerFile || name == guestFile {
					t.Errorf("%s declares %s on Core: lifecycle belongs in store.go or migrate.go", name, fn.Name.Name)
				}
			}
		}
	}
	if owners == 0 || guests == 0 {
		t.Errorf("found %d owner and %d guest methods, the check proves nothing", owners, guests)
	}
}

// checkSides reads the non test .go files of dir and reports every place a
// side reaches over to the other: a file other than guest.go and store.go that
// mentions GuestStore, Guests or the name of a guest method, and guest.go
// mentioning OwnerStore or Owners. The types already keep the two method sets
// apart, but both are in one package: (&GuestStore{pool: s.pool}) in owner.go
// compiles. store.go is where Core hands the two sides out.
func checkSides(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	parsed := map[string]*ast.File{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, err
		}
		parsed[name] = f
	}

	var guestMethods []string
	if f, ok := parsed[guestFile]; ok {
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv != nil && len(fn.Recv.List) > 0 &&
				receiverName(fn.Recv.List[0].Type) == "GuestStore" && fn.Name.IsExported() {
				guestMethods = append(guestMethods, fn.Name.Name)
			}
		}
	}

	var out []string
	// A package level function in guest.go could be called from owner.go and
	// would not be a method of GuestStore, so no selector check would see it.
	// guest.go has none: everything it offers is a method.
	if f, ok := parsed[guestFile]; ok {
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				out = append(out, fmt.Sprintf("%s:%d: declares the package level function %s: guest.go has methods of GuestStore only",
					guestFile, fset.PositionFor(fn.Pos(), false).Line, fn.Name.Name))
			}
		}
	}
	for name, f := range parsed {
		var forbidden []string
		switch name {
		case "store.go":
			continue
		case guestFile:
			forbidden = []string{"OwnerStore", "Owners"}
		default:
			forbidden = []string{"GuestStore", "Guests"}
		}
		ast.Inspect(f, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.Ident:
				if slices.Contains(forbidden, n.Name) {
					out = append(out, fmt.Sprintf("%s:%d: mentions %s, which belongs to the other side",
						name, fset.PositionFor(n.Pos(), false).Line, n.Name))
				}
			case *ast.SelectorExpr:
				if name != guestFile && slices.Contains(guestMethods, n.Sel.Name) {
					out = append(out, fmt.Sprintf("%s:%d: calls the guest method %s",
						name, fset.PositionFor(n.Sel.Pos(), false).Line, n.Sel.Name))
				}
			}
			return true
		})
	}
	slices.Sort(out)
	return out, nil
}

func TestTheSidesDoNotReachAcrossFiles(t *testing.T) {
	t.Parallel()

	found, err := checkSides(".")
	if err != nil {
		t.Fatalf("check sides: %v", err)
	}
	for _, f := range found {
		t.Error(f)
	}
}

func TestCheckSidesCatchesEveryCrossReference(t *testing.T) {
	t.Parallel()

	const guest = "package s\ntype GuestStore struct{}\nfunc (s *GuestStore) Reserve() {}\nfunc (s *GuestStore) GuestWishes() {}\n"
	cases := []struct {
		name  string
		files map[string]string
		want  int
	}{
		{"owner file builds a guest store", map[string]string{
			guestFile: guest,
			"owner.go": "package s\ntype OwnerStore struct{ pool int }\n" +
				"func (s *OwnerStore) X() { (&GuestStore{}).GuestWishes() }\n",
		}, 2},
		{"owner file calls a guest method on a value", map[string]string{
			guestFile:  guest,
			"owner.go": "package s\ntype OwnerStore struct{}\nfunc (s *OwnerStore) X(g interface{ Reserve() }) { g.Reserve() }\n",
		}, 1},
		{"owner file asks Core for the guests", map[string]string{
			guestFile:  guest,
			"owner.go": "package s\ntype OwnerStore struct{ c interface{ Guests() int } }\nfunc (s *OwnerStore) X() { s.c.Guests() }\n",
		}, 1},
		{"another file reaches over", map[string]string{
			guestFile:    guest,
			"helpers.go": "package s\nvar g *GuestStore\n",
		}, 1},
		{"guest file builds an owner store", map[string]string{
			guestFile: guest + "func f() { _ = &OwnerStore{} }\n",
		}, 1},
		{"guest file asks for the owners", map[string]string{
			guestFile: guest + "func f(c interface{ Owners() int }) { c.Owners() }\n",
		}, 1},
		{"guest file with a free function an owner file could call", map[string]string{
			guestFile:  guest + "func helper() int { return 1 }\n",
			"owner.go": "package s\nfunc f() int { return helper() }\n",
		}, 1},
		{"store.go hands both out", map[string]string{
			guestFile:  guest,
			"store.go": "package s\ntype Core struct{}\nfunc (c *Core) Guests() *GuestStore { return &GuestStore{} }\nfunc (c *Core) Owners() *OwnerStore { return nil }\n",
		}, 0},
		{"a field named like a guest method is fine as an identifier", map[string]string{
			guestFile:  guest,
			"owner.go": "package s\nfunc f() { Reserve := 1; _ = Reserve }\n",
		}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			for name, content := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
					t.Fatalf("write fixture: %v", err)
				}
			}
			found, err := checkSides(dir)
			if err != nil {
				t.Fatalf("check sides: %v", err)
			}
			switch {
			case tc.want == 0 && len(found) != 0:
				t.Errorf("found %v, want nothing", found)
			case tc.want > 0 && len(found) < tc.want:
				t.Errorf("found %v, want at least %d", found, tc.want)
			}
		})
	}
}

func receiverName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// TestBoundaryCatchesEverySpelling proves the checker itself on fixture
// directories: each offending tree has to be reported, each innocent one not.
func TestBoundaryCatchesEverySpelling(t *testing.T) {
	t.Parallel()

	const okMigration = "create table reservations (id int);\ncreate index reservations_idx on reservations (id);\n"
	cases := []struct {
		name  string
		files map[string]string
		want  int // violations, at least; 0 means none
	}{
		// Plain spellings.
		{"bare select", map[string]string{"a.go": "package s\nvar q = `select * from reservations`\n"}, 1},
		{"upper case", map[string]string{"a.go": "package s\nvar q = \"SELECT * FROM RESERVATIONS\"\n"}, 1},
		{"mixed case", map[string]string{"a.go": "package s\nvar q = \"select * from Reservations\"\n"}, 1},
		{"quoted identifier", map[string]string{"a.go": "package s\nvar q = `select * from \"reservations\"`\n"}, 1},
		{"schema qualified", map[string]string{"a.go": "package s\nvar q = \"delete from public.reservations\"\n"}, 1},
		{"comma join", map[string]string{"a.go": "package s\nvar q = \"select 1 from wishes w, reservations r\"\n"}, 1},
		{"raw string over lines", map[string]string{"a.go": "package s\nvar q = `\nselect *\nfrom\n\treservations`\n"}, 1},
		{"unicode escape", map[string]string{"a.go": "package s\nvar q = \"select * from \\u0072eservations\"\n"}, 1},
		{"hex escape", map[string]string{"a.go": "package s\nvar q = \"select * from \\x72eservations\"\n"}, 1},
		{"bare name in a constant", map[string]string{"a.go": "package s\nconst table = \"reservations\"\n"}, 1},
		{"nested directory", map[string]string{"sub/a.go": "package s\nvar q = \"select * from reservations\"\n"}, 1},

		// Splits: adjacent, far apart, over files, through the guest file.
		{"concatenation", map[string]string{"a.go": "package s\nvar q = \"select * from res\" + \"ervations\"\n"}, 1},
		{"parenthesised concatenation", map[string]string{"a.go": "package s\nvar q = \"from \" + (\"reserv\" + \"ations\")\n"}, 1},
		{"split across constants", map[string]string{"a.go": "package s\nconst a = \"reserv\"\nconst b = \"ations\"\nvar q = \"select * from \" + a + b\n"}, 1},
		{"split across constants that are not adjacent", map[string]string{
			"a.go": "package s\nconst a = \"reserv\"\nconst x = \"unrelated\"\nconst y = \"other\"\nconst b = \"ations\"\nconst t = a + b\nvar q = \"from \" + t\n",
		}, 1},
		{"constants in two files", map[string]string{
			"a.go": "package s\nconst a = \"reserv\"\n",
			"b.go": "package s\nconst t = a + \"ations\"\n",
		}, 1},
		{"constant from the guest file", map[string]string{
			guestFile: "package s\nconst guestTable = \"reservations\"\n",
			"a.go":    "package s\nvar q = \"select * from \" + guestTable\n",
		}, 1},
		{"typed string constant", map[string]string{"a.go": "package s\ntype T string\nconst c T = \"reservations\"\n"}, 1},

		// SQL spellings.
		{"U& identifier", map[string]string{"a.go": "package s\nvar q = `select * from U&\"\\0072eservations\"`\n"}, 1},
		{"catalog pg_stat", map[string]string{"a.go": "package s\nvar q = \"select * from pg_stat_user_tables where relname like 'reserv%'\"\n"}, 1},
		{"catalog pg_class", map[string]string{"a.go": "package s\nvar q = \"select relname from pg_class\"\n"}, 1},
		{"catalog information_schema", map[string]string{"a.go": "package s\nvar q = \"select * from information_schema.tables\"\n"}, 1},
		{"catalog pg_catalog", map[string]string{"a.go": "package s\nvar q = \"select * from pg_catalog.pg_tables\"\n"}, 1},
		{"catalog regclass", map[string]string{"a.go": "package s\nvar q = \"select to_regclass('x')\"\n"}, 1},

		// Files other than .go.
		{"sql file outside migrations", map[string]string{"queries/x.sql": "select * from reservations;\n"}, 1},
		{"upper case sql extension", map[string]string{"queries/x.SQL": "select * from reservations;\n"}, 1},
		{"txt file", map[string]string{"queries/x.txt": "select * from reservations;\n"}, 1},
		{"file without extension", map[string]string{"queries/x": "select * from Reservations;\n"}, 1},
		{"catalog in a sql file", map[string]string{"x.sql": "select * from pg_stat_user_tables;\n"}, 1},

		// migrations/.
		{"go file in migrations", map[string]string{"migrations/0001_a.sql": okMigration, "migrations/x.go": "package s\n"}, 1},
		{"txt file in migrations", map[string]string{"migrations/0001_a.sql": okMigration, "migrations/notes.txt": "x"}, 1},
		{"upper case sql in migrations", map[string]string{"migrations/0001_a.SQL": okMigration}, 1},
		{"unnumbered migration", map[string]string{"migrations/init.sql": okMigration}, 1},
		{"directory in migrations", map[string]string{"migrations/sub/0001_a.sql": okMigration}, 1},
		{"view over the table", map[string]string{
			"migrations/0001_a.sql": okMigration + "create view v as select id from reservations;\n",
		}, 1},
		{"materialized view", map[string]string{
			"migrations/0001_a.sql": okMigration + "create materialized view v as select count(*) from \"reservations\";\n",
		}, 1},
		{"function over the table", map[string]string{
			"migrations/0001_a.sql": okMigration + "create function f() returns int language sql as 'select count(*)::int from reservations';\n",
		}, 1},
		{"rule over the table", map[string]string{
			"migrations/0001_a.sql": okMigration + "create rule r as on insert to wishes do also insert into reservations (wish_id) values (new.id);\n",
		}, 1},
		{"foreign key from another table to the table", map[string]string{
			"migrations/0001_a.sql": okMigration + "create table audit (r bigint references reservations (id));\n",
		}, 1},
		{"alter another table to point at the table", map[string]string{
			"migrations/0001_a.sql": okMigration + "alter table comments add foreign key (r) references reservations (id);\n",
		}, 1},
		{"dynamic SQL in a do block", map[string]string{
			"migrations/0001_a.sql": okMigration + "do $$ begin execute 'create view v as select * from ' || 'reserv' || 'ations'; end $$;\n",
		}, 1},
		{"U& identifier in a migration", map[string]string{
			"migrations/0001_a.sql": okMigration + "create view v as select * from U&\"\\0072eservations\";\n",
		}, 1},

		// Package level variables, imports, SQL inside SQL, embedded tests,
		// catalogs and renames in migrations.
		{"package variable in the guest file used elsewhere", map[string]string{
			guestFile: "package s\nvar guestTableName = \"reservations\"\n",
			"a.go":    "package s\nvar q = \"select count(*) from \" + guestTableName\n",
		}, 1},
		{"package variables chained", map[string]string{
			guestFile: "package s\nvar a = \"reserv\"\nvar b = a + \"ations\"\n",
			"a.go":    "package s\nvar c = \"from \" + b\n",
		}, 1},
		{"package variable declared after its use", map[string]string{
			"a.go": "package s\nvar q = \"from \" + t\nvar t = u + \"ations\"\nvar u = \"reserv\"\n",
		}, 1},
		{"package variable used on its own", map[string]string{
			guestFile: "package s\nvar guestTableName = \"reservations\"\n",
			"a.go":    "package s\nfunc f(run func(string)) { run(guestTableName) }\n",
		}, 1},
		{"constant from another package", map[string]string{
			"a.go": "package s\nimport \"example.com/sqlnames\"\nvar q = sqlnames.T + \"ations\"\n",
		}, 1},
		{"query_to_xml builds the name in SQL", map[string]string{
			"a.go": "package s\nvar q = \"select query_to_xml('select * from ' || 'reserv' || 'ations', true, false, '')\"\n",
		}, 1},
		{"cursor_to_xml", map[string]string{"a.go": "package s\nvar q = \"select cursor_to_xml(c, 1, false, false, '')\"\n"}, 1},
		{"execute in a string", map[string]string{"a.go": "package s\nvar q = \"execute stmt\"\n"}, 1},
		{"do block in a string", map[string]string{"a.go": "package s\nvar q = \"do $$ begin null; end $$\"\n"}, 1},
		{"embedded test file", map[string]string{
			"a.go":           "package s\nimport _ \"embed\"\n//go:embed q/peek_test.go\nvar peek string\n",
			"q/peek_test.go": "package q\nvar x = \"select * from reservations\"\n",
		}, 1},
		{"embedded directory with a test file", map[string]string{
			"a.go":           "package s\nimport _ \"embed\"\n//go:embed q\nvar peek string\n",
			"q/peek_test.go": "package q\nvar x = \"select * from reservations\"\n",
		}, 1},
		{"view over a catalog in a migration", map[string]string{
			"migrations/0001_a.sql": okMigration +
				"create view wish_stats as select n_live_tup from pg_stat_user_tables where relname like 'reserv%';\n",
		}, 1},
		{"information_schema in a migration", map[string]string{
			"migrations/0001_a.sql": okMigration + "create view v as select * from information_schema.tables;\n",
		}, 1},
		{"rename of the table", map[string]string{
			"migrations/0001_a.sql": okMigration + "alter table reservations rename to gifts;\n",
		}, 1},
		{"set schema of the table", map[string]string{
			"migrations/0001_a.sql": okMigration + "alter table reservations set schema other;\n",
		}, 1},
		{"inherit", map[string]string{
			"migrations/0001_a.sql": okMigration + "alter table reservations inherit parent;\n",
		}, 1},
		{"attach partition", map[string]string{
			"migrations/0001_a.sql": okMigration + "alter table reservations attach partition p for values in (1);\n",
		}, 1},
		{"query_to_xml in a migration", map[string]string{
			"migrations/0001_a.sql": okMigration + "create view v as select query_to_xml('select 1', true, false, '');\n",
		}, 1},

		// Comment markers inside strings are text, not comments.
		{"line comment marker in a string hides the table", map[string]string{
			"migrations/0001_a.sql": okMigration + "create view wish_marks as select '--' as x, wish_id from reservations ;\n",
		}, 1},
		{"block comment marker in a string hides the table", map[string]string{
			"migrations/0001_a.sql": okMigration + "create view wish_marks as select '/*' as x, wish_id from reservations ;\n",
		}, 1},
		{"line comment marker in a quoted identifier", map[string]string{
			"migrations/0001_a.sql": okMigration + "create view v as select \"a--b\", wish_id from reservations;\n",
		}, 1},
		{"line comment marker in an E string with an escaped quote", map[string]string{
			"migrations/0001_a.sql": okMigration + "create view v as select e'a\\'b -- c' as x, id from reservations;\n",
		}, 1},
		{"line comment marker in a doubled quote string", map[string]string{
			"migrations/0001_a.sql": okMigration + "create view v as select 'it''s -- x' as x, id from reservations;\n",
		}, 1},
		{"line comment marker in a dollar quote", map[string]string{
			"migrations/0001_a.sql": okMigration + "create view v as select $$ -- $$ as x, id from reservations;\n",
		}, 1},
		{"line comment marker in a tagged dollar quote", map[string]string{
			"migrations/0001_a.sql": okMigration + "create view v as select $t$ /* $t$ as x, id from reservations;\n",
		}, 1},
		{"a comment that is a comment still hides nothing", map[string]string{
			"migrations/0001_a.sql": okMigration + "create view v as select 1 -- from reservations\n;\n",
		}, 0},
		{"a block comment inside a block comment", map[string]string{
			"migrations/0001_a.sql": okMigration + "/* a /* reservations */ still a comment */ select 1;\n",
		}, 0},

		// Line directives must not rename a file.
		{"line directive naming the guest file", map[string]string{
			"a.go": "package s\n//line guest.go:1\nvar q = \"select * from reservations\"\n",
		}, 1},
		{"block line directive naming the guest file", map[string]string{
			"a.go": "package s\n/*line guest.go:1:1*/var q = \"select * from reservations\"\n",
		}, 1},

		// More ways to run a query that is a string.
		{"ts_stat", map[string]string{"a.go": "package s\nvar q = \"select * from ts_stat('select v from ' || 'reserv' || 'ations')\"\n"}, 1},
		{"xpath_table", map[string]string{"a.go": "package s\nvar q = \"select * from xpath_table('id', 'doc', 't', '/x', 'true')\"\n"}, 1},
		{"query_to_xml_and_xmlschema", map[string]string{"a.go": "package s\nvar q = \"select query_to_xml_and_xmlschema('select 1', true, false, '')\"\n"}, 1},
		{"table_to_xml", map[string]string{"a.go": "package s\nvar q = \"select table_to_xml('t', true, false, '')\"\n"}, 1},
		{"schema_to_xml", map[string]string{"a.go": "package s\nvar q = \"select schema_to_xml('public', true, false, '')\"\n"}, 1},
		{"database_to_xml", map[string]string{"a.go": "package s\nvar q = \"select database_to_xml(true, false, '')\"\n"}, 1},
		{"dblink", map[string]string{"a.go": "package s\nvar q = \"select * from dblink('select 1')\"\n"}, 1},
		{"do block with a line break and spaces", map[string]string{"a.go": "package s\nvar q = \"do   \\n  $$ begin null; end $$\"\n"}, 1},
		{"ts_stat in a migration", map[string]string{
			"migrations/0001_a.sql": okMigration + "create view v as select * from ts_stat('select 1');\n",
		}, 1},
		{"xpath_table in a migration", map[string]string{
			"migrations/0001_a.sql": okMigration + "create view v as select * from xpath_table('a','b','c','d','e');\n",
		}, 1},
		{"detach partition", map[string]string{
			"migrations/0001_a.sql": okMigration + "alter table reservations detach partition p;\n",
		}, 1},
		{"cgo import", map[string]string{"a.go": "package s\n/*\n#define T \"reserv\"\n*/\nimport \"C\"\n"}, 1},

		// Fine.
		{"allowed imports", map[string]string{
			"a.go": "package s\nimport (\n\t\"fmt\"\n\t\"github.com/jackc/pgx/v5\"\n\t\"github.com/jackc/pgx/v5/pgxpool\"\n" +
				"\t\"github.com/ostendorf-cloud/wishlist/internal/config\"\n)\nvar _ = fmt.Sprint\n",
		}, 0},
		{"a test file nothing embeds is skipped", map[string]string{
			"a.go":           "package s\nvar q = 1\n",
			"q/peek_test.go": "package q\nvar x = \"select * from reservations\"\n",
		}, 0},
		{"a package variable that holds something else", map[string]string{
			guestFile: "package s\nvar other = \"wishes\"\n",
			"a.go":    "package s\nvar q = \"select * from \" + other\n",
		}, 0},
		{"table, index and comment in migrations", map[string]string{
			"migrations/0001_a.sql": "-- reservations are the guests' business\n/* reservations */\n" + okMigration +
				"alter table reservations add column x int;\ncreate unique index reservations_u on reservations (id);\n",
			"a.go": "package s\nvar q = \"select 1\"\n",
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
		{"advisory lock is not a catalog query", map[string]string{"a.go": "package s\nvar q = \"select pg_advisory_xact_lock($1)\"\n"}, 0},

		// The limit, stated as a test so nobody reads the check as stronger
		// than it is: a name built at run time is not seen. The handler layer
		// review and the code review cover it.
		{"KNOWN LIMIT: fmt.Sprintf at run time", map[string]string{
			"a.go": "package s\nimport \"fmt\"\nvar q = fmt.Sprintf(\"select * from %s%s\", \"reserv\", \"ations\")\n",
		}, 0},
		{"KNOWN LIMIT: strings.ReplaceAll at run time", map[string]string{
			"a.go": "package s\nimport \"strings\"\nvar q = strings.ReplaceAll(\"select * from rXs\", \"X\", \"eservation\")\n",
		}, 0},
		{"KNOWN LIMIT: rune slice", map[string]string{
			"a.go": "package s\nvar q = string([]rune{'r', 'e', 's', 'e', 'r', 'v', 'a', 't', 'i', 'o', 'n', 's'})\n",
		}, 0},
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
