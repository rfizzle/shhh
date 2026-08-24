package structural

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// stubLookPath makes only the named binaries discoverable for the duration of
// the test.
func stubLookPath(t *testing.T, found map[string]string) {
	t.Helper()
	orig := lookPath
	lookPath = func(name string) (string, bool) {
		path, ok := found[name]
		return path, ok
	}
	t.Cleanup(func() { lookPath = orig })
}

func newTestToolset(t *testing.T, bins map[string]string) *Toolset {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if bins == nil {
		bins = map[string]string{}
	}
	return &Toolset{root: root, bins: bins, timeout: SpawnTimeout}
}

// writeScript drops an executable shell script for run() tests.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixtures need a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "fake-tool")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func contains(argv []string, s string) bool {
	for _, a := range argv {
		if a == s {
			return true
		}
	}
	return false
}

// indexOf returns the first index of s in argv, or -1.
func indexOf(argv []string, s string) int {
	for i, a := range argv {
		if a == s {
			return i
		}
	}
	return -1
}

func TestDetectRegistersOnlyFoundBinaries(t *testing.T) {
	stubLookPath(t, map[string]string{"fd": "/usr/bin/fd", "tokei": "/usr/bin/tokei"})
	ts := NewToolset(t.TempDir())
	if ts == nil {
		t.Fatal("expected a toolset")
	}

	defs := ts.Definitions()
	if len(defs) != 2 {
		t.Fatalf("expected 2 definitions, got %d", len(defs))
	}
	if defs[0].Name != FdToolName || defs[1].Name != TokeiToolName {
		t.Fatalf("unexpected definitions: %s, %s", defs[0].Name, defs[1].Name)
	}
	if !ts.Has(FdToolName) || ts.Has(SdToolName) || ts.Has(JaqToolName) || ts.Has(AstGrepToolName) {
		t.Fatal("Has does not reflect the found binaries")
	}
}

func TestExecuteUnavailableToolIsCleanError(t *testing.T) {
	ts := newTestToolset(t, nil)

	if _, err := ts.Execute(SdToolName, json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "not found on PATH") {
		t.Fatalf("expected a missing-binary error, got %v", err)
	}
	if _, err := ts.Execute("nonsense", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "unknown structural tool") {
		t.Fatalf("expected an unknown-tool error, got %v", err)
	}
}

func TestWrapExecutorFallsThrough(t *testing.T) {
	ts := newTestToolset(t, nil)
	exec := ts.WrapExecutor(func(name string, args json.RawMessage) (string, error) {
		return "fallback:" + name, nil
	})
	out, err := exec("read_file", json.RawMessage(`{}`))
	if err != nil || out != "fallback:read_file" {
		t.Fatalf("expected fallback dispatch, got %q, %v", out, err)
	}
}

func TestResolvePathContainment(t *testing.T) {
	ts := newTestToolset(t, nil)
	sub := filepath.Join(ts.root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	if got, err := ts.resolvePath(""); err != nil || got != ts.root {
		t.Fatalf("empty path should resolve to the root, got %q, %v", got, err)
	}
	if got, err := ts.resolvePath("."); err != nil || got != ts.root {
		t.Fatalf(". should resolve to the root, got %q, %v", got, err)
	}
	if got, err := ts.resolvePath("sub"); err != nil || got != sub {
		t.Fatalf("relative subdirectory should resolve, got %q, %v", got, err)
	}

	if _, err := ts.resolvePath(".."); err == nil || !strings.Contains(err.Error(), "outside the workspace") {
		t.Fatalf("expected .. to be rejected, got %v", err)
	}
	if _, err := ts.resolvePath(filepath.Join("sub", "..", "..")); err == nil {
		t.Fatal("expected traversal through a subdirectory to be rejected")
	}
	outside := t.TempDir()
	if _, err := ts.resolvePath(outside); err == nil {
		t.Fatal("expected an absolute path outside the workspace to be rejected")
	}
	if _, err := ts.resolvePath("does-not-exist"); err == nil || !strings.Contains(err.Error(), "cannot access path") {
		t.Fatalf("expected a missing path error, got %v", err)
	}
}

func TestResolvePathSymlinkEscapeRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixtures need POSIX symlinks")
	}
	ts := newTestToolset(t, nil)
	outside := t.TempDir()
	link := filepath.Join(ts.root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.resolvePath("escape"); err == nil || !strings.Contains(err.Error(), "outside the workspace") {
		t.Fatalf("expected a symlink escape to be rejected, got %v", err)
	}
}

