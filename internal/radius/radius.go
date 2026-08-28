// Package radius resolves the blast radius of a shell command before it is
// approved (S-101): the paths it would write, how big those paths are right
// now, and how severe the whole thing is. It is what turns an approval card
// from "here is a string, press y" into a decision the reader can make
// without parsing the command themselves.
//
// Resolution is static and deliberately incomplete. shhh knows a closed set
// of verbs whose operands are files (writeVerbs) plus shell redirection;
// everything else is reported as unresolved, naming what it could not account
// for. An unresolved command is never described as touching nothing — the
// whole point of the block is that it does not guess.
package radius

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/safety"
)

// Level is how much a pending action could cost, as a word the card leads
// with. Colour reinforces it; the word carries it (DESIGN-TUI.md invariant 1).
type Level int

const (
	// Low: the command resolved, and it writes nothing.
	Low Level = iota
	// Medium: it resolved to writes, or shhh could not resolve it at all —
	// a command nobody can account for is not a low-risk one.
	Medium
	// High: safety.Check flagged it.
	High
)

// String is the word the card prints. HIGH is shouted because it is the one
// level where the reader is expected to stop.
func (l Level) String() string {
	switch l {
	case High:
		return "HIGH"
	case Medium:
		return "medium"
	}
	return "low"
}

// Target is one path a command resolved to write, described as the filesystem
// finds it now. A path that is not there yet is a file the command creates,
// which is a different fact from an empty one.
type Target struct {
	Path   string
	Exists bool
	Dir    bool
	// Files and Bytes describe a directory's contents. Complete is false when
	// the walk hit its bound, so the numbers are a floor and Describe says so.
	Files    int
	Bytes    int64
	Complete bool
}

// walkBound is how many directory entries a describe walk visits before it
// gives up and reports a floor. The walk happens on the UI goroutine while
// the card is assembled, once per decision, so it is bounded rather than
// exhaustive.
const walkBound = 20000

// Command is one shell command's resolved radius.
type Command struct {
	// Risks are safety.Check's warnings in the order it found them.
	Risks []string
	// Writes are the paths the command resolved to modify, deduplicated and
	// in the order they appear in the command.
	Writes []Target
	// Unresolved names each part of the command shhh could not account for,
	// as a phrase fit to print after a dash. It being non-empty is what stops
	// Writes from being read as the whole story.
	Unresolved []string
	// Net is true when a segment's verb is one shhh knows reaches the
	// network. Like Writes it is a floor, not a census: an unresolved segment
	// leaves it false, and Reach says so rather than promising quiet.
	Net bool
	// Sudo is true when any segment runs under a privilege-escalation prefix.
	Sudo  bool
	Level Level
	// describe is whether a recorded write is stat-ed and walked. WritePaths
	// leaves it false: it wants the names, not the sizes.
	describe bool
}

// Resolve reads a command and reports what it would touch. It never runs
// anything and never expands anything: every answer comes from the text and
// from stat-ing paths the text names literally.
func Resolve(command string) Command {
	return resolve(command, true)
}

// WritePaths is Resolve for a caller that needs only the paths — the working
// scope check (S-141), which asks which directories a command reaches and
// nothing about how big they are. It skips the describe walk, so it can be
// asked of every queued decision rather than only the one on screen.
func WritePaths(command string) []string {
	c := resolve(command, false)
	paths := make([]string, 0, len(c.Writes))
	for _, w := range c.Writes {
		paths = append(paths, w.Path)
	}
	return paths
}

func resolve(command string, describe bool) Command {
	c := Command{describe: describe}
	for _, w := range safety.Check(command) {
		c.Risks = append(c.Risks, w.Risk)
	}
	for _, seg := range splitSegments(command) {
		c.resolveSegment(seg)
	}
	switch {
	case len(c.Risks) > 0:
		c.Level = High
	case len(c.Writes) > 0 || len(c.Unresolved) > 0:
		c.Level = Medium
	default:
		c.Level = Low
	}
	return c
}

