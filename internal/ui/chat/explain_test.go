package chat

// The explanation at the command card (run.go): the full view carries a
// paragraph about the command, and the decision is still waiting behind it
// (docs/interface/surfaces.md#the-approval-card).

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// paragraphProvider answers every request with a scripted paragraph, or with
// the failure the test is about. Nothing here reaches a network: the key
// spends money in a real session and must spend none in the suite.
type paragraphProvider struct {
	text  string
	err   error
	hang  bool // return no events at all, so the reading meets its deadline
	calls int
	// seen is the last request's messages, so a test can assert what was
	// sent and — more to the point — what was not.
	seen []provider.Message
}

func (p *paragraphProvider) StreamCompletion(ctx context.Context, msgs []provider.Message, _ provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	p.calls++
	p.seen = msgs
	if p.err != nil {
		return nil, p.err
	}
	ch := make(chan provider.StreamEvent, 1)
	if p.hang {
		// Never written to and never closed: the reading ends on its own
		// deadline, which is the timeout the card has to survive.
		return ch, nil
	}
	ch <- provider.StreamEvent{
		Token: p.text,
		Usage: &provider.Usage{PromptTokens: 210, CompletionTokens: 88},
		Done:  true,
	}
	close(ch)
	return ch, nil
}

func (p *paragraphProvider) Name() string { return "paragraph" }

// explainWording stands in for the instruction the session hands the
// explainer, which is the one-shot's (internal/cli/approvals.go). Which words
// those are is not this package's to assert; that there must be some is what
// the fixtures are showing.
const explainWording = "Explain this shell command concisely."

// explainerModel is a contained session whose card can be asked what a
// command does, wired the way the session wires it: the explainer reaches the
// provider through the gate, so what it spends is billed without the model
// counting anything itself (internal/cli/approvals.go).
func explainerModel(t *testing.T, p provider.Provider, cfg agent.ExplainConfig) Model {
	t.Helper()
	var bare, contained []string
	ledger := meter.New(nil)
	m := containedModel(t, &bare, &contained, "contained: bwrap")
	m.wiring.Ledger = ledger
	m.wiring.Explainer = agent.NewExplainer(ledger.For(p, meter.SourceExplanation), cfg)
	return m
}

// offersExplain reports whether the card says its full view explains.
func offersExplain(card *components.ApprovalCard) bool {
	return strings.Contains(card.FullLabel, explainHeading)
}

// openFull presses enter on the card: the full view, and the reading.
func openFull(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return updated.(Model), cmd
}

func drainExplain(t *testing.T, cmd tea.Cmd) explainDoneMsg {
	t.Helper()
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(explainDoneMsg); ok {
			return msg
		}
	}
	t.Fatal("expected an explainDoneMsg from the full view")
	return explainDoneMsg{}
}

