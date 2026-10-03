package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/tools"
)

// irreplaceableHome points the session at a scratch machine: a home
// directory and a workspace under the test's own directory, so `~` names
// nothing real.
func irreplaceableHome(t *testing.T, m Model) Model {
	t.Helper()
	base := t.TempDir()
	home, ws := filepath.Join(base, "home"), filepath.Join(base, "ws")
	for _, d := range []string{home, ws} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	sc, errs := scope.New(ws)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	return m.WithScope(sc).WithWorkspace(ws)
}

// In auto mode, with every command granted, a delete of the home directory
// is still refused by rule: before the classifier is paid to think, with no
// card, and with a row that names the rule and the target.
func TestIrreplaceableTarget_RefusedBeforeTheClassifierAndTheCard(t *testing.T) {
	var ran []string
	p := &verdictProvider{decision: "allow", reason: "looks fine"}
	m := irreplaceableHome(t, classifierModel(t, &ran, p))
	m.policy.allCommands = true

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: tools.ExecCommandName, Arguments: `{"command":"sudo rm -rf \"$HOME\""}`},
	}})
	m = updated.(Model)

	if p.calls != 0 {
		t.Fatal("the rule answers before the classifier is paid to think")
	}
	if len(ran) != 0 {
		t.Fatalf("a refused command ran: %v", ran)
	}
	if m.approval.request != nil {
		t.Fatal("a rule's refusal drew a card")
	}
	row := m.transcript[len(m.transcript)-1]
	if !agent.IsIrreplaceable(row.denyRule) || !strings.Contains(row.denyRule, "your home directory") {
		t.Fatalf("the row names the rule and the target, got %q", row.denyRule)
	}
	if !strings.Contains(m.lastDenial, "!") {
		t.Fatalf("/permissions why tells the reader their own way through, got %q", m.lastDenial)
	}
}

// The reader's own command is theirs: typed after `!`, the same delete goes
// to the confirm card a person answers, and no rule stands in front of it.
func TestIrreplaceableTarget_TheReadersOwnCommandIsNotRefused(t *testing.T) {
	m := irreplaceableHome(t, runCapableModel("no blocks here"))
	m = sendText(t, m, "!rm -rf ~")
	if m.state != stateConfirmRun || m.pendingRun != "rm -rf ~" {
		t.Fatalf("a bang delete should reach the reader's confirm, got state=%d pending=%q", m.state, m.pendingRun)
	}
}