// Touches is the `touches` field: the value naming the paths, and the detail
// that qualifies it. Both are honest about what was not resolved — a command
// with an unaccounted-for part says so beside whatever it did resolve.
func (c Command) Touches() (value, detail string) {
	switch {
	case len(c.Writes) == 0 && len(c.Unresolved) == 0:
		return "nothing", "the command resolved to reads only"
	case len(c.Writes) == 0:
		return "unknown", c.Unresolved[0]
	}
	value = c.Writes[0].Path
	if n := len(c.Writes) - 1; n > 0 {
		value += fmt.Sprintf(" and %d more", n)
	}
	detail = c.Writes[0].Describe()
	if len(c.Unresolved) > 0 {
		detail += "; " + c.Unresolved[0]
	}
	return value, detail
}

// Describe says what the filesystem holds at a target right now.
func (t Target) Describe() string {
	switch {
	case !t.Exists:
		return "does not exist yet — the command creates it"
	case !t.Dir:
		return formatBytes(t.Bytes)
	}
	files := fmt.Sprintf("%d files", t.Files)
	if t.Files == 1 {
		files = "1 file"
	}
	if !t.Complete {
		files = "at least " + files
	}
	return files + ", " + formatBytes(t.Bytes)
}

// segment is one command in a chain, with whether the chain fed it a pipe.
type segment struct {
	text  string
	piped bool
}

// splitSegments cuts a command line at the operators that end one command and
// begin the next — newline, `;`, `&&`, `||`, `|`, and a trailing `&` —
// respecting quotes and backslashes so an operator inside an argument stays
// part of it. `&` is only a separator when it ends a command: `2>&1` and `&>`
// are redirections and stay where they are.
func splitSegments(command string) []segment {
	var out []segment
	var cur strings.Builder
	piped := false
	flush := func(next bool) {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, segment{text: s, piped: piped})
		}
		cur.Reset()
		piped = next
	}
	runes := []rune(command)
	var quote rune
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			cur.WriteRune(r)
			continue
		}
		switch {
		case r == '\'' || r == '"':
			quote = r
			cur.WriteRune(r)
		case r == '\\' && i+1 < len(runes):
			cur.WriteRune(r)
			i++
			cur.WriteRune(runes[i])
		case r == '\n' || r == ';':
			flush(false)
		case r == '&':
			if next := peek(runes, i+1); next == '&' {
				i++
				flush(false)
			} else if next == 0 || next == ' ' || next == '\t' || next == '\n' {
				flush(false)
			} else {
				cur.WriteRune(r)
			}
		case r == '|':
			if peek(runes, i+1) == '|' {
				i++
				flush(false)
			} else {
				flush(true)
			}
		default:
			cur.WriteRune(r)
		}
	}
	flush(false)
	return out
}

func peek(runes []rune, i int) rune {
	if i < len(runes) {
		return runes[i]
	}
	return 0
}

// token is one word of a segment.
type token struct {
	text string
	// literal is false when the shell expands the word before the command
	// sees it — a glob, a variable, a substitution. The path such a word
	// names cannot be resolved statically, and saying so is the honest
	// answer.
	literal bool
}

// tokenize splits a segment into words, respecting quotes, stripping them,
// and marking a word non-literal when an unquoted part of it would expand.
func tokenize(text string) []token {
	var out []token
	var cur strings.Builder
	literal, started := true, false
	var quote rune
	flush := func() {
		if started {
			out = append(out, token{text: cur.String(), literal: literal})
		}
		cur.Reset()
		literal, started = true, false
	}
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			// Double quotes still expand variables and substitutions.
			if quote == '"' && (r == '$' || r == '`') {
				literal = false
			}
			cur.WriteRune(r)
			continue
		}
		switch {
		case r == ' ' || r == '\t':
			flush()
		case r == '\'' || r == '"':
			quote = r
			started = true
		case r == '\\' && i+1 < len(runes):
			i++
			cur.WriteRune(runes[i])
			started = true
		default:
			if strings.ContainsRune("*?[$`~", r) {
				literal = false
			}
			cur.WriteRune(r)
			started = true
		}
	}
	flush()
	return out
}

