// Package hostgit is how shhh runs git itself, on the host and uncontained:
// the tree reading between rounds, the workspace survey, the quality
// fingerprint, the changeset tracker, a writer's worktree, the backlog
// runner's commit and the git tool. Every one of those runs in a checkout a
// contained command may have written, and git reads programs out of a
// checkout — so every one of them takes its environment from here, and all
// but the tool, whose argv is a closed vocabulary of its own, its command too,
// rather than each keeping a copy of the hygiene and the next call site
// keeping none.
// See docs/capabilities/containment.md#the-hosts-own-git-runs-nothing-a-command-wrote.
package hostgit

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// IgnoreSubmodules is the flag every call that compares the working tree
// passes — status, diff, diff-files, diff-index. Without it git asks each
// submodule whether it is dirty by running `git status` inside it, which
// reads that submodule's own store: its config, and so its clean filters.
// A contained command can make a repository, stage it as a gitlink and write
// its store, and the mask over the superproject's store names none of that;
// the host's next status would run the filter it wrote, as the person. The
// flag and not the configuration key below, because a submodule's
// `submodule.<name>.ignore` in `.gitmodules` — a working-tree file — outranks
// the key and loses to the flag.
const IgnoreSubmodules = "--ignore-submodules=all"

// overrides is the configuration every host-side git call is given, through
// the environment rather than -c so the git tool's argv stays a closed
// vocabulary.
//
//   - core.fsmonitor names a program git execs on status, diff and blame, and
//     no flag turns it off.
//   - diff.ignoreSubmodules is IgnoreSubmodules for the verbs that take no
//     such flag: a commit whose index adds a `.gitmodules` asks the
//     submodule whether it is dirty otherwise. It costs a history reading
//     the lines that say a submodule's pointer moved.
//   - submodule.recurse would have a switch check a submodule out, running
//     that submodule's filters and hooks, when somebody's global
//     configuration turns it on.
var overrides = [][2]string{
	{"core.fsmonitor", ""},
	{"diff.ignoreSubmodules", "all"},
	{"submodule.recurse", "false"},
}

// Env is base with shhh's configuration overrides in place; a nil base is
// this process's environment. Whatever GIT_CONFIG_* base carried is dropped
// first: the variables are numbered, so appending to an inherited set would
// renumber it or be renumbered by it, and either way the override this exists
// for is the one that goes missing. GIT_CONFIG_PARAMETERS — what `git -c`
// hands its children — goes too, because git reads it after the numbered set
// and it would win.
func Env(base []string) []string {
	if base == nil {
		base = os.Environ()
	}
	kept := make([]string, 0, len(base)+1+2*len(overrides))
	for _, pair := range base {
		if strings.HasPrefix(pair, "GIT_CONFIG_COUNT=") ||
			strings.HasPrefix(pair, "GIT_CONFIG_KEY_") ||
			strings.HasPrefix(pair, "GIT_CONFIG_VALUE_") ||
			strings.HasPrefix(pair, "GIT_CONFIG_PARAMETERS=") {
			continue
		}
		kept = append(kept, pair)
	}
	kept = append(kept, "GIT_CONFIG_COUNT="+strconv.Itoa(len(overrides)))
	for i, o := range overrides {
		n := strconv.Itoa(i)
		kept = append(kept, "GIT_CONFIG_KEY_"+n+"="+o[0], "GIT_CONFIG_VALUE_"+n+"="+o[1])
	}
	return kept
}

// verbFlags are the flags Command puts after a verb that takes them. Each
// shuts a program the configuration or the working tree can name: an external
// diff and a textconv driver on anything that prints a patch, a signature
// verifier on anything that prints a commit, and the submodule's own git for
// anything that compares the working tree (IgnoreSubmodules). They are put in
// by verb rather than left to each caller because the caller that forgets one
// is the one nobody reviews. diff-tree takes none: it compares two commits,
// starts no driver unless asked to, and has no working tree to ask a
// submodule about.
var verbFlags = map[string][]string{
	"status":     {IgnoreSubmodules},
	"diff":       {"--no-ext-diff", "--no-textconv", IgnoreSubmodules},
	"diff-files": {IgnoreSubmodules},
	"diff-index": {IgnoreSubmodules},
	"log":        {"--no-ext-diff", "--no-textconv", "--no-show-signature"},
	"show":       {"--no-ext-diff", "--no-textconv", "--no-show-signature"},
}

// Command is git run in dir with args, under Env, with --no-pager in front —
// a pager is a program the configuration names, and that is the one global
// flag that shuts it for every verb — and with verbFlags after the verb. An
// empty dir passes no -C, for a caller that sets the command's directory
// itself. The verb is the first argument that is not an option, a `-c`'s
// value skipped; a caller passing any other global option with a value of its
// own would have that value read as the verb, and none does.
func Command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	argv := make([]string, 0, len(args)+6)
	argv = append(argv, "--no-pager")
	if dir != "" {
		argv = append(argv, "-C", dir)
	}
	verb := -1
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" {
			i++
			continue
		}
		if !strings.HasPrefix(args[i], "-") {
			verb = i
			break
		}
	}
	if verb < 0 {
		argv = append(argv, args...)
	} else {
		argv = append(argv, args[:verb+1]...)
		argv = append(argv, verbFlags[args[verb]]...)
		argv = append(argv, args[verb+1:]...)
	}
	cmd := exec.CommandContext(ctx, "git", argv...)
	cmd.Env = Env(nil)
	return cmd
}