func TestApprovalCard_TheFullViewExplainsAndDecidesNothing(t *testing.T) {
	p := &paragraphProvider{text: "rsync copies src/ into dst/ and deletes anything in dst/ that is not in src/."}
	m := explainerModel(t, p, agent.ExplainConfig{Model: "small", Prompt: explainWording})
	m = execApproval(t, m, "rsync -a --delete src/ dst/")
	if card := m.approvalCard(); !offersExplain(card) {
		t.Fatalf("a command card in a session with an explainer should say its full view explains:\n%s", m.View().Content)
	}

	m, cmd := openFull(t, m)
	// The full view opens at once, with the decision exactly where it was
	// behind it, and says the answer is on its way.
	if m.state != stateOutputFull || m.approval.request == nil {
		t.Fatalf("the full view should open over the waiting card, got state %d pending %v", m.state, m.approval.request)
	}
	if !strings.Contains(m.View().Content, explainHeading+" — asking") {
		t.Fatalf("the full view should say the explanation is being read:\n%s", m.View().Content)
	}
	done := drainExplain(t, cmd)

	updated, _ := m.Update(done)
	m = updated.(Model)
	if m.state != stateOutputFull {
		t.Fatalf("the paragraph should land on the open full view, got state %d", m.state)
	}
	view := m.View().Content
	if !strings.Contains(view, "rsync -a --delete src/ dst/") {
		t.Fatalf("the full view keeps the command it explains:\n%s", view)
	}
	if !strings.Contains(view, "deletes anything in dst/") {
		t.Fatalf("the screen should carry the paragraph:\n%s", view)
	}
	if !strings.Contains(view, "small") {
		t.Fatalf("the screen should name the model that answered:\n%s", view)
	}

	// Esc comes back to the decision, which nothing has answered.
	m = press(t, m, "esc")
	if m.state != stateConfirmRun || m.approval.request == nil {
		t.Fatalf("esc should come back to the waiting decision, got state %d pending %v", m.state, m.approval.request)
	}
	// The paragraph never reaches the conversation: the model asked to run
	// this command and is still waiting for the answer to that.
	for _, msg := range m.Messages() {
		if strings.Contains(msg.Content, "deletes anything in dst/") {
			t.Fatalf("an explanation must never reach the conversation: %+v", msg)
		}
		if msg.Role == provider.RoleTool {
			t.Fatalf("an explanation must never answer the call: %+v", msg)
		}
	}
	// Nor does it leave a row: nothing ran on this machine, so there is
	// nothing for the transcript to account for.
	for _, e := range m.transcript {
		if strings.Contains(e.toolResult, "deletes anything in dst/") {
			t.Fatalf("an explanation is a reading, not an act, and leaves no row: %+v", e)
		}
	}
	if p.calls != 1 {
		t.Fatalf("one press should be one request, got %d", p.calls)
	}
	// A second look reads the paragraph the first one brought back, and
	// asks nothing.
	again, cmd := openFull(t, m)
	if cmd != nil {
		for _, c := range unwrapBatch(cmd) {
			if _, ok := c().(explainDoneMsg); ok {
				t.Fatal("a second enter must not ask again")
			}
		}
	}
	if !strings.Contains(again.View().Content, "deletes anything in dst/") {
		t.Fatalf("the second full view should carry the paragraph:\n%s", again.View().Content)
	}
	// The command travelled as evidence rather than as an instruction.
	last := p.seen[len(p.seen)-1]
	if last.Role != provider.RoleUser || !strings.Contains(last.Content, "UNTRUSTED COMMAND") {
		t.Fatalf("the command should be labelled as the untrusted data it is, got %+v", last)
	}
}

func TestApprovalCard_ExplainIsBilledUnderItsOwnSource(t *testing.T) {
	p := &paragraphProvider{text: "it copies src/ into dst/."}
	m := explainerModel(t, p, agent.ExplainConfig{Model: "small", Prompt: explainWording})
	m = execApproval(t, m, "rsync -a --delete src/ dst/")

	m, cmd := openFull(t, m)
	updated, _ := m.Update(drainExplain(t, cmd))
	m = updated.(Model)

	// The spend is a line in /cost of its own, so a keystroke that costs
	// money is attributable rather than folded into the classifier's total
	// (docs/architecture.md#spend-is-counted-at-the-provider).
	got := m.wiring.Ledger.SourceTotal(meter.SourceExplanation)
	if got.In != 210 || got.Out != 88 {
		t.Fatalf("the explanation should be billed under its own source, got ↑%d ↓%d", got.In, got.Out)
	}
	if other := m.wiring.Ledger.SourceTotal(meter.SourceClassifier); other.Requests != 0 {
		t.Fatalf("an explanation is not the classifier's spend, got %d requests", other.Requests)
	}
	// And it is background spend, not the agent's own turns.
	if m.TotalTokensIn != 0 || m.TotalTokensOut != 0 {
		t.Fatalf("an explanation is not the agent's own spend, got ↑%d ↓%d", m.TotalTokensIn, m.TotalTokensOut)
	}
}

// A reading that did not happen says so and gives the screen back. The two
// ways it can fail are one case here because they are one case to the reader:
// there is no paragraph, and the decision is still waiting.
func TestApprovalCard_AFailedExplanationSaysSoAndReturns(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    *paragraphProvider
		cfg  agent.ExplainConfig
		want string
	}{
		{
			name: "the request failed",
			p:    &paragraphProvider{err: errors.New("no route to host")},
			cfg:  agent.ExplainConfig{Model: "small", Prompt: explainWording},
			want: "no route to host",
		},
		{
			name: "the deadline passed",
			p:    &paragraphProvider{hang: true},
			cfg:  agent.ExplainConfig{Model: "small", Prompt: explainWording, Timeout: time.Millisecond},
			want: "deadline exceeded",
		},
		{
			name: "the model said nothing",
			p:    &paragraphProvider{text: "   "},
			cfg:  agent.ExplainConfig{Model: "small", Prompt: explainWording},
			want: "no explanation",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := explainerModel(t, tc.p, tc.cfg)
			m = execApproval(t, m, "rsync -a --delete src/ dst/")
			m, cmd := openFull(t, m)
			done := drainExplain(t, cmd)
			if !done.verdict.Failed {
				t.Fatalf("the reading should have failed, got %+v", done.verdict)
			}
			updated, _ := m.Update(done)
			m = updated.(Model)
			if m.state != stateOutputFull {
				t.Fatalf("a failure is still an answer to the press, got state %d", m.state)
			}
			view := m.View().Content
			if !strings.Contains(view, "could not be read") || !strings.Contains(view, tc.want) {
				t.Fatalf("the screen should say what went wrong, wanted %q:\n%s", tc.want, view)
			}
			// It never counts as an answer.
			m = press(t, m, "esc")
			if m.state != stateConfirmRun || m.approval.request == nil {
				t.Fatalf("the decision should still be waiting, got state %d pending %v", m.state, m.approval.request)
			}
			for _, msg := range m.Messages() {
				if msg.Role == provider.RoleTool {
					t.Fatalf("a failed explanation must never answer the call: %+v", msg)
				}
			}
			// And the card is not stuck asking.
			if m.approval.request.explaining {
				t.Fatal("a failed reading should not leave the card asking")
			}
		})
	}
}