// operandRule says where in a verb's operands the paths it writes are.
type operandRule struct {
	// skip drops leading operands that are not paths — chmod's mode, sed's
	// script.
	skip int
	// last marks a verb whose final operand is the one it writes (cp, mv,
	// ln); the operands before it are sources it reads.
	last bool
	// needFlag names the flag without which the verb writes no file at all
	// (sed without -i is a filter).
	needFlag string
}

// writeVerbs is the closed set of commands whose operands shhh resolves to
// files they change. It is short on purpose: every entry here is a claim
// about argument shape, and a wrong claim is worse than an honest "unknown".
var writeVerbs = map[string]operandRule{
	"rm":    {},
	"rmdir": {},
	"mkdir": {},
	"touch": {},
	"tee":   {},
	"mv":    {last: true},
	"cp":    {last: true},
	"ln":    {last: true},
	"chmod": {skip: 1},
	"chown": {skip: 1},
	"sed":   {skip: 1, needFlag: "-i"},
}

// argPrefixes are the words a command line can start with that are not the
// command: an environment prefix, or a privilege escalation. The verb behind
// them is the one whose radius matters.
var argPrefixes = map[string]bool{"sudo": true, "doas": true, "command": true, "nohup": true, "time": true}

// escalators are the prefixes among those that change who the command runs
// as. `nohup` moves a process; `sudo` moves the privilege, and that is the
// half a reader needs stated.
var escalators = map[string]bool{"sudo": true, "doas": true}

// harmlessVerbs write nothing of their own. They are not on the approval
// allowlist (internal/agent) because a redirection or a chain turns them into
// something that does write — but the resolver has already split those out by
// the time it looks at the verb, so here they can be named for what they are.
var harmlessVerbs = map[string]bool{
	"echo": true, "printf": true, "true": true, "false": true, "sleep": true,
	"exit": true, "cd": true, "export": true, ":": true,
}

// interpreters are the programs that run whatever is piped into them, so a
// pipe into one makes the whole command unresolvable however innocent the
// left-hand side reads.
var interpreters = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true,
	"python": true, "python3": true, "node": true, "ruby": true, "perl": true,
}

// resolveSegment accounts for one command in the chain, adding what it writes
// and naming what it could not account for.
func (c *Command) resolveSegment(seg segment) {
	toks, redirects := takeRedirects(tokenize(seg.text))
	for _, t := range redirects {
		c.add(t)
	}
	for len(toks) > 0 && (argPrefixes[path.Base(toks[0].text)] || strings.Contains(toks[0].text, "=")) {
		if escalators[path.Base(toks[0].text)] {
			c.Sudo = true
		}
		toks = toks[1:]
	}
	if len(toks) == 0 {
		return
	}
	head := toks[0]
	if !head.literal {
		c.unresolved("the command name is built by the shell, so nothing about it is known")
		return
	}
	verb := path.Base(head.text)
	if reachesNetwork(verb, toks[1:]) {
		c.Net = true
	}
	if seg.piped && interpreters[verb] {
		c.unresolved("piped into " + verb + "; what it runs is not inspected first")
		return
	}
	if verb == "dd" {
		c.resolveDD(toks[1:])
		return
	}
	if rule, ok := writeVerbs[verb]; ok {
		c.resolveVerb(verb, rule, toks[1:])
		return
	}
	// The inspection allowlist (S-061) is the one set of commands already
	// known to change nothing; anything outside it is unaccounted for.
	if harmlessVerbs[verb] || agent.ReadOnlyAllowed(seg.text, nil) {
		return
	}
	c.unresolved("shhh cannot tell what " + verb + " writes")
}

