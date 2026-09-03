package tools

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Tool output caps. Every limit on how much a tool may feed back into
// the model's context lives here, so there is one place to tune them.
const (
	// MaxReadFileLines and MaxReadFileBytes cap read_file output; whichever
	// limit is hit first wins, and the result carries a paging notice.
	MaxReadFileLines = 2000
	MaxReadFileBytes = 65536

	// MaxReadFileSize is the largest file read_file will open at all. It is
	// checked against the file's stat before anything is read, because the
	// cost it bounds is the read itself: the caps above are applied to bytes
	// already in memory, so a path that lands on a database or a packed
	// archive spends the machine before either of them says no.
	//
	// It is far above anything a reader pages through — the largest source
	// file in this repository is under 200 KB and the largest generated file
	// in the Go toolchain is under 3 MB — and far below the sizes this is
	// for. A file over it is not one to read in windows; it is one to search,
	// or to take a part of with a command.
	MaxReadFileSize = 10 << 20

	// SniffBytes is how much of a file is read to decide whether it is text.
	// It is git's number: git calls a file binary on a NUL byte in its first
	// 8000, and a reader that disagreed with git about what is text would be
	// describing files one way while search and the diffs describe them
	// another.
	SniffBytes = 8 << 10

	// MaxSearchResults caps how many matching lines search returns.
	MaxSearchResults = 50
	// MaxSearchLineBytes caps a single matched line, so one minified file
	// cannot dominate a search result.
	MaxSearchLineBytes = 400

	// MaxSearchContextLines caps the context a search may show around each
	// match. Enough to read a signature and its body's first lines, which is
	// what stops the round after the search from being a read of the same
	// place.
	MaxSearchContextLines = 5

	// MaxSearchFileResults caps files_only output. One line per file is
	// cheap, and "which files are involved" is a question worth answering
	// across a whole repository.
	MaxSearchFileResults = 200

	// MaxSearchFileBytes caps the size of a file the pure-Go search fallback
	// will read; larger files are skipped (ripgrep bounds its own reads).
	MaxSearchFileBytes = 1 << 20

	// MaxListEntries caps how many entries list_directory returns.
	MaxListEntries = 500

	// MaxGlobResults caps how many file paths glob returns.
	MaxGlobResults = 500

	// MaxExecOutputBytes caps captured command output embedded in tool
	// results and /run context messages.
	MaxExecOutputBytes = 4000
)

// TruncateOutput caps s at max bytes without splitting a UTF-8 sequence. It
// reports whether anything was cut; callers append their own tool-appropriate
// truncation notice.
func TruncateOutput(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	return cutUTF8(s, max), true
}

// FormatExecResult formats a command's captured output and exit code as the
// tool result for an approved execute_command call, applying the shared
// output cap. Both the chat TUI and headless print mode record this format.
func FormatExecResult(output string, exitCode int) string {
	output = strings.TrimRight(output, "\n")
	if cut, truncated := TruncateOutput(output, MaxExecOutputBytes); truncated {
		output = cut + "\n… (output truncated)"
	}
	if strings.TrimSpace(output) == "" {
		output = "(no output)"
	}
	return fmt.Sprintf("exit code: %d\noutput:\n%s", exitCode, output)
}

// cutUTF8 truncates s to at most max bytes, dropping any trailing partial
// UTF-8 sequence rather than emitting invalid bytes.
func cutUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.RuneStart(s[len(s)-1]) {
		s = s[:len(s)-1]
	}
	if r, size := utf8.DecodeLastRuneInString(s); r == utf8.RuneError && size == 1 {
		s = s[:len(s)-1]
	}
	return s
}
