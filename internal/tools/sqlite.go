package tools

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"modernc.org/libc"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/provider"
)

// SqliteName is the SQLite reader. It is a constant beside the other
// read-only names for their reason: the reduction pipeline asks by name which
// results are already bounded.
const SqliteName = "sqlite"

// SqliteTimeout bounds one call: opening the file, every statement it names
// and every row it counts. A recursive CTE can be written to run for ever,
// and a read that auto-runs must still come back.
const SqliteTimeout = 10 * time.Second

// sqliteMaxValueBytes is the longest string or BLOB the engine builds or
// reads: SQLITE_LIMIT_LENGTH, which is a gigabyte unless set.
const sqliteMaxValueBytes = 16 << 20

// sqliteTimeout is SqliteTimeout, a variable so a test can wait less for it.
var sqliteTimeout = SqliteTimeout

// sqliteRowHook, when set, runs once per statement after its first row is
// read — while the statement, and so the read, is still open. It is how a
// test holds a read open against a writer; nothing else sets it.
var sqliteRowHook func()

// What the sqlite tool is for, and why it answers the way it does.
//
// A session asked about an app's database, a test fixture or shhh's own
// record used to read it with the sqlite3 command, which costs an approval
// or a classifier round per read and works only where the host installed the
// program. This answers the same questions in-process, at the read tier.
//
// It is a read because the engine says so, not because shhh read the SQL:
// the file is opened in SQLite's own read-only mode with query_only set, so
// a write that got past everything else is refused by the engine. The
// statement check in front of it is for what that mode does not cover —
// ATTACH opens a second file by path, around every check the call's own path
// went through, and a pragma that sets a value is how query_only itself
// would be turned off.
//
// Every default is the idiom's, because a built-in that takes more calls,
// more bytes or more arguments than the shell line it replaces is one the
// model walks past: no sql answers the schema with row counts, which is
// .tables, .schema and count(*) as three commands; several statements are
// one call; the rows come back as the aligned table sqlite3 -column prints.
// See docs/capabilities/coding-agent.md#structured-files-are-read-in-one-call
// and docs/capabilities/approvals-and-safety.md#a-closed-verb-set-is-what-makes-a-read-a-read.
var sqliteTool = Definition{
	Tool: provider.Tool{
		Name: SqliteName,
		Description: "Read a SQLite database file — an app's database, a test fixture, shhh's own session record — in-process and read-only. " +
			"The file is opened in SQLite's read-only mode, and a statement that writes, attaches another file, loads an extension or sets a pragma is refused before it runs. " +
			"On a database you have not seen, call it with no sql first: it answers the schema — every table and view with its columns and types, its indexes and its row count — in one call. " +
			"Then pass sql: several statements go in one call as an array, each result labelled, so a reading that needs four queries is one round. " +
			"Bind values with params rather than writing them into the SQL. " +
			"Rows come back as an aligned table: at most 200 rows per statement (limit raises it), long cells cut with …, a BLOB shown as its size, NULL as NULL. " +
			"Output stops at 32 KB; a cut result states how many rows there were and names the evidence id that holds the rest.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "The SQLite database file, relative to the working directory or absolute. It is opened read-only, and nothing is created where no file exists"},
				"sql": {"type": "array", "items": {"type": "string", "description": "One SQL statement that reads: SELECT, WITH, VALUES, EXPLAIN, or a PRAGMA that only reports, such as table_info(name)"}, "description": "The statements to answer, one per entry, all in this one call. Leave it out to get the schema with row counts instead — the cheapest first call on a database you have not seen"},
				"params": {"type": "object", "description": "Values bound to placeholders, so nothing is spliced into the SQL: {\"id\": 42} binds :id, @id or $id, and a numeric key binds a positional ? by its place, {\"1\": \"alice\"}. Every statement takes the placeholders it names"},
				"limit": {"type": "integer", "description": "Rows shown per statement, 200 when left out and at most 1000. Raise it only when the answer needs more rows on screen; a cut result is already kept whole as evidence and its total is stated"}
			},
			"required": ["path"]
		}`),
	},
	// Execute is filled in by ReadOnly, where the session's evidence store
	// is known.
}

type sqliteArgs struct {
	Path   string          `json:"path"`
	SQL    json.RawMessage `json:"sql"`
	Params json.RawMessage `json:"params"`
	Limit  int             `json:"limit"`
}

// sqliteHeader is the first sixteen bytes of every SQLite database file.
var sqliteHeader = []byte("SQLite format 3\x00")

// errSqliteStopped is a statement cut off by the call's time limit.
var errSqliteStopped = errors.New("stopped at the time limit")

func (r *Recorder) executeSqlite(raw json.RawMessage) (string, error) {
	var args sqliteArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("path is required: name the database file")
	}
	stmts, err := sqliteStatements(args.SQL)
	if err != nil {
		return "", err
	}
	// Every statement is judged before any of them runs, so a refusal is
	// never the second half of an answer that already read something.
	for i, s := range stmts {
		if err := checkReadOnlySQL(s); err != nil {
			if len(stmts) > 1 {
				return "", fmt.Errorf("statement %d refused: %w", i+1, err)
			}
			return "", fmt.Errorf("refused: %w", err)
		}
	}
	params, err := sqliteParams(args.Params)
	if err != nil {
		return "", err
	}
	limit := MaxSqliteRows
	if args.Limit > 0 {
		limit = min(args.Limit, MaxSqliteRowsCeiling)
	}
	if err := checkSqliteFile(args.Path); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), sqliteTimeout)
	defer cancel()
	sess, err := openSqlite(ctx, args.Path)
	if err != nil {
		return "", err
	}
	defer sess.close()

	out := &sqliteOutput{}
	if sess.isShhhStore(ctx) {
		out.line(sqliteStoreNote)
	}
	if len(stmts) == 0 {
		if err := sess.schema(ctx, out); err != nil {
			return "", err
		}
		return out.render(r.evidenceKeep()), nil
	}
	labelled := len(stmts) > 1
	for i, s := range stmts {
		if labelled {
			if i > 0 {
				out.line("")
			}
			out.line(fmt.Sprintf("==> %d: %s <==", i+1, sqliteLabel(s)))
		}
		if ctx.Err() != nil {
			out.line(fmt.Sprintf("not run: the call's %s were spent", sqliteTimeout))
			continue
		}
		err := sess.run(ctx, out, s, params, limit)
		if err == nil {
			continue
		}
		if errors.Is(err, errSqliteStopped) {
			err = fmt.Errorf("stopped after %s; narrow it with WHERE or LIMIT, or ask for count(*) first", sqliteTimeout)
		}
		if !labelled {
			return "", err
		}
		// One failing statement among several is a line under its own
		// label, not the loss of every other statement's answer.
		out.line("error: " + err.Error())
	}
	return out.render(r.evidenceKeep()), nil
}

// sqliteStatements reads the sql argument: an array of statements, or one
// statement as a bare string, which is what a model asking one question
// often sends whatever the schema says.
func sqliteStatements(raw json.RawMessage) ([]string, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil, nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		var one string
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, fmt.Errorf("invalid sql: pass an array of statements")
		}
		list = []string{one}
	}
	var out []string
	for i, s := range list {
		if strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("sql entry %d is empty", i+1)
		}
		out = append(out, s)
	}
	if len(out) > MaxSqliteStatements {
		return nil, fmt.Errorf("more than %d statements in one call; ask fewer", MaxSqliteStatements)
	}
	return out, nil
}

// sqliteParams turns the params object into the arguments every statement is
// bound with. A numeric key is a positional placeholder's place and stands
// at that position in the list; every other key is a name, with any :, @ or
// $ it was written with dropped, since database/sql wants the bare name. The
// driver binds each statement's placeholders from this list by name or by
// place and ignores the rest, which is what lets one set serve several
// statements.
func sqliteParams(raw json.RawMessage) ([]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("invalid params: pass an object of placeholder names to values: %w", err)
	}
	positional := map[int]any{}
	maxPos := 0
	var named []string
	for k := range m {
		if n, err := strconv.Atoi(k); err == nil {
			if n < 1 || n > 999 {
				return nil, fmt.Errorf("params %q: a positional key counts from 1", k)
			}
			positional[n] = nil
			maxPos = max(maxPos, n)
			continue
		}
		named = append(named, k)
	}
	sort.Strings(named)
	out := make([]any, maxPos, maxPos+len(named))
	for k, v := range m {
		n, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		val, err := sqliteValue(k, v)
		if err != nil {
			return nil, err
		}
		out[n-1] = val
	}
	for i := 1; i <= maxPos; i++ {
		if _, ok := positional[i]; !ok {
			return nil, fmt.Errorf("params has %d but no %d; positional keys count 1, 2, 3 with no gap", maxPos, i)
		}
	}
	for _, k := range named {
		val, err := sqliteValue(k, m[k])
		if err != nil {
			return nil, err
		}
		name := strings.TrimLeft(k, ":@$")
		// database/sql takes a name only when it starts with a letter.
		if r, _ := utf8.DecodeRuneInString(name); !unicode.IsLetter(r) {
			return nil, fmt.Errorf("params %q: a name starts with a letter, or is a number for a positional ?", k)
		}
		out = append(out, sql.Named(name, val))
	}
	return out, nil
}

// sqliteValue is one bound value: a JSON scalar as the SQLite value it is.
func sqliteValue(key string, v any) (any, error) {
	switch x := v.(type) {
	case nil, string, bool:
		return x, nil
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n, nil
		}
		f, err := x.Float64()
		if err != nil {
			return nil, fmt.Errorf("params %q: %w", key, err)
		}
		return f, nil
	}
	return nil, fmt.Errorf("params %q: bind a string, a number, a boolean or null", key)
}

// checkSqliteFile refuses what is not a SQLite database before the engine is
// asked to open it, so the answer says what the file is rather than what the
// engine made of it.
func checkSqliteFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot read database: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory; name the database file in it", path)
	}
	// A pipe or a device would block the open, and nothing bounds that wait.
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file; name the database file", path)
	}
	if info.Size() == 0 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot read database: %w", err)
	}
	defer f.Close()
	head := make([]byte, len(sqliteHeader))
	if _, err := io.ReadFull(f, head); err != nil || !bytes.Equal(head, sqliteHeader) {
		return fmt.Errorf("%s is not a SQLite database: it does not start with SQLite's file header", path)
	}
	return nil
}

// sqliteDSN is the URI the file is opened by. The path is escaped so that a
// ? or # in a file name stays part of the name rather than becoming a URI
// parameter of the model's choosing. mode=ro is SQLite's own read-only open
// and creates nothing; immutable=0 keeps the locking a reader needs to see a
// database another process is writing. query_only refuses any write to any
// database on the connection; trusted_schema off refuses the functions a
// hostile file's views and triggers could otherwise call; the busy timeout
// is the wait for a writer mid-checkpoint rather than an error.
func sqliteDSN(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("cannot resolve %s: %w", path, err)
	}
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	return u.String() + "?mode=ro&immutable=0" +
		"&_pragma=busy_timeout(2000)&_pragma=query_only(1)&_pragma=trusted_schema(0)", nil
}

// sqliteSession is one call's connection to the file.
type sqliteSession struct {
	db   *sql.DB
	conn *sql.Conn
	stop chan struct{}
	done sync.WaitGroup
}

// openSqlite opens path read-only on one connection and arms the time limit.
//
// The limit is the part the driver does not give. It interrupts a statement
// only while its first row is being found; a statement that has returned a
// row and then spends minutes finding the next is past its reach, and so is
// every row counted after the shown ones. So the engine's own interrupt is
// called on this connection's handle when the deadline passes. The handle is
// read out of the driver's connection because the driver does not export
// it; where it cannot be read the call is refused rather than run with a
// limit that does not hold.
func openSqlite(ctx context.Context, path string) (*sqliteSession, error) {
	dsn, err := sqliteDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("cannot open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("cannot open database: %w", err)
	}
	var handle uintptr
	_ = conn.Raw(func(dc any) error {
		handle = sqliteHandle(dc)
		if handle != 0 {
			// No second database may be attached, so ATTACH is refused by
			// the engine as well as by the statement check. Without this
			// an ATTACH creates the file it names even on a read-only
			// connection, since the main file's read-only mode is not
			// the attached one's.
			tls := libc.NewTLS()
			sqlite3.Xsqlite3_limit(tls, handle, sqlite3.SQLITE_LIMIT_ATTACHED, 0)
			// A function such as randomblob(1e9) is one step of the engine
			// that the interrupt cannot cut short: it allocates and fills a
			// gigabyte first. A bound on a value's length keeps that step
			// small enough to finish inside the time limit.
			sqlite3.Xsqlite3_limit(tls, handle, sqlite3.SQLITE_LIMIT_LENGTH, sqliteMaxValueBytes)
			tls.Close()
		}
		return nil
	})
	s := &sqliteSession{db: db, conn: conn, stop: make(chan struct{})}
	if handle == 0 {
		s.close()
		return nil, fmt.Errorf("cannot bound a query on this SQLite driver, so none is run")
	}
	s.done.Add(1)
	go func() {
		defer s.done.Done()
		select {
		case <-ctx.Done():
			tls := libc.NewTLS()
			sqlite3.Xsqlite3_interrupt(tls, handle)
			tls.Close()
		case <-s.stop:
		}
	}()

	// The read-only guarantee is checked, not assumed: a driver that
	// dropped the pragma would otherwise leave the statement check as the
	// only thing between the model and a write.
	var qo int
	if err := conn.QueryRowContext(ctx, "PRAGMA query_only").Scan(&qo); err != nil {
		s.close()
		return nil, fmt.Errorf("cannot open database: %w", err)
	}
	if qo != 1 {
		s.close()
		return nil, fmt.Errorf("cannot open database read-only, so it is not opened")
	}
	return s, nil
}

// sqliteHandle is the engine's connection handle inside the driver's
// connection, or 0 where the driver is not the one this was written against.
func sqliteHandle(dc any) uintptr {
	v := reflect.ValueOf(dc)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return 0
	}
	f := v.Elem().FieldByName("db")
	if !f.IsValid() || f.Kind() != reflect.Uintptr {
		return 0
	}
	return uintptr(f.Uint())
}

// close stops the interrupt before the connection goes, so it can never
// reach a handle that has been freed.
func (s *sqliteSession) close() {
	close(s.stop)
	s.done.Wait()
	s.conn.Close()
	s.db.Close()
}

// sqliteStoreNote is the line a result on shhh's own store carries: the
// dashboard answers most questions asked of the record in one command.
const sqliteStoreNote = "This is shhh's own session record; `shhh observe` draws its dashboard."

// isShhhStore reports whether the file holds shhh's session record. It is
// read off the schema rather than the path, so it holds wherever the store
// lives and for a copy of it.
func (s *sqliteSession) isShhhStore(ctx context.Context) bool {
	var n int
	err := s.conn.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name IN ('agent_sessions', 'agent_events')`).Scan(&n)
	return err == nil && n == 2
}

