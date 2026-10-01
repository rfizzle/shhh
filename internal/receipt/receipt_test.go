package receipt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/tools"
)

// closedVerbs is the verb vocabulary of
// docs/interface/principles.md#closed-vocabularies, the four git writes
// among them: they are acts rather than tools, which is why a receipt reads
// them out of the call rather than off the name.
var closedVerbs = map[string]bool{"read": true, "search": true, "glob": true, "lsp": true,
	"web": true, "edit": true, "write": true, "patch": true, "run": true,
	"memory": true, "spawn": true, "fan-out": true, "agent": true,
	"report": true, "steer": true, "retry": true, "asked": true,
	"add": true, "commit": true, "branch": true, "switch": true}

// Every tool the table names maps onto a verb the list holds and a kind the
// set holds, and every tool of the base toolset is one the table names: a
// base tool that rendered as its own name would be the table falling behind
// on the tools every session has. A name the table does not hold falls
// through as itself, which is the signal that it is stale.
func TestReceipt_EveryBuiltInToolHasAKindAndAVerb(t *testing.T) {
	for _, name := range Names() {
		r := Build(Call{Name: name})
		if !closedVerbs[r.Verb] {
			t.Errorf("%s maps onto %q, which is not one of the closed verbs", name, r.Verb)
		}
		if r.Kind < KindRead || r.Kind > KindReport {
			t.Errorf("%s is kind %v, which no call is", name, r.Kind)
		}
	}
	base := []string{tools.ExecCommandName}
	for _, d := range append(tools.ReadOnly(), tools.Mutating()...) {
		base = append(base, d.Tool.Name)
	}
	for _, name := range base {
		if _, ok := words[name]; !ok {
			t.Errorf("the base tool %s has no word, so its row reads as its own name", name)
		}
	}

	cases := []struct {
		tool, verb string
		kind       Kind
	}{
		{"read_file", "read", KindRead},
		{"list_directory", "read", KindRead},
		{"search", "search", KindSearch},
		{"ast_grep", "search", KindSearch},
		{"query", "search", KindSearch},
		{"fd", "glob", KindSearch},
		{"references", "lsp", KindLookup},
		{"workspace_symbol", "lsp", KindLookup},
		{"document_symbol", "lsp", KindLookup},
		{"hover", "lsp", KindLookup},
		{"diagnostics", "lsp", KindLookup},
		{"web_fetch", "web", KindLookup},
		{"web_search", "web", KindLookup},
		{"write_file", "write", KindWrite},
		{"sd", "patch", KindWrite},
		{"remember", "memory", KindWrite},
		{"execute_command", "run", KindRun},
		{"quality_gate", "run", KindRun},
		{"process", "run", KindRun},
		{"git_write", "commit", KindRun},
		{"spawn_agent", "spawn", KindSpawn},
		{"agent_report", "agent", KindSpawn},
		{"agent_steer", "steer", KindSpawn},
		{"agent_retry", "retry", KindSpawn},
		{"report", "report", KindReport},
		{"mystery_tool", "mystery_tool", KindRead},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			r := Build(Call{Name: tc.tool})
			if r.Verb != tc.verb || r.Kind != tc.kind {
				t.Errorf("%s reads as %s %q, want %s %q", tc.tool, r.Kind, r.Verb, tc.kind, tc.verb)
			}
			if r.Verb != VerbOf(tc.tool) {
				t.Errorf("the name-only verb %q disagrees with the call's %q", VerbOf(tc.tool), r.Verb)
			}
		})
	}

	// The writing half of git is the one tool whose verb is a field of the
	// call: `commit` is the word a reader scans for, and it is not in the
	// tool's name. Arguments that cannot be read fall back to the table's
	// word, and the call is then not a commit to anything that asks.
	gits := []struct {
		args, verb       string
		commit, switches bool
	}{
		{`{"verb":"commit","message":"feat: x"}`, "commit", true, false},
		{`{"verb":"add","paths":["a.go"]}`, "add", false, false},
		{`{"verb":"branch","branch":"topic"}`, "branch", false, false},
		{`{"verb":"switch","branch":"master"}`, "switch", false, true},
		{`not json`, "commit", false, false},
	}
	for _, tc := range gits {
		r := Build(Call{Name: structural.GitWriteToolName, Args: tc.args})
		if r.Verb != tc.verb || r.IsCommit() != tc.commit || r.SwitchesBranch() != tc.switches {
			t.Errorf("%s reads as %q commit=%v switch=%v, want %q commit=%v switch=%v",
				tc.args, r.Verb, r.IsCommit(), r.SwitchesBranch(), tc.verb, tc.commit, tc.switches)
		}
	}
}

