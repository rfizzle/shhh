package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/rfizzle/shhh/internal/hook"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/nudge"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/stdin"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/subagent"
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
	// exitRejected is the run the provider would refuse again just as it
	// stands. 4 says the built-in waits have been spent and the provider is
	// still not answering, so a script's move is to come back later; a key
	// that was not taken, an account with nothing left on it, an id the
	// endpoint does not serve and a request past the window will all be
	// exactly as wrong in an hour, and sending a caller off to wait out a
	// typo is what this separates out.
	exitRejected = 8
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
	case observe.TurnRejected:
		return exitRejected
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
	// autoMode is `--mode auto`: a gated call neither --yes nor --allow
	// answers goes to the permission classifier instead of being refused
	// outright, and every way of not reaching a verdict is a refusal
	// (approvals.go).
	autoMode bool
	// maxRounds overrides behavior.max_tool_rounds for this run, where 0
	// means no cap at all. maxRoundsSet tells the two zeroes apart: the flag
	// left alone (config, then the default) and --max-rounds 0 (uncapped).
	maxRounds    int
	maxRoundsSet bool
	// schema is --output-schema, read before the run: the shape the final
	// answer is held to, or nil for an answer that is whatever the model
	// wrote.
	// See docs/capabilities/headless.md#an-answer-can-be-held-to-a-schema.
	schema *answerSchema
}

// parseUnattendedMode reads --mode for a surface with nobody in front of it.
//
// `auto` is the only name it takes, and the refusal names the other three
// rather than pretending they do not exist: manual and accept-edits are the
// two modes whose whole content is which calls they stop to ask about, and
// plan mode's refusals are a shape a run left to work on its own has no use
// for. What an unattended run needs is the one mode that decides, and the
// flags already say the rest.
// See docs/capabilities/headless.md#auto-mode-fails-closed.
func parseUnattendedMode(name string) (bool, error) {
	trimmed := strings.ToLower(strings.TrimSpace(name))
	if trimmed == "" {
		return false, nil
	}
	mode, err := agent.ParseMode(trimmed)
	if err != nil {
		return false, fmt.Errorf("--mode: %w", err)
	}
	if mode != agent.ModeAuto {
		return false, fmt.Errorf("--mode %s needs somebody to prompt and there is nobody here: auto is the only mode a run with no terminal takes", mode)
	}
	return true, nil
}

// answered reports the run having been given something that can say yes to a
// gated call at all: --yes answers every one of them in advance, and auto
// mode puts each of them to the classifier. A run with neither refuses them
// all, which is why it is offered no delegate and can land no child's patch
// — neither of those is a decision it has any way to make.
//
// It is one predicate and not two conditions written out at each site,
// because the two sites are the same question asked about the same run: a run
// that may spawn a writer and may not take what the writer wrote has spent
// the whole fan-out for nothing, and the child's own report says the user
// declined it, about a run that has no user.
func (o printOpts) answered() bool { return o.yes || o.autoMode }

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
	// kind is the surface the conversation belongs to — the word `shhh
	// <kind> --resume` takes — kept so the handle a run states names the
	// command that actually opens it: a conversation a reading run left is
	// reopened with `shhh chat` and one a coding run left with `shhh code`.
	kind string
	// at and head are where the reopening's reading sits in the conversation
	// and how long it is. Every save leaves it out: the reading is built from
	// the checkout each time a conversation is opened, so a slot that kept
	// one would hand the next opening a picture of a tree that has since
	// moved, drawn as a message the person never typed.
	at, head int
	// summary is the slot's standing account: what it already carried, and
	// the revision reviseAccount makes of it from this run before the save.
	// A save that wrote an empty one would take away the account a session
	// left, so a run whose revision failed keeps the one it had.
	summary string
	// steps is the session's working list the slot already carried, kept for
	// the same reason: a run reads no list of its own, so a save that wrote
	// none would take away the one a session left
	// (docs/capabilities/coding-agent.md#the-session-keeps-its-own-working-steps).
	steps string
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
	c := &headlessChat{db: db, kind: session.kind}
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
			// No working list: this run has no steps tool to keep one with.
			notice := chat.ResumeContext(db, c.slot, "", false)
			c.summary = notice.Summary
			c.steps = notice.Steps
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

// handles is where this conversation can be picked up, in the three forms a
// caller reading JSON needs it in: the slot, the record row that says what
// the run cost, and the command that opens the conversation again.
//
// The command names the slot rather than leaving it to `--continue`, which is
// the whole point of stating it: "the most recent conversation" is whichever
// run finished last, and on a machine working a backlog that is not this one.
// A run whose store never opened has no slot and states none of the three,
// because a handle that opens nothing is worse than none.
func (c *headlessChat) handles(rec *observeRecorder) headlessHandles {
	if c == nil || c.slot == "" {
		return headlessHandles{}
	}
	h := headlessHandles{chat: c.slot, session: hookSession(rec.sessionID())}
	if c.kind != "" {
		h.resume = "shhh " + c.kind + " --resume=" + shellWord(c.slot)
	}
	return h
}

