package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/radius"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/safety"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/stdin"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/rfizzle/shhh/internal/web"
	"github.com/spf13/cobra"
)

// The shapes an unattended run writes its work in. text is the default and
// the oldest: the answer on stdout as it is written, the activity on stderr.
// json is the whole transcript once the run is over, and jsonl is one event
// per line while it happens, in the record's own vocabulary.
// See docs/capabilities/headless.md#three-shapes-for-the-same-run.
const (
	outputText  = "text"
	outputJSON  = "json"
	outputJSONL = "jsonl"
)

// resolveOutput settles what the run will write from the two spellings that
// say it. `--json` is the older one and stays an alias for `--output json`:
// scripts were written against it, and it means today exactly what it meant.
//
// Naming both and disagreeing is a usage error rather than a silent winner,
// because either answer would be somebody's script quietly reading the wrong
// stream.
func resolveOutput(named string, jsonAlias bool) (string, error) {
	switch named {
	case "":
		if jsonAlias {
			return outputJSON, nil
		}
		return outputText, nil
	case outputText, outputJSON, outputJSONL:
		if jsonAlias && named != outputJSON {
			return "", fmt.Errorf("--json is --output %s and this run also asked for --output %s: pass one of them", outputJSON, named)
		}
		return named, nil
	}
	return "", fmt.Errorf("--output %q: one of %s, %s or %s", named, outputText, outputJSON, outputJSONL)
}

// The closed set of exit codes an unattended run leaves behind. They are a
// contract a script is written against: a code means one thing and goes on
// meaning it, so a round cap, an interrupt and a provider outage are three
// different facts to whatever called shhh.
//
// 1 is deliberately not among them. It is what every command exits with when
// it could not run at all — a flag that will not parse, a config that will not
// load, a provider that cannot be resolved — and a run that never started is
// a different fact from a turn that ended badly.
// See docs/capabilities/headless.md#the-exit-code-is-the-contract.
const (
	exitDone        = 0
	exitRoundCap    = 2
	exitInterrupted = 3
	exitProvider    = 4
	exitGate        = 5
	exitRefused     = 6
)

// errHeadlessRefused is the exit-6 run stated in words, for the stderr line
// and the JSON error field. A status on its own says a call was refused; this
// says what to do about it.
var errHeadlessRefused = errors.New("a tool call was refused: this run denies edits, commands and external actions unless --yes or --allow says otherwise")

// headlessExitCode projects the exit code from the outcome the turn was
// recorded under, which is what keeps the two from ever disagreeing: the
// record's column and the process's status are read off one value.
//
// Two readings sit on top of it and both belong to a turn that finished — a
// suite that failed after the model had stopped, and a call the policy
// refused as the last word before the turn ended. Neither is a turn that
// broke, which is why neither has an outcome of its own to be projected from.
//
// The gate is taken first of the two. It is a verdict about the tree as it
// now stands, which is the more actionable of the two facts, and a refusal
// that mattered usually leaves nothing behind for a suite to have an opinion
// about.
func headlessExitCode(outcome string, gateFailed, refused bool) int {
	switch outcome {
	case observe.TurnCapPaused:
		return exitRoundCap
	case observe.TurnCancelled:
		return exitInterrupted
	case observe.TurnFailed:
		return exitProvider
	}
	switch {
	case gateFailed:
		return exitGate
	case refused:
		return exitRefused
	}
	return exitDone
}

// exitError carries one of those codes out through cobra to the process,
// which is the only way a code can survive the return path: the command tree
// returns an error, the dressing prints it, and main is where the process
// exits (root.go).
type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string { return e.err.Error() }
func (e exitError) Unwrap() error { return e.err }

// lastVerdict is the policy's most recent answer, which is what says whether
// a refusal is why the turn ended.
//
// The last one and not any one. A run that was denied a command, found
// another way and finished was not refused — it did the work — and reporting
// it as refused would teach a script to ignore the code. A denial that is
// still standing when the model stops is the one that ended the turn.
type lastVerdict struct {
	mu   sync.Mutex
	code string
}

// wrap is the reporter the approver is handed: every verdict reaches the
// record through next and is remembered here on its way past, so there is no
// second place a decision has to be reported to and could be forgotten.
func (l *lastVerdict) wrap(next func(decision, reason string)) func(string, string) {
	return func(decision, reason string) {
		l.mu.Lock()
		l.code = decision
		l.mu.Unlock()
		next(decision, reason)
	}
}

func (l *lastVerdict) refused() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.code == observe.DecisionDeny
}

// printOpts are the approval and output flags for headless print mode
// . The default is maximally safe: every approval-gated tool call is
// denied; --yes and --allow opt in explicitly.
type printOpts struct {
	// json is the --json alias as the flag parsed it; output is what it and
	// --output resolved to, and the only one the run itself reads.
	json    bool
	output  string
	yes     bool
	allow   []string
	sandbox bool
	// maxRounds overrides behavior.max_tool_rounds for this run, where 0
	// means no cap at all. maxRoundsSet tells the two zeroes apart: the flag
	// left alone (config, then the default) and --max-rounds 0 (uncapped).
	maxRounds    int
	maxRoundsSet bool
}

// rounds is the per-turn tool-round cap for this run: the flag when it was
// given, the config otherwise. Hitting the cap ends a headless run with
// ErrRoundCap, so --max-rounds 0 is the way to say "run until it is done" on
// a foreground run a person can interrupt.
func (o printOpts) rounds(cfg config.Config) int {
	return maxRoundsFor(cfg, o.maxRounds, o.maxRoundsSet)
}

// maxRoundsFor resolves the round cap the same way for a headless run and a
// session, which is the whole point of it: `--max-rounds 0` means the same
// thing on both sides of --print, and only the way out of a runaway differs
// (the exit code there, the interrupt key here).
//
// The zero the flag was left at and the zero it was set to are different
// answers, which is what set distinguishes: unset falls through to the
// config, where zero in turn means "nobody chose" and negative means no cap.
func maxRoundsFor(cfg config.Config, flag int, set bool) int {
	if !set {
		return cfg.Behavior.MaxToolRounds
	}
	if flag == 0 {
		return agent.UnlimitedToolRounds
	}
	return flag
}

// headlessObserver is the headless run's adaptation to the observer contract
// in internal/observe, the way internal/ui/chat/observe.go is the chat
// model's. The codes live there because every surface reports the same ones;
// what lives here is where a headless run's own accounting — its single
// turn, the round the loop has reached — is read off to fill them in.
//
// A run recorded without a position cannot be told apart from one that
// circled for forty rounds, which is the shape the record exists to show.
// See docs/capabilities/sessions-and-memory.md#every-composition-is-one-population.
type headlessObserver struct {
	rec *observeRecorder
	// rounds is the tool round the loop has reached. It is a function rather
	// than the agent itself because that is the whole of what a position
	// needs from it.
	rounds func() int
	// stream is the event stream the run was asked for, or nil where it was
	// not. It hangs here rather than beside the hooks so that what is written
	// to it and what is written to the record leave from one place: an event
	// that reaches the table and not the stream is how the two vocabularies
	// come apart, and nothing fails when they do.
	stream *jsonlStream
}

