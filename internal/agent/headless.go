package agent

// Headless drives an Agent's loop synchronously to completion: one
// user turn, stream events consumed inline, tool rounds dispatched until the
// model produces a final message or the per-turn round cap is hit. The chat
// TUI drives the same Agent asynchronously through Bubble Tea messages; this
// runner is the scriptable front-end behind `shhh code -p` and each
// sub-agent. Steering and interruption let a supervisor redirect or
// cancel a running turn the way the TUI's steering mechanics do.
//
// It is the session's loop and not a simpler one: a round's auto calls are
// dispatched together through the same bounded concurrency, and a request the
// provider never answered is waited out on the same bound. The two surfaces
// differ in how the wait looks, never in whether it happens.
// See docs/capabilities/coding-agent.md#an-unattended-run-runs-the-same-loop.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

// ErrRoundCap is returned by Headless.Run when a turn uses up its tool
// rounds without reaching a final assistant message.
var ErrRoundCap = errors.New("tool round cap reached")

// ErrInterrupted is returned by Headless.Run when Interrupt cancels the turn.
// The conversation is left well-formed: outstanding tool calls received
// synthetic results via Agent.CancelTurn, mirroring the TUI's cancelStreaming
// semantics.
var ErrInterrupted = errors.New("turn interrupted")

// Headless runs an Agent to completion without a UI. Gate decides which tool
// calls need approval; Resolve decides and (if approved) executes each gated
// call, returning the tool-result content to record — in a non-interactive
// run "approval" is policy, not a prompt. The On* hooks surface activity to
// the front-end and may be nil.
type Headless struct {
	Agent   *Agent
	Gate    ApprovalGate
	Resolve func(tc provider.ToolCall) string

	OnText     func(text string)          // streamed assistant tokens
	OnToolCall func(tc provider.ToolCall) // before a call runs or is resolved
	// OnToolResult is told each result as it is recorded. It carries the
	// whole ToolResult rather than the text alone because a round's auto
	// calls run concurrently: the call is how a front-end matches a result to
	// the row it opened, and the duration is measured around the call itself,
	// which is the only place it can still be measured once several are in
	// flight at once.
	OnToolResult func(r ToolResult)
	OnUsage      func(u *provider.Usage) // per-request usage, when reported

	// Steer, when set, is drained after each tool round: returned messages
	// join the conversation as user messages before the next stream request
	// and reset the round counter (steering semantics for headless runs).
	Steer func() []string

	// Hold, when set, is asked at each round tail whether the run should park
	// before it asks for the next round. It returns nil to go on, or a
	// channel the run waits on until whatever is holding it closes that
	// channel. It is a channel and not a boolean because the wait has to be
	// interruptible: a held run that is then killed must not sit in a poll
	// loop waiting for a release nobody is going to send.
	//
	// The seam exists for the child loop, where a supervisor holds a whole
	// fan-out at once. A `-p` run has no keyboard and nothing sets it.
	// See docs/capabilities/coding-agent.md#a-turn-can-be-held-between-rounds.
	Hold func() <-chan struct{}

	// OnClose, when set, is asked at the one moment a turn's work is
	// finished: the model has answered without calling a tool, its answer is
	// in the conversation, and nothing has been asked of it since. It
	// returns the text to hand back for another round, or "" to let the turn
	// end. It is the seam an unattended run's definition of done is made
	// executable through — the checks run here, and their verdict is the
	// text that comes back.
	//
	// It is a hook rather than a call after Run because the difference is
	// the whole point: a verdict read after the loop has returned can only
	// be reported, and one read here can still be answered.
	// See docs/capabilities/coding-agent.md#it-can-check-itself.
	OnClose func(final string) string

	// Summary, when set, takes periodic readings of the run and hands their
	// verdicts to the intervention policy — a steer for a run that has left
	// its instruction, an early check-in for one that has what it needs
	// (summaryrun.go). Nil takes no readings, and the interval check-in still
	// fires either way.
	Summary *SummaryRun

	// OnIntervene, when set, is told what an intervention was and why, so a
	// front-end can show it the way the chat transcript does. It may be nil.
	OnIntervene func(iv Intervention)

	// OnTree, when set, is told each time the run was told the tree moved
	// (tree.go), for the same reason. It may be nil.
	OnTree func(n TreeNotice)

	// OnSummary, when set, is told each reading that lands, before the
	// policy is offered it. Every reading arrives here and not only the ones
	// that go on to interrupt the turn, because a drift rate is a fraction
	// and this is its denominator.
	// See docs/capabilities/sessions-and-memory.md#observations-are-what-the-session-did.
	OnSummary func(v SummaryVerdict)

	// OnRetry, when set, is told before each wait the run puts itself on
	// after a request the provider never answered. An unattended run that
	// goes quiet for a minute and a hung one look identical from outside, so
	// every surface says which of the two this is.
	// See docs/capabilities/coding-agent.md#an-unattended-run-runs-the-same-loop.
	OnRetry func(n RetryNotice)

	// Compact, when set, is the window-recovery step this run takes at each
	// round boundary: old tool results elided first, and the conversation
	// replaced by a summary of itself where eliding cannot clear the line.
	// Nil leaves a long run to meet its window the way it always did, which
	// is at it.
	//
	// It is a field on the runner rather than something the loop reaches for
	// because the loop is passive: it is a step a driver installs, and a
	// driver that installs none gets a run that never asks for a summary.
	// See docs/capabilities/coding-agent.md#the-window-recovers-where-nobody-is-watching.
	Compact *Compactor

	// OnCompact, when set, is told what each recovery step did, so a
	// scripted run can say it on stderr and a child can say it on its lane.
	// It is told about a compaction that failed as well as one that worked:
	// a run heading for a window it could not recover is the one thing here
	// worth reading.
	OnCompact func(n CompactNotice)

	// retry is the bound across the whole turn, not across one request: three
	// rate limits in a row are three attempts and not three fresh chances
	// (retry.go).
	retry Backoff

	mu sync.Mutex
	// streamCancel aborts whatever the run is currently waiting on — the
	// in-flight stream, or the timer of a retry wait, which registers itself
	// here so Interrupt wakes it the same way it aborts a stream.
	streamCancel func()
	interrupted  bool
}

