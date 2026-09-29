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
func radiusModel(t *testing.T, dir string, c Containment) Model {
	t.Helper()
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "do it"},
	}
	m := New(msgs, mockStream).
		WithWorkspace(dir).
		WithRunner(legacyRunner(func(ctx context.Context, cmd string) (string, int) { return "ran", 0 })).
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
	// the keys are live, so the card is read after the handover.
	return ansi.Strip(handover(t, m).View().Content)
}

func TestBlastRadius_CommandCardStatesTouchesUndoAndNetwork(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := radiusModel(t, dir, Containment{
		Status: "contained: bwrap (workspace profile)", Mechanism: "bwrap",
		Profile: "workspace", Network: true,
	})
	view := confirmFor(t, m, "rm notes.md")

	for _, want := range []string{
		// The size is the half that proves the card measured the file
		// rather than merely repeating the path back: a workspace the
		// session was never told would report a file it is about to create.
		"touches   notes.md — 6 B",
		"undo      ",
		"network   open",
		"sandbox   bwrap · workspace",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("command card should contain %q:\n%s", want, view)
		}
	}
}

// A path shhh cannot resolve statically is reported as unknown, never guessed
// and never reported as nothing.
func TestBlastRadius_UnresolvedPathIsSaidNotGuessed(t *testing.T) {
	dir := t.TempDir()
	m := radiusModel(t, dir, Containment{Status: "contained: bwrap (workspace profile)",
		Mechanism: "bwrap", Profile: "workspace", Network: true})
	view := confirmFor(t, m, "npm run build")
	if !strings.Contains(view, "touches   unknown") {
		t.Fatalf("an unresolvable command should say unknown:\n%s", view)
	}
	if strings.Contains(view, "touches   nothing") {
		t.Fatalf("an unresolvable command must never claim it touches nothing:\n%s", view)
	}
}

// A network held to a host list is stated as the count, with the hosts
// beside it where the card has the room.
func TestBlastRadius_AHostListIsStatedAsItsHosts(t *testing.T) {
	dir := t.TempDir()
	m := radiusModel(t, dir, Containment{
		Status: "contained: bwrap (workspace profile)", Mechanism: "bwrap",
		Profile: "workspace", Network: true, Hosts: []string{"registry.npmjs.org", "proxy.golang.org"},
	})
	view := confirmFor(t, m, "npm run build")
	if !strings.Contains(view, "network   2 hosts") {
		t.Fatalf("the card should count the hosts:\n%s", view)
	}
	if strings.Contains(view, "network   open") {
		t.Fatalf("a host list is not an open network:\n%s", view)
	}
}

// A read-only command resolves, and says so.
func TestBlastRadius_ReadOnlyCommandTouchesNothing(t *testing.T) {
	dir := t.TempDir()
	m := radiusModel(t, dir, Containment{Status: "contained: bwrap (workspace-netless profile)",
		Mechanism: "bwrap", Profile: "workspace-netless"})
	// sed without -i is a filter: it resolves, and it writes nothing. (A
	// command on the inspection allowlist would auto-run and never reach a
	// card at all.)
	view := confirmFor(t, m, "sed -n 1p go.mod")
	for _, want := range []string{"touches   nothing", "undo      nothing to undo", "network   closed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("read-only card should contain %q:\n%s", want, view)
		}
	}
	// A quiet card is its values: every sentence here would read the same on
	// the next read-only card, so none is drawn
	// (docs/interface/departures.md#a-card-rows-gloss-is-a-fact-about-the-call-or-nothing).
	for _, standing := range []string{"resolved to reads only", "no workspace file is modified", "profile removes it"} {
		if strings.Contains(view, standing) {
			t.Fatalf("a quiet card should draw no standing gloss, found %q:\n%s", standing, view)
		}
	}
}

// What the card body leaves out, the full view keeps: the sentences are
// moved, not lost.
func TestBlastRadius_TheFullViewKeepsTheStandingGlosses(t *testing.T) {
	dir := t.TempDir()
	m := radiusModel(t, dir, Containment{Status: "contained: bwrap (workspace-netless profile)",
		Mechanism: "bwrap", Profile: "workspace-netless"})
	m.pendingRun = "sed -n 1p go.mod"
	m.pendingBlast = m.commandRadius(m.pendingRun, cardContainment{assistant: true, mechanism: "bwrap"})
	full := strings.Join(m.commandCardView().Lines, "\n")
	for _, want := range []string{
		"touches  nothing — the command resolved to reads only",
		"undo  nothing to undo — no workspace file is modified",
		"network  closed — the workspace-netless profile removes it",
	} {
		if !strings.Contains(full, want) {
			t.Fatalf("the full view should keep %q:\n%s", want, full)
		}
	}
}

// A flagged command leads with the severity word, withholds [a], and says
// why rather than omitting the key silently.
func TestBlastRadius_FlaggedCommandSaysWhyAlwaysIsMissing(t *testing.T) {
	dir := t.TempDir()
	m := radiusModel(t, dir, Containment{Status: "contained: bwrap (workspace profile)",
		Mechanism: "bwrap", Profile: "workspace", Network: true})
	view := confirmFor(t, m, "rm -rf ./build")
	if !strings.Contains(view, "⚠ HIGH") {
		t.Fatalf("a flagged command leads with its severity:\n%s", view)
	}
	if strings.Contains(view, "[a] allow") {
		t.Fatalf("a flagged command must not offer [a]:\n%s", view)
	}
	if !strings.Contains(view, "[a] always — not offered") {
		t.Fatalf("the card should say why [a] is absent:\n%s", view)
	}
	// Esc is the safe answer, and on a flagged card the row says so in words
	// rather than leaving it to the key's own colour.
	if !strings.Contains(view, "[esc] not now — it keeps waiting") {
		t.Fatalf("a high-severity card states the safe answer in words:\n%s", view)
	}
}

