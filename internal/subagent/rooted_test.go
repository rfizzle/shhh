package subagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
)

func rootedPath(t *testing.T, root, name, args string) (string, error) {
	t.Helper()
	out, err := RootArgs(root, name, json.RawMessage(args))
	if err != nil {
		return "", err
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal rooted args: %v", err)
	}
	p, _ := m["path"].(string)
	return p, nil
}

func TestRootArgs_RelativeJoinsRoot(t *testing.T) {
	root := t.TempDir()
	p, err := rootedPath(t, root, "read_file", `{"path":"sub/main.go"}`)
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(root, "sub/main.go") {
		t.Fatalf("relative path not rooted: %s", p)
	}
}

// An archive entry's name is not a path on disk, so rooting it must not clean
// it: `x.zip!/../y` names an entry, and cleaned it would name the file y
// beside the archive in the child's copy.
func TestRootArgs_AnArchiveEntryIsNotCleaned(t *testing.T) {
	root := t.TempDir()
	p, err := rootedPath(t, root, "read_file", `{"path":"dist/x.zip!/../y.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if want := root + string(filepath.Separator) + "dist/x.zip!/../y.txt"; p != want {
		t.Fatalf("got %s, want %s", p, want)
	}
}

// The database reader takes a path like the file reader, so a writer's
// relative path names the database in its own copy.
func TestRootArgs_SqliteReadsTheChildsCopy(t *testing.T) {
	root := t.TempDir()
	p, err := rootedPath(t, root, tools.SqliteName, `{"path":"testdata/app.db","sql":["SELECT 1"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(root, "testdata/app.db") {
		t.Fatalf("relative database path not rooted: %s", p)
	}
}

// query names its files in an array, and every entry means the child's copy
// by a relative path: a plain file, a glob, and an archive entry, which is
// rooted without cleaning for the reason a single path is. An absolute
// entry is left where it points.
func TestRootArgs_QueryPathsAreRootedEachOne(t *testing.T) {
	root := t.TempDir()
	raw := `{"expression":".version","paths":["package.json","conf/*.yml","**/go.mod","/etc/hostname","dist/x.zip!/../y.json"]}`
	out, err := RootArgs(root, tools.QueryName, json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Expression string   `json:"expression"`
		Paths      []string `json:"paths"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal rooted args: %v", err)
	}
	want := []string{
		filepath.Join(root, "package.json"),
		filepath.Join(root, "conf/*.yml"),
		filepath.Join(root, "**/go.mod"),
		"/etc/hostname",
		root + string(filepath.Separator) + "dist/x.zip!/../y.json",
	}
	if len(got.Paths) != len(want) {
		t.Fatalf("paths = %q, want %q", got.Paths, want)
	}
	for i := range want {
		if got.Paths[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, got.Paths[i], want[i])
		}
	}
	if got.Expression != ".version" {
		t.Errorf("the expression should arrive as written, got %q", got.Expression)
	}
}

// A writer queries a file only its own copy holds, once by name and once by
// a glob, through the rooted executor a host wires. Unrooted, both entries
// would be read from the test's working directory and find nothing.
func TestAWriterQueriesAFileOnlyItsCopyHolds(t *testing.T) {
	repo := initTestRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	env := &scriptedEnv{steps: []streamStep{
		{calls: []provider.ToolCall{{ID: "q1", Name: tools.QueryName,
			Arguments: `{"expression":".held","paths":["conf/only.json","conf/*.json"]}`}}},
		{text: "read it"},
	}}
	inner := env.factory()
	var copyRoot string
	factory := func(ctx context.Context, spec Spec) (Env, error) {
		e, err := inner(ctx, spec)
		if err != nil || spec.Root == repo {
			// The environment built before the spawn is admitted stands in
			// the parent's checkout; only the copy gets the file.
			return e, err
		}
		copyRoot = spec.Root
		if err := os.MkdirAll(filepath.Join(spec.Root, "conf"), 0o755); err != nil {
			return e, err
		}
		if err := os.WriteFile(filepath.Join(spec.Root, "conf", "only.json"), []byte(`{"held":"by the copy"}`), 0o644); err != nil {
			return e, err
		}
		rooted := RootedExecutor(spec.Root, tools.Execute)
		e.Executor = func(name string, args json.RawMessage) (string, error) {
			// Asked at the call: once the child finishes, its patch may
			// land and put the file in the parent's tree as well.
			if _, err := os.Stat(filepath.Join(repo, "conf", "only.json")); err == nil {
				t.Error("the file must be in the child's copy only when it is queried")
			}
			return rooted(name, args)
		}
		return e, nil
	}
	sup := New(ctx, Options{Root: repo, NewEnv: factory})
	t.Cleanup(sup.Close)
	t.Cleanup(cancel)
	go func() {
		for {
			select {
			case ev := <-sup.Events():
				if ev.Kind == EventAsk {
					ev.Ask.Respond(false)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"read the copy's config"}`)
	execTool(t, sup, ReportToolName, `{"name":"writer-1"}`)
	if copyRoot == "" || copyRoot == repo {
		t.Fatalf("a writer should stand in a copy of its own, got %q", copyRoot)
	}
	got := env.lastToolResult()
	if strings.Count(got, "by the copy") != 1 {
		t.Fatalf("query should have read the copy's file, named twice, once; got:\n%s", got)
	}
}

func TestRootArgs_AbsoluteReadAllowed(t *testing.T) {
	p, err := rootedPath(t, t.TempDir(), "read_file", `{"path":"/etc/hostname"}`)
	if err != nil {
		t.Fatal(err)
	}
	if p != "/etc/hostname" {
		t.Fatalf("absolute read path rewritten: %s", p)
	}
}

func TestRootArgs_MutatingEscapeRefused(t *testing.T) {
	root := t.TempDir()
	if _, err := rootedPath(t, root, "write_file", `{"path":"/tmp/other/x.go","content":"x"}`); err == nil {
		t.Fatal("absolute mutating path outside the root must be refused")
	}
	if _, err := rootedPath(t, root, "edit_file", `{"path":"../escape.go","old_text":"a","new_text":"b"}`); err == nil {
		t.Fatal("relative mutating path escaping the root must be refused")
	}
	p, err := rootedPath(t, root, "write_file", `{"path":"ok.go","content":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(root, "ok.go") {
		t.Fatalf("in-root mutating path not rooted: %s", p)
	}
}

func TestRootArgs_OptionalPathDefaultsToRoot(t *testing.T) {
	root := t.TempDir()
	for _, tool := range []string{"search", "glob"} {
		p, err := rootedPath(t, root, tool, `{"pattern":"foo"}`)
		if err != nil {
			t.Fatal(err)
		}
		if p != root {
			t.Fatalf("%s: empty path should default to root, got %q", tool, p)
		}
	}
}

func TestRootArgs_UnknownToolUntouched(t *testing.T) {
	raw := `{"url":"https://example.com"}`
	out, err := RootArgs(t.TempDir(), "web_fetch", json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != raw {
		t.Fatalf("non-path tool args rewritten: %s", out)
	}
}

func TestDisplayPath(t *testing.T) {
	root := t.TempDir()
	if got := displayPath(root, filepath.Join(root, "a/b.go")); got != "a/b.go" {
		t.Fatalf("displayPath = %q", got)
	}
	if got := displayPath(root, "/somewhere/else.go"); !strings.HasPrefix(got, "/somewhere") {
		t.Fatalf("out-of-root path should stay absolute: %q", got)
	}
}

// Rooting a child's call rewrites the path and nothing else. It works
// through a generic map, so a call carrying several edits would lose them to
// any field the rewrite forgot to carry — and the child's approval card is
// built from what comes out of here.
func TestRootArgs_CarriesTheEditsArray(t *testing.T) {
	root := t.TempDir()
	raw := `{"path":"loop.go","edits":[` +
		`{"old_text":"alpha","new_text":"one"},` +
		`{"old_text":"beta","new_text":"two","replace_all":true}]}`
	out, err := RootArgs(root, "edit_file", json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Path  string `json:"path"`
		Edits []struct {
			OldText    string `json:"old_text"`
			NewText    string `json:"new_text"`
			ReplaceAll bool   `json:"replace_all"`
		} `json:"edits"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal rooted args: %v", err)
	}
	if got.Path != filepath.Join(root, "loop.go") {
		t.Errorf("path = %q, want it under the worktree", got.Path)
	}
	if len(got.Edits) != 2 {
		t.Fatalf("both edits should survive rooting, got %d", len(got.Edits))
	}
	if got.Edits[0].OldText != "alpha" || got.Edits[1].NewText != "two" || !got.Edits[1].ReplaceAll {
		t.Errorf("the edits should arrive as written, got %+v", got.Edits)
	}
}

// The language server's questions take a path and mean the child's workspace
// by it. A child shares its parent's server, which resolves a relative path
// against the parent's checkout — so a writer asking about `internal/foo.go`
// would be answered about the copy it is not editing.
func TestRootArgs_TheLanguageServerAsksAboutTheChildsCopy(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{
		lsp.DefinitionToolName, lsp.ReferencesToolName,
		lsp.DocumentSymbolToolName, lsp.HoverToolName, lsp.DiagnosticsToolName,
	} {
		p, err := rootedPath(t, root, name, `{"path":"internal/foo.go","line":3,"symbol":"x"}`)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if p != filepath.Join(root, "internal/foo.go") {
			t.Errorf("%s asked about %s, not the child's own copy", name, p)
		}
	}
	// diagnostics with no path means every file the server has checked, not
	// the workspace, so an absent one is left absent.
	out, err := RootArgs(root, lsp.DiagnosticsToolName, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), root) {
		t.Errorf("a workspace-wide question was narrowed to a path: %s", out)
	}
	// workspace_symbol has no path at all and is left as it was.
	if out, err := RootArgs(root, lsp.WorkspaceSymbolToolName, json.RawMessage(`{"query":"Spawn"}`)); err != nil ||
		string(out) != `{"query":"Spawn"}` {
		t.Errorf("a question with no path was rewritten: %s %v", out, err)
	}
}