// pos is where the run is now. A headless run is one turn by construction —
// one prompt in, one answer out — so the turn is always 1 rather than a
// counter nothing would increment.
func (h headlessObserver) pos() observe.Pos {
	return observe.Pos{Turn: 1, Round: int64(h.rounds())}
}

// signal records one of the loop's own safeguards firing, and puts it on the
// stream under the same code. Every signal below goes through here, so a code
// cannot reach one and not the other.
func (h headlessObserver) signal(code, reason string) {
	at := h.pos()
	h.rec.signal(at, code, reason)
	h.stream.signal(at, code, reason)
}

// text is one piece of the answer as it was written. It reaches the stream
// and not the record: what the model said is content, and the record is
// content-free by construction.
func (h headlessObserver) text(s string) {
	h.stream.text(h.pos(), s)
}

// call is one call the model asked for, before it ran or was resolved. The
// record keeps a call and its result as one row, written when the result
// lands; the stream carries them as two, because between them is the wait
// that is the reason to read a stream at all.
func (h headlessObserver) call(tc provider.ToolCall) {
	h.stream.call(h.pos(), tc)
}

// toolResult records one executed call from its result text: the outcome
// and, for a failure, its class — and the repeat detector's notice where the
// result carries one, since being told it is circling is a thing that
// happened to the run and not a property of the call.
func (h headlessObserver) toolResult(r agent.ToolResult) {
	outcome, class := observe.ToolOutcome(r.Result)
	h.rec.toolCallAt(h.pos(), r.Call.Name, r.Duration, outcome, class)
	h.stream.result(h.pos(), r, outcome, class)
	if agent.IsRepeatNotice(r.Result) {
		h.signal(observe.SignalRepeat, r.Call.Name)
	}
}

// decision records what the approver resolved a gated call to. A headless
// run resolves every one of them from policy rather than from a person, so
// this is the only place its approval rate can come from.
func (h headlessObserver) decision(decision, reason string) {
	at := h.pos()
	h.rec.decisionAt(at, decision, reason)
	h.stream.decision(at, decision, reason)
}

// usage is what the run has spent so far. The record takes it priced; the
// stream carries the tokens, cached ones included, because a script totalling
// a night of runs is doing the pricing itself.
func (h headlessObserver) usage(u provider.Usage) {
	h.stream.usage(h.pos(), u)
}

// summary records a reading. Every reading lands here and not only the ones
// that go on to interrupt the turn: a drift rate is a fraction, and this is
// its denominator.
func (h headlessObserver) summary(v agent.SummaryVerdict) {
	h.signal(observe.SignalSummary, observe.SummaryCode(v.State))
}

// intervene records the run interrupting its own turn to ask it to take
// stock.
func (h headlessObserver) intervene(iv agent.Intervention) {
	h.signal(observe.SignalIntervene, iv.Kind.Signal())
}

// tree records the run being told the tree moved under it.
func (h headlessObserver) tree(n agent.TreeNotice) {
	h.signal(observe.SignalTree, n.Signal())
}

// compact records what a window-recovery step did. Both halves are recorded
// and each under its own code: a trim spends the provider's cached prefix and
// a compaction spends a request and the conversation, and a rate that added
// them together could not tell a run that shaved itself once from one that
// threw its history away.
func (h headlessObserver) compact(n agent.CompactNotice) {
	if n.Elided > 0 {
		h.signal(observe.SignalTrim, observe.TrimReason(n.Elided, n.BeforePct, n.AfterPct))
	}
	if n.Compacted {
		h.signal(observe.SignalCompact, observe.CompactPressure)
	}
}

// retry records one wait the run sat out after a request the provider never
// answered. It is recorded per attempt, so a population of unattended runs
// can be asked how much of its wall clock was a provider's and not its own.
func (h headlessObserver) retry(n agent.RetryNotice) {
	h.signal(observe.SignalRetry, n.Signal())
}

// writtenByCalls is the paths a headless run's mutating calls wrote: the
// subtrahend the tree reading needs, where a session would hand in its
// changeset. A call that came back as an error wrote nothing.
type writtenByCalls struct {
	mu   sync.Mutex
	list []string
}

func (w *writtenByCalls) wrap(resolve func(provider.ToolCall) string) func(provider.ToolCall) string {
	return func(tc provider.ToolCall) string {
		result := resolve(tc)
		w.note(tc, result)
		return result
	}
}

func (w *writtenByCalls) note(tc provider.ToolCall, result string) {
	if !tools.IsMutating(tc.Name) || strings.HasPrefix(result, "error:") {
		return
	}
	var args struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(tc.Arguments), &args) != nil || args.Path == "" {
		return
	}
	w.mu.Lock()
	w.list = append(w.list, args.Path)
	w.mu.Unlock()
}

func (w *writtenByCalls) paths() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.list...)
}

// headlessTree is the reading an unattended run watches its checkout with:
// the session's own wrap, over the paths the run's calls wrote where a
// session hands in its changeset. Assembling a second reading here is how the
// two came to differ once already — the session's block named the likeliest
// author of a change it had not made and the run's did not, which is backwards,
// since the run is the one with nobody in front of it to work that out.
func headlessTree(cfg config.Config, sib sessionSibling, own *writtenByCalls) *agent.TreeCheck {
	c := withSibling(treeCheck(cfg), sib)
	if c == nil {
		return nil
	}
	c.Own = own.paths
	return c
}

// headlessSlotLayout is how an unattended run's slot is named: the moment it
// began, to the second, which is what a session's own unnamed slot is called.
// The two spellings have to be the same one — a name in this shape is read
// as a slot the machine chose rather than one a person typed, and a listing
// of them sorts by it — so a run named some other way would show up in the
// saved chats as a conversation somebody deliberately named after a date.
// See docs/capabilities/sessions-and-memory.md#a-slot-belongs-to-one-session.
const headlessSlotLayout = "2006-01-02 15:04:05"

// headlessChat is where an unattended run's conversation lives and what is
// done to it: the slot it was reopened from or claimed, and the save that
// leaves it somewhere `shhh chat --continue` can find it.
//
// It is one value with one save rather than a name here and a write there,
// because everything that has to agree about the slot has to agree in one
// place: the run that failed is the one worth reopening, and it fails at
// several different points.
type headlessChat struct {
	db   *storage.DB
	slot string
	// at and head are where the reopening's reading sits in the conversation
	// and how long it is. Every save leaves it out: the reading is built from
	// the checkout each time a conversation is opened, so a slot that kept
	// one would hand the next opening a picture of a tree that has since
	// moved, drawn as a message the person never typed.
	at, head int
	// summary is the handoff the slot already carried. Nothing here compacts
	// — a run has no /compact and nobody to ask for one — so a save that
	// wrote an empty summary would take away the one a session left.
	summary string
}

