package eval

// A case with no model in it: one of the harness's own mechanisms put to a
// labelled table, with the model's part scripted.
//
// The other three shapes all measure a model. That is what they are for, and
// it is also why none of them can see the machinery around the model: a steer
// that never quotes the instruction, a compaction that drops the tool result
// the next round was about to read, a child whose report comes back without
// what it did, a gate that answers "pass" for a check that could not be
// started. Every one of those is a failure of the harness rather than of the
// model, every one of them is silent at every surface, and a suite that only
// ever asks a model cannot report any of them.
//
// So this shape scripts the model's part and measures what the harness does
// with it. The reading is stated rather than asked for; the summary a
// compaction is rebuilt from is written in the case file; a child replays a
// scripted stream. What is not scripted is everything this suite is about:
// the intervention policy, the trim and the rebuild, the spawn/report/patch
// loop, the gate's own verdict. They run exactly as they run in a session.
//
// Two things follow from there being no request. A scripted case costs
// nothing, which is what lets the suite have a committed baseline at all —
// these are the rows a run can measure without an account. And its answer is
// a fact rather than a judgement, so a row that misses is a defect and not a
// model having a bad day: the label is what the mechanism did, compared with
// what a person wrote down that it must do.
// See docs/capabilities/evals.md#a-scripted-case-measures-the-harness.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
)

// The four mechanisms, one kind apiece. They are separate kinds rather than
// one "mechanism" kind with a field, because a kind is what decides which
// words an answer may come back with, and a row that could expect "blocked"
// of a compaction would be a row nothing could ever satisfy.
const (
	// KindSteer puts a reading to the intervention policy and asks what the
	// turn would be told.
	KindSteer Kind = "steer"
	// KindCompaction fills a window, runs the recovery step over it, and
	// asks what the rebuilt conversation still holds.
	KindCompaction Kind = "compaction"
	// KindSpawn runs the spawn, report and patch loop against a scripted
	// child and asks what the parent was told about it.
	KindSpawn Kind = "spawn"
	// KindGate runs the quality gate over a workspace the row describes and
	// asks for its verdict.
	KindGate Kind = "gate"
)

// What a steer row's answer can be: the interruption the turn was given, or
// the reason it was given none.
const (
	// LabelSteered is a steer that arrived carrying what the row required of
	// its wording, and LabelUnquoted one that arrived without it. They are
	// two words rather than one and a note, because a steer that does not
	// quote the instruction is the failure this case exists for: the model
	// is told it may have drifted and never told from what.
	LabelSteered  = "steered"
	LabelUnquoted = "unquoted"
	// LabelEnough is the early check-in a sufficiency reading pulls forward.
	LabelEnough = "enough"
	// LabelStale is an interruption withheld because the reading describing
	// it was older than the run it was about.
	LabelStale = "stale"
	// LabelNone is a turn left alone.
	LabelNone = "none"
)

// What a compaction row's answer can be.
const (
	// LabelKept is a recovery step that ran and left everything the row said
	// the next round needs, and LabelLost one that ran and did not.
	LabelKept = "kept"
	LabelLost = "lost"
	// LabelUntouched is a conversation the step found nothing to do to.
	LabelUntouched = "untouched"
)

// What a spawn row's answer can be.
const (
	// LabelReported is a loop that came back carrying everything the row
	// required, and LabelIncomplete one that came back without some of it.
	LabelReported   = "reported"
	LabelIncomplete = "incomplete"
	// LabelBroken is the loop itself failing — no child, no report, an error
	// from the tool. It is separated from an incomplete report for the
	// reason an unanswered table row is separated from a wrong one: a
	// mechanism that did not run says nothing about what it reports.
	LabelBroken = "broken"
)

// What a gate row's answer can be. They are the gate's own verdict words, so
// a row is written in the vocabulary the runner answers in and nothing
// translates between the two.
const (
	LabelPass      = string(quality.VerdictPass)
	LabelFail      = string(quality.VerdictFail)
	LabelGateBlock = string(quality.VerdictBlocked)
	LabelCancelled = string(quality.VerdictCancelled)
)

