package subagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/radius"
	"github.com/rfizzle/shhh/internal/safety"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/secret"
	wtree "github.com/rfizzle/shhh/internal/subagent/worktree"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
)

// AskKind selects the approval card a routed request renders with.
type AskKind int

const (
	AskCommand AskKind = iota
	AskEdit
	AskGeneric
	AskPatch
)

// Ask is one child approval request routed into the parent's approval flow:
// never silently parked, never auto-denied — the child blocks until the user
// answers (or the child is cancelled).
type Ask struct {
	// Agent is the child's name, shown as the card label.
	Agent    string
	Kind     AskKind
	Title    string
	Command  string      // AskCommand: the command text
	Warnings []string    // AskCommand / AskPatch: safety.Check risks, patch clashes
	Hunks    []diff.Hunk // AskEdit / AskPatch: the change to review
	Summary  string      // AskGeneric: one-line description
	Path     string      // AskEdit: the file, spelled as the child's own row spells it
	// Secret is the card's warning for the credential shapes an AskEdit adds
	// to its file (secret.AddedNote), empty where it adds none.
	Secret string

	// The rest is what the parent's approval card needs to say more about a
	// routed request than its title. A card that only says what the action
	// *is* asks the reader to do the risk assessment themselves, at speed,
	// and a child's request is the one where they have least to go on — so
	// the ask carries where its paths live, and the card resolves the blast
	// radius against that rather than against the parent's own checkout.

	// Root is the directory the ask's relative paths are measured from: the
	// child's own working directory for a call it is about to make, and the
	// parent's checkout for a patch, which is the tree a patch lands in.
	Root string
	// Worktree says Root is an isolated checkout rather than the reader's
	// own files. It is the difference between an approval that changes the
	// reader's workspace now and one that changes a copy they will be shown
	// as a patch afterwards, and nothing else on the ask carries it.
	Worktree bool
	// Merged is the files an AskPatch was merged over: the checkout moved
	// under the writer's patch in them since its copy was taken, and the
	// hunks are the merge against the checkout as it stands rather than the
	// writer's own diff. Empty for a patch that applies as written.
	// See docs/interface/surfaces.md#the-agent-manager.
	Merged []string
	// Regenerated is the generator commands an AskPatch's generated files
	// were written by: the patch touched files the project declares
	// generated, and the hunks for those are what the generators wrote over
	// the checkout with the rest of the patch in it, never the writer's own
	// bytes. Empty where the patch touched none.
	// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
	Regenerated []string
	// Reconciles is, on an integration writer's AskPatch, whose applied
	// change the patch settles in the files it was handed — "writer-1's
	// change to loop.go with writer-2's" — and empty everywhere else. Those
	// files carry no clash warning: overwriting both sides is the patch's
	// job, and the card states it as a fact rather than a risk.
	// See docs/capabilities/subagents.md#a-conflict-is-a-task-for-a-writer.
	Reconciles string
	// Files are the paths an AskPatch writes in the parent's checkout, as
	// git names them. They are the patch's blast radius: unlike an edit,
	// whose diff is the whole of it, a patch's diff can be longer than the
	// panel and the count is what survives the fold.
	Files []string

	// Tool and Arguments are the call this request is about, as the child
	// asked for it and already rooted at the child's own workspace. They are
	// empty on an AskPatch, which is the one request with no call behind it:
	// a writer's patch is its whole worktree against the checkout.
	//
	// The fields above are what a card is drawn from and these are not; they
	// are here for the surface that cannot draw one. A protocol client is
	// handed a request in the two fields it is handed every other gated call
	// in, and a client left to read the tool out of a title would be parsing
	// a sentence for something the request already knows
	// (docs/capabilities/headless.md#something-else-can-drive-it).
	Tool      string
	Arguments string

	// Judged is the auto-mode classifier's sentence on a call it would have
	// refused, routed to the person because the session has one
	// (Supervisor.SetAttended). The card draws it as the session's own card
	// draws its classifier's, and it is empty on every other request.
	// See docs/capabilities/subagents.md#a-child-answers-to-the-session.
	Judged string

	once sync.Once
	resp chan bool
}

// NewAsk builds an approval request; the supervisor uses it internally and
// front-end tests use it to drive the routing surface.
func NewAsk(agentName string, kind AskKind, title string) *Ask {
	return &Ask{Agent: agentName, Kind: kind, Title: title, resp: make(chan bool, 1)}
}