// openHeadlessChat resolves what this run carries on from and where it will
// be saved, and answers with the conversation it should open on.
//
// A run told to continue is handed the stored conversation and then the
// checkout as it stands now, in that order and in front of the prompt: the
// transcript describes the tree as it was, and a run that acts on a path
// which has since moved is the one nobody is watching.
// See docs/capabilities/sessions-and-memory.md#an-unattended-run-comes-back-too.
func openHeadlessChat(db *storage.DB, session chatSession, initial []provider.Message, sysPrompt string) (*headlessChat, []provider.Message, error) {
	c := &headlessChat{db: db}
	msgs := initial
	if session.wantsResume() {
		reopened, err := session.resumeChat(db)
		if err != nil {
			return nil, nil, err
		}
		if reopened.slot != "" {
			c.slot = reopened.slot
			msgs = reopened.messages
			// The system prompt is this run's and not the one the
			// conversation was stored under: the shell, the directory and the
			// project's instruction files are read now, not last week.
			if len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
				msgs[0].Content = sysPrompt
			} else {
				msgs = append(append([]provider.Message{}, initial...), msgs...)
			}
			notice := chat.ResumeContext(db, c.slot, "")
			c.summary = notice.Summary
			msgs, c.at, c.head = spliceAfterSystem(msgs, notice.Messages)
		}
	}
	if c.slot == "" && db != nil {
		// A slot of this run's own, minted by the store rather than read off
		// the clock: two runs started in the same second would otherwise both
		// pick the same name, and the one that saved second would be the only
		// one left.
		c.slot = time.Now().Format(headlessSlotLayout)
		// A store that cannot say leaves the run on the name it had. That is
		// the collision back, for one run in a pair started in the same
		// second; dropping the slot instead would lose the conversation of
		// both, and this run has one turn to lose.
		if claimed, err := db.ClaimChatSlot(c.slot); err == nil {
			c.slot = claimed
		}
	}
	return c, msgs, nil
}

// spliceAfterSystem puts add in front of everything the conversation
// remembers but behind the system prompt, which is where a conversation
// states the facts it is to reason from. It answers with where they went and
// how many there were, which is what taking them out again needs.
func spliceAfterSystem(msgs, add []provider.Message) ([]provider.Message, int, int) {
	at := 0
	if len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		at = 1
	}
	joined := make([]provider.Message, 0, len(msgs)+len(add))
	joined = append(joined, msgs[:at]...)
	joined = append(joined, add...)
	joined = append(joined, msgs[at:]...)
	return joined, at, len(add)
}

// withoutReading is the conversation without the reading this opening put in
// front of it.
//
// It cuts by position rather than recognising the block by its shape, which
// is safe here and would not be in a session: an unattended conversation only
// ever grows at its tail — nothing compacts it and no rewind rebuilds it
// around the head — so the reading is still exactly where it was put.
func (c *headlessChat) withoutReading(msgs []provider.Message) []provider.Message {
	if c.head == 0 || len(msgs) < c.at+c.head {
		return msgs
	}
	kept := make([]provider.Message, 0, len(msgs)-c.head)
	kept = append(kept, msgs[:c.at]...)
	return append(kept, msgs[c.at+c.head:]...)
}

// save writes the conversation to this run's slot, however the run ended: a
// finished turn, a round cap, a provider that stopped answering. The reason
// to keep it at all is the run that failed — "open it in a session and look"
// needs the conversation to be somewhere — so the failures are the saves
// that matter most.
//
// The mid-turn mark a session leaves is never written here. It says a person
// quit with a turn parked and means the conversation comes back parked, and
// a run with no keyboard parks nothing.
// See docs/capabilities/sessions-and-memory.md#a-held-turn-comes-back-held.
func (c *headlessChat) save(msgs []provider.Message) {
	if c == nil || c.db == nil || c.slot == "" {
		return
	}
	// A slot another run has taken over is not written to; the store puts the
	// conversation in one of this run's own and says where.
	slot, err := c.db.AutosaveChat(c.slot, time.Now().Format(headlessSlotLayout), c.withoutReading(msgs), nil)
	if err != nil {
		// The failure goes to stderr beside the run's other activity rather
		// than being swallowed: the whole point of the slot is the run
		// somebody comes back to, and a run that cannot be come back to has
		// to say so while there is still an operator reading.
		fmt.Fprintf(os.Stderr, "» this run could not be saved for `--continue`: %v\n", err)
		return
	}
	c.slot = slot
	// And what the conversation is opened again on. The commit is read here,
	// at the save, so the slot says where the tree was when the conversation
	// was last written down rather than where it was when the run started.
	_ = c.db.SetChatResume(slot, storage.ChatResume{Summary: c.summary, Head: project.Head("")})
}