func TestResolvePathsRequiresEntries(t *testing.T) {
	ts := newTestToolset(t, nil)
	if _, err := ts.resolvePaths(nil); err == nil {
		t.Fatal("expected empty paths to be rejected")
	}
	if _, err := ts.resolvePaths([]string{""}); err == nil {
		t.Fatal("expected an empty paths entry to be rejected")
	}
}

func TestBuildFdArgvInvariants(t *testing.T) {
	argv, err := buildFdArgv(fdArgs{Pattern: "--delete", Type: "file", Extension: "-x", Hidden: true, NoIgnore: true, IgnoreCase: true, MaxDepth: 2, Limit: 10}, "/ws/sub")
	if err != nil {
		t.Fatal(err)
	}
	// The pattern rides after the -- delimiter, never as a bare token before it.
	sep := indexOf(argv, "--")
	if sep < 0 || argv[len(argv)-1] != "--delete" || sep != len(argv)-2 {
		t.Fatalf("pattern must be the sole token after --: %v", argv)
	}
	// The search path is attached, never positional.
	if !contains(argv, "--search-path=/ws/sub") {
		t.Fatalf("search path must ride attached: %v", argv)
	}
	// Value-taking options are attached so a leading dash cannot inject.
	for _, want := range []string{"--extension=-x", "--type=f", "--max-depth=2", "--max-results=10", "--color=never", "--hidden", "--no-ignore", "--ignore-case"} {
		if !contains(argv, want) {
			t.Fatalf("missing %s in %v", want, argv)
		}
	}

	if _, err := buildFdArgv(fdArgs{Glob: true, Literal: true}, "/ws"); err == nil {
		t.Fatal("expected glob+literal to be rejected")
	}
	if _, err := buildFdArgv(fdArgs{Type: "socket"}, "/ws"); err == nil {
		t.Fatal("expected an invalid type to be rejected")
	}

	// No pattern: no -- delimiter, listing is bounded by the default limit.
	argv, err = buildFdArgv(fdArgs{}, "/ws")
	if err != nil {
		t.Fatal(err)
	}
	if contains(argv, "--") {
		t.Fatalf("no pattern should mean no delimiter: %v", argv)
	}
	if !contains(argv, "--max-results=200") {
		t.Fatalf("default limit missing: %v", argv)
	}
	// Over-limit requests are clamped.
	argv, _ = buildFdArgv(fdArgs{Limit: 100000}, "/ws")
	if !contains(argv, "--max-results=500") {
		t.Fatalf("limit not clamped: %v", argv)
	}
}

func TestBuildAstGrepArgvInvariants(t *testing.T) {
	argv := buildAstGrepArgv(astGrepArgs{Pattern: "-U", Rewrite: "--update-all", Lang: "-x", Context: 3}, "/ws/pkg")

	// Model-supplied values ride attached, so they can never become options.
	for _, want := range []string{"--pattern=-U", "--rewrite=--update-all", "--lang=-x", "--context=3"} {
		if !contains(argv, want) {
			t.Fatalf("missing %s in %v", want, argv)
		}
	}
	// The write flags are never in the vocabulary: rewrite is preview-only.
	if contains(argv, "-U") || contains(argv, "--update-all") {
		t.Fatalf("update flags must never appear: %v", argv)
	}
	// The search path rides after the -- delimiter.
	sep := indexOf(argv, "--")
	if sep < 0 || argv[len(argv)-1] != "/ws/pkg" || sep != len(argv)-2 {
		t.Fatalf("path must be the sole token after --: %v", argv)
	}
}