// Respond records the user's decision. Safe to call more than once; only the
// first decision counts.
func (a *Ask) Respond(approved bool) {
	a.once.Do(func() { a.resp <- approved })
}

// Answered non-blockingly consumes the recorded decision, for tests.
func (a *Ask) Answered() (approved, ok bool) {
	select {
	case v := <-a.resp:
		return v, true
	default:
		return false, false
	}
}

// refuseUncontained answers a command that must be contained where nothing
// can contain it, ahead of the policy, the classifier, the card and the
// surface's seams, as a session that requires containment answers its own:
// every answer the person could give ends in the same refusal, so there is no
// decision to put to them and none is filed. The refusal is the call's
// result, which is where the child can act on it. A call whose arguments do
// not parse is left to resolveGated, which says so.
// See docs/capabilities/containment.md#containment-can-be-required.
func (s *Supervisor) refuseUncontained(c *child, tc provider.ToolCall) (string, bool) {
	if tc.Name != tools.ExecCommandName || c.env.CommandRefusal == "" {
		return "", false
	}
	rooted, err := RootArgs(c.root, tc.Name, json.RawMessage(tc.Arguments))
	if err != nil {
		return "", false
	}
	action, err := actionFor(tc.Name, rooted)
	if err != nil {
		return "", false
	}
	title := askTitle(tc.Name, s.scopedAction(c, action))
	c.appendEntry(TranscriptEntry{Kind: EntrySystem,
		Text:   "Refused: " + title + " — nothing is containing this agent's commands",
		Result: c.env.CommandRefusal})
	return c.env.CommandRefusal, true
}