// run answers one statement into out.
func (s *sqliteSession) run(ctx context.Context, out *sqliteOutput, stmt string, params []any, limit int) error {
	rows, err := s.conn.QueryContext(ctx, stmt, params...)
	if err != nil {
		if ctx.Err() != nil {
			return errSqliteStopped
		}
		return sqliteError(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return sqliteError(err)
	}
	var (
		shown    [][]string
		kept     [][]string
		keptSize int
		keptCut  bool
		total    int
	)
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return sqliteError(err)
		}
		total++
		cells := make([]string, len(vals))
		size := 0
		for i, v := range vals {
			cells[i] = sqliteCell(v)
			size += len(cells[i]) + 2
		}
		if total <= limit {
			shown = append(shown, cells)
		}
		if !keptCut && keptSize+size <= MaxSqliteKeptBytes {
			kept = append(kept, cells)
			keptSize += size
		} else {
			keptCut = true
		}
		if total == 1 && sqliteRowHook != nil {
			sqliteRowHook()
		}
	}
	counted := true
	if err := rows.Err(); err != nil {
		if ctx.Err() == nil {
			return sqliteError(err)
		}
		// The rows past the ones shown are counted, not shown; a count the
		// time limit stopped still leaves a whole answer to show.
		if total <= limit {
			return errSqliteStopped
		}
		counted = false
	}
	out.table(cols, shown, kept, total, counted, keptCut)
	return nil
}

