package receipt

import (
	"maps"
	"slices"
	"strings"

	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/persona"
	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/receipt/describe"
	"github.com/rfizzle/shhh/internal/reports"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
)

// sources is what every package that defines a tool declares about how a
// call to it reads, each set declared beside the definitions it describes.
// The receipt holds no word of its own for any tool: a verb, a kind, how a
// subject and a count are read are the tool's to say where it is defined, so
// a new tool reaches every front-end without a table here being edited. A
// package that defines its first tool joins this list.
func sources() []map[string]describe.Describer {
	return []map[string]describe.Describer{
		tools.Describers(),
		structural.Describers(),
		lsp.Describers(),
		web.Describers(),
		subagent.Describers(),
		quality.Describers(),
		process.Describers(),
		memory.Describers(),
		ask.Describers(),
		skill.Describers(),
		reports.Describers(),
		evidence.Describers(),
		notebook.Describers(),
		plan.Describers(),
		todo.Describers(),
		persona.Describers(),
		mcp.Describers(),
		project.Describers(),
	}
}

// declared is every tool's describer by name: the lookup over what the tool
// packages declared. A name it does not hold reads as the generic describer —
// its own name as the verb, a read, counted in lines — which is the signal
// that a tool was registered without declaring itself
// (docs/interface/principles.md#closed-vocabularies).
var declared = func() map[string]describe.Describer {
	all := map[string]describe.Describer{}
	for _, set := range sources() {
		maps.Copy(all, set)
	}
	return all
}()

// Declared reports whether tool declared how its calls read.
func Declared(tool string) bool {
	_, ok := declared[tool]
	return ok
}

// Does is what tool does, in a clause, for a screen that offers the tool
// rather than draws a call to it; empty for a tool that said nothing.
func Does(tool string) string { return declared[tool].Does }

