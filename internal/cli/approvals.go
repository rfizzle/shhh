package cli

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
)

// unattendedGate is which of a run's calls are put to a decision rather than
// dispatched outright, for every surface that has no card to draw: the
// scripted run, and a session a client drives over the protocol.
//
// It is one function and not one per surface. This is the line between the
// tier that runs on its own and the tier that has to be answered for
// (docs/architecture.md#tiers-not-permissions), and a second copy of it
// agrees on the day it is written: the next tool that has to be answered for
// is added to whichever copy the author was looking at, and the other surface
// runs it unasked with nothing going red.
//
// Each of the three registrations is a gate of its own because each has its
// own answer. A fetch is an external action; a server call is gated unless
// the server was declared read-only; and the process tool gates on its
// arguments rather than its name, since only a start needs an answer and
// status, read, input and stop do not.
func unattendedGate(webTools *web.Toolset, procSup *process.Supervisor, mcpTools *mcp.Toolset, sup *subagent.Supervisor) agent.ApprovalGate {
	return func(tc provider.ToolCall) bool {
		if webTools != nil && tc.Name == web.FetchToolName {
			return true
		}
		// A question always stops the run, because a question that ran
		// without stopping would be a question nobody answered. It is here
		// and not among the answers below: every other entry decides which
		// *acts* have to be answered for, and a question is not an act, so
		// no flag, no allowlist and not the classifier may settle one
		// (docs/capabilities/coding-agent.md#the-model-can-ask).
		//
		// The entry never fires where nobody is there to answer. The tool is
		// registered only on a session that can put the card to a client, so
		// a scripted run, a child and a served session in auto mode never
		// call it — a tool the run can only be refused is worse than one it
		// never saw
		// (docs/capabilities/headless.md#everything-the-session-has-unless-somebody-has-to-answer).
		if tc.Name == ask.ToolName {
			return true
		}
		// Starting a child is a gated call like any other, and the one this
		// surface used to answer by not offering it. It is gated rather than
		// dispatched because a child spends the session's budget on work
		// nobody reads until it reports, which is the decision --yes was
		// given to make and the card a client attached to a served session
		// draws (docs/capabilities/subagents.md#spawning-is-a-decision).
		if sup != nil && tc.Name == subagent.SpawnToolName {
			return true
		}
		if procSup != nil && tc.Name == process.ToolName {
			return process.NeedsApproval(json.RawMessage(tc.Arguments))
		}
		if mcpTools != nil && mcpTools.Has(tc.Name) {
			return !mcpTools.ReadOnly(tc.Name)
		}
		return headlessGate(tc.Name)
	}
}

// buildClassifier is auto mode's permission classifier, wherever a surface
// needs one: the session's provider through the spend ledger,
// behavior.classifier_model over the provider's small model, and the
// wording a prompt file may have replaced.
//
// One builder and not one per surface. The classifier is the same judgement
// on every one of them — the interactive session, a scripted run in auto
// mode, a served session with no client — and three copies of its
// configuration would drift a timeout or a retry apart without anything
// failing.
// See docs/capabilities/configuration.md#the-classifier-is-configured-once.
func buildClassifier(cfg config.Config, env *sessionEnv, ledger *meter.Ledger) *agent.Classifier {
	return agent.NewClassifier(ledger.For(env.prov, meter.SourceClassifier), agent.ClassifierConfig{
		Model:     gateModel(cfg, env),
		Timeout:   time.Duration(cfg.Behavior.ClassifierTimeoutSeconds) * time.Second,
		MaxTokens: cfg.Behavior.ClassifierMaxTokens,
		Retries:   cfg.Behavior.ClassifierRetries,
		Prompt:    env.prompts.classifier,
	})
}

// gateModel is the inexpensive model the gate reads with: what
// behavior.classifier_model names, and the provider's small model where it
// names nothing.
//
// One resolution and not one per reader. Everything asked at the approval
// card is the same size of question about the same call, and a session where
// the verdict came from one model and the explanation from another would be
// two readings a person could not compare — which is exactly what the reader
// does with them, one under the other, in the moment before answering.
// See docs/capabilities/configuration.md#the-classifier-is-configured-once.
func gateModel(cfg config.Config, env *sessionEnv) string {
	return modelOr(cfg.Behavior.ClassifierModel, auxiliaryModel(env.provName, env.modelName))
}

