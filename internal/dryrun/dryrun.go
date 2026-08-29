// Package dryrun derives the harmless form of a command that has one
// . The one-shot's `[d]` key is an offer to find out what a command
// would do without doing it, and the only honest way to make that offer is to
// know which commands can answer it: `rsync` and `git clean` and `make` were
// built to, `rm` never was.
//
// The derivation is textual and conservative. A line shhh cannot rewrite into
// a no-op stops the whole thing — a "dry run" that half-runs is worse than no
// dry run at all — and a line that already changes nothing is carried through
// unaltered, because a read in the middle of a pipeline is what makes the
// rewrite worth running.
package dryrun

import (
	"strings"

	"github.com/rfizzle/shhh/internal/radius"
)

// Derive returns the dry-run form of command and whether one exists. The
// second return is the whole contract: false means `[d]` is not offered, and
// the surface says nothing about a dry run rather than offering a key that
// would run the real thing.
func Derive(command string) (string, bool) {
	var lines []string
	derived := false
	for _, line := range strings.Split(command, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if d, ok := deriveLine(line); ok {
			lines = append(lines, d)
			derived = true
			continue
		}
		// A line that changes nothing can be carried as it is: running it is
		// what a dry run of the lines around it needs in order to mean
		// anything. Anything else — a write shhh has no no-op for, or a line
		// it could not resolve — ends the offer.
		res := radius.Resolve(line)
		if len(res.Writes) > 0 || len(res.Risks) > 0 || len(res.Unresolved) > 0 {
			return "", false
		}
		lines = append(lines, line)
	}
	if !derived {
		return "", false
	}
	return strings.Join(lines, "\n"), true
}

// rule is one verb's way of being asked rather than told.
type rule struct {
	// sub, when set, is the subcommand the rule applies to (`git clean`).
	sub string
	// flag is inserted after the verb (and subcommand) when the rule fires.
	flag string
	// already names the flags that mean the command is a dry run already, so
	// the offer is not made twice.
	already []string
}

// rules is the closed set of commands that can be asked what they would do.
// Every entry is a claim that the flag makes the command harmless, so the
// table is short on purpose: a wrong claim here runs the real command while
// the surface says it did not.
var rules = map[string][]rule{
	"rsync":            {{flag: "--dry-run", already: dashN}},
	"make":             {{flag: "-n", already: []string{"-n", "--just-print", "--dry-run", "--recon"}}},
	"ansible-playbook": {{flag: "--check", already: []string{"-C", "--check"}}},
	"git":              subRules("--dry-run", dashN, "clean", "rm", "mv", "push"),
	"npm": subRules("--dry-run", []string{"--dry-run"},
		"install", "i", "ci", "uninstall", "prune", "publish", "update", "dedupe"),
	"pnpm": subRules("--dry-run", []string{"--dry-run"},
		"install", "add", "remove", "update", "publish", "prune"),
	"apt":     subRules("--dry-run", simulate, "install", "remove", "purge", "upgrade", "autoremove"),
	"apt-get": subRules("--dry-run", simulate, "install", "remove", "purge", "upgrade", "dist-upgrade", "autoremove"),
	"kubectl": subRules("--dry-run=client", []string{"--dry-run"},
		"apply", "create", "delete", "replace", "patch"),
	"helm": subRules("--dry-run", []string{"--dry-run"},
		"install", "upgrade", "uninstall", "rollback"),
}

var (
	dashN    = []string{"-n", "--dry-run"}
	simulate = []string{"-s", "--dry-run", "--simulate"}
)

// subRules spells one flag out across the subcommands it applies to. A verb
// whose dry-run flag only means something for some of what it does gets an
// entry per subcommand rather than a blanket one: `npm install --dry-run`
// reports, and `npm run build --dry-run` runs the script anyway.
func subRules(flag string, already []string, subs ...string) []rule {
	out := make([]rule, 0, len(subs))
	for _, sub := range subs {
		out = append(out, rule{sub: sub, flag: flag, already: already})
	}
	return out
}

// deriveLine rewrites one command line, or reports that it has no no-op form.
func deriveLine(line string) (string, bool) {
	if d, ok := deriveSed(line); ok {
		return d, true
	}
	if d, ok := deriveFind(line); ok {
		return d, true
	}
	if d, ok := deriveTerraform(line); ok {
		return d, true
	}
	return deriveFlag(line)
}

// deriveFlag handles the common shape: a verb (with an optional subcommand)
// that takes a flag meaning "tell me instead of doing it".
func deriveFlag(line string) (string, bool) {
	words := split(line)
	i := skipPrefixes(words)
	if i >= len(words) {
		return "", false
	}
	verb := base(words[i].text)
	candidates, ok := rules[verb]
	if !ok {
		return "", false
	}
	for _, r := range candidates {
		at := i
		if r.sub != "" {
			sub := firstOperand(words[i+1:])
			if sub < 0 || words[i+1+sub].text != r.sub {
				continue
			}
			at = i + 1 + sub
		}
		for _, f := range r.already {
			if hasWord(words, f) {
				return line, true // it is already the dry run
			}
		}
		return insertAfter(line, words[at], r.flag), true
	}
	return "", false
}