// resolveGated is the child's approval path: the child's clamped mode policy
// decides, and anything it would ask about routes to the parent user. Every
// verdict along the way is recorded at the codes a session records its own
// at — an approval rate that covered the parent and not its children would
// be a rate over the half of the work a person was looking at.
func (s *Supervisor) resolveGated(c *child, tc provider.ToolCall) string {
	raw := json.RawMessage(tc.Arguments)
	// A child's spawn naming a model the session cannot run is refused here,
	// ahead of the policy, the classifier and the card, as the session's own
	// is refused ahead of its queue: no answer the person could give makes
	// the name one the provider serves.
	// See docs/capabilities/subagents.md#the-model-is-offered-the-models-it-can-name.
	if tc.Name == SpawnToolName {
		if _, err := s.CheckModel(raw); err != nil {
			return "error: " + err.Error()
		}
	}
	rooted, err := RootArgs(c.root, tc.Name, raw)
	if err != nil {
		return "error: " + err.Error()
	}

	action, actionErr := actionFor(tc.Name, rooted)
	if actionErr != nil {
		return "error: " + actionErr.Error()
	}
	action = ruledAction(c, s.scopedAction(c, action), s.opts.ScopeDirs)
	title := askTitle(tc.Name, action)
	policy := s.childPolicy(c)
	decision, reason := policy.Decide(action)
	// The policy's reason is free text until it goes through ReasonCode,
	// which is where it stops being able to carry the path it names.
	record := func(d, code string) {
		if c.rec.Decision != nil {
			c.rec.Decision(c.pos(), d, code)
		}
	}
	// recordTook is record for a verdict the classifier reached, with the
	// time the judgement took.
	recordTook := func(d, code string, took time.Duration) {
		c.rec.Decided(c.pos(), d, code, took)
	}
	// The static policy denies for a command the user's deny list names,
	// which is refused whatever this child's mode is; in either read-only
	// mode, which refuses the call with the result that tells the model why
	// nothing ran, in the words of the mode that refused it; and for a path
	// no grant can reach, which says which path and why.
	if decision == agent.Deny {
		if agent.IsIrreplaceable(reason) {
			// Filed with the safety table's refusals, as the session files
			// it: it is that table's destroying rows, read against where
			// they point.
			record(observe.DecisionDeny, observe.ReasonSafety)
			c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: "Refused: " + title + " — " + reason})
			return agent.IrreplaceableResult(reason)
		}
		record(observe.DecisionDeny, observe.ReasonCode(reason))
		if reason == agent.DenyReasonDenylist {
			c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: "Refused: " + title + " — " + reason})
			return agent.DenylistResult
		}
		if strings.HasPrefix(reason, "outside the working scope") {
			c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: "Refused: " + title + " — " + reason})
			return agent.ScopeRefusedResult(reason)
		}
		refused := ""
		if action.Kind == agent.ActionCommand {
			refused = action.Command
		}
		return agent.ModeRefusedResult(reason, refused, policy.ReadOnlyExtra)
	}
	// A write that adds a credential shape is asked in every mode that would
	// have run it, as the session's own is: the denials above stand, but
	// nothing that would have run unasked does, and the classifier is not
	// asked to remove the prompt
	// (docs/capabilities/approvals-and-safety.md#a-write-that-adds-a-secret-is-always-asked).
	addsSecret, decision := secretAsks(tc.Name, rooted, action, decision)
	// Whether the classifier is what decided, because from here the rule has
	// a name of its own and a duration behind it — which the policy's reason
	// never has, and which the record's code can never carry.
	classified := false
	var cost time.Duration
	// standing is the reading's reason where the host's standing is what
	// turned the classifier's yes into a card, which the ask is filed under.
	var standing string
	// judged is the classifier's sentence where it said no and a person is
	// there to be asked instead, which the ask carries to the card.
	var judged string
	if decision == agent.Ask && !addsSecret {
		var denial string
		var byClassifier bool
		decision, cost, denial, byClassifier = s.classify(c, policy.Mode, tc, action)
		if decision == agent.Ask && byClassifier {
			judged = denial
		} else if decision == agent.Ask {
			standing = denial
		}
		classified = true
		if decision == agent.Deny {
			recordTook(observe.DecisionDeny, observe.ReasonClassifier, cost)
			c.appendEntry(TranscriptEntry{Kind: EntrySystem,
				Text: "Refused (" + classifierAccount(cost) + "): " + title + " — " + denial})
			return "error: auto mode denied this tool call: " + denial
		}
	}
	switch decision {
	case agent.Allow:
		code, rule := observe.ReasonCode(reason), reason
		if classified {
			code, rule = observe.ReasonClassifier, classifierRule
		}
		recordTook(observe.DecisionAllow, code, cost)
		// The account rides the act, in the field the call's own row keeps
		// for it. A refusal keeps its row because there is no act under it
		// to carry the reason; an approval has one.
		c.noteAllowed(tc.ID, rule, cost)
	case agent.Ask:
		// Two events, as a session records: what put the call in front of a
		// person, and what they said. The first is what a prompt-rate is
		// made of and the second is what an approval-rate is, and one event
		// carrying both could answer neither.
		//
		// A classifier's no is the first of the two in its own words, the
		// verdict it reached, which the session records the same way: the
		// person's answer after it is what says whether they agreed.
		askCode := observe.AskReason(action)
		if code := observe.HostReason(web.StandingOf(standing)); code != "" {
			askCode = code
		}
		if addsSecret {
			askCode = observe.ReasonSafety
		}
		if judged != "" {
			recordTook(observe.DecisionDeny, observe.ReasonClassifier, cost)
		} else {
			record(observe.DecisionAsk, askCode)
		}
		ask, askErr := s.buildAsk(c, tc.Name, rooted, action)
		if askErr != nil {
			// A child's refusals are rows in its own transcript, which the
			// parent mirrors when it attaches — so a file that moved under
			// the child reads exactly as one that moved under the session.
			var stale tools.StaleError
			if errors.As(askErr, &stale) {
				c.appendEntry(TranscriptEntry{
					Kind:   EntrySystem,
					Text:   stale.Skipped(wtree.DisplayPath(c.root, stale.Path)),
					Result: stale.Error(),
				})
			}
			return "error: " + askErr.Error()
		}
		ask.Judged = judged
		approved, ok := s.await(c, ask)
		if !ok {
			return agent.CancelledResult
		}
		if !approved {
			record(observe.DecisionDeny, observe.ReasonUser)
			if action.Kind == agent.ActionCommand {
				return "error: the user declined to run this command"
			}
			return "error: the user declined this tool call"
		}
		record(observe.DecisionAllow, observe.ReasonUser)
		// And the account rides the act, as the rule's does above: the
		// person who answered the card is why this call ran.
		c.noteApproved(tc.ID)
	}

	if tc.Name == tools.ExecCommandName {
		if c.env.RunCommand == nil {
			return "error: command execution is not available to this agent"
		}
		result := c.env.RunCommand(c.ctx, action.Command)
		// The runner has already scrubbed the output, so the reduction — and
		// the copy the evidence store keeps of it — is over the text the
		// child is allowed to see, as it is on the parent. A read a built-in
		// tool answers says so under it, once per tool in the child's turn,
		// the way it does for the session.
		// See docs/capabilities/coding-agent.md#the-built-in-tools-come-before-the-shell.
		return c.nudgeTurn().Append(c.pos().Turn, action.Command, c.env.execResult(result))
	}
	return agent.ExecuteWith(c.env.ExecuteGated, provider.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: string(rooted)})
}