// sqliteError is the engine's refusal in its own words, without the driver's
// wrapping around them.
func sqliteError(err error) error {
	msg := err.Error()
	msg = strings.TrimPrefix(msg, "SQL logic error: ")
	return errors.New(msg)
}

// sqliteCell is one value as a cell of the table: NULL named so it is not an
// empty string, a BLOB as its size, text on one line and cut at a bound.
func sqliteCell(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return "[blob " + attachment.HumanSize(len(x)) + "]"
	case string:
		return cutCell(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		if x {
			return "1"
		}
		return "0"
	case time.Time:
		return x.Format(time.RFC3339Nano)
	}
	return cutCell(fmt.Sprint(v))
}

// cellEscapes keeps a cell on one line: the table is read by column, and a
// newline inside a cell would start a row that is not one.
var cellEscapes = strings.NewReplacer("\n", `\n`, "\r", `\r`, "\t", `\t`)

func cutCell(s string) string {
	s = cellEscapes.Replace(s)
	if utf8.RuneCountInString(s) <= MaxSqliteCellRunes {
		return s
	}
	n := 0
	for i := range s {
		if n == MaxSqliteCellRunes {
			return s[:i] + "…"
		}
		n++
	}
	return s
}

// sqliteLabel is a statement as its result's label: one line, bounded.
func sqliteLabel(stmt string) string {
	s := strings.Join(strings.Fields(stmt), " ")
	if utf8.RuneCountInString(s) > 80 {
		n := 0
		for i := range s {
			if n == 79 {
				return s[:i] + "…"
			}
			n++
		}
	}
	return s
}

