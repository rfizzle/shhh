package quality

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rerunSuite is a one-check suite running `sh -c script`, with rerun_failed
// written as given, or left out where it is empty.
func rerunSuite(rerun, script string) string {
	b, _ := json.Marshal(script)
	field := ""
	if rerun != "" {
		field = `"rerun_failed": ` + rerun + `, `
	}
	return fmt.Sprintf(`{"suites": {"default": {%s"timeout_seconds": 1, "checks": [{"name": "check", "exe": "sh", "args": ["-c", %s]}]}}}`, field, b)
}

// runs counts the lines a check script appended to its run log, which lives
// outside the workspace so it never moves the tree the gate fingerprints.
func runs(t *testing.T, log string) int {
	t.Helper()
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

// countedEvidence stores nothing and names each capture by the order it
// arrived in; *n is how many captures were handed to it.
func countedEvidence(n *int) EvidenceFunc {
	return func(string, []byte) (string, error) {
		*n++
		return fmt.Sprintf("ev-%d", *n), nil
	}
}

// A check that fails and then passes, alone, over the same tree is a flake:
// the verdict is pass because the rerun passed, the check says flaked, and
// the failure it flaked with is what its evidence still points at.
func TestRunner_ARerunPassIsAFlake(t *testing.T) {
	tests := []struct {
		name        string
		rerun       string
		wantVerdict Verdict
		wantFlaked  bool
		wantRuns    int
	}{
		{"the default reruns once", "", VerdictPass, true, 2},
		{"one rerun, written", "1", VerdictPass, true, 2},
		{"no reruns: the failure stands", "0", VerdictFail, false, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws := gitFixture(t)
			scratch := t.TempDir()
			marker, log := filepath.Join(scratch, "ran"), filepath.Join(scratch, "log")
			writeConfig(t, ws, rerunSuite(tc.rerun, fmt.Sprintf(
				`echo run >> %q; if [ -e %q ]; then echo second; exit 0; fi; touch %q; echo first-failure; exit 1`,
				log, marker, marker)))
			stored := 0
			r := &Runner{Workspace: ws, Evidence: countedEvidence(&stored)}
			res := mustRun(t, r, "default")
			c := res.Checks[0]
			if stored != 1 {
				t.Errorf("%d captures were stored; a rerun's would be an entry nothing points at", stored)
			}
			if res.Verdict != tc.wantVerdict || c.Flaked != tc.wantFlaked {
				t.Fatalf("verdict = %s, flaked = %v; want %s, %v:\n%s", res.Verdict, c.Flaked,
					tc.wantVerdict, tc.wantFlaked, res.Format(res.Fingerprint))
			}
			if got := runs(t, log); got != tc.wantRuns {
				t.Fatalf("the check ran %d times, want %d", got, tc.wantRuns)
			}
			if !strings.Contains(c.Output, "first-failure") || c.EvidenceID != "ev-1" {
				t.Errorf("the check keeps output %q and evidence %q, want the first run's", c.Output, c.EvidenceID)
			}
			if tc.wantFlaked {
				if !c.OK() || !res.OK(res.Fingerprint) {
					t.Errorf("a flaked check counts as passed: check OK = %v, result OK = %v", c.OK(), res.OK(res.Fingerprint))
				}
				out := res.Format(res.Fingerprint)
				if !strings.Contains(out, "PASS — 1/1 checks passed, 1 flaked (") ||
					!strings.Contains(out, "  ~ check — sh -c ") || !strings.Contains(out, "(flaked: failed then passed, ") {
					t.Errorf("the result does not say the check flaked:\n%s", out)
				}
			}
		})
	}
}

// A second failure is a failure: the check is not marked, the first run's
// result stands as it was, and a check that never ran to an exit code is not
// given a second go at all.
func TestRunner_ARerunFailureIsAFailure(t *testing.T) {
	tests := []struct {
		name     string
		script   string // %[1]q is the run log
		wantRuns int
		wantExit int
		timedOut bool
	}{
		{"it fails twice", `echo run >> %[1]q; echo failed; exit 3`, 2, 3, false},
		{"the rerun fails differently, the first failure is reported",
			`echo run >> %[1]q; if [ "$(wc -l < %[1]q)" -gt 1 ]; then exit 4; fi; exit 3`, 2, 3, false},
		{"a timeout is not rerun", `echo run >> %[1]q; sleep 5`, 1, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws := gitFixture(t)
			log := filepath.Join(t.TempDir(), "log")
			writeConfig(t, ws, rerunSuite("", fmt.Sprintf(tc.script, log)))
			stored := 0
			r := &Runner{Workspace: ws, Evidence: countedEvidence(&stored)}
			res := mustRun(t, r, "default")
			c := res.Checks[0]
			if stored > 1 {
				t.Errorf("%d captures were stored; a rerun's would be an entry nothing points at", stored)
			}
			if res.Verdict != VerdictFail || c.Flaked || res.OK(res.Fingerprint) {
				t.Fatalf("verdict = %s, flaked = %v; a second failure is a failure:\n%s",
					res.Verdict, c.Flaked, res.Format(res.Fingerprint))
			}
			if got := runs(t, log); got != tc.wantRuns {
				t.Fatalf("the check ran %d times, want %d", got, tc.wantRuns)
			}
			if c.ExitCode != tc.wantExit || c.TimedOut != tc.timedOut {
				t.Errorf("exit = %d, timed out = %v; want %d, %v", c.ExitCode, c.TimedOut, tc.wantExit, tc.timedOut)
			}
			if strings.Contains(res.Format(res.Fingerprint), "flaked") {
				t.Errorf("a failure is not worded as a flake:\n%s", res.Format(res.Fingerprint))
			}
		})
	}
}

