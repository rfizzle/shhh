package quality

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// moduleFixture is a git checkout holding a module of two packages, so the
// package lister has something to list.
func moduleFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	ws := gitFixture(t)
	files := map[string]string{
		"go.mod":       "module example.test/m\n\ngo 1.21\n",
		"a/a.go":       "package a\n",
		"b/b.go":       "package b\n",
		"b/testdata/x": "not source\n",
	}
	for name, body := range files {
		path := filepath.Join(ws, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

// scopedConfig is a suite of an unscoped check listed first and a scoped one
// after it, each appending a line to log, the scoped one with the packages it
// was handed. scopedExit and restExit are the exit codes they end with.
func scopedConfig(log string, scopedExit, restExit int) string {
	script := func(word string, exit int) string {
		b, _ := json.Marshal(fmt.Sprintf(`echo "%s $*" >> %q; exit %d`, word, log, exit))
		return string(b)
	}
	return fmt.Sprintf(`{"suites": {"default": {"rerun_failed": 0, "checks": [
		{"name": "rest", "exe": "sh", "args": ["-c", %s, "sh"]},
		{"name": "scoped", "exe": "sh", "args": ["-c", %s, "sh", "{packages}"], "scope": "packages"}
	]}}}`, script("rest", restExit), script("scoped", scopedExit))
}

func readLog(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// A scoped check runs before the unscoped checks however the suite lists it,
// over the packages of the changed files, and a failure in it ends the run:
// the verdict is fail and every unscoped check says it did not run.
func TestRunner_ScopedChecksRunFirstAndStopTheRun(t *testing.T) {
	ws := moduleFixture(t)
	log := filepath.Join(t.TempDir(), "log")
	writeConfig(t, ws, scopedConfig(log, 1, 0))
	r := &Runner{Workspace: ws, Changed: func() []string { return []string{"b/b.go", "docs/x.md"} }}

	res := mustRun(t, r, "default")

	if res.Verdict != VerdictFail {
		t.Fatalf("verdict = %s, want fail:\n%s", res.Verdict, res.Format(res.Fingerprint))
	}
	if got := readLog(t, log); len(got) != 1 || got[0] != "scoped example.test/m/b" {
		t.Errorf("ran %q; want the scoped check alone, over the changed package", got)
	}
	rest := res.Checks[0]
	if rest.NotRun != NotRunScoped || rest.OK() {
		t.Errorf("unscoped check = %+v; want it not run for %q", rest, NotRunScoped)
	}
	out := res.Format(res.Fingerprint)
	for _, want := range []string{
		"FAIL — 0/2 checks passed",
		"✗ scoped (b) — sh",
		"- rest — sh",
		"(not run: a scoped check failed first)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("result lacks %q:\n%s", want, out)
		}
	}
	if s := res.Summary(res.Fingerprint); s.Verdict != VerdictFail || s.Total != 2 || s.Passed != 0 {
		t.Errorf("Summary = %+v; want fail 0/2", s)
	}
}

// The scoped run is an ordering and an early exit, never a verdict: a pass
// takes every check, and a scoped pass over a failing suite is a fail.
func TestRunner_APassStillRunsEverything(t *testing.T) {
	tests := []struct {
		name     string
		restExit int
		want     Verdict
		wantPass int
	}{
		{"everything passes", 0, VerdictPass, 2},
		{"the unscoped check still decides", 1, VerdictFail, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws := moduleFixture(t)
			log := filepath.Join(t.TempDir(), "log")
			writeConfig(t, ws, scopedConfig(log, 0, tc.restExit))
			r := &Runner{Workspace: ws, Changed: func() []string { return []string{"a/a.go", "b/b.go"} }}

			res := mustRun(t, r, "default")

			if res.Verdict != tc.want || res.passed() != tc.wantPass {
				t.Fatalf("verdict = %s, passed %d; want %s, %d:\n%s", res.Verdict, res.passed(), tc.want, tc.wantPass, res.Format(res.Fingerprint))
			}
			got := readLog(t, log)
			if len(got) != 2 || got[0] != "scoped example.test/m/a example.test/m/b" || got[1] != "rest" {
				t.Errorf("ran %q; want the scoped check over both packages, then the other", got)
			}
			if out := res.Format(res.Fingerprint); !strings.Contains(out, "✓ scoped (a, b) — ") {
				t.Errorf("result does not say what was scoped:\n%s", out)
			}
		})
	}
}

// Nothing to narrow to is the module's own pattern, which is what a run with
// no changed files has always run.
func TestRunner_AScopeThatNamesNoPackageIsTheWholeModule(t *testing.T) {
	tests := []struct {
		name    string
		changed []string
	}{
		{"no changed files", nil},
		{"no Go among them", []string{"README.md", "b/testdata/x"}},
		{"a package that is gone", []string{"gone/gone.go"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws := moduleFixture(t)
			log := filepath.Join(t.TempDir(), "log")
			writeConfig(t, ws, scopedConfig(log, 0, 0))
			r := &Runner{Workspace: ws, Changed: func() []string { return tc.changed }}

			res := mustRun(t, r, "default")

			if got := readLog(t, log); len(got) != 2 || got[0] != "scoped ./..." {
				t.Errorf("ran %q; want the scoped check over ./...", got)
			}
			if out := res.Format(res.Fingerprint); !strings.Contains(out, "scoped (./...)") {
				t.Errorf("result does not say the whole module was the scope:\n%s", out)
			}
		})
	}
}

// With no source given the changed files are the dirty tree's.
func TestRunner_TheDirtyTreeIsTheDefaultChangedFiles(t *testing.T) {
	ws := moduleFixture(t)
	log := filepath.Join(t.TempDir(), "log")
	writeConfig(t, ws, scopedConfig(log, 0, 0))

	mustRun(t, &Runner{Workspace: ws}, "default")

	if got := readLog(t, log); len(got) != 2 || got[0] != "scoped example.test/m/a example.test/m/b" {
		t.Errorf("ran %q; the untracked packages are the changed files", got)
	}
}

func TestScope_ExpandFillsEveryPlaceholder(t *testing.T) {
	s := scope{pkgs: []string{"x/a", "x/b"}}
	got := strings.Join(s.expand([]string{"test", "{packages}", "P={packages}", "-v"}), "|")
	if want := "test|x/a|x/b|P=x/a x/b|-v"; got != want {
		t.Errorf("expand = %q, want %q", got, want)
	}
}

func TestLoadConfig_ChecksScope(t *testing.T) {
	check := func(scope, arg string) string {
		return fmt.Sprintf(`{"suites": {"default": {"checks": [{"name": "t", "exe": "go", "scope": %q, "args": ["test", %q]}]}}}`, scope, arg)
	}
	tests := []struct {
		name, config, wantErr string
	}{
		{"packages with a placeholder", check("packages", "{packages}"), ""},
		{"a placeholder inside an argument", check("packages", "P={packages}"), ""},
		{"packages without a placeholder", check("packages", "./..."), "needs an argument holding {packages}"},
		{"a placeholder without a scope", check("", "{packages}"), "filled only for a check with scope"},
		{"a scope that does not exist", check("files", "{packages}"), `scope is "files"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			writeConfig(t, ws, tc.config)
			_, err := LoadConfig(ws)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("LoadConfig: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("LoadConfig error = %v, want it to hold %q", err, tc.wantErr)
			}
		})
	}
}

func TestScopeWords_NamesAFewAndCountsTheRest(t *testing.T) {
	tests := []struct {
		scope []string
		want  string
	}{
		{nil, ""},
		{[]string{"internal/quality", "internal/cli"}, " (internal/quality, internal/cli)"},
		{[]string{"a", "b", "c", "d", "e", "f"}, " (a, b, c, d, +2 more)"},
	}
	for _, tc := range tests {
		if got := scopeWords(tc.scope); got != tc.want {
			t.Errorf("scopeWords(%v) = %q, want %q", tc.scope, got, tc.want)
		}
	}
}