// shellWord is a slot name as one word a shell will hand back unchanged.
//
// Single quotes and not double, and not Go's own quoting either. A slot is a
// timestamp on a run that minted one and whatever a person typed on a run
// that resumed a conversation they named, so the text can hold anything —
// and inside double quotes a shell still expands `$name` and a backquoted
// command, which is the difference between a line somebody pastes and a line
// that runs something. Inside single quotes nothing is special but the quote
// itself, and the closing-reopening dance is the one escape POSIX gives for
// it (pubs.opengroup.org/onlinepubs/9699919799/utilities/V3_chap02.html).
func shellWord(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// keepSources files the run's sources ledger under the slot the save
// settled on, the way a session's ledger lives under its slot, so a
// conversation resumed from it opens with what this run read on /sources.
// It is asked after the save because the slot is not settled until then — a
// name another run had taken is written to with a suffix — and it says so on
// stderr when it cannot, for the reason save does.
// See docs/capabilities/chat.md#what-was-read.
func (c *headlessChat) keepSources(l *web.Ledger) {
	if c == nil || c.db == nil || c.slot == "" || l == nil {
		return
	}
	if err := l.Bind(c.slot); err != nil {
		fmt.Fprintf(os.Stderr, "» what this run read could not be kept with its conversation: %v\n", err)
	}
}

// keepFed is keepSources for a session that saves every turn and streams its
// rows as they land: it binds through the feed, so what the slot already held
// is never put on the stream as this session's reading. It is asked after
// each save, which is where the slot settles and where a slot another run
// took over moves the conversation, and the ledger with it, to one of this
// session's own.
// See docs/capabilities/chat.md#what-was-read.
func (c *headlessChat) keepFed(f *sourceFeed) {
	if c == nil || c.db == nil || c.slot == "" {
		return
	}
	if err := f.bind(c.slot, c.db); err != nil {
		fmt.Fprintf(os.Stderr, "» what this session read could not be kept with its conversation: %v\n", err)
	}
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
	_ = c.db.SetChatResume(slot, storage.ChatResume{Summary: c.summary, Head: project.Head(""), Root: project.Root("."), Steps: c.steps})
}

// reviseAccount takes one reading of the slot's standing account over the
// conversation as the save will keep it — the reopening's reading left out —
// revising the account the slot already carried. A reading that fails, or a
// run with nothing asked in it, leaves the account as it was.
func (c *headlessChat) reviseAccount(a *agent.Accountant, msgs []provider.Message) {
	if c == nil || c.db == nil || c.slot == "" || !a.Enabled() {
		return
	}
	req := agent.AccountRequestFrom(c.summary, c.withoutReading(msgs))
	if req.Empty() {
		return
	}
	if v := a.Account(context.Background(), req); !v.Failed {
		c.summary = v.Account
	}
}

// headlessAccountant is the writer a headless run revises its slot's standing
// account with, or nil for a run a backlog runner started to work a stage of
// an item. A stage's conversation is one the runner deletes once the item
// goes through, and where the item blocks instead the item file is what says
// where it stood. The stage cannot tell which it will be — the block is
// decided steps after its own turn — so it pays for no account either way
// (docs/capabilities/todo.md#a-sprint-is-runs-with-a-session-between-them).
func headlessAccountant(cfg config.Config, env *sessionEnv, ledger *meter.Ledger) *agent.Accountant {
	if item, _ := todoStageStamp(); item != "" {
		return nil
	}
	return newAccountant(cfg, env, ledger)
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
	// What this run wrote is read off the calls that wrote it, where a
	// session hands in its changeset; it is named before the toolset because
	// the git stager reads it, and it is the same list the tree reading
	// subtracts below. Nobody is here to grant a directory mid-run, so what
	// the flags and the config say is the whole scope for the run, and a run
	// with nobody in front of it never pops a browser: nobody is guaranteed
	// to be at the desktop, and the URL reaches the transcript either way.
	var own *writtenByCalls
	asm, err := assembleSession(cmd, &session, assemblyOpts{
		kind: "print",
		toolset: func(sc *scope.Scope) toolsetOpts {
			own = &writtenByCalls{}
			return toolsetOpts{scope: sc, gitWrites: headlessWrites(session, own)}
		},
		register: unattendedRegistration(true, true),
	})
	if err != nil {
		return err
	}
	defer asm.close()
	sc, ts, prices, ledger, env := asm.sc, asm.ts, asm.prices, asm.ledger, asm.env
	red, qgate, procSup := ts.evidence, ts.gate, ts.proc
	cfg := env.cfg

	// Piped stdin is the prompt itself when no argument is given, and extra
	// context for the prompt otherwise (mirroring the chat TUI). It is read and
	// an empty one refused before anything is contained, so a run with nothing
	// to do never starts a disposable container.
	initialPrompt, err := printPrompt(args, cfg)
	if err != nil {
		return err
	}
	if strings.TrimSpace(initialPrompt) == "" {
		return fmt.Errorf("print mode needs a prompt: pass one as an argument or pipe it on stdin")
	}
	if err := toolchainCommandRefusal(initialPrompt); err != nil {
		return err
	}
	if opts.schema != nil {
		initialPrompt += opts.schema.instruction()
	}

	allowlist := append([]string{}, cfg.Behavior.CommandAllowlist...)
	allowlist = append(allowlist, opts.allow...)

	// Headless approved commands run contained when a mechanism is available
	// — there is no human watching, so containment matters most here.
	// --sandbox goes further: a disposable container is created for
	// the run and approved commands exec inside it; if the sandbox cannot be
	// created and verified, the run fails instead of downgrading. The
	// container stands in for the host's containment at the tail's first
	// step, and is torn down after everything the tail opened.
	var box *contained
	if opts.sandbox {
		var cleanup func()
		box, cleanup, err = startPrintSandbox(cmd.Context(), cfg, session.vault.Names(), procSup)
		if err != nil {
			return err
		}
		defer cleanup()
	}
	// The mode a run this shape answers with. It is empty unless --mode auto
	// was given: a run answering with --yes and --allow alone is not in a
	// mode, and borrowing the one a session would have started in would put
	// a reading in the record that nothing here reads.
	recordedMode := ""
	if opts.autoMode {
		recordedMode = agent.ModeAuto.String()
	}
	// The item and the stage a driver started this run for, where one did.
	// They are the run's own facts and not the config's, which is why they
	// are filled here beside the mode rather than read inside the allowlist.
	runItem, runStage := todoStageStamp()
	// The containment, what the model is told of it, the hooks, the
	// conversation and its slot, and the record, in the order a served
	// session takes them too. A conversation reaches all of this and can run
	// none of it, so it is told about the containment only where it has the
	// tool the containment is about.
	tail, err := finishUnattended(cmd, asm, &session, tailOpts{
		kind:        "print",
		contained:   box,
		sayCommands: offersCommands(session.toolDefs),
		settings: runSettings{
			mode:       recordedMode,
			item:       runItem,
			stage:      runStage,
			rounds:     roundCapFor(opts.rounds(cfg)),
			classifier: opts.autoMode,
		},
	})
	if err != nil {
		return err
	}
	defer tail.close()
	run, containRefusal, hooks, hookCwd := tail.run, tail.refusal, tail.hooks, tail.hookCwd
	saved, messages, recorder := tail.saved, tail.messages, tail.recorder

	a := agent.New(messages, env.stream)
	a.SetSteering(steering(cfg, env.prompts))
	a.SetProgressIntervals(cfg.Behavior.ProgressIntervalCalls,
		time.Duration(cfg.Behavior.ProgressIntervalSeconds)*time.Second)
	a.SetScrub(session.vault.ScrubMessage)
	if session.skills.Len() > 0 {
		a.KeepResults(skill.IsContent)
	}
	// And where a result it does elide goes. An unattended run recovers its
	// window at every round boundary rather than ahead of a person's request,
	// so it trims far more often than a session does — and there is nobody
	// here to notice a finding gone and ask for it again. The id the
	// placeholder names is one this run's own evidence tool reads.
	// See docs/capabilities/evidence.md#a-trim-makes-the-same-promise.
	a.StoreElided(red.Keep)
	// Auto mode's judge, where --mode auto asked for one: the same
	// classifier a session runs, with the one answer this surface cannot
	// give taken away (approvals.go). It reads the run's own conversation as
	// it stands at the round the call was made in.
	var judge *autoJudge
	classifier := buildClassifier(cfg, env, ledger)
	if opts.autoMode {
		judge = &autoJudge{ctx: cmd.Context(), classifier: classifier, recent: a.Messages, cwd: hookCwd,
			allowHosts: cfg.Web.AllowHosts, denyHosts: cfg.Web.DenyHosts}
	}

	// Sub-agent orchestration: spawn_agent and agent_report short-circuit on
	// the executor chain, and Close cancels the child tree and removes the
	// worktrees its writers were given — the same defer a session tears its
	// children down on, so a run that is killed after spawning leaves no
	// worktree behind either.
	//
	// A child works under this run's policy and not one of its own: --yes is
	// the blanket grant a session records when the user answers [a], and a
	// child that did not inherit it would block on a card nobody can draw.
	//
	// The ceiling is auto whether or not --mode auto was given, because
	// there is no mode that spells "--yes". Auto is the nearest one — edits
	// apply, the allowlist runs, and everything else is judged — and the
	// grants beside it are what make the difference: with them a `--yes`
	// child runs its commands unasked, and without them an auto-mode child
	// reaches the same classifier and the same fail-closed refusal the run
	// itself does.
	// See docs/capabilities/subagents.md#a-child-answers-to-the-session.
	var sup *subagent.Supervisor
	exec := ts.executor(session)
	if session.agents {
		// An unattended run seeds a writer's worktree from `git diff HEAD`
		// and nothing else. A session names the untracked files it created
		// out of its changeset; this run keeps no changeset, and reading the
		// tree for them instead would carry a person's scratch files into
		// every worktree (docs/capabilities/subagents.md#a-writer-starts-from-your-tree).
		sup = buildSupervisor(cmd.Context(), asm, session, recorder, classifier, hooks, nil)
		sup.SetParentMode(agent.ModeAuto)
		sup.SetParentGrants(agent.Grants{AllEdits: opts.yes, AllCommands: opts.yes, Commands: opts.allow})
		sup.SetConversation(a.Messages)
		defer sup.Close()
		exec = sup.WrapExecutor("", exec)
	}

	// Repeat detection goes on outside the shared chain, so it sees every
	// tool the chain can dispatch and the result the model will actually
	// read. A headless run needs it most: there is nobody watching to notice
	// the same search going round for the third time.
	// The tool seams go outside it, so a hook behind a call reads the result
	// the model will actually read — the repeat detector's notice included
	// (hooks.go).
	//
	// One detector for the run, because the two tiers are one history: the
	// approver below is wrapped with this same one, so the command it runs
	// and the search the chain dispatches are counted in the same window.
	repeats := agent.NewRepeatDetector()
	var gate func(provider.ToolCall) bool
	a.SetExecutor(agent.ToolExecutor(hooks.WrapExecutor(hookPos(a.Rounds),
		func(name string, args json.RawMessage) bool {
			return gate(provider.ToolCall{Name: name, Arguments: string(args)})
		},
		hook.Executor(repeats.WrapExecutor(exec)))))
	a.SetMaxRounds(opts.rounds(cfg))
	// A run with nobody in front of it is one turn by construction, which is
	// the turn its events are filed under (headlessObserver.pos). The
	// conversation is stamped with the same one, so the round a call was made
	// in still finds the words it was made with when the slot is read back
	// (docs/capabilities/sessions-and-memory.md#a-round-can-be-read-back).
	a.SetTurn(1)

	// The stream, where one was asked for. It is opened here rather than at
	// the first event so that a consumer that read nothing still sees the
	// close line, and it is nil for every other shape, which every write to
	// it is safe under.
	var events *jsonlStream
	if opts.output == outputJSONL {
		events = newJSONLStream(os.Stdout)
	}
	obs := headlessObserver{rec: recorder, rounds: a.Rounds, stream: events, sources: newSourceFeed(session.sources)}
	// The reading a run closes on is not waited for, so it can land after
	// the loop has returned — and a one-shot has nowhere to put it by then:
	// the stream's last line has been written, the record is being closed,
	// and neither is something a late goroutine may still be writing behind.
	// So it is dropped by rule rather than by however fast the process
	// exits. A surface that outlives its runs is where a closing reading is
	// recorded.
	var lateMu sync.Mutex
	runOver := false
	summary := func(v agent.SummaryVerdict) {
		lateMu.Lock()
		defer lateMu.Unlock()
		if runOver {
			return
		}
		obs.summary(v)
	}
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
	gate = unattendedGate(webTools, procSup, mcpTools, sup)
	// A non-interactive run has nobody to notice it has drifted or that it
	// already has what it needs, which is why readings default on here. The
	// prompt is the instruction every one of them is judged against.
	// It reads what this run has changed off the same list the tree reading
	// and the git stager read, so a reading judging whether the run has
	// started acting is not left to infer it from rows that are all reads.
	summaryRun := agent.NewSummaryRun(
		newSummarizer(cfg, env, ledger, cfg.HeadlessSummaryEnabled()),
		agent.NewRecorder(0), initialPrompt).WithChanges(own.changed).
		WithSweeps(repeats.Sweeps)
	// Every verdict reaches the record and the stream through the observer,
	// and is remembered on its way past: a denial still standing when the
	// model stops is what says this run was refused rather than finished.
	verdict := &lastVerdict{}
	// A command a built-in reader answers says so under its output, once
	// per tool in the turn. The runner is wrapped so the line follows a
	// command that ran and never a refusal (nudge.Turn.Ran).
	nudges := &nudge.Turn{}
	resolve := headlessApprover(cmd.Context(), headlessApproval{
		opts:           opts,
		allowlist:      allowlist,
		denylist:       cfg.Behavior.CommandDenylist,
		run:            nudges.Ran(run),
		containRefusal: containRefusal,
		red:            red,
		record:         verdict.wrap(obs.decision),
		webTools:       session.web,
		procSup:        procSup,
		mutationHook:   chainMutation(lspMutationHook(session.lsp), hookPostMutation(hooks)),
		scope:          sc,
		mcpTools:       session.mcpTools,
		structTools:    session.structural,
		un:             unattended{sup: sup, judge: judge, at: obs.pos, conversation: conversationReads(session.conversation, cfg.Web.DenyHosts)},
	})
	// The gated tier is where an unattended run circles: the test command
	// that fails the same way every round, the edit a policy refuses every
	// time it is proposed. Neither reaches the executor chain — the approver
	// runs the command and dispatches the mutation itself — so the detector
	// is put on here as well, innermost, where the result it keys on is the
	// one the tier produced rather than one a seam has since added to.
	resolve = repeats.WrapResolver(resolve)
	// Outside the detector, which keys on the result: a line on the first
	// of two identical commands and not the second would hide the repeat.
	// See docs/capabilities/coding-agent.md#the-built-in-tools-come-before-the-shell.
	resolve = nudges.WrapResolver(func() int64 { return obs.pos().Turn }, resolve)
	// A supervisor blocks on its event channel, so a run that spawned a
	// child and read nothing would stop the child at its first routed
	// request and itself behind it. What this run's own calls wrote is where
	// a landed patch is added, so the tree reading, the close gate and the
	// git stager all see a child's work as this run's (subagents.go).
	answerChildAsks(sup, opts.answered(), own.wrote, nil, obs.childLives(sup))
	// Three readers want that list — the tree check, as the subtrahend for
	// what somebody else changed; the close run, to know whether this turn
	// changed anything worth checking; and the git stager, which may stage
	// nothing else — so it is kept whether or not the tree check is on.
	resolve = own.wrap(resolve)
	// The hooks go outside the reader of what this run wrote, so a call a
	// hook refused is not counted as one and a call it rewrote is counted as
	// the call that ran. The containment refusal is answered in front of
	// them, so a command that can never run fires no hook (hooks.go).
	resolve = unattendedHooks(hooks, hookPos(a.Rounds), hookNoteLine, verdict.wrap(obs.decision), containRefusal, procSup, resolve)
	// And outside all of it, what this run actually offered. A name nothing
	// registered is not a call, so it is not a hook's event and not a path
	// this run wrote either.
	resolve = onlyRegistered(session.toolDefs, resolve)
	if c := headlessTree(cfg, session.sibling, own); c != nil {
		a.SetTreeCheck(*c)
	}
	// The compaction seams sit on the recovery step itself, which is where
	// an unattended run recovers its window (hooks.go).
	compact := headlessCompactor(cmd.Context(), cfg, env, ledger, prices, session.toolDefs)
	hookCompaction(compact, hooks, hookPos(a.Rounds), nil, hookNoteLine)
	h := &agent.Headless{
		Agent:   a,
		Compact: compact,
		Summary: summaryRun,
		OnProgress: func(text string) {
			obs.progress(text)
		},
		OnIntervene: func(iv agent.Intervention) {
			fmt.Fprintf(os.Stderr, "» %s\n", iv.Notice)
			obs.intervene(iv)
		},
		OnSummary:  summary,
		OnWithheld: obs.withheld,
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
			written: own.paths, before: quality.TakeFingerprint(qgate.Workspace),
		}
		h.OnClose = closing.close
		// The gate's verdict is the unattended turn's standing bad news, and
		// the reading that decides whether to steer needs it: a run that is
		// on target with a red suite is not the run that has drifted, and
		// only one of the two is worth interrupting.
		summaryRun.WithAlerts(closing.alerts)
	}
	// The schema is asked after the checks, because it is about the answer
	// and the answer that counts is the one written after the last hand-back
	// the checks made. Both hand back the same way, as a user message.
	var shape *schemaClose
	if opts.schema != nil {
		shape = &schemaClose{schema: opts.schema, truncated: h.TruncatedReply}
		checks := h.OnClose
		h.OnClose = func(final string) string {
			if checks != nil {
				if fb := checks(final); fb != "" {
					return fb
				}
			}
			return shape.close(final)
		}
	}
	// Where the answer goes as it is written: stdout for a person or a
	// `$(...)`, the stream for a consumer reading events, and nowhere at all
	// for the transcript shape, which states the whole answer at the end.
	switch opts.output {
	case outputText:
		// Under a schema the answer is data, and stdout carries it once it
		// has been checked rather than every attempt as it is written.
		if shape == nil {
			h.OnText = func(text string) { fmt.Fprint(os.Stdout, text) }
		}
	case outputJSONL:
		h.OnText = obs.text
	}

	// A signal reaching this run stops the turn, and the handler is up for
	// exactly as long as there is a turn to stop: once the loop has returned,
	// the save and the record below are as killable as they were before this
	// existed.
	// See docs/capabilities/headless.md#what-a-signal-does-to-a-run.
	stopSignals := interruptOnSignal(h.Interrupt)
	// The turn boundary, which is where the TUI takes a server's re-listing
	// too: a list-changed notification that arrived while the session was
	// still assembling is applied before the first round rather than parked
	// for a session that has no later boundary
	// (docs/capabilities/mcp.md#a-server-may-change-what-it-offers).
	mcpTurnBoundary(session.mcpTools)
	final, err := h.Run(initialPrompt)
	stopSignals()
	// A turn that finished is held to the schema once more, after the
	// hand-back has been spent: an answer that still misses is the run's
	// ending, and it is a failed turn in the record like any other.
	var answer json.RawMessage
	if err == nil && shape != nil {
		answer, err = shape.check(final)
	}
	// And again on the other side of it, because an unattended run is one
	// turn: a server that went mid-run has no later boundary to be said at,
	// and stderr is where this run's other diagnostics are
	// (docs/capabilities/mcp.md#a-server-that-dies-is-noticed).
	mcpTurnBoundary(session.mcpTools)
	// Past here the record and the stream belong to the shutdown below, and
	// a reading still out is on its own.
	lateMu.Lock()
	runOver = true
	lateMu.Unlock()
	runErr = err
	// Whatever ended it, the conversation is left where the next `shhh chat
	// --continue` will find it, and the record is told which slot that is —
	// the name is the only thing joining what the run cost to what it said,
	// and it is not known until the save has settled where the words went.
	// The slot's standing account is revised from what the run did before
	// the save that carries it, so a run resumed with --resume says what it
	// did (docs/capabilities/sessions-and-memory.md#a-title-you-did-not-write).
	saved.reviseAccount(headlessAccountant(cfg, env, ledger), a.Messages())
	saved.save(a.Messages())
	recorder.link(saved.slot)
	// Where this run can be picked up, in the three forms both JSON shapes
	// state. It is read after the save because the slot is not settled until
	// the save has landed: a name another run had taken is written to with a
	// suffix, and a handle naming the name this run asked for would open
	// somebody else's conversation.
	left := saved.handles(recorder)
	switch {
	case opts.output != outputText:
	case shape != nil:
		if answer != nil {
			fmt.Fprintln(os.Stdout, string(answer))
		}
	case final != "" && !strings.HasSuffix(final, "\n"):
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
	// The turn closing and the run stopping, in that order, because that is
	// the order they happen in: an unattended run is one turn, and the seam
	// that ends it is not the seam that ends the process.
	hookNoteLine(hooks.TurnClose(cmd.Context(), hookPos(a.Rounds)(), final))
	hookNoteLine(hooks.Stop(cmd.Context(), hookPos(a.Rounds)(), final))
	outcome := headlessTurnOutcome(runErr)
	code := headlessExitCode(outcome, gateErr != nil, refused)
	switch opts.output {
	case outputJSON:
		if err := writeJSONTranscript(os.Stdout, jsonRun{
			messages: a.Messages(), final: final, answer: answer,
			// Under a schema a cut answer is a miss rather than a label: the
			// check above refused it, so what is quoted is never half a value.
			truncated: h.TruncatedReply() && shape == nil,
			usage:     usage, gate: closing.state(), written: own.paths(), sources: session.sources.List(),
			left: left, err: out,
		}); err != nil {
			return err
		}
	case outputJSONL:
		obs.sourcesRead()
		events.closed(obs.pos(), outcome, code, final, answer, usage, left, out)
	}
	// What the run read goes with its conversation, so the session that
	// resumes the slot has it on /sources. It is bound here and not beside the
	// save: binding loads what the slot already held in front of this run's
	// rows, and both shapes above state this run's reading and nobody else's.
	saved.keepSources(session.sources)
	// Nothing to report and nothing to report it as: every code above zero
	// has an error behind it, which is what carries it out to the process.
	if out == nil {
		return nil
	}
	return exitError{code: code, err: out}
}

// printPrompt is the prompt a scripted run was given: its argument, and
// piped stdin as the prompt itself when there is no argument or as context
// for it when there is.
func printPrompt(args []string, cfg config.Config) (string, error) {
	initialPrompt := ""
	if len(args) > 0 {
		initialPrompt = args[0]
	}
	if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
		maxChars := cfg.EffectiveContextMaxTokens() * 4
		content, err := stdin.Read(os.Stdin, maxChars)
		if err != nil {
			return "", err
		}
		if content != "" {
			if initialPrompt == "" {
				initialPrompt = content
			} else {
				initialPrompt = stdin.FormatPromptWithContext(initialPrompt, content)
			}
		}
	}
	return initialPrompt, nil
}

