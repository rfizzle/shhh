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

func TestFormatExecResult_ClassifiesEveryCommandEnding(t *testing.T) {
	cases := []struct {
		name   string
		result ExecResult
		prefix string
		wants  []string
	}{
		{"success", ExecResult{Output: "done", Outcome: ExecSucceeded}, "exit code: 0", []string{"output:", "done"}},
		{"non-zero exit", ExecResult{Output: "stderr", ExitCode: 1, Outcome: ExecExited}, "error:", []string{"status 1", "stderr"}},
		{"signal", ExecResult{ExitCode: -9, Outcome: ExecSignaled}, "error:", []string{"signal 9", "(no output)"}},
		{"timeout", ExecResult{Output: "partial", ExitCode: -2, Outcome: ExecTimedOut}, "error:", []string{"timed out", "partial"}},
		{"stopped", ExecResult{ExitCode: -2, Outcome: ExecStopped}, "error:", []string{"stopped", "(no output)"}},
		{"spawn failure", ExecResult{Output: "executable not found", ExitCode: -1, Outcome: ExecDidNotStart}, "error:", []string{"did not start", "executable not found"}},
		{"handoff", ExecResult{Output: `process "watch"`, Outcome: ExecHandedOff}, "exit code: 0", []string{"process", "watch"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatExecResult(tc.result)
			if !strings.HasPrefix(got, tc.prefix) {
				t.Fatalf("FormatExecResult() = %q, want prefix %q", got, tc.prefix)
			}
			for _, want := range tc.wants {
				if !strings.Contains(got, want) {
					t.Errorf("FormatExecResult() = %q, want %q", got, want)
				}
			}
			if tc.result.Failed() != strings.HasPrefix(got, "error:") {
				t.Errorf("failed/result prefix disagree: %+v => %q", tc.result, got)
			}
		})
	}
}

// verboseBuild is a failing build's output: a first compiler error at the top,
// a great deal of noise, and the verdict on the last line. It is the shape the
// bound exists for — a prefix cut keeps the first error and throws the verdict
// away.
func verboseBuild(middleLines int) string {
	var b strings.Builder
	b.WriteString("# github.com/example/pkg\n")
	b.WriteString("pkg/first.go:12:9: undefined: firstThing\n")
	for i := range middleLines {
		fmt.Fprintf(&b, "compiling package number %d of a great many\n", i)
	}
	b.WriteString("pkg/last.go:99:2: undefined: lastThing\n")
	b.WriteString("FAIL\tgithub.com/example/pkg [build failed]\n")
	return b.String()
}

func TestFormatExecResult_KeepsBothEndsOfAnOversizeResult(t *testing.T) {
	output := verboseBuild(400)
	if len(output) <= MaxExecOutputBytes {
		t.Fatalf("fixture is only %d bytes; it must exceed the cap to exercise it", len(output))
	}
	got := FormatExecResult(ExecResult{Output: output, ExitCode: 2, Outcome: ExecExited})

	if !strings.HasPrefix(got, "error: command exited with status 2\n") {
		t.Errorf("the error status must survive the bound, got %q", firstLine(got))
	}
	for _, want := range []string{
		"pkg/first.go:12:9: undefined: firstThing",
		"pkg/last.go:99:2: undefined: lastThing",
		"FAIL\tgithub.com/example/pkg [build failed]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("bounded result dropped %q", want)
		}
	}
	if !strings.HasSuffix(got, "FAIL\tgithub.com/example/pkg [build failed]") {
		t.Errorf("the last line of the command is the last line of the result, got tail %q", lastLine(got))
	}
	if len(got) > MaxExecOutputBytes+execNoticeRoom {
		t.Errorf("bounded result is %d bytes, over the cap plus its notice's room", len(got))
	}
}

func TestBoundExecOutput_NoticeCountsWhatWentAndNamesNoIdWithoutAStore(t *testing.T) {
	output := verboseBuild(400)
	got := BoundExecOutput(output, nil)

	notice := noticeLine(t, got)
	if strings.Contains(notice, "evidence") {
		t.Errorf("a session with no store must not offer an id: %q", notice)
	}
	// The notice's count is the arithmetic a reader would do themselves:
	// what the command printed, less the two ends that survived.
	head, tail, ok := strings.Cut(got, "\n"+notice+"\n")
	if !ok {
		t.Fatalf("notice is not between two ends in %q", got)
	}
	omitted := len(strings.TrimRight(output, "\n")) - len(head) - len(tail)
	if !strings.Contains(notice, fmt.Sprintf("%d bytes", omitted)) {
		t.Errorf("notice %q should say %d bytes went", notice, omitted)
	}
	if !TruncationNotice(notice) {
		t.Errorf("notice %q is not in the shape every cap announces itself in", notice)
	}
}

