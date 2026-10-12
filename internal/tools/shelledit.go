package tools

import (
	"path"
	"path/filepath"
	"strings"
)

// shellEditAnswer is what a command that edits a workspace file in place is
// answered with the first time it is sent. It is the one place the words
// live: the redirect names the editor's spellings, and says what the editor
// cannot yet do, so that a later change to the editor re-aims this text and
// nothing else.
const shellEditAnswer = "this edits a file in place; edit_file does it where it is recorded and can be undone: " +
	"one place is old_text and new_text, several places in one file are one call carrying edits, " +
	"every occurrence is replace_all, text at the end is append. " +
	"It has no spelling yet for a replacement that depends on what matched, or one replacement across several files. " +
	"For those, send the same line again and it runs"

// ShellEditError is what a command that edits a workspace file in place is
// answered with in place of a run: the model reads it as the call's result
// and spells the edit with edit_file, or sends the same line again.
//
// The redirect is a nudge and not a wall. The line it answered is not asked
// again: the identical line, sent again, runs as it always did, so a closed
// list that fails open never stands between the model and an edit the editor
// cannot yet make. The person's own /run and ! never come through the
// model's tool, so they are not asked.
// See docs/capabilities/coding-agent.md#the-built-in-tools-come-before-the-shell.
type ShellEditError struct {
	// Command is the line that was answered, trimmed: what the caller
	// remembers so the same line is not answered twice.
	Command string
}

func (ShellEditError) Error() string { return shellEditAnswer }

// ShellScope is where a session's files are: the directory a relative path is
// read against and the only one a redirection counts in, and the scratch
// directories inside which a write is not an edit.
type ShellScope struct {
	Workspace string
	Scratch   []string
}

// ShellEdit reports whether command, as the model wrote it, edits a file of
// the workspace in place: sed -i, perl -i, or a redirection or tee onto a path
// inside the workspace. The list is closed and fails open: a line the parser
// cannot read, a path it cannot resolve, a program it does not name is not an
// edit it recognises. A session with no workspace recognises only the two
// in-place flags, because a path has nothing to be inside of.
func ShellEdit(command string, scope ShellScope) bool {
	for _, seg := range splitSegments(withoutHeredocBodies(command)) {
		if shellEditSegment(seg, scope) {
			return true
		}
	}
	return false
}

// withoutHeredocBodies drops the lines a here-document carries as data, from
// the line after its operator to its terminator, and keeps the line the
// operator is on, whose other commands are still the model's. It reads the
// physical lines and not the words, because a quote in the body (an
// apostrophe) would otherwise join lines and hide the terminator. A heredoc
// whose terminator is never found drops nothing: the body is read as
// commands, which can only recognise too much, once, and never hides an edit
// behind an unclosed operator.
func withoutHeredocBodies(command string) string {
	if !strings.Contains(command, "<<") {
		return command
	}
	lines := strings.Split(command, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		out = append(out, lines[i])
		end := i
		for _, d := range heredocDelimiters(lines[i]) {
			found := -1
			for k := end + 1; k < len(lines); k++ {
				l := lines[k]
				if d.stripTabs {
					l = strings.TrimLeft(l, "\t")
				}
				if l == d.word {
					found = k
					break
				}
			}
			if found < 0 {
				end = i
				break
			}
			end = found
		}
		i = end
	}
	return strings.Join(out, "\n")
}

type heredocDelimiter struct {
	word      string
	stripTabs bool
}

// heredocDelimiters reads the unquoted << operators of one line and the word
// each one names, whether written <<EOF, << EOF, <<-EOF or <<'EOF'.
func heredocDelimiters(line string) []heredocDelimiter {
	var found []heredocDelimiter
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' {
				i++
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '\\':
			i++
		case c == '<' && strings.HasPrefix(line[i:], "<<") && !strings.HasPrefix(line[i:], "<<<"):
			i += 2
			d := heredocDelimiter{}
			if i < len(line) && line[i] == '-' {
				d.stripTabs = true
				i++
			}
			for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
				i++
			}
			var word strings.Builder
			for ; i < len(line); i++ {
				c := line[i]
				if c == '\'' || c == '"' || c == '\\' {
					continue
				}
				if c == ' ' || c == '\t' || c == ';' || c == '|' || c == '&' || c == '<' || c == '>' || c == ')' {
					break
				}
				word.WriteByte(c)
			}
			i--
			if word.Len() > 0 {
				d.word = word.String()
				found = append(found, d)
			}
		}
	}
	return found
}

