package agent

import (
	"regexp"
	"strings"
)

// readOnlyRules are the entries of the built-in inspection list whose
// operands, not only their flags, decide whether they write. Each reads the
// words after the entry and says whether the invocation is still a read.
var readOnlyRules = map[string]func(rest []string) bool{
	"git branch": gitBranchReads,
	"uniq":       uniqReads,
	"date":       dateReads,
	"hostname":   noOperands,

	"go list":      goVerbReads("go list"),
	"go doc":       goVerbReads("go doc"),
	"go mod graph": goVerbReads("go mod graph"),
	"go mod why":   goVerbReads("go mod why"),
}

// gitBranchFlags are the flags of `git branch` that list. Everything else
// creates, renames, copies, deletes or configures a branch.
var gitBranchFlags = map[string]bool{
	"-a": true, "-r": true, "-v": true, "-vv": true, "-l": true, "-i": true,
	"--all": true, "--remotes": true, "--verbose": true, "--list": true,
	"--show-current": true, "--no-color": true, "--color": true,
	"--no-column": true, "--column": true, "--ignore-case": true,
	"--omit-empty": true,
}

// gitBranchValueFlags take a revision as the next word.
var gitBranchValueFlags = map[string]bool{
	"--contains": true, "--no-contains": true, "--merged": true,
	"--no-merged": true, "--points-at": true,
}

// gitBranchAttached are the flags that carry their value after an equals sign.
var gitBranchAttached = []string{
	"--sort=", "--format=", "--abbrev=", "--color=", "--column=", "--contains=",
	"--no-contains=", "--merged=", "--no-merged=", "--points-at=",
}

// gitBranchReads: `git branch` lists, and `git branch name` makes a branch. A
// word that is not a flag is a pattern only when the listing was asked for.
func gitBranchReads(rest []string) bool {
	listing := false
	for _, w := range rest {
		if w == "-l" || w == "--list" {
			listing = true
		}
	}
	for i := 0; i < len(rest); i++ {
		w := rest[i]
		switch {
		case gitBranchValueFlags[w]:
			i++ // its revision
		case gitBranchFlags[w]:
		case strings.HasPrefix(w, "-"):
			if !hasAnyPrefix(w, gitBranchAttached) {
				return false
			}
		case !listing:
			return false
		}
	}
	return true
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// uniqReads: a second operand of uniq is the file it writes.
func uniqReads(rest []string) bool {
	operands := 0
	for i := 0; i < len(rest); i++ {
		switch w := rest[i]; {
		case w == "-f" || w == "-s" || w == "-w" || w == "--skip-fields" || w == "--skip-chars" || w == "--check-chars":
			i++ // its count
		case strings.HasPrefix(w, "-") && w != "-":
		default:
			operands++
		}
	}
	return operands <= 1
}

// dateReads: date prints, and sets the clock given -s or a bare timestamp.
// The operand a print takes starts with a plus (a format).
func dateReads(rest []string) bool {
	for i := 0; i < len(rest); i++ {
		w := rest[i]
		switch {
		case w == "-d" || w == "-r" || w == "-f" || w == "--date" || w == "--reference" || w == "--file":
			i++ // a time or a file to read
		case w == "-s" || strings.HasPrefix(w, "--set"):
			return false
		case strings.HasPrefix(w, "--"):
		case strings.HasPrefix(w, "-"):
			if strings.ContainsRune(w[1:], 's') {
				return false
			}
		case !strings.HasPrefix(w, "+"):
			return false
		}
	}
	return true
}

// goVerbReads closes a Go read verb by list: a word that is a flag must be
// on the verb's goSafeFlags, as -name or --name, with an =value attached or
// (for -f) the template as the next word. Anything else is a package path.
func goVerbReads(verb string) func(rest []string) bool {
	return func(rest []string) bool {
		safe := map[string]bool{}
		for _, f := range goSafeFlags[verb] {
			safe[f] = true
		}
		for i := 0; i < len(rest); i++ {
			w := rest[i]
			if !strings.HasPrefix(w, "-") || w == "-" {
				continue
			}
			name, value, attached := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(w, "-"), "-"), "=")
			if !safe[name] {
				return false
			}
			if name != "f" {
				continue
			}
			if !attached {
				i++
				if i >= len(rest) {
					return false
				}
				value = rest[i]
			}
			if !goTemplateReads(value) {
				return false
			}
		}
		return true
	}
}

// goTemplateAction is the actions a go list template may hold: a field path,
// a range, if or with over one, else, end, and join of one with a literal.
// Every other action (call, template, a function, a pipeline) is refused.
var goTemplateAction = regexp.MustCompile(`^(\.([A-Za-z]\w*(\.[A-Za-z]\w*)*)?|(range|if|with) \.[A-Za-z]\w*(\.[A-Za-z]\w*)*|else|end|join \.[A-Za-z]\w*(\.[A-Za-z]\w*)* "[^"\\]*")$`)

// goTemplateReads reports whether a -f template only prints fields.
func goTemplateReads(tmpl string) bool {
	for {
		_, after, found := strings.Cut(tmpl, "{{")
		if !found {
			return !strings.Contains(tmpl, "}}")
		}
		action, rest, closed := strings.Cut(after, "}}")
		if !closed {
			return false
		}
		if !goTemplateAction.MatchString(strings.TrimSpace(action)) {
			return false
		}
		tmpl = rest
	}
}

// noOperands: hostname with a name sets it.
func noOperands(rest []string) bool {
	for _, w := range rest {
		if !strings.HasPrefix(w, "-") {
			return false
		}
	}
	return true
}
