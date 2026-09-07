package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateOutput_UnderCap(t *testing.T) {
	got, truncated := TruncateOutput("short", 100)
	if truncated {
		t.Fatal("expected no truncation")
	}
	if got != "short" {
		t.Errorf("expected input unchanged, got %q", got)
	}
}

func TestTruncateOutput_OverCap(t *testing.T) {
	got, truncated := TruncateOutput(strings.Repeat("a", 50), 10)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if got != strings.Repeat("a", 10) {
		t.Errorf("expected 10-byte cut, got %q", got)
	}
}

func TestTruncateOutput_UTF8Boundary(t *testing.T) {
	// "héllo" — cutting at byte 2 would land inside the two-byte é.
	got, truncated := TruncateOutput("héllo", 2)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if !utf8.ValidString(got) {
		t.Errorf("cut produced invalid UTF-8: %q", got)
	}
	if got != "h" {
		t.Errorf("expected %q, got %q", "h", got)
	}
}

func TestReadFile_LineCapTruncation(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "big.txt")
	lines := make([]string, MaxReadFileLines+100)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	must(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644))

	out, err := executeReadFile(json.RawMessage(fmt.Sprintf(`{"path": %q}`, path)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantNotice := fmt.Sprintf("showing lines 1-%d of %d", MaxReadFileLines, MaxReadFileLines+100)
	if !strings.Contains(out, wantNotice) {
		t.Errorf("expected notice containing %q, got tail %q", wantNotice, out[len(out)-120:])
	}
	if !strings.Contains(out, fmt.Sprintf("start_line=%d", MaxReadFileLines+1)) {
		t.Error("notice should tell the model which start_line continues the file")
	}
	if !strings.Contains(out, fmt.Sprintf("line %d", MaxReadFileLines)) {
		t.Errorf("last line under the cap should be present")
	}
	if strings.Contains(out, fmt.Sprintf("line %d\n", MaxReadFileLines+1)) {
		t.Errorf("lines past the cap should be cut")
	}
}

func TestReadFile_ByteCapTruncation(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "wide.txt")
	lines := make([]string, 5)
	for i := range lines {
		lines[i] = strings.Repeat("x", 20000)
	}
	must(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644))

	out, err := executeReadFile(json.RawMessage(fmt.Sprintf(`{"path": %q}`, path)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) > MaxReadFileBytes+200 {
		t.Errorf("output exceeds byte cap: %d bytes", len(out))
	}
	// 65536-byte cap over 20001-byte lines keeps 3 whole lines.
	if !strings.Contains(out, "showing lines 1-3 of 5") {
		t.Errorf("expected whole-line accounting in the notice, got tail %q", out[len(out)-120:])
	}
	if !strings.Contains(out, "start_line=4") {
		t.Error("notice should point at the next unread line")
	}
}

