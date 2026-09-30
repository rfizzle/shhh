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
	if !ts.Has(FdToolName) || ts.Has(SdToolName) || ts.Has(AstGrepToolName) {
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
	argv := buildAstGrepArgv(astGrepArgs{Pattern: "-U", Rewrite: "--update-all", Lang: "-x", Selector: "-y", Context: 3}, "/ws/pkg")

	// Model-supplied values ride attached, so they can never become options.
	for _, want := range []string{"--pattern=-U", "--rewrite=--update-all", "--lang=-x", "--selector=-y", "--context=3"} {
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

// Structured data files are read by the built-in query tool, in every
// session, so a jaq or yq on PATH registers nothing here and doctor does not
// look for either: a second tool for the same question is a second schema in
// every request and a choice the model has to make for nothing.
// See docs/capabilities/coding-agent.md#structured-files-are-read-in-one-call.
func TestNewToolsetRegistersNoDataQueryTool(t *testing.T) {
	stubLookPath(t, map[string]string{"jaq": "/usr/bin/jaq", "yq": "/usr/bin/yq"})
	ts := NewToolset(t.TempDir())
	if ts == nil {
		t.Fatal("expected a toolset")
	}
	if defs := ts.Definitions(); len(defs) != 0 {
		t.Fatalf("a jaq or yq on PATH reached the model: %+v", defs)
	}
	for _, bin := range ToolBinaries() {
		if bin == "jaq" || bin == "yq" {
			t.Errorf("doctor still looks for %s", bin)
		}
	}
	for _, d := range Registrable() {
		if d.Name == "jaq" || d.Name == "yq" {
			t.Errorf("%s is still registrable", d.Name)
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
	// sd previews one file at a time and prints it as it would read, so a
	// file with no match comes back as it was.
	unchanged := writeScript(t, `for last; do :; done; cat "$last"`)
	ts := newTestToolset(t, map[string]string{
		FdToolName:      script,
		AstGrepToolName: script,
		SdToolName:      unchanged,
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

// A sub-agent stands somewhere else and needs the same tools contained to
// where it stands. The probe is not repeated — PATH is what it was, and a
// spawn, a retry and a handoff would each pay for it again — and the writing
// half of git does not come along, because a child has no approval card and
// its work comes back as a patch.
func TestRootedIsTheSameToolsElsewhereWithoutTheWriteHalf(t *testing.T) {
	session := newTestToolset(t, map[string]string{
		FdToolName:       "/usr/bin/fd",
		GitToolName:      "/usr/bin/git",
		GitWriteToolName: "/usr/bin/git",
	})
	elsewhere, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	child := session.Rooted(elsewhere)
	if child == nil {
		t.Fatal("a toolset that found its binaries handed a child none")
	}
	if child.root != elsewhere {
		t.Errorf("the child's tools are contained to %q, not where it stands", child.root)
	}
	for _, want := range []string{FdToolName, GitToolName} {
		if !child.Has(want) {
			t.Errorf("%s was found once and not carried over", want)
		}
	}
	if child.Has(GitWriteToolName) {
		t.Error("the writing half of git followed a child that has nobody to ask")
	}
	// The session's own toolset is untouched by the copy.
	if !session.Has(GitWriteToolName) || session.root == elsewhere {
		t.Error("rooting a copy changed the toolset it was copied from")
	}
	// A root that cannot be resolved is no toolset rather than one contained
	// to nothing, and a session that registered none hands out none.
	if got := session.Rooted(filepath.Join(elsewhere, "does-not-exist")); got != nil {
		t.Errorf("a root that is not there produced a toolset: %+v", got)
	}
	if got := (*Toolset)(nil).Rooted(elsewhere); got != nil {
		t.Errorf("a session with no tools handed a child some: %+v", got)
	}
}

// A broad structural search or a replacement preview over many files runs past
// MaxOutputBytes, and what is cut is kept nowhere, so both definitions have to
// say how to narrow and must not let a truncated result pass for the whole.
// sd's preview is a diff of the change, so it must say so and must not go
// on telling the model every named file comes back whole.
func TestStructuralPreviewDefinitionsTeachNarrowing(t *testing.T) {
	for _, tool := range []struct {
		name   string
		desc   string
		schema string
		want   []string
		args   []string
	}{
		{AstGrepToolName, astGrepTool.Description, string(astGrepTool.Parameters),
			[]string{"PREVIEW", "point path at the directory or file", "set lang", "leave context off", "cut off and lost", "not every match", "not the whole diff", "narrower path"},
			[]string{"name the narrowest one", "keeps other languages' files out", "leave it unset"}},
		{SdToolName, sdTool.Description, string(sdTool.Parameters),
			[]string{"PREVIEW", "never modifies files", "unified diff of the lines that would change", "no match adds nothing", "cut off and lost", "not every file", "smaller batches"},
			[]string{"no match adds nothing to the diff"}},
	} {
		for _, want := range tool.want {
			if !strings.Contains(tool.desc, want) {
				t.Errorf("%s description should say %q:\n%s", tool.name, want, tool.desc)
			}
		}
		for _, want := range tool.args {
			if !strings.Contains(tool.schema, want) {
				t.Errorf("%s arguments should say %q:\n%s", tool.name, want, tool.schema)
			}
		}
		if strings.Contains(tool.desc, "evidence") {
			t.Errorf("%s description must not send a truncated result to evidence:\n%s", tool.name, tool.desc)
		}
	}
	for _, stale := range []string{"in full", "printed whole"} {
		if strings.Contains(sdTool.Description, stale) || strings.Contains(string(sdTool.Parameters), stale) {
			t.Errorf("sd's definition still says %q, which the diff preview made untrue", stale)
		}
	}
}

// tree-sitter-go reads a qualified call with one argument, written alone, as
// a conversion to the type pkg.Name, so errors.New($A) matches no call in
// ast-grep 0.45.3 while strings.Contains($A, $B), which no conversion can be,
// matches. The one shape that finds every such call is the call inside a
// function body with the selector naming the call, so the pattern's
// description has to say so and the selector has to exist for it to name.
func TestAstGrepPatternTeachesTheOneArgumentGoCall(t *testing.T) {
	schema := string(astGrepTool.Parameters)
	for _, want := range []string{"errors.New($A)", "parses as a type conversion and matches nothing", `func _() { errors.New($A) }`, `set selector to \"call_expression\"`} {
		if !strings.Contains(schema, want) {
			t.Errorf("ast_grep's pattern description should say %q:\n%s", want, schema)
		}
	}
	var params struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(astGrepTool.Parameters, &params); err != nil {
		t.Fatal(err)
	}
	if _, ok := params.Properties["selector"]; !ok {
		t.Fatalf("the pattern description names a selector the definition does not offer:\n%s", schema)
	}
}

// The failure the ast_grep guidance is for: a pattern run over the whole
// workspace floods past the cap and comes back cut off, while the same
// pattern pointed at the directory that holds the matches comes back whole.
// The script stands in for ast-grep: it floods when handed the workspace root
// and answers two matches for anything narrower.
func TestAstGrepNarrowPathAnswersWhereTheWholeTreeIsCut(t *testing.T) {
	script := writeScript(t, `for last; do :; done
if [ "$last" = "$ROOT" ]; then yes 'pkg/a.go:1:	if err != nil { return err }' | head -c 300000
else printf 'internal/x/a.go:3:	if err != nil { return err }\ninternal/x/b.go:9:	if err != nil { return err }\n'; fi`)
	ts := newTestToolset(t, map[string]string{AstGrepToolName: script})
	t.Setenv("ROOT", ts.root)
	if err := os.MkdirAll(filepath.Join(ts.root, "internal", "x"), 0o755); err != nil {
		t.Fatal(err)
	}

	whole, err := ts.Execute(AstGrepToolName, json.RawMessage(`{"pattern": "if err != nil { return $$$R }", "lang": "go"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(whole, "output truncated") || !strings.Contains(whole, "narrow the query") {
		t.Fatalf("a search over the whole tree should be cut at the cap, got %d bytes ending %q", len(whole), whole[max(0, len(whole)-80):])
	}

	narrow, err := ts.Execute(AstGrepToolName, json.RawMessage(`{"pattern": "if err != nil { return $$$R }", "lang": "go", "path": "internal/x"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "internal/x/a.go:3:\tif err != nil { return err }\ninternal/x/b.go:9:\tif err != nil { return err }"
	if narrow != want {
		t.Fatalf("a search over the directory holding the matches should come back whole, got %q", narrow)
	}
}

// sd's own preview prints every named file whole, so five 16 KB files with one
// occurrence each ran past the cap. The preview is now the diff of the change:
// one hunk per changed file whatever the file's size, nothing for a file with
// no match. The script stands in for sd handed one path, which prints that
// file as it would read after the replacement.
func TestSdPreviewIsTheDiffOfTheChange(t *testing.T) {
	script := writeScript(t, `for last; do :; done; sed 's/Foo/Bar/' "$last"`)
	ts := newTestToolset(t, map[string]string{SdToolName: script})
	fileOf := func(lines int) string {
		var b strings.Builder
		for i := range lines {
			if i == lines/2 {
				b.WriteString("line Foo\n")
				continue
			}
			b.WriteString("line padding padding padding\n")
		}
		return b.String()
	}
	var names []string
	for _, n := range []string{"a.go", "b.go", "c.go", "d.go", "e.go"} {
		if err := os.WriteFile(filepath.Join(ts.root, n), []byte(fileOf(550)), 0o644); err != nil {
			t.Fatal(err)
		}
		names = append(names, `"`+n+`"`)
	}
	if err := os.WriteFile(filepath.Join(ts.root, "big.go"), []byte(fileOf(5500)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ts.root, "none.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	names = append(names, `"big.go"`, `"none.go"`)

	out, err := ts.Execute(SdToolName, json.RawMessage(`{"pattern": "Foo", "replacement": "Bar", "paths": [`+strings.Join(names, ",")+`]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Preview only") || strings.Contains(out, "output truncated") {
		t.Fatalf("a five-occurrence rename should come back whole, got %d bytes starting %q", len(out), out[:min(len(out), 120)])
	}
	if len(out) > 4<<10 {
		t.Fatalf("the preview should follow the change, not the %d bytes of files named: got %d bytes", 5*len(fileOf(550))+len(fileOf(5500)), len(out))
	}
	for _, n := range []string{"a.go", "b.go", "c.go", "d.go", "e.go", "big.go"} {
		if !strings.Contains(out, "--- a/"+n+"\n+++ b/"+n+"\n@@ ") {
			t.Errorf("%s should have a diff of its own:\n%s", n, out)
		}
	}
	if strings.Contains(out, "none.go") {
		t.Errorf("a file with no match should add nothing:\n%s", out)
	}
	if got := strings.Count(out, "\n@@ "); got != 6 {
		t.Errorf("one change per file should be one hunk per file, got %d:\n%s", got, out)
	}
	if got := strings.Count(out, "\n-line Foo\n+line Bar\n"); got != 6 {
		t.Errorf("each hunk should carry the changed line, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "@@ -2748,7 +2748,7 @@") {
		t.Errorf("the large file's hunk should sit where its change is:\n%s", out)
	}
}

// A change that really is large is still cut at the cap, with the notice the
// rest of the package's cuts carry, and a file sd cannot be handed is refused
// before anything is spawned.
func TestSdPreviewOfALargeChangeIsCutAtTheCap(t *testing.T) {
	script := writeScript(t, `for last; do :; done; sed 's/Foo/Bar/' "$last"`)
	ts := newTestToolset(t, map[string]string{SdToolName: script})
	body := strings.Repeat("line Foo padding padding\n", 3000)
	if err := os.WriteFile(filepath.Join(ts.root, "a.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := ts.Execute(SdToolName, json.RawMessage(`{"pattern": "Foo", "replacement": "Bar", "paths": ["a.go"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "output truncated at 65536 bytes") {
		t.Fatalf("a diff past the cap should be cut with the notice, got %d bytes ending %q", len(out), out[max(0, len(out)-80):])
	}

	if err := os.Mkdir(filepath.Join(ts.root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Execute(SdToolName, json.RawMessage(`{"pattern": "Foo", "replacement": "Bar", "paths": ["dir"]}`)); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("a directory should be refused by name, got %v", err)
	}
}

// The preview's text is the unified diff internal/diff renders, byte for
// byte: the file header, each hunk's header and its marked lines, and the
// one sentence for a change a line diff cannot show.
func TestSdPreviewSpellsTheUnifiedDiffExactly(t *testing.T) {
	script := writeScript(t, `for last; do :; done; sed 's/Foo/Bar/' "$last"`)
	ts := newTestToolset(t, map[string]string{SdToolName: script})
	if err := os.WriteFile(filepath.Join(ts.root, "a.go"), []byte("one\ntwo Foo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ts.sdFileDiff(sdArgs{Pattern: "Foo", Replacement: "Bar"}, filepath.Join(ts.root, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,3 @@\n one\n-two Foo\n+two Bar\n three\n"; got != want {
		t.Errorf("preview =\n%q\nwant\n%q", got, want)
	}

	ts = newTestToolset(t, map[string]string{SdToolName: writeScript(t, `printf 'x'`)})
	if err := os.WriteFile(filepath.Join(ts.root, "b.go"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = ts.sdFileDiff(sdArgs{Pattern: "x", Replacement: "x"}, filepath.Join(ts.root, "b.go"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "--- a/b.go\n+++ b/b.go\n(only the final newline changes)\n"; got != want {
		t.Errorf("preview =\n%q\nwant\n%q", got, want)
	}
}

// What the probe came to reads back as the binaries found, each with the
// tools registered over it and git last, and the optional ones missing — the
// tools screen's rows, which start nothing to answer.
func TestBinariesNameWhatWasFoundAndWhatWasNot(t *testing.T) {
	ts := newTestToolset(t, map[string]string{
		FdToolName: "/usr/bin/fd", SdToolName: "/usr/bin/sd", GitToolName: "/usr/bin/git",
	})
	found, missing := ts.Binaries()
	var names []string
	for _, b := range found {
		names = append(names, b.Name+"="+strings.Join(b.Tools, ","))
	}
	if got := strings.Join(names, " "); got != "fd=fd sd=sd git=git" {
		t.Errorf("found = %q", got)
	}
	if got := strings.Join(missing, " "); got != "ast-grep tokei" {
		t.Errorf("missing = %q", got)
	}
	if f, m := (*Toolset)(nil).Binaries(); f != nil || m != nil {
		t.Errorf("a nil toolset found %v and missed %v", f, m)
	}
}
