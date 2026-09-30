// Package nudge is the line a shell read's result carries naming the
// built-in tool that would have answered it.
//
// A command that only reads — a cat, a tail, a grep over the tree — costs
// what a built-in reader never does: an approval card, a classifier round in
// auto mode, a refusal in a read-only session when it is piped. The model is
// told so in execute_command's own description, before the call; this is the
// same thing said at the moment it can act on it, once per turn for each
// tool, because a line repeated at every call is a line the model learns to
// skip.
//
// Whether a line is a plain read at all is not decided here. It is the
// record's reading of the line (observe.CommandPurpose), which already walks
// every command safety.Commands finds and files the line under the strongest
// thing any of them did — so a sed -i, a redirect into a file, a pipe into an
// interpreter or a find -exec is never a read, and nothing here has to learn
// that a second time. What this package adds is the other half: which tool
// answers each reading program, as the rows of one table.
// See docs/capabilities/coding-agent.md#the-built-in-tools-come-before-the-shell.
package nudge

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/safety"
	"github.com/rfizzle/shhh/internal/tools"
)

// row is one reading program and the tool that answers it. when, where set,
// is what the program's arguments must say for the tool to answer it: a
// tail that follows a file is a watcher and no reader answers it, and yq
// told to edit in place is not a read however the record files it.
type row struct {
	verbs []string
	when  func(args []string) bool
	tool  string
	line  string
}

// rows is the table, in the order a line with several readers in it picks
// from: the tool of the earliest row any of its commands matches is the one
// named, so `grep -rn x . | head` is a search and `cat f | jq .a` a query.
// Adding a reader is a row here.
var rows = []row{
	{verbs: []string{"grep", "egrep", "fgrep", "rg"}, when: headingPattern, tool: tools.DocumentSymbolName,
		line: "document_symbol answers this without an approval: a file's outline, a Markdown document's headings, each with its line."},
	{verbs: []string{"jq", "jaq", "gojq"}, tool: tools.QueryName,
		line: "query answers this without an approval: part of a JSON, YAML, TOML, XML or CSV file by a jq expression, or with none, the file's shape."},
	{verbs: []string{"yq"}, when: without("-i", "--inplace"), tool: tools.QueryName,
		line: "query answers this without an approval: part of a JSON, YAML, TOML, XML or CSV file by a jq expression, or with none, the file's shape."},
	// python only reaches here as the record's read: a -c snippet that
	// reads JSON and writes nothing.
	{verbs: []string{"python", "python3"}, tool: tools.QueryName,
		line: "query answers this without an approval: part of a JSON, YAML, TOML, XML or CSV file by a jq expression, or with none, the file's shape."},
	{verbs: []string{"sqlite3"}, tool: tools.SqliteName,
		line: "sqlite answers this without an approval: a database read-only — with no sql, every table's columns, indexes and row count; several statements in one call as an array."},
	// The archive rows stand before search's: a listing piped into grep is
	// still a question about what the archive holds, which search does not
	// look inside.
	{verbs: []string{"unzip"}, when: bundled("lvZ"), tool: tools.ListDirectoryName,
		line: "list_directory answers this without an approval: a .zip, .jar, .tar or .tar.gz lists like a directory, each entry with its size."},
	{verbs: []string{"tar"}, tool: tools.ListDirectoryName,
		line: "list_directory answers this without an approval: a .zip, .jar, .tar or .tar.gz lists like a directory, each entry with its size."},
	{verbs: []string{"unzip"}, tool: tools.ReadFileName,
		line: "read_file answers this without an approval: one entry of an archive is read as archive.zip!/path/in/it, numbered as any file."},
	{verbs: []string{"grep", "egrep", "fgrep", "rg"}, tool: tools.SearchName,
		line: "search answers this without an approval: a pattern across the tree or in one file, each match with the lines around it; files_only names only the files, include narrows to one kind of file."},
	{verbs: []string{"find"}, when: without("-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprint0", "-fprintf", "-fls"), tool: tools.GlobName,
		line: "glob answers this without an approval: files by name pattern across the tree, each with its size."},
	{verbs: []string{"fd", "fdfind"}, when: without("-x", "-X", "--exec", "--exec-batch"), tool: tools.GlobName,
		line: "glob answers this without an approval: files by name pattern across the tree, each with its size."},
	{verbs: []string{"ls", "tree"}, tool: tools.ListDirectoryName,
		line: "list_directory answers this without an approval: a directory's entries, each file with its size; depth walks below it."},
	{verbs: []string{"tail"}, when: notFollowing, tool: tools.ReadFileName,
		line: "read_file answers this without an approval: tail_lines reads the end of a file, numbered as in the file."},
	{verbs: []string{"wc"}, tool: tools.ReadFileName,
		line: "read_file answers this without an approval: a read that shows part of a file opens with its line and byte count, and list_directory and glob give every file's size."},
	{verbs: []string{"cat", "head", "nl", "bat"}, tool: tools.ReadFileName,
		line: "read_file answers this without an approval: a whole file in one call, or the lines between start_line and end_line."},
	// sed only where it was told to print nothing but what its script
	// names: without -n it prints the file transformed, which no reader
	// answers.
	{verbs: []string{"sed"}, when: quietSed, tool: tools.ReadFileName,
		line: "read_file answers this without an approval: a whole file in one call, or the lines between start_line and end_line."},
	// The decompressing readers reach here only in the forms the record
	// files as reads, and the one that decompresses to the output is the one
	// a reader answers: gzip -l reports a ratio no reader states.
	{verbs: []string{"zcat", "gzcat", "gzip", "gunzip"}, when: notListing, tool: tools.ReadFileName,
		line: "read_file answers this without an approval: a .gz file reads as the text it decompresses to, and search looks inside it too."},
}

