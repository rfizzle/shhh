package tools

import "unicode/utf8"

// Tool output caps (S-051). Every limit on how much a tool may feed back into
// the model's context lives here, so there is one place to tune them.
const (
	// MaxReadFileLines and MaxReadFileBytes cap read_file output; whichever
	// limit is hit first wins, and the result carries a paging notice.
	MaxReadFileLines = 2000
	MaxReadFileBytes = 65536

	// MaxSearchResults caps how many matching lines search returns.
	MaxSearchResults = 50
	// MaxSearchLineBytes caps a single matched line, so one minified file
	// cannot dominate a search result.
	MaxSearchLineBytes = 400

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