// runPrintSession runs the agent loop to completion without the TUI:
// assistant text streams to stdout, tool activity to stderr, and --output
// replaces the streamed text with a transcript at the end or an event stream
// while it happens. What it returns carries the exit code the run leaves
// behind, which is a projection of the outcome its turn was recorded under.
// See docs/capabilities/headless.md#the-exit-code-is-the-contract.
func runPrintSession(cmd *cobra.Command, args []string, session chatSession, opts printOpts) error {
	if err := headlessFlagCheck(session); err != nil {
		return err
	}
	// The working scope, mirroring the interactive session: the
	// directory the run was started in, plus config's scope_dirs and any
	// --add-dir. Nobody is here to grant a directory mid-run, so what the
	// flags and the config say is the whole scope for the run.
	sc, err := sessionScope(ConfigFrom(cmd.Context()), session.addDirs)
	if err != nil {
		return err
	}

	// The same registration the interactive session runs, on the same
	// conditions (toolset.go) — one definition rather than a copy here that
	// agrees with it on the day it is written. What differs is the browser: a
	// run with nobody in front of it never pops one, because nobody is
	// guaranteed to be at the desktop and the URL reaches the transcript
	// either way.
	ts, err := buildToolset(cmd, &session, "print", toolsetOpts{scope: sc})
	if err != nil {
		return err
	}
	defer ts.close()
	red, qgate, procSup := ts.evidence, ts.gate, ts.proc

	// The local store is opened here rather than with the recorder below
	// because trust for a project MCP server is read from it.
	db, _ := openStore()
	if db != nil {
		defer db.Close()
	}
	// Pointed at the store before the prompt and the tree reading are built
	// from it, exactly as a session does. A run nobody is watching is the one
	// that most needs the answer: told nothing, it sets about explaining or
	// reverting a change another session made, and there is nobody there to
	// stop it.
	// See docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone.
	session.sibling = readSibling(db)
	// MCP servers, mirroring the interactive session. A read-only server's
	// tools run; every other server's calls are gated and resolved the way
	// web_fetch is — --yes opts in, the default denies.
	if session.mcp {
		defer session.attachMCP(cmd.Context(), db, false)()
	}

	registerSkills(&session)

	// The model is told where the work is; a headless run cannot be
	// asked for a directory mid-flight, so knowing the boundary is the
	// difference between a report that names it and a round spent retrying.
	session.promptExtra = prompt.CombineExtra(session.promptExtra, scopePromptBlock(sc))

	// …and what it has to work with, for the same reason: nobody is
	// there to suggest the tool it did not know it had.
	session.promptExtra = prompt.CombineExtra(session.promptExtra, prompt.Toolbox(session.toolDefs))

	// Headless runs bill through the same gate the TUI does; a print run
	// that under-reported would be the harder one to notice, because nobody
	// is watching a rail while it works.
	prices := loadPricing()
	ledger := meter.New(prices)

	env, err := buildSessionEnv(cmd, session, ledger)
	if err != nil {
		return err
	}
	cfg := env.cfg

	initialPrompt := ""
	if len(args) > 0 {
		initialPrompt = args[0]
	}
	// Piped stdin is the prompt itself when no argument is given, and extra
	// context for the prompt otherwise (mirroring the chat TUI).
	if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
		maxChars := cfg.EffectiveContextMaxTokens() * 4
		content, err := stdin.Read(os.Stdin, maxChars)
		if err != nil {
			return err
		}
		if content != "" {
			if initialPrompt == "" {
				initialPrompt = content
			} else {
				initialPrompt = stdin.FormatPromptWithContext(initialPrompt, content)
			}
		}
	}
	if strings.TrimSpace(initialPrompt) == "" {
		return fmt.Errorf("print mode needs a prompt: pass one as an argument or pipe it on stdin")
	}

	allowlist := append([]string{}, cfg.Behavior.CommandAllowlist...)
	allowlist = append(allowlist, opts.allow...)

	// Headless approved commands run contained when a mechanism is available
	// — there is no human watching, so containment matters most here.
	// --sandbox goes further: a disposable container is created for
	// the run and approved commands exec inside it; if the sandbox cannot be
	// created and verified, the run fails instead of downgrading.
	run := runner.RunCapture
	// sandboxProfile is the profile the run's commands are actually under,
	// for the record: empty when nothing contains them.
	sandboxProfile := ""
	if opts.sandbox {
		srun, cleanup, err := startSandbox(cmd.Context(), cfg, session.vault.Names())
		if err != nil {
			return fmt.Errorf("sandbox: %w", err)
		}
		defer cleanup()
		run = srun
		// The container took the same profile the spec parsed; a name the
		// parser refused could not have started it.
		if profile, err := sandbox.ParseProfile(cfg.Sandbox.Profile); err == nil {
			sandboxProfile = string(profile)
		}
		if procSup != nil {
			// Approved commands exec inside the disposable container and a
			// started process cannot follow them in: what the supervisor
			// would hold is the exec client, not the process, so stopping
			// it would leave something running in a container nothing is
			// watching. Starting on the host instead would put the one
			// thing that keeps running outside the strongest containment
			// this run has, so a start is refused and says why.
			// See docs/capabilities/containment.md#a-started-process-is-contained-too.
			procSup.SetContainment(process.Containment{
				Mechanism: "container sandbox",
				Wrap: func(string, []string) ([]string, error) {
					return nil, fmt.Errorf("a long-running process cannot be started inside this run's disposable container; use execute_command, or run without --sandbox")
				},
			})
		}
	} else {
		containment, err := buildContainment(cfg, sc, procSup)
		if err != nil {
			return err
		}
		if containment.Run != nil {
			run = containment.Run
			sandboxProfile = containment.Profile
		}
	}
	run = scrubRunner(session.vault, run)
	// The ceiling matters most here. A session has a reader who can cancel a
	// command that is never going to finish; a headless run has nobody, and
	// the executor it is holding is held until something outside kills it.
	run = boundedRunner(run, cfg.CommandTimeout())

	// The conversation this run carries on, and the slot it will be left in.
	// The claim happens here rather than at the save so that two runs started
	// in the same second settle which of them owns the name before either
	// writes a word to it.
	saved, messages, err := openHeadlessChat(db, session, env.messages, env.sysPrompt)
	if err != nil {
		return err
	}
	// A slot this run claimed and never wrote to is given back on the way
	// out, so a run whose save never landed leaves no name behind for the
	// next one to be given a suffix around. A slot it resumed belongs to
	// whoever made it and is left alone.
	defer func() {
		if db != nil {
			_ = db.ReleaseChatSlot(saved.slot)
		}
	}()

	a := agent.New(messages, env.stream)
	a.SetSteering(steering(cfg, env.prompts))
	a.SetScrub(session.vault.ScrubMessage)
	if session.skills.Len() > 0 {
		a.KeepResults(skill.IsContent)
	}
	// Repeat detection goes on outside the shared chain, so it sees every
	// tool the chain can dispatch and the result the model will actually
	// read. A headless run needs it most: there is nobody watching to notice
	// the same search going round for the third time.
	a.SetExecutor(agent.NewRepeatDetector().WrapExecutor(ts.executor(session)))
	a.SetMaxRounds(opts.rounds(cfg))

	// Session observability: headless runs record the same
	// content-free events as interactive sessions; failure just disables
	// recording. Tool calls are strictly sequential here, so one start
	// timestamp is enough for durations.
	recorder := startObserveRecorder(db, "print", env.prov.Name(), env.modelName, prices)
	defer recorder.end()
	// No mode and no classifier: a headless run answers approvals with
	// --yes and --allow, and the record says so by leaving both empty
	// rather than borrowing the mode a session would have started in.
	recorder.stamp(env.prompts.fingerprintOf(env.sysPrompt), session.skills.Len(), projectFingerprintRoot(), sessionSettings(cfg, runSettings{
		effort:  env.effort,
		rounds:  roundCapFor(opts.rounds(cfg)),
		sandbox: sandboxProfile,
		model:   auxiliaryModel(env.provName, env.modelName),
		summary: cfg.HeadlessSummaryEnabled(),
	}))
	// The gate's verdict, mirroring the interactive session — and a run
	// with nobody in front of it is the one whose verdict the record most
	// needs, because there was no one there to read it on the way past.
	recordGateVerdicts(qgate, recorder)
	// The stream, where one was asked for. It is opened here rather than at
	// the first event so that a consumer that read nothing still sees the
	// close line, and it is nil for every other shape, which every write to
	// it is safe under.
	var events *jsonlStream
	if opts.output == outputJSONL {
		events = newJSONLStream(os.Stdout)
	}
	obs := headlessObserver{rec: recorder, rounds: a.Rounds, stream: events}
	// A headless run is one turn; it closes here with the rounds it took,
	// the same event an interactive turn ends with.
	var runErr error
	runStart := time.Now()
	defer func() {
		recorder.turn(1, int64(a.Rounds()), time.Since(runStart), headlessTurnOutcome(runErr))
	}()

	var usage provider.Usage
	webTools := session.web
	mcpTools := session.mcpTools
	gate := func(tc provider.ToolCall) bool {
		if webTools != nil && tc.Name == web.FetchToolName {
			return true
		}
		// The process tool gates on its arguments: only start needs
		// approval; status/read/input/stop auto-run.
		if procSup != nil && tc.Name == process.ToolName {
			return process.NeedsApproval(json.RawMessage(tc.Arguments))
		}
		if mcpTools != nil && mcpTools.Has(tc.Name) {
			return !mcpTools.ReadOnly(tc.Name)
		}
		return headlessGate(tc.Name)
	}
	// A non-interactive run has nobody to notice it has drifted or that it
	// already has what it needs, which is why readings default on here. The
	// prompt is the instruction every one of them is judged against.
	summaryRun := agent.NewSummaryRun(
		newSummarizer(cfg, env, ledger, cfg.HeadlessSummaryEnabled()),
		agent.NewRecorder(0), initialPrompt)
	// Every verdict reaches the record and the stream through the observer,
	// and is remembered on its way past: a denial still standing when the
	// model stops is what says this run was refused rather than finished.
	verdict := &lastVerdict{}
	resolve := headlessApprover(cmd.Context(), opts, allowlist, run, red, verdict.wrap(obs.decision),
		session.web, procSup, lspMutationHook(session.lsp), sc, session.mcpTools)
	// A headless run has no changeset, so what it wrote is read off the
	// calls that wrote it. Two readers want that list — the tree check, as
	// the subtrahend for what somebody else changed, and the close run, to
	// know whether this turn changed anything worth checking — so it is
	// kept whether or not the tree check is on.
	own := &writtenByCalls{}
	resolve = own.wrap(resolve)
	if c := headlessTree(cfg, session.sibling, own); c != nil {
		a.SetTreeCheck(*c)
	}
	h := &agent.Headless{
		Agent:   a,
		Compact: headlessCompactor(cmd.Context(), cfg, env, ledger, prices, session.toolDefs),
		Summary: summaryRun,
		OnIntervene: func(iv agent.Intervention) {
			fmt.Fprintf(os.Stderr, "» %s\n", iv.Notice)
			obs.intervene(iv)
		},
		OnSummary: obs.summary,
		// A run nobody is reading still says when its conversation was
		// recycled, on the same stream as its other activity: an answer that
		// arrived after a compaction was written by a model that had been
		// handed a summary of what it did, and a reader comparing two runs
		// has to be able to see which of them that was.
		OnCompact: func(n agent.CompactNotice) {
			fmt.Fprintf(os.Stderr, "» %s\n", n.Notice)
			obs.compact(n)
		},
		Gate:    gate,
		Resolve: resolve,
		OnTree: func(n agent.TreeNotice) {
			fmt.Fprintf(os.Stderr, "» %s\n", n.Notice)
			obs.tree(n)
		},
		OnToolCall: func(tc provider.ToolCall) {
			fmt.Fprintf(os.Stderr, "» %s %s\n", tc.Name, clipActivityLine(tc.Arguments))
			obs.call(tc)
		},
		OnToolResult: func(r agent.ToolResult) {
			if outcome, _ := observe.ToolOutcome(r.Result); outcome == observe.OutcomeError {
				// The call is named on the failure line as well as on its own
				// line above, because a round's reads go out together: the
				// line the indent used to point at is no longer the line
				// directly above it.
				fmt.Fprintf(os.Stderr, "  ↳ %s: %s\n", r.Call.Name, clipActivityLine(r.Result))
			}
			obs.toolResult(r)
		},
		// The wait goes to stderr beside the run's other activity, because a
		// script that reads stdout for the answer is not the reader this line
		// is for: a run that has gone quiet for a minute is otherwise
		// indistinguishable from one that has hung.
		OnRetry: func(n agent.RetryNotice) {
			if n.Partial != "" && !strings.HasSuffix(n.Partial, "\n") {
				// Half a sentence is already on stdout and the retry asks the
				// whole question again, so it is closed off here rather than
				// left for the reply that replaces it to run on from.
				fmt.Fprintln(os.Stdout)
			}
			fmt.Fprintf(os.Stderr, "» %s\n", n.Notice)
			obs.retry(n)
		},
		OnUsage: func(*provider.Usage) {
			// What the run has spent is read back from the ledger rather than
			// summed here. The request that just landed was billed at the
			// gate, and so is anything a later feature adds to a headless
			// run without touching this callback.
			t := ledger.Total()
			usage = provider.Usage{
				PromptTokens:     int(t.In),
				CompletionTokens: int(t.Out),
				CachedTokens:     int(t.Cached),
			}
			recorder.usagePriced(1, t.In, t.Out, t.Cost, t.Priced)
			obs.usage(usage)
		},
	}
	h.SetRetryLimit(cfg.Behavior.ProviderRetries)
	// The checks run at the close of an unattended turn without being asked
	// for, because this is the surface where "the model said it was done" is
	// otherwise the only signal there is: nobody read the answer, and
	// nothing between the last edit and the exit code has an opinion about
	// whether the tree still builds.
	var closing *headlessCloseGate
	if suite, retries, ok := onCloseGate(qgate); ok {
		closing = &headlessCloseGate{
			ctx: cmd.Context(), gate: qgate, suite: suite, retries: retries,
			written: own.paths,
		}
		h.OnClose = closing.close
	}
	// Where the answer goes as it is written: stdout for a person or a
	// `$(...)`, the stream for a consumer reading events, and nowhere at all
	// for the transcript shape, which states the whole answer at the end.
	switch opts.output {
	case outputText:
		h.OnText = func(text string) { fmt.Fprint(os.Stdout, text) }
	case outputJSONL:
		h.OnText = obs.text
	}

	// A signal reaching this run stops the turn, and the handler is up for
	// exactly as long as there is a turn to stop: once the loop has returned,
	// the save and the record below are as killable as they were before this
	// existed.
	// See docs/capabilities/headless.md#what-a-signal-does-to-a-run.
	stopSignals := interruptOnSignal(h.Interrupt)
	final, err := h.Run(initialPrompt)
	stopSignals()
	runErr = err
	// Whatever ended it, the conversation is left where the next `shhh chat
	// --continue` will find it, and the record is told which slot that is —
	// the name is the only thing joining what the run cost to what it said,
	// and it is not known until the save has settled where the words went.
	saved.save(a.Messages())
	recorder.link(saved.slot)
	if opts.output == outputText && final != "" && !strings.HasSuffix(final, "\n") {
		fmt.Fprintln(os.Stdout)
	}
	// The loop ended the way it ended; whether the code it left behind
	// passes is a second answer, and the exit code is where an unattended
	// run states it. The turn's own outcome above is untouched by it: the
	// turn finished, and a failing suite is the gate's row in the record
	// rather than a turn that broke.
	gateErr := closing.err()
	refused := verdict.refused()
	out := runErr
	switch {
	case out != nil:
		// The loop's own ending stands, and the two readings below belong to
		// a turn that got as far as finishing.
	case gateErr != nil:
		out = gateErr
	case refused:
		// A run whose last word from the policy was a refusal did not do what
		// it was asked, and its status has to say so — but nothing failed, so
		// there is no error to report and one is stated here.
		out = errHeadlessRefused
	}
	outcome := headlessTurnOutcome(runErr)
	code := headlessExitCode(outcome, gateErr != nil, refused)
	switch opts.output {
	case outputJSON:
		if err := writeJSONTranscript(os.Stdout, a.Messages(), final, usage, out); err != nil {
			return err
		}
	case outputJSONL:
		events.closed(obs.pos(), outcome, code, final, usage, out)
	}
	// Nothing to report and nothing to report it as: every code above zero
	// has an error behind it, which is what carries it out to the process.
	if out == nil {
		return nil
	}
	return exitError{code: code, err: out}
}

