package chat

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/provider"
)

// radiusModel is a session that runs the assistant's commands and shows their
// approval cards, wide enough that the block's details are not dropped.
func radiusModel(t *testing.T, c Containment) Model {
	t.Helper()
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "do it"},
	}
	m := New(msgs, mockStream).
		WithRunner(func(ctx context.Context, cmd string) (string, int) { return "ran", 0 }).
		WithContainment(c)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 40})
	m = updated.(Model)
	m.state = stateStreaming
	return m
}

// confirmFor arms the approval for one command and returns the card's render.
func confirmFor(t *testing.T, m Model, command string) string {
	t.Helper()
	args, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_r", Name: "execute_command", Arguments: string(args)},
	}})
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("%q should have armed a confirm, got state %d", command, m.state)
	}
	// The consequences a card prints beside its keys are only printed once
	// the keys are live (S-117), so the card is read after ctrl+g.
	return ansi.Strip(handover(t, m).View().Content)
}

// chdir moves into a scratch directory for the test, so path resolution has
// something real to stat.
func chdir(t *testing.T) string {
	t.Helper()
	was, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(was) })
	return dir
}

func TestBlastRadius_CommandCardStatesTouchesUndoAndNetwork(t *testing.T) {
	dir := chdir(t)
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := radiusModel(t, Containment{
		Status: "contained: bwrap (workspace profile)", Mechanism: "bwrap",
		Profile: "workspace", Network: true,
	})
	view := confirmFor(t, m, "rm notes.md")

	for _, want := range []string{
		"touches   notes.md",
		"undo      ",
		"network   open",
		"⛨ bwrap · workspace",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("command card should contain %q:\n%s", want, view)
		}
	}
}

// A path shhh cannot resolve statically is reported as unknown, never guessed
// and never reported as nothing.
func TestBlastRadius_UnresolvedPathIsSaidNotGuessed(t *testing.T) {
	chdir(t)
	m := radiusModel(t, Containment{Status: "contained: bwrap (workspace profile)",
		Mechanism: "bwrap", Profile: "workspace", Network: true})
	view := confirmFor(t, m, "npm run build")
	if !strings.Contains(view, "touches   unknown") {
		t.Fatalf("an unresolvable command should say unknown:\n%s", view)
	}
	if strings.Contains(view, "touches   nothing") {
		t.Fatalf("an unresolvable command must never claim it touches nothing:\n%s", view)
	}
}

// A read-only command resolves, and says so.
func TestBlastRadius_ReadOnlyCommandTouchesNothing(t *testing.T) {
	chdir(t)
	m := radiusModel(t, Containment{Status: "contained: bwrap (workspace-netless profile)",
		Mechanism: "bwrap", Profile: "workspace-netless"})
	// sed without -i is a filter: it resolves, and it writes nothing. (A
	// command on the inspection allowlist would auto-run and never reach a
	// card at all.)
	view := confirmFor(t, m, "sed -n 1p go.mod")
	for _, want := range []string{"touches   nothing", "undo      n/a", "network   closed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("read-only card should contain %q:\n%s", want, view)
		}
	}
}

// A flagged command leads with the severity word, withholds [a], and says
// why rather than omitting the key silently.
func TestBlastRadius_FlaggedCommandSaysWhyAlwaysIsMissing(t *testing.T) {
	chdir(t)
	m := radiusModel(t, Containment{Status: "contained: bwrap (workspace profile)",
		Mechanism: "bwrap", Profile: "workspace", Network: true})
	view := confirmFor(t, m, "rm -rf ./build")
	if !strings.Contains(view, "⚠ HIGH") {
		t.Fatalf("a flagged command leads with its severity:\n%s", view)
	}
	if strings.Contains(view, "[y/n/a]") {
		t.Fatalf("a flagged command must not offer [a]:\n%s", view)
	}
	if !strings.Contains(view, "[a] always — not offered") {
		t.Fatalf("the card should say why [a] is absent:\n%s", view)
	}
	// [n], not esc: esc on a gated card hands the keyboard back and leaves
	// the decision waiting (S-117).
	if !strings.Contains(view, "[n] deny — the safe answer") {
		t.Fatalf("a high-severity card states the safe default in words:\n%s", view)
	}
}

