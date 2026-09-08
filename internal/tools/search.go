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
// re-running with a longer pattern.
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
				"limit": {"type": "integer", "description": "Maximum matches to return (default 50, or 200 with files_only; max 500)"}
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
	Limit         int    `json:"limit"`

	// context is ContextLines resolved against the default, and limit is
	// Limit resolved against this mode's default and the ceiling. Both are
	// what every backend actually reads: a default applied twice, once per
	// backend, is a search that answers differently depending on which one
	// the machine has.
	context int
	limit   int
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
func searchExpr(args searchArgs) string {
	if args.CaseSensitive {
		return searchPattern(args)
	}
	return "(?i)" + searchPattern(args)
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
	args.limit = MaxSearchResults
	if args.FilesOnly {
		args.limit = MaxSearchFileResults
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

	if _, err := os.Stat(args.Path); err != nil {
		return "", fmt.Errorf("cannot access path: %w", err)
	}

	if rg, ok := lookupRg(); ok {
		results, matches, err := searchWithRipgrep(rg, args)
		if err == nil {
			return formatSearchResults(results, matches, args), nil
		}
		// Ripgrep failed (e.g. a pattern its regex engine rejects): fall
		// through to the pure-Go walker.
	}

	results, matches, err := searchWithWalker(re, include, args)
	if err != nil {
		return "", err
	}
	return formatSearchResults(results, matches, args), nil
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
	argv := []string{
		"--line-number", "--no-heading", "--with-filename", "--color=never",
		"--no-messages", "--null", "--hidden",
		"--max-columns", strconv.Itoa(MaxSearchLineBytes), "--max-columns-preview",
		"--glob", "!.git", "--glob", "!node_modules", "--glob", "!vendor",
	}
	if !args.CaseSensitive {
		argv = append(argv, "--ignore-case")
	}
	if args.Include != "" {
		argv = append(argv, "--glob", args.Include)
	}
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
	info, err := os.Stat(args.Path)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot access path: %w", err)
	}
	limit := args.limit
	if !info.IsDir() {
		results, matches = searchFile(args.Path, re, args, nil, 0, limit)
		return results, matches, nil
	}

	// Ripgrep honours .gitignore of its own accord, so the walker does too:
	// otherwise the same search answers differently depending on whether rg
	// happens to be installed on the machine.
	ignore := newWalkIgnore(args.Path)
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
		if matches >= limit {
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
		if isBinary(p) {
			return nil
		}
		results, matches = searchFile(p, re, args, results, matches, limit)
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("search error: %w", err)
	}
	return results, matches, nil
}

// searchFile appends one file's hits to results, honouring files_only and the
// context window around each match. It returns the results and the running
// match count, which is what the cap is measured in — context lines ride
// along with the match that earned them.
func searchFile(path string, re *regexp.Regexp, args searchArgs, results []string, matches, limit int) ([]string, int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return results, matches
	}
	lines := strings.Split(string(data), "\n")

	var hits []int
	for i, line := range lines {
		if re.MatchString(line) {
			hits = append(hits, i)
			if args.FilesOnly {
				continue
			}
			if matches+len(hits) >= limit {
				break
			}
		}
	}
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
			sep := "-"
			if i == hit {
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
		switch m := searchMatchLine.FindStringSubmatch(line); {
		case TruncationNotice(line):
			size.Truncated = true
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
// in it to match, and the walk has better uses for the round.
func isBinary(path string) bool {
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