// With no containment mechanism the card promotes ⚠ UNCONTAINED, explains the
// missing mechanism and offers the doctor.
func TestBlastRadius_UncontainedPromotesAndExplains(t *testing.T) {
	dir := t.TempDir()
	m := radiusModel(t, dir, Containment{
		Status:  "unconfined — bubblewrap (bwrap) not found on PATH",
		Detail:  "bubblewrap (bwrap) not found on PATH",
		Network: true,
	})
	view := confirmFor(t, m, "make install")
	for _, want := range []string{
		"⚠ UNCONTAINED",
		"sandbox   no sandbox",
		"/sandbox doctor",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("uncontained card should contain %q:\n%s", want, view)
		}
	}
	// The detector's reason is the same on every card of the session: the
	// footnote names the door and the full view keeps the words.
	if strings.Contains(view, "bubblewrap (bwrap) not found on PATH") {
		t.Fatalf("the card body should not repeat the detector's reason:\n%s", view)
	}
	m.pendingRun = "make install"
	m.pendingBlast = m.commandRadius(m.pendingRun, cardContainment{assistant: true})
	if full := strings.Join(m.commandCardView().Lines, "\n"); !strings.Contains(full, "bubblewrap (bwrap) not found on PATH; the command runs as you") {
		t.Fatalf("the full view should keep the detector's reason:\n%s", full)
	}
}

// Reversibility for a command is what git could do about the paths it wrote,
// and it is honest about a directory that was never a repository.
func TestBlastRadius_UndoTracksGit(t *testing.T) {
	dir := t.TempDir()
	m := radiusModel(t, dir, Containment{Status: "contained: bwrap (workspace profile)",
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
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "edit it"},
	}
	m := New(msgs, mockStream).WithWorkspace(dir)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 40})
	m = updated.(Model)
	m.state = stateStreaming

	// The mutating tools read the path the call names, so the call names the
	// file where it actually is rather than relying on where the process
	// happens to be standing.
	args, err := json.Marshal(map[string]string{"path": path, "old_text": "beta", "new_text": "delta"})
	if err != nil {
		t.Fatal(err)
	}
	updated, _ = m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_e", Name: "edit_file", Arguments: string(args)},
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

// A gated tool that described its own radius carries that block.
func TestBlastRadius_GenericToolCarriesItsOwnFields(t *testing.T) {
	dir := t.TempDir()
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "look it up"},
	}
	m := New(msgs, mockStream).
		WithWorkspace(dir).
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
	// Both glosses are the fetch card's fixed sentences, so the rows are
	// their values alone (docs/interface/departures.md).
	for _, want := range []string{
		"domain    pkg.go.dev",
		"sends     the URL and a user-agent",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("generic card should carry the tool's own fields %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "no file contents") {
		t.Fatalf("a fixed sentence is not drawn on the card:\n%s", view)
	}
}

// confirmForStart arms the approval for one process start and returns the
// card's render.
func confirmForStart(t *testing.T, m Model, name, command string) string {
	t.Helper()
	args, err := json.Marshal(map[string]string{"action": "start", "name": name, "command": command})
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_s", Name: "process", Arguments: string(args)},
	}})
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("a process start should have armed a confirm, got state %d", m.state)
	}
	return ansi.Strip(handover(t, m).View().Content)
}

// A start's card names what the supervisor is contained by, not what the
// runner is. The two are wired from one policy, so they normally agree — and
// where they do not, the card that a person answers has to be about the
// process that is going to run.
func TestBlastRadius_ProcessStartRowReadsTheSupervisor(t *testing.T) {
	dir := t.TempDir()
	base := radiusModel(t, dir, Containment{
		Status:    "contained: bwrap (workspace profile)",
		Mechanism: "bwrap",
		Profile:   "workspace",
		Network:   true,
	})
	withSupervisor := func(mechanism string) Model {
		return base.WithProcesses(Processes{
			Manage:    func([]string) string { return "process list" },
			Contained: func() string { return mechanism },
		})
	}

	view := confirmForStart(t, withSupervisor("bwrap"), "web", "npm run dev")
	for _, want := range []string{"start process web", "sandbox   bwrap · workspace", "network   open"} {
		if !strings.Contains(view, want) {
			t.Fatalf("a contained start's card should contain %q:\n%s", want, view)
		}
	}

	// Nothing wraps a start: the card says so even though the runner beside
	// it is contained, because a row reading "bwrap" over a bare process is
	// the defect this seam exists to close.
	view = confirmForStart(t, withSupervisor(""), "web", "npm run dev")
	if strings.Contains(view, "⛨ bwrap") {
		t.Fatalf("an unwrapped start must not claim the runner's mechanism:\n%s", view)
	}
	for _, want := range []string{"⚠ UNCONTAINED", "no sandbox", "/sandbox doctor"} {
		if !strings.Contains(view, want) {
			t.Fatalf("an unwrapped start's card should contain %q:\n%s", want, view)
		}
	}
}
