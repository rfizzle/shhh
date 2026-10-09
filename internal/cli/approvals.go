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
// What tier a call sits at is the classifier's, so the line this draws is the
// one the session's card draws; what the run holds is its own. A fetch is held
// where a fetcher is, a child where a supervisor is, a server's tool unless its
// server was declared read-only, and the process tool where processes are
// managed — whose reading goes with it, since only a start needs an answer and
// status, read, input and stop do not. The command, the two file tools and
// git's writing half are held always.
// See docs/capabilities/approvals-and-safety.md#one-classifier-names-a-calls-tier.
func unattendedGate(webTools *web.Toolset, procSup *process.Supervisor, mcpTools *mcp.Toolset, sup *subagent.Supervisor) agent.ApprovalGate {
	holds := agent.Answers{Has: func(name string) bool {
		switch {
		case webTools != nil && name == web.FetchToolName:
			return true
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
		case name == ask.ToolName:
			return true
		// Starting a child is a gated call like any other, and the one this
		// surface used to answer by not offering it. It is gated rather than
		// dispatched because a child spends the session's budget on work
		// nobody reads until it reports, which is the decision --yes was
		// given to make and the card a client attached to a served session
		// draws (docs/capabilities/subagents.md#spawning-is-a-decision).
		case sup != nil && name == subagent.SpawnToolName:
			return true
		case procSup != nil && name == process.ToolName:
			return true
		case mcpTools != nil && mcpTools.Has(name):
			return !mcpTools.ReadOnly(name)
		}
		return headlessGate(name)
	}}
	if procSup != nil {
		holds.Command = process.CommandOf
	}
	return func(tc provider.ToolCall) bool {
		call, _ := agent.ClassifyCall(tc.Name, json.RawMessage(tc.Arguments), holds)
		return call.Gated
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
		ModelAt:   gateModel(cfg, env),
		Timeout:   time.Duration(cfg.Behavior.ClassifierTimeoutSeconds) * time.Second,
		MaxTokens: cfg.Behavior.ClassifierMaxTokens,
		Retries:   cfg.Behavior.ClassifierRetries,
		Prompt:    env.prompts.classifier,
		Backend:   cfg.Behavior.ClassifierBackend,
		Threshold: cfg.Behavior.ClassifierThreshold,
	})
}

// gateModel is the inexpensive model the gate reads with: what
// behavior.classifier_model names, and the bounded-call chain's answer where
// it names nothing, asked at each judgement so a model the session took for
// itself on the config screen is the one the next call is judged on.
//
// One resolution and not one per reader. Everything asked at the approval
// card is the same size of question about the same call, and a session where
// the verdict came from one model and the explanation from another would be
// two readings a person could not compare — which is exactly what the reader
// does with them, one under the other, in the moment before answering.
// See docs/capabilities/configuration.md#the-classifier-is-configured-once.
func gateModel(cfg config.Config, env *sessionEnv) func() string {
	return env.flowModelAt(cfg, flowClassifier)
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
// behavior.explainer_model is the one way to part the two models, and it is
// a person's choice: unset, the explanation reads with the classifier's
// model, for the reason gateModel gives.
//
// The words are the one-shot's, handed down rather than restated: what an
// explanation of a command says and how long it is was settled for `shhh cmd`
// and `[x]` there, and a second wording here would be the same rule in two
// places with one of them out of date. The long form is what a screen with
// nothing else on it is for.
func buildExplainer(cfg config.Config, env *sessionEnv, ledger *meter.Ledger) *agent.Explainer {
	return agent.NewExplainer(ledger.For(env.prov, meter.SourceExplanation), agent.ExplainConfig{
		ModelAt: env.flowModelAt(cfg, flowExplanation),
		Prompt:  prompt.BuildExplain(true),
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
	// allowHosts and denyHosts are the person's own host lists
	// (web.allow_hosts, web.deny_hosts). A fetch is put to the same policy a
	// session's is before the classifier is paid to think about it, so the
	// person's lists answer first and a host's standing answers where the
	// session's would: a known host goes through, and a host the lists warn
	// about is refused where the classifier would have let it through
	// (docs/capabilities/approvals-and-safety.md#a-host-is-read-against-the-world-before-it-is-judged).
	allowHosts, denyHosts []string
}

// newAutoJudge is auto mode's judge for an unattended run: the classifier,
// the run's own conversation as it stands at each call, the directory it
// runs in, and the person's two host lists from the configuration.
func newAutoJudge(ctx context.Context, cfg config.Config, classifier *agent.Classifier, recent func() []provider.Message, cwd string) *autoJudge {
	return &autoJudge{ctx: ctx, classifier: classifier, recent: recent, cwd: cwd,
		allowHosts: cfg.Web.AllowHosts, denyHosts: cfg.Web.DenyHosts}
}

// decide answers one gated call: the verdict, the sentence a refusal is
// stated with, and the code the record files it under — a refusal the
// classifier reached and one it never got to are different facts about a run.
//
// A nil judge is a run that was not put in auto mode, and its answer is the
// flat refusal such a run has always given.
func (j *autoJudge) decide(tc provider.ToolCall, action agent.Action) (agent.Decision, string, string) {
	decision, why, code, _ := j.judge(tc, action)
	return decision, why, code
}

// judge is decide with how long the classifier took, zero where it was not
// asked, for the row the verdict is recorded under.
func (j *autoJudge) judge(tc provider.ToolCall, action agent.Action) (agent.Decision, string, string, time.Duration) {
	if j == nil {
		return agent.Deny, "", observe.ReasonHeadlessDefault, 0
	}
	if action.Kind == agent.ActionFetch {
		policy := agent.ModePolicy{Mode: agent.ModeAuto, AllowHosts: j.allowHosts, DenyHosts: j.denyHosts}
		if decision, why := policy.Decide(action); decision != agent.Ask {
			return decision, why, observe.ReasonCode(why), 0
		}
	}
	var recent []provider.Message
	if j.recent != nil {
		recent = j.recent()
	}
	v := j.classifier.Judge(j.ctx, agent.ClassifierRequest{
		Tool: tc.Name, Arguments: tc.Arguments, CWD: j.cwd, Recent: recent, Reading: action.Reading,
	})
	code := observe.ReasonClassifier
	if v.Failed {
		code = observe.ReasonClassifierFailed
	}
	decision, reason := agent.ResolveUnattended(action, v)
	if host := observe.HostReason(web.StandingOf(reason)); host != "" && !v.Failed {
		code = host
	}
	return decision, reason, code, v.Elapsed
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
	// conversation is the host deny list of a run that registered nothing
	// that acts, and nil on every other run. A fetch there is answered the
	// way the conversation on screen answers it — the deny list, then a read
	// nobody is asked about — because a surface that asks nobody on screen
	// has no reason to refuse the same read with nobody watching, and --yes
	// is a yes to acts a conversation cannot make.
	// See docs/capabilities/chat.md#a-conversation-has-one-mode.
	conversation *agent.ModePolicy
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

// conversationReads is the policy an unattended conversation answers a fetch
// with, and nil for a run that is not one — a coding run's fetch is answered
// by its flags and its judge as it always was.
func conversationReads(conversation bool, denyHosts []string) *agent.ModePolicy {
	if !conversation {
		return nil
	}
	return &agent.ModePolicy{Conversation: true, DenyHosts: denyHosts}
}
