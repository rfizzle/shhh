package web

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// stubPDFText writes a stand-in for the PDF reader that prints text and
// returns its path. The suite has to run on a machine with no poppler
// installed, which is most of them.
func stubPDFText(t *testing.T, text string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub reader is a shell script")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, PDFTextBinary)
	// The script prints the file beside it, so a test decides how much text
	// the reader produces without quoting it into the script.
	if err := os.WriteFile(path, []byte("#!/bin/sh\ncat \"$0.txt\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".txt", []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// stubFailingPDFText is a reader that refuses the document.
func stubFailingPDFText(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub reader is a shell script")
	}
	path := filepath.Join(t.TempDir(), PDFTextBinary)
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'Syntax Error: Couldn'\"'\"'t find trailer dictionary' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDetectPDFText(t *testing.T) {
	bin := stubPDFText(t, "text")
	t.Setenv("PATH", filepath.Dir(bin))
	if got := DetectPDFText(); got == "" {
		t.Fatal("a reader on PATH must be found")
	}
	t.Setenv("PATH", t.TempDir())
	if got := DetectPDFText(); got != "" {
		t.Fatalf("no reader on PATH must answer empty, got %q", got)
	}
}

func TestPDFToText(t *testing.T) {
	bin := stubPDFText(t, "Page one.\nPage two.\n")
	text, truncated, err := pdfToText(context.Background(), bin, []byte("%PDF-1.7"), 1<<20)
	if err != nil {
		t.Fatalf("pdfToText: %v", err)
	}
	if truncated {
		t.Error("short text reported as truncated")
	}
	if !strings.Contains(text, "Page two.") {
		t.Errorf("text = %q", text)
	}
}

// A document that expands into more text than the ceiling allows is cut
// there rather than read into memory whole.
func TestPDFToTextStopsAtTheCeiling(t *testing.T) {
	bin := stubPDFText(t, strings.Repeat("a", 8192))
	text, truncated, err := pdfToText(context.Background(), bin, []byte("%PDF-1.7"), 1024)
	if err != nil {
		t.Fatalf("pdfToText: %v", err)
	}
	if !truncated {
		t.Error("text past the ceiling must be reported as cut")
	}
	if len(text) > 1024 {
		t.Errorf("kept %d bytes past a 1024-byte ceiling", len(text))
	}
}

// A reader that refuses the document says why: the model is told this PDF is
// unreadable rather than that the fetch failed.
func TestPDFToTextReportsTheReadersComplaint(t *testing.T) {
	bin := stubFailingPDFText(t)
	_, _, err := pdfToText(context.Background(), bin, []byte("not a pdf"), 1<<20)
	if err == nil {
		t.Fatal("a refusing reader must be an error")
	}
	if !strings.Contains(err.Error(), "trailer dictionary") {
		t.Errorf("the reader's complaint is missing: %v", err)
	}
}
