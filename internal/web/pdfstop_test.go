package web

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// A stop reaches the reader mid-extraction: the extraction answers with the
// stop itself, well inside the reader's own time, so the call ends as
// stopped and is not told that the PDF could not be read.
func TestWeb_AStopEndsPDFExtraction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub reader is a shell script")
	}
	bin := filepath.Join(t.TempDir(), PDFTextBinary)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop := time.AfterFunc(100*time.Millisecond, cancel)
	defer stop.Stop()
	defer cancel()

	start := time.Now()
	_, _, err := pdfToText(ctx, bin, []byte("%PDF-1.7"), 1<<20)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the stop", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the stop took %s to reach the reader", took)
	}
}