// buildExplainer is the card's explanation: the same model the classifier
// reads with, billed under its own source so a keystroke that spends money is
// a line in /cost rather than an unattributed request
// (docs/architecture.md#spend-is-counted-at-the-provider).
//
// It is built wherever a card can be drawn, which is the interactive session
// and nothing else: the key is a person's, and a surface with nobody in front
// of it has nobody to press it.
//
// The model is shared with the classifier and the bound is not: the
// classifier's timeout is how long a blocked session may wait for a verdict,
// and this one is how long a person will look at a card that says "asking".
// A reader who lengthened the first did not ask for the second.
//
// The words are the one-shot's, handed down rather than restated: what an
// explanation of a command says and how long it is was settled for `shhh cmd`
// and `[x]` there, and a second wording here would be the same rule in two
// places with one of them out of date. The long form is what a screen with
// nothing else on it is for.
func buildExplainer(cfg config.Config, env *sessionEnv, ledger *meter.Ledger) *agent.Explainer {
	return agent.NewExplainer(ledger.For(env.prov, meter.SourceExplanation), agent.ExplainConfig{
		Model:  gateModel(cfg, env),
		Prompt: prompt.BuildExplain(true),
	})
}

// autoJudge is the answer an unattended run gives a gated call its flags did
// not answer: the classifier's verdict, resolved by ResolveUnattended so that
// every way of not reaching one ends in a refusal rather than in a prompt
// nobody would see.
//
// It carries the conversation rather than a snapshot of it, because the
// evidence a verdict is worth anything on is what the run has said and been
// told by the round the call was made in — a slice taken when the run started
// would judge the twentieth call on the first round's context.
type autoJudge struct {
	ctx        context.Context
	classifier *agent.Classifier
	recent     func() []provider.Message
	cwd        string
}

// decide answers one gated call: the verdict, the sentence a refusal is
// stated with, and the code the record files it under — a refusal the
// classifier reached and one it never got to are different facts about a run.
//
// A nil judge is a run that was not put in auto mode, and its answer is the
// flat refusal such a run has always given.
func (j *autoJudge) decide(tc provider.ToolCall, action agent.Action) (agent.Decision, string, string) {
	if j == nil {
		return agent.Deny, "", observe.ReasonHeadlessDefault
	}
	var recent []provider.Message
	if j.recent != nil {
		recent = j.recent()
	}
	v := j.classifier.Judge(j.ctx, agent.ClassifierRequest{
		Tool: tc.Name, Arguments: tc.Arguments, CWD: j.cwd, Recent: recent,
	})
	code := observe.ReasonClassifier
	if v.Failed {
		code = observe.ReasonClassifierFailed
	}
	decision, reason := agent.ResolveUnattended(action, v)
	return decision, reason, code
}

// unattended is what a run with nobody in front of it answers a gated call
// with beyond its flags: the supervisor a spawn is handed to, the judge a
// call the flags do not answer is put to, and the record a file modification
// is checked against. All three are nil on a run that was given none, which
// is the surface exactly as it was — a nil record being the process-wide one,
// which is what a run that is the only conversation in its process has.
type unattended struct {
	sup   *subagent.Supervisor
	judge *autoJudge
	// seen is the read record the run's own askers answer from.
	seen *tools.Recorder
	// at is where the run has got to, for the line a refusal leaves in the
	// diagnostic log. It is a function and not a position because the
	// approver is built once and asked on every round, and it is here rather
	// than a parameter of its own because this is already what the approver
	// knows about the run it is answering for rather than about the call.
	at func() observe.Pos
}

// pos is where the run is now, and the zero position for a surface that was
// given no way to say — a test, and any caller for which the answer would be
// a guess.
func (u unattended) pos() observe.Pos {
	if u.at == nil {
		return observe.Pos{}
	}
	return u.at()
}
