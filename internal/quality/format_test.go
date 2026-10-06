package quality

import (
	"testing"
	"time"
)

// Summarize is the one reader of the text Format writes, for the gate rows
// that exist only as text. A word Format adds for the model must not stop a
// verdict counting on the screen, so every check state and every staleness
// Format can write is read back here, and the reading has to agree both with
// the values the result holds and with Result.Summary, which the rows that
// hold the result read instead.
func TestFormat_SummarizeReadsEveryLineFormatWrites(t *testing.T) {
	clean := Fingerprint{Repo: true, Head: "abc", StatusHash: "s1"}
	moved := Fingerprint{Repo: true, Head: "abc", StatusHash: "s2"}
	unhashed := Fingerprint{Repo: true, Head: "abc", Unhashed: true}
	every := []CheckResult{
		{Name: "ok", Command: "go vet ./...", Duration: 1200 * time.Millisecond},
		{Name: "skipping", Command: "go test ./...", Duration: time.Second, EvidenceID: "ev1",
			Skips: []string{SkipLine(3, "no Seatbelt containment here")}},
		{Name: "exit", Command: "golangci-lint run", ExitCode: 2, Duration: 3 * time.Second,
			Output: "a.go:1: bad\nSTALE: a check's own output, indented under its row", EvidenceID: "ev2"},
		{Name: "slow", Command: "make test", TimedOut: true, Duration: time.Minute},
		{Name: "absent", Command: "tokei", Err: "executable not found"},
	}
	tests := []struct {
		name    string
		res     Result
		current Fingerprint
		want    Summary
	}{
		{"a pass", Result{Suite: "default", Verdict: VerdictPass, Fingerprint: clean,
			Duration: 2250 * time.Millisecond, Checks: every[:2]}, clean,
			Summary{Suite: "default", Verdict: VerdictPass, Passed: 2, Total: 2, Duration: "2.3s"}},
		{"a fail over every check state", Result{Suite: "full", Verdict: VerdictFail, Fingerprint: clean,
			Duration: 65 * time.Second, Contained: "bubblewrap", Checks: every}, clean,
			Summary{Suite: "full", Verdict: VerdictFail, Passed: 2, Total: 5, Duration: "1m5s"}},
		{"a fail with no checks", Result{Suite: "empty", Verdict: VerdictFail}, Fingerprint{},
			Summary{Suite: "empty", Verdict: VerdictFail, Duration: "0s"}},
		{"blocked", Result{Suite: "default", Verdict: VerdictBlocked, Reason: "unknown suite \"x\"",
			Fingerprint: clean}, clean,
			Summary{Suite: "default", Verdict: VerdictBlocked}},
		{"cancelled", Result{Suite: "default", Verdict: VerdictCancelled,
			Reason: "the run was cancelled before completing", Fingerprint: clean}, clean,
			Summary{Suite: "default", Verdict: VerdictCancelled}},
		{"a pass the tree moved under", Result{Suite: "default", Verdict: VerdictPass, Fingerprint: clean,
			ChangedDuringRun: true, Duration: time.Second, Checks: every[:1]}, clean,
			Summary{Suite: "default", Verdict: VerdictPass, Passed: 1, Total: 1, Duration: "1s", Stale: true}},
		{"a pass over a tree too large to hash", Result{Suite: "default", Verdict: VerdictPass,
			Fingerprint: unhashed, Duration: time.Second, Checks: every[:1]}, unhashed,
			Summary{Suite: "default", Verdict: VerdictPass, Passed: 1, Total: 1, Duration: "1s", Stale: true}},
		{"a pass over a tree that has changed since", Result{Suite: "default", Verdict: VerdictPass,
			Fingerprint: clean, Duration: time.Second, Checks: every[:1]}, moved,
			Summary{Suite: "default", Verdict: VerdictPass, Passed: 1, Total: 1, Duration: "1s", Stale: true}},
		{"a blocked run that is also stale", Result{Suite: "default", Verdict: VerdictBlocked,
			Reason: "a check could not start", Fingerprint: clean}, moved,
			Summary{Suite: "default", Verdict: VerdictBlocked, Stale: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			text := tc.res.Format(tc.current)
			got, ok := Summarize(text)
			if !ok {
				t.Fatalf("Summarize did not recognise Format's text:\n%s", text)
			}
			if got != tc.want {
				t.Errorf("Summarize read %+v, want %+v, from:\n%s", got, tc.want, text)
			}
			if held := tc.res.Summary(tc.current); held != got {
				t.Errorf("Result.Summary = %+v, but the text reads back as %+v", held, got)
			}
		})
	}
}

// A pass is one a caller may act on only over the tree it ran against. Every
// surface that holds a Result asks OK, and OK has to agree with the reading
// the close row stores, so a stale pass cannot count on one surface and not
// on another.
func TestResult_OKIsTheCloseRowsReading(t *testing.T) {
	clean := Fingerprint{Repo: true, Head: "abc", StatusHash: "s1"}
	moved := Fingerprint{Repo: true, Head: "abc", StatusHash: "s2"}
	tests := []struct {
		name    string
		res     Result
		current Fingerprint
		want    bool
	}{
		{"a pass over the current tree", Result{Verdict: VerdictPass, Fingerprint: clean}, clean, true},
		{"a pass outside a repository", Result{Verdict: VerdictPass}, Fingerprint{}, true},
		{"a pass whose tree changed while the checks ran",
			Result{Verdict: VerdictPass, Fingerprint: clean, ChangedDuringRun: true}, clean, false},
		{"a pass outside a repository whose tree changed while the checks ran",
			Result{Verdict: VerdictPass, ChangedDuringRun: true}, Fingerprint{}, false},
		{"a pass over a tree that has moved since", Result{Verdict: VerdictPass, Fingerprint: clean}, moved, false},
		{"a fail", Result{Verdict: VerdictFail, Fingerprint: clean}, clean, false},
		{"a blocked run", Result{Verdict: VerdictBlocked}, Fingerprint{}, false},
		{"a cancelled run", Result{Verdict: VerdictCancelled}, Fingerprint{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.res.OK(tc.current)
			if got != tc.want {
				t.Errorf("OK = %v, want %v", got, tc.want)
			}
			if row := tc.res.Summary(tc.current).OK(); got != row {
				t.Errorf("OK = %v, the close row reads %v", got, row)
			}
		})
	}
}