// escalations run the command after them as somebody else.
var escalations = map[string]bool{
	"sudo": true, "doas": true, "su": true, "pkexec": true, "run0": true,
}

// escalated reports whether any command on the line starts with an
// escalation. A read under another user's privileges is one no built-in
// reader can make, and safety.Commands hands back the command behind the
// escalation rather than the escalation itself, so the line is asked
// directly: the first word of every piece between the shell's separators.
func escalated(line string) bool {
	for _, seg := range strings.FieldsFunc(line, func(r rune) bool {
		return strings.ContainsRune(";&|()`\n", r)
	}) {
		if words := strings.Fields(seg); len(words) > 0 && escalations[safety.BaseName(words[0])] {
			return true
		}
	}
	return false
}

// prefix is what the line opens with, so a reader of the result can tell the
// harness's sentence from the command's output above it.
const prefix = "[built-in: "

// Line is the line a command's result carries, and the tool it names — or
// two empty strings for a command no built-in tool answers.
//
// A command qualifies only where the record reads the whole line as a read,
// a search or a listing, and then only where every reading program in it
// has a row: `grep x f | sort | uniq -c` counts, which no tool does, and a
// line naming a tool for half of it would be a line the model is right to
// ignore.
func Line(command string) (tool, line string) {
	switch observe.CommandPurpose(command) {
	case observe.PurposeRead, observe.PurposeSearch, observe.PurposeList:
	default:
		return "", ""
	}
	if escalated(command) {
		return "", ""
	}
	best := -1
	for _, cmd := range safety.Commands(command) {
		words := strings.Fields(cmd)
		if len(words) == 0 {
			continue
		}
		i := match(safety.BaseName(words[0]), words[1:])
		if i >= 0 {
			if best < 0 || i < best {
				best = i
			}
			continue
		}
		// safety.Commands over-reads — a quoted `a|b` is offered as a
		// command called b — so a piece nothing matched is asked what it
		// is on its own: a fragment or a word of the shell's own reads as
		// something other than a read and is passed over, and a reading
		// program with no row stops the line from qualifying.
		switch observe.CommandPurpose(cmd) {
		case observe.PurposeRead, observe.PurposeSearch, observe.PurposeList:
			return "", ""
		}
	}
	if best < 0 {
		return "", ""
	}
	return rows[best].tool, prefix + rows[best].line + "]"
}

// match is the index of the first row this program and its arguments
// satisfy, or -1.
func match(verb string, args []string) int {
	for i, r := range rows {
		for _, v := range r.verbs {
			if v == verb && (r.when == nil || r.when(args)) {
				return i
			}
		}
	}
	return -1
}

// without holds where none of these options was passed, alone or with a
// value joined by '='.
func without(flags ...string) func([]string) bool {
	return func(args []string) bool {
		for _, a := range args {
			for _, f := range flags {
				if a == f || strings.HasPrefix(a, f+"=") {
					return false
				}
			}
		}
		return true
	}
}