// SetRetryLimit bounds this run's stalls at the attempts a setting names;
// nil is a file that named none and keeps the built-in bound (retry.go).
func (h *Headless) SetRetryLimit(n *int) { h.retry.SetLimit(n) }

// Interrupt cancels the current turn from another goroutine: the in-flight
// stream is aborted and Run returns ErrInterrupted at the next checkpoint,
// after Agent.CancelTurn has kept the conversation well-formed. The Headless
// is reusable afterwards — a later Run starts a fresh turn.
func (h *Headless) Interrupt() {
	h.mu.Lock()
	h.interrupted = true
	if h.streamCancel != nil {
		h.streamCancel()
	}
	h.mu.Unlock()
}

// summaryTarget is the instruction a steer quotes back. It comes from the
// reading's own anchor, which was captured when the run started.
func (h *Headless) summaryTarget() string {
	if h.Summary == nil {
		return ""
	}
	return h.Summary.target
}

func (h *Headless) wasInterrupted() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.interrupted
}

// Run executes one user turn to completion and returns the final assistant
// text. The conversation (including tool results) accumulates on the Agent,
// so callers can inspect Messages() afterwards or run another turn.
func (h *Headless) Run(prompt string) (string, error) {
	h.mu.Lock()
	h.interrupted = false
	h.mu.Unlock()
	// Starting a turn ends whatever stall the last one was in, the same way
	// the session's own start does. A Headless is reused across a child's
	// turns, so a budget carried over from a turn interrupted mid-backoff
	// would hand the next, unrelated one whatever was left of three attempts.
	h.retry.Reset()

	// What moved since the run last looked comes before what is asked of it,
	// so the instruction is read against the tree as it is.
	h.deliverTree(true)
	h.Agent.StartTurn(prompt)
	for {
		// The window is recovered here and nowhere else. This is a round
		// boundary — no stream is open and no call is owed a result — and it
		// is the boundary in front of a request, which is the only one that
		// can keep a request from being the one that does not fit. A turn
		// resumed onto a conversation that was already full crosses the line
		// before its first round, so the check belongs ahead of the request
		// rather than behind the round.
		h.recoverContext()
		text, calls, err := h.streamOnce()
		if err != nil {
			notice, ok := h.retry.Next(err)
			if !ok {
				return "", err
			}
			notice.Partial = text
			if !h.waitToRetry(notice) {
				h.Agent.CancelTurn()
				return "", ErrInterrupted
			}
			// The request that failed never reached the conversation, so
			// asking again is the same question rather than a second one —
			// which is the whole of what an unattended run can do here. A
			// session offers to keep a reply that stopped halfway and let the
			// model carry on from its own last sentence, because deciding
			// whether half a sentence is worth having is a judgement, and the
			// loop is passive: it cannot make one, and here there is nobody
			// to ask.
			// See docs/architecture.md#one-agent-several-front-ends.
			continue
		}
		// A request the provider answered ends the stall, whatever the answer
		// was: the bound is on consecutive failures.
		h.retry.Reset()
		// What the agent believes it is doing is most of what a reading is.
		h.Summary.Recorder().Assistant(text)
		if h.wasInterrupted() {
			// A partial response without tool calls is safe to keep; tool calls
			// from an aborted stream are dropped whole so no assistant message
			// is ever owed results. CancelTurn fences off the run either way.
			if len(calls) == 0 && text != "" {
				h.Agent.Append(provider.Message{Role: provider.RoleAssistant, Content: text})
			}
			h.Agent.CancelTurn()
			return "", ErrInterrupted
		}
		if len(calls) == 0 {
			if text != "" {
				h.Agent.Append(provider.Message{Role: provider.RoleAssistant, Content: text})
			}
			// The answer is in the conversation before the close is asked
			// anything, so a hand-back reads as a reply to what was just
			// said rather than as an interruption of it.
			if fb := h.closeFeedback(text); fb != "" {
				h.Agent.Append(provider.Message{Role: provider.RoleUser, Content: fb})
				// The round counter is untouched: the turn goes on under
				// the ceiling it was already under, because a turn that
				// could not finish inside its budget must not be handed a
				// fresh one for having failed a check.
				continue
			}
			return text, nil
		}

		auto, _ := h.Agent.BeginToolRound(text, calls, h.Gate)
		// The round's auto calls go out together, through the same bounded
		// dispatcher the session uses. The prompt tells the model its
		// independent reads and searches can be asked for in one round; a
		// runner that then ran them one at a time made that advice a lie
		// wherever nobody was watching, which is every fan-out.
		for _, tc := range auto {
			h.notifyCall(tc)
		}
		results := h.Agent.ExecuteCalls(auto)
		for _, r := range results {
			h.notifyResult(r)
		}
		h.Agent.RecordAutoResults(results)

		// Gated calls stay one at a time: each is a decision, and in a run
		// with nobody in front of it the decision is policy's, which is
		// allowed to depend on what the calls before it did.
		for {
			tc, ok := h.Agent.NextApproval()
			if !ok {
				break
			}
			h.notifyCall(tc)
			start := time.Now()
			result := h.resolveGated(tc)
			h.Agent.ResolveApproval(result)
			h.notifyResult(ToolResult{Call: tc, Result: result, Duration: time.Since(start)})
		}

		if h.wasInterrupted() {
			h.Agent.CancelTurn()
			return "", ErrInterrupted
		}

		// Steering messages queued mid-turn join the conversation between tool
		// rounds; they count as fresh user input, so they also reset the round
		// counter (matching the TUI's injectSteering).
		if h.Steer != nil {
			if msgs := h.Steer(); len(msgs) > 0 {
				for _, msg := range msgs {
					h.Agent.Append(provider.Message{Role: provider.RoleUser, Content: msg})
				}
				h.Agent.ResetRounds()
			}
		}

		// Mirrors the TUI's resumeToolLoop: the cap is checked after a round's
		// results are recorded, before the next stream request. Headless has no
		// user to hand control back to, so hitting the cap is a failure — and
		// an uncapped agent (--max-rounds 0) never reaches it, which is why
		// that spelling is only offered to a foreground run someone can
		// interrupt.
		if h.Agent.CapReached() {
			return "", fmt.Errorf("%w after %d rounds", ErrRoundCap, h.Agent.Rounds())
		}

		// A hold parks the run here and nowhere else. The round's results
		// are in the conversation and nothing has been asked of the model
		// yet, so the wait holds no stream, owes no results and leaves the
		// conversation exactly as the round left it. An open stream cannot
		// be paused — a reader that stops reading backs the socket up until
		// the provider gives up on the request — which is why a hold waits
		// for the boundary rather than taking effect where it is asked for.
		if !h.waitOnHold() {
			h.Agent.CancelTurn()
			return "", ErrInterrupted
		}

		// The tree first, then the question: a check-in asked against a tree
		// the run has not been told about is answered against the wrong one.
		h.deliverTree(false)

		// The same take-stock check-in the TUI injects. A headless run needs
		// it more, not less: there is nobody here to ask a turn whether it has
		// enough yet, so the run itself has to ask.
		// A reading due now goes out in the background; whatever an earlier
		// one returned is offered to the policy here. The reading never
		// blocks the round, so a run is never slower for having one.
		if h.Summary != nil {
			h.Agent.SetInterveneCooldown(h.Summary.Cooldown())
			if v, ok := h.Summary.Tick(h.Agent.Rounds()); ok {
				if h.OnSummary != nil {
					h.OnSummary(v)
				}
				h.Agent.ConsiderVerdict(v, true)
			}
		}
		if iv, ok := h.Agent.NextIntervention(h.summaryTarget()); ok {
			h.Agent.Append(provider.Message{Role: provider.RoleUser, Content: iv.Message})
			if h.OnIntervene != nil {
				h.OnIntervene(iv)
			}
		}
	}
}

