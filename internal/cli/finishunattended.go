package cli

import (
	"fmt"
	"os"

	"github.com/rfizzle/shhh/internal/hook"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/spf13/cobra"
)

// contained is what runs a session's commands, in the pieces the steps after
// it read: the runner, the refusal every command gets where containment is
// required and the host has none, what contains a hook, and what the model
// is told about all of it.
type contained struct {
	run chat.RunFunc
	// profile is the profile the commands are actually under, for the
	// record: empty when nothing contains them.
	profile string
	refusal string
	wrap    func(string) ([]string, error)
	said    commandEnvironment
	// missing is the declared tools the commands will not find.
	missing []string
	// noHooks, where it is set, is what is said on stderr in place of
	// building the hooks, because nothing here can contain one.
	noHooks string
}

// hostContainment is the containment the host has for a session's commands,
// in the pieces the tail reads.
func hostContainment(asm *assembly) (*contained, error) {
	cfg, procSup := asm.env.cfg, asm.ts.proc
	containment, err := buildContainment(cfg, asm.sc, procSup)
	if err != nil {
		return nil, err
	}
	c := &contained{
		run:     runner.RunCaptureResult,
		refusal: containment.Refusal,
		wrap:    containment.Wrap,
		missing: containment.Toolchain.Missing,
		said: commandEnvironment{
			Mechanism: containment.Mechanism,
			Profile:   containment.Profile,
			Network:   containment.Network,
			Hosts:     containment.Hosts,
			Refused:   containment.Refusal != "",
			Ceiling:   cfg.CommandTimeout(),
			// A ceiling backgrounds a command that is still printing only
			// where there is a supervisor to hand it to (process.go).
			Backgrounds: procSup != nil,
		},
	}
	if containment.Run != nil {
		c.run = containment.Run
		c.profile = containment.Profile
	}
	return c, nil
}

// tailOpts is what a surface nobody answers at a keyboard hands the steps
// that follow its assembly: the facts that differ between a scripted run
// and a served session.
type tailOpts struct {
	// kind is how the record names the surface.
	kind string
	// contained is a containment the surface started for itself, nil where
	// the host's own is built.
	contained *contained
	// sayCommands tells the model what its commands run under; a surface
	// that offers no command has nothing for that to describe.
	sayCommands bool
	// initial is a conversation the session begins from in place of the
	// fresh one, nil for the fresh one.
	initial []provider.Message
	// settings is the surface's own part of what the record is stamped
	// with: the mode, the item and stage, the round cap and the classifier.
	settings runSettings
}

// unattendedTail is what the tail hands back: the runner commands go
// through, the refusal they get, the hooks, the conversation and its slot,
// and the record.
type unattendedTail struct {
	run      chat.RunFunc
	refusal  string
	hooks    *hook.Runner
	hookCwd  string
	saved    *headlessChat
	messages []provider.Message
	recorder *observeRecorder

	// closers end what the tail opened, in the reverse order it was opened.
	closers []func()
}

// close ends what the tail opened, last first.
func (t *unattendedTail) close() {
	for i := len(t.closers) - 1; i >= 0; i-- {
		t.closers[i]()
	}
	t.closers = nil
}

