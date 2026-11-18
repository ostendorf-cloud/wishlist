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
//     apart the parts are. Escapes are decoded, raw strings are read as they
//     are. The match is case insensitive and on the substring, so quoted,
//     schema qualified and prefixed spellings fall under it.
//   - the SQL identifier spelling U&"..." in those strings, which can write the
//     name with escapes.
//   - catalog and metadata queries in those strings (pg_stat, pg_class,
//     pg_catalog, information_schema and a few more), which can find the table
//     without naming it.
//   - every other file in the store tree, of any extension and any case, outside
//     migrations/: scanned as text for the name and for the same two things.
//   - migrations/: only files named NNNN_name.sql may be there. Every statement
//     that mentions the table has to be its own create table, an index on it or
//     an alter table of it. A view, function, rule, trigger, policy, or another
//     table's foreign key over the table is a violation, and so is dynamic SQL
//     (do, execute), which cannot be read.
//
// What it does NOT catch: a name built when the program runs, for example with
// fmt.Sprintf, strings.ReplaceAll, a rune slice or a function result. A static
// test cannot see those. The review of the handler layer and the code review
// cover them, and the type split keeps an owner handler away from guest
// methods in the first place. It also cannot prove that a migration object
// which does not mention the table is harmless.

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
	lowered := strings.ToLower(text)
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
	return reasons
}

// checkBoundary reads the store tree under root, see the file comment.
func checkBoundary(root string) (scan, error) {
	var result scan
	goFiles := map[string][]string{} // directory to its non test .go files

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
	for _, p := range paths {
		parsed, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return nil, err
		}
		files = append(files, parsed)
	}

	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
		Defs:  map[*ast.Ident]types.Object{},
	}
	conf := types.Config{
		Importer:    stubImporter{},
		FakeImportC: true,
		Error:       func(error) {},
	}
	// The errors are expected, see stubImporter. A real syntax problem already
	// failed in ParseFile.
	_, _ = conf.Check("store", fset, files, info)

	seen := map[string]bool{}
	var out []string
	report := func(pos token.Pos, value string) {
		position := fset.Position(pos)
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

var (
	blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	lineComment  = regexp.MustCompile(`--[^\n]*`)
)

// migrationViolations reads one migration's statements. It is a text rule, not
// a SQL parser: it errs on the strict side, and a statement it cannot place is
// reported rather than guessed at.
func migrationViolations(sql string) []string {
	sql = blockComment.ReplaceAllString(sql, " ")
	sql = lineComment.ReplaceAllString(sql, " ")

	var out []string
	for statement := range strings.SplitSeq(strings.ToLower(sql), ";") {
		statement = strings.Join(strings.Fields(statement), " ")
		if statement == "" {
			continue
		}
		unquoted := strings.ReplaceAll(statement, `"`, "")
		switch {
		case strings.HasPrefix(statement, "do ") || strings.Contains(statement, "execute "):
			out = append(out, "dynamic SQL (do, execute) cannot be checked: "+clip(statement))
		case strings.Contains(statement, `u&"`):
			out = append(out, `a U&" identifier can spell the table name with escapes: `+clip(statement))
		case strings.Contains(unquoted, guestTable) && !allowedTableStatement(statement):
			out = append(out, "a statement other than the table's own create table, an index on it or an alter table of it "+
				"mentions it: "+clip(statement))
		}
	}
	return out
}

func allowedTableStatement(statement string) bool {
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

		// Fine.
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
