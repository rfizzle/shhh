package tools

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
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

	// MaxSearchLimit is the ceiling on search's own limit argument, which is
	// how a caller raises either default. The defaults above are sized for
	// the common question — the first fifty matches usually settle it — and
	// a cap with nothing above it is answered by re-running the same search
	// with a longer pattern, which costs a round and finds the same lines.
	// The ceiling is where a result stops being an answer and becomes a file
	// to read: five hundred matched lines is what fd already allows itself
	// in paths, and beyond it files_only is the shorter question.
	MaxSearchLimit = 500

	// MaxGlobLimit is the ceiling on glob's limit argument. It is
	// MaxGlobResults, so limit only ever narrows: a path list is already the
	// cheapest answer any reader here returns, and there is no question
	// five hundred paths leaves open that a longer list closes — the pattern
	// is what narrows it.
	MaxGlobLimit = MaxGlobResults

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

// The sentences a reader returns when it found nothing. They are sentences
// rather than an empty result because a model handed nothing at all reads it
// as a broken tool and calls again — and they are named because anything
// measuring a result has to know that one line of prose is not one item.
//
// NoMatchesFound is search's. NoFilesMatched is glob's and fd's, which ask
// the same question of a tree and so answer a fruitless one in the same
// words.
const (
	NoMatchesFound = "No matches found."
	NoFilesMatched = "No files matched."
)

// TruncationNotice reports whether a line is a bounded reader's own "there is
// more" sentence rather than a part of the answer.
//
// Every cap above is announced the same way — `… (truncated at 50 matches;
// …)`, `… (truncated at 500 entries; …)`, fd's `… (results capped at 200;
// …)` — and the shape is what anything counting a result has to know: a path
// list is one line per path apart from this one, so a count that swallowed it
// would report one item more than the tool found, on exactly the results
// where the number is being read as thoroughness.
// See docs/interface/principles.md#one-grid.
func TruncationNotice(line string) bool {
	return strings.HasPrefix(line, "… (")
}

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

// MaxCapturedOutputBytes bounds what one running command's output may hold in
// memory before anything is truncated for the model. It is more than two
// hundred and fifty times MaxExecOutputBytes, so nothing a reader or a model
// was ever going to see is affected by it: what it bounds is the command that
// prints faster than anyone reads — a build with a verbose flag left on, a
// watcher looping on an error — whose output would otherwise be held whole,
// in the turn's memory, until it finished.
const MaxCapturedOutputBytes = 1 << 20

// CaptureBuffer accumulates a command's output up to a byte bound, counting
// what it had to drop. It keeps both ends — the first half of the bound
// verbatim, the last half in a ring — and drops the middle.
//
// The tail is kept because the tail is where a command says how it went: a
// build prints its verdict last, a test run summarises last, an installer
// names the package it died on last. A buffer that kept only the head handed
// every reader below it — the reduction the model is shown, the evidence
// store fed from the same bytes — the tail of the first megabyte instead, so
// a three-megabyte `go build` was reported by its warmup, and the model's
// cheapest way out was to run the whole thing again with `| tail`.
//
// The head is kept as well because a failure's first line is often its cause
// (the first compile error, the first stack frame), and because a reader who
// asked for a command's output and got its last screen would have no idea
// what it started doing.
type CaptureBuffer struct {
	max int

	mu   sync.Mutex
	head []byte
	// tail is a ring of the most recent bytes, at most max less the head's
	// bound. start is the index of its oldest byte, and stays 0 until the
	// ring fills.
	tail    []byte
	start   int
	dropped int64
}

// NewCaptureBuffer returns a buffer that keeps at most max bytes. A max of
// zero or less keeps everything, which is the caller saying it has its own
// bound.
func NewCaptureBuffer(max int) *CaptureBuffer {
	return &CaptureBuffer{max: max}
}

// headBound is how much of max the verbatim head may take; the rest is the
// tail's ring. An even split needs no argument for one end over the other,
// and each half is still two orders of magnitude above anything a reader or
// a model is shown.
func (b *CaptureBuffer) headBound() int { return b.max / 2 }

// Write appends what it can and counts the rest.
//
// It always reports having written the whole slice: this is os/exec's output
// copier on the other end, which reads a short write as a broken pipe and
// tears the capture down, so a buffer that reported the truth about a full
// buffer would turn a chatty command into one with no output at all.
func (b *CaptureBuffer) Write(p []byte) (int, error) {
	wrote := len(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.max <= 0 {
		b.head = append(b.head, p...)
		return wrote, nil
	}
	if room := b.headBound() - len(b.head); room > 0 {
		if len(p) <= room {
			b.head = append(b.head, p...)
			return wrote, nil
		}
		b.head = append(b.head, p[:room]...)
		p = p[room:]
	}
	b.push(p)
	return wrote, nil
}

// push adds p to the tail ring, counting whatever it displaces as dropped.
// The caller holds the lock and has already filled the head.
func (b *CaptureBuffer) push(p []byte) {
	max := b.max - b.headBound()
	if max <= 0 {
		b.dropped += int64(len(p))
		return
	}
	if len(p) >= max {
		// One write bigger than the ring: everything in it goes, and so does
		// all of p but its last max bytes.
		b.dropped += int64(len(b.tail)) + int64(len(p)-max)
		b.tail = append(b.tail[:0], p[len(p)-max:]...)
		b.start = 0
		return
	}
	if room := max - len(b.tail); room > 0 {
		n := min(room, len(p))
		b.tail = append(b.tail, p[:n]...)
		p = p[n:]
		if len(p) == 0 {
			return
		}
	}
	// Full from here, so every byte written displaces one.
	b.dropped += int64(len(p))
	n := copy(b.tail[b.start:], p)
	copy(b.tail, p[n:])
	b.start = (b.start + len(p)) % max
}

// Bytes is what was kept: the head, a line naming what was dropped, and the
// tail. The notice is part of the bytes rather than a second return value
// because every reader of a command's output is a place a silent gap would be
// read as the command having stopped printing — and with both ends kept, a
// gap with nothing in it would read as two runs glued together.
func (b *CaptureBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.dropped == 0 {
		// Nothing was displaced, so the ring never wrapped and the two
		// halves are one contiguous run of the command's output.
		out := make([]byte, 0, len(b.head)+len(b.tail))
		return append(append(out, b.head...), b.tail...)
	}
	notice := fmt.Sprintf("\n… (%d bytes from the middle were dropped: one command's output is held to %d bytes while it runs, so both ends survive)\n",
		b.dropped, b.max)
	head := bytes.TrimRight(b.head, "\n")
	tail := b.ordered()
	out := make([]byte, 0, len(head)+len(notice)+len(tail))
	out = append(out, head...)
	out = append(out, notice...)
	return append(out, tail...)
}

// ordered flattens the ring oldest byte first, dropping any leading bytes of
// a UTF-8 sequence the bound split: the ring starts wherever the command
// happened to be, and a lone continuation byte reaches the model as a
// replacement character in the middle of a word.
func (b *CaptureBuffer) ordered() []byte {
	out := make([]byte, 0, len(b.tail))
	out = append(out, b.tail[b.start:]...)
	out = append(out, b.tail[:b.start]...)
	for len(out) > 0 && !utf8.RuneStart(out[0]) {
		out = out[1:]
	}
	return out
}

// String is Bytes as text.
func (b *CaptureBuffer) String() string { return string(b.Bytes()) }

// Len is how many bytes the command has printed, dropped ones included.
func (b *CaptureBuffer) Len() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return int64(len(b.head)) + int64(len(b.tail)) + b.dropped
}
