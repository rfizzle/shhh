package structural

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubInsideRepo decides the repository question for the duration of a test.
func stubInsideRepo(t *testing.T, inside bool) {
	t.Helper()
	orig := insideRepo
	insideRepo = func(string) bool { return inside }
	t.Cleanup(func() { insideRepo = orig })
}

// gitArgvFor builds one call's argv with its paths already resolved, so a
// test can read the argv a verb produces without a repository or a spawn.
func gitArgvFor(t *testing.T, ts *Toolset, raw string) []string {
	t.Helper()
	var a gitArgs
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		t.Fatal(err)
	}
	paths, err := ts.resolveGitPaths(a.Paths)
	if err != nil {
		t.Fatalf("resolving paths for %s: %v", raw, err)
	}
	argv, err := buildGitArgv(a, paths)
	if err != nil {
		t.Fatalf("building argv for %s: %v", raw, err)
	}
	return argv
}

func TestGitRegisteredOnlyInsideARepository(t *testing.T) {
	stubLookPath(t, map[string]string{"git": "/usr/bin/git"})

	stubInsideRepo(t, true)
	ts := NewToolset(t.TempDir())
	if ts == nil || !ts.Has(GitToolName) {
		t.Fatal("git should register inside a repository")
	}
	defs := ts.Definitions()
	if len(defs) != 1 || defs[0].Name != GitToolName {
		t.Fatalf("expected the git definition alone, got %v", defs)
	}

	stubInsideRepo(t, false)
	ts = NewToolset(t.TempDir())
	if ts == nil {
		t.Fatal("expected a toolset")
	}
	if ts.Has(GitToolName) {
		t.Fatal("git must not register outside a repository")
	}
	if len(ts.Definitions()) != 0 {
		t.Fatalf("expected no definitions outside a repository, got %v", ts.Definitions())
	}
	if _, err := ts.Execute(GitToolName, json.RawMessage(`{"verb":"status"}`)); err == nil ||
		!strings.Contains(err.Error(), "not inside a git repository") {
		t.Fatalf("expected a clean not-a-repository error, got %v", err)
	}
}