// deriveSed drops sed's -i: without it sed is the filter it always was, and
// the edit it would have written goes to the terminal instead.
func deriveSed(line string) (string, bool) {
	words := split(line)
	i := skipPrefixes(words)
	if i >= len(words) || base(words[i].text) != "sed" {
		return "", false
	}
	for _, w := range words[i+1:] {
		if w.text == "-i" || strings.HasPrefix(w.text, "-i.") || w.text == "--in-place" {
			return cut(line, w), true
		}
	}
	return "", false
}

// deriveFind turns find's two destructive tails into the listing they are the
// answer to: `-delete` and `-exec <write> …` both become `-print`.
func deriveFind(line string) (string, bool) {
	words := split(line)
	i := skipPrefixes(words)
	if i >= len(words) || base(words[i].text) != "find" {
		return "", false
	}
	for j := i + 1; j < len(words); j++ {
		switch words[j].text {
		case "-delete":
			return replace(line, words[j], words[j], "-print"), true
		case "-exec", "-execdir", "-ok", "-okdir":
			end := execEnd(words, j)
			if end < 0 {
				return "", false
			}
			return replace(line, words[j], words[end], "-print"), true
		}
	}
	return "", false
}

// execEnd finds the word that closes a find -exec clause: `;` or `+`.
func execEnd(words []word, start int) int {
	for j := start + 1; j < len(words); j++ {
		if words[j].text == ";" || words[j].text == "+" {
			return j
		}
	}
	return -1
}

// deriveTerraform swaps apply for the command whose whole job is to say what
// apply would do.
func deriveTerraform(line string) (string, bool) {
	words := split(line)
	i := skipPrefixes(words)
	if i >= len(words) || base(words[i].text) != "terraform" {
		return "", false
	}
	sub := firstOperand(words[i+1:])
	if sub < 0 {
		return "", false
	}
	at := words[i+1+sub]
	switch at.text {
	case "plan":
		return line, true
	case "apply", "destroy":
		return replace(line, at, at, "plan"), true
	}
	return "", false
}

// word is one shell word with the byte range it occupies in the line, which
// is what lets the rewrites be surgical: everything the command said about
// quoting and spacing outside the edit survives untouched.
type word struct {
	text       string
	start, end int
}

// split cuts a line into words, honouring quotes and backslashes and keeping
// each word's position. Quotes are stripped from text and kept in the line.
func split(line string) []word {
	var out []word
	var cur strings.Builder
	var quote rune
	start, started := 0, false
	runes := []rune(line)
	offs := make([]int, len(runes)+1)
	b := 0
	for i, r := range runes {
		offs[i] = b
		b += len(string(r))
	}
	offs[len(runes)] = b
	flush := func(end int) {
		if started {
			out = append(out, word{text: cur.String(), start: start, end: end})
		}
		cur.Reset()
		started = false
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			cur.WriteRune(r)
			continue
		}
		switch {
		case r == ' ' || r == '\t':
			flush(offs[i])
		case r == '\'' || r == '"':
			if !started {
				start, started = offs[i], true
			}
			quote = r
		case r == '\\' && i+1 < len(runes):
			if !started {
				start, started = offs[i], true
			}
			i++
			cur.WriteRune(runes[i])
		default:
			if !started {
				start, started = offs[i], true
			}
			cur.WriteRune(r)
		}
	}
	flush(offs[len(runes)])
	return out
}

// prefixes are the words a line can open with that are not the command.
var prefixes = map[string]bool{
	"sudo": true, "doas": true, "command": true, "nohup": true, "time": true,
	"env": true,
}

// skipPrefixes returns the index of the actual verb.
func skipPrefixes(words []word) int {
	i := 0
	for i < len(words) && (prefixes[base(words[i].text)] || strings.Contains(words[i].text, "=")) {
		i++
	}
	return i
}

// firstOperand is the offset of the first word that is not a flag.
func firstOperand(words []word) int {
	for i, w := range words {
		if strings.HasPrefix(w.text, "-") {
			continue
		}
		return i
	}
	return -1
}

func hasWord(words []word, text string) bool {
	for _, w := range words {
		if w.text == text {
			return true
		}
	}
	return false
}

func base(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// insertAfter puts flag directly behind w.
func insertAfter(line string, w word, flag string) string {
	return line[:w.end] + " " + flag + line[w.end:]
}

// replace swaps everything from first through last for text.
func replace(line string, first, last word, text string) string {
	return line[:first.start] + text + line[last.end:]
}

// cut removes w and the space in front of it.
func cut(line string, w word) string {
	start := w.start
	for start > 0 && (line[start-1] == ' ' || line[start-1] == '\t') {
		start--
	}
	return line[:start] + line[w.end:]
}