// A search prints context around every match and a notice when it stopped
// early, so the height of the result is not the size of the answer, and the
// receipt must not report the one as the other.
func TestReceipt_ACountIsOfWhatTheCallFoundNotWhatItPrinted(t *testing.T) {
	var sweep strings.Builder
	for i := 1; i <= 50; i++ {
		fmt.Fprintf(&sweep, "a.go:%d- above\na.go:%d: needle\na.go:%d- below\n--\n", i*10-1, i*10, i*10+1)
	}
	sweep.WriteString("… (truncated at 50 matches; narrow the pattern or path, " +
		"or raise limit to at most 500, or use files_only to see which files are involved)")
	if lines := strings.Count(sweep.String(), "\n") + 1; lines < 200 {
		t.Fatalf("the fixture must print far more lines than it found, got %d", lines)
	}

	cases := []struct {
		name, tool, result, want string
	}{
		{"a truncated sweep is its matches and says there are more",
			"search", sweep.String(), "50+ matches"},
		{"context lines are not matches",
			"search", "a.go:9- above\na.go:10: needle\na.go:11- below", "1 match"},
		{"files_only counts files",
			"search", "a.go: 1 match\nb.go: 12 matches", "2 files"},
		{"a search that found nothing claims nothing",
			"search", tools.NoMatchesFound, ""},
		{"a path list really is one line per item",
			"glob", "a.go\nb.go\nc.go", "3 items"},
		{"but its notice is not one of them",
			"glob", "a.go\nb.go\n… (truncated at 2 files; narrow the pattern or path to see more)", "2+ items"},
		{"fd's cap notice is the same shape",
			structural.FdToolName, "a.go\n… (results capped at 1; narrow the pattern or path to see more)", "1+ items"},
		{"a listing that found nothing",
			structural.FdToolName, tools.NoFilesMatched, ""},
		{"a paged read is its window, not its window plus the notice",
			"read_file", "package a\nfunc A() {}\n" +
				"… (truncated: showing lines 1-2 of 90; call read_file again with start_line=3 to continue)",
			"2+ lines"},
		{"nor is the size line a partial read opens with",
			"read_file", "a.log: 90 lines, 1,204 bytes; showing lines 89-90\n89\tlast but one\n90\tlast",
			"2 lines"},
		{"a foreign tool's output is measured in what it is",
			structural.AstGrepToolName, "a.go\n12│\tneedle\n13│\tbelow", "3 lines"},
		{"ast_grep's nothing is nothing",
			structural.AstGrepToolName, structural.NoMatches, ""},
		{"a git write answers with a receipt, not a quantity",
			structural.GitWriteToolName, "[master 1a2b3c4] feat: x\n 1 file changed", ""},
		{"an answered question is one decision",
			"ask", "answered: yes\nwith a note", ""},
		{"an empty result counts nothing",
			"read_file", "  \n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Build(Call{Name: tc.tool, Result: tc.result}).Counts(); got != tc.want {
				t.Errorf("%s counts %q, want %q", tc.tool, got, tc.want)
			}
		})
	}
}

// A server's tool reads as a read when the person marked its server
// read-only and as a remote act otherwise, and whether it is marked is the
// session's answer, handed in: the name alone says only that it is a
// server's.
func TestReceipt_AServerCallIsAReadOnlyWhenMarked(t *testing.T) {
	name := "gh" + mcp.Separator + "create_issue"
	cases := []struct {
		name             string
		served, readOnly bool
		kind             Kind
		rail             bool
	}{
		{"a server the person marked read-only", true, true, KindRead, false},
		{"a server nobody vouched for", true, false, KindRemote, true},
		{"a name shaped like a server's this session never registered", false, false, KindRead, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Build(Call{Name: name, Args: `{"title":"Bug"}`, Served: tc.served, ReadOnly: tc.readOnly})
			if r.Kind != tc.kind || r.Rail() != tc.rail {
				t.Errorf("kind %s rail %v, want %s rail %v", r.Kind, r.Rail(), tc.kind, tc.rail)
			}
			if r.Verb != "mcp" || r.Subject != "gh create_issue title=Bug" {
				t.Errorf("reads as %q %q", r.Verb, r.Subject)
			}
		})
	}
}

