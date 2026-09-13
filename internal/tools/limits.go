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
	// results and /run context messages. What it cuts is the middle: the cap
	// is spent on both ends (BoundExecOutput), never on a prefix.
	MaxExecOutputBytes = 4000

	// execHeadShare is the fraction of the budget the verbatim head takes;
	// the rest is the tail. It is the proportion the quality gate's excerpt
	// already cuts a check's output at, for the reason that applies here
	// unchanged: a compiler or a linter says what is wrong first and stops,
	// while a test run says it first and counts it last, so the end that
	// carries the verdict is the end worth the larger share.
	execHeadShare = 4

	// execNoticeRoom is what the line between the two ends costs at its
	// widest — the byte count, the evidence id and the sentence naming the
	// tool that pages it back. The two ends are cut to leave room for it,
	// because a bound that the sentence about the bound pushes past is not
	// one.
	execNoticeRoom = 192
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

// ExecOutcome is how an execute_command invocation ended. It is carried to
// the formatter rather than inferred from its output: output is evidence from
// the command, not a protocol the tool result parser should have to recognise.
type ExecOutcome string

const (
	ExecSucceeded   ExecOutcome = "succeeded"
	ExecExited      ExecOutcome = "exited"
	ExecSignaled    ExecOutcome = "signaled"
	ExecTimedOut    ExecOutcome = "timed out"
	ExecStopped     ExecOutcome = "stopped"
	ExecDidNotStart ExecOutcome = "did not start"
	ExecHandedOff   ExecOutcome = "handed off"
)

// didNotStart is the first line of every result for a command that never
// became a process, classified or not. It is a constant because two things
// depend on the exact words: the `error:` convention the digest and the hooks
// read, and ExecPrereqOf, which will not read a category out of a result that
// does not open with it.
const didNotStart = "error: command did not start"

// ExecPrereq is the harness prerequisite a command needed and did not have.
// It is the closed vocabulary of ways a command can fail for a reason that is
// not the command's: the directory it was to run in, the shell that runs it,
// the containment it runs under, the operating system's permission to run it
// at all, and the spawn itself.
//
// It qualifies ExecDidNotStart and nothing else. A command that ran and
// failed — a compiler with an error, a shell answering `command not found`
// for a program that is not installed — is the command's own output and is
// never one of these: over-classifying it would tell a model to look at the
// machine when what is wrong is the line it wrote.
//
// The values are codes rather than sentences because they are what a record
// and an event stream carry, and prose is not a code. The sentence a person
// or a model reads is ExecPrereqReport.
type ExecPrereq string

const (
	// PrereqWorkingDir: the directory the command was to run in is not
	// there. A worktree or checkout removed under a running session is what
	// this usually is.
	PrereqWorkingDir ExecPrereq = "working-directory"
	// PrereqShell: the shell every captured command is run through is not
	// available on this host (shell.Execution).
	PrereqShell ExecPrereq = "execution-shell"
	// PrereqContainment: the mechanism that was to contain the command is
	// not available, and a contained command is never run uncontained.
	PrereqContainment ExecPrereq = "containment"
	// PrereqPermission: the operating system refused to run it.
	PrereqPermission ExecPrereq = "permission"
	// PrereqSpawn: the spawn failed for a reason none of the above names. It
	// is the bucket that keeps the four above honest — a category nothing
	// falls out of is one that has started guessing.
	PrereqSpawn ExecPrereq = "spawn"
)

// prereqLabel is the category as a reader meets it: the first words of the
// status line and the first words of the report, so the two say the same
// thing and ExecPrereqOf has one string to look for rather than two.
func prereqLabel(p ExecPrereq) string {
	switch p {
	case PrereqWorkingDir:
		return "working directory unavailable"
	case PrereqShell:
		return "execution shell unavailable"
	case PrereqContainment:
		return "containment unavailable"
	case PrereqPermission:
		return "permission denied"
	case PrereqSpawn:
		return "spawn refused"
	}
	return ""
}

// prereqAdvice is the narrow next action, which is the only part of the
// report that is not a fact. Each one is what remains possible from where the
// reader is standing, and none of them is a way around a decision: a session
// that will not run a command uncontained is not offered a command that runs
// uncontained, and an operating system's refusal is not answered with a way
// to stop being refused.
func prereqAdvice(p ExecPrereq) string {
	switch p {
	case PrereqWorkingDir:
		return "Nothing ran: a command's working directory has to be there when it starts. " +
			"Find out where it went — a worktree or checkout removed under a running session is the usual cause — " +
			"and run the command somewhere that still exists."
	case PrereqShell:
		return "Nothing ran, and nothing will until that shell is back: every command here goes through it, " +
			"so another spelling of the same command reaches the same missing program. Report it."
	case PrereqContainment:
		return "Nothing ran, and it was not run uncontained — that is the one substitution this session will not make. " +
			"Report the mechanism as missing; `shhh doctor` says what this host needs."
	case PrereqPermission:
		return "Nothing ran. The refusal is the operating system's and not a decision this session made or can undo, " +
			"so use a program and a path the current user is already allowed, or report what is refusing it."
	case PrereqSpawn:
		return "Nothing ran, and the reason above is the host's rather than the command's. " +
			"Retry only if that reason names something that has since changed; otherwise report it."
	}
	return ""
}