// A rerun is a verdict about the tree the suite ran over, so a tree that
// moved while the suite ran is reported as it is today and nothing is rerun,
// and a tree that moves during the rerun makes even its pass stale.
func TestRunner_AMovedTreeIsNotRerun(t *testing.T) {
	tests := []struct {
		name        string
		script      string // %[1]q is the run log, %[2]q the workspace's a.txt
		wantRuns    int
		wantVerdict Verdict
		wantFlaked  bool
	}{
		{"the tree moved during the suite", `echo run >> %[1]q; echo moved >> %[2]q; exit 1`,
			1, VerdictFail, false},
		{"the tree moved during the rerun",
			`echo run >> %[1]q; if [ "$(wc -l < %[1]q)" -gt 1 ]; then echo moved >> %[2]q; exit 0; fi; exit 1`,
			2, VerdictPass, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws := gitFixture(t)
			log := filepath.Join(t.TempDir(), "log")
			writeConfig(t, ws, rerunSuite("", fmt.Sprintf(tc.script, log, filepath.Join(ws, "a.txt"))))
			r := &Runner{Workspace: ws}
			res := mustRun(t, r, "default")
			if got := runs(t, log); got != tc.wantRuns {
				t.Fatalf("the check ran %d times, want %d", got, tc.wantRuns)
			}
			if res.Verdict != tc.wantVerdict || res.Checks[0].Flaked != tc.wantFlaked {
				t.Fatalf("verdict = %s, flaked = %v; want %s, %v", res.Verdict, res.Checks[0].Flaked,
					tc.wantVerdict, tc.wantFlaked)
			}
			if !res.ChangedDuringRun || res.OK(TakeFingerprint(ws)) {
				t.Errorf("changed during run = %v, OK = %v; a moved tree is never a pass",
					res.ChangedDuringRun, res.OK(TakeFingerprint(ws)))
			}
		})
	}
}

// rerun_failed is 0 or 1. More is refused at load rather than clamped, since
// a check given three tries passes when it fails two times in three.
func TestLoadConfig_RerunFailed(t *testing.T) {
	tests := []struct {
		name    string
		rerun   string
		want    int
		wantErr bool
	}{
		{"absent takes the default", "", DefaultRerunFailed, false},
		{"zero turns it off", "0", 0, false},
		{"one", "1", 1, false},
		{"two is refused", "2", 0, true},
		{"negative is refused", "-1", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			writeConfig(t, ws, rerunSuite(tc.rerun, "true"))
			cfg, err := LoadConfig(ws)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "rerun_failed") {
					t.Fatalf("err = %v, want one naming rerun_failed", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Suites["default"].Reruns(); got != tc.want {
				t.Errorf("Reruns = %d, want %d", got, tc.want)
			}
		})
	}
}

// A flake is told to the ledger with the exit code the failing run gave,
// which the check no longer carries once the rerun passed, and the ledger's
// answer is the check's FlakedBefore and the line's clause. A check that
// passed first time or failed twice is told nothing, and the ledger can only
// answer a count: whatever it says, the verdict is the rerun's.
func TestRunner_AFlakeIsToldToTheLedger(t *testing.T) {
	tests := []struct {
		name       string
		script     string // %[1]q is a marker outside the tree
		answer     int
		wantTold   []Flake
		wantBefore int
		wantLine   string
	}{
		{"a flake is told, and the count rides the line",
			`if [ -e %[1]q ]; then exit 0; fi; touch %[1]q; exit 5`, 3,
			[]Flake{{Suite: "default", Check: "check", FirstExit: 5}}, 3, ", 3 times before)"},
		{"a ledger that could not count answers nothing, and the pass stands",
			`if [ -e %[1]q ]; then exit 0; fi; touch %[1]q; exit 1`, -1,
			[]Flake{{Suite: "default", Check: "check", FirstExit: 1}}, 0, "(flaked: failed then passed, "},
		{"a pass is not a flake", `: %[1]q; exit 0`, 9, nil, 0, "✓ check"},
		{"two failures are not a flake", `: %[1]q; exit 2`, 9, nil, 0, "✗ check"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws := gitFixture(t)
			writeConfig(t, ws, rerunSuite("", fmt.Sprintf(tc.script, filepath.Join(t.TempDir(), "ran"))))
			var told []Flake
			r := &Runner{Workspace: ws, Flakes: func(f Flake) int {
				f.Command = ""
				told = append(told, f)
				return tc.answer
			}}
			res := mustRun(t, r, "default")
			if fmt.Sprint(told) != fmt.Sprint(tc.wantTold) {
				t.Errorf("the ledger was told %+v, want %+v", told, tc.wantTold)
			}
			if c := res.Checks[0]; c.FlakedBefore != tc.wantBefore {
				t.Errorf("FlakedBefore = %d, want %d", c.FlakedBefore, tc.wantBefore)
			}
			if out := res.Format(res.Fingerprint); !strings.Contains(out, tc.wantLine) {
				t.Errorf("the result lacks %q:\n%s", tc.wantLine, out)
			}
			if tc.wantTold != nil && res.Verdict != VerdictPass {
				t.Errorf("verdict = %s; a flake is a pass whatever the ledger says", res.Verdict)
			}
		})
	}
}
