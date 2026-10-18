package chat

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/llm"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/structural"
)

// claimModel is a session in accept-edits whose git write carries one claim,
// with an executor that records whether the call ran.
func claimModel(t *testing.T, ran *bool) (Model, string) {
	t.Helper()
	root := t.TempDir()
	gen := filepath.Join(root, "gen.pb.go")
	claim := structural.Claim{Path: "gen.pb.go", Abs: gen, Why: "written by make gen this turn", Dirty: true, Generated: true}
	m := gatedModelWith(t, func(_ context.Context, name string, _ json.RawMessage) (string, error) {
		*ran = true
		return "staged 1 file · 1 generated", nil
	}, map[string]GatedPreviewFunc{
		structural.GitWriteToolName: func(json.RawMessage) (GatedPreview, error) {
			return GatedPreview{Title: "stage 1 file", Action: "add", Summary: "gen.pb.go", DenyLine: "git add",
				Fields: []GatedField{{Label: "claims", Value: claim.Facts()}, {Label: "why", Value: claim.Reason()}},
				Claims: []structural.Claim{claim}}, nil
		},
	}, Wiring{Workspace: root})
	m.policy.mode = agent.ModeAcceptEdits
	updated, _ := m.Update(toolCallsMsg{calls: []llm.ToolCall{{ID: "call_claim", Name: structural.GitWriteToolName,
		Arguments: `{"verb":"add","claim":[{"path":"gen.pb.go","why":"written by make gen this turn"}]}`}}})
	return updated.(Model), gen
}

// A claim is a question the mode does not answer: accept-edits would apply a
// plain add, and puts a claimed one on the card, naming the claim. A yes
// there records the path as claimed with the model's reason, which is what
// the stager admits; the plain add beside it was never asked.
func TestClaim_IsPutToTheCardAndAYesRecordsIt(t *testing.T) {
	var ran bool
	m, gen := claimModel(t, &ran)
	if m.approval.request == nil || ran {
		t.Fatal("accept-edits answered a claim without a card")
	}
	card := strings.Join(m.confirmLines(), "\n")
	if !strings.Contains(card, "claims    gen.pb.go · generated · dirty") ||
		!strings.Contains(card, `why       "written by make gen this turn"`) {
		t.Fatalf("the card does not name the claim:\n%s", card)
	}
	m = handover(t, m)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(approvedToolDoneMsg); ok {
			updated, _ = m.Update(msg)
			m = updated.(Model)
		}
	}
	if !ran {
		t.Fatal("the answered claim never ran")
	}
	unknown := m.changes.Unknown()
	if len(unknown) != 1 || unknown[0].Path != gen || unknown[0].Origin != changeset.ByClaim ||
		unknown[0].Evidence != "written by make gen this turn" || !unknown[0].Generated {
		t.Fatalf("the claim was recorded as %+v", unknown)
	}
	if !slices.Contains(m.changes.Paths(), gen) {
		t.Fatal("the stager's list does not hold the answered claim")
	}
}

// A claim never joins the queue's batch, as the card in front or as a row
// behind it: the list states a call's summary, not the claim's rows.
func TestClaim_NeverJoinsABatch(t *testing.T) {
	var ran bool
	m, _ := claimModel(t, &ran)
	if _, batchable := m.batchCategory(m.approval.request); batchable {
		t.Fatal("a claim can be answered from the queue list")
	}
}

// A no to a claim refuses it by name and records nothing.
func TestClaim_ANoRefusesItByName(t *testing.T) {
	var ran bool
	m, _ := claimModel(t, &ran)
	m = handover(t, m)
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = updated.(Model)
	if ran || len(m.changes.Unknown()) != 0 {
		t.Fatal("a refused claim ran or was recorded")
	}
	msgs := m.agent.Messages()
	last := msgs[len(msgs)-1].Content
	if !strings.Contains(last, "declined the claim on gen.pb.go") {
		t.Fatalf("the model read %q", last)
	}
}