// ExecPrereqReport is the harness's own account of a command that never ran:
// the category, the operating system's words for it kept verbatim, and the
// one thing the reader can still do about it.
//
// It is composed here rather than where the classification is made because
// every route a command's result takes reads it — the typed result the tool
// formatter builds, and the older output/status pair the chat, a child and a
// contained runner still hand back — and a wording that existed twice would
// be two wordings within a release.
//
// The detail leads because it is the fact: a model that already knows what a
// missing directory means needs the path, and everything after the first line
// is what it does not know.
func ExecPrereqReport(p ExecPrereq, detail string) string {
	label := prereqLabel(p)
	if label == "" {
		return strings.TrimSpace(detail)
	}
	if detail = strings.TrimSpace(detail); detail != "" {
		label += ": " + detail
	}
	return label + "\n" + prereqAdvice(p)
}

// ExecPrereqOf is the category read back out of a formatted result, for the
// consumers that are handed the text and not the typed result — the session
// record and the event stream, which have to file this under the same closed
// category every other surface shows.
//
// It reads only a result that says the command did not start, so a command
// that ran and printed the word "containment" is not filed as a harness
// failure. Within one of those the text is the harness's own: the command
// never ran, so nothing else printed anything.
func ExecPrereqOf(result string) ExecPrereq {
	if !strings.HasPrefix(result, didNotStart) {
		return ""
	}
	for _, p := range []ExecPrereq{PrereqWorkingDir, PrereqShell, PrereqContainment, PrereqPermission, PrereqSpawn} {
		if strings.Contains(result, prereqLabel(p)) {
			return p
		}
	}
	return ""
}

// ExecResult is one command's output and the fact of how it ended. ExitCode is
// retained for the activity row and callers that need an ordinary process
// status; Outcome is the answer every tool-result consumer needs.
type ExecResult struct {
	Output   string
	ExitCode int
	Outcome  ExecOutcome
	// Prereq is the harness prerequisite that failed, where the runner
	// classified one. It qualifies ExecDidNotStart and is empty on every
	// other outcome — and empty on a did-not-start the runner could not
	// classify, which stays the sentence it was before.
	Prereq ExecPrereq
}

// Failed reports whether the command did not complete successfully. A handoff
// is working elsewhere under the process supervisor, so it is deliberately not
// an error result.
func (r ExecResult) Failed() bool {
	return r.Outcome != ExecSucceeded && r.Outcome != ExecHandedOff
}

// InferExecResult converts the legacy output/status runner seam. New runners
// supply a more precise Outcome; this fallback keeps existing execution seams
// compatible while never treating a non-zero status as success.
func InferExecResult(output string, exitCode int) ExecResult {
	result := ExecResult{Output: output, ExitCode: exitCode}
	switch {
	case exitCode == 0:
		result.Outcome = ExecSucceeded
	case exitCode > 0:
		result.Outcome = ExecExited
	case exitCode < -1:
		result.Outcome = ExecSignaled
	default:
		result.Outcome = ExecDidNotStart
	}
	return result
}

// ExecKeep stores output the model-visible bound is about to cut and answers
// with the opaque evidence id that pages it back, or false where the store
// would not take it. It is handed in rather than reached for, because this
// package must not know what an evidence store is; nil is a session with
// nowhere to put the middle, which is a bound that says what went and offers
// nothing back.
// See docs/capabilities/evidence.md#the-reader-can-always-get-the-whole-thing-back.
type ExecKeep func(tool, content string) (id string, ok bool)

// FormatExecResult formats a command's captured result for an approved
// execute_command call with no evidence store behind it. All failures lead
// with the error convention the digest, hooks, repeat detector and observers
// read.
func FormatExecResult(result ExecResult) string {
	return FormatExecResultKeeping(result, nil)
}

// FormatExecResultKeeping is FormatExecResult with somewhere to put what the
// cap cuts, so the notice between the two ends can name the entry that holds
// the whole of it.
func FormatExecResultKeeping(result ExecResult, keep ExecKeep) string {
	return execStatusLine(result) + "\noutput:\n" + BoundExecOutput(result.Output, keep)
}

