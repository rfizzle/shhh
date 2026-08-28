package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/spf13/cobra"
)

// sessionScope builds a session's working scope (S-141): the directory the
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
// call reaches outside its working scope (S-141). There is nobody to ask, so
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

// scopePromptBlock tells the model where the work is (S-141). A model that
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