// renderSqliteTable is rows aligned under their column names, the way
// sqlite3 -column prints them.
func renderSqliteTable(cols []string, rows [][]string) []string {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = utf8.RuneCountInString(c)
	}
	for _, r := range rows {
		for i, c := range r {
			widths[i] = max(widths[i], utf8.RuneCountInString(c))
		}
	}
	line := func(cells []string) string {
		var b strings.Builder
		for i, c := range cells {
			if i > 0 {
				b.WriteString("  ")
			}
			b.WriteString(c)
			if i < len(cells)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c)))
			}
		}
		return strings.TrimRight(b.String(), " ")
	}
	rules := make([]string, len(cols))
	for i := range cols {
		rules[i] = strings.Repeat("-", max(widths[i], 1))
	}
	out := []string{line(cols), line(rules)}
	for _, r := range rows {
		out = append(out, line(r))
	}
	return out
}

// sqliteOutput collects an answer under the tool's byte bound, and beside it
// the whole answer as far as it can be kept, so a cut answer can say where
// the rest went.
type sqliteOutput struct {
	shown strings.Builder
	full  strings.Builder
	// cut is set once anything was left out of what is shown: rows past
	// the limit, or the byte bound reached.
	cut bool
	// full is past the store's bound, so what is kept is a head too.
	fullCut bool
}

// line adds a line that is not a table: a label, a note, the schema.
func (o *sqliteOutput) line(text string) {
	o.keep(text)
	if o.shown.Len()+len(text)+1 > MaxSqliteOutputBytes {
		o.cut = true
		return
	}
	o.shown.WriteString(text)
	o.shown.WriteByte('\n')
}

