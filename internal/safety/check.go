// Package safety reads a command line and names the shapes in it that a
// person has to answer for themselves. What it finds is never a refusal on
// its own: it moves the safe key to the front of a card and takes the
// answer away from every permission mode, so the decision reaches whoever is
// sitting there.
//
// Most of what it looks for is a verb carrying a set of options, and those
// are a table rather than a regular expression because one act has many
// spellings. `rm -rf`, `rm -r -f`, `rm -fr` and `rm -R --force` are the same
// command written four ways, and a pattern that knows the first is a pattern
// that stops at the first person who types the second. What stays in regular
// expressions is the handful of dangers that are genuinely text — a
// redirection, a pipe into an interpreter, a statement in another language.
package safety

import (
	"regexp"
	"strings"
)

// Warning is one dangerous shape a command matched.
type Warning struct {
	// Pattern names the rule that matched, in the words the rule is written
	// in ("rm -r -f"), so a reader chasing a warning can find the row that
	// produced it. It is a label rather than a spelling to match against:
	// the row it names accepts many.
	Pattern string
	Risk    string
}

// flag is the spellings of one option a rule requires. A command satisfies
// it by carrying any of them, which is what lets one row cover a bundle
// (`-rf`), a letter alone (`-r`), and the long form written out.
type flag struct {
	// letters are short-option letters, any one of which satisfies the flag.
	// They are looked for inside a bundle as well as alone, because `-rf`
	// and `-r -f` are the same command.
	letters string
	// words are options matched whole: the long forms, and the single-dash
	// long options a few programs use (`-delete`).
	words []string
}

// rule is one dangerous command shape: the words it starts with, the options
// it must carry, an optional question about what it is pointed at, and what
// it costs.
type rule struct {
	// name labels the row, for the Warning it produces.
	name string
	// verb is the leading words, matched exactly. It is a list because the
	// dangerous thing is usually a subcommand: `git push` is not `git`.
	verb []string
	// flags must all be satisfied. An empty list matches the verb alone, so
	// only a rule with a where clause should leave it out.
	flags []flag
	// where asks about the operands the command names, for the rules whose
	// danger is in what they point at rather than in how they were spelled.
	// Nil means the flags are the whole of it.
	where func(opts options) bool
	risk  string
}

