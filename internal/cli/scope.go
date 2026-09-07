package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/spf13/cobra"
)

// sessionScope builds a session's working scope: the directory the
// session was opened in, plus the directories config and --add-dir put beside
// it. A directory named on the command line that cannot be granted fails the
// session — the user typed it and is waiting for it to be in scope — while a
// stale entry in the config file is reported and skipped, because a directory
// that moved months ago should not stop a session starting.
func sessionScope(cfg config.Config, flagged []string) (*scope.Scope, error) {
	ws, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	sc, problems := scope.New(ws)
	if len(problems) > 0 {
		return nil, problems[0]
	}
	for _, dir := range cfg.Behavior.ScopeDirs {
		if _, err := sc.Add(dir); err != nil && err != scope.ErrAlreadyInScope {
			fmt.Fprintf(os.Stderr, "warning: behavior.scope_dirs: %v\n", err)
		}
	}
	for _, dir := range flagged {
		if _, err := sc.Add(dir); err != nil && err != scope.ErrAlreadyInScope {
			return nil, fmt.Errorf("--add-dir: %w", err)
		}
	}
	return sc, nil
}

// addDirFlag registers --add-dir on a command that opens a session. It is the
// start-up form of /add-dir: the same grant, made before there is a session
// to make it in.
func addDirFlag(cmd *cobra.Command, target *[]string) {
	cmd.Flags().StringArrayVar(target, "add-dir", nil,
		"add a directory to the session's working scope (repeatable; extends behavior.scope_dirs)")
}

// headlessScopeCheck answers what a headless run may do about the paths a
// call reaches outside its working scope. There is nobody to ask, so
// the answer is the same one the interactive card would get from a permissive
// mode: an ordinary directory comes into scope under --yes, a sensitive one
// never does, and a directory behind the containment deny mask is refused
// whatever the flags say. Everything granted here is added to the scope, so
// the contained command that follows can actually write there.
func headlessScopeCheck(sc *scope.Scope, yes bool, paths []string) (deny string, ok bool) {
	if sc == nil || len(paths) == 0 {
		return "", true
	}
	dirs := sc.Outside(paths...)
	if len(dirs) == 0 {
		return "", true
	}
	for _, dir := range dirs {
		switch class, reason := scope.Classify(dir); class {
		case scope.Refused:
			return "error: " + dir + " is outside the working scope and cannot be granted (" + reason + ")", false
		case scope.Sensitive:
			return "error: " + dir + " is outside the working scope and is sensitive (" + reason +
				"); a headless run never adds one — pass --add-dir " + dir + " if that is what you want", false
		}
	}
	if !yes {
		return "error: " + dirs[0] + " is outside the working scope; headless runs stay inside it (pass --add-dir " +
			dirs[0] + ", or --yes to let the run add ordinary directories itself)", false
	}
	for _, dir := range dirs {
		if _, err := sc.Add(dir); err != nil && err != scope.ErrAlreadyInScope {
			return "error: " + err.Error(), false
		}
	}
	return "", true
}

// scopePromptBlock tells the model where the work is. A model that
// does not know the boundary spends its rounds proposing calls the user has
// to refuse one at a time; one that does asks for the directory instead,
// which is a sentence the user can answer with /add-dir.
func scopePromptBlock(sc *scope.Scope) string {
	if sc == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Working scope\nThis session works in " + sc.Root())
	for _, d := range sc.Dirs() {
		b.WriteString(", " + d)
	}
	b.WriteString(".\nPaths outside it need the user's approval before anything writes to them, whatever the permission mode says, ")
	b.WriteString("and credential stores cannot be granted at all. ")
	b.WriteString("If the work genuinely needs another directory, say which one and why, and ask the user to run /add-dir <path> — do not work around the boundary.")
	return b.String()
}