func (o *sqliteOutput) keep(text string) {
	if o.full.Len()+len(text)+1 > MaxSqliteKeptBytes {
		o.fullCut = true
		return
	}
	o.full.WriteString(text)
	o.full.WriteByte('\n')
}

// table adds one statement's result. shown is the rows under the row limit;
// kept is every row the store's bound held; total is how many there were,
// or how many were counted before the time limit when counted is false.
func (o *sqliteOutput) table(cols []string, shown, kept [][]string, total int, counted, keptCut bool) {
	if len(cols) == 0 {
		o.line("(no rows)")
		return
	}
	for _, l := range renderSqliteTable(cols, kept) {
		o.keep(l)
	}
	if keptCut {
		o.fullCut = true
	}
	lines := renderSqliteTable(cols, shown)
	room := MaxSqliteOutputBytes - o.shown.Len()
	n := 0
	used := 0
	for _, l := range lines {
		if used+len(l)+1 > room {
			break
		}
		used += len(l) + 1
		n++
	}
	// Two lines are the header and its rule; everything after is a row.
	fitRows := max(n-2, 0)
	if fitRows < len(shown) {
		shown = shown[:fitRows]
		// Re-rendered even with no row left: the header and its rule are
		// then all that is drawn, not the lines that did not fit.
		lines = renderSqliteTable(cols, shown)
	}
	if n >= 2 {
		for _, l := range lines {
			o.shown.WriteString(l)
			o.shown.WriteByte('\n')
		}
	}
	if total == 0 {
		o.line("(no rows)")
		return
	}
	if len(shown) == total && counted {
		return
	}
	o.cut = true
	of := sqliteCount(total) + " rows"
	if !counted {
		of = "at least " + of + " — counting stopped at the time limit"
	}
	note := fmt.Sprintf("… %s of %s shown", sqliteCount(len(shown)), of)
	if o.shown.Len()+len(note)+1 <= MaxSqliteOutputBytes {
		o.shown.WriteString(note)
		o.shown.WriteByte('\n')
	}
}

// sqliteCount is a row count as a reader reads it, with thousands grouped.
func sqliteCount(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// render is the answer the model reads, with the notice a cut one carries.
func (o *sqliteOutput) render(keep ExecKeep) string {
	body := strings.TrimRight(o.shown.String(), "\n")
	if !o.cut {
		return body
	}
	where := "narrow it with WHERE or LIMIT, or ask for count(*) first"
	if keep != nil {
		if id, ok := keep(SqliteName, o.full.String()); ok {
			held := "the whole answer"
			if o.fullCut {
				held = "the answer up to " + attachment.HumanSize(MaxSqliteKeptBytes)
			}
			where = "full output stored as evidence " + id + " (" + held + ") — retrieve it with the evidence tool (info/read/search), or narrow the query"
		}
	}
	return body + "\n… (truncated; " + where + ")"
}

// schema answers a call with no sql: every table and view in the main
// database with its columns, indexes and row count. The tables SQLite keeps
// behind a virtual table (an FTS index's shadow tables) are counted rather
// than listed: they are the index's storage, and listing them would bury the
// tables someone wrote.
func (s *sqliteSession) schema(ctx context.Context, out *sqliteOutput) error {
	type object struct {
		name, kind string
		cols       []string
		indexes    []string
	}
	rows, err := s.conn.QueryContext(ctx,
		`SELECT name, type FROM pragma_table_list WHERE schema = 'main' AND name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY type = 'view', name`)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("stopped after %s reading the schema", sqliteTimeout)
		}
		return sqliteError(err)
	}
	var objs []object
	shadows := 0
	for rows.Next() {
		var o object
		if err := rows.Scan(&o.name, &o.kind); err != nil {
			rows.Close()
			return sqliteError(err)
		}
		if o.kind == "shadow" {
			shadows++
			continue
		}
		objs = append(objs, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return sqliteError(err)
	}
	if len(objs) == 0 {
		out.line("empty: no tables or views")
		return nil
	}
	for i := range objs {
		o := &objs[i]
		o.cols = s.columns(ctx, o.name)
		if o.kind != "view" {
			o.indexes = s.indexes(ctx, o.name)
		}
	}
	for i, o := range objs {
		if i > 0 {
			out.line("")
		}
		head := o.kind + " " + o.name
		switch {
		case o.kind == "view":
			head += " — rows not counted: counting runs the view's query"
		case ctx.Err() != nil:
			head += " — rows not counted: the time limit was spent"
		default:
			var n int64
			if err := s.conn.QueryRowContext(ctx, "SELECT count(*) FROM main."+quoteIdent(o.name)).Scan(&n); err != nil {
				head += " — rows not counted: " + sqliteError(err).Error()
			} else {
				head += " — " + sqliteCount(int(n)) + " rows"
			}
		}
		out.line(head)
		for _, c := range o.cols {
			out.line("  " + c)
		}
		for _, ix := range o.indexes {
			out.line("  " + ix)
		}
	}
	if shadows > 0 {
		out.line("")
		out.line(plural(shadows, "%d table behind a virtual table is not listed", "%d tables behind virtual tables are not listed"))
	}
	return nil
}