func TestBuildSdArgvInvariants(t *testing.T) {
	argv := buildSdArgv(sdArgs{Pattern: "-f", Replacement: "--preview-x", IgnoreCase: true, Multiline: true, DotAll: true, WordBoundary: true, MaxReplacements: 5, FixedStrings: true}, []string{"/ws/a.txt", "/ws/b.txt"})

	// --preview is always present: sd writes in place by default.
	if argv[0] != "--preview" {
		t.Fatalf("--preview must always lead: %v", argv)
	}
	// Pattern, replacement, and paths all follow the -- delimiter — a "-f"
	// pattern is otherwise consumed as the --flags value.
	sep := indexOf(argv, "--")
	if sep < 0 {
		t.Fatalf("missing -- delimiter: %v", argv)
	}
	tail := argv[sep+1:]
	if len(tail) != 4 || tail[0] != "-f" || tail[1] != "--preview-x" || tail[2] != "/ws/a.txt" || tail[3] != "/ws/b.txt" {
		t.Fatalf("pattern, replacement, and paths must follow --: %v", argv)
	}
	for _, want := range []string{"--fixed-strings", "--flags=imsw", "--max-replacements=5"} {
		if !contains(argv[:sep], want) {
			t.Fatalf("missing %s in %v", want, argv)
		}
	}

	// --preview survives every input combination, including none.
	if argv := buildSdArgv(sdArgs{Pattern: "a", Replacement: "b"}, []string{"/ws/x"}); argv[0] != "--preview" {
		t.Fatalf("--preview must always lead: %v", argv)
	}
}

func TestBuildTokeiArgvInvariants(t *testing.T) {
	argv, err := buildTokeiArgv(tokeiArgs{Exclude: []string{"-evil", "*.min.js"}, Hidden: true, NoIgnore: true, Sort: "code"}, "/ws")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--exclude=-evil", "--exclude=*.min.js", "--sort=code", "--hidden", "--no-ignore"} {
		if !contains(argv, want) {
			t.Fatalf("missing %s in %v", want, argv)
		}
	}
	sep := indexOf(argv, "--")
	if sep < 0 || argv[len(argv)-1] != "/ws" || sep != len(argv)-2 {
		t.Fatalf("path must be the sole token after --: %v", argv)
	}

	if _, err := buildTokeiArgv(tokeiArgs{Sort: "--files"}, "/ws"); err == nil {
		t.Fatal("expected an invalid sort to be rejected")
	}
	if _, err := buildTokeiArgv(tokeiArgs{Exclude: []string{""}}, "/ws"); err == nil {
		t.Fatal("expected an empty exclude entry to be rejected")
	}
}

func TestBuildJaqArgvInvariants(t *testing.T) {
	argv := buildJaqArgv(jaqArgs{Expression: "--in-place", Slurp: true, RawOutput: true, Compact: true, Indent: 2}, []string{"/ws/a.json"})

	// The expression and paths follow the -- delimiter — a dash-prefixed
	// expression is otherwise parsed as an unknown flag.
	sep := indexOf(argv, "--")
	if sep < 0 {
		t.Fatalf("missing -- delimiter: %v", argv)
	}
	tail := argv[sep+1:]
	if len(tail) != 2 || tail[0] != "--in-place" || tail[1] != "/ws/a.json" {
		t.Fatalf("expression and paths must follow --: %v", argv)
	}
	// --indent rides as two separate tokens: jaq rejects the attached form.
	if i := indexOf(argv[:sep], "--indent"); i < 0 || argv[i+1] != "2" {
		t.Fatalf("--indent must be two tokens: %v", argv)
	}
	// The file-reading and in-place flags are never in the vocabulary.
	for _, forbidden := range []string{"-L", "-f", "--from-file", "--slurpfile", "--rawfile", "-i"} {
		if contains(argv[:sep], forbidden) {
			t.Fatalf("forbidden flag %s in %v", forbidden, argv)
		}
	}
	for _, want := range []string{"--slurp", "--raw-output", "--compact-output"} {
		if !contains(argv[:sep], want) {
			t.Fatalf("missing %s in %v", want, argv)
		}
	}
}