// recoverContext takes the run's window-recovery step and reports what it
// did. A step with nothing to do says nothing: the ordinary round boundary is
// one where the conversation is nowhere near its window.
func (h *Headless) recoverContext() {
	if h.Compact == nil || h.wasInterrupted() {
		return
	}
	n := h.Compact.Recover(h.Agent, h.askSummary)
	if n.Notice == "" || h.OnCompact == nil {
		return
	}
	h.OnCompact(n)
}

// askSummary opens one summary request and reads it back as prose. Like the
// retry and the hold it registers its cancel where the stream's goes, so an
// Interrupt aborts it rather than leaving a killed run waiting out a request
// nobody will read.
//
// Usage is reported the way a turn's own is: the request is a real request
// against a real budget, and a run whose spend jumped without a line to
// explain it is a bill nobody can account for. It teaches the estimator
// nothing, though, unlike a turn's — this request may have gone to another
// model, and a ratio measured on one tokenizer and applied to another is a
// correction that is confidently wrong.
func (h *Headless) askSummary(msgs []provider.Message, choice string) (string, error) {
	events, cancel, err := h.Compact.open(h.Agent, msgs, choice)
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	if h.interrupted {
		h.mu.Unlock()
		cancel()
		return "", ErrInterrupted
	}
	h.streamCancel = cancel
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.streamCancel = nil
		h.mu.Unlock()
		cancel()
	}()

	var text strings.Builder
	for ev := range events {
		if ev.Err != nil {
			if h.wasInterrupted() {
				// The abort we caused is not a compaction that failed, and
				// saying it was would leave a killed run's last word a
				// complaint about something nobody asked for.
				return "", ErrInterrupted
			}
			return "", ev.Err
		}
		text.WriteString(ev.Token)
		if ev.Usage != nil && h.OnUsage != nil {
			h.OnUsage(ev.Usage)
		}
		if len(ev.ToolCalls) > 0 {
			return "", errCompactToolCall
		}
		if ev.Done {
			break
		}
	}
	if h.wasInterrupted() {
		return "", ErrInterrupted
	}
	return text.String(), nil
}

