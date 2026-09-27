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
	"github.com/rfizzle/shhh/internal/sandbox"
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
	// The test process's TMPDIR is shhh's session scratch, which production
	// correctly classifies as sensitive. These fixtures instead need ordinary
	// directories so their differing scope membership is the only decision.
	base, err := os.MkdirTemp(".", ".scope-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	root, outside := filepath.Join(base, "root"), filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
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
	// The row's detail is the first thing a narrow card drops, so what
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
	if !strings.Contains(transcriptText(m), "added to the working scope") {
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
	if out := m.scopeCommand([]string{"/add-dir", filepath.Join(outside, "nope")}); !strings.HasPrefix(out, "✗ add-dir  ") {
		t.Fatalf("a path that is not there should be an error, got:\n%s", out)
	}
	if out := m.scopeCommand([]string{"/add-dir", "drop", outside}); !strings.Contains(out, "dropped") {
		t.Fatalf("/add-dir drop should take it back, got:\n%s", out)
	}
	if m.scope.Contains(filepath.Join(outside, "x")) {
		t.Fatal("a dropped directory must leave the scope")
	}
}

// Granting the session's own checkout is recorded for writers, and the
// notice says so rather than claiming the session's scope grew.
// See docs/capabilities/subagents.md#a-child-inherits-its-scope-not-more.
func TestScopeCommandGrantsTheCheckoutToWriters(t *testing.T) {
	root := t.TempDir()
	m := scopedModel(t, root, agent.ModeManual)
	out := m.scopeCommand([]string{"/add-dir", root})
	if !strings.Contains(out, "Granted") || !strings.Contains(out, "to writers") || strings.Contains(out, "Added") {
		t.Fatalf("/add-dir <root> should say it grants the checkout to writers, got:\n%s", out)
	}
	if len(m.scope.Dirs()) != 1 {
		t.Fatalf("the grant was not recorded: %v", m.scope.Dirs())
	}
	if out := m.scopeCommand([]string{"/add-dir"}); !strings.Contains(out, "for writers") {
		t.Fatalf("bare /add-dir should mark the checkout's grant as a writer's, got:\n%s", out)
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

func TestScopeCommandAddsShhhDirectories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	m := scopedModel(t, t.TempDir(), agent.ModeManual)

	seen := make(map[string]bool)
	for _, dir := range sandbox.ShhhPaths() {
		if seen[dir] {
			continue
		}
		seen[dir] = true
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		out := m.scopeCommand([]string{"/add-dir", dir})
		if !strings.Contains(out, "Added") || !strings.Contains(out, "sensitive") {
			t.Fatalf("/add-dir should explicitly grant shhh directory %q, got:\n%s", dir, out)
		}
		if !m.scope.Contains(filepath.Join(dir, "child")) {
			t.Fatalf("%q should be in the working scope after /add-dir", dir)
		}
	}
}

func TestGrantsAndHelpNameTheScope(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	m := scopedModel(t, root, agent.ModeManual)
	m.scopeCommand([]string{"/add-dir", outside})

	if out := m.grantStatus(); !strings.Contains(out, "scope") {
		t.Fatalf("/permissions grants should list the working scope, got:\n%s", out)
	}
	if out := m.policyHelp(); !strings.Contains(out, "  scope      ") || !strings.Contains(out, "/add-dir") {
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

// /permissions describes the session's containment: a grant of the checkout
// is on the writers' line and out of the session's scope, so the summary
// does not count it as a directory the session added.
// See docs/capabilities/subagents.md#a-child-inherits-its-scope-not-more.
func TestPermissionsNameACheckoutGrantForWriters(t *testing.T) {
	root := t.TempDir()
	m := scopedModel(t, root, agent.ModeManual)
	m.scopeCommand([]string{"/add-dir", root})

	grants := m.grantStatus()
	if strings.Contains(grants, "  scope      ") {
		t.Fatalf("/permissions grants listed the checkout as the session's own scope:\n%s", grants)
	}
	if !strings.Contains(grants, "  writers    ") || !strings.Contains(grants, "for writers") {
		t.Fatalf("/permissions grants did not name the checkout for writers:\n%s", grants)
	}
	help := m.policyHelp()
	if strings.Contains(help, "added directory") {
		t.Fatalf("/permissions counted the checkout as an added directory:\n%s", help)
	}
	if !strings.Contains(help, "granted for writers") {
		t.Fatalf("/permissions did not name the checkout's grant for writers:\n%s", help)
	}
	if got := scopeDropArgs(&m); len(got) != 1 {
		t.Fatalf("/add-dir drop should still offer the checkout's grant, got %v", got)
	}
}

// machineMessagesNaming is every message the session wrote for the model that
// names dir.
func machineMessagesNaming(m Model, dir string) []provider.Message {
	var out []provider.Message
	for _, msg := range m.Messages() {
		if msg.Machine && strings.Contains(msg.Content, dir) {
			out = append(out, msg)
		}
	}
	return out
}

// A grant typed mid-session is said to the model once, because the system
// prompt that named the scope was written before it; the next session's
// prompt is built with the grant, so the new conversation carries no
// announcement of its own.
// See docs/capabilities/containment.md#scope-is-the-set-of-directories-the-work-may-reach.
func TestAddDirMidSessionIsAnnouncedToTheModelOnce(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	m := scopedModel(t, root, agent.ModeManual).
		WithNewSession(func() SessionStart { return SessionStart{Prompt: "sys"} })
	m.state = stateInput

	m.scopeCommand([]string{"/add-dir", outside})
	dir := m.scope.Dirs()[0]
	said := machineMessagesNaming(m, dir)
	if len(said) != 1 || said[0].Role != provider.RoleUser || !strings.Contains(said[0].Content, "/add-dir") {
		t.Fatalf("/add-dir should put one announcement naming %s into the conversation, got %+v", dir, said)
	}

	m.startNewSession()
	if got := machineMessagesNaming(m, dir); len(got) != 0 {
		t.Fatalf("a new session is told the grant by its prompt, not again by a message, got %+v", got)
	}

	m.scopeCommand([]string{"/add-dir", "drop", dir})
	if got := machineMessagesNaming(m, dir); len(got) != 1 || !strings.Contains(got[0].Content, "dropped") {
		t.Fatalf("/add-dir drop should be said to the model, got %+v", got)
	}
}

// While the agent works, the announcement waits for the round boundary as
// machine steering, the way a /secret change does.
func TestAddDirWhileWorkingIsQueuedAsMachineSteering(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	m := scopedModel(t, root, agent.ModeManual)
	m.state = stateStreaming

	m.scopeCommand([]string{"/add-dir", outside})
	if len(m.steering) != 1 || !m.steering[0].machine || !strings.Contains(m.steering[0].text, m.scope.Dirs()[0]) {
		t.Fatalf("the grant should be queued as one machine steering item, got %+v", m.steering)
	}
}

// The routes that do not move what the prompt describes say nothing: a card
// grant answered the model's own call, and a grant of the checkout is for
// writers.
func TestScopeGrantsTheModelAlreadyKnowsAreNotAnnounced(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	m := scopedModel(t, root, agent.ModeManual)
	m.scopeCommand([]string{"/add-dir", root})
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{writeCall("out", filepath.Join(outside, "config.toml"), "new\n")}})
	m = handover(t, updated.(Model))
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	if len(m.scope.Dirs()) != 2 {
		t.Fatalf("both grants should have been recorded, got %v", m.scope.Dirs())
	}
	for _, msg := range m.Messages() {
		if msg.Machine && strings.Contains(msg.Content, "working scope") {
			t.Fatalf("neither grant should be announced, got %+v", msg)
		}
	}
	for _, s := range m.steering {
		if strings.Contains(s.text, "working scope") {
			t.Fatalf("neither grant should be queued for the model, got %+v", s)
		}
	}
}
