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

var search = Definition{
	Tool: provider.Tool{
		Name:        "search",
		Description: "Search file contents with a regular expression (RE2 syntax). Case-insensitive by default. Returns matching lines as path:line: text. Skips .git, node_modules, and vendor directories.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"pattern": {"type": "string", "description": "Regular expression to search for (case-insensitive unless case_sensitive is true)"},
				"path": {"type": "string", "description": "Optional directory or file path to search in (defaults to current directory)"},
				"case_sensitive": {"type": "boolean", "description": "Match case-sensitively (default false)"}
			},
			"required": ["pattern"]
		}`),
	},
	Execute: executeSearch,
}

type searchArgs struct {
	Pattern       string `json:"pattern"`
	Path          string `json:"path"`
	CaseSensitive bool   `json:"case_sensitive"`
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

	if _, err := os.Stat(args.Path); err != nil {
		return "", fmt.Errorf("cannot access path: %w", err)
	}

	if rg, ok := lookupRg(); ok {
		results, err := searchWithRipgrep(rg, args)
		if err == nil {
			return formatSearchResults(results), nil
		}
		// Ripgrep failed (e.g. a pattern its regex engine rejects): fall
		// through to the pure-Go walker.
	}

	results, err := searchWithWalker(re, args.Path)
	if err != nil {
		return "", err
	}
	return formatSearchResults(results), nil
}

// searchWithRipgrep shells out to rg for speed. The excluded directories
// mirror the walker's skip list; --with-filename keeps single-file output in
// the same path:line format, and --null makes the path separator unambiguous.
func searchWithRipgrep(rg string, args searchArgs) ([]string, error) {
	argv := []string{
		"--line-number", "--no-heading", "--with-filename", "--color=never",
		"--no-messages", "--null",
		"--max-columns", strconv.Itoa(MaxSearchLineBytes), "--max-columns-preview",
		"--glob", "!.git", "--glob", "!node_modules", "--glob", "!vendor",
	}
	if !args.CaseSensitive {
		argv = append(argv, "--ignore-case")
	}
	argv = append(argv, "--regexp", args.Pattern, "--", args.Path)

	cmd := exec.Command(rg, argv...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var results []string
	for len(results) < MaxSearchResults && scanner.Scan() {
		line := scanner.Text()
		nul := strings.IndexByte(line, 0)
		if nul < 0 {
			continue
		}
		path, rest := line[:nul], line[nul+1:]
		colon := strings.IndexByte(rest, ':')
		if colon < 0 {
			continue
		}
		results = append(results, formatMatch(path, rest[:colon], rest[colon+1:]))
	}
	stoppedEarly := len(results) >= MaxSearchResults || scanner.Err() != nil
	if stoppedEarly {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()

	if stoppedEarly && len(results) > 0 {
		return results, nil
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil // exit 1 means no matches
		}
		if len(results) > 0 {
			// Partial errors (e.g. unreadable files) still produced matches.
			return results, nil
		}
		return nil, fmt.Errorf("ripgrep failed: %w", waitErr)
	}
	return results, nil
}

// searchWithWalker is the pure-Go fallback: walk the tree, skipping the
// standard directories plus binary and oversized files.
func searchWithWalker(re *regexp.Regexp, root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("cannot access path: %w", err)
	}
	if !info.IsDir() {
		return searchFile(root, re, nil), nil
	}

	var results []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (name == ".git" || name == "node_modules" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if len(results) >= MaxSearchResults {
			return filepath.SkipAll
		}
		if fi, err := d.Info(); err != nil || fi.Size() > MaxSearchFileBytes {
			return nil
		}
		if isBinary(path) {
			return nil
		}
		results = searchFile(path, re, results)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("search error: %w", err)
	}
	return results, nil
}

func searchFile(path string, re *regexp.Regexp, results []string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return results
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if len(results) >= MaxSearchResults {
			break
		}
		if re.MatchString(line) {
			results = append(results, formatMatch(path, strconv.Itoa(i+1), line))
		}
	}
	return results
}

func formatMatch(path, lineNo, text string) string {
	text = strings.TrimRight(text, "\r")
	if len(text) > MaxSearchLineBytes {
		text = cutUTF8(text, MaxSearchLineBytes) + " … (line truncated)"
	}
	return fmt.Sprintf("%s:%s: %s", path, lineNo, text)
}

func formatSearchResults(results []string) string {
	if len(results) == 0 {
		return "No matches found."
	}
	out := strings.Join(results, "\n")
	if len(results) >= MaxSearchResults {
		out += fmt.Sprintf("\n… (truncated at %d results; narrow the pattern or path to see more)", MaxSearchResults)
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