// columns is a table's or view's columns, one line each: name, declared type
// and the constraints that say how to query it.
func (s *sqliteSession) columns(ctx context.Context, table string) []string {
	rows, err := s.conn.QueryContext(ctx, `SELECT name, type, "notnull", pk FROM pragma_table_info(?)`, table)
	if err != nil {
		return []string{"columns not read: " + sqliteError(err).Error()}
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name, typ string
		var notNull, pk int
		if err := rows.Scan(&name, &typ, &notNull, &pk); err != nil {
			return out
		}
		line := name
		if typ != "" {
			line += " " + typ
		}
		if pk > 0 {
			line += " PRIMARY KEY"
		}
		if notNull == 1 {
			line += " NOT NULL"
		}
		out = append(out, line)
	}
	return out
}

// indexes is a table's indexes, one line each, with the columns they cover.
func (s *sqliteSession) indexes(ctx context.Context, table string) []string {
	rows, err := s.conn.QueryContext(ctx, `SELECT name, "unique" FROM pragma_index_list(?) ORDER BY name`, table)
	if err != nil {
		return nil
	}
	type ix struct {
		name   string
		unique bool
	}
	var list []ix
	for rows.Next() {
		var i ix
		var u int
		if err := rows.Scan(&i.name, &u); err != nil {
			break
		}
		i.unique = u == 1
		list = append(list, i)
	}
	rows.Close()
	var out []string
	for _, i := range list {
		var cols []string
		crows, err := s.conn.QueryContext(ctx, `SELECT coalesce(name, '<expr>') FROM pragma_index_info(?) ORDER BY seqno`, i.name)
		if err == nil {
			for crows.Next() {
				var c string
				if crows.Scan(&c) == nil {
					cols = append(cols, c)
				}
			}
			crows.Close()
		}
		line := "index " + i.name
		if i.unique {
			line += " UNIQUE"
		}
		out = append(out, line+" ("+strings.Join(cols, ", ")+")")
	}
	return out
}

// quoteIdent quotes a name for SQL, doubling any quote inside it.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// The statement check.
//
// The engine's read-only open is what refuses a write; this is what refuses
// the rest, and it errs towards refusing. It reads each statement as tokens —
// strings, quoted names and comments are never mistaken for keywords, and a
// keyword inside a string is text — and holds it to four rules: one
// statement per entry, since the driver runs every statement in a string it
// is handed; a first word that reads (SELECT, WITH, VALUES, EXPLAIN, or a
// PRAGMA from the reading list); no word anywhere that writes, attaches or
// opens a transaction, since a WITH can end in an INSERT; and no function
// that reaches outside the file. A column somebody named after one of those
// words is refused until it is written quoted, which is the price of a check
// that cannot be walked past by a word in an unexpected place.

// sqliteFirstWords is what a reading statement may start with.
var sqliteFirstWords = map[string]bool{"SELECT": true, "WITH": true, "VALUES": true, "EXPLAIN": true, "PRAGMA": true}

// sqliteRefusedWords is every keyword that acts, with what it would do. Any
// of them anywhere in a statement refuses it.
var sqliteRefusedWords = map[string]string{
	"INSERT":      "INSERT writes",
	"UPDATE":      "UPDATE writes",
	"DELETE":      "DELETE writes",
	"REPLACE":     "REPLACE writes",
	"UPSERT":      "UPSERT writes",
	"CREATE":      "CREATE writes",
	"DROP":        "DROP writes",
	"ALTER":       "ALTER writes",
	"VACUUM":      "VACUUM writes, and VACUUM INTO writes a new file by path",
	"REINDEX":     "REINDEX writes",
	"ANALYZE":     "ANALYZE writes",
	"ATTACH":      "ATTACH opens another file by path, around the checks this call's own path went through",
	"DETACH":      "DETACH changes which files the connection has open",
	"BEGIN":       "a transaction is not a read",
	"COMMIT":      "a transaction is not a read",
	"END":         "a transaction is not a read",
	"ROLLBACK":    "a transaction is not a read",
	"SAVEPOINT":   "a transaction is not a read",
	"RELEASE":     "a transaction is not a read",
	"TRANSACTION": "a transaction is not a read",
	"PRAGMA":      "a PRAGMA is a statement of its own, not part of another",
}

// sqliteRefusedFuncs are functions that reach outside the file: they load
// code, read or write other files, or replace a tokenizer with an address.
var sqliteRefusedFuncs = map[string]string{
	"load_extension": "loading an extension runs a library's code",
	"readfile":       "readfile reads another file by path",
	"writefile":      "writefile writes a file",
	"edit":           "edit starts an editor",
	"fts3_tokenizer": "fts3_tokenizer can install a tokenizer by address",
}