func TestRunCapturesOutput(t *testing.T) {
	script := writeScript(t, `printf 'hello\nworld\n'`)
	ts := newTestToolset(t, map[string]string{FdToolName: script})

	out, err := ts.run(FdToolName, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello\nworld\n" {
		t.Fatalf("unexpected output %q", out)
	}
}

func TestRunNonZeroExitIsCleanError(t *testing.T) {
	script := writeScript(t, `echo 'bad pattern' >&2; exit 2`)
	ts := newTestToolset(t, map[string]string{FdToolName: script})

	_, err := ts.run(FdToolName, nil)
	if err == nil || !strings.Contains(err.Error(), "bad pattern") {
		t.Fatalf("expected the stderr detail in the error, got %v", err)
	}
}

func TestRunTimesOut(t *testing.T) {
	script := writeScript(t, `sleep 5`)
	ts := newTestToolset(t, map[string]string{FdToolName: script})
	ts.timeout = 100 * time.Millisecond

	start := time.Now()
	_, err := ts.run(FdToolName, nil)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected a timeout error, got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("timeout did not bound the run")
	}
}

func TestRunOutputFloodIsTruncated(t *testing.T) {
	script := writeScript(t, `while :; do printf 'xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n'; done`)
	ts := newTestToolset(t, map[string]string{FdToolName: script})

	start := time.Now()
	out, err := ts.run(FdToolName, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "output truncated") {
		t.Fatal("expected a truncation notice")
	}
	if len(out) > MaxOutputBytes+200 {
		t.Fatalf("output not bounded: %d bytes", len(out))
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("flooding process was not killed promptly")
	}
}

func TestExecuteFdEndToEnd(t *testing.T) {
	// A fake fd that echoes its argv proves the resolved path reaches the
	// spawn and the pattern stays behind the delimiter.
	script := writeScript(t, `printf '%s\n' "$@"`)
	ts := newTestToolset(t, map[string]string{FdToolName: script})

	out, err := ts.Execute(FdToolName, json.RawMessage(`{"pattern": "-x", "path": "."}`))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	if lines[len(lines)-1] != "-x" || lines[len(lines)-2] != "--" {
		t.Fatalf("pattern must trail the -- delimiter: %q", out)
	}
	if !contains(lines, "--search-path="+ts.root) {
		t.Fatalf("resolved search path missing: %q", out)
	}

	// A path outside the workspace never spawns.
	if _, err := ts.Execute(FdToolName, json.RawMessage(`{"path": ".."}`)); err == nil || !strings.Contains(err.Error(), "outside the workspace") {
		t.Fatalf("expected containment rejection, got %v", err)
	}
}

func TestExecuteEmptyResultsMessages(t *testing.T) {
	script := writeScript(t, `:`)
	ts := newTestToolset(t, map[string]string{
		FdToolName:      script,
		AstGrepToolName: script,
		SdToolName:      script,
		JaqToolName:     script,
	})
	file := filepath.Join(ts.root, "a.json")
	if err := os.WriteFile(file, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		tool string
		args string
		want string
	}{
		{FdToolName, `{}`, "No files matched."},
		{AstGrepToolName, `{"pattern": "foo"}`, "No matches."},
		{SdToolName, `{"pattern": "a", "replacement": "b", "paths": ["a.json"]}`, "No replacements: the pattern did not match."},
		{JaqToolName, `{"expression": ".", "paths": ["a.json"]}`, "(no output)"},
	}
	for _, c := range cases {
		out, err := ts.Execute(c.tool, json.RawMessage(c.args))
		if err != nil {
			t.Fatalf("%s: %v", c.tool, err)
		}
		if out != c.want {
			t.Fatalf("%s: got %q, want %q", c.tool, out, c.want)
		}
	}
}

func TestExecuteSdPreviewBanner(t *testing.T) {
	script := writeScript(t, `printf 'changed line\n'`)
	ts := newTestToolset(t, map[string]string{SdToolName: script})
	file := filepath.Join(ts.root, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := ts.Execute(SdToolName, json.RawMessage(`{"pattern": "a", "replacement": "b", "paths": ["a.txt"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Preview only — no file was changed.") {
		t.Fatalf("expected the preview banner, got %q", out)
	}
}
