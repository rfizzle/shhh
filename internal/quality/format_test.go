package quality

import (
	"strings"
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
		{Name: "flaky", Command: "go test ./internal/subagent", Flaked: true, Duration: 1200 * time.Millisecond,
			RerunDuration: 900 * time.Millisecond, Output: "--- FAIL: TestLanding", EvidenceID: "ev3"},
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
			Summary{Suite: "full", Verdict: VerdictFail, Passed: 3, Total: 6, Flaked: 1, Duration: "1m5s"}},
		{"a pass with a flake", Result{Suite: "default", Verdict: VerdictPass, Fingerprint: clean,
			Duration: 2100 * time.Millisecond, Checks: []CheckResult{every[0], every[5]}}, clean,
			Summary{Suite: "default", Verdict: VerdictPass, Passed: 2, Total: 2, Flaked: 1, Duration: "2.1s"}},
		{"a stale pass with a flake", Result{Suite: "default", Verdict: VerdictPass, Fingerprint: clean,
			ChangedDuringRun: true, Duration: 2100 * time.Millisecond, Checks: every[5:]}, clean,
			Summary{Suite: "default", Verdict: VerdictPass, Passed: 1, Total: 1, Flaked: 1, Duration: "2.1s", Stale: true}},
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

// A flake is worded once, in the check's line, and counted once, on the first
// line, where both readers of the text find it. The verdict stays pass: the
// rerun passed, and that is the only reason it does.
func TestFormat_AFlakeIsOnTheLineAndInTheSummary(t *testing.T) {
	res := Result{Suite: "default", Verdict: VerdictPass, Duration: 2100 * time.Millisecond,
		Checks: []CheckResult{
			{Name: "vet", Command: "go vet ./...", Duration: 300 * time.Millisecond},
			{Name: "test", Command: "make test", Flaked: true, Duration: 1200 * time.Millisecond,
				RerunDuration: 1100 * time.Millisecond, Output: "--- FAIL: TestLanding", EvidenceID: "ev1"},
			{Name: "lint", Command: "make lint", Duration: 200 * time.Millisecond},
			{Name: "docs", Command: "make docs-check", Duration: 100 * time.Millisecond},
		}}
	text := res.Format(res.Fingerprint)
	for _, want := range []string{
		`Quality gate "default": PASS — 4/4 checks passed, 1 flaked (2.1s)`,
		"  ~ test — make test (flaked: failed then passed, 1.2s + 1.1s) [full output: evidence ev1]\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Format lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "TestLanding") {
		t.Errorf("a check that counts as passed carries no failure excerpt inline:\n%s", text)
	}
	s, ok := Summarize(text)
	if !ok || s.Flaked != 1 || s.Passed != 4 || !s.OK() {
		t.Errorf("Summarize = %+v, %v; want 4 passed, 1 flaked, a pass", s, ok)
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
