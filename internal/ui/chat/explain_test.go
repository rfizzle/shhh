package chat

// The explanation at the command card (run.go): one key puts a paragraph
// about the command on the screen, and the decision is still waiting behind
// it (docs/interface/surfaces.md#the-approval-card).

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
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
	return m.WithLedger(ledger).
		WithExplainer(agent.NewExplainer(ledger.For(p, meter.SourceExplanation), cfg))
}

// offersExplain reports whether the card is advertising the explain key.
func offersExplain(card *components.ApprovalCard) bool {
	return slices.ContainsFunc(card.ExtraHints, func(o components.KeyOffer) bool {
		return o.Key == keys.Bracket(keys.Decision.Explain)
	})
}

func drainExplain(t *testing.T, cmd tea.Cmd) explainDoneMsg {
	t.Helper()
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(explainDoneMsg); ok {
			return msg
		}
	}
	t.Fatal("expected an explainDoneMsg from the explain key")
	return explainDoneMsg{}
}

func TestApprovalCard_ExplainOpensTheParagraphAndDecidesNothing(t *testing.T) {
	p := &paragraphProvider{text: "rsync copies src/ into dst/ and deletes anything in dst/ that is not in src/."}
	m := explainerModel(t, p, agent.ExplainConfig{Model: "small", Prompt: explainWording})
	m = execApproval(t, m, "rsync -a --delete src/ dst/")
	if card := m.approvalCard(); !offersExplain(card) {
		t.Fatalf("a command card in a session with an explainer should offer the key:\n%s", m.View().Content)
	}

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = updated.(Model)
	// The decision is exactly where it was while the reading is in flight,
	// and the card says the answer is on its way.
	if m.state != stateConfirmRun || m.pendingApproval == nil {
		t.Fatalf("the card should still be waiting, got state %d pending %v", m.state, m.pendingApproval)
	}
	if !strings.Contains(m.View().Content, "explain — asking") {
		t.Fatalf("the card should say the explanation is being read:\n%s", m.View().Content)
	}
	done := drainExplain(t, cmd)

	updated, _ = m.Update(done)
	m = updated.(Model)
	if m.state != stateOutputFull {
		t.Fatalf("the paragraph should open on the screen, got state %d", m.state)
	}
	view := m.View().Content
	if !strings.Contains(view, "deletes anything in dst/") {
		t.Fatalf("the screen should carry the paragraph:\n%s", view)
	}
	if !strings.Contains(view, "small") {
		t.Fatalf("the screen should name the model that answered:\n%s", view)
	}

	// Esc comes back to the decision, which nothing has answered.
	m = press(t, m, "esc")
	if m.state != stateConfirmRun || m.pendingApproval == nil {
		t.Fatalf("esc should come back to the waiting decision, got state %d pending %v", m.state, m.pendingApproval)
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

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = updated.(Model)
	updated, _ = m.Update(drainExplain(t, cmd))
	m = updated.(Model)

	// The spend is a line in /cost of its own, so a keystroke that costs
	// money is attributable rather than folded into the classifier's total
	// (docs/architecture.md#spend-is-counted-at-the-provider).
	got := m.ledger.SourceTotal(meter.SourceExplanation)
	if got.In != 210 || got.Out != 88 {
		t.Fatalf("the explanation should be billed under its own source, got ↑%d ↓%d", got.In, got.Out)
	}
	if other := m.ledger.SourceTotal(meter.SourceClassifier); other.Requests != 0 {
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
			updated, cmd := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
			m = updated.(Model)
			done := drainExplain(t, cmd)
			if !done.verdict.Failed {
				t.Fatalf("the reading should have failed, got %+v", done.verdict)
			}
			updated, _ = m.Update(done)
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
			if m.state != stateConfirmRun || m.pendingApproval == nil {
				t.Fatalf("the decision should still be waiting, got state %d pending %v", m.state, m.pendingApproval)
			}
			for _, msg := range m.Messages() {
				if msg.Role == provider.RoleTool {
					t.Fatalf("a failed explanation must never answer the call: %+v", msg)
				}
			}
			// And the offer is back, rather than stuck saying "asking".
			if !strings.Contains(m.View().Content, "explain — what this command does") {
				t.Fatalf("the key should be offered again:\n%s", m.View().Content)
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
				t.Fatalf("a session that cannot answer must not offer the key:\n%s", m.View().Content)
			}
			// And the key is not secretly live.
			updated, cmd := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
			m = updated.(Model)
			if cmd != nil {
				t.Fatal("the key should start nothing where there is nothing to ask")
			}
			if p.calls != 0 {
				t.Fatalf("nothing should have been asked, got %d requests", p.calls)
			}
			if m.state != stateConfirmRun || m.pendingApproval == nil {
				t.Fatalf("the card should still be waiting, got state %d pending %v", m.state, m.pendingApproval)
			}
		})
	}
}

// It is the command card's key and not every card's: a diff is already the
// explanation of an edit, and the full view already shows it whole.
func TestApprovalCard_ExplainIsNotOfferedOnAnEdit(t *testing.T) {
	p := &paragraphProvider{text: "unused"}
	ledger := meter.New(nil)
	m := handover(t, interruptedModel(t, ""))
	m = m.WithLedger(ledger).
		WithExplainer(agent.NewExplainer(ledger.For(p, meter.SourceExplanation), agent.ExplainConfig{Model: "small", Prompt: explainWording}))
	if m.pendingApproval == nil || m.pendingApproval.kind != approvalDiff {
		t.Fatalf("the fixture should be sitting on an edit card, got %v", m.pendingApproval)
	}
	if card := m.approvalCard(); offersExplain(card) {
		t.Fatalf("an edit card must not offer the key:\n%s", m.View().Content)
	}
	if p.calls != 0 {
		t.Fatalf("nothing should have been asked, got %d requests", p.calls)
	}
}
