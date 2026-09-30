package tools

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// makeSqliteDB writes a scratch database under dir with a writable
// connection of its own, the way an app would have, and returns its path.
func makeSqliteDB(t *testing.T, dir, name string, stmts ...string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("fixture %q: %v", s, err)
		}
	}
	return p
}

// usersDB is the fixture most tests read: a table with an index and a view.
func usersDB(t *testing.T) string {
	t.Helper()
	return makeSqliteDB(t, t.TempDir(), "app.db",
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, email TEXT, age INTEGER)`,
		`CREATE UNIQUE INDEX users_email ON users(email)`,
		`CREATE VIEW adults AS SELECT id, name FROM users WHERE age >= 18`,
		`INSERT INTO users (name, email, age) VALUES ('alice', 'a@example.com', 34), ('bob', 'b@example.com', 12), ('carol', NULL, 51)`,
	)
}

func runSqlite(t *testing.T, r *Recorder, args map[string]any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return r.Execute(SqliteName, raw)
}

func mustSqlite(t *testing.T, args map[string]any) string {
	t.Helper()
	out, err := runSqlite(t, NewRecorder(), args)
	if err != nil {
		t.Fatalf("sqlite %v: %v", args, err)
	}
	return out
}

// With no sql the answer is the schema — what .tables, .schema and a
// count(*) per table take three commands to say — in one call.
func TestSqlite_NoSQLAnswersTheSchema(t *testing.T) {
	p := usersDB(t)
	got := mustSqlite(t, map[string]any{"path": p})
	for _, want := range []string{
		"table users — 3 rows",
		"  id INTEGER PRIMARY KEY",
		"  name TEXT NOT NULL",
		"  email TEXT",
		"  index users_email UNIQUE (email)",
		"view adults — rows not counted",
		"  id INTEGER",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("schema should say %q, got:\n%s", want, got)
		}
	}
	if strings.Index(got, "view adults") < strings.Index(got, "table users") {
		t.Errorf("tables should come before views:\n%s", got)
	}
}

// An FTS index's own storage tables are counted, not listed beside the
// tables somebody wrote.
func TestSqlite_TheSchemaFoldsShadowTables(t *testing.T) {
	p := makeSqliteDB(t, t.TempDir(), "fts.db", `CREATE VIRTUAL TABLE notes USING fts5(body)`, `INSERT INTO notes VALUES ('hello')`)
	got := mustSqlite(t, map[string]any{"path": p})
	if !strings.Contains(got, "virtual notes — 1 rows") {
		t.Errorf("the virtual table should be listed and counted:\n%s", got)
	}
	if strings.Contains(got, "notes_data") || !strings.Contains(got, "tables behind virtual tables are not listed") {
		t.Errorf("shadow tables should be counted, not listed:\n%s", got)
	}
	empty := makeSqliteDB(t, t.TempDir(), "empty.db", `PRAGMA user_version = 1`)
	if got := mustSqlite(t, map[string]any{"path": empty}); got != "empty: no tables or views" {
		t.Errorf("empty database = %q", got)
	}
}

// One statement is an aligned table; several are one call, each labelled.
func TestSqlite_SeveralStatementsAreOneCallEachLabelled(t *testing.T) {
	p := usersDB(t)
	got := mustSqlite(t, map[string]any{"path": p, "sql": []string{"SELECT id, name FROM users ORDER BY id"}})
	want := "id  name\n--  -----\n1   alice\n2   bob\n3   carol"
	if got != want {
		t.Errorf("one statement:\n%s\nwant:\n%s", got, want)
	}
	// A bare string is one statement too.
	if got := mustSqlite(t, map[string]any{"path": p, "sql": "SELECT count(*) AS n FROM users"}); got != "n\n-\n3" {
		t.Errorf("bare string = %q", got)
	}

	got = mustSqlite(t, map[string]any{"path": p, "sql": []string{
		"SELECT count(*) AS n FROM users",
		"SELECT name FROM adults ORDER BY name",
		"SELECT nope FROM users",
		"SELECT name FROM users WHERE 0",
	}})
	for _, want := range []string{
		"==> 1: SELECT count(*) AS n FROM users <==\nn\n-\n3",
		"==> 2: SELECT name FROM adults ORDER BY name <==\nname\n-----\nalice\ncarol",
		"==> 3: SELECT nope FROM users <==\nerror: no such column: nope",
		"==> 4: SELECT name FROM users WHERE 0 <==\nname\n----\n(no rows)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("several statements should include %q, got:\n%s", want, got)
		}
	}
}

// Values are bound, never spliced: a value that is SQL stays a value.
func TestSqlite_ParamsBindValues(t *testing.T) {
	p := usersDB(t)
	got := mustSqlite(t, map[string]any{"path": p,
		"sql":    []string{"SELECT name FROM users WHERE age > :min ORDER BY name", "SELECT ? AS a, ?2 AS b, @who AS c"},
		"params": map[string]any{"min": 30, "1": "'; DROP TABLE users; --", "2": 2.5, ":who": nil}})
	for _, want := range []string{"alice\ncarol", "'; DROP TABLE users; --  2.5  NULL"} {
		if !strings.Contains(got, want) {
			t.Errorf("params should give %q, got:\n%s", want, got)
		}
	}
	if got := mustSqlite(t, map[string]any{"path": p, "sql": []string{"SELECT count(*) FROM users"}}); !strings.Contains(got, "3") {
		t.Errorf("the table should be untouched: %s", got)
	}
	for _, bad := range []map[string]any{
		{"1": "a", "3": "c"},
		{"x": []int{1}},
		{"0": 1},
	} {
		if _, err := runSqlite(t, NewRecorder(), map[string]any{"path": p, "sql": []string{"SELECT 1"}, "params": bad}); err == nil {
			t.Errorf("params %v should be refused", bad)
		}
	}
}

// Every statement that could write, reach another file or undo the
// read-only open is refused before anything runs, and the file is left
// exactly as it was, with no file created beside it.
func TestSqlite_RefusesWhatIsNotARead(t *testing.T) {
	dir := t.TempDir()
	p := makeSqliteDB(t, dir, "app.db", `CREATE TABLE t (x)`, `INSERT INTO t VALUES (1)`)
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other.db")
	cases := []struct {
		sql  string
		want string
	}{
		{"INSERT INTO t VALUES (2)", "only reads"},
		{"insert into t values (2)", "only reads"},
		{"UPDATE t SET x = 2", "only reads"},
		{"DELETE FROM t", "only reads"},
		{"REPLACE INTO t VALUES (3)", "only reads"},
		{"CREATE TABLE u (y)", "only reads"},
		{"DROP TABLE t", "only reads"},
		{"ATTACH '" + other + "' AS o", "only reads"},
		{"SELECT 1; ATTACH '" + other + "' AS o", "one statement per entry"},
		{"SELECT 1; DELETE FROM t", "one statement per entry"},
		{"WITH c AS (SELECT 1) INSERT INTO t SELECT * FROM c", "INSERT writes"},
		{"WITH c AS (SELECT 1) DELETE FROM t", "DELETE writes"},
		{"VACUUM INTO '" + other + "'", "only reads"},
		{"SELECT 1 FROM t WHERE 0 UNION SELECT 2; VACUUM", "one statement per entry"},
		{"PRAGMA journal_mode=delete", "sets a value"},
		{"PRAGMA journal_mode = DELETE", "sets a value"},
		{"PRAGMA journal_mode(delete)", "sets a value"},
		{"PRAGMA query_only=0", "sets a value"},
		{"PRAGMA query_only(0)", "sets a value"},
		{"PRAGMA main.writable_schema", "only report"},
		{"PRAGMA wal_checkpoint", "only report"},
		{"SELECT load_extension('/tmp/evil')", "loading an extension"},
		{"SELECT writefile('" + other + "', 'x')", "writes a file"},
		{"SELECT * FROM pragma_wal_checkpoint", "only report"},
		{"EXPLAIN DELETE FROM t", "DELETE writes"},
		{"BEGIN", "only reads"},
		{"SELECT 1; COMMIT", "one statement per entry"},
		{"SELECT 'unterminated", "unterminated"},
		{"   ", "empty"},
	}
	for _, tc := range cases {
		_, err := runSqlite(t, NewRecorder(), map[string]any{"path": p, "sql": []string{tc.sql}})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err = %v, want it refused with %q", tc.sql, err, tc.want)
		}
	}
	// A refused statement among several refuses the call before the reads
	// beside it run.
	_, err = runSqlite(t, NewRecorder(), map[string]any{"path": p, "sql": []string{"SELECT 1", "DELETE FROM t"}})
	if err == nil || !strings.Contains(err.Error(), "statement 2 refused") {
		t.Errorf("a refused second statement: err = %v", err)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the database changed under refused statements")
	}
	if _, err := os.Stat(other); err == nil {
		t.Error("a refused statement created a file")
	}
}

// Words inside strings, quoted names and comments are text, and the reading
// forms of the refused words still read.
func TestSqlite_ReadsWhatOnlyLooksLikeAWrite(t *testing.T) {
	p := makeSqliteDB(t, t.TempDir(), "app.db", `CREATE TABLE "update" ("delete" TEXT)`, `INSERT INTO "update" VALUES ('drop table x; insert')`)
	for _, q := range []string{
		`SELECT "delete" FROM "update"`,
		`SELECT replace("delete", 'drop', 'keep') FROM [update]`,
		`SELECT 'INSERT INTO t' AS s -- DELETE FROM t`,
		`/* ATTACH */ SELECT CASE WHEN 1 THEN 'a' ELSE 'b' END AS c`,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 3) SELECT i FROM n`,
		`VALUES (1, x'00ff')`,
		`EXPLAIN QUERY PLAN SELECT * FROM "update"`,
		`PRAGMA table_info("update")`,
		`PRAGMA main.table_info('update')`,
		`PRAGMA journal_mode`,
		`PRAGMA integrity_check(5)`,
		`SELECT name FROM pragma_table_info('update')`,
		`SELECT 1;`,
	} {
		if _, err := runSqlite(t, NewRecorder(), map[string]any{"path": p, "sql": []string{q}}); err != nil {
			t.Errorf("%q should read: %v", q, err)
		}
	}
}

