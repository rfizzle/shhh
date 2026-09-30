package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/itchyny/gojq"
	"go.yaml.in/yaml/v4"

	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/provider"
)

// QueryName is the structured-data reader. It is a constant beside the other
// read-only names for their reason: the reduction pipeline asks by name which
// results are already bounded.
const QueryName = "query"

// QueryTimeout bounds one call: decoding every file named and running the
// expression over each input. A jq expression can be written to run for
// ever (`repeat(.)`), and a read that auto-runs must still come back.
const QueryTimeout = 10 * time.Second

// queryTimeout is QueryTimeout, and the three after it are the bounds of
// the same names in limits.go: variables so a test can reach them with a
// small input instead of a large one.
var (
	queryTimeout          = QueryTimeout
	queryValues           = MaxQueryValues
	queryMemory           = uint64(MaxQueryMemory)
	queryExpressionMemory = uint64(MaxQueryExpressionMemory)
)

// What the query tool is for, and why it answers the way it does.
//
// A session reads structured files constantly — a manifest's version, a
// workflow's job names, one package out of a lockfile — and it used to do it
// one of three ways: read the file whole, which spends a window on a
// three-thousand-line lockfile to learn one line of it; shell out to jq, yq or
// a Python one-liner, which costs an approval or a classifier round each time
// and depends on what the host has installed; or reach for a wrapper that was
// registered only where jaq or yq happened to be on PATH. This answers the
// same expression in-process, in every session.
//
// Every default here is the idiom's own, because a built-in that takes more
// calls, more bytes or more arguments than the shell line it replaces is one
// the model walks past: no expression answers the file's shape, the first
// call anyone makes of an unfamiliar file; several files are one call; a
// string result is raw, with no -r to remember; results are compact, one per
// line.
// See docs/capabilities/coding-agent.md#structured-files-are-read-in-one-call.
var query = Definition{
	Tool: provider.Tool{
		Name: QueryName,
		Description: "Read structured data files — JSON, JSONL, YAML, TOML, XML, CSV and TSV — with a jq expression, in-process; it reads and never writes. " +
			"On a file you have not seen, call it with no expression first: it answers the file's shape — its top-level keys, their types and array lengths — in a few lines instead of the whole file. " +
			"Then ask for exactly what the question needs. Name several files, or a glob, in one call; each file's results come back under its path. " +
			"A string result comes back raw and anything else as compact JSON, one result per line. " +
			"YAML documents and JSONL lines are separate inputs, and slurp folds them into one array; YAML anchors are resolved; XML attributes read as +@name and element text as +content, as yq reads them; CSV and TSV rows are objects keyed by the header row, every cell a string. " +
			"A file too large to read whole can still be queried. Output stops at 200 results or 32 KB, and a cut answer names the evidence id that holds the rest.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"expression": {"type": "string", "description": "jq expression selecting only what the question needs, e.g. .version, .jobs | keys, or .packages[] | select(.name == \"cobra\") | .version. Leave it out to get the file's shape instead — the cheapest first call on a file you have not seen"},
				"paths": {"type": "array", "items": {"type": "string"}, "description": "Files to read, relative to the working directory or absolute, and glob patterns such as .github/workflows/*.yml or **/package.json. Put every file the question spans in this one call rather than one call per file"},
				"format": {"type": "string", "enum": ["json", "jsonl", "yaml", "toml", "xml", "csv", "tsv"], "description": "Parse every file named as this format instead of going by its extension. Pass it only for a file whose extension does not say, such as a .lock file or a config with no extension"},
				"slurp": {"type": "boolean", "description": "Fold each file's inputs — the documents of a multi-document YAML file, the lines of a JSONL file, the rows of a CSV — into one array before the expression runs, so length, group_by or sort_by sees them together"}
			},
			"required": ["paths"]
		}`),
	},
	// Execute is filled in by ReadOnly, where the session's evidence store
	// is known.
}

type queryArgs struct {
	Expression string   `json:"expression"`
	Paths      []string `json:"paths"`
	Format     string   `json:"format"`
	Slurp      bool     `json:"slurp"`
}

// queryFormats is every format the tool reads, in the order a refusal names
// them.
var queryFormats = []string{"json", "jsonl", "yaml", "toml", "xml", "csv", "tsv"}

// queryExtensions is what a file's extension says it is.
var queryExtensions = map[string]string{
	".json":   "json",
	".jsonl":  "jsonl",
	".ndjson": "jsonl",
	".yaml":   "yaml",
	".yml":    "yaml",
	".toml":   "toml",
	".xml":    "xml",
	".csv":    "csv",
	".tsv":    "tsv",
}

// UseEvidence hands the query tool the session's evidence store: where a cut
// answer keeps the rest of itself, and the id its notice names. keep is
// expected to scrub before it writes, which evidence.Reducer.Keep does,
// because the stored copy outlives the turn. Nil leaves a cut answer with
// nowhere to go, and its notice says to narrow the expression instead.
//
// It hangs off the record because the record is the owner of this session's
// read tools: a process serving several sessions gives each its own, so one
// session's answer is never filed in another's store. A nil receiver is the
// process's own record, which every surface that names none shares.
func (r *Recorder) UseEvidence(keep ExecKeep) {
	r = r.records()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keep = keep
}

func (r *Recorder) evidenceKeep() ExecKeep {
	r = r.records()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.keep
}

func (r *Recorder) executeQuery(raw json.RawMessage) (string, error) {
	var args queryArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if len(args.Paths) == 0 {
		return "", fmt.Errorf("paths is required: name at least one file")
	}
	if args.Format != "" && queryFormatValid(args.Format) == "" {
		return "", fmt.Errorf("invalid format %q: use %s", args.Format, strings.Join(queryFormats, ", "))
	}

	var code *gojq.Code
	if strings.TrimSpace(args.Expression) != "" {
		c, err := compileQuery(args.Expression)
		if err != nil {
			return "", err
		}
		code = c
	}

	files, err := expandQueryPaths(args.Paths)
	if err != nil {
		return "", err
	}

	timed, cancelTimed := context.WithTimeout(context.Background(), queryTimeout)
	defer cancelTimed()
	ctx, cancel := context.WithCancelCause(timed)
	defer cancel(nil)
	watch := watchQueryMemory(ctx, cancel)
	defer watch.stop()

	out := &queryOutput{}
	labelled := len(files) > 1
	for _, f := range files {
		if labelled {
			out.header(f)
		}
		err := queryFile(ctx, watch, out, f, args, code)
		if errors.Is(err, errQueryFull) {
			break
		}
		if ctx.Err() != nil {
			// A memory bound ends the call as the timeout does, rather than
			// one file's answer: every file after it would be read under
			// the context it cancelled.
			var memory *queryMemoryError
			if errors.As(queryBound(ctx, f, ctx.Err()), &memory) {
				return "", memory
			}
			return "", fmt.Errorf("query stopped after %s; narrow the expression or name fewer files", queryTimeout)
		}
		if err != nil {
			if !labelled {
				return "", err
			}
			// One unreadable file among several is a line under its own
			// label, not the loss of every other file's answer.
			out.note("error: " + err.Error())
		}
	}
	return out.render(r.evidenceKeep()), nil
}

// queryFormatValid returns format when it is one the tool reads, else "".
func queryFormatValid(format string) string {
	for _, f := range queryFormats {
		if f == format {
			return f
		}
	}
	return ""
}

// compileQuery parses and compiles an expression. The engine is given no
// environment, no module loader and no input iterator, so an expression has
// no way to read anything but the input it is handed: `env` and `$ENV` are
// empty objects, and `input`, `include` and `import` are compile errors.
// That is what keeps the path check in front of this tool the whole of what
// it can read.
func compileQuery(expr string) (*gojq.Code, error) {
	q, err := gojq.Parse(expr)
	if err != nil {
		var pe *gojq.ParseError
		if errors.As(err, &pe) {
			col := pe.Offset - len(pe.Token) + 1
			if col < 1 {
				col = 1
			}
			return nil, fmt.Errorf("invalid expression at column %d: %v\n  %s\n  %s^", col, err, expr, strings.Repeat(" ", col-1))
		}
		return nil, fmt.Errorf("invalid expression: %w", err)
	}
	code, err := gojq.Compile(q, gojq.WithEnvironLoader(func() []string { return nil }))
	if err != nil {
		return nil, fmt.Errorf("invalid expression: %w", err)
	}
	return code, nil
}

// queryGlobMeta reports whether a path entry is a glob pattern.
func queryGlobMeta(p string) bool { return strings.ContainsAny(p, "*?[") }

// expandQueryPaths turns the paths argument into the files to read, in the
// order they were named, globs expanded in walk order and duplicates dropped.
// A glob walks as glob does — .git, node_modules, vendor and what .gitignore
// names are not entered — and one that matches nothing is an error rather
// than an empty answer, since an answer about no files reads like an answer.
func expandQueryPaths(entries []string) ([]string, error) {
	var files []string
	seen := map[string]bool{}
	add := func(p string) error {
		if seen[p] {
			return nil
		}
		seen[p] = true
		files = append(files, p)
		if len(files) > MaxQueryFiles {
			return fmt.Errorf("more than %d files named; name fewer, or narrow the glob", MaxQueryFiles)
		}
		return nil
	}
	for _, entry := range entries {
		if entry == "" {
			return nil, fmt.Errorf("paths entries must not be empty")
		}
		if !queryGlobMeta(entry) {
			if err := add(entry); err != nil {
				return nil, err
			}
			continue
		}
		matched, err := expandQueryGlob(entry)
		if err != nil {
			return nil, err
		}
		if len(matched) == 0 {
			return nil, fmt.Errorf("no files matched %q", entry)
		}
		for _, m := range matched {
			if err := add(m); err != nil {
				return nil, err
			}
		}
	}
	return files, nil
}

// expandQueryGlob walks from the pattern's literal leading directories and
// matches the rest with glob's own matcher, so ** means here what it means
// there.
func expandQueryGlob(pattern string) ([]string, error) {
	slashed := filepath.ToSlash(pattern)
	abs := strings.HasPrefix(slashed, "/")
	segs := strings.Split(strings.Trim(slashed, "/"), "/")
	split := 0
	for split < len(segs) && !queryGlobMeta(segs[split]) {
		split++
	}
	base := strings.Join(segs[:split], "/")
	if abs {
		base = "/" + base
	}
	if base == "" {
		base = "."
	}
	patSegs := segs[split:]
	for _, seg := range patSegs {
		if seg == "**" {
			continue
		}
		if _, err := filepath.Match(seg, "x"); err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", pattern, err)
		}
	}
	base = filepath.FromSlash(base)
	info, err := os.Stat(base)
	if err != nil || !info.IsDir() {
		return nil, nil
	}

	var out []string
	ignore := newWalkIgnore(base)
	err = filepath.WalkDir(base, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if p != base && skipWalk(d.Name()) {
				return filepath.SkipDir
			}
			if ignore.dir(p) {
				return filepath.SkipDir
			}
			return nil
		}
		if ignore.file(p) {
			return nil
		}
		rel, err := filepath.Rel(base, p)
		if err != nil {
			return nil
		}
		ok, err := matchGlob(patSegs, strings.Split(filepath.ToSlash(rel), "/"))
		if err != nil {
			return err
		}
		if ok {
			out = append(out, p)
			if len(out) > MaxQueryFiles {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("invalid pattern %q: %w", pattern, err)
	}
	return out, nil
}

// errQueryFull is how a file's evaluation stops once the answer has grown
// past everything that could be kept of it.
var errQueryFull = errors.New("query answer full")

// queryFile reads one file and answers the expression, or its shape when
// there is none, into out.
func queryFile(ctx context.Context, watch *queryMemoryWatch, out *queryOutput, path string, args queryArgs, code *gojq.Code) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot read file: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory; name the files in it, or a glob such as %s", path, filepath.ToSlash(filepath.Join(path, "*.json")))
	}
	if err := notRegular(path, info); err != nil {
		return err
	}
	if info.Size() > MaxQueryFileSize {
		return fmt.Errorf("%s is %s and query reads at most %s of one file", path,
			attachment.HumanSize(int(info.Size())), attachment.HumanSize(MaxQueryFileSize))
	}
	format := args.Format
	if format == "" {
		format = queryExtensions[strings.ToLower(filepath.Ext(path))]
		if format == "" {
			return fmt.Errorf("%s: cannot tell the format from its extension; pass format (one of %s)", path, strings.Join(queryFormats, ", "))
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot read file: %w", err)
	}
	defer f.Close()

	// The inputs are decoded one at a time and handed on, so a file of many
	// documents, lines or rows is never held whole unless slurp asks for it
	// — and so the value bound is counted per input, except under slurp,
	// where every input is one array held at once.
	eval := func(v any) error {
		defer watch.evaluating()()
		return evalQuery(ctx, out, code, v)
	}
	var (
		slurped []any
		first   any
		count   int
		left    = queryValues
	)
	each := func(v any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !args.Slurp {
			left = queryValues
		}
		v = normalizeQueryValue(v, &left)
		if left < 0 {
			return &queryValuesError{path: path}
		}
		count++
		switch {
		case args.Slurp:
			slurped = append(slurped, v)
			return nil
		case code == nil:
			if count == 1 {
				first = v
			}
			return nil
		}
		return eval(v)
	}
	// The reader stops once the call's context is done, which is what stops
	// a decoder that builds as it reads — YAML's, XML's, CSV's — part way
	// through a document the memory bound has already refused.
	if err := decodeQueryInputs(format, path, bufio.NewReader(queryContextReader{ctx, f}), each); err != nil {
		return queryBound(ctx, path, err)
	}

	if args.Slurp {
		all := any(slurped)
		if slurped == nil {
			all = []any{}
		}
		if code == nil {
			out.shape(describeShape(all, 1, ""))
			return nil
		}
		return queryBound(ctx, path, eval(all))
	}
	if code == nil {
		if count == 0 {
			out.shape("empty: no " + queryUnit(format))
			return nil
		}
		out.shape(describeShape(first, count, queryUnit(format)))
	}
	return nil
}

// evalQuery runs the expression over one input.
func evalQuery(ctx context.Context, out *queryOutput, code *gojq.Code, v any) error {
	iter := code.RunWithContext(ctx, v)
	for {
		r, ok := iter.Next()
		if !ok {
			return nil
		}
		if err, isErr := r.(error); isErr {
			var halt *gojq.HaltError
			if errors.As(err, &halt) && halt.Value() == nil {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("expression failed: %w", err)
		}
		line, err := formatQueryResult(r)
		if err != nil {
			return err
		}
		if !out.result(line) {
			return errQueryFull
		}
	}
}

// The two causes the memory watch cancels a call's context with: the heap
// grew past MaxQueryMemory while a file was being decoded, or past
// MaxQueryExpressionMemory while the expression ran.
var (
	errQueryDecodeMemory     = errors.New("query decode memory bound reached")
	errQueryExpressionMemory = errors.New("query expression memory bound reached")
)

// queryMemoryError is a call a memory bound stopped.
type queryMemoryError struct {
	path       string
	expression bool
}

func (e *queryMemoryError) Error() string {
	if e.expression {
		return fmt.Sprintf("query stopped: the expression built more than %s in memory over %s, the most one expression may hold; ask for a count, a slice or a filtered part rather than every value at once",
			attachment.HumanSize(int(queryExpressionMemory)), e.path)
	}
	return fmt.Sprintf("query stopped: %s decodes to more than %s in memory, the most one call may hold of a file; it is refused rather than read",
		e.path, attachment.HumanSize(int(queryMemory)))
}

// queryValuesError is a document that decodes to more values than one
// document may.
type queryValuesError struct{ path string }

func (e *queryValuesError) Error() string {
	return fmt.Sprintf("%s decodes to more than %d values in one document, the most query decodes; it is refused rather than read", e.path, queryValues)
}

// queryBound puts the decode and memory bounds' refusals in their own
// words, whatever a decoder wrapped them in: a decoder reports the reader's
// error, or the refusal each handed it, as one of its own.
func queryBound(ctx context.Context, path string, err error) error {
	if err == nil {
		return nil
	}
	var values *queryValuesError
	if errors.As(err, &values) {
		return values
	}
	switch context.Cause(ctx) {
	case errQueryDecodeMemory:
		return &queryMemoryError{path: path}
	case errQueryExpressionMemory:
		return &queryMemoryError{path: path, expression: true}
	}
	return err
}

// queryContextReader is a file that stops reading once the call is over.
type queryContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c queryContextReader) Read(p []byte) (int, error) {
	if c.ctx.Err() != nil {
		return 0, context.Cause(c.ctx)
	}
	return c.r.Read(p)
}

// queryMemoryWatch cancels a call once the heap still live has grown past
// the bound in force: MaxQueryMemory over the heap as it stood when the call
// began, while a file is decoded, and MaxQueryExpressionMemory over the heap
// as it stood when the expression began, while one runs.
//
// The engine evaluates iteratively and keeps what it builds — an array being
// collected, a string being joined — inside itself, where no result, output
// writer or iteration count sees it: `[range(1e9)]` is one result, reached
// after the memory is spent. So the heap is what is measured. It is sampled
// between the engine's steps, and the engine reads the context at every
// step, so a cancel stops it at once. The heap is the process's: what else
// the session allocates in the same seconds counts too, which is why the
// figure judged is what the last collection found live and never the heap's
// raw size — garbage, the call's own or another goroutine's, is not what
// trips it. A raw size past the bound asks for a collection, at most once
// per queryMemoryCollect, so the bound holds with collection switched off
// too, without a call that stays under it spending its time collecting.
type queryMemoryWatch struct {
	phase atomic.Pointer[queryMemoryPhase]
	call  *queryMemoryPhase
	done  chan struct{}
}

// queryMemoryPhase is one bound in force: the heap it is measured over, how
// far past it the heap may grow, and the cause a call past it ends with.
type queryMemoryPhase struct {
	base, limit uint64
	cause       error
}

// watchQueryMemory starts the watch over a call; stop ends it.
func watchQueryMemory(ctx context.Context, cancel context.CancelCauseFunc) *queryMemoryWatch {
	samples := []metrics.Sample{
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/gc/heap/live:bytes"},
	}
	metrics.Read(samples)
	w := &queryMemoryWatch{
		call: &queryMemoryPhase{base: samples[0].Value.Uint64(), limit: queryMemory, cause: errQueryDecodeMemory},
		done: make(chan struct{}),
	}
	w.phase.Store(w.call)
	go func() {
		tick := time.NewTicker(queryMemoryTick)
		defer tick.Stop()
		var collected time.Time
		for {
			select {
			case <-w.done:
				return
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			p := w.phase.Load()
			past := func(n uint64) bool { return n > p.base && n-p.base > p.limit }
			metrics.Read(samples)
			if !past(samples[1].Value.Uint64()) {
				if !past(samples[0].Value.Uint64()) || time.Since(collected) < queryMemoryCollect {
					continue
				}
				runtime.GC()
				collected = time.Now()
				metrics.Read(samples)
				if !past(samples[1].Value.Uint64()) {
					continue
				}
			}
			cancel(p.cause)
			return
		}
	}()
	return w
}

// evaluating puts the expression's bound in force, over the heap as it
// stands, until the function it returns puts the call's back.
func (w *queryMemoryWatch) evaluating() func() {
	samples := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(samples)
	w.phase.Store(&queryMemoryPhase{base: samples[0].Value.Uint64(), limit: queryExpressionMemory, cause: errQueryExpressionMemory})
	return func() { w.phase.Store(w.call) }
}

func (w *queryMemoryWatch) stop() { close(w.done) }

// queryMemoryTick is how often the memory watch samples the heap: the engine
// allocates some tens of megabytes between two samples at most, which the
// bounds have room for. queryMemoryCollect is the least time between two
// collections the watch asks for.
const (
	queryMemoryTick    = 5 * time.Millisecond
	queryMemoryCollect = 100 * time.Millisecond
)

// formatQueryResult is one result as the model reads it: a string raw, since
// a string is nearly always wanted as text and jq's -r is the flag nobody
// remembers until the quotes come back; anything else as compact JSON.
func formatQueryResult(v any) (string, error) {
	if s, ok := v.(string); ok {
		return s, nil
	}
	b, err := gojq.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("cannot print result: %w", err)
	}
	return string(b), nil
}

// queryUnit is what one input of a format is called in a shape answer.
func queryUnit(format string) string {
	switch format {
	case "jsonl":
		return "lines"
	case "yaml":
		return "documents"
	case "csv", "tsv":
		return "rows"
	}
	return "values"
}

// describeShape is the answer to a call with no expression: what the value
// is, and for an object each key with its type, bounded. count and unit say
// how many inputs the file held when there was more than one.
func describeShape(v any, count int, unit string) string {
	var b strings.Builder
	if count > 1 {
		fmt.Fprintf(&b, "%d %s; the first is %s\n", count, unit, shapeOf(v))
	} else {
		b.WriteString(shapeOf(v) + "\n")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		if arr, isArr := v.([]any); isArr && len(arr) > 0 {
			b.WriteString("the first item is " + shapeOf(arr[0]) + "\n")
			obj, ok = arr[0].(map[string]any)
		}
	}
	if ok {
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i == MaxQueryShapeKeys {
				fmt.Fprintf(&b, "  … and %d more keys\n", len(keys)-i)
				break
			}
			fmt.Fprintf(&b, "  %s: %s\n", k, shapeOf(obj[k]))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// shapeOf is one value's type, with its size where it has one.
func shapeOf(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case map[string]any:
		return plural(len(x), "an object, %d key", "an object, %d keys")
	case []any:
		return plural(len(x), "an array, %d item", "an array, %d items")
	}
	return "number"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf(one, n)
	}
	return fmt.Sprintf(many, n)
}

// queryOutput collects an answer under the tool's bound, and beside it as
// much of the whole answer as could be kept, so a cut answer can say where
// the rest went.
type queryOutput struct {
	shown   strings.Builder
	full    strings.Builder
	results int
	shownN  int
	cut     bool
	fullCut bool
}

// header labels the lines after it with the file they came from.
func (o *queryOutput) header(path string) {
	o.line("==> "+path+" <==", false)
}

// note is a line that is not a result: an error under a label.
func (o *queryOutput) note(text string) { o.line(text, false) }

// shape is a shape answer: it is several lines and counts as one result.
func (o *queryOutput) shape(text string) { o.result(text) }

// result adds one result and reports whether there is room for more.
func (o *queryOutput) result(text string) bool {
	o.results++
	o.line(text, true)
	return !o.fullCut
}

func (o *queryOutput) line(text string, isResult bool) {
	if o.full.Len()+len(text)+1 > MaxQueryKeptBytes {
		o.fullCut = true
	} else {
		o.full.WriteString(text)
		o.full.WriteByte('\n')
	}
	if o.cut {
		return
	}
	fits := o.shown.Len()+len(text)+1 <= MaxQueryOutputBytes
	if o.shownN >= MaxQueryResults {
		o.cut = true
		return
	}
	if !fits {
		o.cut = true
		// A single result larger than the whole bound is still the one
		// thing there is to show, so its head is shown rather than nothing.
		if isResult && o.shownN == 0 {
			o.shown.WriteString(cutUTF8(text, MaxQueryOutputBytes))
			o.shown.WriteString(" …\n")
			o.shownN++
		}
		return
	}
	o.shown.WriteString(text)
	o.shown.WriteByte('\n')
	if isResult {
		o.shownN++
	}
}

// NoQueryResults is what an expression that produced nothing answers with:
// a sentence, for the reason NoMatchesFound is one.
const NoQueryResults = "No results."

// render is the answer the model reads, with the notice a cut one carries.
func (o *queryOutput) render(keep ExecKeep) string {
	body := strings.TrimRight(o.shown.String(), "\n")
	if o.results == 0 {
		if body == "" {
			return NoQueryResults
		}
		return body + "\n" + NoQueryResults
	}
	if !o.cut {
		return body
	}
	total := strconv.Itoa(o.results)
	if o.fullCut {
		total = "more than " + total
	}
	where := "narrow the expression, or ask for keys or length first"
	if keep != nil {
		if id, ok := keep(QueryName, o.full.String()); ok {
			where = "full output stored as evidence " + id + " — retrieve it with the evidence tool (info/read/search), or narrow the expression"
		}
	}
	return fmt.Sprintf("%s\n… (truncated at %d of %s results; %s)", body, o.shownN, total, where)
}

// decodeQueryInputs reads a file as format and hands each input to each in
// turn. A syntax error names the file, the line and the column.
func decodeQueryInputs(format, path string, r *bufio.Reader, each func(any) error) error {
	skipBOM(r)
	switch format {
	case "json":
		return decodeJSONStream(path, r, each)
	case "jsonl":
		return decodeJSONLines(path, r, each)
	case "yaml":
		return decodeYAML(path, r, each)
	case "toml":
		return decodeTOML(path, r, each)
	case "xml":
		return decodeXML(path, r, each)
	case "csv":
		return decodeCSV(path, r, ',', each)
	case "tsv":
		return decodeCSV(path, r, '\t', each)
	}
	return fmt.Errorf("invalid format %q: use %s", format, strings.Join(queryFormats, ", "))
}

// skipBOM drops a UTF-8 byte-order mark, which every one of these decoders
// would otherwise read as the first character of the file.
func skipBOM(r *bufio.Reader) {
	if b, err := r.Peek(3); err == nil && bytes.Equal(b, []byte{0xEF, 0xBB, 0xBF}) {
		_, _ = r.Discard(3)
	}
}

// parseError is a decode failure at a position in a file.
func parseError(path string, line, col int, msg string) error {
	if col > 0 {
		return fmt.Errorf("%s:%d:%d: cannot parse: %s", path, line, col, msg)
	}
	return fmt.Errorf("%s:%d: cannot parse: %s", path, line, msg)
}

// offsetPosition turns a byte offset into a line and column by reading the
// file again up to it. Only an error pays for this, and a decoder that
// reports offsets does not keep what it has read.
func offsetPosition(path string, offset int64) (line, col int) {
	line, col = 1, 1
	f, err := os.Open(path)
	if err != nil {
		return line, 0
	}
	defer f.Close()
	r := bufio.NewReader(io.LimitReader(f, offset))
	for {
		b, err := r.ReadByte()
		if err != nil {
			return line, col
		}
		if b == '\n' {
			line++
			col = 1
		} else if b < 0x80 || b >= 0xC0 {
			col++
		}
	}
}

// decodeJSONStream reads one JSON value, or several in a row as jq does.
func decodeJSONStream(path string, r io.Reader, each func(any) error) error {
	dec := json.NewDecoder(&jsonValueCounter{r: r, path: path, left: queryValues})
	dec.UseNumber()
	for {
		var v any
		err := dec.Decode(&v)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			var se *json.SyntaxError
			if errors.As(err, &se) {
				// Offset counts the byte that was wrong, so the byte
				// itself is the one before it.
				line, col := offsetPosition(path, max(se.Offset-1, 0))
				return parseError(path, line, col, se.Error())
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				// The decoder reports no offset for a value the file ended
				// inside (it reads 0), and the place that is wrong is the end.
				line, col := offsetPosition(path, math.MaxInt64)
				return parseError(path, line, col, "unexpected end of file")
			}
			return fmt.Errorf("%s: cannot parse: %w", path, err)
		}
		if err := each(v); err != nil {
			return err
		}
	}
}

// jsonValueCounter counts the values in JSON text as the decoder reads it,
// and fails the read that takes a document past the value bound. The
// decoder reads a whole value's text before it builds anything from it, so
// a refusal here comes before the tree does: a 64 MiB array of empty arrays
// is refused some twenty-four megabytes into its text rather than after two
// gigabytes of tree. It counts what normalizeQueryValue counts — every
// scalar, array and object, keys aside — and starts again at each top-level
// value, as the walk does for a document of several.
type jsonValueCounter struct {
	r       io.Reader
	path    string
	left    int
	objects []bool // for each open container, whether it is an object
	last    byte   // the last byte outside a string that was not space
	str     bool   // inside a string
	escaped bool   // the byte before was a backslash inside a string
}

func (c *jsonValueCounter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	for _, b := range p[:n] {
		if c.str {
			switch {
			case c.escaped:
				c.escaped = false
			case b == '\\':
				c.escaped = true
			case b == '"':
				c.str = false
				c.last = '"'
			}
			continue
		}
		switch b {
		case ' ', '\t', '\n', '\r':
			if c.last != '"' && !strings.ContainsRune("[]{},:", rune(c.last)) {
				c.last = ' '
			}
			continue
		case '"':
			c.str = true
			// A string straight after an object's opening brace or a comma
			// inside one is a key.
			if len(c.objects) > 0 && c.objects[len(c.objects)-1] && (c.last == '{' || c.last == ',') {
				continue
			}
			c.left--
		case '[', '{':
			c.objects = append(c.objects, b == '{')
			c.left--
		case ']', '}':
			if len(c.objects) > 0 {
				c.objects = c.objects[:len(c.objects)-1]
			}
			if len(c.objects) == 0 {
				c.left = queryValues
			}
		case ',', ':':
		default:
			// A number, true, false or null: counted at its first byte.
			if c.last == 0 || c.last == ' ' || c.last == '[' || c.last == ',' || c.last == ':' {
				c.left--
			}
		}
		c.last = b
		if c.left < 0 {
			return 0, &queryValuesError{path: c.path}
		}
	}
	return n, err
}

// decodeJSONLines reads one JSON value per line, skipping blank lines.
func decodeJSONLines(path string, r *bufio.Reader, each func(any) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), MaxQueryFileSize)
	line := 0
	for sc.Scan() {
		line++
		text := bytes.TrimSpace(sc.Bytes())
		if len(text) == 0 {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(text))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			col := 0
			var se *json.SyntaxError
			if errors.As(err, &se) {
				col = int(se.Offset)
			}
			return parseError(path, line, col, err.Error())
		}
		if dec.More() {
			return parseError(path, line, int(dec.InputOffset())+1, "more than one value on the line")
		}
		if err := each(v); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("%s: cannot read: %w", path, err)
	}
	return nil
}

// decodeYAML reads every document in turn. Anchors, aliases and merge keys
// are resolved by the decoder, so an expression sees the values they stand
// for.
func decodeYAML(path string, r io.Reader, each func(any) error) error {
	dec := yaml.NewDecoder(r)
	for {
		var v any
		err := dec.Decode(&v)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			// The YAML parser reports a line and no column — the line
			// of the construct the error was found in — so that is what
			// the error can say.
			var pe *yaml.ParserError
			if errors.As(err, &pe) {
				return parseError(path, pe.Line, 0, pe.Message)
			}
			var te *yaml.TypeError
			if errors.As(err, &te) && len(te.Errors) > 0 {
				ue := te.Errors[0]
				return parseError(path, ue.Line, ue.Column, ue.Err.Error())
			}
			return fmt.Errorf("%s: cannot parse: %w", path, err)
		}
		if err := each(v); err != nil {
			return err
		}
	}
}

// decodeTOML reads the document as one input.
func decodeTOML(path string, r io.Reader, each func(any) error) error {
	var v map[string]any
	if _, err := toml.NewDecoder(r).Decode(&v); err != nil {
		var pe toml.ParseError
		if errors.As(err, &pe) {
			return parseError(path, pe.Position.Line, pe.Position.Col, pe.Message)
		}
		return fmt.Errorf("%s: cannot parse: %w", path, err)
	}
	if v == nil {
		v = map[string]any{}
	}
	return each(v)
}

// xmlNode is an element while its document is being read.
type xmlNode struct {
	name     string
	attrs    [][2]string
	children []*xmlNode
	text     strings.Builder
}

// decodeXML reads the document as one input, shaped the way yq shapes one:
// the root element is the one key of the top-level object, an attribute is
// +@name, text beside attributes or child elements is +content, an element
// holding only text is that text, and a repeated child is an array. An
// expression written against yq's reading of a file answers the same here.
// Namespace prefixes stay on the names as written (ns:item), as they do there.
func decodeXML(path string, r io.Reader, each func(any) error) error {
	dec := xml.NewDecoder(r)
	var stack []*xmlNode
	var root *xmlNode
	fail := func(msg string) error {
		line, col := dec.InputPos()
		return parseError(path, line, col, msg)
	}
	for {
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			var se *xml.SyntaxError
			if errors.As(err, &se) {
				return fail(se.Msg)
			}
			return fail(err.Error())
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(stack) == 0 && root != nil {
				return fail("a second root element <" + xmlName(t.Name) + ">")
			}
			n := &xmlNode{name: xmlName(t.Name)}
			for _, a := range t.Attr {
				n.attrs = append(n.attrs, [2]string{xmlName(a.Name), a.Value})
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, n)
			} else {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1].name != xmlName(t.Name) {
				return fail("unexpected closing tag </" + xmlName(t.Name) + ">")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text.Write(t)
			} else if len(bytes.TrimSpace(t)) > 0 {
				return fail("text outside the root element")
			}
		}
	}
	if len(stack) > 0 {
		return fail("unexpected end of file inside <" + stack[len(stack)-1].name + ">")
	}
	if root == nil {
		return fail("no root element")
	}
	return each(map[string]any{root.name: root.value()})
}

// xmlName is a name as the file wrote it, prefix included.
func xmlName(n xml.Name) string {
	if n.Space != "" {
		return n.Space + ":" + n.Local
	}
	return n.Local
}

// value is the element's reading.
func (n *xmlNode) value() any {
	text := strings.TrimSpace(n.text.String())
	if len(n.attrs) == 0 && len(n.children) == 0 {
		return text
	}
	m := map[string]any{}
	for _, a := range n.attrs {
		m["+@"+a[0]] = a[1]
	}
	counts := map[string]int{}
	for _, c := range n.children {
		counts[c.name]++
	}
	for _, c := range n.children {
		if counts[c.name] > 1 {
			list, _ := m[c.name].([]any)
			m[c.name] = append(list, c.value())
			continue
		}
		m[c.name] = c.value()
	}
	if text != "" {
		m["+content"] = text
	}
	return m
}

// decodeCSV reads the header row, then hands on each row as an object keyed
// by it. Cells stay strings: a column of ZIP codes or version numbers read
// as numbers would lose what it said, and tonumber is one word away.
func decodeCSV(path string, r io.Reader, comma rune, each func(any) error) error {
	cr := csv.NewReader(r)
	cr.Comma = comma
	if comma == '\t' {
		// A TSV has no quoting convention of its own; a quote in a cell is
		// a character in it.
		cr.LazyQuotes = true
	}
	header, err := cr.Read()
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return csvError(path, err)
	}
	for i, h := range header {
		if h == "" {
			header[i] = "column" + strconv.Itoa(i+1)
		}
	}
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return csvError(path, err)
		}
		row := make(map[string]any, len(header))
		for i, h := range header {
			if i < len(rec) {
				row[h] = rec[i]
			}
		}
		if err := each(row); err != nil {
			return err
		}
	}
}

func csvError(path string, err error) error {
	var pe *csv.ParseError
	if errors.As(err, &pe) {
		return parseError(path, pe.Line, pe.Column, pe.Err.Error())
	}
	return fmt.Errorf("%s: cannot parse: %w", path, err)
}

// normalizeQueryValue puts a decoded value into the types the engine takes —
// nil, bool, int, float64, *big.Int, string, []any and map[string]any — and
// nothing else, since anything else is a panic inside it. The decoders hand
// back more than that: JSON numbers kept as text so a large integer is not
// rounded, YAML's integer widths and non-string keys, and TOML's datetimes.
//
// It also counts the document against the value bound: left is what the
// document may still hold, and once it goes below zero the walk stops and
// the value is to be refused, not read. An array or object is rewritten in
// place rather than copied, since the decoder's value is not used again and
// a copy would hold the document twice at the moment it is largest.
func normalizeQueryValue(v any, left *int) any {
	if *left--; *left < 0 {
		return nil
	}
	switch x := v.(type) {
	case nil, bool, string, float64, int:
		return x
	case json.Number:
		if i, err := x.Int64(); err == nil && i >= math.MinInt && i <= math.MaxInt {
			return int(i)
		}
		if b, ok := new(big.Int).SetString(string(x), 10); ok {
			return b
		}
		f, err := x.Float64()
		if err != nil {
			return string(x)
		}
		return f
	case int8:
		return int(x)
	case int16:
		return int(x)
	case int32:
		return int(x)
	case int64:
		if x >= math.MinInt && x <= math.MaxInt {
			return int(x)
		}
		return big.NewInt(x)
	case uint:
		return normalizeUint(uint64(x))
	case uint8:
		return int(x)
	case uint16:
		return int(x)
	case uint32:
		return normalizeUint(uint64(x))
	case uint64:
		return normalizeUint(x)
	case float32:
		return float64(x)
	case *big.Int:
		return x
	case time.Time:
		return formatQueryTime(x)
	case []byte:
		return string(x)
	case []any:
		for i, e := range x {
			if x[i] = normalizeQueryValue(e, left); *left < 0 {
				return nil
			}
		}
		return x
	case []map[string]any:
		out := make([]any, len(x))
		for i, e := range x {
			if out[i] = normalizeQueryValue(e, left); *left < 0 {
				return nil
			}
		}
		return out
	case map[string]any:
		for k, e := range x {
			if x[k] = normalizeQueryValue(e, left); *left < 0 {
				return nil
			}
		}
		return x
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			if out[fmt.Sprint(k)] = normalizeQueryValue(e, left); *left < 0 {
				return nil
			}
		}
		return out
	}
	return fmt.Sprint(v)
}

func normalizeUint(u uint64) any {
	if u <= math.MaxInt {
		return int(u)
	}
	return new(big.Int).SetUint64(u)
}

// formatQueryTime writes a datetime as the text the file most likely held.
// TOML's local date, time and datetime arrive as a time.Time in a zone named
// for which of the three it was, and printing one of them with an offset
// would put a timezone in a value that deliberately had none.
func formatQueryTime(t time.Time) string {
	switch t.Location().String() {
	case "date-local":
		return t.Format("2006-01-02")
	case "time-local":
		return t.Format("15:04:05.999999999")
	case "datetime-local":
		return t.Format("2006-01-02T15:04:05.999999999")
	}
	return t.Format(time.RFC3339Nano)
}
