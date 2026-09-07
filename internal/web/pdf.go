package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// PDFTextBinary is the program that turns a fetched PDF into text: poppler's
// pdftotext. A fetch that finds no such binary names it in the result, so the
// reader learns this machine cannot read PDFs at all rather than fetching the
// same URL again with a different guess.
//
// A Go library was the alternative and is not one: the pure-Go readers handle
// a fraction of what is actually published and fail on the rest without
// saying which part they could not parse, which is a worse answer than "no
// reader here" because it looks like a property of the document.
// See docs/capabilities/evidence.md#a-page-is-kept-whole.
const PDFTextBinary = "pdftotext"

const (
	// pdfTimeout bounds one conversion and pdfWaitDelay how long a killed
	// process has to die. Both match what the structural tools spawn under:
	// this is the same shape of call, one short-lived local program over
	// bytes already in hand.
	pdfTimeout   = 30 * time.Second
	pdfWaitDelay = 2 * time.Second

	// maxPDFStderrBytes bounds the reader's complaint quoted in an error.
	maxPDFStderrBytes = 2 << 10
)

// lookPath resolves a binary on PATH; a variable so tests decide which
// binaries this machine has.
var lookPath = exec.LookPath

// DetectPDFText returns the path of the PDF reader, or "" where this machine
// has none. A session asks once, when it builds its web tools, the way the
// structural tools probe PATH at start: asked per fetch, this would put a
// filesystem walk in front of every page for an answer that cannot change
// while the session runs.
func DetectPDFText() string {
	path, err := lookPath(PDFTextBinary)
	if err != nil {
		return ""
	}
	return path
}

// pdfToText renders a fetched PDF to plain text with bin, bounded by
// pdfTimeout and by max bytes of text. It reports whether the text hit that
// ceiling.
//
// The bytes go to a file rather than to the reader's stdin. A PDF is read by
// seeking to the table at its end, so a reader handed a stream either spools
// it to a file itself or refuses outright, and which of the two happens
// depends on the poppler version installed — the failure would land on the
// user with the older one, in a message about a document that is fine.
func pdfToText(ctx context.Context, bin string, body []byte, max int) (text string, truncated bool, err error) {
	dir, err := os.MkdirTemp("", "shhh-pdf-")
	if err != nil {
		return "", false, fmt.Errorf("cannot stage the PDF: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	src := filepath.Join(dir, "fetched.pdf")
	if err := os.WriteFile(src, body, 0o600); err != nil {
		return "", false, fmt.Errorf("cannot stage the PDF: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, pdfTimeout)
	defer cancel()
	// -q keeps the reader's own chatter out of the text, -enc UTF-8 makes
	// the output one encoding whatever the document declares, -nopgbrk drops
	// the form feed between pages, and the trailing "-" writes to stdout.
	cmd := exec.CommandContext(ctx, bin, "-q", "-enc", "UTF-8", "-nopgbrk", src, "-")
	stdout := &capWriter{limit: max, cancel: cancel}
	stderr := &capWriter{limit: maxPDFStderrBytes}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = pdfWaitDelay

	runErr := cmd.Run()
	if stdout.overflowed() {
		// The process was killed for flooding; what was kept is the text.
		return stdout.buf.String(), true, nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", false, fmt.Errorf("%s timed out after %s", PDFTextBinary, pdfTimeout)
	}
	if runErr != nil {
		detail := strings.TrimSpace(stderr.buf.String())
		if detail == "" {
			return "", false, fmt.Errorf("%s failed: %v", PDFTextBinary, runErr)
		}
		return "", false, fmt.Errorf("%s failed: %v: %s", PDFTextBinary, runErr, detail)
	}
	return stdout.buf.String(), false, nil
}

// capWriter buffers up to limit bytes and cancels the process once more than
// that arrives, so a document that expands into far more text than it weighs
// is killed instead of filling memory. Writes always report success;
// overflow is recorded, not surfaced as a write error, because a reader told
// its output failed would report a broken document.
type capWriter struct {
	buf    bytes.Buffer
	limit  int
	total  int
	cancel context.CancelFunc
}

func (w *capWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.total += n
	if remaining := w.limit - w.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		w.buf.Write(p)
	}
	if w.total > w.limit && w.cancel != nil {
		w.cancel()
	}
	return n, nil
}

// overflowed reports whether output beyond the cap was dropped.
func (w *capWriter) overflowed() bool { return w.total > w.limit }