// Scripted reports whether this kind measures the harness with the model's
// part written down, which is also what says the run needs no provider and
// no account.
func (k Kind) Scripted() bool {
	switch k {
	case KindSteer, KindCompaction, KindSpawn, KindGate:
		return true
	}
	return false
}

// scriptedLabels is the closed set each scripted kind answers in.
func scriptedLabels(k Kind) []string {
	switch k {
	case KindSteer:
		return []string{LabelSteered, LabelUnquoted, LabelEnough, LabelStale, LabelNone}
	case KindCompaction:
		return []string{LabelKept, LabelLost, LabelUntouched}
	case KindSpawn:
		return []string{LabelReported, LabelIncomplete, LabelBroken}
	case KindGate:
		return []string{LabelPass, LabelFail, LabelGateBlock, LabelCancelled}
	}
	return nil
}

// askScripted runs one row against the mechanism its kind names.
func askScripted(ctx context.Context, kind Kind, row Row) Answer {
	start := time.Now()
	var a Answer
	switch kind {
	case KindSteer:
		a = askSteer(row)
	case KindCompaction:
		a = askCompaction(row)
	case KindSpawn:
		a = askSpawn(ctx, row)
	case KindGate:
		a = askGate(ctx, row)
	default:
		a = Answer{Err: string(kind) + " is not a mechanism this suite scripts"}
	}
	// The row and the clock are set here rather than in each of them: what a
	// row answered is the same fact whichever mechanism answered it, and four
	// copies of it is four places for one of them to be forgotten.
	a.Row = row
	a.Elapsed = time.Since(start)
	return a
}

// missing is the first thing text does not carry, and "" when it carries
// them all. The first and not all of them: a row that missed is read beside
// the rule it was written for, and one missing string is enough to say which
// rule that was.
func missing(text string, needs []string) string {
	for _, need := range needs {
		if need = strings.TrimSpace(need); need != "" && !strings.Contains(text, need) {
			return need
		}
	}
	return ""
}

// summaryState is a reading as the case file spells it, in the same words a
// summary table's rows are labelled with. The default is where "unclear"
// lands, which is a reading a row may write and the one the policy is
// supposed to do nothing about; a word outside the four is refused at load,
// so nothing else ever arrives here.
func summaryState(label string) agent.SummaryState {
	switch label {
	case LabelOnTarget:
		return agent.SummaryOnTarget
	case LabelOffTarget:
		return agent.SummaryOffTarget
	case LabelSufficient:
		return agent.SummarySufficient
	}
	return agent.SummaryUncertain
}

// askSteer puts one reading to the policy that decides what a turn is told.
//
// The reading is scripted because the reading is the summarizer's, and a
// summary table already measures that. What this measures is everything
// after it: whether a departure earns an interruption, whether a reading too
// old for the run it describes is withheld, whether a reading that lands
// after the turn stopped is dropped — and, where a steer is delivered, that
// its wording carries the instruction the model is being asked to compare
// its work against. A steer that quotes nothing tells the model it may have
// drifted and never says from what.
func askSteer(row Row) Answer {
	a := agent.New(nil, nil)
	verdict := agent.SummaryVerdict{State: summaryState(row.State), Reason: row.Reason, Round: row.Round}
	rounds := max(row.Rounds, row.Round)
	if withheld := a.ConsiderVerdict(verdict, rounds, !row.Finished); withheld == agent.InterveneStale {
		return Answer{Label: LabelStale, Reason: "the reading described work the run had already left behind"}
	}
	iv, ok := a.NextIntervention(row.Instruction)
	if !ok {
		return Answer{Label: LabelNone, Reason: "the turn was left alone"}
	}
	switch iv.Kind {
	case agent.InterveneSteer:
		if gone := missing(iv.Message, row.needs(row.Instruction)); gone != "" {
			return Answer{Label: LabelUnquoted, Reason: "the steer did not carry " + quoteFragment(gone)}
		}
		return Answer{Label: LabelSteered, Reason: iv.Notice}
	case agent.InterveneEnough:
		return Answer{Label: LabelEnough, Reason: iv.Notice}
	}
	return Answer{Label: LabelNone, Reason: iv.Notice}
}