// interruptOnSignal turns the first interrupt or termination signal the run
// is sent into an interrupt for its turn, and hands back the teardown that
// takes the handler down again.
//
// Without one the process dies where it stood: nothing says how the turn
// ended, there is no conversation to continue from, and whatever ran the
// command reads a signal status where the contract promises a code. With one
// the loop stops at its next checkpoint and the run ends the way its other
// endings end — saved, recorded, and reported as a status.
//
// The handler is one grace and not a mode, which is why it comes down as the
// first signal is taken and before the loop is told anything: the second
// signal has to reach the default disposition and kill the process, because
// killing it is all that is left for a run stuck somewhere no checkpoint
// runs. Stopping the channel is what restores that disposition — the runtime
// hands a signal back to the operating system once the last channel watching
// it is gone — and a reset would hand back every other registration in the
// process along with this one.
//
// The teardown waits for the watcher to be gone rather than only asking it to
// stop, so a caller that has returned from it knows nothing is left that
// could interrupt the turn after it.
// See docs/capabilities/headless.md#what-a-signal-does-to-a-run.
func interruptOnSignal(interrupt func()) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	done, gone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(gone)
		select {
		case <-ch:
			signal.Stop(ch)
			interrupt()
		case <-done:
			signal.Stop(ch)
		}
	}()
	return sync.OnceFunc(func() {
		close(done)
		<-gone
	})
}

