package tools

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
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
			"Returns matching lines as path:line: text. Skips .git, node_modules, and vendor directories. " +
			"Each match comes with two lines of context by default, so the answer arrives with the hit instead of in the round after it; " +
			"set context_lines to widen or narrow that, files_only to find which files are involved without quoting any, " +
			"and include to limit the search to one kind of file.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"pattern": {"type": "string", "description": "Regular expression to search for (case-insensitive unless case_sensitive is true)"},
				"path": {"type": "string", "description": "Optional directory or file path to search in (defaults to current directory)"},
				"case_sensitive": {"type": "boolean", "description": "Match case-sensitively (default false)"},
				"include": {"type": "string", "description": "Optional glob limiting which files are searched, e.g. *.go or internal/**/*_test.go"},
				"context_lines": {"type": "integer", "description": "Lines of context to show around each match (0-5, default 2). Pass 0 for matching lines alone. Context lines are shown as path-line- text"},
				"files_only": {"type": "boolean", "description": "Return one line per matching file with its match count instead of the matching lines. Use it to find where something lives"}
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

	// context is ContextLines resolved against the default, which is what
	// every backend actually reads.
	context int
}

// lookupRg reports where ripgrep lives, if it is on PATH. A variable so tests
// can force the pure-Go fallback path.
var lookupRg = func() (string, bool) {
	path, err := exec.LookPath("rg")
	return path, err == nil
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

	// Validate the pattern up front so both backends reject the same inputs
	// with the same error.
	expr := args.Pattern
	if !args.CaseSensitive {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
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
func searchWithRipgrep(rg string, args searchArgs) (results []string, matches int, err error) {
	argv := []string{
		"--line-number", "--no-heading", "--with-filename", "--color=never",
		"--no-messages", "--null",
		"--max-columns", strconv.Itoa(MaxSearchLineBytes), "--max-columns-preview",
		"--glob", "!.git", "--glob", "!node_modules", "--glob", "!vendor",
	}
	if !args.CaseSensitive {
		argv = append(argv, "--ignore-case")
	}
	if args.Include != "" {
		argv = append(argv, "--glob", args.Include)
	}
	limit := MaxSearchResults
	switch {
	case args.FilesOnly:
		// --count-matches answers "where does this live, and how much of it
		// is there" in one line per file, which is the question files_only
		// is for. --line-number is meaningless with it and rg says so.
		argv = append(argv, "--count-matches")
		argv = removeArg(argv, "--line-number")
		limit = MaxSearchFileResults
	case args.context > 0:
		argv = append(argv, "--context", strconv.Itoa(args.context))
	}
	argv = append(argv, "--regexp", args.Pattern, "--", args.Path)

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
// standard directories plus binary and oversized files.
func searchWithWalker(re *regexp.Regexp, include *includeMatcher, args searchArgs) (results []string, matches int, err error) {
	info, err := os.Stat(args.Path)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot access path: %w", err)
	}
	limit := MaxSearchResults
	if args.FilesOnly {
		limit = MaxSearchFileResults
	}
	if !info.IsDir() {
		results, matches = searchFile(args.Path, re, args, nil, 0, limit)
		return results, matches, nil
	}

	err = filepath.WalkDir(args.Path, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if p != args.Path && skipWalk(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if matches >= limit {
			return filepath.SkipAll
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

func formatSearchResults(results []string, matches int, args searchArgs) string {
	if len(results) == 0 {
		return "No matches found."
	}
	out := strings.Join(results, "\n")
	if args.FilesOnly {
		if matches >= MaxSearchFileResults {
			out += fmt.Sprintf("\n… (truncated at %d files; narrow the pattern or path to see more)", MaxSearchFileResults)
		}
		return out
	}
	if matches >= MaxSearchResults {
		out += fmt.Sprintf("\n… (truncated at %d matches; narrow the pattern or path, or use files_only to see which files are involved)", MaxSearchResults)
	}
	return out
}

func isBinary(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return true
	}
	for _, b := range buf[:n] {
		if b == 0 {
			return true
		}
	}
	return false
}
