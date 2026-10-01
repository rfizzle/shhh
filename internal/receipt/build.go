package receipt

import (
	"slices"
	"strings"

	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/reports"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
)

// word is one tool's entry in the table: the verb a row reads and the kind
// of act it is.
type word struct {
	verb string
	kind Kind
}

// words is the one table mapping tool names onto the closed verb vocabulary
// of docs/interface/principles.md#closed-vocabularies — read, search, glob,
// lsp, web, edit, write, patch, run, memory, spawn, fan-out, agent, report,
// steer, retry, asked, and the four git writes — add, commit, branch,
// switch — and onto the kind of act each is. A tool that maps onto none of
// them is a hole in this table, not a new verb invented at the call site: it
// renders as itself, clipped to the verb column, which is the signal that the
// table is stale.
//
// The writing half of git is the one tool whose verb here is a default rather
// than an answer. Its four verbs are four different acts, so the verb is read
// out of the call (Build); this entry is what a call whose arguments could
// not be read falls back to, which is a call that is about to be a failed row
// anyway. It is a run, with the rail, because it is the same act a
// `git commit` line would have been, and the tier it is approved at does not
// change what the reader is looking at.
var words = map[string]word{
	tools.ReadFileName:          {"read", KindRead},
	tools.ListDirectoryName:     {"read", KindRead},
	evidence.ToolName:           {"read", KindRead},
	tools.SearchName:            {"search", KindSearch},
	structural.AstGrepToolName:  {"search", KindSearch},
	tools.QueryName:             {"search", KindSearch},
	tools.SqliteName:            {"search", KindSearch},
	structural.TokeiToolName:    {"search", KindSearch},
	structural.GitToolName:      {"read", KindRead},
	structural.GitWriteToolName: {"commit", KindRun},
	tools.GlobName:              {"glob", KindSearch},
	structural.FdToolName:       {"glob", KindSearch},
	lsp.DefinitionToolName:      {"lsp", KindLookup},
	lsp.ReferencesToolName:      {"lsp", KindLookup},
	lsp.WorkspaceSymbolToolName: {"lsp", KindLookup},
	lsp.DocumentSymbolToolName:  {"lsp", KindLookup},
	lsp.HoverToolName:           {"lsp", KindLookup},
	lsp.DiagnosticsToolName:     {"lsp", KindLookup},
	web.FetchToolName:           {"web", KindLookup},
	web.SearchToolName:          {"web", KindLookup},
	tools.EditFileName:          {"edit", KindWrite},
	tools.WriteFileName:         {"write", KindWrite},
	structural.SdToolName:       {"patch", KindWrite},
	tools.ExecCommandName:       {"run", KindRun},
	process.ToolName:            {"run", KindRun},
	quality.ToolName:            {"run", KindRun},
	memory.RememberToolName:     {"memory", KindWrite},
	ask.ToolName:                {"asked", KindLookup},
	skill.ToolName:              {"read", KindRead},
	subagent.SpawnToolName:      {"spawn", KindSpawn},
	subagent.ReportToolName:     {"agent", KindSpawn},
	subagent.SteerToolName:      {"steer", KindSpawn},
	subagent.RetryToolName:      {"retry", KindSpawn},
	reports.ToolName:            {"report", KindReport},
}

