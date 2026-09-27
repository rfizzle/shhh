package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/structural"
)

// headedBy is a session opened on ws, its header reading the branch the
// start survey found.
func headedBy(t *testing.T, m Model, ws string) Model {
	t.Helper()
	m = m.WithStartScreen(StartInfo{Project: project.Info{Dir: ws, Display: "~/ws", Repo: true, Branch: "master"}})
	if got := stripANSI(m.headerRow(200)); !strings.Contains(got, "~/ws · master") {
		t.Fatalf("the header should open on the surveyed branch, got %q", got)
	}
	return m
}

// A switch the session made with the git tool re-reads the header's branch,
// so the row does not go on naming the branch the session started on.
func TestHeader_TheBranchFollowsAGitSwitch(t *testing.T) {
	ws := treeRepo(t)
	m := gatedModel(t,
		func(string, json.RawMessage) (string, error) {
			gitIn(t, ws, "switch", "-q", "-c", "feature/x")
			return "switched to feature/x", nil
		},
		map[string]GatedPreviewFunc{
			structural.GitWriteToolName: func(json.RawMessage) (GatedPreview, error) {
				return GatedPreview{Title: "switch to feature/x", Write: true}, nil
			},
		})
	m.policy.mode = agent.ModeAcceptEdits
	m = headedBy(t, m, ws)

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_sw", Name: structural.GitWriteToolName, Arguments: `{"verb":"switch","branch":"feature/x","create":true}`},
	}})
	m = updated.(Model)
	updated, _ = m.Update(driveApprovedTool(t, cmd))
	m = updated.(Model)

	if got := stripANSI(m.headerRow(200)); !strings.Contains(got, "~/ws · feature/x") {
		t.Fatalf("the header should name the branch the switch landed on, got %q", got)
	}
}

// A branch moved by anything else — a command, another terminal — is seen by
// the tree reading, and the header follows it there.
func TestHeader_TheBranchFollowsTheTreeReading(t *testing.T) {
	ws := treeRepo(t)
	m := headedBy(t, gatedModel(t, nil, nil).WithTreeCheck(&agent.TreeCheck{Dir: ws}), ws)
	m.injectTreeNotice(true) // the baseline

	gitIn(t, ws, "switch", "-q", "-c", "feature/y")
	m.injectTreeNotice(false)

	if got := stripANSI(m.headerRow(200)); !strings.Contains(got, "~/ws · feature/y") {
		t.Fatalf("the header should name the branch the tree reading saw, got %q", got)
	}
}