func TestBuildGitArgvPerVerb(t *testing.T) {
	ts := newTestToolset(t, nil)
	file := filepath.Join(ts.root, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		args string
		want []string
	}{
		{
			name: "status",
			args: `{"verb":"status"}`,
			want: []string{"--no-pager", "--no-optional-locks", "status", "--porcelain=v1", "--branch", "--"},
		},
		{
			name: "log",
			args: `{"verb":"log","limit":5,"search":"NewToolset","ref":"main","paths":["main.go"]}`,
			want: []string{
				"--no-pager", "--no-optional-locks", "log", "--no-color", "--no-ext-diff",
				"--no-textconv", "--no-show-signature",
				"--date=short", "--pretty=format:%h %ad %an %s", "--max-count=5",
				"-SNewToolset", "main", "--", file,
			},
		},
		{
			name: "show",
			args: `{"verb":"show","ref":"HEAD~2","stat":true}`,
			want: []string{
				"--no-pager", "--no-optional-locks", "show", "--no-color", "--no-ext-diff",
				"--no-textconv", "--no-show-signature",
				"--unified=3", "--date=short", "--stat", "HEAD~2", "--",
			},
		},
		{
			name: "diff between two refs",
			args: `{"verb":"diff","ref":"main","to_ref":"HEAD"}`,
			want: []string{
				"--no-pager", "--no-optional-locks", "diff", "--no-color", "--no-ext-diff", "--no-textconv",
				"--unified=3", "main", "HEAD", "--",
			},
		},
		{
			name: "diff of the index",
			args: `{"verb":"diff","ref":"main","staged":true,"stat":true}`,
			want: []string{
				"--no-pager", "--no-optional-locks", "diff", "--no-color", "--no-ext-diff", "--no-textconv",
				"--unified=3", "--staged", "--stat", "main", "--",
			},
		},
		{
			name: "blame",
			args: `{"verb":"blame","paths":["main.go"],"start_line":10,"end_line":40,"ref":"v1.2.0"}`,
			want: []string{
				"--no-pager", "--no-optional-locks", "blame", "--no-color-lines", "--no-color-by-age",
				"--no-textconv", "--date=short", "-L10,40", "v1.2.0", "--", file,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := gitArgvFor(t, ts, tc.args)
			if len(got) != len(tc.want) {
				t.Fatalf("argv = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("argv[%d] = %q, want %q\nfull: %v", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

func TestBuildGitArgvAlwaysCarriesTheSafeFlags(t *testing.T) {
	ts := newTestToolset(t, nil)
	if err := os.WriteFile(filepath.Join(ts.root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The minimal call each verb accepts: nothing optional is set, so a flag
	// missing here is one that rides on an argument rather than every spawn.
	minimal := []string{
		`{"verb":"status"}`,
		`{"verb":"log"}`,
		`{"verb":"show","ref":"HEAD"}`,
		`{"verb":"diff"}`,
		`{"verb":"blame","paths":["main.go"]}`,
	}
	// Every flag that writes a file, runs a program, or reaches configuration
	// that names one. None of them has a field to arrive in, so none may
	// appear in any argv this builder produces — and the match is by prefix,
	// because the attached --flag=value form is how one would arrive.
	forbidden := []string{
		"--output", "-O", "-c", "--config-env", "--exec-path",
		"--upload-pack", "--receive-pack", "--ext-diff", "--textconv",
		"--pager", "--show-signature", "--optional-locks",
	}
	for _, raw := range minimal {
		argv := gitArgvFor(t, ts, raw)
		if argv[0] != "--no-pager" {
			t.Fatalf("%s: every spawn leads with --no-pager, got %v", raw, argv)
		}
		if indexOf(argv, "--") < 0 {
			t.Fatalf("%s: every spawn carries the path delimiter, got %v", raw, argv)
		}
		for _, flag := range forbidden {
			for _, got := range argv {
				if got == flag || strings.HasPrefix(got, flag+"=") {
					t.Fatalf("%s: %s must not be in the vocabulary, got %v", raw, flag, argv)
				}
			}
		}
		// Colour is off on every verb, but git spells it three ways.
		coloured := contains(argv, "--no-color") ||
			contains(argv, "--porcelain=v1") ||
			(contains(argv, "--no-color-lines") && contains(argv, "--no-color-by-age"))
		if !coloured {
			t.Fatalf("%s: colour is not forced off, got %v", raw, argv)
		}
	}
}

// The pickaxe value is the one model-supplied string that rides attached to
// its flag rather than after the delimiter, because a separated one that
// began with a dash would be read as the next option.
func TestGitSearchRidesAttachedToItsFlag(t *testing.T) {
	argv, err := buildGitArgv(gitArgs{Verb: gitLog, Search: "-foo"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(argv, "-S-foo") {
		t.Fatalf("the search value should ride attached to -S, got %v", argv)
	}
	if contains(argv, "-foo") || contains(argv, "-S") {
		t.Fatalf("the search value must not become its own token, got %v", argv)
	}
}

// Pathspec magic is neutralised by resolution rather than by a check: every
// path is made absolute first, and magic is only magic at the start of the
// string. Pinned here so it is not re-derived from scratch.
func TestGitPathspecMagicIsNeutralised(t *testing.T) {
	ts := newTestToolset(t, nil)
	got, err := ts.resolveGitPaths([]string{":(glob)**/*.go"})
	if err != nil {
		t.Fatalf("a path that looks like pathspec magic should resolve as a path, got %v", err)
	}
	if !strings.HasPrefix(got[0], ts.root+string(os.PathSeparator)) {
		t.Fatalf("magic should be neutralised by making the path absolute, got %q", got[0])
	}
	// ":/" means "the root of the repository" as a pathspec, and the
	// repository is not the workspace. Resolution turns it into a literal
	// path under the workspace root, which is the neutralising.
	got, err = ts.resolveGitPaths([]string{":/"})
	if err != nil {
		t.Fatalf("the repository-root pathspec should resolve as a path, got %v", err)
	}
	if got[0] != filepath.Join(ts.root, ":") {
		t.Fatalf("the repository-root pathspec should not survive as magic, got %q", got[0])
	}
}

// The spawn's environment is the only place git configuration is overridden,
// and core.fsmonitor is why: git execs it on status, diff and blame.
func TestGitSpawnEnvBlanksTheHookConfig(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.fsmonitor")
	t.Setenv("GIT_CONFIG_VALUE_0", "/tmp/attacker")

	env := spawnEnv(GitToolName)
	var count, key, value int
	for _, v := range env {
		switch {
		case strings.HasPrefix(v, "GIT_CONFIG_COUNT="):
			count++
			if v != "GIT_CONFIG_COUNT=1" {
				t.Fatalf("unexpected count entry %q", v)
			}
		case strings.HasPrefix(v, "GIT_CONFIG_KEY_"):
			key++
			if v != "GIT_CONFIG_KEY_0=core.fsmonitor" {
				t.Fatalf("unexpected key entry %q", v)
			}
		case strings.HasPrefix(v, "GIT_CONFIG_VALUE_"):
			value++
			if v != "GIT_CONFIG_VALUE_0=" {
				t.Fatalf("an inherited hook path survived: %q", v)
			}
		}
	}
	if count != 1 || key != 1 || value != 1 {
		t.Fatalf("expected exactly one override triple, got %d/%d/%d", count, key, value)
	}
	if spawnEnv(FdToolName) != nil {
		t.Fatal("only git needs an environment; every other tool inherits the session's")
	}
}

func TestBuildGitArgvRefusesFlagShapedRefs(t *testing.T) {
	bad := []string{
		"--output=/tmp/x", "-n1", "--upload-pack=touch x",
		"HEAD;rm -rf /", "main branch", "HEAD:../outside", "$(id)", "..",
		strings.Repeat("a", maxGitRefLen+1),
	}
	for _, ref := range bad {
		a := gitArgs{Verb: gitShow, Ref: ref}
		if _, err := buildGitArgv(a, nil); err == nil {
			t.Fatalf("ref %q should be refused", ref)
		}
		a = gitArgs{Verb: gitDiff, Ref: "HEAD", ToRef: ref}
		if _, err := buildGitArgv(a, nil); err == nil {
			t.Fatalf("to_ref %q should be refused", ref)
		}
	}
	good := []string{"HEAD", "HEAD~3", "main", "v1.2.0", "feature/thing", "a1b2c3d", "main..HEAD", "@{u}", "HEAD^"}
	for _, ref := range good {
		if _, err := buildGitArgv(gitArgs{Verb: gitShow, Ref: ref}, nil); err != nil {
			t.Fatalf("ref %q should be accepted, got %v", ref, err)
		}
	}
}

func TestBuildGitArgvRefusesAVerbOutsideTheSet(t *testing.T) {
	for _, verb := range []string{"push", "checkout", "reset", "clean", "commit", ""} {
		if _, err := buildGitArgv(gitArgs{Verb: verb}, nil); err == nil {
			t.Fatalf("verb %q should be unreachable", verb)
		}
	}
}

func TestBuildGitArgvRefusesFieldsAVerbDoesNotTake(t *testing.T) {
	cases := []struct {
		args gitArgs
		want string
	}{
		{gitArgs{Verb: gitStatus, Ref: "HEAD"}, "ref"},
		{gitArgs{Verb: gitLog, Staged: true}, "staged"},
		{gitArgs{Verb: gitLog, StartLine: 3}, "start_line"},
		{gitArgs{Verb: gitShow, Ref: "HEAD", Limit: 5}, "limit"},
		{gitArgs{Verb: gitDiff, Search: "x"}, "search"},
		{gitArgs{Verb: gitBlame, Stat: true}, "stat"},
	}
	for _, tc := range cases {
		_, err := buildGitArgv(tc.args, []string{"/tmp/x"})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s with %s should be refused by name, got %v", tc.args.Verb, tc.want, err)
		}
	}
}

func TestBuildGitArgvVerbRequirements(t *testing.T) {
	if _, err := buildGitArgv(gitArgs{Verb: gitShow}, nil); err == nil {
		t.Fatal("show without a ref should be refused")
	}
	if _, err := buildGitArgv(gitArgs{Verb: gitDiff, ToRef: "HEAD"}, nil); err == nil {
		t.Fatal("to_ref without a ref should be refused")
	}
	// git takes at most one commit beside --staged; the pair earns a usage
	// dump, so it is refused here rather than spawned.
	if _, err := buildGitArgv(gitArgs{Verb: gitDiff, Ref: "main", ToRef: "HEAD", Staged: true}, nil); err == nil {
		t.Fatal("staged with two refs should be refused")
	}
	if _, err := buildGitArgv(gitArgs{Verb: gitBlame}, nil); err == nil {
		t.Fatal("blame without a path should be refused")
	}
	if _, err := buildGitArgv(gitArgs{Verb: gitBlame}, []string{"/a", "/b"}); err == nil {
		t.Fatal("blame with two paths should be refused")
	}
	if _, err := buildGitArgv(gitArgs{Verb: gitBlame, StartLine: 40, EndLine: 10}, []string{"/a"}); err == nil {
		t.Fatal("an inverted line window should be refused")
	}
	// A limit past the cap is clamped rather than refused: the model asked a
	// reasonable question with an unreasonable number.
	argv, err := buildGitArgv(gitArgs{Verb: gitLog, Limit: MaxGitLogCommits * 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(argv, "--max-count=100") {
		t.Fatalf("limit should clamp to the cap, got %v", argv)
	}
}

func TestGitPathsLandAfterTheDelimiter(t *testing.T) {
	ts := newTestToolset(t, nil)
	dashed := filepath.Join(ts.root, "-weird.go")
	if err := os.WriteFile(dashed, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	argv := gitArgvFor(t, ts, `{"verb":"log","paths":["-weird.go"]}`)
	delim := indexOf(argv, "--")
	at := indexOf(argv, dashed)
	if delim < 0 || at < 0 || at < delim {
		t.Fatalf("a dash-prefixed path must follow the delimiter, got %v", argv)
	}
}

func TestGitPathsContainment(t *testing.T) {
	ts := newTestToolset(t, nil)

	if _, err := ts.resolveGitPaths([]string{".."}); err == nil ||
		!strings.Contains(err.Error(), "outside the workspace") {
		t.Fatalf("expected .. to be refused, got %v", err)
	}
	outside := t.TempDir()
	if _, err := ts.resolveGitPaths([]string{outside}); err == nil {
		t.Fatal("expected an absolute path outside the workspace to be refused")
	}
	if _, err := ts.resolveGitPaths([]string{""}); err == nil {
		t.Fatal("expected an empty path to be refused")
	}

	// A path that no longer exists is the tool's own subject matter: log and
	// show are asked about files deleted commits ago, so containment is
	// decided lexically rather than by resolving something that is gone.
	got, err := ts.resolveGitPaths([]string{"deleted/three/commits/ago.go"})
	if err != nil {
		t.Fatalf("a missing path should still resolve for history, got %v", err)
	}
	if want := filepath.Join(ts.root, "deleted/three/commits/ago.go"); got[0] != want {
		t.Fatalf("resolved %q, want %q", got[0], want)
	}
	if _, err := ts.resolveGitPaths([]string{"gone/../../elsewhere.go"}); err == nil {
		t.Fatal("expected traversal through a missing directory to be refused")
	}
}

// The containment check runs before anything is spawned: the binary here
// does not exist, so a call that reached the spawn would fail saying so.
func TestExecuteGitRefusesAnEscapingPathBeforeTheSpawn(t *testing.T) {
	ts := newTestToolset(t, map[string]string{GitToolName: filepath.Join(t.TempDir(), "no-such-git")})
	outside := t.TempDir()
	_, err := ts.Execute(GitToolName, json.RawMessage(`{"verb":"log","paths":["`+outside+`"]}`))
	if err == nil || !strings.Contains(err.Error(), "outside the workspace") {
		t.Fatalf("expected containment to refuse before the spawn, got %v", err)
	}
}

func TestGitPathsSymlinkEscapeRejected(t *testing.T) {
	ts := newTestToolset(t, nil)
	outside := t.TempDir()
	target := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ts.root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ts.resolveGitPaths([]string{"link.txt"}); err == nil ||
		!strings.Contains(err.Error(), "outside the workspace") {
		t.Fatalf("a symlink out of the workspace should be refused, got %v", err)
	}
}

func TestShapeGitOutputBoundsTheVerbsWithANarrowerQuestion(t *testing.T) {
	// show and diff have no argument that would return less, so their size is
	// the reduction pipeline's job: a bound here would drop the tail of a
	// patch somewhere nothing can retrieve it.
	long := strings.Repeat("line\n", 5000)
	for _, verb := range []string{gitShow, gitDiff} {
		if got := shapeGitOutput(verb, long); got != strings.TrimRight(long, "\n") {
			t.Fatalf("%s should pass its output through whole, got %d bytes of %d",
				verb, len(got), len(long))
		}
	}
	blame := strings.Repeat("a1b2c3d (Someone 2026-01-01 1) x\n", MaxGitBlameLines+10)
	if got := shapeGitOutput(gitBlame, blame); !strings.Contains(got, "start_line") {
		t.Fatalf("a long blame should point at its window, got the tail %q", tail(got))
	}
	status := strings.Repeat(" M file\n", MaxGitStatusLines+10)
	if got := shapeGitOutput(gitStatus, status); !strings.Contains(got, "truncated at 300 lines") {
		t.Fatalf("a long status should be bounded, got the tail %q", tail(got))
	}
	short := "## main\n M internal/structural/git.go"
	if got := shapeGitOutput(gitStatus, short); got != short {
		t.Fatalf("output under the bound should pass through, got %q", got)
	}
	if got := shapeGitOutput(gitLog, ""); got != "No commits matched." {
		t.Fatalf("empty log should read as a result, got %q", got)
	}
	if got := shapeGitOutput(gitDiff, ""); got != "No changes." {
		t.Fatalf("empty diff should read as a result, got %q", got)
	}
	if got := shapeGitOutput(gitShow, ""); got != "(no output)" {
		t.Fatalf("empty show should read as a result, got %q", got)
	}
}

func tail(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

func TestExecuteGitEndToEnd(t *testing.T) {
	if _, ok := lookPath("git"); !ok {
		t.Skip("git is not on PATH")
	}
	root := newGitRepo(t)
	ts := &Toolset{root: root, bins: map[string]string{GitToolName: "git"}, timeout: SpawnTimeout}

	out, err := ts.Execute(GitToolName, json.RawMessage(`{"verb":"log","limit":5}`))
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if lines := strings.Split(out, "\n"); len(lines) != 1 || !strings.Contains(lines[0], "first commit") {
		t.Fatalf("log should be one line per commit, got %q", out)
	}

	out, err = ts.Execute(GitToolName, json.RawMessage(`{"verb":"blame","paths":["main.go"]}`))
	if err != nil {
		t.Fatalf("blame: %v", err)
	}
	if !strings.Contains(out, "package main") {
		t.Fatalf("blame should attribute the file's lines, got %q", out)
	}

	out, err = ts.Execute(GitToolName, json.RawMessage(`{"verb":"status"}`))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.HasPrefix(out, "## ") {
		t.Fatalf("status should lead with the branch line, got %q", out)
	}

	out, err = ts.Execute(GitToolName, json.RawMessage(`{"verb":"show","ref":"HEAD"}`))
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(out, "first commit") || !strings.Contains(out, "+package main") {
		t.Fatalf("show should carry the message and the patch, got %q", out)
	}

	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = ts.Execute(GitToolName, json.RawMessage(`{"verb":"diff","paths":["main.go"]}`))
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	// The unified form is what the rest of the session already speaks, so a
	// patch from here reads like a patch from anywhere else.
	if !strings.Contains(out, "@@ ") || !strings.Contains(out, "+func main() {}") {
		t.Fatalf("diff should be a unified patch, got %q", out)
	}

	if _, err := ts.Execute(GitToolName, json.RawMessage(`{"verb":"push"}`)); err == nil {
		t.Fatal("a verb outside the set must not reach git")
	}
}

// A repository carries its own configuration, and core.fsmonitor names a
// program git runs. This is the test that the tool cannot be turned into one
// by the repository it is pointed at.
func TestExecuteGitDoesNotRunTheRepositorysHookProgram(t *testing.T) {
	if _, ok := lookPath("git"); !ok {
		t.Skip("git is not on PATH")
	}
	root := newGitRepo(t)
	ran := filepath.Join(t.TempDir(), "ran")
	hook := filepath.Join(t.TempDir(), "hook.sh")
	script := "#!/bin/sh\necho ran >> " + ran + "\nexit 1\n"
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", root, "config", "core.fsmonitor", hook)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("setting the hook: %v: %s", err, out)
	}

	ts := &Toolset{root: root, bins: map[string]string{GitToolName: "git"}, timeout: SpawnTimeout}
	// The three verbs git consults the monitor for.
	for _, args := range []string{
		`{"verb":"status"}`,
		`{"verb":"diff"}`,
		`{"verb":"blame","paths":["main.go"]}`,
	} {
		if _, err := ts.Execute(GitToolName, json.RawMessage(args)); err != nil {
			t.Fatalf("%s: %v", args, err)
		}
	}
	if _, err := os.Stat(ran); !os.IsNotExist(err) {
		data, _ := os.ReadFile(ran)
		t.Fatalf("the repository's hook program ran: %q", data)
	}

	// And the same status without the override does run it, so the assertion
	// above is about the defence rather than about git's mood.
	bare := exec.Command("git", "-C", root, "--no-pager", "--no-optional-locks",
		"status", "--porcelain=v1", "--branch", "--")
	if err := bare.Run(); err != nil {
		t.Fatalf("the control run should still succeed: %v", err)
	}
	if _, err := os.Stat(ran); os.IsNotExist(err) {
		t.Fatal("the control run should have run the hook; the fixture proves nothing as it stands")
	}
}

// newGitRepo builds a one-commit repository and returns its resolved root.
func newGitRepo(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	for _, argv := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "main.go"},
		{"commit", "-q", "-m", "first commit"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, argv...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v failed (%v): %s", argv, err, out)
		}
	}
	return root
}