// execStatusLine is the result's first line: the process status for a command
// that succeeded, and the `error:` convention for every other ending, because
// that prefix is what the digest, hooks, repeat detector and observers read.
//
// It is also the one place a failure of the harness is told apart from a
// command that ran and failed. The classified prerequisite — a working
// directory that has gone, no shell, no containment, a refusal, a spawn that
// failed — refines ExecDidNotStart and no other case, and nothing else in the
// formatter has to learn about it. An unclassified one is the sentence it
// always was.
func execStatusLine(result ExecResult) string {
	if !result.Failed() {
		return fmt.Sprintf("exit code: %d", result.ExitCode)
	}
	switch result.Outcome {
	case ExecExited:
		return fmt.Sprintf("error: command exited with status %d", result.ExitCode)
	case ExecSignaled:
		return fmt.Sprintf("error: command was killed by signal %d", -result.ExitCode)
	case ExecTimedOut:
		return "error: command timed out"
	case ExecStopped:
		return "error: command was stopped before it finished"
	case ExecDidNotStart:
		if label := prereqLabel(result.Prereq); label != "" {
			return didNotStart + ": " + label
		}
		return didNotStart
	default:
		return "error: command did not complete"
	}
}

// BoundExecOutput holds one command's output to MaxExecOutputBytes for the
// model, keeping a verbatim head and a verbatim tail with a line between them
// saying what fell out.
//
// It is not a prefix cut, and that is the whole of the change: the runner
// already bounds a chatty command at both ends (CaptureBuffer) precisely
// because the tail is where a command says how it went, and a second,
// prefix-only cut on top of that threw the surviving tail away again — so a
// `go build` that failed on its last file was reported by its warmup, and the
// model's cheapest way out was to run the whole thing again with `| tail`.
//
// Both ends are cut on a line boundary where there is one, because the
// diagnostic the reader is here for is a line, and on a UTF-8 boundary
// otherwise, because a single enormous line still must not reach the model as
// a replacement character.
func BoundExecOutput(output string, keep ExecKeep) string {
	output = strings.TrimRight(output, "\n")
	if len(output) > MaxExecOutputBytes {
		head, tail, omitted := splitExecOutput(output, MaxExecOutputBytes-execNoticeRoom)
		parts := make([]string, 0, 3)
		if head != "" {
			parts = append(parts, head)
		}
		parts = append(parts, execOmissionNotice(omitted, keep, output))
		if tail != "" {
			parts = append(parts, tail)
		}
		output = strings.Join(parts, "\n")
	}
	if strings.TrimSpace(output) == "" {
		return "(no output)"
	}
	return output
}

// splitExecOutput cuts s to a verbatim head and a verbatim tail totalling at
// most budget bytes, and reports how many bytes fell between them. A budget
// with no room in it keeps neither end, which leaves the notice alone to say
// what the output was.
func splitExecOutput(s string, budget int) (head, tail string, omitted int) {
	if budget > 0 {
		headMax := budget / execHeadShare
		head = wholeLinesHead(cutUTF8(s, headMax))
		tail = wholeLinesTail(trimPartialRune(s[len(s)-(budget-headMax):]))
	}
	return head, tail, len(s) - len(head) - len(tail)
}

// execOmissionNotice is the line between the two ends.
//
// Where the session has an evidence store the middle goes into it and the
// notice names the id in the one wording the toolbox teaches — a notice
// carrying an id is paged back with the evidence tool — so the model is told
// that once and it covers a reduction, a window trim and this. Where it has
// none the notice still says exactly how much went and what to do instead,
// because a gap with nothing in it reads as the command having stopped
// printing.
// See docs/capabilities/evidence.md#the-reader-can-always-get-the-whole-thing-back.
func execOmissionNotice(omitted int, keep ExecKeep, full string) string {
	if keep != nil {
		if id, ok := keep(ExecCommandName, full); ok {
			return fmt.Sprintf("… (%d bytes from the middle omitted; full output stored as evidence %s — retrieve it with the evidence tool (info/read/search))", omitted, id)
		}
	}
	return fmt.Sprintf("… (%d bytes from the middle omitted; this session has nowhere to store them, so narrow the command if you need them)", omitted)
}

// wholeLinesHead drops a partial last line, so a head ends where a line does.
// A head with no line break in it at all is kept as it is: one very long line
// is still the only thing there is to show.
func wholeLinesHead(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}

// wholeLinesTail drops a partial first line, on the same terms.
func wholeLinesTail(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 && i < len(s)-1 {
		return s[i+1:]
	}
	return s
}

// trimPartialRune drops the leading bytes of a UTF-8 sequence a byte cut
// split, so a tail does not open on a replacement character.
func trimPartialRune(s string) string {
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
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