// What the result alone says about how the call came out is the receipt's to
// say; everything about the session around it — running, refused, cancelled
// — is not, and leaves the outcome blank.
func TestReceipt_TheOutcomeIsWhatTheResultSays(t *testing.T) {
	cases := []struct {
		name, tool, args, result string
		outcome                  string
		failed                   bool
	}{
		{"an error answer", "read_file", `{"path":"gone.go"}`, "error: open gone.go: no such file", OutcomeError, true},
		{"a commit's receipt line, not the boundaries under it", structural.GitWriteToolName,
			`{"verb":"commit","message":"x"}`, "[master 1a2b3c4] x\nhooks not run: untrusted\n", "[master 1a2b3c4] x", false},
		{"a failed git write is an error, not a receipt", structural.GitWriteToolName,
			`{"verb":"commit"}`, "error: nothing to commit", OutcomeError, true},
		{"a receipt line that reads as the word is still a receipt", structural.GitWriteToolName,
			`{"verb":"commit"}`, "error\n", "error", false},
		{"a report's link", "report", `{"title":"t"}`,
			"http://127.0.0.1:52104/r/rp-1\nreport \"t\" published (id rp-1).", "→ http://127.0.0.1:52104/r/rp-1 …", false},
		{"an ordinary read says nothing of its own", "read_file", `{"path":"a.go"}`, "package a", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Build(Call{Name: tc.tool, Args: tc.args, Result: tc.result})
			if r.Outcome != tc.outcome || r.Failed() != tc.failed {
				t.Errorf("outcome %q failed %v, want %q failed %v", r.Outcome, r.Failed(), tc.outcome, tc.failed)
			}
		})
	}
}

// A command is a run about its first line, ended however the runner says it
// ended, and one that never started names the prerequisite it was missing.
func TestReceipt_ACommandIsItsLineAndItsEnding(t *testing.T) {
	cases := []struct {
		name    string
		ended   tools.ExecResult
		account string
	}{
		{"exited", tools.ExecResult{Outcome: tools.ExecExited, ExitCode: 2}, ""},
		{"never started for want of its directory",
			tools.ExecResult{Outcome: tools.ExecDidNotStart, Prereq: tools.PrereqWorkingDir}, "working dir"},
		{"never started for a reason nobody classified", tools.ExecResult{Outcome: tools.ExecDidNotStart}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Build(Call{Args: "go test ./...\n# and more", Result: "ok\nFAIL", Exec: &tc.ended})
			if r.Kind != KindRun || r.Verb != "run" || r.Subject != "go test ./... …" {
				t.Errorf("reads as %s %q %q", r.Kind, r.Verb, r.Subject)
			}
			if r.Ended != tc.ended || r.Account != tc.account {
				t.Errorf("ended %+v account %q, want %+v %q", r.Ended, r.Account, tc.ended, tc.account)
			}
			if r.Title() != "$ go test ./... …" || r.Counts() != "2 lines" {
				t.Errorf("title %q counts %q", r.Title(), r.Counts())
			}
		})
	}
}

// A search's place is its scope only where the subject ends with it; a search
// of the whole tree has a subject and no scope.
func TestReceipt_AScopeIsWhereTheSubjectSaysItWasPut(t *testing.T) {
	cases := []struct {
		name, args, subject, scope string
	}{
		{"a directory behind the pattern", `{"pattern":"needle","path":"internal/ui"}`, "needle ./internal/ui", "./internal/ui"},
		{"the whole tree", `{"pattern":"needle"}`, "needle", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Build(Call{Name: tools.SearchName, Args: tc.args})
			if r.Subject != tc.subject || r.Scope != tc.scope {
				t.Errorf("subject %q scope %q, want %q %q", r.Subject, r.Scope, tc.subject, tc.scope)
			}
		})
	}
}
