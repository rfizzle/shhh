package chat

// Compact activity feed (docs/interface/principles.md#one-grid): tool
// calls and commands render as one-line activity rows — glyph, action, key
// argument, outcome, counts, duration — never raw output blocks by default.
// Focus mode expands a row in place, /ui verbosity changes the default
// density, and a running command shows a live output tail in its row.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/receipt"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/caps"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/web"
)

// verbosity is a rung of the surface's one density ladder: how much the
// whole screen explains, set once by /ui verbosity and appearance.verbosity
// rather than decided surface by surface, because densities chosen one
// surface at a time add up to a first session that gets everything at once.
// What each rung draws is one table
// (docs/interface/principles.md#density-is-one-ladder), and a surface asks
// it through Model.density rather than comparing the setting itself. In the
// activity feed (docs/interface/surfaces.md#the-step) low draws each card as
// its header alone and no think row at all
// (docs/interface/surfaces.md#the-think-row), normal draws a card's header,
// body and footer, and high opens every card onto its calls, each with its
// bounded detail body.
type verbosity int

const (
	verbosityLow verbosity = iota
	verbosityNormal
	verbosityHigh
)

func (v verbosity) String() string {
	switch v {
	case verbosityLow:
		return "low"
	case verbosityHigh:
		return "high"
	}
	return "normal"
}

// density reports whether the setting draws what the rung draws: the ladder
// is ordered, so a surface that appears from normal up asks
// density(verbosityNormal), and one that only high draws asks
// density(verbosityHigh). It is the one question every surface asks of the
// setting, which is what keeps adding a rung to a surface one row of the
// principles table.
func (m Model) density(rung verbosity) bool { return m.verbosity >= rung }

// WithVerbosity sets the rung the session starts on (appearance.verbosity).
// A word the ladder does not have starts the session on normal rather than
// refusing it: the settings writer has already judged the word, so one that
// reaches here is a file edited by hand, and a session that will not start
// over a density is a worse answer than the default.
func (m Model) WithVerbosity(word string) Model {
	if v, err := parseVerbosity(strings.TrimSpace(word)); err == nil {
		m.verbosity = v
	}
	return m
}

func parseVerbosity(s string) (verbosity, error) {
	switch s {
	case "low":
		return verbosityLow, nil
	case "normal", "norm", "med", "medium":
		return verbosityNormal, nil
	case "high":
		return verbosityHigh, nil
	}
	return verbosityNormal, fmt.Errorf("unknown verbosity %q (low, normal, high)", s)
}

// RunFunc runs one command line and answers with how it ended. It is the
// typed result rather than an output and a status so that a command that
// never started reaches the row with the prerequisite it was missing
// (tools.ExecPrereq) instead of an exit code of -1 and the category composed
// into its text.
type RunFunc func(ctx context.Context, command string) tools.ExecResult

// TailFunc runs a command like the plain runner while reporting each
// completed output line, so the row can show a live tail. onLine may be
// called from other goroutines.
type TailFunc func(ctx context.Context, command string, onLine func(string)) tools.ExecResult

// WithTailRunner sets the tail-capable runner used for assistant commands and
// /run; without one, commands run with no live tail.
func (m Model) WithTailRunner(fn TailFunc) Model {
	m.tailRunFn = fn
	return m
}

// commandTail is the running command's last output line, shared between the
// runner goroutine and the render loop.
type commandTail struct {
	mu   sync.Mutex
	line string
}

func (t *commandTail) Set(line string) {
	t.mu.Lock()
	t.line = line
	t.mu.Unlock()
}

func (t *commandTail) Line() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.line
}

// pendingToolResult marks a mirrored child tool call that hasn't finished yet
// ; the activity row renders it as running.
const pendingToolResult = "running…"

// cancelledToolResult is the synthetic result left on a tool call abandoned
// when the turn was cancelled.
const cancelledToolResult = "cancelled by user"

// The deciders named on a denied row: your preference, or a rule.
const (
	decidedByYou  = "you"
	decidedByAuto = "auto"
)

// The two rules that allow a call without a mode having a name for it. Every
// other rule is the mode machine's own word for what it matched
// ("auto mode", "allowlist", "session grant"), which the row prints as it
// comes.
const (
	// batchRule is one [A] answering the decisions behind the one it was
	// pressed on. It says "batch" rather than "you" because the reader
	// answered a shape of call and not this call, which is the whole
	// difference between the two keys.
	batchRule = "batch"
	// classifierRule is the auto-mode judge, and the one rule whose
	// judgement costs seconds — which is why the row prints them beside it.
	classifierRule = "classifier"
)

// receiptOf is what the entry's call did, read the one way every front-end
// reads a call. The screen holds no table of tools: what a row states about
// one — its kind, verb, subject, how it came out and how much it found — is
// the receipt's, and the screen's part is how that is drawn.
// See docs/architecture.md#one-agent-several-front-ends.
func (m Model) receiptOf(e entry) receipt.Receipt {
	if e.kind == entryDiff && e.diff != nil {
		// An applied edit holds its change rather than its result, so its
		// receipt is read from the change: the file and the hunks, under the
		// tool that made it. A row filed before it kept the tool's name is
		// still a write.
		rc := receipt.Build(receipt.Call{Name: e.toolName, Path: e.diff.Path, Hunks: e.diff.Hunks})
		rc.Kind = receipt.KindWrite
		if rc.Subject == "" {
			rc.Subject = e.diff.Path
		}
		return rc
	}
	if e.kind == entryCommand {
		ended := e.commandResult
		if ended.Outcome == "" {
			ended = tools.InferExecResult(e.toolResult, e.exitCode)
		}
		return receipt.Build(receipt.Call{Args: e.text, Result: e.toolResult, Exec: &ended})
	}
	return m.callReceipt(e.toolName, e.toolArgs, e.toolResult)
}