// An offer that cannot be answered is worse than no offer, so both halves of
// "configured" have to turn it off: the model that answers, and the
// instruction the session hands down.
func TestApprovalCard_ExplainNotOfferedWithoutAModel(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  agent.ExplainConfig
	}{
		{name: "nothing configured at all", cfg: agent.ExplainConfig{}},
		{name: "a model and no instruction", cfg: agent.ExplainConfig{Model: "small"}},
		{name: "an instruction and no model", cfg: agent.ExplainConfig{Prompt: explainWording}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &paragraphProvider{text: "unused"}
			m := explainerModel(t, p, tc.cfg)
			m = execApproval(t, m, "rsync -a --delete src/ dst/")
			if card := m.approvalCard(); offersExplain(card) {
				t.Fatalf("a session that cannot answer must not promise an explanation:\n%s", m.View().Content)
			}
			// And the full view does not secretly ask.
			m, cmd := openFull(t, m)
			if cmd != nil {
				for _, c := range unwrapBatch(cmd) {
					if _, ok := c().(explainDoneMsg); ok {
						t.Fatal("the full view should ask nothing where there is nothing to ask")
					}
				}
			}
			if p.calls != 0 {
				t.Fatalf("nothing should have been asked, got %d requests", p.calls)
			}
			if strings.Contains(m.View().Content, explainHeading) {
				t.Fatalf("the full view should carry no explanation:\n%s", m.View().Content)
			}
			if m.approval.request == nil {
				t.Fatal("the card should still be waiting")
			}
		})
	}
}

// It is the command card's and not every card's: a diff is already the
// explanation of an edit, and the full view already shows it whole.
func TestApprovalCard_ExplainIsNotOfferedOnAnEdit(t *testing.T) {
	p := &paragraphProvider{text: "unused"}
	ledger := meter.New(nil)
	m := handover(t, interruptedModel(t, ""))
	m.wiring.Ledger = ledger
	m.wiring.Explainer = agent.NewExplainer(ledger.For(p, meter.SourceExplanation), agent.ExplainConfig{Model: "small", Prompt: explainWording})
	if m.approval.request == nil || m.approval.request.kind != approvalDiff {
		t.Fatalf("the fixture should be sitting on an edit card, got %v", m.approval.request)
	}
	if card := m.approvalCard(); offersExplain(card) {
		t.Fatalf("an edit card must not offer the key:\n%s", m.View().Content)
	}
	if p.calls != 0 {
		t.Fatalf("nothing should have been asked, got %d requests", p.calls)
	}
}

// A reading of a line the reader has since amended is dropped whole: it
// neither lands on the new line's view nor ends the asking about it.
func TestApprovalCard_AnAmendedLinesReadingIsNotTheOldOnes(t *testing.T) {
	p := &paragraphProvider{text: "it copies src/ into dst/."}
	m := explainerModel(t, p, agent.ExplainConfig{Model: "small", Prompt: explainWording})
	m = execApproval(t, m, "rsync -a --delete src/ dst/")
	m, cmd := openFull(t, m)
	old := drainExplain(t, cmd)
	req := m.approval.request
	req.command, req.explaining, req.explained = "rsync -a src/ dst/", true, nil
	updated, _ := m.Update(old)
	m = updated.(Model)
	if !m.approval.request.explaining || m.approval.request.explained != nil {
		t.Fatalf("the old line's reading should leave the new line asking, got %+v", m.approval.request.explained)
	}
}
