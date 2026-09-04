package config

// The settings file as shhh would have written it: every key present,
// commented out at its default, under the sentence that says what it
// decides.
//
// It is the one place shhh writes every key at once, and it is not the
// flattening the targeted rewrite exists to prevent: a commented default is
// not a value in the file, and the command that writes this refuses a file
// that is already there
// (docs/capabilities/configuration.md#a-write-changes-one-line).
//
// It is written from the settings table for the reason the reference section
// is: a second hand-kept list of every key with its sentence is a list that
// goes stale silently, and the failure is a file that says a default the
// code stopped honouring two releases ago.

import (
	"fmt"
	"sort"
	"strings"
)

// scaffoldHeader is what the file says about itself. It names what is not
// here as well as what is: a person who pastes this over a file holding MCP
// servers would otherwise lose them with nothing on screen to say so.
const scaffoldHeader = `# shhh settings.
#
# Every setting is here, commented out at its default with the sentence that
# says what it decides above it. Uncomment a line to change one; a key left
# commented is a key nothing sets, which is not the same as a key written
# down at its zero.
#
# Two tables are not written here, because each has a command of its own:
# the MCP servers and the hooks.
`

// scaffoldProjectHeader is what the checkout's own file says beyond that:
// which keys are missing from it and why, so nobody reads the gap as a key
// that has gone away.
const scaffoldProjectHeader = `#
# This is the checkout's own file, so the keys a checkout may not decide are
# not here: a credential, the containment, the variables a session may spend,
# and the wordings — which travel as files under prompts/ instead. Those stay
# in your own settings, and this file layers over that one key by key.
`

// scaffoldWidth is where a comment line wraps. It is the width the code
// around it wraps at, so the file reads like the rest of the tree in a
// terminal nobody has widened.
const scaffoldWidth = 76

