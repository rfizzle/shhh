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
	heredoc := ""
	for _, seg := range splitSegments(command) {
		if heredoc != "" {
			if len(seg.words) == 1 && seg.words[0] == heredoc {
				heredoc = ""
			}
			continue
		}
		if shellEditSegment(seg, scope) {
			return true
		}
		for _, w := range seg.words {
			if d, ok := strings.CutPrefix(w, "<<"); ok && !strings.HasPrefix(d, "<") {
				heredoc = strings.TrimPrefix(d, "-")
			}
		}
	}
	return false
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
		return inPlaceFlag(args, "nEsuzr", "--in-place")
	case "perl":
		return inPlaceFlag(args, "pnlawsSTtuUWXc0123456789", "")
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
// the program has one.
func inPlaceFlag(args []string, carry, long string) bool {
	for _, a := range args {
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
	if scope.Workspace == "" || p == "" || strings.ContainsAny(p, "$`~*?") {
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