// rules are the verb-shaped dangers, most specific first: Check reports the
// first row that matches a line, so `rm -rf /` has to be read before the
// general recursive delete underneath it.
var rules = []rule{{
	name: "rm -rf /",
	verb: []string{"rm"},
	flags: []flag{
		{letters: "rR", words: []string{"--recursive", "--dir"}},
		{letters: "f", words: []string{"--force"}},
	},
	where: func(o options) bool { return o.pointsAt("/") },
	risk:  "deletes entire filesystem",
}, {
	name: "rm -rf ~",
	verb: []string{"rm"},
	flags: []flag{
		{letters: "rR", words: []string{"--recursive", "--dir"}},
		{letters: "f", words: []string{"--force"}},
	},
	where: func(o options) bool { return o.pointsUnder("~") },
	risk:  "deletes entire home directory",
}, {
	name: "rm -r -f",
	verb: []string{"rm"},
	flags: []flag{
		{letters: "rR", words: []string{"--recursive", "--dir"}},
		{letters: "f", words: []string{"--force"}},
	},
	risk: "recursive forced deletion — may destroy files irrecoverably",
}, {
	// Force is not what makes a recursive delete permanent — it only stops
	// rm asking about a write-protected file — so a rule that required it
	// let a whole tree go as long as nothing in it happened to be read-only.
	name:  "rm -r",
	verb:  []string{"rm"},
	flags: []flag{{letters: "rR", words: []string{"--recursive", "--dir"}}},
	risk:  "recursive deletion — the directory and everything under it goes",
}, {
	name:  "find -delete",
	verb:  []string{"find"},
	flags: []flag{{words: []string{"-delete"}}},
	risk:  "deletes every file the search matched",
}, {
	name:  "git push --force",
	verb:  []string{"git", "push"},
	flags: []flag{{letters: "f", words: []string{"--force", "--force-with-lease", "--force-if-includes"}}},
	risk:  "force push — may overwrite remote history",
}, {
	name:  "git reset --hard",
	verb:  []string{"git", "reset"},
	flags: []flag{{words: []string{"--hard"}}},
	risk:  "discards all uncommitted changes permanently",
}, {
	// `git clean -f` deletes untracked files on its own; -d takes the
	// directories with them and -x the ignored ones. Force is the whole
	// condition because git refuses to clean without it.
	name:  "git clean -f",
	verb:  []string{"git", "clean"},
	flags: []flag{{letters: "f", words: []string{"--force"}}},
	risk:  "deletes untracked files — nothing in git can bring them back",
}, {
	// Switching branches is not a loss; throwing away the working tree is.
	// The two are told apart by the pathspec — an explicit `--`, or a bare
	// `.` standing where a branch name would.
	name: "git checkout -- .",
	verb: []string{"git", "checkout"},
	where: func(o options) bool {
		return (o.separated && len(o.operands) > 0) || o.pointsAt(".")
	},
	risk: "discards uncommitted changes to the named paths permanently",
}, {
	name:  "chmod -R 777",
	verb:  []string{"chmod"},
	flags: []flag{{letters: "R", words: []string{"--recursive"}}},
	where: func(o options) bool { return len(o.operands) > 0 && permissiveMode(o.operands[0]) },
	risk:  "recursively removes all permission restrictions",
}, {
	// Without -R it is one file, and it is still every account on the
	// machine being handed it.
	name:  "chmod 777",
	verb:  []string{"chmod"},
	where: func(o options) bool { return len(o.operands) > 0 && permissiveMode(o.operands[0]) },
	risk:  "removes all permission restrictions",
}, {
	name: "dd of=/dev/",
	verb: []string{"dd"},
	where: func(o options) bool {
		for _, w := range o.operands {
			if strings.HasPrefix(w, "of=/dev/") {
				return true
			}
		}
		return false
	},
	risk: "writes directly to a device — may destroy disk contents",
}}

// patterns are the dangers that are not a verb carrying options: a
// redirection, a pipe into an interpreter, a fork bomb, a statement in SQL.
// Each of these is a shape in the text, so a regular expression is the
// honest reading of it rather than a table straining to be one.
var patterns = []struct {
	name string
	re   *regexp.Regexp
	risk string
}{
	{"mkfs", regexp.MustCompile(`\bmkfs\.`), "formats a filesystem — all data on target will be lost"},
	{"> /dev/sd", regexp.MustCompile(`>\s*/dev/sd[a-z]`), "overwrites a raw block device"},
	{"> /dev/nvme", regexp.MustCompile(`>\s*/dev/nvme`), "overwrites a raw block device"},
	{"fork bomb", regexp.MustCompile(`\b:(){ :\|:& };:`), "fork bomb — will crash the system"},
	{"drop", regexp.MustCompile(`(?i)\bdrop\s+(database|table)\b`), "drops a database or table — data loss is permanent"},
	{"truncate", regexp.MustCompile(`(?i)\btruncate\s+table\b`), "truncates a table — all rows will be deleted"},
	{"> /etc/passwd", regexp.MustCompile(`\b>\s*/etc/passwd\b`), "overwrites system password file"},
	{"curl | sh", regexp.MustCompile(`\bcurl\b.*\|\s*(sudo\s+)?(ba)?sh\b`), "pipes remote content to shell — executes untrusted code"},
	{"wget | sh", regexp.MustCompile(`\bwget\b.*\|\s*(sudo\s+)?(ba)?sh\b`), "pipes remote content to shell — executes untrusted code"},
}

