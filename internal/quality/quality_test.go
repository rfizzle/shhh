package quality

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, ws, content string) {
	t.Helper()
	dir := filepath.Join(ws, ".shhh")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "quality.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// shSuite is a one-suite config whose single check runs `sh -c script`.
func shSuite(script string) string {
	b, _ := json.Marshal(script)
	return fmt.Sprintf(`{"suites": {"default": {"checks": [{"name": "check", "exe": "sh", "args": ["-c", %s]}]}}}`, b)
}

func gitFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	ws := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", ws}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "init")
	return ws
}

func mustRun(t *testing.T, r *Runner, suite string) *Result {
	t.Helper()
	res, err := r.Run(context.Background(), suite)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestLoadConfig(t *testing.T) {
	ws := t.TempDir()
	if _, err := LoadConfig(ws); !os.IsNotExist(err) {
		t.Fatalf("missing config should surface IsNotExist, got %v", err)
	}

	writeConfig(t, ws, `not json`)
	if _, err := LoadConfig(ws); err == nil || os.IsNotExist(err) {
		t.Fatalf("invalid JSON should error, got %v", err)
	}

	writeConfig(t, ws, `{"suites": {}}`)
	if _, err := LoadConfig(ws); err == nil || !strings.Contains(err.Error(), "no suites") {
		t.Fatalf("empty suites should be rejected, got %v", err)
	}

	writeConfig(t, ws, `{"suites": {"default": {"checks": []}}}`)
	if _, err := LoadConfig(ws); err == nil || !strings.Contains(err.Error(), "no checks") {
		t.Fatalf("empty checks should be rejected, got %v", err)
	}

	writeConfig(t, ws, `{"suites": {"default": {"checks": [{"name": "x"}]}}}`)
	if _, err := LoadConfig(ws); err == nil || !strings.Contains(err.Error(), "no exe") {
		t.Fatalf("missing exe should be rejected, got %v", err)
	}

	writeConfig(t, ws, `{"typo_field": 1, "suites": {"default": {"checks": [{"name": "x", "exe": "true"}]}}}`)
	if _, err := LoadConfig(ws); err == nil {
		t.Fatal("unknown fields should be rejected — the config is trusted and typos must not silently weaken it")
	}

	writeConfig(t, ws, `{"max_parallel": 9, "suites": {"b": {"checks": [{"name": "x", "exe": "true"}]}, "a": {"checks": [{"name": "y", "exe": "true"}]}}}`)
	cfg, err := LoadConfig(ws)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.SuiteNames(); strings.Join(got, ",") != "a,b" {
		t.Fatalf("SuiteNames = %v", got)
	}
	if cfg.effectiveParallel() != MaxParallelChecks {
		t.Fatalf("effectiveParallel = %d, want clamp to %d", cfg.effectiveParallel(), MaxParallelChecks)
	}
	if (Config{}).effectiveParallel() != 1 {
		t.Fatal("default parallelism must be 1")
	}
}

func TestRun_NoConfigBlocked(t *testing.T) {
	r := &Runner{Workspace: t.TempDir()}
	res := mustRun(t, r, "")
	if res.Verdict != VerdictBlocked || !strings.Contains(res.Reason, ConfigRelPath) {
		t.Fatalf("verdict = %s, reason = %q", res.Verdict, res.Reason)
	}
	if !strings.Contains(res.Format(res.Fingerprint), "Blocked is never a pass") {
		t.Fatal("blocked result must say it is not a pass")
	}
}

func TestRun_UnknownSuiteBlocked(t *testing.T) {
	ws := t.TempDir()
	writeConfig(t, ws, shSuite("exit 0"))
	r := &Runner{Workspace: ws}
	res := mustRun(t, r, "nope")
	if res.Verdict != VerdictBlocked || !strings.Contains(res.Reason, `unknown suite "nope"`) || !strings.Contains(res.Reason, "default") {
		t.Fatalf("verdict = %s, reason = %q", res.Verdict, res.Reason)
	}
}

func TestRun_MissingExecutableBlocked(t *testing.T) {
	ws := t.TempDir()
	writeConfig(t, ws, `{"suites": {"default": {"checks": [
		{"name": "gone", "exe": "definitely-not-a-real-binary-shhh"},
		{"name": "mark", "exe": "sh", "args": ["-c", "touch ran.txt"]}
	]}}}`)
	r := &Runner{Workspace: ws}
	res := mustRun(t, r, "default")
	if res.Verdict != VerdictBlocked || !strings.Contains(res.Reason, "not found on PATH") {
		t.Fatalf("verdict = %s, reason = %q", res.Verdict, res.Reason)
	}
	if _, err := os.Stat(filepath.Join(ws, "ran.txt")); err == nil {
		t.Fatal("a blocked run must not execute any check")
	}
}

func TestRun_PassAndFail(t *testing.T) {
	ws := t.TempDir()
	writeConfig(t, ws, `{"suites": {
		"good": {"checks": [{"name": "ok", "exe": "sh", "args": ["-c", "echo fine"]}]},
		"bad": {"checks": [
			{"name": "ok", "exe": "sh", "args": ["-c", "echo fine"]},
			{"name": "boom", "exe": "sh", "args": ["-c", "echo kaboom >&2; exit 3"]}
		]}
	}}`)
	r := &Runner{Workspace: ws}

	res := mustRun(t, r, "good")
	if res.Verdict != VerdictPass {
		t.Fatalf("good suite verdict = %s: %s", res.Verdict, res.Format(res.Fingerprint))
	}

	res = mustRun(t, r, "bad")
	if res.Verdict != VerdictFail {
		t.Fatalf("bad suite verdict = %s", res.Verdict)
	}
	if res.Checks[1].ExitCode != 3 || !strings.Contains(res.Checks[1].Output, "kaboom") {
		t.Fatalf("failing check = %+v", res.Checks[1])
	}
	out := res.Format(res.Fingerprint)
	if !strings.Contains(out, "FAIL — 1/2 checks passed") {
		t.Fatalf("summary missing: %s", out)
	}
	if !strings.Contains(out, "✓ ok") || !strings.Contains(out, "✗ boom") || !strings.Contains(out, "exit 3") {
		t.Fatalf("check lines missing: %s", out)
	}
	if !strings.Contains(out, "    kaboom") {
		t.Fatalf("failing output excerpt missing: %s", out)
	}
}

func TestRun_TimeoutFails(t *testing.T) {
	ws := t.TempDir()
	writeConfig(t, ws, `{"suites": {"default": {"timeout_seconds": 1, "checks": [
		{"name": "slow", "exe": "sh", "args": ["-c", "sleep 30"]}
	]}}}`)
	r := &Runner{Workspace: ws}
	res := mustRun(t, r, "default")
	if res.Verdict != VerdictFail || !res.Checks[0].TimedOut {
		t.Fatalf("verdict = %s, check = %+v", res.Verdict, res.Checks[0])
	}
	if !strings.Contains(res.Format(res.Fingerprint), "timed out") {
		t.Fatal("timeout must be reported")
	}
}

func TestRun_CancelledNeverPass(t *testing.T) {
	ws := t.TempDir()
	writeConfig(t, ws, shSuite("echo fine"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &Runner{Workspace: ws}
	res, err := r.Run(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictCancelled {
		t.Fatalf("verdict = %s", res.Verdict)
	}
	if !strings.Contains(res.Format(res.Fingerprint), "Cancelled is never a pass") {
		t.Fatal("cancelled result must say it is not a pass")
	}
}

func TestRun_WrapFailureBlocksInsteadOfRunningBare(t *testing.T) {
	ws := t.TempDir()
	writeConfig(t, ws, shSuite("touch ran.txt"))
	r := &Runner{
		Workspace: ws,
		Wrap:      func([]string, bool) ([]string, error) { return nil, fmt.Errorf("wrap unsupported: gone") },
	}
	res := mustRun(t, r, "default")
	if res.Verdict != VerdictBlocked || !strings.Contains(res.Reason, "wrap unsupported") {
		t.Fatalf("verdict = %s, reason = %q", res.Verdict, res.Reason)
	}
	if _, err := os.Stat(filepath.Join(ws, "ran.txt")); err == nil {
		t.Fatal("a wrap failure must never run the check bare")
	}
}

func TestRun_WrapReceivesAllowWrite(t *testing.T) {
	ws := t.TempDir()
	writeConfig(t, ws, `{"suites": {"default": {"allow_write": true, "checks": [
		{"name": "ok", "exe": "sh", "args": ["-c", "true"]}
	]}}}`)
	var gotAllow bool
	r := &Runner{
		Workspace: ws,
		Mechanism: "bwrap",
		Wrap: func(argv []string, allowWrite bool) ([]string, error) {
			gotAllow = allowWrite
			return argv, nil
		},
	}
	res := mustRun(t, r, "default")
	if !gotAllow {
		t.Fatal("allow_write suites must request a writable workspace")
	}
	if !strings.Contains(res.Contained, "workspace writable") {
		t.Fatalf("Contained = %q", res.Contained)
	}
}

func TestRun_ContainmentReportedHonestly(t *testing.T) {
	ws := t.TempDir()
	writeConfig(t, ws, shSuite("true"))
	res := mustRun(t, &Runner{Workspace: ws}, "default")
	if !strings.Contains(res.Contained, "unconfined") {
		t.Fatalf("bare runs must report unconfined, got %q", res.Contained)
	}
	r := &Runner{Workspace: ws, Mechanism: "bwrap", Wrap: func(argv []string, _ bool) ([]string, error) { return argv, nil }}
	res = mustRun(t, r, "default")
	if !strings.Contains(res.Contained, "bwrap, read-only workspace") {
		t.Fatalf("Contained = %q", res.Contained)
	}
}

func TestRun_EvidenceStoredAndCited(t *testing.T) {
	ws := t.TempDir()
	writeConfig(t, ws, shSuite("echo kept-output; exit 1"))
	var storedTool, storedContent string
	r := &Runner{
		Workspace: ws,
		Evidence: func(tool string, content []byte) (string, error) {
			storedTool, storedContent = tool, string(content)
			return "ev-0011223344556677", nil
		},
	}
	res := mustRun(t, r, "default")
	if storedTool != "quality_gate:default:check" || !strings.Contains(storedContent, "kept-output") {
		t.Fatalf("evidence stored = %q %q", storedTool, storedContent)
	}
	if res.Checks[0].EvidenceID != "ev-0011223344556677" {
		t.Fatalf("check evidence id = %q", res.Checks[0].EvidenceID)
	}
	if !strings.Contains(res.Format(res.Fingerprint), "evidence ev-0011223344556677") {
		t.Fatal("formatted result must cite the evidence id")
	}
}

func TestRun_InlineOutputBounded(t *testing.T) {
	ws := t.TempDir()
	writeConfig(t, ws, shSuite(`i=0; while [ $i -lt 2000 ]; do echo "line $i is filler"; i=$((i+1)); done; exit 1`))
	r := &Runner{Workspace: ws}
	res := mustRun(t, r, "default")
	if len(res.Checks[0].Output) > MaxInlineBytes {
		t.Fatalf("inline excerpt = %d bytes, cap %d", len(res.Checks[0].Output), MaxInlineBytes)
	}
	if !strings.Contains(res.Checks[0].Output, "line 1999") {
		t.Fatal("excerpt must keep the tail, where failures land")
	}
}

func TestRun_SingleFlight(t *testing.T) {
	ws := t.TempDir()
	writeConfig(t, ws, shSuite("true"))
	r := &Runner{Workspace: ws}
	if err := r.begin("default"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), "default"); err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("concurrent run should be refused, got %v", err)
	}
	if got := r.Start("default"); !strings.Contains(got, "already in progress") {
		t.Fatalf("Start during a run = %q", got)
	}
	if got := r.Status(); !strings.Contains(got, "in progress") {
		t.Fatalf("Status during a run = %q", got)
	}
	r.finish(&Result{Suite: "default", Verdict: VerdictPass})
}

func TestStatus_NoRunsYet(t *testing.T) {
	r := &Runner{Workspace: t.TempDir()}
	if got := r.Status(); !strings.Contains(got, "No gate runs") || !strings.Contains(got, ConfigRelPath) {
		t.Fatalf("Status = %q", got)
	}
}

func TestStart_BackgroundRunLandsInStatus(t *testing.T) {
	ws := t.TempDir()
	writeConfig(t, ws, shSuite("echo done"))
	r := &Runner{Workspace: ws}
	if got := r.Start(""); !strings.Contains(got, `suite "default"`) {
		t.Fatalf("Start = %q", got)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := r.Status()
		if strings.Contains(got, "PASS") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background run never finished: %q", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestFingerprint_NonRepoUnverified(t *testing.T) {
	fp := TakeFingerprint(t.TempDir())
	if fp.Repo {
		t.Fatal("a plain directory must not fingerprint as a repo")
	}
	if !strings.Contains(fp.Describe(), "not a git repository") {
		t.Fatalf("Describe = %q", fp.Describe())
	}
}

func TestFingerprint_TracksHeadAndDirt(t *testing.T) {
	ws := gitFixture(t)
	clean := TakeFingerprint(ws)
	if !clean.Repo || clean.Head == "" || clean.DirtyPaths != 0 {
		t.Fatalf("clean fingerprint = %+v", clean)
	}
	if err := os.WriteFile(filepath.Join(ws, "b.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty := TakeFingerprint(ws)
	if dirty == clean || dirty.DirtyPaths != 1 {
		t.Fatalf("dirty fingerprint = %+v", dirty)
	}
	if !strings.Contains(dirty.Describe(), "dirty tree (1") {
		t.Fatalf("Describe = %q", dirty.Describe())
	}
}

func TestResult_StaleOverChangedTree(t *testing.T) {
	ws := gitFixture(t)
	writeConfig(t, ws, shSuite("true"))
	r := &Runner{Workspace: ws}
	res := mustRun(t, r, "default")
	if res.Verdict != VerdictPass {
		t.Fatalf("verdict = %s", res.Verdict)
	}
	if strings.Contains(res.Format(res.Fingerprint), "STALE") {
		t.Fatal("an untouched tree must not report stale")
	}
	if err := os.WriteFile(filepath.Join(ws, "changed.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := r.Status()
	if !strings.Contains(out, "PASS") || !strings.Contains(out, "STALE") {
		t.Fatalf("a pass over a changed tree must report stale, got: %s", out)
	}
}

func TestResult_TreeChangedDuringRunIsStale(t *testing.T) {
	ws := gitFixture(t)
	writeConfig(t, ws, shSuite("echo mid-run >> a.txt"))
	r := &Runner{Workspace: ws}
	res := mustRun(t, r, "default")
	if !res.ChangedDuringRun {
		t.Fatal("a check that mutates the tree must mark the result changed-during-run")
	}
	if !strings.Contains(res.Format(res.Fingerprint), "STALE") {
		t.Fatal("changed-during-run must render stale")
	}
}

func TestTool_RunAndResult(t *testing.T) {
	ws := t.TempDir()
	writeConfig(t, ws, shSuite("echo fine"))
	r := &Runner{Workspace: ws}

	out, err := r.ExecuteTool(json.RawMessage(`{"action": "run"}`))
	if err != nil || !strings.Contains(out, "PASS") {
		t.Fatalf("run = %q, %v", out, err)
	}
	out, err = r.ExecuteTool(json.RawMessage(`{"action": "result"}`))
	if err != nil || !strings.Contains(out, "PASS") {
		t.Fatalf("result = %q, %v", out, err)
	}
	if _, err := r.ExecuteTool(json.RawMessage(`{"action": "install"}`)); err == nil {
		t.Fatal("unknown actions must error")
	}
	if _, err := r.ExecuteTool(json.RawMessage(`nope`)); err == nil {
		t.Fatal("invalid arguments must error")
	}
}

func TestTool_WrapExecutorDispatch(t *testing.T) {
	ws := t.TempDir()
	writeConfig(t, ws, shSuite("true"))
	r := &Runner{Workspace: ws}
	executor := r.WrapExecutor(func(name string, _ json.RawMessage) (string, error) { return "next:" + name, nil })
	out, err := executor(ToolName, json.RawMessage(`{"action": "result"}`))
	if err != nil || !strings.Contains(out, "No gate runs") {
		t.Fatalf("gate dispatch = %q, %v", out, err)
	}
	out, err = executor("read_file", nil)
	if err != nil || out != "next:read_file" {
		t.Fatalf("passthrough = %q, %v", out, err)
	}
}

func TestBoundedWriter(t *testing.T) {
	w := &boundedWriter{limit: 100}
	if _, err := w.Write([]byte("short")); err != nil {
		t.Fatal(err)
	}
	if w.String() != "short" {
		t.Fatalf("small writes must pass through, got %q", w.String())
	}

	w = &boundedWriter{limit: 100}
	for i := 0; i < 100; i++ {
		if _, err := fmt.Fprintf(w, "chunk-%03d\n", i); err != nil {
			t.Fatal(err)
		}
	}
	out := w.String()
	if len(out) > 200 {
		t.Fatalf("bounded output = %d bytes", len(out))
	}
	if !strings.Contains(out, "chunk-000") || !strings.Contains(out, "chunk-099") || !strings.Contains(out, "elided") {
		t.Fatalf("head/tail/elision missing: %q", out)
	}
}

func TestTailExcerpt(t *testing.T) {
	if got := tailExcerpt("small", 100); got != "small" {
		t.Fatalf("tailExcerpt small = %q", got)
	}
	long := strings.Repeat("aaaa\n", 100) + "final line"
	// The 20-byte tail starts mid-line; the partial first line is trimmed.
	if got := tailExcerpt(long, 20); got != "aaaa\nfinal line" {
		t.Fatalf("tailExcerpt = %q", got)
	}
}

// Summarize reads back what Format writes (S-098): the round trip is the
// point, so the fixtures here are real runs rather than hand-typed strings.
func TestSummarize_RoundTripsAFormattedResult(t *testing.T) {
	ws := t.TempDir()
	writeConfig(t, ws, `{"suites": {
		"good": {"checks": [{"name": "ok", "exe": "sh", "args": ["-c", "echo fine"]}]},
		"bad": {"checks": [
			{"name": "ok", "exe": "sh", "args": ["-c", "echo fine"]},
			{"name": "boom", "exe": "sh", "args": ["-c", "exit 3"]}
		]}
	}}`)
	r := &Runner{Workspace: ws}

	res := mustRun(t, r, "good")
	s, ok := Summarize(res.Format(res.Fingerprint))
	if !ok {
		t.Fatal("a formatted pass should summarize")
	}
	if s.Suite != "good" || s.Verdict != VerdictPass || !s.OK() {
		t.Fatalf("pass summary = %+v", s)
	}
	if s.Passed != 1 || s.Total != 1 || s.Duration == "" {
		t.Fatalf("the tally and duration should survive the round trip: %+v", s)
	}

	res = mustRun(t, r, "bad")
	if s, _ = Summarize(res.Format(res.Fingerprint)); s.Verdict != VerdictFail || s.Passed != 1 || s.Total != 2 || s.OK() {
		t.Fatalf("fail summary = %+v", s)
	}
}

func TestSummarize_AStalePassIsNotAPass(t *testing.T) {
	res := &Result{
		Suite: "default", Verdict: VerdictPass, ChangedDuringRun: true,
		Checks: []CheckResult{{Name: "ok"}},
	}
	s, ok := Summarize(res.Format(res.Fingerprint))
	if !ok || !s.Stale {
		t.Fatalf("a verdict the run disowned is stale, got %+v (ok=%v)", s, ok)
	}
	if s.OK() {
		t.Fatal("a stale pass must not read as green")
	}
}

func TestSummarize_RejectsAnythingElse(t *testing.T) {
	for _, in := range []string{
		"", "error: no such tool",
		"No gate runs this session yet. Suites are defined in .shhh/quality.json.",
		"A gate run (suite \"default\") is in progress; ask again shortly.",
	} {
		if s, ok := Summarize(in); ok {
			t.Errorf("Summarize(%q) should report false, got %+v", in, s)
		}
	}
}
