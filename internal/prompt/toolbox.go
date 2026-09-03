package prompt

import (
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

// Saying what is actually in the box.
//
// A session's toolset is assembled from what the machine turned out to have:
// a language server if one was detected, fd and ast-grep if they are on PATH,
// web tools if a key is configured, sub-agents only when a human is there to
// approve them. The system prompt named four tools and knew about none of
// this, so the model met the rest as bare schemas — and a tool you have to
// infer from its schema is one you reach for last, if at all. The session
// that ground `search` over one directory forty times had `references`
// sitting unused beside it.
//
// So the prompt is told what was registered, and only that: a line per tool
// saying when it is the right answer. Nothing here describes a tool the
// session does not have, because the far worse failure is a prompt that
// promises one.

// toolboxNotes is what each optional tool is for, in the order they are
// worth being told about: navigation first, since that is where a session
// wastes the most rounds, then editing, then the rest.
var toolboxNotes = []struct{ name, note string }{
	{"definition", "where a symbol is defined, answered by the language server. Prefer it over search for any symbol — search finds the word, this finds the declaration."},
	{"references", "every real usage of a symbol, declaration included. Prefer it over search before changing anything shared: it finds usages, not string matches in comments."},
	{"ast_grep", "structural search by syntax pattern (e.g. `if $A != nil { return $A }`). For shapes a regex cannot describe."},
	{"fd", "find files by name, fast. Use it instead of `execute_command find`."},
	{"sd", "find-and-replace across many files at once. For a mechanical rename that spans files — several places inside one file are one edit_file call carrying its edits array."},
	{"jaq", "query JSON without writing a script for it."},
	{"tokei", "counts of code by language — the shape and size of an unfamiliar repository in one call."},
	{"git", "read this repository's history: status, log, show, diff, blame. Ask it rather than running git as a command — it is read-only, so it answers without an approval."},
	{"web_fetch", "read a URL. Approval-gated, since it leaves the machine."},
	{"web_search", "search the web when the answer is not in the workspace."},
	{"process", "start and watch a long-running process (a dev server, a watcher) without blocking the session on it."},
	{"quality_gate", "run the project's own configured checks by suite name."},
	{"report", "publish an answer that is a page rather than a paragraph — timings, comparisons, structures — as a local graphical page. The first line of its result is the link; put it in your answer. Plain text stays right for anything a sentence or a short table answers."},
	{"spawn_agent", "delegate a self-contained piece of work — a wide search, an independent change — to a sub-agent. Its context is its own, so this is how a broad hunt happens without spending yours."},
	{"agent_report", "collect what a spawned agent found."},
	{"evidence", "retrieve the full output of an earlier tool result that was reduced. The notice on a reduced result carries its id."},
	{"remember", "propose a durable fact worth keeping across sessions. The user confirms it before it persists."},
	{"skill", "load the full instructions of a skill from the Skills list when the task matches one. Do it before starting the work, not after."},
}

// Toolbox describes the optional tools this session registered, for the
// system prompt. It names only tools that are actually present, and returns
// "" when none of them are — a session with the base toolset alone has
// nothing to add beyond what BuildAgent already says.
func Toolbox(tools []provider.Tool) string {
	have := make(map[string]bool, len(tools))
	for _, t := range tools {
		have[t.Name] = true
	}

	var lines []string
	for _, n := range toolboxNotes {
		if have[n.name] {
			lines = append(lines, "- "+n.name+" — "+n.note)
		}
	}
	if len(lines) == 0 {
		return ""
	}

	return "# Toolbox\n" +
		"This session also registered the tools below — they exist because this machine and this project have what they need, so another session may not have them.\n" +
		"Reach for the one that answers the question directly instead of working around its absence:\n" +
		strings.Join(lines, "\n")
}