// headlessCompactor is the window-recovery step for an unattended run: the
// window it measures its conversation against, what the toolset costs on
// every request, and where a summary is asked for when eliding old tool
// results can no longer make room.
//
// The window is the pricing table's answer and the model family's behind it,
// and no step at all when neither can say. Recovering against a guessed
// window would throw a conversation away that had most of its room left,
// while doing nothing is exactly what a long run did before this existed —
// the cheaper mistake by a wide margin. The endpoint's own answer is
// deliberately not asked for: a session takes it in the background across
// many turns, and a one-shot run would race the query rather than read it.
func headlessCompactor(ctx context.Context, cfg config.Config, env *sessionEnv, ledger *meter.Ledger, prices *pricing.Table, defs []provider.Tool) *agent.Compactor {
	var window int64
	if prices != nil {
		window, _ = prices.ContextWindow(env.modelName)
	}
	if window <= 0 {
		window, _ = provider.ContextWindowFor(env.modelName)
	}
	if window <= 0 {
		return nil
	}
	c := &agent.Compactor{Model: env.modelName, Window: window}
	// The definitions are on every request and are not in the conversation,
	// so a run that left them out of the estimate would think it had a
	// toolset's worth of room it does not have.
	for _, t := range toolDefTokens(defs) {
		c.ToolTokens += t.Tokens
	}
	// A summary is a bounded piece of prose about a conversation, so it goes
	// to the model a configuration named for one where it named one. Never to
	// a model with a smaller window than the conversation it is being handed,
	// though, and never to one nothing can vouch for the window of: the
	// moment a compaction is asked for is the moment that conversation is
	// nearly a window's worth, so a smaller model would refuse the request at
	// precisely the point there is no room to fail.
	if name := strings.TrimSpace(cfg.Summary.Model); name != "" && name != env.modelName {
		if w, ok := summaryModelWindow(prices, name); ok && w >= window {
			c.Stream = summaryModelStream(ctx, env, ledger, defs, name)
		}
	}
	return c
}

// summaryModelWindow is what anything can say about a model that is not the
// one the conversation is on.
func summaryModelWindow(prices *pricing.Table, model string) (int64, bool) {
	if prices != nil {
		if w, ok := prices.ContextWindow(model); ok {
			return w, true
		}
	}
	return provider.ContextWindowFor(model)
}

// summaryModelStream sends one request on another model of the same provider,
// billed as the summary work it is. It carries the conversation's own tool
// definitions and the caller's tool choice, because what makes a summary
// request safe is the choice forbidding a call and not the tools being
// missing from the request.
func summaryModelStream(ctx context.Context, env *sessionEnv, ledger *meter.Ledger, defs []provider.Tool, model string) agent.StreamFunc {
	return func(msgs []provider.Message, choice string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		reqCtx, cancel := context.WithCancel(ctx)
		events, err := ledger.For(env.prov, meter.SourceSummary).StreamCompletion(reqCtx, msgs, provider.CompletionOpts{
			Model:      model,
			Tools:      defs,
			ToolChoice: choice,
			// A shallow thought over a conversation that is already written.
			// The judgement asked for is what mattered in it, not what to do
			// next, and a deep one here is paid for out of the budget the run
			// is trying to get back under.
			Effort: provider.EffortLow,
		})
		if err != nil {
			cancel()
			return nil, nil, err
		}
		return events, cancel, nil
	}
}

// headlessCloseGate is the on-close gate run for an unattended turn: the
// suite the workspace names, run once the model has stopped calling tools,
// with a failing verdict handed back for another round while the config's
// budget of hand-backs lasts.
//
// It is a value with a life of its own because the count has to survive the
// hook being called again: the hand-back continues the same turn, and a
// counter reset each time it is asked would let a run that never passes
// alternate between the model and the suite until the round cap stopped it.
type headlessCloseGate struct {
	ctx     context.Context
	gate    *quality.Runner
	suite   string
	retries int
	// written is what the run's mutating calls wrote, the unattended
	// stand-in for a session's changeset.
	written func() []string

	fed  int
	last *quality.Result
}

// close is the agent.Headless.OnClose hook.
func (g *headlessCloseGate) close(string) string {
	// A turn that wrote nothing, or wrote only under shhh's own state
	// directory, has nothing a suite could have an opinion about
	// (changeset.AnyCheckable). It runs nothing and says nothing.
	if !changeset.AnyCheckable(g.written()) {
		return ""
	}
	res, err := g.gate.Run(g.ctx, g.suite)
	if err != nil {
		// The only error Run reports is a run already in flight, which here
		// means a suite the model asked for itself is still going. Its
		// verdict is the one the turn is about to be judged on anyway.
		return ""
	}
	g.last = res
	text := res.Format(quality.TakeFingerprint(g.gate.Workspace))
	// The verdict goes to stderr beside the run's other activity rather than
	// into the answer on stdout, which belongs to whatever is reading it.
	fmt.Fprintf(os.Stderr, "» %s\n", text)
	if res.Verdict != quality.VerdictFail && res.Verdict != quality.VerdictBlocked {
		return ""
	}
	if g.fed >= g.retries {
		return ""
	}
	g.fed++
	// The same text the tool returns, and nothing else: a run told about a
	// failure in words the model has never seen from the gate would be
	// learning a second vocabulary for the same event.
	return text
}

// err is what the last verdict says about the exit code. A pass, a
// cancellation and a turn that never ran the suite are all nil: cancelled is
// the run being stopped, which the interrupt already answers for.
func (g *headlessCloseGate) err() error {
	if g == nil || g.last == nil {
		return nil
	}
	switch g.last.Verdict {
	case quality.VerdictFail, quality.VerdictBlocked:
		return fmt.Errorf("quality gate %q: %s", g.last.Suite, g.last.Verdict)
	}
	return nil
}

// headlessTurnOutcome is how a headless run's single turn ended, in the
// closed set a session's turns end in. The round cap is `cap-paused` and not
// a failure even though it ends this run: the event being counted is a turn
// that stopped at its cap, which is the same event on every surface, and
// spelling it `failed` here would read the whole headless population as
// having no capped turns at all. What differs is only that nobody is here to
// grant more rounds, and that difference is the exit code's to report.
func headlessTurnOutcome(err error) string {
	switch {
	case err == nil:
		return observe.TurnDone
	case errors.Is(err, agent.ErrRoundCap):
		return observe.TurnCapPaused
	case errors.Is(err, agent.ErrInterrupted):
		return observe.TurnCancelled
	}
	return observe.TurnFailed
}