// startPrintSandbox starts the disposable container a --sandbox run's
// approved commands exec inside, in the pieces the tail reads in place of the
// host's containment, and hands back what tears it down.
func startPrintSandbox(ctx context.Context, cfg config.Config, secrets []string, procSup *process.Supervisor) (*contained, func(), error) {
	srun, cleanup, err := startSandbox(ctx, cfg, secrets)
	if err != nil {
		return nil, nil, fmt.Errorf("sandbox: %w", err)
	}
	c := &contained{
		run: srun,
		// The mechanism is settled by the container existing, and the
		// profile only names it: a session told nothing about the profile
		// would still be wrong to be told nothing contains its commands.
		// A ceiling backgrounds a command that is still printing only where
		// there is a supervisor to hand it to, and that is taken back below.
		said: commandEnvironment{Mechanism: "a disposable container", Ceiling: cfg.CommandTimeout(), Backgrounds: procSup != nil},
		// No hook runs in this run: a hook cannot follow the commands into the
		// disposable container, and running it on the host instead would put
		// the person's own command line outside the strongest containment this
		// run has — the same answer, for the same reason, a process start gets.
		// See docs/capabilities/containment.md#a-started-process-is-contained-too.
		noHooks: "» hooks: none run in a --sandbox run; a hook cannot follow the commands into the container",
	}
	// The container took the same profile the spec parsed; a name the
	// parser refused could not have started it.
	if profile, err := sandbox.ParseProfile(cfg.Sandbox.Profile); err == nil {
		c.profile = string(profile)
		c.said.Profile = string(profile)
		c.said.Network = profile != sandbox.ProfileWorkspaceNetless
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
			Wrap: func(string, []string, []string) ([]string, error) {
				return nil, fmt.Errorf("a long-running process cannot be started inside this run's disposable container; use execute_command, or run without --sandbox")
			},
		})
		// The command ceiling answers to the same fact. A command that
		// will not finish here is running in the container and the local
		// process is the exec client, so handing that to the supervisor
		// would put a name and a stop verb on something that is not the
		// process. It is stopped at the ceiling instead.
		// See docs/capabilities/containment.md#a-started-process-is-contained-too.
		runner.SetAdopter(nil)
		c.said.Backgrounds = false
	}
	return c, cleanup, nil
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
	// A rebuilt conversation opens on the checkout as it is now, the same
	// reading the session takes after its own compaction.
	c.Workspace = func(system string) string {
		return project.ReplaceBlock(system, env.workspaceBlock())
	}
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
	// precisely the point there is no room to fail. Which name is tried is
	// the bounded-call chain's answer — summary.model, then
	// provider.cheap_model — without the provider's small model, whose window
	// nothing here has vouched for.
	if name := strings.TrimSpace(resolveFlow(cfg, flowCompaction, env.provName, env.modelName).model); name != "" && name != env.modelName {
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
	// before is the tree as it stood when the turn began. A turn's change
	// does not have to arrive through a mutating call — `gofmt -w`, a code
	// generator, a build that writes its own manifest — and a close that
	// asked only the call log would run nothing over a tree that had moved.
	before quality.Fingerprint

	fed int
	// mu guards the verdict and whether the suite ran. The close runs on the
	// run's own goroutine and the turn's readings are taken on theirs, and a
	// reading that goes out while the gate is running is exactly the one that
	// most wants the answer.
	mu   sync.Mutex
	last *quality.Result
	// ran says the suite was started, which is what separates the two
	// answers a nil result carries: a turn with nothing to check, and one
	// whose suite could not be run.
	ran bool
}

