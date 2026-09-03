package chat

// Stream resume and the cheaper-model fallback. The two paths are
// tested apart because they are apart: a drop has something to keep and never
// re-requests on its own, and a wait has nothing to keep and does.

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// resumeModel is a session with two priced models from one provider, so the
// fallback has something honest to name.
func resumeModel(t *testing.T) Model {
	t.Helper()
	table := pricing.NewTable(map[string]pricing.ModelPricing{
		"gpt-4o":      {InputCostPerToken: 0.00001, OutputCostPerToken: 0.00003},
		"gpt-4o-mini": {InputCostPerToken: 0.0000006, OutputCostPerToken: 0.0000024},
		"gpt-4.1":     {InputCostPerToken: 0.000002, OutputCostPerToken: 0.000008},
	})
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream).
		WithPricing(table, "gpt-4o").
		WithModelOptions([]string{"gpt-4o", "gpt-4.1", "gpt-4o-mini"}).
		WithModelSwitcher(func(string) {})
	m.providerName = "openai"
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	return updated.(Model)
}

// streamed puts the model mid-turn with text already on screen.
func streamed(m Model, partial string) Model {
	m.turnOpen = true
	m.turnStarted = time.Now()
	m.setTurnState(stateStreaming)
	m.streaming = partial
	return m
}

// dropEntry finds the stream-drop row in a transcript.
func dropEntry(t *testing.T, m Model) entry {
	t.Helper()
	for _, e := range m.transcript {
		if e.kind == entryStreamDrop {
			return e
		}
	}
	t.Fatalf("no stream-drop row in %d entries", len(m.transcript))
	return entry{}
}

func networkFailure() *provider.Failure {
	return &provider.Failure{
		Class: provider.ClassNetwork, Provider: "openai",
		Message: "unexpected EOF",
	}
}

func TestStreamDrop_KeepsThePartialAndOffersBothWaysOn(t *testing.T) {
	m := streamed(resumeModel(t), "so I'll thread the sentinel through runRound and then")
	updated, _ := m.Update(streamErrMsg{err: networkFailure()})
	next := updated.(Model)

	e := dropEntry(t, next)
	if e.resume.text == "" {
		t.Fatal("the partial reply is the whole point of the row")
	}
	view := stripANSI(next.dropRow(e).View(110))
	for _, want := range []string{
		"stream", "dropped mid-reply", "tokens kept", "partial",
		"…so I'll thread the sentinel", "[c]", "continue from here",
		"[r]", "ask again from scratch", "the partial reply stays",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the row should say %q, got:\n%s", want, view)
		}
	}
	// The classified failure keeps its own row above the offer.
	var failures int
	for _, entry := range next.transcript {
		if entry.kind == entryFailure {
			failures++
		}
	}
	if failures != 1 {
		t.Errorf("the failure is still a row of its own, got %d", failures)
	}
	// Nothing is re-requested behind your back.
	if next.turnState() != stateInput {
		t.Errorf("state = %v, want the input back", next.turnState())
	}
}

func TestStreamDrop_ContinueSendsThePartialBackAsContext(t *testing.T) {
	m := streamed(resumeModel(t), "half a sentence")
	updated, _ := m.Update(streamErrMsg{err: networkFailure()})
	next := updated.(Model)

	before := len(next.agent.Messages())
	next.focusIdx = indexOfKind(t, next, entryStreamDrop)
	resumed, cmd, claimed := next.dropKey(keys.Shown(keys.Row.Continue))
	if !claimed {
		t.Fatal("[c] should be claimed by the focused drop row")
	}
	after := resumed.(Model)
	if cmd == nil {
		t.Fatal("continuing should ask the model")
	}
	if after.turnState() != stateStreaming {
		t.Errorf("state = %v, want streaming", after.turnState())
	}

	msgs := after.agent.Messages()
	if len(msgs) != before+2 {
		t.Fatalf("continuing adds the partial turn and the instruction, got %d new", len(msgs)-before)
	}
	assistant := msgs[len(msgs)-2]
	if assistant.Role != provider.RoleAssistant || assistant.Content != "half a sentence" {
		t.Errorf("the partial should go back as the assistant's own turn, got %+v", assistant)
	}
	if last := msgs[len(msgs)-1]; last.Role != provider.RoleUser || !strings.Contains(last.Content, "Continue it") {
		t.Errorf("the instruction to carry on should follow it, got %+v", last)
	}
	// Taking the offer spends it: the same partial cannot be sent twice.
	if _, _, claimed := after.dropKey(keys.Shown(keys.Row.Continue)); claimed {
		t.Error("a spent offer should stop claiming its key")
	}
	if keys := after.dropKeys(dropEntry(t, after).resume); len(keys) != 0 {
		t.Errorf("a spent offer keeps its words and loses its keys, got %v", keys)
	}
}