// callReceipt is the receipt of one tool call, told what this session knows
// about the server the tool belongs to, if any.
func (m Model) callReceipt(name, args, result string, atts ...provider.Attachment) receipt.Receipt {
	served := m.mcp.Has != nil && m.mcp.Has(name)
	return receipt.Build(receipt.Call{Name: name, Args: args, Result: result,
		Served: served, ReadOnly: served && m.mcp.ReadOnly(name), Attachments: atts})
}

// activityKind is the glyph a kind of act draws with: ⚙ reads of every
// sort, $ commands, ✎ anything that persists, ◇ sub-agents, ⇄ a server call
// the user did not mark read-only. The glyph carries the mutation rail with
// it, so the kinds that draw alike agree on the rail.
func activityKind(k receipt.Kind) components.ActivityKind {
	switch k {
	case receipt.KindRun:
		return components.ActivityCommand
	case receipt.KindWrite:
		return components.ActivityEdit
	case receipt.KindSpawn:
		return components.ActivitySubagent
	case receipt.KindRemote:
		return components.ActivityRemote
	case receipt.KindReport:
		return components.ActivityReport
	case receipt.KindSummary:
		return components.ActivitySummary
	}
	return components.ActivityTool
}

// toolKind is the glyph of a call to the named tool, for a caller that has
// no result to read: the card a call is put to.
func (m Model) toolKind(name string) components.ActivityKind {
	return activityKind(m.callReceipt(name, "", "").Kind)
}