// With no containment mechanism the card promotes ⚠ UNCONTAINED, explains the
// missing mechanism and offers the doctor.
func TestBlastRadius_UncontainedPromotesAndExplains(t *testing.T) {
	chdir(t)
	m := radiusModel(t, Containment{
		Status:  "unconfined — bubblewrap (bwrap) not found on PATH",
		Detail:  "bubblewrap (bwrap) not found on PATH",
		Network: true,
	})
	view := confirmFor(t, m, "make install")
	for _, want := range []string{
		"⚠ UNCONTAINED",
		"⛨         no sandbox",
		"bubblewrap (bwrap) not found on PATH",
		"/sandbox doctor",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("uncontained card should contain %q:\n%s", want, view)
		}
	}
}

// Reversibility for a command is what git could do about the paths it wrote,
// and it is honest about a directory that was never a repository.
func TestBlastRadius_UndoTracksGit(t *testing.T) {
	dir := chdir(t)
	m := radiusModel(t, Containment{Status: "contained: bwrap (workspace profile)",
		Mechanism: "bwrap", Profile: "workspace", Network: true})

	if err := os.WriteFile(filepath.Join(dir, "kept.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.tracker = changeset.NewTracker(dir)
	if view := confirmFor(t, m, "rm kept.txt"); !strings.Contains(view, "undo      none") {
		t.Fatalf("outside a repository undo is none, with the reason:\n%s", view)
	}

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	for _, args := range [][]string{
		{"init", "-q"}, {"add", "kept.txt"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git setup failed: %v (%s)", err, out)
		}
	}
	m.tracker = changeset.NewTracker(dir)
	view := confirmFor(t, m, "rm kept.txt")
	if !strings.Contains(view, "undo      git") {
		t.Fatalf("a tracked path is restorable by git, and the card says so:\n%s", view)
	}
}

// An edit's radius is the diff itself; the one fact it adds rides the stats
// line so the diff loses no rows to it.
func TestBlastRadius_EditStatesReversibilityOnTheStatsLine(t *testing.T) {
	dir := chdir(t)
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "edit it"},
	}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 40})
	m = updated.(Model)
	m.state = stateStreaming

	updated, _ = m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_e", Name: "edit_file",
			Arguments: `{"path":"main.go","old_text":"beta","new_text":"delta"}`},
	}})
	m = updated.(Model)
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "· undo yes — recorded") {
		t.Fatalf("the edit card should state reversibility on its stats line:\n%s", view)
	}
	if strings.Contains(view, "touches   ") {
		t.Fatalf("an edit card needs no touches row — the diff is the radius:\n%s", view)
	}
}

// A gated tool that described its own radius carries that block (S-101).
func TestBlastRadius_GenericToolCarriesItsOwnFields(t *testing.T) {
	chdir(t)
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "look it up"},
	}
	m := New(msgs, mockStream).
		WithToolExecutor(func(name string, args json.RawMessage) (string, error) { return "ok", nil }).
		WithGatedTools(map[string]GatedPreviewFunc{
			"web_fetch": func(json.RawMessage) (GatedPreview, error) {
				return GatedPreview{Summary: "GET https://pkg.go.dev/context", Fields: []GatedField{
					{Label: "domain", Value: "pkg.go.dev", Detail: "the request leaves this machine", Open: true},
					{Label: "sends", Value: "the URL and a user-agent", Detail: "no file contents, no credentials"},
				}}, nil
			},
		})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 40})
	m = updated.(Model)
	m.state = stateStreaming

	updated, _ = m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_f", Name: "web_fetch", Arguments: `{"url":"https://pkg.go.dev/context"}`},
	}})
	m = updated.(Model)
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"domain    pkg.go.dev — the request leaves this machine",
		"sends     the URL and a user-agent — no file contents, no credentials",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("generic card should carry the tool's own fields %q:\n%s", want, view)
		}
	}
}
