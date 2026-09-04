package agent

// Shared approval-policy helpers used by both front-ends: the chat TUI's
// session policy and headless print mode's --allow flags.

import (
	"path/filepath"
	"strings"

	"github.com/rfizzle/shhh/internal/safety"
)

// allowlistUnsafe are shell metacharacters that could chain a second command
// onto an allowlisted prefix (e.g. "git status; rm -rf ~" prefix-matching the
// entry "git status"), so commands containing any of them never match.
const allowlistUnsafe = ";&|`$()<>\n"

// AllowlistMatches reports whether command's leading words exactly match all
// words of some allowlist entry ("go test" matches "go test ./...").
func AllowlistMatches(allowlist []string, command string) bool {
	if strings.ContainsAny(command, allowlistUnsafe) {
		return false
	}
	return anyPrefix(allowlist, command)
}

// DenylistMatches reports whether any command in the line is one the deny
// list names, by the same leading-words rule the allowlist uses. It is asked
// before anything that can allow, and nothing downstream can overrule it.
//
// What counts as "a command in the line" is the safety package's reading of
// it, which the two gates share on purpose. Both are asked the same question
// — is this line going to run that — and a spelling one of them learns and
// the other does not is a hole in whichever was not told.
//
// It is the opposite reading of a chain from the allowlist's. A line an
// allowlist cannot parse is a line it refuses to match, and the reader is
// asked; the cost of over-reading there is a prompt. A line a deny list
// cannot parse would be a line it lets through, and `git status && git push`
// is exactly the spelling somebody would reach for.
func DenylistMatches(denylist []string, command string) bool {
	if len(denylist) == 0 {
		return false
	}
	for _, cmd := range safety.Commands(command) {
		if denies(denylist, cmd) {
			return true
		}
	}
	return false
}

// denies matches one command against the list, as it was written and again
// with the program's own name in place of the path it was reached by:
// `/usr/bin/git push` is a `git push`. Both spellings are tried rather than
// only the second, because an entry that names a path of its own —
// `./scripts/deploy.sh` — has to keep matching the way it was written.
func denies(denylist []string, cmd string) bool {
	if anyPrefix(denylist, cmd) {
		return true
	}
	words := strings.Fields(cmd)
	if len(words) == 0 {
		return false
	}
	base := safety.BaseName(words[0])
	if base == words[0] {
		return false
	}
	words[0] = base
	return anyPrefix(denylist, strings.Join(words, " "))
}

// anyPrefix reports whether command's leading words are exactly the words of
// some entry. It is the matching rule both lists share, so a person who has
// learned how one of them reads a command has learned the other.
func anyPrefix(entries []string, command string) bool {
	words := strings.Fields(command)
	for _, entry := range entries {
		pattern := strings.Fields(entry)
		if len(pattern) == 0 || len(pattern) > len(words) {
			continue
		}
		match := true
		for i, w := range pattern {
			if words[i] != w {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// GrantPrefix is the allowlist entry [a] records for one command:
// its leading bare words, and nothing past the first argument.
//
// The blanket grant [a] used to hand out — every command, for the rest of the
// session — was the only rung above "this once", so a reader who wanted to
// stop being asked about `go test` had to stop being asked about everything.
// The prefix is the rung in between, and it is derived rather than typed
// because a decision surface is the wrong place to compose a pattern: the
// card shows what the key will grant, and the reader either recognises it or
// presses [n].
//
// A bare word is a command or a subcommand — letters, digits and the
// punctuation that shows up inside tool names. The first word that is not one
// is an argument (a path, a flag, a glob), and the grant stops in front of
// it, which is what makes `go test ./internal/...` grant `go test` and
// `npm run lint` grant all three of its words. The first word is always kept:
// `./scripts/release.sh` is the whole name of what it runs.
func GrantPrefix(command string) string {
	words := strings.Fields(command)
	if len(words) == 0 {
		return ""
	}
	n := 1
	for _, w := range words[1:] {
		if !bareWord(w) {
			break
		}
		n++
	}
	return strings.Join(words[:n], " ")
}

// bareWord reports whether w is a command or subcommand rather than an
// argument. A flag, a path, a glob and anything carrying shell punctuation
// are all arguments, and the grant stops in front of the first of them.
func bareWord(w string) bool {
	if w == "" {
		return false
	}
	for i, r := range w {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_':
			// Leading punctuation is a flag ("-v", "--fast"), not a word.
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// PathUnder reports whether path is inside one of dirs — the shape of the
// edit grant [a] records, which is the edited file's own directory rather
// than every edit in the session.
//
// Both sides are made absolute before they are compared, because the model
// writes whichever kind of path it happens to be holding and the grant has to
// mean the same thing either way. Symlinks are not resolved: the paths are
// compared as written, so a link planted inside a granted directory points
// wherever it points. That is the same trust the config allowlist extends to
// its own entries, and the grant is the reader's own act.
func PathUnder(dirs []string, path string) bool {
	if len(dirs) == 0 || path == "" {
		return false
	}
	target := absClean(path)
	for _, dir := range dirs {
		d := absClean(dir)
		if target == d {
			continue
		}
		if strings.HasPrefix(target, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// absClean resolves p against the working directory and cleans it, falling
// back to a plain clean where that cannot be done.
func absClean(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

// Grants are the session approval grants one surface hands another: the
// scoped ones [a] records on a card, and the blanket ones `/mode allow` sets.
// They travel as a struct because sub-agents inherit all four and a
// signature that grows a parameter per grant is a signature that drifts.
type Grants struct {
	// AllEdits and AllCommands are the blanket grants: every edit, every
	// command, for the rest of the session.
	AllEdits    bool
	AllCommands bool
	// EditDirs are directories edits are allowed under, and Commands are
	// allowlist entries in GrantPrefix's shape.
	EditDirs []string
	Commands []string
}

// Any reports whether anything has been granted at all.
func (g Grants) Any() bool {
	return g.AllEdits || g.AllCommands || len(g.EditDirs) > 0 || len(g.Commands) > 0
}