// The file is read-only because the engine opened it so, not because shhh
// read the SQL: a write sent straight to the connection, past the statement
// check, is refused by SQLite itself, and so is an ATTACH.
func TestSqlite_TheEngineRefusesAWriteOnItsOwn(t *testing.T) {
	dir := t.TempDir()
	p := makeSqliteDB(t, dir, "app.db", `CREATE TABLE t (x)`)
	s, err := openSqlite(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	other := filepath.Join(dir, "other.db")
	for _, q := range []string{
		"INSERT INTO t VALUES (1)",
		"CREATE TEMP TABLE z (y)",
		"PRAGMA query_only = 0",
		"ATTACH '" + other + "' AS o",
		"VACUUM INTO '" + other + "'",
	} {
		if _, err := s.conn.ExecContext(context.Background(), q); err == nil && !strings.HasPrefix(q, "PRAGMA") {
			t.Errorf("the engine ran %q", q)
		}
	}
	if _, err := os.Stat(other); err == nil {
		t.Error("the engine created a file for an attach")
	}
}

// Nothing that is not a database is opened, and nothing is created where no
// file is.
func TestSqlite_RefusesWhatIsNotADatabase(t *testing.T) {
	dir := t.TempDir()
	text := filepath.Join(dir, "notes.db")
	if err := os.WriteFile(text, []byte("not a database at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "gone.db")
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{}, "path is required"},
		{map[string]any{"path": text}, "not a SQLite database"},
		{map[string]any{"path": missing}, "cannot read database"},
		{map[string]any{"path": dir}, "is a directory"},
		{map[string]any{"path": text, "sql": 7}, "invalid sql"},
	} {
		if _, err := runSqlite(t, NewRecorder(), tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want %q", tc.args, err, tc.want)
		}
	}
	if _, err := os.Stat(missing); err == nil {
		t.Error("reading a missing database created it")
	}
}

// A file name carrying URI punctuation is the file's name and nothing else.
func TestSqlite_APathIsNeverAURIParameter(t *testing.T) {
	dir := t.TempDir()
	plain := makeSqliteDB(t, dir, "plain.db", `CREATE TABLE t (x)`, `INSERT INTO t VALUES (7)`)
	p := filepath.Join(dir, "odd name?mode=rwc#x.db")
	if err := os.Rename(plain, p); err != nil {
		t.Fatal(err)
	}
	if got := mustSqlite(t, map[string]any{"path": p, "sql": []string{"SELECT x FROM t"}}); got != "x\n-\n7" {
		t.Errorf("got %q", got)
	}
}

// Rows past the limit are counted and kept, not shown; cells are one line,
// bounded, with NULL and a BLOB's size named.
func TestSqlite_RowsAreBoundedAndCellsShown(t *testing.T) {
	dir := t.TempDir()
	p := makeSqliteDB(t, dir, "rows.db",
		`CREATE TABLE t (n INTEGER, s TEXT, b BLOB)`,
		`WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM c WHERE i < 250) INSERT INTO t SELECT i, 'row ' || i, NULL FROM c`,
		`UPDATE t SET s = 'line one'||char(10)||'line two', b = zeroblob(2048) WHERE n = 1`,
		`CREATE TABLE long (s TEXT)`,
		`INSERT INTO long VALUES (printf('%.500c', 'x'))`,
	)
	long := mustSqlite(t, map[string]any{"path": p, "sql": []string{"SELECT s FROM long"}})
	if strings.Contains(long, strings.Repeat("x", MaxSqliteCellRunes+1)) || !strings.Contains(long, strings.Repeat("x", MaxSqliteCellRunes)+"…") {
		t.Errorf("a long cell should be cut at %d characters with a mark: %q", MaxSqliteCellRunes, long)
	}
	var kept string
	r := NewRecorder()
	r.UseEvidence(func(tool, content string) (string, bool) {
		if tool != SqliteName {
			t.Errorf("kept under %q", tool)
		}
		kept = content
		return "ev-rows", true
	})
	got, err := runSqlite(t, r, map[string]any{"path": p, "sql": []string{"SELECT n, s, b FROM t ORDER BY n"}})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 2+MaxSqliteRows+2 {
		t.Errorf("want the header, %d rows and the notices; got %d lines", MaxSqliteRows, len(lines))
	}
	for _, want := range []string{
		`line one\nline two`, "[blob 2 KB]", "NULL", "… 200 of 250 rows shown",
		"full output stored as evidence ev-rows",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("answer should include %q:\n%s", want, got[len(got)-min(len(got), 400):])
		}
	}
	if !strings.Contains(kept, "row 250") || strings.Count(kept, "\n") < 250 {
		t.Errorf("the kept answer should hold every row, got %d lines", strings.Count(kept, "\n"))
	}

	// limit raises the rows shown, up to the ceiling.
	got = mustSqlite(t, map[string]any{"path": p, "sql": []string{"SELECT n FROM t"}, "limit": 250})
	if strings.Contains(got, "truncated") || !strings.Contains(got, "\n250") {
		t.Errorf("limit 250 should show every row:\n%s", got[len(got)-min(len(got), 200):])
	}
	got = mustSqlite(t, map[string]any{"path": p, "sql": []string{"WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM c WHERE i < 1500) SELECT i FROM c"}, "limit": 5000})
	if !strings.Contains(got, fmt.Sprintf("… %s of 1,500 rows shown", sqliteCount(MaxSqliteRowsCeiling))) {
		t.Errorf("limit should stop at the ceiling:\n%s", got[len(got)-min(len(got), 200):])
	}
	// No evidence store: the notice says to narrow it instead.
	if !strings.Contains(got, "narrow it with WHERE or LIMIT") {
		t.Errorf("a cut answer with nowhere to keep it should say to narrow it:\n%s", got[len(got)-min(len(got), 200):])
	}
}