// Scaffold is that file, as text.
//
// cfg fills in what a file already holds: a key it sets is written
// uncommented at that value, so expanding a three-line file is a print and a
// paste rather than a rewrite, and no answer already given is lost on the
// way. A zero Config comments every line, which is what a new file is.
//
// project drops the keys a checkout may not decide, so what this writes into
// a repository is a file that will load in one.
func Scaffold(cfg Config, project bool) string {
	var b strings.Builder
	b.WriteString(scaffoldHeader)
	if project {
		b.WriteString(scaffoldProjectHeader)
	}
	group := ""
	for _, s := range settings {
		if project && RefusedInProject(s.Key) != "" {
			continue
		}
		if g := s.Group(); g != group {
			group = g
			fmt.Fprintf(&b, "\n[%s]\n", group)
		}
		b.WriteString("\n")
		for _, line := range scaffoldNotes(s) {
			b.WriteString(strings.TrimRight("# "+line, " ") + "\n")
		}
		for _, line := range scaffoldLines(cfg, s) {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

// scaffoldNotes is the comment above one key: what it decides, what stands
// when nothing sets it where the commented line is not that, and what
// outranks the file for the handful of keys anything does.
func scaffoldNotes(s Setting) []string {
	notes := wrapComment(s.Desc, scaffoldWidth)
	// The default is stated in words wherever the commented line is not
	// itself those words — where there is no value to write, and where the
	// default is a sentence the file cannot hold. `2 MiB` reaches the line
	// as 2097152 and `on when a summary model is set` as true, and a reader
	// who saw only the number would have lost the half that explains it.
	if s.Default != s.written() {
		notes = append(notes, wrapComment("The default is "+unbracket(s.Default)+".", scaffoldWidth)...)
	}
	if out := scaffoldOutranks(s); out != "" {
		notes = append(notes, wrapComment(out, scaffoldWidth)...)
	}
	return notes
}

// scaffoldOutranks is the line under a key something can overrule. It names
// the ranks in the order they win, so a reader who has uncommented a line
// and seen nothing change reads why here rather than in the documentation.
func scaffoldOutranks(s Setting) string {
	switch {
	case s.Flag != "" && s.Env != "":
		return s.Flag + " on the command line, then " + s.Env +
			" in the environment, outrank this."
	case s.Env != "":
		return s.Env + " in the environment outranks this."
	}
	return ""
}

// scaffoldLines is the key itself: the commented default, and above it an
// uncommented line for every value cfg already holds.
//
// A per-role key is the one key with a segment the person chooses, so it is
// written once per role the config names and once more as the shape a new
// role takes, with the role quoted — a placeholder in a bare key would be a
// line that does not parse the moment somebody uncomments it.
func scaffoldLines(cfg Config, s Setting) []string {
	name := strings.TrimPrefix(s.Key, s.Group()+".")
	if strings.Contains(s.Key, RoleWildcard) {
		var out []string
		for _, role := range scaffoldRoles(cfg, s.Key) {
			key := strings.Replace(s.Key, RoleWildcard, role, 1)
			if line, ok := scaffoldSet(cfg, key, quoteSegment(strings.TrimPrefix(key, s.Group()+"."), role)); ok {
				out = append(out, line)
			}
		}
		return append(out, "#"+quoteSegment(strings.Replace(name, RoleWildcard, roleShown, 1), roleShown)+" = "+scaffoldLiteral(s))
	}
	if line, ok := scaffoldSet(cfg, s.Key, name); ok {
		return []string{line}
	}
	return []string{"#" + name + " = " + scaffoldLiteral(s)}
}

// scaffoldSet is the uncommented line for a key this config holds a value
// for, and ok=false where it holds none.
//
// A secret is written out as it stands rather than masked. The reader is
// looking at their own file on their own terminal, and the whole use of the
// filled-in form is that it can be pasted back: a masked key pasted back is
// a session that cannot reach its provider.
func scaffoldSet(cfg Config, key, name string) (string, bool) {
	text, set := Value(cfg, key)
	if !set {
		return "", false
	}
	_, lit, err := literalFor(key, text)
	if err != nil || lit == "" {
		return "", false
	}
	return name + " = " + lit, true
}

// scaffoldRoles is the roles this config names a model for, in a settled
// order so two runs of the command write the same file.
func scaffoldRoles(cfg Config, key string) []string {
	var out []string
	for role := range cfg.Agents.Profiles {
		if _, set := Value(cfg, strings.Replace(key, RoleWildcard, role, 1)); set {
			out = append(out, role)
		}
	}
	sort.Strings(out)
	return out
}

// roleShown is how the per-role key's chosen segment is written on the line
// nobody has filled in: the word the person replaces, rather than the `*`
// the table matches keys against.
const roleShown = "<role>"

// quoteSegment quotes the chosen segment of a per-role key. `<role>` is not
// a bare key and neither is a role somebody named with a dot in it, so the
// segment is quoted in both cases rather than only when it has to be.
func quoteSegment(name, segment string) string {
	return strings.Replace(name, segment, quoteString(segment), 1)
}

// written is the default in the words `config set` takes it in, and "" for a
// key whose default is not a value at all.
func (s Setting) written() string {
	if s.Literal != "" {
		return s.Literal
	}
	// A default with a space or a bracket in it is a sentence about what
	// happens, not something to type: `(the provider's own)` and `90 days`
	// are both answers to "what stands", and neither is a value.
	if strings.ContainsAny(s.Default, " (") {
		return ""
	}
	// A switch reads as `on` and `off` everywhere a person meets one, and
	// the file takes `true` and `false`. Without this the parse of `on`
	// fails and the line falls back to the key's empty shape — which for a
	// key defaulted on is a commented line that turns it off.
	switch {
	case s.Kind == KindBool && s.Default == "on":
		return "true"
	case s.Kind == KindBool && s.Default == "off":
		return "false"
	}
	return s.Default
}

// unbracket takes the brackets off a default that has no value of its own,
// because this is the one place the default is read as a sentence rather
// than as a cell in a table: `Unset it and the provider's own stands.` is a
// sentence and `Unset it and (the provider's own) stands.` is a lookup with
// a full stop after it.
func unbracket(s string) string {
	if inner, ok := strings.CutPrefix(s, "("); ok {
		if inner, ok := strings.CutSuffix(inner, ")"); ok {
			return inner
		}
	}
	return s
}

// scaffoldLiteral is what the commented line holds: the default where that
// is a value, and the key's own empty shape where it is not. The empty shape
// is what unset means in the file, so a reader who uncomments a line and
// changes nothing else has changed nothing.
func scaffoldLiteral(s Setting) string {
	if v := s.written(); v != "" {
		if _, lit, err := literalFor(s.Key, v); err == nil && lit != "" {
			return lit
		}
	}
	switch s.Kind {
	case KindInt:
		return "0"
	case KindBool:
		return "false"
	case KindList:
		return "[]"
	}
	return `""`
}

// wrapComment breaks a sentence at width, at spaces only: a default written
// as `(the provider's own)` has to survive the wrap whole, and a comment is
// not the place to invent a hyphen.
func wrapComment(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var out []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > width {
			out = append(out, line)
			line = w
			continue
		}
		line += " " + w
	}
	return append(out, line)
}

// ScaffoldKeys is how many keys a scaffold in this scope writes, for a
// caller that says what it wrote without counting the lines back.
func ScaffoldKeys(project bool) int {
	n := 0
	for _, s := range settings {
		if project && RefusedInProject(s.Key) != "" {
			continue
		}
		n++
	}
	return n
}