// commandEnvironment is what a session resolved about the commands it will
// run, in the pieces the model has to be told: what wraps one, what that
// wrap allows, and how long one may take. It is resolved once, where the
// containment is built, so the prompt cannot say one thing in the first round
// and another in the fortieth.
type commandEnvironment struct {
	// Mechanism is what contains a command; empty is a session whose
	// commands run bare, which is a fact in its own right and not an absence
	// to leave unsaid.
	Mechanism string
	// Profile is the mechanism's named policy, and Network the one question
	// about it a command can fail on without any sign of why.
	Profile string
	Network bool
	// Refused is a session that requires containment on a host with none:
	// nothing it asks for will run, so the block says that instead.
	Refused bool
	// Ceiling is how long one command may take. Zero is a session that does
	// not bound one.
	Ceiling time.Duration
	// Backgrounds says a command still printing when the ceiling arrives is
	// handed to the process supervisor rather than stopped. A surface with no
	// supervisor has nowhere to put one, and a run whose commands are inside
	// a disposable container has nowhere it could be put — so the two say
	// different things about the same number.
	Backgrounds bool
}

// offersCommands reports whether a surface registered the tool the command
// environment describes. A conversation has no runner, and telling it what a
// command of its would run under describes an action it cannot take — which
// is the failure prompt.Toolbox exists to avoid, in the other direction.
func offersCommands(defs []provider.Tool) bool {
	for _, d := range defs {
		if d.Name == tools.ExecCommandName {
			return true
		}
	}
	return false
}

// commandEnvironmentBlock states what a command this session runs can
// actually reach, beside the block that states where it may write.
//
// The netless profile pays for the block on its own: a contained session's
// package install fails on the name lookup, which reads exactly like a broken
// resolver, and a model that has not been told there is no network spends
// rounds on a proxy setting and a second registry before it gives up. Told
// plainly, it says what it needed and works with what is in the checkout.
// See docs/capabilities/containment.md#the-model-is-told-what-its-commands-run-under.
func commandEnvironmentBlock(e commandEnvironment) string {
	var b strings.Builder
	b.WriteString("# Command environment\n")
	switch {
	case e.Refused:
		b.WriteString("This session was told to run its commands contained and this machine has no mechanism to contain them, so every command is refused before it runs. " +
			"Work with the file tools, and say what you would have run rather than trying it a second way.")
		return b.String()
	case e.Mechanism == "":
		b.WriteString("Nothing contains the commands you run: they run on this machine as the user, with the user's filesystem and the user's network. " +
			"The working scope above is the only boundary, and it is enforced when the call is approved rather than by the machine.")
	default:
		fmt.Fprintf(&b, "Commands you run are contained by %s", e.Mechanism)
		if e.Profile != "" {
			fmt.Fprintf(&b, " under the %s profile", e.Profile)
		}
		b.WriteString(": a write lands inside the working scope above and is refused outside it")
		if e.Network {
			b.WriteString(", and the network is open.")
		} else {
			b.WriteString(", and there is no network at all. A download, a package install or an API call fails on the connection itself — that is this profile and not a broken proxy or a bad resolver, so do not debug it as one. Say what you needed and carry on with what is already here.")
		}
	}
	switch {
	case e.Ceiling <= 0:
		// A session that bounds nothing says nothing: a ceiling stated as
		// absent is one more sentence for the model to reason about, and the
		// only surface with no ceiling is the one with a reader at the
		// keyboard who is the ceiling.
	case e.Backgrounds:
		fmt.Fprintf(&b, "\nA command still running after %s is moved to the background as a named process rather than killed, and the result says so; start anything meant to keep running — a server, a watcher — as a process from the outset.", ceilingWords(e.Ceiling))
	default:
		fmt.Fprintf(&b, "\nA command still running after %s is stopped, and the result says it was stopped rather than that it failed.", ceilingWords(e.Ceiling))
	}
	return b.String()
}

// ceilingWords spells a command ceiling the way a sentence needs it: the
// default prints as "10m0s", which is a duration literal and not an English
// one.
func ceilingWords(d time.Duration) string {
	d = d.Round(time.Second)
	if d%time.Minute == 0 {
		if m := int(d / time.Minute); m != 1 {
			return fmt.Sprintf("%d minutes", m)
		}
		return "1 minute"
	}
	return d.String()
}