// headlessFlagCheck refuses a flag this run cannot honour, before anything
// is built on it. Only the picker is one: --resume with no chat named is a
// full-screen program and a person choosing, and a run with nobody in front
// of it can neither draw it nor be answered.
//
// Refusing is the point. A flag that is parsed, accepted and never read
// leaves a run that says it resumed and started from nothing, and the script
// chaining runs on the strength of it works from nothing too, silently.
func headlessFlagCheck(session chatSession) error {
	if session.resumePick {
		return fmt.Errorf("--resume opens the saved-chat picker and this run has nobody to pick with: " +
			"name the conversation with --resume=<name>, or pass --continue for the most recent")
	}
	return nil
}

// headlessGate mirrors the TUI's requiresApproval: exec and file-modification
// tools go through approval; read-only tools run directly.
func headlessGate(name string) bool {
	return name == tools.ExecCommandName || tools.IsMutating(name)
}

// headlessApprover resolves approval-gated tool calls without a prompt:
// policy decides. Safety-flagged commands are always denied — there is no
// human to confirm them in a headless run. Approved results run through the
// reduction pipeline (red is nil-safe) like every other tool result. Each
// verdict is reported to record (nil-safe) as a content-free decision event.
func headlessApprover(ctx context.Context, opts printOpts, allowlist []string, run func(context.Context, string) (string, int), red *evidence.Reducer, record func(decision, reason string), webTools *web.Toolset, procSup *process.Supervisor, mutationHook chat.MutationHook, sc *scope.Scope, mcpTools *mcp.Toolset) func(provider.ToolCall) string {
	note := func(decision, reason string) {
		if record != nil {
			record(decision, reason)
		}
	}
	return func(tc provider.ToolCall) string {
		// A server call is an external action like a fetch: --yes opts
		// in, the default denies.
		if mcpTools != nil && mcpTools.Has(tc.Name) {
			if opts.yes {
				note(observe.DecisionAllow, observe.ReasonHeadlessYes)
				return red.Process(tc.Name, agent.ExecuteWith(mcpTools.Execute, tc))
			}
			note(observe.DecisionDeny, observe.ReasonHeadlessDefault)
			return "error: " + tc.Name + " not approved: headless mode denies external actions by default (run with --yes)"
		}
		// web_fetch is an external action: --yes opts in, the default
		// denies like every other gated call.
		if webTools != nil && tc.Name == web.FetchToolName {
			if opts.yes {
				note(observe.DecisionAllow, observe.ReasonHeadlessYes)
				return red.Process(tc.Name, agent.ExecuteWith(webTools.Execute, tc))
			}
			note(observe.DecisionDeny, observe.ReasonHeadlessDefault)
			return "error: web fetch not approved: headless mode denies external actions by default (run with --yes)"
		}
		// A process start is approved like a command: safety-flagged
		// commands are always denied headless; --yes or an allowlist match
		// opts in.
		if procSup != nil && tc.Name == process.ToolName {
			_, command, err := process.StartSummary(json.RawMessage(tc.Arguments))
			if err != nil {
				return "error: " + err.Error()
			}
			if warnings := safety.Check(command); len(warnings) > 0 {
				risks := make([]string, 0, len(warnings))
				for _, w := range warnings {
					risks = append(risks, w.Risk)
				}
				note(observe.DecisionDeny, observe.ReasonSafety)
				return "error: process start denied (" + strings.Join(risks, "; ") + "); safety-flagged commands require interactive approval"
			}
			if opts.yes || agent.AllowlistMatches(allowlist, command) {
				// A process start is a command, and the working scope
				// applies to it as much as to a foreground one.
				if deny, ok := headlessScopeCheck(sc, opts.yes, radius.WritePaths(command)); !ok {
					note(observe.DecisionDeny, observe.ReasonOutOfScope)
					return deny
				}
				if opts.yes {
					note(observe.DecisionAllow, observe.ReasonHeadlessYes)
				} else {
					note(observe.DecisionAllow, observe.ReasonAllowlist)
				}
				exec := func(_ string, args json.RawMessage) (string, error) { return procSup.Execute(args) }
				return red.Process(tc.Name, agent.ExecuteWith(exec, tc))
			}
			note(observe.DecisionDeny, observe.ReasonHeadlessDefault)
			return "error: process start not approved: headless mode denies commands by default (run with --yes or --allow)"
		}
		if tc.Name == tools.ExecCommandName {
			var args struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil || strings.TrimSpace(args.Command) == "" {
				return "error: invalid command arguments"
			}
			if warnings := safety.Check(args.Command); len(warnings) > 0 {
				risks := make([]string, 0, len(warnings))
				for _, w := range warnings {
					risks = append(risks, w.Risk)
				}
				note(observe.DecisionDeny, observe.ReasonSafety)
				return "error: command denied (" + strings.Join(risks, "; ") + "); safety-flagged commands require interactive approval"
			}
			if opts.yes || agent.AllowlistMatches(allowlist, args.Command) {
				// The working scope is checked before the grant is
				// spent: an allowlisted command shape is not a licence to
				// write outside the directories this run was given.
				if deny, ok := headlessScopeCheck(sc, opts.yes, radius.WritePaths(args.Command)); !ok {
					note(observe.DecisionDeny, observe.ReasonOutOfScope)
					return deny
				}
				if opts.yes {
					note(observe.DecisionAllow, observe.ReasonHeadlessYes)
				} else {
					note(observe.DecisionAllow, observe.ReasonAllowlist)
				}
				out, code := run(ctx, args.Command)
				return tools.FormatExecResult(red.Process(tools.ExecCommandName, out), code)
			}
			note(observe.DecisionDeny, observe.ReasonHeadlessDefault)
			return "error: command not approved: headless mode denies commands by default (run with --yes or --allow)"
		}
		if tools.IsMutating(tc.Name) {
			if opts.yes {
				if mut, err := tools.PreviewMutation(tc.Name, json.RawMessage(tc.Arguments)); err == nil {
					if deny, ok := headlessScopeCheck(sc, opts.yes, []string{mut.Path}); !ok {
						note(observe.DecisionDeny, observe.ReasonOutOfScope)
						return deny
					}
				}
				note(observe.DecisionAllow, observe.ReasonHeadlessYes)
				result := agent.ExecuteWith(tools.ExecuteMutating, tc)
				if mutationHook != nil {
					result = mutationHook(tc.Name, json.RawMessage(tc.Arguments), result)
				}
				return red.Process(tc.Name, result)
			}
			note(observe.DecisionDeny, observe.ReasonHeadlessDefault)
			return "error: file modification not approved: headless mode denies edits by default (run with --yes)"
		}
		return "error: tool " + tc.Name + " cannot be approved in this session"
	}
}

// clipActivityLine renders text as a single bounded line for stderr activity
// logging.
func clipActivityLine(raw string) string {
	s := strings.Join(strings.Fields(raw), " ")
	if cut, truncated := tools.TruncateOutput(s, 120); truncated {
		return cut + "…"
	}
	return s
}