// bundled holds where any of these letters was passed in a short option
// bundle.
func bundled(letters string) func([]string) bool {
	return func(args []string) bool {
		for _, a := range args {
			if len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsAny(a[1:], letters) {
				return true
			}
		}
		return false
	}
}

// notListing is a gzip that was not told -l, in a bundle or spelled out.
func notListing(args []string) bool {
	return !bundled("l")(args) && without("--list")(args)
}

// notFollowing is a tail that prints and exits: -f or -F in any bundle, or
// the long spellings, make it a watcher, which is the process tool's.
func notFollowing(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "--follow") || a == "--retry" {
			return false
		}
		if len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsAny(a[1:], "fF") {
			return false
		}
	}
	return true
}

// quietSed is a sed told -n, alone or in a bundle, and never one editing in
// place — which the record already refuses, but a row should not depend on
// the order the checks happen to run in.
func quietSed(args []string) bool {
	quiet := false
	for _, a := range args {
		if a == "--quiet" || a == "--silent" {
			quiet = true
		}
		if a == "--in-place" || strings.HasPrefix(a, "--in-place=") {
			return false
		}
		if len(a) > 1 && a[0] == '-' && a[1] != '-' {
			if strings.ContainsRune(a[1:], 'i') {
				return false
			}
			if strings.ContainsRune(a[1:], 'n') {
				quiet = true
			}
		}
	}
	return quiet
}

// headingPattern is a grep whose pattern anchors a `#` at the start of the
// line — the shell's way of asking for a Markdown document's headings.
func headingPattern(args []string) bool {
	for _, a := range args {
		a = strings.Trim(a, `'"`)
		if strings.HasPrefix(a, "^#") {
			return true
		}
	}
	return false
}

// Turn is which tools one agent has already been pointed at in the turn it
// is on. The zero value is ready; a nil one says nothing, so a surface that
// holds none appends nothing.
type Turn struct {
	mu   sync.Mutex
	turn int64
	told map[string]bool
	// ran is set by the runner Ran wraps and taken by the resolver
	// WrapResolver wraps. A gated call is answered one at a time on every
	// surface that uses the pair, so one flag is the call in hand.
	ran bool
}

// Append is result with the line for command after it, where command is a
// read a built-in tool answers and that tool has not been named yet in turn.
// A turn number that moved forgets what the last one was told.
func (t *Turn) Append(turn int64, command, result string) string {
	if t == nil {
		return result
	}
	tool, line := Line(command)
	if tool == "" {
		return result
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.told == nil || turn != t.turn {
		t.turn, t.told = turn, map[string]bool{}
	}
	if t.told[tool] {
		return result
	}
	t.told[tool] = true
	return result + "\n" + line
}

// Ran wraps the runner an unattended surface's approver runs a command
// through, so the resolver around the approver knows the call it answers
// ran rather than being refused or declined — a refusal is the approver's
// sentence, and a line under it about the command's cost would be a line
// about a command that cost nothing.
func (t *Turn) Ran(run func(context.Context, string) tools.ExecResult) func(context.Context, string) tools.ExecResult {
	if t == nil {
		return run
	}
	return func(ctx context.Context, command string) tools.ExecResult {
		res := run(ctx, command)
		t.mu.Lock()
		t.ran = true
		t.mu.Unlock()
		return res
	}
}

// WrapResolver appends the line to an execute_command call's result where
// the command ran, at the turn turn names. It goes on outside the repeat
// detector's wrapper, never inside it: the detector keys on the result, and
// a line on the first call and not the second would make two identical
// commands look like two different ones.
func (t *Turn) WrapResolver(turn func() int64, next func(provider.ToolCall) string) func(provider.ToolCall) string {
	if t == nil {
		return next
	}
	return func(tc provider.ToolCall) string {
		t.mu.Lock()
		t.ran = false
		t.mu.Unlock()
		result := next(tc)
		t.mu.Lock()
		ran := t.ran
		t.ran = false
		t.mu.Unlock()
		if !ran || tc.Name != tools.ExecCommandName {
			return result
		}
		var args struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(tc.Arguments), &args) != nil {
			return result
		}
		return t.Append(turn(), args.Command, result)
	}
}