// classify runs the auto-mode permission classifier for a call the
// static policy would have asked about, giving children the same treatment
// the parent gets. Anything other than auto mode, a missing classifier, or a
// safety-flagged action leaves the decision at Ask — the classifier can only
// ever remove a prompt it is allowed to remove, never add permission.
// It returns the decision, what the judgement took — the one rule whose cost
// the child's transcript states — and, for a denial, the reason the model is
// told; for an ask the host's standing forced, the standing's reason; and for
// an ask the classifier's own no became, its sentence, with judged set.
//
// That last is the session's person asked in place of a refusal, and it
// happens only where there is one (SetAttended). Everywhere else the no is
// refused here, as it always was: the only answerer on the other end of the
// route is a rule, and it would decline the call in somebody's name.
// See docs/capabilities/subagents.md#a-child-answers-to-the-session.
func (s *Supervisor) classify(c *child, mode agent.Mode, tc provider.ToolCall, action agent.Action) (decision agent.Decision, cost time.Duration, denial string, judged bool) {
	if mode != agent.ModeAuto || s.opts.Classifier == nil || action.SafetyFlagged {
		return agent.Ask, 0, "", false
	}
	// A reach outside the child's scope is the person's, whatever the
	// directory: nothing the classifier says widens what the child may write,
	// so its yes would be spent on a command the contained runner refuses
	// anyway, and the child would read that failure as an approved call.
	// See docs/capabilities/subagents.md#a-child-inherits-its-scope-not-more.
	if len(action.OutOfScope) > 0 {
		return agent.Ask, 0, "", false
	}
	req := agent.ClassifierRequest{
		Tool:      tc.Name,
		Arguments: tc.Arguments,
		CWD:       c.root,
		Recent:    c.agent.RequestMessages(),
		Reading:   action.Reading,
	}
	// What the role's commands are for rides a command and nothing else:
	// it is a statement about commands, and read against an edit or a
	// fetch it would narrow calls it never described. A checkout's profile
	// states none here (ClassifierScope).
	// See docs/capabilities/approvals-and-safety.md#a-profile-can-narrow-the-classifier-never-widen-it.
	if action.Kind == agent.ActionCommand {
		req.ProfileScope = c.profile.ClassifierScope()
	}
	v := s.opts.Classifier.Judge(c.ctx, req)
	// Classifier spend is the child's spend: it counts toward the child's
	// token budget, and exhausting it cancels the child like any other
	// overrun.
	if c.addUsage(&v.Usage) {
		c.cancel()
	}
	if c.rec.Usage != nil {
		// The turn count goes back with it: the totals are a whole-row
		// update, so reporting spend without it would blank the column the
		// last turn wrote.
		t := c.attemptSpend()
		c.rec.Usage(c.pos().Turn, t.In, t.Out, t.Cost, t.Priced)
	}
	verdict, reason := agent.ResolveAuto(action, v)
	switch {
	case verdict == agent.Allow:
		return agent.Allow, v.Elapsed, "", false
	case verdict == agent.Deny:
		return agent.Deny, v.Elapsed, reason, false
	case agent.JudgedDenialAsks(action, v):
		s.mu.Lock()
		attended := s.attended
		s.mu.Unlock()
		if !attended {
			return agent.Deny, v.Elapsed, reason, false
		}
		// The classifier said no and a person is there to answer instead;
		// the child's transcript says whose no it was.
		c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: "Asking the user: the classifier would refuse — " + reason + "."})
		return agent.Ask, v.Elapsed, reason, true
	case !v.Failed && web.StandingOf(reason) != "":
		// The classifier said yes and the host's standing put the call to
		// the person instead; the child's transcript says which list did.
		c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: "Asking the user: " + reason + "."})
		return agent.Ask, 0, reason, false
	case v.Failed:
		// Fails closed: the user decides, and sees why they were asked.
		c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: "Classifier unavailable (" + v.Reason + "); asking the user instead."})
	}
	return agent.Ask, 0, "", false
}

// classifierRule is what a child's transcript names as the rule when the
// auto-mode judge is what allowed a call. It is the word the session's own
// row uses for the same decision, so a parent mirroring a child reads the
// same account it reads in its own feed.
const classifierRule = "classifier"