func shellEditSegment(seg segment, scope ShellScope) bool {
	for i := range seg.words {
		if seg.quoted[i] {
			continue
		}
		if target, ok := redirectTarget(seg, i); ok && inScope(target, scope) {
			return true
		}
	}
	return shellEditWords(stripWrappers(seg.words), scope)
}

func shellEditWords(words []string, scope ShellScope) bool {
	if len(words) == 0 {
		return false
	}
	cmd, args := path.Base(words[0]), words[1:]
	switch cmd {
	case "sed":
		return inPlaceFlag(args, "nEsuzr", "--in-place", false)
	case "perl":
		return inPlaceFlag(args, "pnlawsSTtuUWXc0123456789", "", true)
	case "tee":
		for _, a := range args {
			if !strings.HasPrefix(a, "-") && inScope(a, scope) {
				return true
			}
		}
	case "xargs":
		for i := 0; i < len(args); i++ {
			a := args[i]
			if !strings.HasPrefix(a, "-") {
				return shellEditWords(args[i:], scope)
			}
			if len(a) == 2 && strings.Contains("IndPLEs", a[1:]) {
				i++
			}
		}
	case "find":
		for i, a := range args {
			if a == "-exec" || a == "-execdir" {
				return shellEditWords(args[i+1:], scope)
			}
		}
	}
	return false
}

// inPlaceFlag reads the option words of sed or perl for the flag that edits in
// place. A short cluster is read letter by letter: the letters in carry are
// other flags that take nothing, an i is the in-place flag (whatever follows
// it is the backup suffix), and any other letter ends the cluster, since it
// takes the rest of the word or the next one. long is the long spelling, if
// the program has one. With operands, the options end at the first word that
// is not one (perl's script, or the code after -e), so a flag-shaped word
// given to the script is not read as perl's.
func inPlaceFlag(args []string, carry, long string, operands bool) bool {
	skip := false
	for _, a := range args {
		if skip {
			skip = false
			continue
		}
		if operands && !strings.HasPrefix(a, "-") {
			return false
		}
		switch {
		case a == "--":
			return false
		case long != "" && (a == long || strings.HasPrefix(a, long+"=")):
			return true
		case strings.HasPrefix(a, "--") || !strings.HasPrefix(a, "-") || len(a) < 2:
			continue
		}
		for _, r := range a[1:] {
			if r == 'i' {
				return true
			}
			if !strings.ContainsRune(carry, r) {
				skip = operands && (r == 'e' || r == 'E') && strings.HasSuffix(a, string(r))
				break
			}
		}
	}
	return false
}

// redirectTarget reads word i of a segment as an output redirection and
// returns the path it writes. A descriptor duplication (2>&1, >&2), a
// redirection of descriptor 2, and a word with no target are not a file of the
// model's; the target is the rest of the word, or the next word.
func redirectTarget(seg segment, i int) (string, bool) {
	w := seg.words[i]
	at := strings.IndexByte(w, '>')
	if at < 0 || w[:at] == "2" {
		return "", false
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(w[at:], ">"), ">")
	if rest == "" {
		if i+1 >= len(seg.words) {
			return "", false
		}
		rest = seg.words[i+1]
	}
	if strings.HasPrefix(rest, "&") || strings.HasPrefix(rest, "|") {
		return "", false
	}
	return rest, true
}

// inScope reports whether a path the model named is a file of the workspace
// and not scratch. A path with a variable, a command substitution or a tilde
// in it cannot be read here and is not recognised.
func inScope(p string, scope ShellScope) bool {
	if scope.Workspace == "" || p == "" || strings.ContainsAny(p, "$`~*?()") {
		return false
	}
	ws := filepath.Clean(scope.Workspace)
	if !filepath.IsAbs(p) {
		p = filepath.Join(ws, p)
	}
	p = filepath.Clean(p)
	if !under(ws, p) {
		return false
	}
	for _, s := range scope.Scratch {
		s = filepath.Clean(s)
		// A scratch directory that holds the workspace itself (a checkout
		// under /tmp) would make every path scratch.
		if s != "" && !under(s, ws) && under(s, p) {
			return false
		}
	}
	return true
}

// under reports whether p is dir or below it.
func under(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