func TestBoundExecOutput_NoticeNamesTheEvidenceEntryWhereThereIsOne(t *testing.T) {
	output := verboseBuild(400)
	var storedTool, storedContent string
	keep := func(tool, content string) (string, bool) {
		storedTool, storedContent = tool, content
		return "ev-0123456789abcdef", true
	}
	got := BoundExecOutput(output, keep)

	notice := noticeLine(t, got)
	for _, want := range []string{
		"full output stored as evidence ev-0123456789abcdef",
		"retrieve it with the evidence tool (info/read/search)",
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice %q should contain %q", notice, want)
		}
	}
	if storedTool != ExecCommandName {
		t.Errorf("entry filed under %q, want %q", storedTool, ExecCommandName)
	}
	if storedContent != strings.TrimRight(output, "\n") {
		t.Error("the whole output is what the store keeps, not the bounded view")
	}
}

func TestBoundExecOutput_StoreThatRefusesFallsBackToTheBareCount(t *testing.T) {
	refuse := func(string, string) (string, bool) { return "", false }
	got := BoundExecOutput(verboseBuild(400), refuse)
	notice := noticeLine(t, got)
	if strings.Contains(notice, "evidence") {
		t.Errorf("a refused store must not leave an id nobody can resolve: %q", notice)
	}
	if !strings.Contains(notice, "bytes from the middle omitted") {
		t.Errorf("notice %q should still say what went", notice)
	}
}

func TestBoundExecOutput_CutsOnUTF8Boundaries(t *testing.T) {
	// Multi-byte runes packed with no line break anywhere, so both ends are
	// cut mid-line and the only boundary left to respect is the rune's.
	output := strings.Repeat("héllo wörld ", 2000)
	got := BoundExecOutput(output, nil)
	if !utf8.ValidString(got) {
		t.Fatal("bounded output is not valid UTF-8")
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Error("bounded output contains a replacement character")
	}
	if !strings.HasPrefix(got, "héllo") {
		t.Errorf("head lost, got %q", got[:20])
	}
	if !strings.HasSuffix(got, "wörld ") {
		t.Errorf("tail lost, got %q", got[len(got)-20:])
	}
}

func TestBoundExecOutput_OneEnormousLineStillKeepsBothEnds(t *testing.T) {
	// A single line over the cap has no boundary to fall back on: the ends
	// are still both there rather than the head alone.
	output := "START" + strings.Repeat("x", 3*MaxExecOutputBytes) + "END"
	got := BoundExecOutput(output, nil)
	if !strings.HasPrefix(got, "START") {
		t.Errorf("head lost, got %q", got[:20])
	}
	if !strings.HasSuffix(got, "END") {
		t.Errorf("tail lost, got %q", got[len(got)-20:])
	}
}

func TestBoundExecOutput_UnderTheCapIsUntouched(t *testing.T) {
	if got := BoundExecOutput("ok\n", nil); got != "ok" {
		t.Errorf("BoundExecOutput() = %q, want %q", got, "ok")
	}
	if got := BoundExecOutput("   \n", nil); got != "(no output)" {
		t.Errorf("BoundExecOutput() = %q, want %q", got, "(no output)")
	}
}

// noticeLine is the one line of a bounded result that says what fell out.
func noticeLine(t *testing.T, bounded string) string {
	t.Helper()
	for _, line := range strings.Split(bounded, "\n") {
		if TruncationNotice(line) {
			return line
		}
	}
	t.Fatalf("no omission notice in %q", bounded)
	return ""
}

func firstLine(s string) string { return strings.SplitN(s, "\n", 2)[0] }

func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	return lines[len(lines)-1]
}

func TestReadFile_LineCapTruncation(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "big.txt")
	lines := make([]string, MaxReadFileLines+100)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	must(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644))

	out, err := shared.executeReadFile(json.RawMessage(fmt.Sprintf(`{"path": %q}`, path)))
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

	out, err := shared.executeReadFile(json.RawMessage(fmt.Sprintf(`{"path": %q}`, path)))
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

	out, err := shared.executeReadFile(json.RawMessage(fmt.Sprintf(`{"path": %q, "start_line": 100}`, path)))
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

	_, err := shared.executeReadFile(json.RawMessage(fmt.Sprintf(`{"path": %q, "start_line": 3, "end_line": 1}`, path)))
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