// The byte bound cuts before the row limit when rows are wide.
func TestSqlite_TheByteBoundCutsWideRows(t *testing.T) {
	p := makeSqliteDB(t, t.TempDir(), "wide.db",
		`CREATE TABLE t (a TEXT, b TEXT, c TEXT)`,
		`WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM c WHERE i < 150) INSERT INTO t SELECT printf('%.190c', 'a'), printf('%.190c', 'b'), printf('%.190c', 'c') FROM c`,
	)
	got := mustSqlite(t, map[string]any{"path": p, "sql": []string{"SELECT * FROM t"}})
	if len(got) > MaxSqliteOutputBytes+200 {
		t.Errorf("answer is %d bytes, past the %d-byte bound", len(got), MaxSqliteOutputBytes)
	}
	if !strings.Contains(got, "of 150 rows shown") {
		t.Errorf("a byte-cut answer should state the total:\n%s", got[len(got)-min(len(got), 300):])
	}
}

// A statement that never finishes is interrupted — while its first row is
// being found, and also after it has returned rows, which the driver's own
// interrupt does not reach.
func TestSqlite_ATimeoutInterruptsTheStatement(t *testing.T) {
	old := sqliteTimeout
	sqliteTimeout = 200 * time.Millisecond
	t.Cleanup(func() { sqliteTimeout = old })
	p := makeSqliteDB(t, t.TempDir(), "a.db", `CREATE TABLE t (x)`)
	const forever = `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c) `
	for _, q := range []string{
		forever + `SELECT count(*) FROM c`,
		forever + `SELECT x FROM c WHERE x = 1 OR x < 0`,
	} {
		start := time.Now()
		_, err := runSqlite(t, NewRecorder(), map[string]any{"path": p, "sql": []string{q}})
		if err == nil || !strings.Contains(err.Error(), "stopped after") {
			t.Errorf("%q: err = %v", q, err)
		}
		if el := time.Since(start); el > 5*time.Second {
			t.Errorf("%q took %s to stop", q, el)
		}
	}
	// Rows past the limit are counted; a count the limit stops still shows
	// the rows it has, and says the total is a floor.
	got := mustSqlite(t, map[string]any{"path": p, "sql": []string{forever + `SELECT x FROM c WHERE x <= 300 OR x < 0`}})
	if !strings.Contains(got, "… 200 of at least 300 rows — counting stopped at the time limit shown") {
		t.Errorf("a stopped count should be stated as a floor:\n%s", got[len(got)-min(len(got), 300):])
	}
	// Among several statements, the one stopped says so and the rest say
	// they did not run.
	got = mustSqlite(t, map[string]any{"path": p, "sql": []string{"SELECT 1 AS one", forever + `SELECT count(*) FROM c`, "SELECT 2"}})
	if !strings.Contains(got, "one\n---\n1") || !strings.Contains(got, "error: stopped after") || !strings.Contains(got, "not run") {
		t.Errorf("several statements with one stopped:\n%s", got)
	}
}