// sqliteReadPragmas are the pragmas that only report, asked with no value.
// Anything else — and any pragma given a value — is refused: a pragma that
// sets a value is how query_only itself would be turned off.
var sqliteReadPragmas = map[string]bool{
	"application_id": true, "auto_vacuum": true, "collation_list": true,
	"compile_options": true, "data_version": true, "database_list": true,
	"encoding": true, "foreign_key_check": true, "foreign_key_list": true,
	"foreign_keys": true, "freelist_count": true, "function_list": true,
	"index_info": true, "index_list": true, "index_xinfo": true,
	"integrity_check": true, "journal_mode": true, "module_list": true,
	"page_count": true, "page_size": true, "pragma_list": true,
	"query_only": true, "quick_check": true, "schema_version": true,
	"table_info": true, "table_list": true, "table_xinfo": true,
	"user_version": true,
}

// sqliteArgPragmas are the reading pragmas that take an argument: a table or
// index to report on, or how many problems to list. Every other pragma's
// argument form, PRAGMA journal_mode(delete) included, sets a value.
var sqliteArgPragmas = map[string]bool{
	"foreign_key_check": true, "foreign_key_list": true, "index_info": true,
	"index_list": true, "index_xinfo": true, "integrity_check": true,
	"quick_check": true, "table_info": true, "table_list": true,
	"table_xinfo": true,
}

// sqlToken is one token of a statement. kind is 'w' for a bare word, 'q' for
// a quoted name, 's' for a string or blob literal, 'n' for a number, 'p' for
// a placeholder and 'o' for any other character.
type sqlToken struct {
	kind byte
	text string
}

// sqlWordByte reports whether c continues a bare word: SQLite's identifier
// characters, with every byte of a multi-byte character counted as one.
func sqlWordByte(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c >= utf8.RuneSelf
}