// Names is every tool that declared a word of its own, sorted: the register
// a front-end's tests walk to cover each tool it can draw.
func Names() []string {
	var names []string
	for name, d := range declared {
		if d.Verb != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

const (
	commitVerb = structural.CommitVerb
	switchVerb = "switch"
)

// wholeTreeScope is what a search that named no directory was put against.
// It is a place to a reading grouping calls by where they went and is not
// one to a row: the subject of such a call is its pattern and nothing else,
// so there is nothing behind the subject to mark.
const wholeTreeScope = "."

// Build reads the receipt of one call.
func Build(c Call) Receipt {
	if c.Exec != nil {
		return command(c)
	}
	d := declared[c.Name]
	r := Receipt{
		Kind:    kindOf(c),
		Verb:    VerbOf(c.Name),
		tool:    c.Name,
		arg:     digest.Arg(c.Name, c.Args),
		args:    c.Args,
		path:    c.Path,
		result:  c.Result,
		gitVerb: gitVerbOf(c.Name, c.Args),
	}
	if len(c.Hunks) > 0 {
		r.Hunk = hunkHead(r.Path(), c.Hunks)
	}
	// Only the writing half of git needs the arguments for its verb:
	// `commit` is the word the reader scans a transcript for, and it is a
	// field of the call rather than part of the tool's name. Everything that
	// names a call to the person reads this rather than the name-only form,
	// because a running `add` reported as `commit` is a worse answer than no
	// answer.
	if r.gitVerb != "" {
		r.Verb = r.gitVerb
	}
	read := describe.Call{Name: c.Name, Args: c.Args, Arg: r.arg, Result: c.Result}
	r.Subject = r.arg
	if d.Subject != nil {
		r.Subject = d.Subject(read)
	}
	// A search's subject is its pattern and then where it was put, and only
	// the pattern is the subject proper: the place is the scope behind it,
	// so the column reads as one question asked somewhere
	// (docs/interface/principles.md#one-grid).
	//
	// Only where the subject is actually carrying it. A call that named no
	// directory is answered with the whole tree — a scope like any other to
	// a reading that groups calls by where they were put, but not a place
	// the subject spends a column marking — and a row told to look for one
	// would find the tail of a pattern that happened to end the same way and
	// dim half the subject.
	if scope, ok := digest.SearchScope(c.Name, c.Args); ok &&
		scope != wholeTreeScope && strings.HasSuffix(r.Subject, " "+scope) {
		r.Scope = scope
	}
	switch {
	case strings.HasPrefix(c.Result, "error:"):
		r.Outcome, r.failed = OutcomeError, true
	case IsGitWrite(c.Name):
		// The git write's receipt is the outcome: the sha is the one part of
		// a commit nobody can reconstruct from the call.
		r.Outcome, _, _ = strings.Cut(strings.TrimRight(c.Result, "\n"), "\n")
	case r.Kind == KindReport:
		// The link is the outcome. The result's first line is the URL by
		// the tool's own contract.
		r.Outcome = "→ " + digest.FirstLine(c.Result)
	}
	r.Count, r.Noun, r.Short = count(d, read)
	return r
}

// command is the receipt of a command the runner ran: a run, about its first
// line, ended however the runner says it ended.
func command(c Call) Receipt {
	r := Receipt{
		Kind:    KindRun,
		Verb:    "run",
		Subject: digest.FirstLine(c.Args),
		Ended:   *c.Exec,
		result:  c.Result,
		command: true,
	}
	if r.Ended.Outcome == tools.ExecDidNotStart {
		r.Account = prereqWord(r.Ended.Prereq)
	}
	r.Count, r.Noun, r.Short = count(describe.Describer{}, describe.Call{Args: c.Args, Result: c.Result})
	return r
}

// kindOf picks the kind of act, which picks a row's glyph and, with it,
// whether the row carries the mutation rail.
func kindOf(c Call) Kind {
	if c.Served {
		return mcp.ServerReceipt(c.ReadOnly).Kind
	}
	if d, ok := declared[c.Name]; ok {
		return d.Kind
	}
	if tools.IsMutating(c.Name) {
		return KindWrite
	}
	return KindRead
}

// VerbOf is a tool's verb from its name alone, for a reader with no call to
// hand: what a group of calls is counted in. Anything naming one call reads
// its receipt's Verb, which is the git write's own act rather than its
// default.
func VerbOf(tool string) string {
	if d := declared[tool]; d.Verb != "" {
		return d.Verb
	}
	if _, ok := mcp.SplitName(tool); ok {
		return mcp.ServerReceipt(false).Verb
	}
	return tool
}

func gitVerbOf(tool, args string) string {
	if !IsGitWrite(tool) {
		return ""
	}
	return digest.GitVerb(args)
}

// count measures a result with the noun its tool declared (matches found,
// items listed, lines read), or in lines where it declared none.
//
// The number is of what the call found, not of what it printed
// (docs/interface/principles.md#one-grid), which is why the tool that wrote
// the format is the one that says how it is counted. What every tool shares
// is the reader's "nothing here" sentence, which is left blank rather than
// counted as `1 match`, and the last line of a bounded reader, which is the
// tool's own notice and not a thing it found.
func count(d describe.Describer, c describe.Call) (n int, noun string, short bool) {
	if strings.TrimSpace(c.Result) == "" || foundNothing(c.Result) {
		return 0, "", false
	}
	if d.Count != nil {
		return d.Count(c)
	}
	lines, more := tools.ResultLines(c.Result)
	return describe.Measured(len(lines), more, "line", "lines")
}

// foundNothing reports whether a result is a reader's "nothing here"
// sentence — search's, glob's and fd's, and ast_grep's. The count is left
// blank rather than counting the sentence, which read as `1 match`: the
// opposite of what the call found.
func foundNothing(result string) bool {
	switch strings.TrimSpace(result) {
	case tools.NoMatchesFound, tools.NoFilesMatched, structural.NoMatches:
		return true
	}
	return false
}

// prereqWord is a harness prerequisite as a command's account names it: the
// category in the reader's words, after `did not start ·`. The codes are the
// record's and the event stream's spelling (tools.ExecPrereq); these are the
// same five, spaced for a person. An unclassified failure has none, and the
// row then says only that the command did not start.
//
// The working directory is `working dir` because the category is the word
// the row exists to state: spelled out, `did not start · working directory`
// is wider than a 60-column row leaves the outcome field, and the grid clips
// the field's tail, so the frame cut the one word that says what was missing
// (docs/interface/principles.md#fold-never-hide). The words are the category
// column of the table in
// docs/capabilities/containment.md#a-command-that-never-started-names-what-it-needed.
func prereqWord(p tools.ExecPrereq) string {
	switch p {
	case tools.PrereqWorkingDir:
		return "working dir"
	case tools.PrereqShell:
		return "execution shell"
	case tools.PrereqContainment:
		return "containment"
	case tools.PrereqPermission:
		return "permission"
	case tools.PrereqSpawn:
		return "spawn"
	}
	return ""
}

// groupNouns names a verb in a counted label. A verb with no entry here
// pluralizes by suffix, which is the signal that this table has fallen behind
// the closed verb table rather than a wrong word in the feed.
var groupNouns = map[string][2]string{
	"read":   {"read", "reads"},
	"search": {"search", "searches"},
	"glob":   {"glob", "globs"},
	"lsp":    {"lookup", "lookups"},
	"web":    {"fetch", "fetches"},
}

// GroupNoun is n calls of one verb as a count of them reads: `6 reads`,
// `2 searches`, `1 lookup` — the noun only.
func GroupNoun(verb string, n int) string {
	forms, ok := groupNouns[verb]
	if !ok {
		forms = [2]string{verb, verb + "s"}
	}
	if n == 1 {
		return forms[0]
	}
	return forms[1]
}

// ApprovalWords is the title and the yes of a card for a call that is
// neither a command nor an edit nor a spawn, named for the act the reader is
// asked to allow: a person asked to approve a web fetch reads "tool" as a
// word about the program, not about the page it is about to read. Anything
// that is not a fetch or a server's tool keeps the general words.
func ApprovalWords(tool string) (title, answer string) {
	if tool == web.FetchToolName {
		return "Approve fetch", "fetch it"
	}
	if _, ok := mcp.SplitName(tool); ok {
		return "Approve server call", "call it"
	}
	return "Approve tool", "allow it"
}

// The named questions a front-end asks of a tool by name, where it holds no
// call to read a receipt from: whether the turn ran the project's checks,
// what a tier lists, which card a call is put to.

// IsGate reports whether tool is the project's quality gate.
func IsGate(tool string) bool { return tool == quality.ToolName }

// IsGitWrite reports whether tool is the writing half of git.
func IsGitWrite(tool string) bool { return tool == structural.GitWriteToolName }

// IsFetch reports whether tool reads one page off the web.
func IsFetch(tool string) bool { return tool == web.FetchToolName }

// IsProcess reports whether tool starts and manages a supervised process.
func IsProcess(tool string) bool { return tool == process.ToolName }
