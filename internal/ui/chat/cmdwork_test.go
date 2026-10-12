package chat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
)

// cmdWorkRepo is a repository with a committed file, an ignore rule, and a
// file somebody else had already changed before any command ran.
func cmdWorkRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := fixtureDir(t, map[string]string{
		"loop.go": "package agent\n", "theirs.go": "package agent\n", ".gitignore": "*.log\n",
	})
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"add", "."},
		{"commit", "-q", "-m", "seed"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "theirs.go"), []byte("package agent\n\n// mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// runModelCommand puts one execute_command through its card and lands it,
// with does standing in for what the command did to the tree.
func runModelCommand(t *testing.T, root, line string, does func()) Model {
	t.Helper()
	m := gatedModelWith(t, nil, nil, Wiring{
		Workspace: root,
		Tracker:   changeset.NewTracker(root),
		Runner: func(context.Context, string) tools.ExecResult {
			does()
			return tools.ExecResult{}
		},
	})
	m.state = stateInput
	m = sendText(t, m, "regenerate the protobufs")
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_c", Name: "execute_command", Arguments: `{"command":` + jsonString(line) + `}`},
	}})
	m = handover(t, updated.(Model))
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	updated, _ = m.Update(drainCmdDone(t, m, cmd))
	return updated.(Model)
}

func jsonString(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// What the model's command made dirty is recorded as the command's work —
// a file it created and a tracked file it changed — and the stager's list
// holds them; a file dirty before it ran, and one git ignores, are not.
func TestCommandBracket_RecordsWhatTheCommandMadeDirty(t *testing.T) {
	root := cmdWorkRepo(t)
	m := runModelCommand(t, root, "make gen", func() {
		write(t, filepath.Join(root, "gen.pb.go"), "package agent\n\nvar Gen = 1\n")
		write(t, filepath.Join(root, "loop.go"), "package agent\n\nconst limit = 3\n")
		write(t, filepath.Join(root, "theirs.go"), "package agent\n\n// mine, changed again\n")
		write(t, filepath.Join(root, "build.log"), "noise\n")
	})
	turn, ok := m.changes.Turn(m.turnCount)
	if !ok {
		t.Fatal("the command recorded nothing")
	}
	var got []string
	for _, r := range turn.Records {
		if r.Origin != changeset.ByCommand || r.Evidence != "make gen" || !r.BeforeUnknown() {
			t.Fatalf("record %+v", r)
		}
		got = append(got, filepath.Base(r.Path))
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"gen.pb.go", "loop.go"}) {
		t.Fatalf("recorded %v, want the created file and the changed tracked one", got)
	}
	paths := m.changes.Paths()
	for _, p := range paths {
		if base := filepath.Base(p); base == "theirs.go" || base == "build.log" {
			t.Fatalf("%s is not the command's work: %v", base, paths)
		}
	}
	// The command's row says what it recorded.
	row := m.transcript[len(m.transcript)-1]
	if row.kind != entryCommand || row.recorded != "2 files recorded" {
		t.Fatalf("command row recorded = %q", row.recorded)
	}
	// And the turn's close counts them in their own words.
	m = finishTurn(t, m)
	close := ansi.Strip(lastClose(t, m).View(110))
	if !strings.Contains(close, "2 files changed") || !strings.Contains(close, "2 by commands · not restorable") {
		t.Fatalf("close row:\n%s", close)
	}
}

// A command that changed nothing new records nothing, and its row says
// nothing about it.
func TestCommandBracket_ACommandThatChangesNothingRecordsNothing(t *testing.T) {
	root := cmdWorkRepo(t)
	m := runModelCommand(t, root, "./scripts/check.sh", func() {})
	if _, ok := m.changes.Turn(m.turnCount); ok {
		t.Fatal("a command that changed nothing recorded a turn")
	}
	if row := m.transcript[len(m.transcript)-1]; row.recorded != "" {
		t.Fatalf("row says %q", row.recorded)
	}
}

// A path the project declares generated carries that word, on the record and
// on the row.
func TestCommandBracket_AGeneratedPathSaysSo(t *testing.T) {
	root := cmdWorkRepo(t)
	if err := os.MkdirAll(filepath.Join(root, ".shhh"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, ".shhh", "quality.json"),
		`{"suites":{"d":{"checks":[{"name":"x","exe":"true"}]}},"generated":[{"name":"protobuf","paths":["*.pb.go"],"exe":"make","args":["gen"]}]}`)
	m := runModelCommand(t, root, "make gen", func() {
		write(t, filepath.Join(root, "gen.pb.go"), "package agent\n")
	})
	turn, _ := m.changes.Turn(m.turnCount)
	var gen changeset.Record
	for _, r := range turn.Records {
		if filepath.Base(r.Path) == "gen.pb.go" {
			gen = r
		}
	}
	if !gen.Generated {
		t.Fatalf("the generated block's path is not marked: %+v", turn.Records)
	}
	if row := m.transcript[len(m.transcript)-1]; !strings.Contains(row.recorded, "1 generated") {
		t.Fatalf("row says %q", row.recorded)
	}
}