// closeFeedback asks the close hook what the turn still owes. An interrupted
// run is asked nothing: the checks would be reporting on a tree the run was
// stopped halfway through changing, and the answer would be handed to a
// conversation that is about to be fenced off anyway.
func (h *Headless) closeFeedback(final string) string {
	if h.OnClose == nil || h.wasInterrupted() {
		return ""
	}
	return h.OnClose(final)
}

// deliverTree appends the tree notice this boundary owes, if any, and shows
// it. Mirrors the TUI's injectTreeNotice.
func (h *Headless) deliverTree(turnStart bool) {
	n, ok := h.Agent.NextTreeNotice(turnStart)
	if !ok {
		return
	}
	h.Agent.Append(provider.Message{Role: provider.RoleUser, Content: n.Message})
	if h.OnTree != nil {
		h.OnTree(n)
	}
}

// streamOnce opens one completion stream over the current conversation and
// consumes it, returning the assistant text and any tool calls that ended the
// stream. Like the TUI's terminalMsg, a tool-call event ends the stream.
func (h *Headless) streamOnce() (string, []provider.ToolCall, error) {
	events, cancel, err := h.Agent.Stream(h.Agent.RequestMessages())
	if err != nil {
		return "", nil, err
	}
	h.mu.Lock()
	if h.interrupted {
		h.mu.Unlock()
		cancel()
		return "", nil, nil
	}
	h.streamCancel = cancel
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.streamCancel = nil
		h.mu.Unlock()
		cancel()
	}()

	var text strings.Builder
	for ev := range events {
		if ev.Err != nil {
			if h.wasInterrupted() {
				// The abort we caused is not a real stream failure.
				return text.String(), nil, nil
			}
			// The words written before the wire broke come back with the
			// error. Nothing appends them to the conversation — a partial
			// reply is not an answer — but a caller that has already shown
			// them needs to know they were shown.
			return text.String(), nil, ev.Err
		}
		if ev.Token != "" {
			text.WriteString(ev.Token)
			if h.OnText != nil {
				h.OnText(ev.Token)
			}
		}
		if ev.Usage != nil {
			// What the prompt actually came to, measured against what this
			// run estimated for the same messages. It is folded in here
			// because here is the one moment the two describe the same
			// conversation: the response has not joined it yet.
			h.Compact.Observe(int64(ev.Usage.PromptTokens), h.Agent.Messages())
			if h.OnUsage != nil {
				h.OnUsage(ev.Usage)
			}
		}
		if len(ev.ToolCalls) > 0 {
			// The thinking behind the calls travels with them into the round
			// that records them.
			h.Agent.CarryReasoning(ev.Reasoning)
			return text.String(), ev.ToolCalls, nil
		}
		if ev.Done {
			// A response that asked for no tools has nowhere to carry its
			// thinking, and a latch left set would attach it to a later
			// round's calls.
			h.Agent.CarryReasoning(nil)
			break
		}
	}
	return text.String(), nil, nil
}