// Check reports the dangerous shapes in a command, at most one per line: the
// first row that matches is the one a card leads with, and a second warning
// about the same line would only push the first one further from the key.
func Check(command string) []Warning {
	var warnings []Warning
	for _, line := range strings.Split(command, "\n") {
		if w, ok := checkLine(strings.TrimSpace(line)); ok {
			warnings = append(warnings, w)
		}
	}
	return warnings
}

// checkLine reads one line: each command in it against the verb table, then
// the line whole against the text patterns.
func checkLine(line string) (Warning, bool) {
	for _, cmd := range Commands(line) {
		words := strings.Fields(cmd)
		// The program's own name stands in for the path it was reached by:
		// `/bin/rm -rf /` is an rm, and a table keyed on the verb would
		// otherwise see a path it has never heard of.
		words[0] = BaseName(words[0])
		for _, r := range rules {
			if r.matches(words) {
				return Warning{Pattern: r.name, Risk: r.risk}, true
			}
		}
	}
	for _, p := range patterns {
		if p.re.MatchString(line) {
			return Warning{Pattern: p.name, Risk: p.risk}, true
		}
	}
	return Warning{}, false
}

// Commands is every command line a shell line will actually run, as far as
// reading the text can say: each piece of a chain, with the escalation and
// the environment in front of it stripped, and the command line an
// interpreter or a `-exec` was handed pulled out as one of its own.
//
// It over-reads on purpose, and everything that reads it is a gate. The split
// ignores quoting, so a quoted `rm -rf /` is offered as a command — a stop
// somebody can see is wrong costs them one keystroke, and one that never
// happened is what the gate was built for. What follows an escalation is
// offered at every word rather than at the first, because `sudo -u root rm
// -rf /` cannot be told from `sudo -E rm -rf /` without knowing sudo's own
// option table, and stopping at the first flag is how both walk past.
func Commands(line string) []string {
	var out []string
	for _, seg := range segments(line) {
		for _, cmd := range candidates(seg) {
			out = append(out, cmd)
			if nested := nestedCommand(cmd); nested != "" {
				out = append(out, nested)
			}
		}
	}
	return out
}

// BaseName is the last element of a command's path, by either separator: a
// Windows shell writes the one Go's path package does not split on.
func BaseName(w string) string {
	if i := strings.LastIndexAny(w, `/\`); i >= 0 {
		return w[i+1:]
	}
	return w
}

// separators are the characters that can begin another command inside one
// line.
const separators = ";&|<>()`$\n{}"

func segments(line string) []string {
	return strings.FieldsFunc(line, func(r rune) bool {
		return strings.ContainsRune(separators, r)
	})
}

// prefixes are the words that stand in front of the command they run. The
// verb behind them is the one that matters — `sudo rm -rf /` is an rm — and
// a reading of the first word alone would miss every escalated spelling.
var prefixes = map[string]bool{
	"sudo": true, "doas": true, "command": true, "env": true,
	"nohup": true, "time": true, "xargs": true, "eval": true,
}

// interpreters take the command they run as an argument of their own, so
// what sits after their options is a command line rather than an operand.
// `sh -c "git push"` is a push, and it is the one spelling no amount of
// splitting on shell operators will find.
var interpreters = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true,
	"fish": true, "python": true, "python3": true, "node": true,
	"ruby": true, "perl": true,
}

// execFlags are the options that hand the rest of a line to another program.
// `find . -exec rm -rf {} \;` is an rm, and the words after the flag are a
// command line rather than more operands of the search.
var execFlags = map[string]bool{
	"-exec": true, "-execdir": true, "-ok": true, "-okdir": true,
}

// candidates is the command a segment runs, and — where the segment put an
// escalation in front of it — every later word the real command could start
// at.
//
// A quote left at either end of a word goes with the prefixes. The split cuts
// through quoted text without pairing it up, so the piece after an operator
// inside a quoted argument arrives carrying one: `bash -lc "cd /tmp && rm -rf
// /"` hands the second half the closing quote, and a `/"` that matched
// nothing would be the hole rather than the punctuation it is.
func candidates(seg string) []string {
	words := strings.Fields(seg)
	for i, w := range words {
		words[i] = strings.Trim(w, `'"`)
	}
	escalated := false
	for len(words) > 0 && (prefixes[BaseName(words[0])] || strings.Contains(words[0], "=")) {
		words, escalated = words[1:], true
	}
	if len(words) == 0 {
		return nil
	}
	out := []string{strings.Join(words, " ")}
	if !escalated {
		return out
	}
	for i := 1; i < len(words); i++ {
		out = append(out, strings.Join(words[i:], " "))
	}
	return out
}

// nestedCommand is the command line this one carries as an argument, or ""
// when it carries none: what an interpreter was told to run, or what a
// search was told to run over what it found.
func nestedCommand(cmd string) string {
	words := strings.Fields(cmd)
	if len(words) == 0 {
		return ""
	}
	if interpreters[BaseName(words[0])] {
		// The interpreter's own options go with its name; what is left is
		// what will run.
		rest := words[1:]
		for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
			rest = rest[1:]
		}
		return strings.Join(rest, " ")
	}
	for i, w := range words {
		if execFlags[w] {
			return strings.Join(words[i+1:], " ")
		}
	}
	return ""
}