// Names is every tool the table has a word for, sorted: the register a
// front-end's tests walk to cover each tool it can draw.
func Names() []string {
	names := make([]string, 0, len(words))
	for name := range words {
		names = append(names, name)
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
	r.Subject = gateSubject(r.arg, c.Name, c.Result)
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
	r.Count, r.Noun, r.Short = count(c.Name, c.Result)
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
	r.Count, r.Noun, r.Short = count("", c.Result)
	return r
}

// kindOf picks the kind of act, which picks a row's glyph and, with it,
// whether the row carries the mutation rail.
func kindOf(c Call) Kind {
	if c.Served {
		// A read-only server's call is a read and draws as one; every
		// other server's call is an act shhh cannot see the far side of
		// (docs/capabilities/mcp.md#a-call-is-a-command-unless-you-said-otherwise).
		if c.ReadOnly {
			return KindRead
		}
		return KindRemote
	}
	if w, ok := words[c.Name]; ok {
		return w.kind
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
	if w, ok := words[tool]; ok {
		return w.verb
	}
	if _, ok := mcp.SplitName(tool); ok {
		return "mcp"
	}
	return tool
}

func gitVerbOf(tool, args string) string {
	if !IsGitWrite(tool) {
		return ""
	}
	return digest.GitVerb(args)
}

// gateSubject adds to a quality gate's subject the one thing about the suite
// that only the verdict knows: how many checks it is. The arguments say
// which suite was asked for, so `quality gate · default` is what the row reads
// while the checks run; the verdict says what that suite turned out to be, so
// the finished row reads `quality gate · default · 5 checks` and a reader
// scanning the column knows how much was verified without opening anything
// (docs/interface/principles.md#one-grid).
//
// The suite comes off the verdict rather than off the call once there is a
// verdict to read, because a run that named no suite fell back to the
// configured default and the row would otherwise never say which one that was.
// Everything else — a re-report, a run still in flight, a call that was
// refused — has no verdict, and keeps the subject the arguments gave it.
func gateSubject(subject, tool, result string) string {
	if tool != quality.ToolName {
		return subject
	}
	s, ok := quality.Summarize(result)
	if !ok || s.Suite == "" {
		return subject
	}
	subject = digest.GateSubject + " · " + s.Suite
	if s.Total > 0 {
		subject += " · " + countPhrase(s.Total, false, "check", "checks")
	}
	return subject
}

// count measures a result with a tool-appropriate noun (matches found, items
// listed, lines read).
//
// The number is of what the call found, not of what it printed
// (docs/interface/principles.md#one-grid). For a search those are different
// numbers: the result carries context lines around every match and a notice
// when the tool stopped at its cap, so counting lines described a truncated
// fifty-match answer as `298 matches` — six times the finding, and
// exhaustive-sounding at the moment the tool was saying it had been cut
// short. So search is measured by the tool that wrote the format, and a
// result it cut short reads `50+`.
//
// A path list is the case that hid this: `glob`, `list_directory` and `fd`
// really are one line per item. What they share with every other bounded
// reader is the last line, which is the tool's own notice and not a thing it
// found — a paged read is `2000+ lines` and not 2001. `ast_grep` is the case
// that cannot be measured at all: its output is ast-grep's own, with its own
// context lines, so its count is how many lines came back, which is the only
// thing that is true.
func count(tool, result string) (n int, noun string, short bool) {
	if strings.TrimSpace(result) == "" || foundNothing(result) {
		return 0, "", false
	}
	if tool == structural.GitWriteToolName || tool == ask.ToolName {
		// A git write answers with a receipt and the boundaries of the act,
		// not with output. `2 lines` about it would be a measurement of the
		// sentence rather than of anything that happened — and an answered
		// question is the same: what came back is one decision, not a
		// quantity of anything.
		return 0, "", false
	}
	if tool == tools.SearchName {
		size := tools.MeasureSearch(result)
		if size.Files {
			return measured(size.N, size.Truncated, "file", "files")
		}
		return measured(size.N, size.Truncated, "match", "matches")
	}
	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	more := tools.TruncationNotice(lines[len(lines)-1])
	if more {
		lines = lines[:len(lines)-1]
	}
	// A read of part of a file opens with the whole file's size, which is
	// the tool's own line about the file and not one of the lines it read.
	if tool == tools.ReadFileName && len(lines) > 1 && tools.IsSizeLine(lines[0]) {
		lines = lines[1:]
	}
	switch tool {
	case tools.GlobName, tools.ListDirectoryName, structural.FdToolName:
		return measured(len(lines), more, "item", "items")
	}
	return measured(len(lines), more, "line", "lines")
}

// measured is a count with the noun that agrees with it: the plural for a
// count the tool stopped short of, since `50+` is always more than one.
func measured(n int, more bool, singular, plural string) (int, string, bool) {
	if n == 1 && !more {
		return n, singular, more
	}
	return n, plural, more
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

// countPhrase is a count and its noun as one phrase, `+` marking a count the
// tool stopped short of.
func countPhrase(n int, more bool, singular, plural string) string {
	n, noun, more := measured(n, more, singular, plural)
	return Receipt{Count: n, Noun: noun, Short: more}.Counts()
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
