package tools

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

// What one search is worth.
//
// search used to answer only one question — which lines match — and answering
// it took a round each time. A matched line with nothing around it rarely
// settles anything, so the round after it was another search, and the shape
// of an investigation became dozens of one-line questions. The three options
// here are the ones that let a single call finish a thought: `context_lines`
// shows the code around a match, `files_only` answers "where does this live"
// without quoting anything, and `include` narrows by file type instead of by
// re-running with a longer pattern. `only_matching` answers "which of these
// exist" with the matched texts and their counts rather than the lines, and
// `multiline` lets a pattern cross a line break; each is a shell pipeline or
// flag the model otherwise reaches for through a command.
// See docs/capabilities/coding-agent.md#finding-things.
var search = Definition{
	Tool: provider.Tool{
		Name: SearchName,
		Description: "Search file contents with a regular expression (RE2 syntax). Case-insensitive by default. " +
			"Returns matching lines as path:line: text. Hidden files are searched; .git, node_modules, vendor and anything .gitignore names are not. " +
			"Each match comes with two lines of context by default, so the answer arrives with the hit instead of in the round after it; " +
			"set context_lines to widen or narrow that, files_only to find which files are involved without quoting any, " +
			"and include to limit the search to one kind of file. " +
			"Set literal to search for the pattern as written, so punctuation needs no escaping, and word_boundary to match whole words only.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"pattern": {"type": "string", "description": "Regular expression to search for (case-insensitive unless case_sensitive is true)"},
				"path": {"type": "string", "description": "Optional directory or file path to search in (defaults to current directory)"},
				"case_sensitive": {"type": "boolean", "description": "Match case-sensitively (default false)"},
				"include": {"type": "string", "description": "Optional glob limiting which files are searched, e.g. *.go or internal/**/*_test.go"},
				"context_lines": {"type": "integer", "description": "Lines of context to show around each match (0-5, default 2). Pass 0 for matching lines alone. Context lines are shown as path-line- text"},
				"files_only": {"type": "boolean", "description": "Return one line per matching file with its match count instead of the matching lines. Use it to find where something lives"},
				"literal": {"type": "boolean", "description": "Treat pattern as plain text rather than a regular expression, so ( ) $ . * need no escaping"},
				"word_boundary": {"type": "boolean", "description": "Match only where the pattern is a whole word, so Add does not match AddMemory"},
				"only_matching": {"type": "boolean", "description": "Return each distinct matched text instead of the lines, with how many times and in how many files it occurs, most frequent first. Use it to ask which IDs, keys or names exist"},
				"multiline": {"type": "boolean", "description": "Let a match span lines, so \\n and \\s can match a line break (. still does not). Every line a match spans is shown as matching"},
				"limit": {"type": "integer", "description": "Maximum matches to return (default 50, or 200 with files_only or only_matching; max 500)"}
			},
			"required": ["pattern"]
		}`),
	},
	Execute: executeSearch,
}

// DefaultSearchContextLines is what a search returns around each match when
// the caller does not say. It is not zero because a bare matching line is the
// expensive answer: it settles nothing, so the round after it is a read of
// the same place. Two lines is what turns one search into one answer.
const DefaultSearchContextLines = 2

type searchArgs struct {
	Pattern       string `json:"pattern"`
	Path          string `json:"path"`
	CaseSensitive bool   `json:"case_sensitive"`
	Include       string `json:"include"`
	ContextLines  *int   `json:"context_lines"`
	FilesOnly     bool   `json:"files_only"`
	Literal       bool   `json:"literal"`
	WordBoundary  bool   `json:"word_boundary"`
	OnlyMatching  bool   `json:"only_matching"`
	Multiline     bool   `json:"multiline"`
	Limit         int    `json:"limit"`

	// context is ContextLines resolved against the default, and limit is
	// Limit resolved against this mode's default and the ceiling. Both are
	// what every backend actually reads: a default applied twice, once per
	// backend, is a search that answers differently depending on which one
	// the machine has.
	context int
	limit   int

	// inflate is the call's budget for decompressing .gz files, shared by
	// every file the call reads.
	inflate *inflateBudget
}

// inflateBudget is how much one search may decompress across every .gz it
// reads: the read ceiling, once per call, not once per file — a directory of
// rotated logs is otherwise ten megabytes each. What a bound cut or refused
// is said at the end of the answer, since a search that quietly read half a
// file answers a question nobody asked.
type inflateBudget struct {
	left    int64
	notes   []string
	skipped int
}

func newInflateBudget() *inflateBudget { return &inflateBudget{left: MaxReadFileSize} }

// read decompresses one .gz under what is left of the budget, and returns
// the text to search — possibly only its head — or an error for a file that
// is not searched at all.
func (b *inflateBudget) read(path string) (string, error) {
	if b.left <= 0 {
		b.skipped++
		return "", errPastCeiling
	}
	data, _, err := inflateGzip(path, b.left)
	b.left -= int64(len(data))
	var ratio *archiveRatioError
	switch {
	case errors.As(err, &ratio):
		b.notes = append(b.notes, fmt.Sprintf("… (%s not searched: %s)", path, ratio.Error()))
		return "", err
	case errors.Is(err, errPastCeiling):
		b.notes = append(b.notes, fmt.Sprintf("… (%s searched only in its first %s decompressed: one search decompresses at most %s)",
			path, provider.HumanSize(len(data)), provider.HumanSize(MaxReadFileSize)))
	case err != nil:
		return "", err
	}
	if _, text := sniffText(data[:min(len(data), SniffBytes)]); len(data) == 0 || !text {
		return "", errors.New("not text")
	}
	return string(data), nil
}

// tail is the lines a search's answer ends with for what its budget cut.
func (b *inflateBudget) tail() []string {
	if b == nil {
		return nil
	}
	out := b.notes
	if b.skipped > 0 {
		out = append(out, fmt.Sprintf("… (%d more compressed %s not searched: one search decompresses at most %s)",
			b.skipped, noun(b.skipped, "file", "files"), provider.HumanSize(MaxReadFileSize)))
	}
	return out
}

// withInflateNotes adds the budget's lines to a formatted answer.
func withInflateNotes(out string, args searchArgs) string {
	if tail := args.inflate.tail(); len(tail) > 0 {
		return out + "\n" + strings.Join(tail, "\n")
	}
	return out
}

// lookupRg reports where ripgrep lives, if it is on PATH. A variable so tests
// can force the pure-Go fallback path.
var lookupRg = func() (string, bool) {
	path, err := exec.LookPath("rg")
	return path, err == nil
}

// searchPattern applies the two pattern arguments, and is what both backends
// match with: ripgrep is handed this string rather than --fixed-strings and
// --word-regexp.
//
// Those flags look like the same two questions and are not. -F selects a
// literal matcher, and -w is a rule about what surrounds a match, which
// ripgrep applies as half-boundaries: on a pattern whose own end is
// punctuation — `foo(`, the pattern the literal argument exists for — -w
// refuses the line `x := foo(1)` because a word character follows the match,
// while `\b` accepts it because the boundary is between `(` and `1`. Two
// backends that disagree in opposite directions on the same call is the
// failure this whole pair of arguments was added to end, so there is one
// wrapping and both engines read it. What a quoted pattern means is not in
// question: it is one literal string either way.
//
// The order matters. Quoting comes first, so a literal search for `foo(`
// escapes the parenthesis and not the `\b` the boundary wrapper adds after
// it; and the boundary group is a group, so `\b(?:foo|bar)\b` binds the
// whole alternation rather than only its two ends.
func searchPattern(args searchArgs) string {
	expr := args.Pattern
	if args.Literal {
		expr = regexp.QuoteMeta(expr)
	}
	if args.WordBoundary {
		expr = `\b(?:` + expr + `)\b`
	}
	return expr
}

// searchExpr is what the walker compiles: the shared pattern with the case
// question, which ripgrep is asked through --ignore-case instead. Compiling
// it is also what decides whether a call is valid at all, so the two backends
// refuse the same inputs with the same error.
//
// A multiline search is matched against the whole file rather than a line at
// a time, so it takes (?m): ripgrep's ^ and $ are line anchors whether or not
// -U is on, and without the flag the walker's would mean the file's ends.
func searchExpr(args searchArgs) string {
	flags := ""
	if !args.CaseSensitive {
		flags += "i"
	}
	if args.Multiline {
		flags += "m"
	}
	if flags == "" {
		return searchPattern(args)
	}
	return "(?" + flags + ")" + searchPattern(args)
}

func executeSearch(raw json.RawMessage) (string, error) {
	var args searchArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	if args.Path == "" {
		args.Path = "."
	}
	args.context = DefaultSearchContextLines
	if args.ContextLines != nil {
		args.context = *args.ContextLines
	}
	if args.context < 0 {
		args.context = 0
	}
	if args.context > MaxSearchContextLines {
		args.context = MaxSearchContextLines
	}
	if args.FilesOnly && args.OnlyMatching {
		return "", fmt.Errorf("files_only and only_matching are two different answers; pass one of them")
	}
	args.limit = MaxSearchResults
	switch {
	case args.FilesOnly:
		args.limit = MaxSearchFileResults
	case args.OnlyMatching:
		args.limit = MaxSearchDistinctResults
	}
	if args.Limit > 0 {
		args.limit = min(args.Limit, MaxSearchLimit)
	}

	// Validate the pattern up front so both backends reject the same inputs
	// with the same error.
	re, err := regexp.Compile(searchExpr(args))
	if err != nil {
		return "", fmt.Errorf("invalid regular expression: %w", err)
	}

	include, err := compileInclude(args.Include)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(args.Path)
	if err != nil {
		return "", fmt.Errorf("cannot access path: %w", err)
	}
	if err := notRegular(args.Path, info); err != nil {
		return "", err
	}
	args.inflate = newInflateBudget()

	if args.OnlyMatching {
		return searchDistinct(re, include, args)
	}

	// Ripgrep reads a .gz as the bytes it is, so a named one is searched
	// by the walker, and a directory's are searched by the walker after
	// ripgrep has answered for everything else (ripgrepArgv leaves them
	// out). Both backends then read one file the same way.
	if rg, ok := lookupRg(); ok && !isGzipFile(args.Path) {
		results, matches, err := searchWithRipgrep(rg, args)
		if err == nil {
			if info.IsDir() {
				for _, p := range ripgrepGzipFiles(rg, include, args) {
					if matches >= args.limit {
						break
					}
					results, matches = searchFile(p, re, args, results, matches, args.limit)
				}
			}
			return withInflateNotes(formatSearchResults(results, matches, args), args), nil
		}
		// Ripgrep failed (e.g. a pattern its regex engine rejects): fall
		// through to the pure-Go walker.
	}

	results, matches, err := searchWithWalker(re, include, args)
	if err != nil {
		return "", err
	}
	return withInflateNotes(formatSearchResults(results, matches, args), args), nil
}

// includeMatcher tests a path relative to the search root against the
// `include` glob. A pattern with no separator matches a file's name at any
// depth, which is what `*.go` is asking for and what ripgrep's own --glob
// does with it.
type includeMatcher struct {
	segs []string
}

func compileInclude(pattern string) (*includeMatcher, error) {
	if pattern == "" {
		return nil, nil
	}
	pattern = filepath.ToSlash(pattern)
	if !strings.Contains(pattern, "/") {
		pattern = "**/" + pattern
	}
	segs := strings.Split(strings.Trim(pattern, "/"), "/")
	for _, seg := range segs {
		if seg == "**" {
			continue
		}
		if _, err := filepath.Match(seg, "x"); err != nil {
			return nil, fmt.Errorf("invalid include pattern %q: %w", pattern, err)
		}
	}
	return &includeMatcher{segs: segs}, nil
}

func (m *includeMatcher) match(rel string) bool {
	if m == nil {
		return true
	}
	ok, err := matchGlob(m.segs, strings.Split(filepath.ToSlash(rel), "/"))
	return err == nil && ok
}

// searchWithRipgrep shells out to rg for speed. The excluded directories
// mirror the walker's skip list; --with-filename keeps single-file output in
// the same path:line format, and --null makes the path separator unambiguous.
// It returns the formatted lines and how many of them are matches rather than
// context, because the result cap counts matches.
//
// --hidden is what makes the two backends answer the same question. ripgrep
// skips a dotfile unless told otherwise and the walker has never had such a
// rule, so without it `.github/workflows`, `.golangci.yml` and the project's
// own `.agents/skills` are tracked files that search reports as absent on
// every machine with rg installed — and the model, told "No matches found",
// concludes the file does not exist and writes a new one. The three excluded
// globs are what keeps .git out once hidden files are in.
func searchWithRipgrep(rg string, args searchArgs) (results []string, matches int, err error) {
	argv := ripgrepArgv(args)
	limit := args.limit
	switch {
	case args.FilesOnly:
		// --count-matches answers "where does this live, and how much of it
		// is there" in one line per file, which is the question files_only
		// is for. --line-number is meaningless with it and rg says so.
		argv = append(argv, "--count-matches")
		argv = removeArg(argv, "--line-number")
	case args.context > 0:
		argv = append(argv, "--context", strconv.Itoa(args.context))
	}
	argv = append(argv, "--regexp", searchPattern(args), "--", args.Path)

	cmd := exec.Command(rg, argv...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, 0, err
	}
	if err := cmd.Start(); err != nil {
		return nil, 0, err
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for matches < limit && scanner.Scan() {
		line := scanner.Text()
		// rg separates non-adjacent context groups with a bare "--". It
		// carries no path, so it is kept as itself.
		if line == "--" {
			if len(results) > 0 {
				results = append(results, "--")
			}
			continue
		}
		nul := strings.IndexByte(line, 0)
		if nul < 0 {
			continue
		}
		path, rest := line[:nul], line[nul+1:]
		if args.FilesOnly {
			results = append(results, formatFileCount(path, rest))
			matches++
			continue
		}
		// A match line is path\0N:text, a context line path\0N-text. The
		// separator is whatever follows the digits.
		end := 0
		for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
			end++
		}
		if end == 0 || end >= len(rest) {
			continue
		}
		sep := rest[end]
		if sep != ':' && sep != '-' {
			continue
		}
		results = append(results, formatMatch(path, rest[:end], string(sep), rest[end+1:]))
		if sep == ':' {
			matches++
		}
	}
	stoppedEarly := matches >= limit || scanner.Err() != nil
	if stoppedEarly {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()

	if stoppedEarly && len(results) > 0 {
		return results, matches, nil
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, 0, nil // exit 1 means no matches
		}
		if len(results) > 0 {
			// Partial errors (e.g. unreadable files) still produced matches.
			return results, matches, nil
		}
		return nil, 0, fmt.Errorf("ripgrep failed: %w", waitErr)
	}
	return results, matches, nil
}

// ripgrepArgv is the part of the ripgrep command line every mode shares:
// which files are searched, how a line is written, and the questions the
// arguments ask of the pattern. --multiline is ripgrep's -U, which lets a
// match cross a line break and prints every line it spans as a match.
func ripgrepArgv(args searchArgs) []string {
	argv := []string{
		"--line-number", "--no-heading", "--with-filename", "--color=never",
		"--no-messages", "--null", "--hidden",
		"--max-columns", strconv.Itoa(MaxSearchLineBytes), "--max-columns-preview",
		"--glob", "!.git", "--glob", "!node_modules", "--glob", "!vendor",
	}
	if !args.CaseSensitive {
		argv = append(argv, "--ignore-case")
	}
	if args.Multiline {
		argv = append(argv, "--multiline")
	}
	if args.Include != "" {
		argv = append(argv, "--glob", args.Include)
	}
	// Last, so it outranks an include of *.gz: those files are the walker's
	// (ripgrepGzipFiles), and ripgrep reading them as bytes as well would
	// answer for the same file twice.
	return append(argv, "--iglob", "!*.gz")
}

// ripgrepGzipFiles names the .gz files under a directory that ripgrepArgv
// left out, for the walker to search as their text: ripgrep chooses them, so
// the ignore rules are the ones every other file was chosen by, and the
// include glob and the walker's size and binary tests are then asked here,
// as walkSearch asks them.
func ripgrepGzipFiles(rg string, include *includeMatcher, args searchArgs) []string {
	argv := []string{
		"--files", "--no-messages", "--null", "--hidden",
		"--glob", "!.git", "--glob", "!node_modules", "--glob", "!vendor",
		"--iglob", "*.gz", "--", args.Path,
	}
	out, _ := exec.Command(rg, argv...).Output()
	var files []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p = strings.Trim(p, "\n"); p == "" || !strings.HasSuffix(strings.ToLower(p), ".gz") {
			continue
		}
		if include != nil {
			rel, err := filepath.Rel(args.Path, p)
			if err != nil || !include.match(rel) {
				continue
			}
		}
		if fi, err := os.Stat(p); err != nil || fi.Size() > MaxSearchFileBytes {
			continue
		}
		if !isGzipFile(p) && isBinary(p) {
			continue
		}
		files = append(files, p)
	}
	sort.Strings(files)
	return files
}

// ripgrepFiles asks ripgrep only which files hold a match. It is how
// only_matching uses ripgrep: the walk, the ignore rules and the first pass
// over each file are ripgrep's, and the matches are then read out of the
// files it named with the same compiled expression the walker uses — so the
// texts and their counts are one engine's answer on every machine, rather
// than ripgrep's -o on one and RE2 on another.
func ripgrepFiles(rg string, args searchArgs) ([]string, error) {
	argv := removeArg(ripgrepArgv(args), "--line-number")
	argv = append(argv, "--files-with-matches", "--regexp", searchPattern(args), "--", args.Path)
	out, err := exec.Command(rg, argv...).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil // exit 1 means no matches
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("ripgrep failed: %w", err)
		}
		// Partial errors (e.g. unreadable files) still named files.
	}
	var files []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p = strings.Trim(p, "\n"); p != "" {
			files = append(files, p)
		}
	}
	return files, nil
}

func removeArg(argv []string, drop string) []string {
	out := argv[:0]
	for _, a := range argv {
		if a != drop {
			out = append(out, a)
		}
	}
	return out
}

// searchWithWalker is the pure-Go fallback: walk the tree, skipping the
// standard directories, the paths .gitignore names, and binary or oversized
// files.
func searchWithWalker(re *regexp.Regexp, include *includeMatcher, args searchArgs) (results []string, matches int, err error) {
	limit := args.limit
	err = walkSearch(include, args, func(p string) bool {
		results, matches = searchFile(p, re, args, results, matches, limit)
		return matches < limit
	})
	if err != nil {
		return nil, 0, err
	}
	return results, matches, nil
}

// walkSearch hands visit every file under args.Path the search reads, and
// stops when visit answers false. A path naming one file is that file alone.
func walkSearch(include *includeMatcher, args searchArgs, visit func(path string) bool) error {
	info, err := os.Stat(args.Path)
	if err != nil {
		return fmt.Errorf("cannot access path: %w", err)
	}
	if !info.IsDir() {
		visit(args.Path)
		return nil
	}

	// Ripgrep honours .gitignore of its own accord, so the walker does too:
	// otherwise the same search answers differently depending on whether rg
	// happens to be installed on the machine.
	ignore := newWalkIgnore(args.Path)
	more := true
	err = filepath.WalkDir(args.Path, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if p != args.Path && skipWalk(name) {
				return filepath.SkipDir
			}
			if ignore.dir(p) {
				return filepath.SkipDir
			}
			return nil
		}
		if !more {
			return filepath.SkipAll
		}
		if ignore.file(p) {
			return nil
		}
		if include != nil {
			rel, relErr := filepath.Rel(args.Path, p)
			if relErr != nil || !include.match(rel) {
				return nil
			}
		}
		if fi, err := d.Info(); err != nil || fi.Size() > MaxSearchFileBytes {
			return nil
		}
		if !isGzipFile(p) && isBinary(p) {
			return nil
		}
		more = visit(p)
		return nil
	})
	if err != nil {
		return fmt.Errorf("search error: %w", err)
	}
	return nil
}

// readSearchFile is the one place the walker reads a file's text, for every
// mode, so a reader that has to turn a file into text first wraps this and
// nothing else.
func readSearchFile(path string, args searchArgs) (string, error) {
	if args.inflate != nil && isGzipFile(path) {
		return args.inflate.read(path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if err := notRegular(path, info); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// matchedLines is which lines of a file hold a match, as indexes into lines.
// Line by line, a line matches or it does not. A multiline search matches the
// whole text instead, and every line a match touches is a matched line — the
// lines ripgrep's -U prints — where a match that ends on a line break ends on
// the line the break closes.
func matchedLines(text string, lines []string, re *regexp.Regexp, args searchArgs, room int) []int {
	var hits []int
	if !args.Multiline {
		for i, line := range lines {
			if re.MatchString(line) {
				hits = append(hits, i)
				if !args.FilesOnly && len(hits) >= room {
					break
				}
			}
		}
		return hits
	}
	// starts[i] is the offset line i begins at.
	starts := make([]int, len(lines))
	for i, off := 1, 0; i < len(lines); i++ {
		off += len(lines[i-1]) + 1
		starts[i] = off
	}
	lineAt := func(off int) int { return sort.SearchInts(starts, off+1) - 1 }
	next := 0
	for _, m := range re.FindAllStringIndex(text, -1) {
		first, last := lineAt(m[0]), lineAt(m[0])
		if m[1] > m[0] {
			last = lineAt(m[1] - 1)
		}
		for i := max(first, next); i <= last; i++ {
			hits = append(hits, i)
		}
		next = max(next, last+1)
	}
	if !args.FilesOnly && len(hits) > room {
		hits = hits[:max(room, 0)]
	}
	return hits
}

// searchFile appends one file's hits to results, honouring files_only and the
// context window around each match. It returns the results and the running
// match count, which is what the cap is measured in — context lines ride
// along with the match that earned them.
func searchFile(path string, re *regexp.Regexp, args searchArgs, results []string, matches, limit int) ([]string, int) {
	text, err := readSearchFile(path, args)
	if err != nil {
		return results, matches
	}
	lines := strings.Split(text, "\n")

	hits := matchedLines(text, lines, re, args, limit-matches)
	if len(hits) == 0 {
		return results, matches
	}

	if args.FilesOnly {
		return append(results, formatFileCount(path, strconv.Itoa(len(hits)))), matches + 1
	}

	// Expand each hit into its context window and merge the windows that
	// overlap, so a run of nearby matches reads as one excerpt rather than
	// as the same lines repeated.
	prevEnd := -1
	for _, hit := range hits {
		if matches >= limit {
			break
		}
		start := hit - args.context
		if start < 0 {
			start = 0
		}
		end := hit + args.context
		if end >= len(lines) {
			end = len(lines) - 1
		}
		if prevEnd >= 0 {
			if start <= prevEnd+1 {
				start = prevEnd + 1
			} else if args.context > 0 {
				results = append(results, "--")
			}
		}
		for i := start; i <= end; i++ {
			// A matched line inside the window of the match before it is
			// still a match: the next hit's window starts past it, so this
			// is the one place it is written.
			sep := "-"
			if _, ok := slices.BinarySearch(hits, i); ok {
				sep = ":"
			}
			results = append(results, formatMatch(path, strconv.Itoa(i+1), sep, lines[i]))
		}
		if end > prevEnd {
			prevEnd = end
		}
		matches++
	}
	return results, matches
}

// distinctMatch is one text only_matching reports: how many times the
// pattern matched it and in how many files.
type distinctMatch struct {
	text  string
	count int
	files int
}

// searchDistinct answers only_matching — `grep -oh | sort | uniq -c | sort
// -rn` in one call: every text the pattern matched, once, with how often and
// in how many files, most frequent first. The counts are over everything the
// search reaches rather than the first matches found, since a count taken
// before the walk ends is a count of the walk's order; what is bounded is how
// many distinct texts are shown.
//
// Ripgrep, where it is on PATH, chooses the files; the texts are read out of
// them by the compiled expression either way (ripgrepFiles says why).
func searchDistinct(re *regexp.Regexp, include *includeMatcher, args searchArgs) (string, error) {
	tally := map[string]*distinctMatch{}
	count := func(path string) {
		text, err := readSearchFile(path, args)
		if err != nil {
			return
		}
		seen := map[string]bool{}
		for _, m := range distinctTexts(text, re, args) {
			d := tally[m]
			if d == nil {
				d = &distinctMatch{text: m}
				tally[m] = d
			}
			d.count++
			if !seen[m] {
				seen[m] = true
				d.files++
			}
		}
	}

	walked := false
	if rg, ok := lookupRg(); ok && !isGzipFile(args.Path) {
		if files, err := ripgrepFiles(rg, args); err == nil {
			// The walker leaves an oversized file out of a directory's
			// search and reads a file it was pointed at, so this does too.
			dir := false
			if info, err := os.Stat(args.Path); err == nil {
				dir = info.IsDir()
			}
			for _, p := range files {
				if fi, err := os.Stat(p); err != nil || (dir && fi.Size() > MaxSearchFileBytes) {
					continue
				}
				count(p)
			}
			if dir {
				for _, p := range ripgrepGzipFiles(rg, include, args) {
					count(p)
				}
			}
			walked = true
		}
	}
	if !walked {
		if err := walkSearch(include, args, func(p string) bool { count(p); return true }); err != nil {
			return "", err
		}
	}
	return withInflateNotes(formatDistinct(tally, args), args), nil
}

// distinctTexts is every non-empty text re matches in a file, a line at a
// time or, for a multiline search, across the whole text. A line ending's
// carriage return is not part of what was matched: the same name on a CRLF
// line and an LF one is one name.
func distinctTexts(text string, re *regexp.Regexp, args searchArgs) []string {
	var found []string
	keep := func(ms []string) {
		for _, m := range ms {
			m = strings.TrimSuffix(strings.ReplaceAll(m, "\r\n", "\n"), "\r")
			if m != "" {
				found = append(found, m)
			}
		}
	}
	if args.Multiline {
		keep(re.FindAllString(text, -1))
		return found
	}
	for _, line := range strings.Split(text, "\n") {
		keep(re.FindAllString(line, -1))
	}
	return found
}

// formatDistinct writes only_matching's answer, one line per text, bounded at
// the limit with a notice saying how many were left out.
func formatDistinct(tally map[string]*distinctMatch, args searchArgs) string {
	if len(tally) == 0 {
		return NoMatchesFound
	}
	all := make([]*distinctMatch, 0, len(tally))
	for _, d := range tally {
		all = append(all, d)
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.count != b.count {
			return a.count > b.count
		}
		if a.files != b.files {
			return a.files > b.files
		}
		return a.text < b.text
	})
	shown := all[:min(len(all), args.limit)]
	lines := make([]string, 0, len(shown)+1)
	for _, d := range shown {
		lines = append(lines, formatDistinctLine(d))
	}
	if len(all) > len(shown) {
		lines = append(lines, fmt.Sprintf("… (truncated at %d of %d distinct matches; narrow the pattern or path%s)",
			len(shown), len(all), raiseLimitHint(args.limit)))
	}
	return strings.Join(lines, "\n")
}

// formatDistinctLine puts the counts first and the text last, so whatever the
// text holds cannot be read as part of the line's own shape. A text that
// spans lines is written on one, its breaks as \n.
func formatDistinctLine(d *distinctMatch) string {
	text := strings.ReplaceAll(d.text, "\n", `\n`)
	if len(text) > MaxSearchLineBytes {
		text = cutUTF8(text, MaxSearchLineBytes) + " … (match truncated)"
	}
	times := fmt.Sprintf("%d matches", d.count)
	if d.count == 1 {
		times = "1 match"
	}
	files := fmt.Sprintf("%d files", d.files)
	if d.files == 1 {
		files = "1 file"
	}
	return fmt.Sprintf("%s in %s: %s", times, files, text)
}

func formatMatch(path, lineNo, sep, text string) string {
	text = strings.TrimRight(text, "\r")
	if len(text) > MaxSearchLineBytes {
		text = cutUTF8(text, MaxSearchLineBytes) + " … (line truncated)"
	}
	return fmt.Sprintf("%s:%s%s %s", path, lineNo, sep, text)
}

func formatFileCount(path, count string) string {
	n, err := strconv.Atoi(strings.TrimSpace(count))
	if err != nil {
		return path
	}
	if n == 1 {
		return fmt.Sprintf("%s: 1 match", path)
	}
	return fmt.Sprintf("%s: %d matches", path, n)
}

// raiseLimitHint offers the limit argument in a truncation notice, and says
// nothing once the ceiling is what was hit: a notice that tells the reader to
// raise a number already at its maximum spends the round it exists to save.
func raiseLimitHint(limit int) string {
	if limit >= MaxSearchLimit {
		return ""
	}
	return fmt.Sprintf(", or raise limit to at most %d", MaxSearchLimit)
}

func formatSearchResults(results []string, matches int, args searchArgs) string {
	if len(results) == 0 {
		return NoMatchesFound
	}
	out := strings.Join(results, "\n")
	if args.FilesOnly {
		if matches >= args.limit {
			out += fmt.Sprintf("\n… (truncated at %d files; narrow the pattern or path%s)", args.limit, raiseLimitHint(args.limit))
		}
		return out
	}
	if matches >= args.limit {
		out += fmt.Sprintf("\n… (truncated at %d matches; narrow the pattern or path%s, or use files_only to see which files are involved)",
			args.limit, raiseLimitHint(args.limit))
	}
	return out
}

// SearchSize is what a search result says about its own size: how many things
// it quotes, whether those are files or matched lines, and whether the tool
// stopped at its cap with more left to find.
type SearchSize struct {
	N         int
	Files     bool
	Truncated bool
}

// searchMatchLine is the shape formatMatch writes — a path, a line number,
// and the separator saying whether the line matched (`:`) or is context
// around one that did (`-`). The path is taken lazily so a matched line
// quoting something like `3:4-` is still read by its own prefix rather than
// by the code it found.
var searchMatchLine = regexp.MustCompile(`^.+?:\d+([:-])`)

// searchFileLine is the shape formatFileCount writes, which is the whole of a
// files_only result: one line per file, not per match.
var searchFileLine = regexp.MustCompile(`^.+: \d+ match(?:es)?$`)

// searchDistinctLine is the shape formatDistinctLine writes. It is read
// before the other two, because the text it ends with is the matched text and
// may look like either of them.
var searchDistinctLine = regexp.MustCompile(`^(\d+) match(?:es)? in \d+ files?: `)

// MeasureSearch reads a search result back into the count the tool had when
// it wrote it, so a reader can say how much was found rather than how much
// was printed.
//
// Those are different numbers. A result carries up to MaxSearchContextLines
// around every match, a separator between groups that do not touch, and a
// notice when the cap was reached, so fifty matches arrive as three hundred
// lines — and a reader that measured the rendering would report the search as
// six times more thorough than it was, at the moment the tool was saying it
// had been cut short. It lives here, beside the formatter, because it is the
// same knowledge: the separator after the line number is what says which
// lines matched, and nothing else can know that. Nothing carries a count out
// of Execute — a tool returns a string, and a row is rebuilt from the stored
// result when a session is reopened — so the format is the channel.
func MeasureSearch(result string) SearchSize {
	var size SearchSize
	body := strings.TrimRight(result, "\n")
	if body == "" || body == NoMatchesFound {
		return size
	}
	for _, line := range strings.Split(body, "\n") {
		d := searchDistinctLine.FindStringSubmatch(line)
		switch m := searchMatchLine.FindStringSubmatch(line); {
		case TruncationNotice(line):
			size.Truncated = true
		case d != nil:
			// An only_matching line stands for as many matches as it counts.
			n, _ := strconv.Atoi(d[1])
			size.N += n
		case m != nil:
			if m[1] == ":" {
				size.N++
			}
		case searchFileLine.MatchString(line):
			size.Files, size.N = true, size.N+1
		}
	}
	return size
}

// isBinary reports whether a file is one search has nothing to quote from.
// It asks read_file's question, over the same window, so the two readers
// cannot disagree about which files are text — a file search skipped and
// read_file then returned as lines would be one the model was told twice
// about, differently.
//
// A file that cannot be opened or is empty counts as binary: there is nothing
// in it to match, and the walk has better uses for the round. So does a pipe,
// a socket or a device, decided before the open, which on a pipe would wait.
func isBinary(path string) bool {
	if info, err := os.Stat(path); err != nil || notRegular(path, info) != nil {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()
	buf := make([]byte, SniffBytes)
	n, err := io.ReadFull(f, buf)
	if n == 0 || (err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF)) {
		return true
	}
	_, text := sniffText(buf[:n])
	return !text
}