// close is the agent.Headless.OnClose hook.
func (g *headlessCloseGate) close(string) string {
	// A turn that wrote nothing, or wrote only under shhh's own state
	// directory, has nothing a suite could have an opinion about
	// (changeset.AnyCheckable). It runs nothing and says nothing — but only
	// where the tree agrees with the call log, because the call log is not
	// the whole of what a turn can change.
	if !changeset.AnyCheckable(g.written()) && quality.TakeFingerprint(g.gate.Workspace) == g.before {
		return ""
	}
	g.mu.Lock()
	g.ran = true
	g.mu.Unlock()
	res, err := g.gate.Run(g.ctx, g.suite)
	if err != nil {
		// The only error Run reports is a run already in flight, which here
		// means a suite the model asked for itself is still going. Its
		// verdict is the one the turn is about to be judged on anyway.
		return ""
	}
	g.mu.Lock()
	g.last = res
	g.mu.Unlock()
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

// state is what the close did about the project's checks, in the three
// answers a reader of the transcript needs. It is the field a backlog run
// reads instead of the process's exit status: a turn that ran no checks and
// a turn whose checks passed both exit 0, and only one of them says anything
// about the tree.
func (g *headlessCloseGate) state() quality.Closing {
	if g == nil {
		return quality.ClosingNotRun
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.ran || g.last == nil {
		return quality.ClosingNotRun
	}
	return quality.ClosingOf(g.last.Verdict)
}

// alerts is the last verdict as the standing bad news a reading is given: the
// gate's own word for how it came back, and a row per check that is not
// green. A pass, a cancellation and a turn that ran no suite say nothing —
// this field is what the digest calls failing checks, and a reader handed an
// empty one for a green tree would be told the checks are a subject when they
// are not.
//
// It states a check's name and how it came back and never a byte the check
// printed. The formatted verdict carries the tail of each failing check's
// output, which is exactly the material an outside party can write into — a
// test that prints what a fetched fixture said — and this evidence becomes
// the instruction a drifting run is steered with.
// See docs/capabilities/coding-agent.md#the-verdict-is-a-steering-signal-so-the-digest-is-a-boundary.
func (g *headlessCloseGate) alerts() []string {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.last == nil {
		return nil
	}
	switch g.last.Verdict {
	case quality.VerdictFail, quality.VerdictBlocked:
	default:
		return nil
	}
	// The gate's own sentence for a blocked run — an unknown suite, a check
	// that never started — is shhh's text and not the check's, and without it
	// "blocked" is a word with no way to act on it.
	head := fmt.Sprintf("quality gate %q — %s", g.last.Suite, g.last.Verdict)
	if g.last.Verdict == quality.VerdictBlocked && g.last.Reason != "" {
		head += ": " + g.last.Reason
	}
	rows := []string{head}
	for _, c := range g.last.Checks {
		if c.OK() {
			continue
		}
		rows = append(rows, c.Name+" — "+checkOutcome(c))
	}
	return rows
}

// checkOutcome is how one check came back, in the closed set the transcript's
// outcome words come from.
func checkOutcome(c quality.CheckResult) string {
	switch {
	case c.Err != "":
		return "did not run"
	case c.TimedOut:
		return "timed out"
	}
	return fmt.Sprintf("exit %d", c.ExitCode)
}

// err is what the last verdict says about the exit code. A pass, a
// cancellation and a turn that never ran the suite are all nil: cancelled is
// the run being stopped, which the interrupt already answers for.
func (g *headlessCloseGate) err() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.last == nil {
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
//
// A turn the provider refused is `rejected` rather than `failed` for the
// opposite reason: the two are one event only to a person watching, and to a
// script they are opposite instructions — wait, or stop and fix something.
// The record is where the exit code is read off, so the distinction has to
// exist there for the status to be able to carry it.
func headlessTurnOutcome(err error) string {
	switch {
	case err == nil:
		return observe.TurnDone
	case errors.Is(err, agent.ErrRoundCap):
		return observe.TurnCapPaused
	case errors.Is(err, agent.ErrInterrupted):
		return observe.TurnCancelled
	case providerRefusedRequest(err):
		return observe.TurnRejected
	}
	return observe.TurnFailed
}

// providerRefusedRequest reports whether the run ended because the provider
// objected to the request itself, which is the one thing a status can say
// that tells a script not to come back later and try the same call again.
//
// The classes are named rather than derived from Failure.Recoverable, and the
// difference is what happens to a class nobody has thought about here yet. A
// class this list does not know falls to the outcome it fell to before this
// existed, so a reader who adds one gets today's answer until somebody
// decides otherwise; taking "not recoverable" as the rule would instead
// enrol every future class — and, today, a malformed response and an
// unclassified failure — into a status that tells its reader their
// configuration is wrong, on no evidence that it is.
func providerRefusedRequest(err error) bool {
	f, ok := provider.AsFailure(err)
	if !ok {
		return false
	}
	switch f.Class {
	case provider.ClassAuth, provider.ClassQuota,
		provider.ClassContextLength, provider.ClassModelNotFound:
		return true
	}
	return false
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