func TestStreamDrop_ContinueWithToolCallsResumesTheRound(t *testing.T) {
	m := streamed(resumeModel(t), "reading the file first")
	calls := []provider.ToolCall{{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`}}
	updated, _ := m.Update(streamErrMsg{err: networkFailure(), calls: calls})
	next := updated.(Model)

	e := dropEntry(t, next)
	if len(e.resume.calls) != 1 {
		t.Fatalf("the finished call should be kept, got %+v", e.resume.calls)
	}
	if !strings.Contains(stripANSI(next.dropRow(e).View(110)), "1 tool call") {
		t.Errorf("the row should count what it kept:\n%s", stripANSI(next.dropRow(e).View(110)))
	}

	next.focusIdx = indexOfKind(t, next, entryStreamDrop)
	resumed, _, claimed := next.dropKey(keys.Shown(keys.Row.Continue))
	if !claimed {
		t.Fatal("[c] should be claimed with tool calls too")
	}
	after := resumed.(Model)
	msgs := after.agent.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != provider.RoleAssistant || len(last.ToolCalls) != 1 {
		t.Fatalf("continuing a call round appends the assistant turn that made them, got %+v", last)
	}
	if last.ToolCalls[0].ID != "call_1" {
		t.Errorf("the call kept should be the call resumed, got %+v", last.ToolCalls[0])
	}
	// No second "carry on" instruction: the calls are the continuation.
	if msgs[len(msgs)-1].Role == provider.RoleUser {
		t.Error("a resumed tool round should not also be nudged in prose")
	}
}

func TestStreamDrop_AskAgainDiscardsThePartial(t *testing.T) {
	m := streamed(resumeModel(t), "half a sentence")
	updated, _ := m.Update(streamErrMsg{err: networkFailure()})
	next := updated.(Model)
	before := len(next.agent.Messages())

	next.focusIdx = indexOfKind(t, next, entryStreamDrop)
	again, cmd, claimed := next.dropKey(keys.Shown(keys.Row.Retry))
	if !claimed {
		t.Fatal("[r] should be claimed by the drop row")
	}
	after := again.(Model)
	if cmd == nil || after.turnState() != stateStreaming {
		t.Fatalf("asking again reopens the turn, state = %v", after.turnState())
	}
	if len(after.agent.Messages()) != before {
		t.Errorf("asking again from scratch adds nothing to the conversation, got %d new",
			len(after.agent.Messages())-before)
	}
}

func TestStreamDrop_FocusOpensOnTheOffer(t *testing.T) {
	m := streamed(resumeModel(t), "half a sentence")
	updated, _ := m.Update(streamErrMsg{err: networkFailure()})
	next := updated.(Model)
	next.invalidateRenderCache()

	focused, _ := next.enterFocusMode()
	after := focused.(Model)
	if after.transcript[after.focusIdx].kind != entryStreamDrop {
		t.Errorf("focus should open on the row holding the way out, got %v",
			after.transcript[after.focusIdx].kind)
	}
}

func rateLimit(after time.Duration) *provider.Failure {
	return &provider.Failure{
		Class: provider.ClassRateLimit, Status: 429, Provider: "openai",
		Message: "Rate limit reached for gpt-4o. Please try again in 20s.", RetryAfter: after,
	}
}

func TestRateLimit_WaitsOnACountdownWithItsBound(t *testing.T) {
	m := streamed(resumeModel(t), "")
	updated, _ := m.Update(streamErrMsg{err: rateLimit(20 * time.Second)})
	next := updated.(Model)

	if next.turnState() != stateRetryWait || next.retry == nil {
		t.Fatalf("a rate limit with nothing to keep should wait, state = %v", next.turnState())
	}
	if next.retry.wait != 20*time.Second {
		t.Errorf("the provider's own wait should be believed, got %v", next.retry.wait)
	}
	block := stripANSI(next.retryWaitBlock(110))
	for _, want := range []string{"▰", "retry in", "attempt 1 of 3", "[m]", "gpt-4.1", "[esc]"} {
		if !strings.Contains(block, want) {
			t.Errorf("the countdown should say %q, got:\n%s", want, block)
		}
	}
	// The row above it is the classified failure, without a second set of
	// keys answering the same stall.
	e := next.transcript[indexOfKind(t, next, entryFailure)]
	if row := next.failureRow(e); len(row.Keys) != 0 {
		t.Errorf("the stalled row hands its offers to the countdown, got %v", row.Keys)
	}
	// The turn is still in flight, and the input is not taking keys.
	if !next.working() || next.inputLive() {
		t.Errorf("working = %v, inputLive = %v; a wait is a turn with the keyboard", next.working(), next.inputLive())
	}
}

func TestRetryWait_TickResumesWhenTheCountdownRunsOut(t *testing.T) {
	m := streamed(resumeModel(t), "")
	updated, _ := m.Update(streamErrMsg{err: rateLimit(0)})
	next := updated.(Model)
	if next.retry == nil {
		t.Fatal("a rate limit without a named wait still waits")
	}
	// A tick before the deadline redraws and schedules the next one.
	ticked, cmd := next.Update(retryTickMsg{seq: next.retry.seq})
	if cmd == nil || ticked.(Model).turnState() != stateRetryWait {
		t.Fatal("a tick inside the wait keeps waiting")
	}
	// A tick after it sends the request again.
	next.retry.deadline = time.Now().Add(-time.Second)
	fired, cmd := next.Update(retryTickMsg{seq: next.retry.seq})
	after := fired.(Model)
	if after.turnState() != stateStreaming || cmd == nil {
		t.Fatalf("the retry should go out when the countdown runs out, state = %v", after.turnState())
	}
	if after.retry != nil {
		t.Error("the wait is over once the request is out")
	}
	// A tick from a wait that has been superseded changes nothing.
	stale, cmd := after.Update(retryTickMsg{seq: 0})
	if cmd != nil || stale.(Model).turnState() != stateStreaming {
		t.Error("a stale tick should be ignored")
	}
}

func TestRetryWait_EscStopsWithoutLosingTheTurn(t *testing.T) {
	m := streamed(resumeModel(t), "")
	updated, _ := m.Update(streamErrMsg{err: rateLimit(30 * time.Second)})
	next := updated.(Model)
	before := len(next.transcript)

	stopped, _ := next.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	after := stopped.(Model)
	if after.turnState() != stateInput || after.retry != nil {
		t.Fatalf("esc should end the wait, state = %v", after.turnState())
	}
	if after.turnOutcome != components.TurnCancelled {
		t.Errorf("outcome = %v, want cancelled — you stopped it", after.turnOutcome)
	}
	if len(after.transcript) < before {
		t.Error("stopping keeps everything the turn already did")
	}
}

func TestRetryWait_BoundIsHonoured(t *testing.T) {
	m := streamed(resumeModel(t), "")
	for i := 1; i <= agent.MaxRetryAttempts; i++ {
		updated, _ := m.Update(streamErrMsg{err: rateLimit(time.Second)})
		m = updated.(Model)
		if m.turnState() != stateRetryWait {
			t.Fatalf("attempt %d should still be waiting, state = %v", i, m.turnState())
		}
		if m.retry.attempt != i {
			t.Fatalf("attempt = %d, want %d", m.retry.attempt, i)
		}
		if !strings.Contains(stripANSI(m.retryWaitBlock(110)), "of 3") {
			t.Error("the bound is stated on every attempt")
		}
		m.setTurnState(stateStreaming)
	}
	// One more failure is past the bound: it becomes an ordinary failure row
	// with its own keys, and the turn ends.
	updated, _ := m.Update(streamErrMsg{err: rateLimit(time.Second)})
	last := updated.(Model)
	if last.turnState() != stateInput || last.retry != nil {
		t.Fatalf("past the bound the turn ends, state = %v", last.turnState())
	}
	e := last.transcript[indexOfKind(t, last, entryFailure)]
	if len(last.failureKeys(e.fail)) == 0 {
		t.Error("the row that reports the bound keeps its own way out")
	}
}

func TestRetryChain_ResetByARequestThatWasAnswered(t *testing.T) {
	m := streamed(resumeModel(t), "")
	updated, _ := m.Update(streamErrMsg{err: rateLimit(time.Second)})
	next := updated.(Model)
	if next.backoff.Attempt() != 1 {
		t.Fatalf("attempt = %d, want 1", next.backoff.Attempt())
	}
	answered, _ := next.Update(tokenMsg{text: "hello"})
	done := answered.(Model)
	if done.backoff.Attempt() != 0 {
		t.Error("a provider that answered ends the stall; the next one gets its own bound")
	}
}

func TestRetryWait_FallbackFinishesOnACheaperModel(t *testing.T) {
	m := streamed(resumeModel(t), "")
	updated, _ := m.Update(streamErrMsg{err: rateLimit(30 * time.Second)})
	next := updated.(Model)
	// Closest cheaper, not cheapest: the turn still has to finish.
	if next.retry.fallback != "gpt-4.1" {
		t.Fatalf("fallback = %q, want the closest cheaper model from the same provider", next.retry.fallback)
	}

	switched, cmd := next.Update(keyPress('m'))
	after := switched.(Model)
	if after.modelName != "gpt-4.1" {
		t.Fatalf("model = %q, want the fallback", after.modelName)
	}
	if after.turnState() != stateStreaming || cmd == nil {
		t.Fatalf("the fallback finishes the turn rather than waiting out a limit it is not under, state = %v", after.turnState())
	}
	// The switch is on the record in the transcript…
	var noted bool
	for _, e := range after.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "gpt-4.1") {
			noted = true
		}
	}
	if !noted {
		t.Error("switching model mid-turn belongs in the transcript")
	}
	// …and in what /stats reports, so the cost attribution stays honest.
	after.accumulateUsage(&provider.Usage{PromptTokens: 100, CompletionTokens: 50})
	stats := after.statsReport()
	if !strings.Contains(stats, "By model:") || !strings.Contains(stats, "gpt-4.1") {
		t.Errorf("/stats should split the spend by model, got:\n%s", stats)
	}
}

func TestCheaperModel_ClosestBelowAndNothingInvented(t *testing.T) {
	m := resumeModel(t)
	// Closest cheaper, not cheapest: the turn still has to finish, and the
	// least capable model in the catalog is the one least likely to.
	if got := m.cheaperModel(); got != "gpt-4.1" {
		t.Errorf("cheaperModel = %q, want the closest below gpt-4o", got)
	}
	// Already on the cheapest: nothing to offer.
	m.modelName = "gpt-4o-mini"
	if got := m.cheaperModel(); got != "" {
		t.Errorf("nothing is cheaper than the cheapest, got %q", got)
	}
	// No price for the model in hand: no honest comparison, so no offer.
	m.modelName = "some-local-model"
	if got := m.cheaperModel(); got != "" {
		t.Errorf("an unpriced model has no cheaper, got %q", got)
	}
}

func TestRetryWait_NoFallbackOfferedWhenThereIsNone(t *testing.T) {
	m := resumeModel(t)
	m.modelName = "gpt-4o-mini"
	m = streamed(m, "")
	updated, _ := m.Update(streamErrMsg{err: rateLimit(10 * time.Second)})
	next := updated.(Model)
	if next.retry.fallback != "" {
		t.Fatalf("fallback = %q, want none", next.retry.fallback)
	}
	block := stripANSI(next.retryWaitBlock(110))
	if strings.Contains(block, "[m]") {
		t.Errorf("a key the session cannot honour is not offered:\n%s", block)
	}
	if !strings.Contains(block, "[esc]") {
		t.Errorf("esc is always offered — a wait you cannot stop is a hang:\n%s", block)
	}
	// And the key does nothing when it is not on the row.
	pressed, _ := next.Update(keyPress('m'))
	if pressed.(Model).turnState() != stateRetryWait {
		t.Error("an unoffered key should not act")
	}
}

func TestCountdownText_WholeSeconds(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{20 * time.Second, "20s"},
		{1500 * time.Millisecond, "2s"},
		{100 * time.Millisecond, "1s"},
		{0, "0s"},
		{75 * time.Second, "1m 15s"},
	} {
		if got := countdownText(tc.in); got != tc.want {
			t.Errorf("countdownText(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCancelledStream_IsNeitherADropNorAWait(t *testing.T) {
	m := streamed(resumeModel(t), "half a sentence")
	updated, _ := m.Update(streamErrMsg{err: &provider.Failure{Class: provider.ClassCancelled}})
	next := updated.(Model)
	for _, e := range next.transcript {
		if e.kind == entryStreamDrop {
			t.Fatal("a stop you asked for is not a stall to recover from")
		}
	}
	if next.retry != nil || next.turnState() != stateInput {
		t.Errorf("a cancellation ends the turn, state = %v", next.turnState())
	}
}

// indexOfKind is the first transcript index holding a kind.
func indexOfKind(t *testing.T, m Model, kind entryKind) int {
	t.Helper()
	for i, e := range m.transcript {
		if e.kind == kind {
			return i
		}
	}
	t.Fatalf("no entry of kind %v in %d entries", kind, len(m.transcript))
	return -1
}

// The session records the wait it draws. The meter is what the person sees;
// the signal is what the count of "which surface is being throttled" is made
// of, and a surface that only drew the wait would be missing from it.
func TestRetryWait_IsOnTheRecord(t *testing.T) {
	type signal struct{ code, reason string }
	var signals []signal
	m := resumeModel(t).WithObserver(observe.Observer{
		Signal: func(_ observe.Pos, code, reason string) {
			signals = append(signals, signal{code, reason})
		},
	})
	updated, _ := streamed(m, "").Update(streamErrMsg{err: rateLimit(time.Second)})
	if updated.(Model).turnState() != stateRetryWait {
		t.Fatal("a rate limit should put the turn on a wait")
	}
	if len(signals) != 1 || signals[0].code != observe.SignalRetry || signals[0].reason != "rate-limit" {
		t.Fatalf("expected one retry signal naming its class, got %+v", signals)
	}
}