func TestSqlite_IsAReadThatBoundsItself(t *testing.T) {
	if !SelfBounding(SqliteName) {
		t.Error("sqlite bounds its own answer and should say so")
	}
	found := false
	for _, d := range NewRecorder().ReadOnly() {
		if d.Tool.Name == SqliteName {
			found = true
		}
	}
	if !found {
		t.Error("sqlite should be one of the read-only definitions")
	}
	if IsMutating(SqliteName) {
		t.Error("sqlite must not be a mutating tool")
	}
}

// SQLite reads $name(…) as one variable, up to the first space or ), so a '
// inside it opens no string. A check that took it for a string let the text
// after it hide from the keyword scan while the engine ran it as the next
// statement.
func TestSqlite_AVariableCannotHideAStatement(t *testing.T) {
	for _, stmt := range []string{
		"SELECT 1 WHERE 0 AND $a(') ; VACUUM INTO '/x' ; SELECT 'x",
		"SELECT 1 WHERE 0 AND :a(') ; ATTACH '/x' AS y ; SELECT 'x",
		"SELECT 1 WHERE 0 AND @a::b(') ; DROP TABLE t ; SELECT 'x",
		`SELECT "load_extension"('x')`,
		`SELECT [readfile]('/etc/passwd')`,
		`SELECT * FROM "pragma_wal_checkpoint"`,
	} {
		if err := checkReadOnlySQL(stmt); err == nil {
			t.Errorf("%q should be refused", stmt)
		}
	}
	for _, stmt := range []string{`SELECT $a(x) FROM t`, `SELECT :a, @b, ?1, $c::d`, `SELECT * FROM "pragma_table_info"('t')`} {
		if err := checkReadOnlySQL(stmt); err != nil {
			t.Errorf("%q should be answered: %v", stmt, err)
		}
	}
}