// finishUnattended runs the steps a scripted run and a served session both
// take after their assembly, in the one order they keep: the containment,
// what the model is told about it, the runner's wraps, the hooks and their
// first seam, the conversation and its slot, and the record with its stamp.
// On an error it closes what it opened and returns nothing; otherwise the
// caller closes the tail before whatever it opened ahead of it.
// See docs/architecture.md#the-unattended-surfaces-share-one-tail.
func finishUnattended(cmd *cobra.Command, asm *assembly, session *chatSession, opts tailOpts) (_ *unattendedTail, err error) {
	t := &unattendedTail{}
	defer func() {
		if err != nil {
			t.close()
		}
	}()
	env, cfg := asm.env, asm.env.cfg

	c := opts.contained
	if c == nil {
		if c, err = hostContainment(asm); err != nil {
			return nil, err
		}
	}
	t.refusal = c.refusal
	// What the model is told about it, beside where it was told the work is
	// (scope.go). It is joined to the prompt already built because the
	// containment is resolved after the provider is, and the prompt had to
	// exist for that. The declared tools its commands will not find are said
	// beside it, as nothing anyone here can install (toolchain.go).
	if opts.sayCommands {
		env.addBuiltPrompt(commandEnvironmentBlock(c.said))
		env.addBuiltPrompt(toolchainPromptBlock(c.missing, false))
	}
	t.run = scrubResultRunner(session.vault, c.run)
	// Nobody is at a keyboard to cancel a command that will not finish, and
	// the executor it is holding is held until something outside kills it.
	t.run = boundedRunner(t.run, cfg.CommandTimeout())

	// The person's own commands at the session's seams (hooks.go), assembled
	// after the containment because what contains the session's commands is
	// what contains its hooks.
	t.hookCwd, _ = os.Getwd()
	hooked := hookSet(cfg)
	for _, note := range hookNotes(hooked) {
		fmt.Fprintf(os.Stderr, "» hooks: %s\n", note)
	}
	if c.noHooks != "" && hooked.Len() > 0 {
		fmt.Fprintln(os.Stderr, c.noHooks)
	} else {
		t.hooks = buildHooks(cfg, hooked, c.wrap, t.hookCwd)
	}
	// The first seam. The prompt is already built — it had to be, for the
	// provider to be resolved — so what a session-start hook says is joined to
	// it here rather than folded in before it.
	hookStart := t.hooks.SessionStart(cmd.Context())
	hookNoteLine(hookStart)
	if hookStart.Context != "" {
		env.sysPrompt = prompt.CombineExtra(env.sysPrompt, hookStart.Context)
		if len(env.messages) > 0 && env.messages[0].Role == provider.RoleSystem {
			env.messages[0].Content = env.sysPrompt
		}
	}

	// The conversation the session carries on, and the slot it will be left
	// in. The claim happens here rather than at the save so that two sessions
	// started in the same second settle which of them owns the name before
	// either writes a word to it.
	messages := env.messages
	if opts.initial != nil {
		// A fork carries its parent's conversation, and the parent's system
		// prompt with it: the two were built together and a fresh one over an
		// old conversation would describe a different session.
		messages = opts.initial
		session.continueLast, session.resumeName = false, ""
	}
	db := asm.db
	saved, messages, err := openHeadlessChat(db, *session, messages, env.sysPrompt)
	if err != nil {
		return nil, err
	}
	t.saved, t.messages = saved, messages
	// A slot this session claimed and never wrote to is given back on the
	// way out, so a session whose save never landed leaves no name behind
	// for the next one to be given a suffix around. A slot it resumed
	// belongs to whoever made it and is left alone.
	t.closers = append(t.closers, func() {
		if db != nil {
			_ = db.ReleaseChatSlot(saved.slot)
		}
	})

	// Session observability: the same content-free events an interactive
	// session records; failure just disables recording. It is opened before
	// the agent because the sub-agent supervisor is built with it: a child's
	// own row is linked to this one, and a supervisor built first would have
	// no parent to link to.
	t.recorder = startObserveRecorder(db, opts.kind, env.prov.Name(), env.modelName, asm.prices)
	t.closers = append(t.closers, t.recorder.end)
	// The phases paid before this row existed; a run with no screen has no
	// first paint to add to them (startup.go).
	startupFrom(cmd.Context()).Attach(t.recorder.startupRow)
	t.hooks.SetSession(hookSession(t.recorder.sessionID()))
	own := opts.settings
	t.recorder.stamp(env.prompts.fingerprintOf(env.sysPrompt), session.skills.Len(), projectFingerprintRoot(), sessionSettings(cfg, runSettings{
		mode:       own.mode,
		item:       own.item,
		stage:      own.stage,
		effort:     env.effort,
		rounds:     own.rounds,
		checkIn:    checkInFor(cfg.Behavior.CheckInIntervalRounds),
		sandbox:    c.profile,
		model:      auxiliaryModel(cfg, env.provName, env.modelName),
		summary:    cfg.HeadlessSummaryEnabled(),
		classifier: own.classifier,
	}))
	// The gate's verdict, mirroring the interactive session — and a session
	// with nobody in front of it is the one whose verdict the record most
	// needs, because there was no one there to read it on the way past.
	recordGateVerdicts(asm.ts.gate, t.recorder)
	recordSearches(session.web, t.recorder)
	return t, nil
}