func TestReadFile_RangeStillCapped(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "big.txt")
	lines := make([]string, MaxReadFileLines+500)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	must(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644))

	out, err := executeReadFile(json.RawMessage(fmt.Sprintf(`{"path": %q, "start_line": 100}`, path)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantNotice := fmt.Sprintf("showing lines 100-%d of %d", 99+MaxReadFileLines, MaxReadFileLines+500)
	if !strings.Contains(out, wantNotice) {
		t.Errorf("expected notice containing %q, got tail %q", wantNotice, out[len(out)-120:])
	}
}

func TestReadFile_EndBeforeStart(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	must(t, os.WriteFile(path, []byte("a\nb\nc\n"), 0o644))

	_, err := executeReadFile(json.RawMessage(fmt.Sprintf(`{"path": %q, "start_line": 3, "end_line": 1}`, path)))
	if err == nil {
		t.Fatal("expected error when end_line is before start_line")
	}
}

func TestListDirectory_EntryCapTruncation(t *testing.T) {
	tmp := t.TempDir()
	for i := 0; i < MaxListEntries+20; i++ {
		must(t, os.WriteFile(filepath.Join(tmp, fmt.Sprintf("f%04d.txt", i)), []byte("x"), 0o644))
	}

	out, err := executeListDirectory(json.RawMessage(fmt.Sprintf(`{"path": %q}`, tmp)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, fmt.Sprintf("truncated at %d entries", MaxListEntries)) {
		t.Errorf("expected truncation notice, got tail %q", out[len(out)-120:])
	}
	if got := strings.Count(out, "file: "); got != MaxListEntries {
		t.Errorf("expected %d entries, got %d", MaxListEntries, got)
	}
}

func TestSearch_ResultCapNotice(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "many.txt")
	must(t, os.WriteFile(path, []byte(strings.Repeat("needle here\n", MaxSearchResults+10)), 0o644))

	// context_lines is pinned off: the cap counts matches, and context rides
	// along with the match that earned it, so the default would put a couple
	// of extra lines past the last one.
	out, err := executeSearch(json.RawMessage(fmt.Sprintf(`{"pattern": "needle", "path": %q, "context_lines": 0}`, tmp)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, fmt.Sprintf("truncated at %d matches", MaxSearchResults)) {
		t.Errorf("expected truncation notice, got tail %q", out[len(out)-140:])
	}
	if got := strings.Count(out, "needle here"); got != MaxSearchResults {
		t.Errorf("expected %d result lines, got %d", MaxSearchResults, got)
	}
}

func TestSearch_LongMatchLineTrimmed(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "minified.txt")
	must(t, os.WriteFile(path, []byte("needle"+strings.Repeat("x", 2*MaxSearchLineBytes)+"\n"), 0o644))

	out, err := executeSearch(json.RawMessage(fmt.Sprintf(`{"pattern": "needle", "path": %q}`, tmp)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "(line truncated)") {
		t.Error("expected per-line truncation marker")
	}
	if len(out) > MaxSearchLineBytes+len(path)+100 {
		t.Errorf("matched line not trimmed: %d bytes", len(out))
	}
}

// A command that prints faster than anyone reads is held to the bound while
// it runs, long before the truncation the model sees. The count of what was
// dropped is what keeps the gap from reading as the command having gone
// quiet.
func TestCaptureBuffer_HoldsABurstToItsBound(t *testing.T) {
	b := NewCaptureBuffer(MaxCapturedOutputBytes)
	chunk := bytes.Repeat([]byte("x"), 1<<16)
	const burst = 100 << 20
	for written := 0; written < burst; written += len(chunk) {
		if n, err := b.Write(chunk); n != len(chunk) || err != nil {
			t.Fatalf("a full buffer must still report the whole write: %d %v", n, err)
		}
	}
	if got := b.Len(); got != burst {
		t.Errorf("printed %d bytes, want %d", got, burst)
	}
	out := b.String()
	// The bound is on the command's bytes; the notice that names the gap is
	// the buffer's own and is the only thing above it.
	if len(out) > MaxCapturedOutputBytes+512 {
		t.Errorf("kept %d bytes, want the bound %d and a notice", len(out), MaxCapturedOutputBytes)
	}
	if !strings.Contains(out, "dropped") {
		t.Error("the output has to say bytes went missing, or the gap reads as silence")
	}
	if !strings.Contains(out, strconv.Itoa(burst-MaxCapturedOutputBytes)) {
		t.Error("the notice should count what was dropped")
	}
	// Whatever survives still passes through the model's own cap unchanged.
	if cut, truncated := TruncateOutput(out, MaxExecOutputBytes); !truncated || len(cut) > MaxExecOutputBytes {
		t.Errorf("the bound sits above the truncation, not instead of it: %d", len(cut))
	}
}

// Both ends are kept and the middle is what goes, because a command says how
// it went in its last line: a buffer that kept only the head would report a
// three-megabyte build by its warmup.
func TestCaptureBuffer_KeepsTheVerdictInTheTail(t *testing.T) {
	b := NewCaptureBuffer(MaxCapturedOutputBytes)
	const first = "=== RUN   TestFirst\n"
	const verdict = "FAIL\tgithub.com/example/pkg/forty\t0.312s\n"
	filler := bytes.Repeat([]byte("ok  \tgithub.com/example/pkg\t0.002s\n"), 1<<16)
	printed := int64(0)
	write := func(p []byte) {
		t.Helper()
		if n, err := b.Write(p); n != len(p) || err != nil {
			t.Fatalf("a full buffer must still report the whole write: %d %v", n, err)
		}
		printed += int64(len(p))
	}
	write([]byte(first))
	for written := 0; written < 3<<20; written += len(filler) {
		write(filler)
	}
	write([]byte(verdict))

	out := b.String()
	if !strings.HasSuffix(out, verdict) {
		t.Errorf("the last line written has to be the last line kept, got %q", out[max(0, len(out)-120):])
	}
	if !strings.HasPrefix(out, first) {
		t.Errorf("the head is kept too, got %q", out[:min(len(out), 120)])
	}
	// What went is exactly the middle: everything printed, less the two ends
	// still in the buffer.
	if got := b.Len(); got != printed {
		t.Errorf("printed %d bytes, want %d", got, printed)
	}
	if !strings.Contains(out, strconv.FormatInt(printed-MaxCapturedOutputBytes, 10)) {
		t.Errorf("the notice should count the middle, %d bytes", printed-MaxCapturedOutputBytes)
	}
	// The notice sits between the two ends, not after them.
	gap := strings.Index(out, "dropped")
	if gap < 0 || gap > len(out)-len(verdict) {
		t.Errorf("the drop notice belongs between the head and the tail, found at %d of %d", gap, len(out))
	}
}

// The split is even and the tail is a ring, so the bytes on either side of
// the notice are the first and last the command printed.
func TestCaptureBuffer_KeepsBothEnds(t *testing.T) {
	b := NewCaptureBuffer(8)
	for _, w := range []string{"abcd", "efg", "hij", "klmn"} {
		if n, err := b.Write([]byte(w)); n != len(w) || err != nil {
			t.Fatalf("write %q: %d %v", w, n, err)
		}
	}
	out := b.String()
	if !strings.HasPrefix(out, "abcd\n… (") {
		t.Errorf("head should be the first four bytes: %q", out)
	}
	if !strings.HasSuffix(out, ")\nklmn") {
		t.Errorf("tail should be the last four bytes: %q", out)
	}
	if got, want := b.Len(), int64(14); got != want {
		t.Errorf("printed %d bytes, want %d", got, want)
	}
	if !strings.Contains(out, "6 bytes from the middle") {
		t.Errorf("six bytes went from the middle: %q", out)
	}
}

// The ring wraps a byte at a time as often as it wraps in one write, and the
// accounting has to survive both.
func TestCaptureBuffer_RingWrapsByteByByte(t *testing.T) {
	b := NewCaptureBuffer(8)
	const src = "0123456789abcdefghij"
	for i := 0; i < len(src); i++ {
		if _, err := b.Write([]byte{src[i]}); err != nil {
			t.Fatal(err)
		}
	}
	out := b.String()
	if !strings.HasPrefix(out, "0123\n") {
		t.Errorf("head should be %q: %q", src[:4], out)
	}
	if !strings.HasSuffix(out, "ghij") {
		t.Errorf("tail should be %q: %q", src[len(src)-4:], out)
	}
	if got, want := b.Len(), int64(len(src)); got != want {
		t.Errorf("printed %d bytes, want %d", got, want)
	}
}

// A bound that falls inside a multi-byte rune must not start the tail on a
// continuation byte; the model would read a replacement character mid-word.
func TestCaptureBuffer_TailStartsOnARune(t *testing.T) {
	b := NewCaptureBuffer(8)
	_, _ = b.Write([]byte("abcd"))
	_, _ = b.Write([]byte("xy€z"))
	out := b.String()
	if !utf8.ValidString(out) {
		t.Errorf("kept output must stay valid UTF-8: %q", out)
	}
	if !strings.HasSuffix(out, "€z") {
		t.Errorf("the whole rune has to survive: %q", out)
	}
}

func TestCaptureBuffer_UnboundedKeepsEverything(t *testing.T) {
	b := NewCaptureBuffer(0)
	_, _ = b.Write([]byte("everything"))
	if got := b.String(); got != "everything" {
		t.Fatalf("got %q", got)
	}
}