// jsonTranscript is the --json output: the outcome, usage totals, and the
// full conversation including tool calls and results.
type jsonTranscript struct {
	Success  bool          `json:"success"`
	Error    string        `json:"error,omitempty"`
	Final    string        `json:"final"`
	Usage    jsonUsage     `json:"usage"`
	Messages []jsonMessage `json:"messages"`
}

// jsonUsage is what the run cost, as every JSON shape reports it. The cached
// tokens are part of the prompt total and stated separately because they are
// billed at a fraction of it: a script totalling a night of runs against a
// price list cannot work out what it spent from the other two figures, and
// the run already knows.
// See docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once.
type jsonUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	CachedTokens     int `json:"cached_tokens"`
}

// usageOf is the one reading of a run's totals into the shape both the
// transcript and the stream state them in.
func usageOf(u provider.Usage) jsonUsage {
	return jsonUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		CachedTokens:     u.CachedTokens,
	}
}

// jsonMessage is one message as every JSON transcript emits it: the role and
// the words, and a tool call's name and arguments where there was one.
// Attachments are left out — a transcript for a script is text.
//
// `shhh chats show --json` and `shhh code --print --json` are two views of the
// same conversation, so they are one projection: two structs of the same
// fields built by two loops drift the moment either grows a field.
type jsonMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []jsonToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// jsonToolCall is one call the assistant made, name and arguments apart so a
// script does not have to split a string. The id is what a tool result refers
// back to, and is always present — a consumer that keys on it should find an
// empty string rather than no field where one was never recorded.
type jsonToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// jsonMessages is the conversation as data.
func jsonMessages(msgs []provider.Message) []jsonMessage {
	out := make([]jsonMessage, 0, len(msgs))
	for _, m := range msgs {
		row := jsonMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			row.ToolCalls = append(row.ToolCalls, jsonToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
		}
		out = append(out, row)
	}
	return out
}

func writeJSONTranscript(w io.Writer, msgs []provider.Message, final string, usage provider.Usage, runErr error) error {
	t := jsonTranscript{
		Success:  runErr == nil,
		Final:    final,
		Usage:    usageOf(usage),
		Messages: jsonMessages(msgs),
	}
	if runErr != nil {
		t.Error = runErr.Error()
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(t)
}

// jsonlStream is what `--output jsonl` writes: one JSON object per line as
// the run happens, in the vocabulary internal/observe already fixes for the
// record. A reader of one shape therefore already knows the other, and a hook
// written against the record's codes matches the stream's without a second
// table to learn.
//
// The lines are the event and nothing else — no wrapping, no indentation, no
// trailing summary — because the thing reading them is a loop over a pipe
// that has to be able to act on a line before the run is over.
// See docs/capabilities/headless.md#the-stream-is-the-record-as-it-happens.
type jsonlStream struct {
	// mu is around the encoder rather than around each caller. Everything
	// reaches it on the run's own goroutine today, and interleaved halves of
	// two objects would be a corrupt stream that nothing reports — the
	// cheapest possible insurance against the round that dispatches its
	// callbacks the way it already dispatches its calls.
	mu  sync.Mutex
	enc *json.Encoder
}

func newJSONLStream(w io.Writer) *jsonlStream {
	return &jsonlStream{enc: json.NewEncoder(w)}
}

// jsonEvent is one line of that stream. Every field that names a kind, an
// outcome, a decision, a reason or a signal holds a constant from
// internal/observe and never text this run composed: a script matches on
// them, and a code it has to parse prose out of is not a code.
type jsonEvent struct {
	Kind  string `json:"kind"`
	Turn  int64  `json:"turn"`
	Round int64  `json:"round"`

	Text       string `json:"text,omitempty"`
	ID         string `json:"id,omitempty"`
	Tool       string `json:"tool,omitempty"`
	Arguments  string `json:"arguments,omitempty"`
	Result     string `json:"result,omitempty"`
	Outcome    string `json:"outcome,omitempty"`
	Class      string `json:"class,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Decision   string `json:"decision,omitempty"`
	Code       string `json:"code,omitempty"`
	Reason     string `json:"reason,omitempty"`

	Usage *jsonUsage `json:"usage,omitempty"`
	// Exit and Final belong to the close line alone. Exit is a pointer so
	// that the code the run is about to exit with is stated even when it is
	// zero, which is the one value a reader most needs to see written down.
	Exit  *int   `json:"exit,omitempty"`
	Final string `json:"final,omitempty"`
	Error string `json:"error,omitempty"`
}

// write puts one event on the stream. A nil stream is the run that asked for
// another shape, and writes nothing.
func (s *jsonlStream) write(ev jsonEvent) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A stream nobody is reading any more — a consumer that stopped at the
	// first line it wanted — is not a reason to end the run: the record and
	// the exit code are still owed.
	_ = s.enc.Encode(ev)
}

func (s *jsonlStream) text(at observe.Pos, text string) {
	if s == nil || text == "" {
		return
	}
	s.write(jsonEvent{Kind: observe.EventText, Turn: at.Turn, Round: at.Round, Text: text})
}

func (s *jsonlStream) call(at observe.Pos, tc provider.ToolCall) {
	if s == nil {
		return
	}
	s.write(jsonEvent{Kind: observe.EventToolCall, Turn: at.Turn, Round: at.Round,
		ID: tc.ID, Tool: tc.Name, Arguments: tc.Arguments})
}

func (s *jsonlStream) result(at observe.Pos, r agent.ToolResult, outcome, class string) {
	if s == nil {
		return
	}
	s.write(jsonEvent{Kind: observe.EventToolResult, Turn: at.Turn, Round: at.Round,
		ID: r.Call.ID, Tool: r.Call.Name, Result: r.Result,
		Outcome: outcome, Class: class, DurationMS: r.Duration.Milliseconds()})
}

func (s *jsonlStream) decision(at observe.Pos, decision, reason string) {
	if s == nil {
		return
	}
	s.write(jsonEvent{Kind: observe.EventDecision, Turn: at.Turn, Round: at.Round,
		Decision: decision, Reason: reason})
}

func (s *jsonlStream) signal(at observe.Pos, code, reason string) {
	if s == nil {
		return
	}
	s.write(jsonEvent{Kind: observe.EventSignal, Turn: at.Turn, Round: at.Round,
		Code: code, Reason: reason})
}

func (s *jsonlStream) usage(at observe.Pos, u provider.Usage) {
	if s == nil {
		return
	}
	priced := usageOf(u)
	s.write(jsonEvent{Kind: observe.EventUsage, Turn: at.Turn, Round: at.Round, Usage: &priced})
}

// closed is the last line of every stream: how the turn ended, in the same
// word the record keeps, the exit code projected from it, the answer, and
// what the run spent getting there. A consumer that reads only this line has
// everything the exit status says and the answer besides.
func (s *jsonlStream) closed(at observe.Pos, outcome string, code int, final string, u provider.Usage, err error) {
	if s == nil {
		return
	}
	priced := usageOf(u)
	ev := jsonEvent{Kind: observe.EventClose, Turn: at.Turn, Round: at.Round,
		Outcome: outcome, Exit: &code, Final: final, Usage: &priced}
	if err != nil {
		ev.Error = err.Error()
	}
	s.write(ev)
}