// resolveVerb applies one verb's operand rule.
func (c *Command) resolveVerb(verb string, rule operandRule, rest []token) {
	if rule.needFlag != "" && !hasFlag(rest, rule.needFlag) {
		return // sed without -i writes nothing; it is a filter.
	}
	var operands []token
	for _, t := range rest {
		if strings.HasPrefix(t.text, "-") && t.text != "-" {
			continue
		}
		operands = append(operands, t)
	}
	if len(operands) > rule.skip {
		operands = operands[rule.skip:]
	} else {
		operands = nil
	}
	if len(operands) == 0 {
		c.unresolved(verb + " names no path shhh can read from the command")
		return
	}
	if rule.last {
		operands = operands[len(operands)-1:]
	}
	for _, t := range operands {
		if !t.literal {
			c.unresolved("the shell expands " + t.text + " before " + verb + " sees it")
			continue
		}
		c.add(t.text)
	}
}

// resolveDD reads dd's of= operand, which is the only place it writes.
func (c *Command) resolveDD(rest []token) {
	for _, t := range rest {
		if !strings.HasPrefix(t.text, "of=") {
			continue
		}
		if !t.literal {
			c.unresolved("dd writes to a path the shell expands")
			return
		}
		c.add(strings.TrimPrefix(t.text, "of="))
		return
	}
	c.unresolved("dd names no of= target shhh can read from the command")
}

// takeRedirects pulls `>`/`>>` targets out of a segment's tokens: they are
// writes whatever the command in front of them is. The remaining tokens are
// the command itself.
func takeRedirects(toks []token) (rest []token, writes []string) {
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		op, target := splitRedirect(t.text)
		if op == "" {
			rest = append(rest, t)
			continue
		}
		if target == "" {
			if i+1 >= len(toks) {
				continue
			}
			i++
			target, t = toks[i].text, toks[i]
		}
		// `2>&1` duplicates a descriptor; it opens no file.
		if strings.HasPrefix(target, "&") {
			continue
		}
		if !t.literal {
			continue
		}
		writes = append(writes, target)
	}
	return rest, writes
}

// splitRedirect recognizes an output redirection token and separates it from
// an inline target. Input redirections (`<`) read and are not blast radius.
func splitRedirect(s string) (op, target string) {
	body := strings.TrimLeft(s, "0123456789&")
	if !strings.HasPrefix(body, ">") {
		return "", ""
	}
	body = strings.TrimPrefix(body, ">")
	body = strings.TrimPrefix(body, ">")
	body = strings.TrimPrefix(body, "|")
	return ">", body
}

func hasFlag(toks []token, flag string) bool {
	for _, t := range toks {
		if strings.HasPrefix(t.text, flag) {
			return true
		}
	}
	return false
}

// unresolved records a part of the command shhh could not account for, once.
func (c *Command) unresolved(reason string) {
	for _, r := range c.Unresolved {
		if r == reason {
			return
		}
	}
	c.Unresolved = append(c.Unresolved, reason)
}

// add records a write target, describing it from the filesystem. A path named
// twice in one command is one target.
func (c *Command) add(p string) {
	p = strings.TrimSpace(p)
	if p == "" {
		return
	}
	for _, t := range c.Writes {
		if t.Path == p {
			return
		}
	}
	if !c.describe {
		c.Writes = append(c.Writes, Target{Path: p})
		return
	}
	c.Writes = append(c.Writes, Describe(p))
}

// Describe stats a path, walking a directory under a bound so a command
// pointed at a huge tree still assembles its card promptly. It is exported
// for the callers that already know the path an action touches — a file
// edit, whose target was never in doubt.
func Describe(p string) Target {
	t := Target{Path: p, Complete: true}
	info, err := os.Lstat(p)
	if err != nil {
		return t
	}
	t.Exists = true
	if !info.IsDir() {
		t.Bytes = info.Size()
		return t
	}
	t.Dir = true
	visited := 0
	_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable subtree is not a reason to report nothing
		}
		visited++
		if visited > walkBound {
			t.Complete = false
			return fs.SkipAll
		}
		if d.IsDir() {
			return nil
		}
		t.Files++
		if info, err := d.Info(); err == nil {
			t.Bytes += info.Size()
		}
		return nil
	})
	return t
}

// formatBytes renders a size the way the rest of the UI does: whole units,
// no more precision than the reader needs to judge scale.
func formatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