// options is what a command carries after its verb.
type options struct {
	letters  map[rune]bool
	words    map[string]bool
	operands []string
	// separated is whether a bare `--` ended the options, which for a few
	// commands is the difference between naming a branch and naming a file.
	separated bool
}

// readOptions splits a command's arguments into the options it carries and
// the operands it points at.
func readOptions(args []string) options {
	o := options{letters: map[rune]bool{}, words: map[string]bool{}}
	done := false
	for _, w := range args {
		switch {
		case done:
			o.operands = append(o.operands, w)
		case w == "--":
			done, o.separated = true, true
		case strings.HasPrefix(w, "--"):
			// A long option's value is not part of its name.
			o.words[strings.SplitN(w, "=", 2)[0]] = true
		case len(w) > 1 && w[0] == '-':
			// A single dash carries a bundle of letters, and a few programs
			// spell a whole option that way, so the word is kept as well.
			o.words[w] = true
			for _, r := range w[1:] {
				o.letters[r] = true
			}
		default:
			o.operands = append(o.operands, w)
		}
	}
	return o
}

// pointsAt reports whether the command names exactly this operand.
func (o options) pointsAt(path string) bool {
	for _, w := range o.operands {
		if w == path {
			return true
		}
	}
	return false
}

// pointsUnder reports whether an operand is this path or anything beneath it.
func (o options) pointsUnder(path string) bool {
	for _, w := range o.operands {
		if w == path || strings.HasPrefix(w, path+"/") {
			return true
		}
	}
	return false
}

// has reports whether the command satisfies one flag, in any of its
// spellings.
func (o options) has(f flag) bool {
	for _, r := range f.letters {
		if o.letters[r] {
			return true
		}
	}
	for _, w := range f.words {
		if o.words[w] {
			return true
		}
	}
	return false
}

// matches reports whether a command is this rule's: the verb, then every
// flag, then the question about what it points at.
func (r rule) matches(words []string) bool {
	if len(words) < len(r.verb) {
		return false
	}
	for i, v := range r.verb {
		if words[i] != v {
			return false
		}
	}
	o := readOptions(words[len(r.verb):])
	for _, f := range r.flags {
		if !o.has(f) {
			return false
		}
	}
	return r.where == nil || r.where(o)
}

// permissiveMode reports whether a chmod mode hands the whole machine
// everything. It is deliberately narrow: 777 and its spellings are what
// people reach for when a permission error is in the way, and they are the
// one mode worth stopping. A mode that merely widens a group is a judgement
// call, and a warning nobody agrees with is a warning everybody dismisses.
func permissiveMode(mode string) bool {
	switch mode {
	case "777", "0777", "a+rwx", "ugo+rwx", "a=rwx", "+rwx", "o+rwx":
		return true
	}
	return false
}