// classifierAccount is the same rule written for a row that has no field to
// put the cost in: a refusal is its own notice, and the seconds the
// judgement took go in the notice's own parenthesis.
func classifierAccount(cost time.Duration) string {
	return fmt.Sprintf("%s, %.1fs", classifierRule, cost.Seconds())
}

// askTitle is the one-line description of a gated call for the child's own
// transcript.
func askTitle(name string, action agent.Action) string {
	if action.Kind == agent.ActionCommand {
		return "run " + firstLine(action.Command)
	}
	return "use " + name
}

// actionFor is a gated call as the child's mode policy reads it: the
// classifier's action, with the two things the child reads for itself. Every
// call that reaches it is one the child's own registration gated, so the
// child answers for every name it is asked about, and a name the classifier
// does not know is answered as the strictest kind there is.
// See docs/capabilities/approvals-and-safety.md#one-classifier-names-a-calls-tier.
func actionFor(name string, args json.RawMessage) (agent.Action, error) {
	call, err := agent.ClassifyCall(name, args, agent.Answers{Has: func(string) bool { return true }})
	if err != nil {
		return agent.Action{}, err
	}
	a := call.Action
	switch a.Kind {
	case agent.ActionCommand:
		// The child runs the line trimmed, and the trimmed line is what its
		// policy, its title and its runner all read.
		a.Command = strings.TrimSpace(a.Command)
	case agent.ActionFetch:
		// A child's fetch is decided on the same host the parent's card
		// would have named, so a granted host is as quiet in a child as it
		// is in the session that granted it.
		a.Host = web.FetchHost(args)
	}
	return a, nil
}

// scopedAction fills in what a child's command reaches outside the working
// scope: its own worktree plus the directories the person added to the
// parent session — the set its contained runner may write to. The parent's
// own checkout is not in it unless the person added a directory holding it,
// because a writer's work reaches the checkout through its patch and nothing
// else. A child's file edits never need this — RootArgs already refuses a
// path outside the worktree — so it applies to commands, which can name any
// path they like.
// See docs/capabilities/subagents.md#a-child-inherits-its-scope-not-more.
func (s *Supervisor) scopedAction(c *child, a agent.Action) agent.Action {
	if s.opts.ScopeDirs == nil || a.Kind != agent.ActionCommand || c.root == "" {
		return a
	}
	sc, _ := scope.New(c.root, s.opts.ScopeDirs()...)
	if sc == nil {
		return a
	}
	dirs := sc.Outside(childWritePaths(c.root, a.Command)...)
	if len(dirs) == 0 {
		return a
	}
	a.OutOfScope = dirs
	// The parent's checkout is the person's to open to a child, never a
	// permissive mode's or the classifier's: marking it sensitive is what
	// turns an auto-mode yes back into the parent's card.
	var checkout *scope.Scope
	if s.opts.Root != "" {
		checkout, _ = scope.New(s.opts.Root)
	}
	for _, d := range dirs {
		class, reason := scope.Classify(d)
		if class == scope.Ordinary && checkout.Contains(d) {
			class, reason = scope.Sensitive, parentCheckoutReason
		}
		switch class {
		case scope.Refused:
			a.ScopeRefused, a.ScopeReason = true, reason
		case scope.Sensitive:
			if !a.ScopeRefused {
				a.ScopeSensitive, a.ScopeReason = true, reason
			}
		}
	}
	return a
}

// ruledAction fills in what a child's command destroys that the child may
// not, read from the directory it runs in against the scope scopedAction
// reads — its own directory, plus what the person added to the session. A
// child is refused through the policy the session is, so the same command is
// refused the same way whichever agent proposed it.
// See docs/capabilities/approvals-and-safety.md#some-targets-are-never-destroyed.
func ruledAction(c *child, a agent.Action, added func() []string) agent.Action {
	if a.Kind != agent.ActionCommand || a.Command == "" {
		return a
	}
	where := radius.Where{Dir: c.root, Root: c.root}
	if c.root != "" {
		var dirs []string
		if added != nil {
			dirs = added()
		}
		where.Scope, _ = scope.New(c.root, dirs...)
	}
	where.Home, _ = os.UserHomeDir()
	a.Irreplaceable = radius.Destroys(a.Command, where).Refusal()
	return a
}

// parentCheckoutReason is why a child's command into the parent's own
// checkout is put to the person.
const parentCheckoutReason = "the parent session's own checkout, which a writer's work reaches through its patch"

