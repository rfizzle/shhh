package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