func (h *Headless) resolveGated(tc provider.ToolCall) string {
	if h.Resolve == nil {
		return "error: tool " + tc.Name + " cannot be approved in this session"
	}
	return h.Resolve(tc)
}

func (h *Headless) notifyCall(tc provider.ToolCall) {
	if h.OnToolCall != nil {
		h.OnToolCall(tc)
	}
}

func (h *Headless) notifyResult(r ToolResult) {
	// The digest is fed here rather than by the caller: every surface that
	// takes readings needs the same rows, and a hook a front-end has to
	// remember to wire is one that goes missing in the surface added next.
	// A nil Summary records nothing.
	h.Summary.Recorder().Tool(r.Call.Name, r.Call.Arguments, r.Result)
	if h.OnToolResult != nil {
		h.OnToolResult(r)
	}
}

// waitOnHold parks the run while a hold stands and reports whether it may go
// on. Like the retry wait it registers its cancel where the stream's goes, so
// an Interrupt wakes it rather than leaving a killed run parked on a release
// that is never coming.
func (h *Headless) waitOnHold() bool {
	if h.Hold == nil {
		return true
	}
	release := h.Hold()
	if release == nil {
		return true
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.mu.Lock()
	if h.interrupted {
		h.mu.Unlock()
		return false
	}
	h.streamCancel = cancel
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.streamCancel = nil
		h.mu.Unlock()
	}()
	select {
	case <-release:
	case <-ctx.Done():
	}
	return !h.wasInterrupted()
}

// waitToRetry sits out one wait and reports whether the run may go on. It is
// false when the run was interrupted while waiting, which is the one thing
// that can happen during it: a wait holds no stream, owes no results, and
// leaves the conversation exactly as the failed request found it.
func (h *Headless) waitToRetry(n RetryNotice) bool {
	if h.OnRetry != nil {
		h.OnRetry(n)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.mu.Lock()
	if h.interrupted {
		h.mu.Unlock()
		return false
	}
	h.streamCancel = cancel
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.streamCancel = nil
		h.mu.Unlock()
	}()
	timer := time.NewTimer(n.Wait)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
	return !h.wasInterrupted()
}