// childWritePaths is what a child's command writes, measured from where the
// command runs rather than from where shhh runs. The resolver reads text, so
// a relative path means nothing until it is given a directory, and measured
// from shhh's own directory every file a writer creates in its copy would
// read as a write into the parent's checkout. A relative path is checked from
// the child's directory and from each directory the line changes into, so
// `cd <elsewhere> && touch f` answers for elsewhere; checking it from every
// one of them over-reads a line that changes back, which costs a card.
func childWritePaths(root, command string) []string {
	writes := radius.WritePaths(command)
	if len(writes) == 0 {
		return nil
	}
	bases := []string{root}
	base := root
	for _, cmd := range safety.Commands(command) {
		fields := strings.Fields(cmd)
		if len(fields) < 2 || (fields[0] != "cd" && fields[0] != "pushd") {
			continue
		}
		target := strings.Trim(fields[1], `'"`)
		if target == "" || target[0] == '-' || strings.ContainsAny(target, "$`") {
			continue
		}
		if !filepath.IsAbs(target) && !isHomePath(target) {
			target = filepath.Join(base, target)
		}
		base = target
		bases = append(bases, target)
	}
	var out []string
	for _, p := range writes {
		if filepath.IsAbs(p) || isHomePath(p) {
			out = append(out, p)
			continue
		}
		for _, b := range bases {
			out = append(out, filepath.Join(b, p))
		}
	}
	return out
}

// isHomePath is a path the scope expands against the home directory itself.
func isHomePath(p string) bool {
	return p == "~" || strings.HasPrefix(p, "~/")
}

// buildAsk assembles the approval request the parent user reviews, and stamps
// it with where the child's paths live. The stamp is applied in one place
// rather than per kind so a request added later cannot reach the parent's
// card with nothing behind its blast-radius block.
func (s *Supervisor) buildAsk(c *child, name string, rooted json.RawMessage, action agent.Action) (*Ask, error) {
	ask, err := askFor(c, name, rooted, action)
	if err != nil {
		return nil, err
	}
	ask.Root, ask.Worktree = c.root, c.worktree != ""
	ask.Tool, ask.Arguments = name, string(rooted)
	return ask, nil
}

// askFor is the request itself: the variant, and what only that variant knows.
func askFor(c *child, name string, rooted json.RawMessage, action agent.Action) (*Ask, error) {
	switch action.Kind {
	case agent.ActionCommand:
		ask := NewAsk(c.name, AskCommand, "run "+firstLine(action.Command))
		ask.Command = action.Command
		for _, w := range safety.Check(action.Command) {
			ask.Warnings = append(ask.Warnings, w.Risk)
		}
		return ask, nil
	case agent.ActionEdit:
		mut, err := tools.PreviewMutation(name, rooted)
		if err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		path := wtree.DisplayPath(c.root, mut.Path)
		ask := NewAsk(c.name, AskEdit, mut.Action+" "+path)
		ask.Path = path
		ask.Hunks = diff.Compute(mut.OldText, mut.NewText)
		ask.Secret = secret.AddedNote(mut.OldText, mut.NewText)
		return ask, nil
	}
	ask := NewAsk(c.name, AskGeneric, "use "+name)
	ask.Summary = compactArgs(rooted)
	return ask, nil
}

// secretAsks reports whether the call is a write that adds a credential
// shape, and the decision with a yes turned into an ask where it is.
func secretAsks(name string, rooted json.RawMessage, action agent.Action, decision agent.Decision) (bool, agent.Decision) {
	if action.Kind != agent.ActionEdit || editSecret(name, rooted) == "" {
		return false, decision
	}
	if decision == agent.Allow {
		decision = agent.Ask
	}
	return true, decision
}

// editSecret is the note the card would draw for the credential shapes this
// edit adds, empty where it adds none or the call does not preview (the ask
// reports that error itself).
func editSecret(name string, rooted json.RawMessage) string {
	mut, err := tools.PreviewMutation(name, rooted)
	if err != nil {
		return ""
	}
	return secret.AddedNote(mut.OldText, mut.NewText)
}

// await routes one ask to the parent and blocks the child until the user
// answers or the child is cancelled (killed, or its turn interrupted); ok is
// false on cancellation.
func (s *Supervisor) await(c *child, ask *Ask) (approved, ok bool) {
	c.set(StateBlocked, "waiting approval: "+ask.Title)
	s.emit(Event{Kind: EventAsk, Ask: ask, Status: c.status()})
	select {
	case v := <-ask.resp:
		c.set(StateRunning, "running")
		s.emitUpdate(c)
		return v, true
	case <-c.interruptCh():
		return false, false
	case <-c.ctx.Done():
		return false, false
	}
}