// tokenizeSQL splits a statement the way SQLite's own tokenizer would for
// the purpose of telling keywords from everything else.
func tokenizeSQL(s string) ([]sqlToken, error) {
	var toks []sqlToken
	i := 0
	quoted := func(closing, kind byte, what string) error {
		j := i + 1
		for {
			k := strings.IndexByte(s[j:], closing)
			if k < 0 {
				return fmt.Errorf("unterminated %s", what)
			}
			j += k + 1
			// A doubled closing quote is an escaped one, except for [ ]
			// which has no escape.
			if closing != ']' && j < len(s) && s[j] == closing {
				j++
				continue
			}
			break
		}
		toks = append(toks, sqlToken{kind, s[i:j]})
		i = j
		return nil
	}
	for i < len(s) {
		c := s[i]
		var err error
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			if k := strings.IndexByte(s[i:], '\n'); k >= 0 {
				i += k + 1
			} else {
				i = len(s)
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			if k := strings.Index(s[i+2:], "*/"); k >= 0 {
				i += k + 4
			} else {
				i = len(s)
			}
		case c == '\'':
			err = quoted('\'', 's', "string")
		case c == '"':
			err = quoted('"', 'q', "quoted name")
		case c == '`':
			err = quoted('`', 'q', "quoted name")
		case c == '[':
			err = quoted(']', 'q', "quoted name")
		case c >= '0' && c <= '9' || (c == '.' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9'):
			j := i + 1
			for j < len(s) && (sqlWordByte(s[j]) || s[j] == '.') {
				j++
			}
			toks = append(toks, sqlToken{'n', s[i:j]})
			i = j
		case c == '?':
			// A ? takes digits only, as in SQLite's own tokenizer: a word
			// after it is a word of its own, so it is not swallowed here.
			j := i + 1
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			toks = append(toks, sqlToken{'p', s[i:j]})
			i = j
		case c == ':' || c == '@' || (c == '$' && i+1 < len(s) && sqlWordByte(s[i+1])):
			// SQLite's variable: word bytes, `::` inside, and an optional
			// (suffix) that runs to the first space or ) — a ' inside it is
			// not a string. Reading it any other way lets text after a
			// quote hide from the check while the engine runs it.
			j := i + 1
			n := 0
			closed := true
		scan:
			for j < len(s) {
				switch {
				case sqlWordByte(s[j]):
					n++
					j++
				case s[j] == '(' && n > 0:
					j++
					for j < len(s) && !strings.ContainsRune(" \t\n\r\f\v)", rune(s[j])) {
						j++
					}
					if j < len(s) && s[j] == ')' {
						j++
					} else {
						closed = false
					}
					break scan
				case s[j] == ':' && j+1 < len(s) && s[j+1] == ':':
					j += 2
				default:
					break scan
				}
			}
			if !closed {
				return nil, fmt.Errorf("unterminated parameter name")
			}
			kind := byte('p')
			if n == 0 {
				kind = 'o'
			}
			toks = append(toks, sqlToken{kind, s[i:j]})
			i = j
		case (c == 'x' || c == 'X') && i+1 < len(s) && s[i+1] == '\'':
			// x'…' is a blob literal, not the word x.
			i++
			err = quoted('\'', 's', "blob literal")
		case sqlWordByte(c):
			j := i + 1
			for j < len(s) && sqlWordByte(s[j]) {
				j++
			}
			toks = append(toks, sqlToken{'w', s[i:j]})
			i = j
		default:
			toks = append(toks, sqlToken{'o', string(c)})
			i++
		}
		if err != nil {
			return nil, err
		}
	}
	return toks, nil
}

// checkReadOnlySQL refuses a statement that is not one read of this file,
// with the reason in words.
func checkReadOnlySQL(stmt string) error {
	toks, err := tokenizeSQL(stmt)
	if err != nil {
		return err
	}
	// One statement: a trailing semicolon is allowed, a second statement
	// after one is not.
	for len(toks) > 0 && toks[len(toks)-1].kind == 'o' && toks[len(toks)-1].text == ";" {
		toks = toks[:len(toks)-1]
	}
	if len(toks) == 0 {
		return fmt.Errorf("the statement is empty")
	}
	for _, t := range toks {
		if t.kind == 'o' && t.text == ";" {
			return fmt.Errorf("one statement per entry; pass several as separate entries of sql")
		}
	}
	first := toks[0]
	if first.kind != 'w' || !sqliteFirstWords[strings.ToUpper(first.text)] {
		return fmt.Errorf("only reads are answered: a statement starts with SELECT, WITH, VALUES, EXPLAIN or a PRAGMA that reports")
	}
	if strings.EqualFold(first.text, "PRAGMA") {
		return checkPragma(toks[1:])
	}
	for i, t := range toks {
		if t.kind == 'q' {
			// A quoted name still names a function or a table-valued
			// function: "load_extension"(...) is the same call.
			call := i+1 < len(toks) && toks[i+1].kind == 'o' && toks[i+1].text == "("
			if err := checkSqliteName(unquoteName(t.text), call); err != nil {
				return err
			}
			continue
		}
		if t.kind != 'w' {
			continue
		}
		up := strings.ToUpper(t.text)
		call := i+1 < len(toks) && toks[i+1].kind == 'o' && toks[i+1].text == "("
		// replace(x, y, z) is the string function, not the statement.
		if up == "REPLACE" && call {
			continue
		}
		// CASE … END closes an expression, not a transaction.
		if up == "END" && sqliteInCase(toks[:i]) {
			continue
		}
		if why, ok := sqliteRefusedWords[up]; ok {
			return fmt.Errorf("%s; this tool only reads", why)
		}
		if err := checkSqliteName(t.text, call); err != nil {
			return err
		}
	}
	return nil
}

// checkSqliteName refuses a function that reaches outside the file and a
// pragma table-valued function that does not only report.
func checkSqliteName(name string, call bool) error {
	low := strings.ToLower(name)
	if why, ok := sqliteRefusedFuncs[low]; ok && call {
		return errors.New(why)
	}
	if p, ok := strings.CutPrefix(low, "pragma_"); ok && !sqliteReadPragmas[p] {
		return fmt.Errorf("pragma_%s is not one of the pragmas that only report", p)
	}
	return nil
}

// unquoteName is a quoted name without its quotes, a doubled quote inside
// undoubled.
func unquoteName(q string) string {
	if len(q) < 2 {
		return q
	}
	inner := q[1 : len(q)-1]
	if c := q[0]; c != '[' {
		inner = strings.ReplaceAll(inner, string(c)+string(c), string(c))
	}
	return inner
}

// sqliteInCase reports whether the tokens before an END leave a CASE open,
// which is the one place END is not a transaction's.
func sqliteInCase(before []sqlToken) bool {
	depth := 0
	for _, t := range before {
		if t.kind != 'w' {
			continue
		}
		switch strings.ToUpper(t.text) {
		case "CASE":
			depth++
		case "END":
			depth--
		}
	}
	return depth > 0
}

// checkPragma holds the tokens after PRAGMA to the one shape that reads:
// [schema.]name, or [schema.]name(argument) for a pragma that reports on
// its argument.
func checkPragma(toks []sqlToken) error {
	for _, t := range toks {
		if t.kind == 'o' && t.text == "=" {
			return fmt.Errorf("a PRAGMA that sets a value is refused; ask its value with the PRAGMA's name alone")
		}
	}
	if len(toks) >= 2 && toks[1].kind == 'o' && toks[1].text == "." {
		toks = toks[2:]
	}
	if len(toks) == 0 || toks[0].kind != 'w' {
		return fmt.Errorf("a PRAGMA names the pragma to report")
	}
	name := strings.ToLower(toks[0].text)
	if !sqliteReadPragmas[name] {
		return fmt.Errorf("PRAGMA %s is not one of the pragmas that only report", name)
	}
	rest := toks[1:]
	switch {
	case len(rest) == 0:
		return nil
	case len(rest) == 3 && rest[0].text == "(" && rest[2].text == ")" && rest[1].kind != 'o' && rest[1].kind != 'p':
		if !sqliteArgPragmas[name] {
			return fmt.Errorf("PRAGMA %s(…) sets a value; ask its value with PRAGMA %s alone", name, name)
		}
		return nil
	}
	return fmt.Errorf("a PRAGMA is its name, or its name and one argument in parentheses")
}