// A row too wide for the byte bound leaves the header and its rule, not the
// rows that did not fit.
func TestSqlite_TheByteBoundHoldsWhenNoRowFits(t *testing.T) {
	p := makeSqliteDB(t, t.TempDir(), "a.db", `CREATE TABLE t(a)`)
	var cols []string
	for i := 0; i < 60; i++ {
		cols = append(cols, fmt.Sprintf("printf('%%0200d', %d) AS c%d", i, i))
	}
	q := "SELECT " + strings.Join(cols, ",") + " UNION ALL SELECT " + strings.Join(cols, ",")
	got := mustSqlite(t, map[string]any{"path": p, "sql": []string{q}})
	if len(got) > MaxSqliteOutputBytes+512 {
		t.Fatalf("answer is %d bytes, past the %d bound", len(got), MaxSqliteOutputBytes)
	}
	if !strings.Contains(got, "0 of 2 rows shown") {
		t.Errorf("the notice should state the rows shown, got tail %q", got[max(0, len(got)-200):])
	}
}

// randomblob(1e9) allocates and fills a gigabyte in one step the interrupt
// cannot reach, far past the time limit.
func TestSqlite_AnOverlongValueIsRefusedNotBuilt(t *testing.T) {
	p := makeSqliteDB(t, t.TempDir(), "a.db", `CREATE TABLE t(a)`)
	start := time.Now()
	_, err := runSqlite(t, NewRecorder(), map[string]any{"path": p, "sql": []string{`SELECT randomblob(1000000000)`}})
	if err == nil || !strings.Contains(err.Error(), "too big") {
		t.Fatalf("want a too-big refusal, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("refusal took %s", time.Since(start))
	}
}