func formatDuration(d time.Duration) string {
	if d < 10*time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// activityDuration renders the row's duration field. Under 0.5s it is
// blank rather than 0.0s: a column of zeroes down the feed is noise.
func activityDuration(d time.Duration) string {
	if d < 500*time.Millisecond {
		return ""
	}
	return formatDuration(d)
}

// turnDuration renders the duration field of a row whose span is a whole turn
// rather than one call: the three recovery rows, and the round-limit pause
// above all, since a turn that spent 25 tool rounds is measured in minutes.
// Past a minute `252s` stops reading as a duration, so the field takes
// minutes and seconds — packed, because the grid gives duration six columns
// and FormatElapsed's `4m 12s` would fill them and touch the outcome beside
// it. It is the form docs/interface/surfaces.md#the-recovery-row draws on
// this row.
func turnDuration(d time.Duration) string {
	if d < time.Minute {
		return activityDuration(d)
	}
	if mins := int(d.Minutes()); mins < 100 {
		return fmt.Sprintf("%dm%02ds", mins, int(d.Seconds())%60)
	}
	// Longer than that and the seconds are not the interesting part anyway.
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// allowedLabel is the account of a gated call that ran without the reader
// being asked: the outcome word, the rule that answered, and what that rule
// cost where it cost anything — which is the classifier, whose seconds are
// the reader's. The cost follows the same floor a duration field does, so a
// judgement too quick to time says only who made it rather than `0.0s`; a
// real classifier call is a request to a provider and never lands there.
func allowedLabel(rule string, elapsed time.Duration) string {
	if rule == "" {
		return ""
	}
	return components.OutcomeBy(components.OutcomeAutoAllowed, ruleAccount(rule, elapsed))
}

// approvalAccount is the account a gated call's row carries: the rule that
// answered for the reader, or the reader who answered the card. One function
// because it is one question — how this act came to be allowed — asked of
// the one request that knows, and because a caller that reached for
// allowedLabel alone left the person's half of the answer unsaid.
func approvalAccount(req *approvalRequest) string {
	if req.autoRule != "" {
		return allowedLabel(req.autoRule, req.autoCost)
	}
	return components.ApprovedBy(decidedByYou)
}

// commandEnd is how a command ended where its exit code cannot say: the word
// from the outcome vocabulary, and the number that qualifies it. It is empty
// on every command that exited on its own, which is nearly all of them.
type commandEnd struct {
	// outcome is components.OutcomeStopped, OutcomeKilled or OutcomeTimedOut.
	outcome string
	// account is the number beside the word — `signal 9`, `30s` — and is
	// empty where there is none to name.
	account string
}

// commandEnding reads what ended a command off the two things that know. A
// negative code is the runner saying the command never exited (-N for signal
// N), and the context the command was run on says whether that was the
// reader's cancel, the ceiling arriving, or neither — which leaves a signal
// nobody in this session sent. Go collapses all three to -1 and a row that
// printed the number would report `exit -1` as though a program had returned
// it (docs/interface/principles.md#closed-vocabularies).
//
// The reader's cancel is read off ctx.Err rather than off the signal because
// the signal is the same one the ceiling sends: what differs is who asked,
// and only the context holds that.
func commandEnding(ctxErr error, code int, limit time.Duration) commandEnd {
	if code >= 0 {
		return commandEnd{}
	}
	switch {
	case errors.Is(ctxErr, context.DeadlineExceeded):
		return commandEnd{outcome: components.OutcomeTimedOut, account: turnDuration(limit)}
	case ctxErr != nil:
		return commandEnd{outcome: components.OutcomeStopped}
	case code < -1:
		return commandEnd{outcome: components.OutcomeKilled, account: components.SignalAccount(-code)}
	}
	// -1 is the runner's "no status and no signal to name": a command that
	// could not be spawned at all, or one the platform ended without saying
	// how. Something outside the session stopped it, which is the word, and
	// there is no number to put beside it.
	return commandEnd{outcome: components.OutcomeKilled}
}

// ruleAccount is the rule that answered and what its judgement cost, which is
// the account either way a rule can answer: it let the call run, or it
// blocked it. A denial states it in the same field for the same reason — the
// outcome column says what happened in a word from the closed vocabulary, and
// which rule said so is the account of that word.
func ruleAccount(rule string, elapsed time.Duration) string {
	if cost := activityDuration(elapsed); cost != "" {
		return rule + " " + cost
	}
	return rule
}

// detailLines is prose as a row's detail body shows it: wrapped to the
// body's width, which is the pane less the indent every detail body carries.
//
// Wrapping is not decoration here. A detail body clips what does not fit,
// which is right for the output of a program — a log line's information is at
// its head — and wrong for prose. A round's reasoning is one paragraph on one
// physical line hundreds of characters long, and a judgement's reason is a
// sentence; clipped, an opened row would show the head of it and an ellipsis
// where the fold promised the whole
// (docs/interface/principles.md#fold-never-hide).
//
// A width of zero is a caller reading a row's state rather than drawing it,
// and there is no body to build.
func (m Model) detailLines(text string, width int) []string {
	text = strings.TrimRight(text, "\n")
	if text == "" || width <= 0 {
		return nil
	}
	return strings.Split(m.wordWrap(text, max(width-components.GridDetailIndent, 1)), "\n")
}

// activityRowFor builds the compact row for a tool or command entry, as
// it renders outside any step that has been opened. Everything that only
// wants to read a row's state — what it is, whether it ran, whether it broke
// — asks through here, because none of those answers depend on how much of
// the output is showing.
func (m Model) activityRowFor(e entry) components.ActivityRow {
	return m.activityRowDetail(e, false, 0)
}

// activityRowDetail is the same row told whether the step around it has its
// detail open, and how wide the pane it is going into is. Collapsed rows
// never show output; focus-mode expansion opens the wider in-place window,
// with the whole result one more press away on the full screen
// (docs/interface/surfaces.md#the-activity-row); failed rows, an opened step
// and high verbosity show the bounded detail view; and low verbosity hides
// counts. Every bounded body counts what it swallowed.
//
// A row you opened yourself keeps its wider body inside an opened step: the
// step's answer is the default for its rows, never a ceiling on one you
// asked about by name.
//
// The width is only ever read to wrap a body that is prose, and a caller
// that is reading the row's state rather than drawing it passes zero. That
// is not a render at width zero: every other field is a fact about the entry
// and answers the same at any width, which is the whole reason one function
// serves both.
func (m Model) activityRowDetail(e entry, stepDetail bool, width int) components.ActivityRow {
	row := components.ActivityRow{
		Expanded:  e.expanded || stepDetail || m.density(verbosityHigh),
		MaxDetail: maxToolResultLines,
		Duration:  activityDuration(e.duration),
		Frame:     m.spinFrame,
	}
	if e.expanded {
		row.MaxDetail = maxExpandedResultLines
	}
	result := e.toolResult
	rc := m.receiptOf(e)
	row.Kind, row.Verb = activityKind(rc.Kind), rc.Verb
	if e.kind == entryCommand {
		row.Target = rc.Subject
		ended := rc.Ended
		switch {
		case ended.Outcome == tools.ExecDidNotStart:
			// Nothing ran, so the row says which prerequisite was missing in
			// the outcome field itself — a word, not only the del the row is
			// drawn in — and a dash where the duration would be. The
			// operating system's words and the one thing still possible are
			// the body, wrapped because the second half is a sentence
			// (docs/interface/principles.md#fold-never-hide).
			// See docs/capabilities/containment.md#a-command-that-never-started-names-what-it-needed.
			row.State = components.ActivityFailed
			row.Outcome = components.OutcomeDidNotStart
			if word := rc.Account; word != "" {
				row.Outcome += " · " + word
			}
			row.Duration = components.NoDuration
			if width > 0 {
				row.Detail = m.detailLines(result, width)
				result = ""
			}
		case ended.Outcome == tools.ExecDidNotComplete:
			// It ran and nobody could read how it ended. Not `stopped`,
			// which is the reader's own cancel, and not `killed`, which
			// would name a signal nobody saw.
			row.State = components.ActivityFailed
			row.Outcome = components.OutcomeDidNotComplete
		case e.end.outcome == components.OutcomeStopped:
			// The reader stopped it themselves, so the row is as quiet as
			// their refusal is: ⊘ and dim, not ✗ and del. Nothing broke —
			// they changed their mind while it was running
			// (docs/interface/principles.md#two-denials-are-not-one-denial).
			// The duration stays: unlike a call that was never put, this one
			// ran, and how long for is why they stopped it.
			row.State = components.ActivityDenied
			row.Outcome = components.OutcomeStopped
		case e.end.outcome != "":
			// The ceiling, or a signal from outside the session. Neither is
			// the reader's doing and neither did what it was asked to, so
			// both are breaks — with the number that qualifies the word in
			// the account field beside it.
			row.State = components.ActivityFailed
			row.Outcome, row.Allowed = e.end.outcome, e.end.account
		case e.exitCode != 0:
			row.State = components.ActivityFailed
			row.Outcome = components.OutcomeExit(e.exitCode)
		default:
			row.Outcome = components.OutcomeOK
		}
		// A `!!` run's output never joined the conversation, and the
		// outcome is where the row says so (bang.go).
		if e.localRun {
			row.Outcome += " · " + components.OutcomeLocal
		}
	} else {
		// A search's place goes dim behind its pattern, so the column reads
		// as one question asked somewhere
		// (docs/interface/principles.md#one-grid).
		row.Target, row.Scope = rc.Subject, rc.Scope
		switch {
		case e.skipped != "":
			// A call the queue refused before it could reach a card
			// (queue.go). It is the denied row's shape because that is what
			// happened — ⊘, everything dim, and a dash where the duration
			// would be — with `skipped` in the outcome and why in the
			// account beside it. The subject is the file the call named
			// (queue.go), which is the one thing that tells five refusals
			// apart.
			row.State = components.ActivityDenied
			row.Outcome = components.OutcomeSkipped
			row.Allowed = e.skipped
			row.Target, row.Scope = e.text, ""
			row.Duration = components.NoDuration
			// The sentence the model was given, whole, under the row rather
			// than clipped into the outcome field beside the reason
			// (docs/interface/principles.md#fold-never-hide). It is not
			// output — the call never ran — so nothing counts it, and it is
			// wrapped rather than clipped for the same reason a judged
			// denial's reason is: it is a sentence, and a body that clipped
			// it would be the fold hiding what it promised.
			row.Detail = m.detailLines(result, width)
			result = ""
		case e.deniedBy != "":
			// A refusal is not a failure: ⊘ and the decider's name say the
			// call never ran, and the duration field says so too.
			row.State = components.ActivityDenied
			row.ByRule = e.deniedBy != decidedByYou
			if row.ByRule {
				// A rule's no is its own word, in del, with the rule that
				// said it — and what that rule cost — in the account field
				// beside it, the way an allowed call names what allowed it
				// (docs/interface/principles.md#two-denials-are-not-one-denial).
				row.Outcome = components.OutcomeBlocked
				row.Allowed = ruleAccount(e.denyRule, e.duration)
				if e.denyWhy == "" {
					// A rule that only matched sends the reader to the
					// session's own answer for the longer one. A rule that
					// judged has already folded its sentence under this row,
					// so the offer would point away from where the answer is
					// — and it would cost the row the columns the rule's
					// name is in, which is the one field the outcome cannot
					// do without.
					row.Keys = "/permissions why"
				}
			} else {
				row.Outcome = components.OutcomeBy(components.OutcomeDenied, e.deniedBy)
			}
			// Nothing ran, whoever refused it: a judgement's seconds are the
			// account of the decision and never the duration of the act.
			row.Duration = components.NoDuration
			result = ""
			// What the reader said when they refused goes under the row,
			// whole, rather than into the outcome field beside their name:
			// the outcome is a closed vocabulary and a sentence clipped into
			// it would be a reason nobody could read
			// (docs/interface/principles.md#fold-never-hide). It is set here
			// rather than left to the body below because a denial has no
			// output to count and none of the readings that follow — the
			// error prefix, the receipt, the link — describe a sentence.
			//
			// A rule that judged rather than matched says why in the same
			// place, and the two never meet: one is the reader's sentence
			// and the other the judge's, and only one of them refused this
			// call. The judge's is wrapped because it is prose and the body
			// clips rather than wraps — a reason cut off at the pane is the
			// fold hiding what it promised
			// (docs/interface/principles.md#fold-never-hide).
			switch {
			case e.denyNote != "":
				row.Detail = strings.Split(e.denyNote, "\n")
			case e.denyWhy != "":
				row.Detail = m.detailLines(e.denyWhy, width)
			}
		case result == pendingToolResult:
			row.State = components.ActivityRunning
			row.Outcome = components.OutcomeRunning
			if outcome, counts, ok := m.fetchWaitFields(e.toolName, e.toolArgs); ok {
				row.Outcome, row.Counts = outcome, counts
			}
			// The row animates from the session's one frame, and only
			// while the loop that advances it is running: a call left pending
			// by a cancelled turn keeps the still `▸` rather than standing on
			// one braille frame, which would read as a hang.
			row.Spin = m.spinnerWanted()
			result = ""
		case result == cancelledToolResult:
			// Ctrl+C during a turn: you stopped it, so it reads as your
			// refusal rather than as a break.
			row.State = components.ActivityDenied
			row.Outcome = components.OutcomeBy(components.OutcomeDenied, decidedByYou)
			row.Duration = components.NoDuration
			result = ""
		case rc.Failed():
			row.State = components.ActivityFailed
			row.Outcome = rc.Outcome
		case rc.IsGitWrite():
			// The receipt is the outcome, the field the target clips for: the
			// sha is the one part of a commit nobody can reconstruct from
			// the call, and a row that kept it in a one-line body would make
			// the reader open the row to learn what landed.
			//
			// Anything the result says under the receipt — the hooks a
			// commit on an untrusted checkout did not run — opens the row
			// itself. It is a boundary of the act rather than output, so it
			// is stated where the reader already is rather than behind a
			// keystroke; a row wide enough to carry it in the outcome field
			// would be wider than eighty columns.
			_, rest, _ := strings.Cut(strings.TrimRight(result, "\n"), "\n")
			row.Outcome, result = rc.Outcome, strings.TrimSpace(rest)
			if result != "" {
				row.Expanded = true
			}
		case e.answerRule != "":
			// A question nobody was put says what answered it rather than
			// who. `answered` would name a decision the reader never made,
			// and the reader is owed the reason their session stopped
			// bringing them questions
			// (docs/capabilities/coding-agent.md#the-model-can-ask).
			row.Outcome = components.OutcomeBy(components.OutcomeSkipped, e.answerRule)
		case e.answered != "":
			// A question is a decision and not an act, so the row states how
			// it was answered rather than what came back: the answer itself
			// is the body, and the outcome column is where the reader looks
			// for what happened (docs/interface/surfaces.md#the-activity-row).
			row.Outcome = components.OutcomeBy(components.OutcomeAnswered, string(e.answered))
		case rc.IsReport():
			// The link is the outcome — the field the target clips for —
			// and the page is the body, so the row keeps nothing else.
			row.Outcome = rc.Outcome
			result = ""
		}
	}
	// What let the call run without the reader being asked, in the act's own
	// outcome field: the feed states an act once, so the approval of it is
	// part of the row rather than a notice above the row repeating its verb
	// and target (docs/interface/surfaces.md#the-activity-row).
	if row.Allowed == "" {
		row.Allowed = allowedLabel(e.allowedBy, e.allowElapsed)
	}
	// And where nothing allowed it but the reader, the row says so in the
	// same field, in the other of the two words the split has: `approved by
	// you`, after the counts, which is what a person's yes reads as where a
	// rule's reads as an account of itself
	// (docs/interface/principles.md#two-denials-are-not-one-denial). It is
	// stated on the act rather than left implicit in the absence of a rule,
	// because scrolling back past an edit a week later is exactly when the
	// reader cannot tell the calls they answered from the ones they were
	// never shown.
	if row.Allowed == "" && e.approvedBy != "" {
		row.Allowed = components.ApprovedBy(e.approvedBy)
	}
	// A line the reader wrote at the card is the same fact about the same
	// act — how it came to be the act it is — so it is stated in the same
	// field. The target is the line that ran either way, because the
	// transcript is the account of what ran on this machine
	// (docs/capabilities/approvals-and-safety.md#an-amended-command-is-a-new-command).
	// The two cannot both hold: a call nobody was asked about is not one
	// anybody amended.
	if e.amendedFrom != "" {
		row.Allowed = components.OutcomeBy(components.OutcomeAmended, decidedByYou)
	}
	if strings.TrimSpace(result) != "" {
		row.Detail = strings.Split(strings.TrimRight(result, "\n"), "\n")
		if !row.Failed() && m.density(verbosityNormal) {
			row.Counts = rc.Counts()
		}
	}
	if e.elided != nil {
		// The body is the window trim's placeholder now, so everything the
		// switch above just read off it describes the placeholder rather
		// than the call: a failed call would read as clean, a commit would
		// lose its receipt, and every row would report one line. What the
		// row says is what it said before the trim (context.go).
		row.State, row.Outcome = e.elided.state, e.elided.outcome
		row.Counts = ""
		if !row.Failed() && m.density(verbosityNormal) {
			row.Counts = e.elided.counts
		}
	}
	return row
}

// WithFetchWaits installs the fetcher's two answers about a paced fetch: how
// much of a host's refusal is still being sat out, and how to give those
// waits up. Without them a fetch row reads as running for as long as the
// wait lasts, which is the one thing a wait must not look like.
// See docs/capabilities/evidence.md#a-site-is-read-at-the-pace-it-answers.
func (m Model) WithFetchWaits(waiting func(host string) (time.Duration, bool), abandon func()) Model {
	m.fetchWaiting, m.abandonFetchWaits = waiting, abandon
	return m
}

// fetchWaitFields are the outcome and counts of an in-flight fetch whose
// host is being waited out: `waiting 8s · docs.rs asked`, dim, where a
// running row would otherwise say only that it is running. Two fields rather
// than one string because they are the row's two right-hand fields, which
// the grid joins and paints itself.
//
// The countdown needs no timer of its own: the row is redrawn on the frames
// the running spinner is already asking for, and it reads the remaining wait
// off the fetcher each time.
func (m Model) fetchWaitFields(toolName, toolArgs string) (outcome, counts string, ok bool) {
	if !receipt.IsFetch(toolName) || m.fetchWaiting == nil {
		return "", "", false
	}
	host := web.FetchHost(json.RawMessage(toolArgs))
	if host == "" {
		return "", "", false
	}
	left, waiting := m.fetchWaiting(host)
	if !waiting {
		return "", "", false
	}
	// "asked" is what the host did, not what shhh decided: the wait is the
	// site's own number, and the row says whose it is so the reader knows
	// the session is being polite rather than slow.
	return "waiting " + countdownText(left), host + " asked", true
}

// fetchWaitRow is the live row for the session's own in-flight fetch while
// the host it asked is being waited out. The parent's tool calls reach the
// transcript only once they finish — a child's mirrored call has a row
// already, and gets its countdown there — so without this a twenty-second
// wait would be twenty seconds of a session that had simply gone quiet.
func (m Model) fetchWaitRow(width int) (string, bool) {
	if m.pendingApproval == nil {
		return "", false
	}
	call := m.pendingApproval.call
	outcome, counts, ok := m.fetchWaitFields(call.Name, call.Arguments)
	if !ok {
		return "", false
	}
	rc := m.callReceipt(call.Name, call.Arguments, "")
	row := components.ActivityRow{
		Kind:    activityKind(rc.Kind),
		State:   components.ActivityRunning,
		Verb:    rc.Verb,
		Target:  rc.Subject,
		Outcome: outcome,
		Counts:  counts,
		Spin:    m.spinnerWanted(),
		Frame:   m.spinFrame,
	}
	return row.View(width), true
}

// runningCommandRow renders the in-flight command as a live activity row with
// its output tail; spinner ticks keep it re-rendering while the command runs.
func (m Model) runningCommandRow(width int) string {
	row := components.ActivityRow{
		Kind:    components.ActivityCommand,
		State:   components.ActivityRunning,
		Verb:    "run",
		Target:  firstLine(m.runningCommand),
		Outcome: components.OutcomeRunning,
		Spin:    m.spinnerWanted(),
		Frame:   m.spinFrame,
	}
	if !m.runStart.IsZero() {
		row.Duration = activityDuration(clock().Sub(m.runStart))
	}
	if m.runTail != nil {
		row.Tail = m.runTail.Line()
	}
	return row.View(width)
}

// uiCommand handles /ui: the surface's verbosity, mono conformance,
// terminal mouse reporting, desktop notifications, what the terminal's own
// window is called, and what the terminal itself can do.
func (m *Model) uiCommand(parts []string) string {
	if len(parts) == 1 {
		return fmt.Sprintf("verbosity: %s\ntheme: %s\nscreen ground: %s\nmonochrome: %s\nmouse reporting: %s\ndesktop notifications: %s\nsession titles: %s\nnext-step suggestions: %s\nwindow title: %s\nlayout: %s\nterminal: %s\n"+uiUsage, m.verbosity, m.themeStatus(), groundStatus(), monoStatus(), m.mouseStatus(), m.notifyStatus(), m.titleStatus(), m.suggestStatus(), m.windowStatus(), m.inspectorStatus(), terminalName(m.caps))
	}
	switch parts[1] {
	case "verbosity":
		if len(parts) == 2 {
			return fmt.Sprintf("verbosity: %s — how much every surface explains\nusage: /ui verbosity <low|normal|high> — low shows step headers only and drops think rows, normal folds read-only groups, high expands every row\nfor one step rather than all of them, /step opens the detail of the step in flight", m.verbosity)
		}
		if len(parts) != 3 {
			return "usage: /ui verbosity <low|normal|high>"
		}
		v, err := parseVerbosity(parts[2])
		if err != nil {
			return failed("ui", err.Error())
		}
		m.verbosity = v
		m.invalidateRenderCache()
		note := fmt.Sprintf("verbosity set to %s", v)
		if m.writeConfig == nil {
			return note + "\nthis session cannot write the config file, so it is for this session only"
		}
		if err := m.writeConfig("appearance.verbosity", v.String()); err != nil {
			return note + "\n" + failed("ui", "could not save it: "+err.Error())
		}
		return note + " · saved — new sessions start this way"
	case "theme":
		return m.themeCommand(parts)
	case "ground":
		return m.groundCommand(parts)
	case "mono":
		return m.monoCommand(parts)
	case "mouse":
		return m.mouseCommand(parts)
	case "notify":
		return m.notifyCommand(parts)
	case "title":
		return m.titleCommand(parts)
	case "suggest":
		return m.suggestCommand(parts)
	case "window":
		return m.windowCommand(parts)
	case "rail":
		return m.railCommand(parts)
	case "terminal":
		return m.terminalReport()
	}
	return uiUsage
}

// uiUsage is the one line naming everything /ui answers for. It is a constant
// because the bare readout and the unknown-subcommand reply are the same
// list, and a list written twice is a list that drifts.
const uiUsage = "usage: /ui verbosity <low|normal|high> · /ui theme <auto|dark|light|charm> · /ui ground <on|off> · /ui mono <on|off> · /ui mouse <on|off> · /ui notify <on|off> · /ui title <on|off> · /ui suggest <on|off> · /ui window <on|off> · /ui rail <auto|columns> · /ui terminal"

// terminalName is the one-line answer the bare /ui gives: what the terminal
// called itself when shhh asked. A terminal that was asked
// and did not name itself is not the same as one shhh never asked, and a
// readout that could not tell them apart would be the reason someone
// distrusts the rest of it.
func terminalName(t caps.Terminal) string {
	switch {
	case t.Name != "":
		return t.Name
	case !t.Asked:
		return "not asked"
	case t.Held != "":
		return "unnamed — " + t.Held
	}
	return "unnamed"
}

// terminalReport handles /ui terminal: what this terminal answered when shhh
// asked what it can do. It is a diagnostic, and the question
// it exists to answer is "why did that not happen here" — so a capability
// nobody asked about says so rather than reading as a no.
func (m Model) terminalReport() string {
	t := m.caps
	if !t.Asked {
		return "terminal: not asked — " + t.Held + "\nnothing was queried, so nothing here would be an answer"
	}
	if t.Dumb {
		return "terminal: dumb — TERM says so\nnothing was asked of it and nothing is sent to it: no title on the tab, no progress state beside it, no notification"
	}
	lines := []string{
		"terminal: " + terminalName(t),
		"inline images: " + imageSupport(t),
		"desktop notifications: " + pick(t.Notifications, "OSC 99", "no OSC 99 answer — OSC 777 is the blind fallback"),
		"focus events: " + pick(t.FocusEvents, "reported", "not reported"),
		// The progress state has no query to answer, the way OSC 777 has
		// none: it is written and either understood or ignored, and a
		// readout that listed it beside the answered capabilities would be
		// claiming an answer nobody gave (terminal.go).
		"progress indicator: sent blind — the sequence has no query, so silence here is not a no",
	}
	// The cell size is the terminal's pixels over the session's own columns
	// and rows, which is why it is measured here rather than kept there.
	if w, h := t.CellSize(m.width, m.height); w > 0 && h > 0 {
		lines = append(lines, fmt.Sprintf("cell size: %d×%d px", w, h))
	}
	if t.Held != "" {
		lines = append(lines, "graphics and name were not asked for: "+t.Held)
	}
	return strings.Join(lines, "\n")
}

// imageSupport names how a staged image is drawn here, or says
// why there is no name to give.
//
// It answers for what shhh spends rather than for what the terminal offered,
// which is the difference between a diagnostic and a list. Sixel is detected
// and deliberately not drawn (internal/ui/caps/graphics.go), so a terminal
// that offered only sixel is a terminal whose pictures are half-blocks — and
// the reader looking at this line is looking at it to find out which of those
// they are about to see.
func imageSupport(t caps.Terminal) string {
	switch {
	case t.Kitty:
		return "kitty graphics"
	case t.Sixel:
		return "sixel, which shhh does not draw — pictures here are half-blocks"
	case t.Held != "":
		return "not asked"
	}
	return "neither kitty graphics nor sixel — pictures here are half-blocks"
}

// pick is the two words a capability comes back as. It exists so the three
// rows above read as a table rather than as three if statements.
func pick(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}

// themeStatus describes which colour table the surfaces are drawing with. The
// auto answer says what the terminal reported as well as which table it chose,
// because those are two facts and the one worth reading is usually the first:
// a reader looking at a light theme they did not ask for is looking for the
// terminal that asked for it.
func (m Model) themeStatus() string {
	name := components.ThemeName()
	if name != components.ThemeAuto {
		return name
	}
	dark, answered := m.caps.DarkGround()
	switch {
	case !answered:
		return "auto — this terminal has not said what its background is, so the dark table stands"
	case dark:
		return "auto — dark, which is what this terminal reports its background as"
	}
	return "auto — light, which is what this terminal reports its background as"
}

// themeCommand handles /ui theme: which of the shipped tables every surface
// draws with. Mono outranks it, so a theme asked for under mono says what will
// happen rather than pretending nothing did
// (docs/interface/principles.md#a-colour-is-three-values-and-a-ground).
func (m *Model) themeCommand(parts []string) string {
	if len(parts) == 2 {
		return fmt.Sprintf("theme: %s\n%s", m.themeStatus(), themeUsage)
	}
	if len(parts) != 3 {
		return themeUsage
	}
	if err := components.SetTheme(parts[2]); err != nil {
		return failed("theme", err.Error())
	}
	m.invalidateRenderCache()
	note := fmt.Sprintf("theme: %s", m.themeStatus())
	if components.Mono() {
		note += " · monochrome is on, so it takes effect when that goes off"
	}
	if m.writeConfig == nil {
		return note + "\nthis session cannot write the config file, so it is for this session only"
	}
	if err := m.writeConfig("appearance.theme", parts[2]); err != nil {
		return note + "\n" + failed("theme", "could not save it: "+err.Error())
	}
	return note + " · saved — new sessions start this way"
}

// themeUsage is the one line /ui theme answers with, built from the tables
// that ship rather than from a list written twice.
var themeUsage = "usage: /ui theme <" + strings.Join(components.ThemeNames(), "|") +
	"> — auto takes the table chosen for the background this terminal reports; the others name one"

// groundStatus describes what the screen behind the surfaces is painted with.
func groundStatus() string {
	if components.GroundPainted() {
		return "the theme's own"
	}
	return "the terminal's own"
}

// groundCommand handles /ui ground: whether the theme repaints the whole
// screen with the background it was chosen against. Off is the default and
// the reason is the reader's, not the palette's — their terminal's background
// is what every other program on that screen sits on.
func (m *Model) groundCommand(parts []string) string {
	if len(parts) == 2 {
		return fmt.Sprintf("screen ground: %s\n%s", groundStatus(), groundUsage)
	}
	if len(parts) != 3 {
		return groundUsage
	}
	on, ok := parseToggle(parts[2])
	if !ok {
		return failed("ui", fmt.Sprintf("unknown ground setting %q (on, off)", parts[2]))
	}
	if !components.PaintGround(on) {
		return fmt.Sprintf("screen ground already %s", groundStatus())
	}
	m.invalidateRenderCache()
	if on {
		return "screen ground: the theme's own — shhh paints the background it was drawn against, for this session"
	}
	return "screen ground: the terminal's own — shhh paints no background, which is where it starts"
}

const groundUsage = "usage: /ui ground <on|off> — on paints the whole screen with the background the theme was chosen against; off leaves the terminal's own. It lasts for this session"

// monoStatus describes the current monochrome state, naming the environment
// when it is what turned mono on.
func monoStatus() string {
	switch {
	case components.MonoForced():
		return "on (NO_COLOR)"
	case components.Mono():
		return "on"
	}
	return "off"
}

// monoCommand handles /ui mono: strip every surface to the two greys of the
// first invariant
// (docs/interface/principles.md#colour-never-carries-meaning-alone), so that
// a state distinguished only by colour becomes visibly wrong.
// NO_COLOR and TERM=dumb turn it on for the whole session and it cannot be
// turned back off from inside — the environment asked, not the user.
func (m *Model) monoCommand(parts []string) string {
	if len(parts) == 2 {
		return fmt.Sprintf("monochrome: %s\nusage: /ui mono <on|off> — strips every surface to two greys; glyphs, words and layout carry the states", monoStatus())
	}
	if len(parts) != 3 {
		return "usage: /ui mono <on|off>"
	}
	on, ok := parseToggle(parts[2])
	if !ok {
		return failed("ui", fmt.Sprintf("unknown mono setting %q (on, off)", parts[2]))
	}
	if !on && components.MonoForced() {
		return "monochrome is on because NO_COLOR is set in this environment; it cannot be turned off from here"
	}
	if on == components.Mono() {
		return fmt.Sprintf("monochrome already %s", monoStatus())
	}
	components.SetMono(on)
	m.invalidateRenderCache()
	if on {
		return "monochrome on — every surface renders in two greys"
	}
	return "monochrome off — the full palette is back"
}

// The compose row
// (docs/capabilities/coding-agent.md#a-long-call-is-counted-while-it-is-written):
// how much of a tool call the model has written, while it is writing it.
//
// A round that ends in a large edit spends most of itself inside one JSON
// blob, and nothing about the call is true until the blob closes — no target,
// no outcome, no duration — so the transcript's last act stays whatever the
// model said before it started, for as long as the write takes. The fragments
// the stream reports (internal/provider) are counted here and stated as a
// size on one row.
//
// It is a reading of the round in flight and not an entry in the history: it
// is drawn under the answer as it arrives, the way the answer itself is drawn
// before it becomes an entry, and it goes when the round's calls land. What
// replaces it is those calls' own rows, which say what was written and what
// came of it — a compose row left behind them would be a second row about one
// act, and the grid gives an act one row
// (docs/interface/principles.md#one-grid).
//
// It states a size and never a target, because a fragment carries the call's
// id and its bytes: the tool's name arrives with the finished call, which is
// the first moment it is true.

// composeVerb is the row's verb, closed like every other
// (docs/interface/principles.md#closed-vocabularies).
const composeVerb = "compose"

// composeMark is the row's mark: the round's own work in flight, the glyph
// the model's reasoning wore when it was a row.
const composeMark = "✻"

// composeFloor is how much a round has to have written before the row is
// drawn at all. It is measured against the calls not worth watching: a read,
// a search or a glob is a path and a pattern, a couple of hundred bytes at
// the outside, while a file being written or a batch of edits runs to
// kilobytes. A kilobyte sits clear of the first group and well under the
// second, so the row appears for the calls a reader is waiting on and for no
// others.
const composeFloor = 1 << 10

// toolDeltaMsg is one fragment of a tool call's arguments, off the stream.
type toolDeltaMsg struct{ delta provider.ToolCallDelta }

// repaintsOnTick reports whether a token batch that ended on this message can
// leave its repaint to the spinner's tick. Nothing terminal can — a round
// that has ended has to draw what it ended with — but a fragment is not the
// end of anything, and the row it feeds restates a size that only moves every
// kilobyte. A stream that sends its arguments in twenty-byte chunks would
// otherwise re-render the transcript twenty bytes at a time, which is the
// cost this row exists to make visible rather than to pay.
func repaintsOnTick(final tea.Msg) bool {
	if final == nil {
		return true
	}
	_, ok := final.(toolDeltaMsg)
	return ok
}

// appendCompose counts an arriving fragment into the round's total. The
// reading is the round's and not the call's, the way the think row is
// (think.go): a round writing three calls at once is one wait to the person
// watching it, and three numbers counting interleaved fragments are three
// numbers nobody can add up.
func (m *Model) appendCompose(d provider.ToolCallDelta) {
	if m.compacting {
		// A compaction is housekeeping, not a turn, and it calls no tools; a
		// reading of one would be a reading of something that never happened
		// (context.go).
		return
	}
	m.composed += len(d.Arguments)
}

// showCompose reports whether the compose row is drawn at all. Low verbosity
// is step headers only, and this row reports no act either — the act it was
// counting is the row that lands in its place a moment later.
func (m Model) showCompose() bool { return m.density(verbosityNormal) }

// composeRowLine is the round's compose row as its rendered line, and nothing
// where the row has not earned its place: no round in flight, nothing written
// worth waiting for, or a verbosity that draws no such row. prev is whatever
// the transcript rendered last, which is what decides the spacing above it.
//
// The row does not spin. What moves on it is the count, which is the reading
// itself; the frame's own status line is where the turn says it is running.
func (m Model) composeRowLine(width int, prev entry, havePrev bool) string {
	if m.events == nil || m.composed < composeFloor || !m.showCompose() {
		return ""
	}
	// Flat and dim at the glyph column, on no band: it is a reading of the
	// round and not a step, so it is drawn the way a notice is, `✻` in the
	// slot (docs/interface/surfaces.md#the-step).
	row := components.NoticeLine{Mark: composeMark, Text: composeVerb + " " + attachment.HumanSize(m.composed)}
	// The spacing above the row is the transcript's own, so it is asked for
	// rather than chosen here. The answer arriving is the one thing above it
	// whose last line is still open — every finished entry ends its own — so
	// that case closes the line first and the separator follows.
	lead := ""
	switch {
	case m.answerIsArriving():
		lead = "\n" + separatorBefore(entry{kind: entryAssistant}, entry{kind: entryTool})
	case havePrev:
		lead = separatorBefore(prev, entry{kind: entryTool})
	}
	return lead + row.View(width) + "\n"
}
