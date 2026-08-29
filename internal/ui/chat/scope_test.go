package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/scope"
)

// scopedModel is a gated session whose working scope is root, with the
// permission mode already set: the modes are what the scope has to outrank.
func scopedModel(t *testing.T, root string, mode agent.Mode) Model {
	t.Helper()
	sc, problems := scope.New(root)
	if sc == nil {
		t.Fatalf("scope.New(%q): %v", root, problems)
	}
	m := gatedModel(t, func(string, json.RawMessage) (string, error) { return "ok", nil },
		map[string]GatedPreviewFunc{"write_file": writeFilePreview("old\n")})
	return m.WithScope(sc).WithApprovalMode(mode, nil)
}

func TestEditOutsideTheScopeAsksEvenInAcceptEdits(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	m := scopedModel(t, root, agent.ModeAcceptEdits)

	// Inside the scope, accept-edits answers for the edit itself.
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{writeCall("in", filepath.Join(root, "main.go"), "new\n")}})
	if inside := updated.(Model); inside.state == stateConfirmRun {
		t.Fatal("accept-edits should not ask about an edit inside the working scope")
	}

	m = scopedModel(t, root, agent.ModeAcceptEdits)
	updated, _ = m.Update(toolCallsMsg{calls: []provider.ToolCall{writeCall("out", filepath.Join(outside, "config.toml"), "new\n")}})
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("an edit outside the working scope must ask, state = %d", m.state)
	}
	if view := m.View().Content; !strings.Contains(view, "scope") {
		t.Fatalf("the card should carry a scope row, got:\n%s", view)
	}
	// The row's detail is the first thing a narrow card drops (§2), so what
	// the key would grant is read at a width that can carry it.
	wide, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 40})
	if view := wide.(Model).View().Content; !strings.Contains(view, "approving adds it for this session") {
		t.Fatalf("the card should say what answering yes grants, got:\n%s", view)
	}
}

func TestApprovingAnOutOfScopeEditAddsTheDirectory(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	m := scopedModel(t, root, agent.ModeManual)
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{writeCall("out", filepath.Join(outside, "config.toml"), "new\n")}})
	m = updated.(Model)
	if !m.pendingScope.any() {
		t.Fatal("the pending decision should have resolved what it reaches")
	}

	m = handover(t, m)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)

	if !m.scope.Contains(filepath.Join(outside, "other.toml")) {
		t.Fatal("approving should have put the directory in the working scope")
	}
	if !strings.Contains(transcriptText(m), "Added to the working scope") {
		t.Fatalf("the grant must be said in the transcript, got:\n%s", transcriptText(m))
	}
	// And the next edit in the same directory no longer leaves the scope.
	if m.scopeReachFor(&approvalRequest{kind: approvalDiff, path: filepath.Join(outside, "other.toml")}).any() {
		t.Error("a granted directory should stop being out of scope")
	}
}

func TestDecliningAnOutOfScopeEditGrantsNothing(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	m := scopedModel(t, root, agent.ModeManual)
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{writeCall("out", filepath.Join(outside, "config.toml"), "new\n")}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = updated.(Model)
	if len(m.scope.Dirs()) != 0 {
		t.Fatalf("a refused decision must widen nothing, scope holds %v", m.scope.Dirs())
	}
}

func TestMaskedPathIsRefusedRatherThanAsked(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory on this host")
	}
	ssh := filepath.Join(home, ".ssh")
	if _, err := os.Stat(ssh); err != nil {
		t.Skip("no ~/.ssh on this host")
	}
	m := scopedModel(t, t.TempDir(), agent.ModeManual)
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{writeCall("ssh", filepath.Join(ssh, "config"), "new\n")}})
	m = updated.(Model)
	if m.state == stateConfirmRun {
		t.Fatal("a path behind the deny mask must not be offered as a decision")
	}
	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || !strings.Contains(last.Content, "working scope") {
		t.Fatalf("the model should be told why nothing ran, got %+v", last)
	}
}

func TestScopeCommandAddsListsAndDrops(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	m := scopedModel(t, root, agent.ModeManual)

	if out := m.scopeCommand([]string{"/add-dir"}); !strings.Contains(out, root) || !strings.Contains(out, "(none)") {
		t.Fatalf("bare /add-dir should list the scope, got:\n%s", out)
	}
	if out := m.scopeCommand([]string{"/add-dir", outside}); !strings.Contains(out, "Added") {
		t.Fatalf("/add-dir <path> should add it, got:\n%s", out)
	}
	if !m.scope.Contains(filepath.Join(outside, "x")) {
		t.Fatal("the directory should be in scope after /add-dir")
	}
	if out := m.scopeCommand([]string{"/add-dir", outside}); !strings.Contains(out, "already") {
		t.Fatalf("adding it twice should say so, got:\n%s", out)
	}
	if out := m.scopeCommand([]string{"/add-dir", filepath.Join(outside, "nope")}); !strings.Contains(out, "Error") {
		t.Fatalf("a path that is not there should be an error, got:\n%s", out)
	}
	if out := m.scopeCommand([]string{"/add-dir", "drop", outside}); !strings.Contains(out, "Dropped") {
		t.Fatalf("/add-dir drop should take it back, got:\n%s", out)
	}
	if m.scope.Contains(filepath.Join(outside, "x")) {
		t.Fatal("a dropped directory must leave the scope")
	}
}

func TestScopeCommandNamesASensitiveGrant(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory on this host")
	}
	m := scopedModel(t, t.TempDir(), agent.ModeManual)
	out := m.scopeCommand([]string{"/add-dir", home})
	if !strings.Contains(out, "sensitive") {
		t.Fatalf("granting a sensitive directory should say so, got:\n%s", out)
	}
}

func TestGrantsAndHelpNameTheScope(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	m := scopedModel(t, root, agent.ModeManual)
	m.scopeCommand([]string{"/add-dir", outside})

	if out := m.grantStatus(); !strings.Contains(out, "scope") {
		t.Fatalf("/permissions grants should list the working scope, got:\n%s", out)
	}
	if out := m.policyHelp(); !strings.Contains(out, "scope:") || !strings.Contains(out, "/add-dir") {
		t.Fatalf("/help should describe the scope and how to change it, got:\n%s", out)
	}
}

func TestOutOfScopeDecisionsAreNeverBatched(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	m := scopedModel(t, root, agent.ModeManual)
	req := &approvalRequest{kind: approvalDiff, path: filepath.Join(outside, "a.toml")}
	m.pendingScope = m.scopeReachFor(req)
	if _, ok := m.batchCategory(req); ok {
		t.Fatal("[A] must not sweep up a decision that leaves the working scope")
	}
	inScope := &approvalRequest{kind: approvalDiff, path: filepath.Join(root, "a.go")}
	if _, ok := m.batchCategory(inScope); !ok {
		t.Fatal("an in-scope edit is still batchable")
	}
}

// transcriptText is every system entry in the session's transcript.
func transcriptText(m Model) string {
	var b strings.Builder
	for _, e := range m.transcript {
		b.WriteString(e.text + "\n")
	}
	return b.String()
}