// askCompaction fills a window and asks what survived recovering it.
//
// The summary is scripted for the reason the reading above is: what a model
// writes into it is the model's, and this is about what the step does around
// it. A conversation is assembled from the row, the window is the row's, and
// the step runs the trim and the rebuild it runs in a session — then the
// rebuilt conversation is searched for the strings the row says the next
// round needs. A tool result the next round was about to read, elided or
// summarized away, is the failure worth catching: nothing reports it, the
// model simply carries on without what it had.
func askCompaction(row Row) Answer {
	a := agent.New(append([]provider.Message(nil), row.Conversation...), nil)
	c := &agent.Compactor{Window: row.Window, Model: "scripted"}
	summary := row.Summary
	notice := c.Recover(a, func([]provider.Message, string) (string, error) { return summary, nil })
	if notice.Err != nil {
		return Answer{Err: notice.Err.Error()}
	}
	if !notice.Compacted && notice.Elided == 0 {
		return Answer{Label: LabelUntouched,
			Reason: fmt.Sprintf("the conversation was at %d%% of the window, which is under the line", notice.BeforePct)}
	}
	var held strings.Builder
	for _, msg := range a.Messages() {
		held.WriteString(msg.Content)
		held.WriteString("\n")
	}
	if gone := missing(held.String(), row.needs()); gone != "" {
		return Answer{Label: LabelLost, Reason: "the rebuilt conversation no longer holds " + quoteFragment(gone)}
	}
	return Answer{Label: LabelKept, Reason: notice.Notice}
}

// askGate runs the quality gate over a workspace the row describes.
//
// The three states are the whole case. A gate that answers "pass" is vouching
// for the tree, and the two ways it can do that dishonestly are answering
// pass for a check that failed and answering pass for a check that never ran
// — a missing executable, a containment that could not be built. The runner
// separates blocked from fail for exactly that reason, and nothing until now
// held it to it.
func askGate(ctx context.Context, row Row) Answer {
	dir, err := os.MkdirTemp("", "shhh-eval-gate-")
	if err != nil {
		return Answer{Err: "cannot make a workspace: " + err.Error()}
	}
	defer func() { _ = os.RemoveAll(dir) }()

	if row.Config != "" {
		path := filepath.Join(dir, filepath.FromSlash(quality.ConfigRelPath))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return Answer{Err: "cannot write the gate config: " + err.Error()}
		}
		if err := os.WriteFile(path, []byte(row.Config), 0o644); err != nil {
			return Answer{Err: "cannot write the gate config: " + err.Error()}
		}
	}
	// The tree is a repository because a result is fingerprinted against one,
	// and a gate measured outside a checkout would be measured somewhere no
	// session ever runs.
	if err := initRepo(dir); err != nil {
		return Answer{Err: err.Error()}
	}

	runner := &quality.Runner{Workspace: dir}
	res, err := runner.Run(ctx, row.Suite)
	if err != nil {
		return Answer{Err: err.Error()}
	}
	reason := res.Reason
	if reason == "" {
		reason = fmt.Sprintf("%d of %d checks passed", passedChecks(res), len(res.Checks))
	}
	return Answer{Label: string(res.Verdict), Reason: reason}
}

func passedChecks(res *quality.Result) int {
	n := 0
	for _, c := range res.Checks {
		if c.OK() {
			n++
		}
	}
	return n
}

// quoteFragment is how a miss's reason names the thing that was not there.
// It is not strconv.Quote: what it quotes is a fragment of a case file, and
// escaping a newline in it would print the escape rather than say the
// fragment ran onto another line.
func quoteFragment(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i]) + "…"
	}
	return "\"" + s + "\""
}

// scriptedArgs is one JSON argument object for a scripted tool call, built
// here so a case file never holds JSON it would have to escape.
func scriptedArgs(fields map[string]any) json.RawMessage {
	data, err := json.Marshal(fields)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}
