package prompt

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/shell"
)

func testShell() shell.Info {
	return shell.Info{Shell: "bash", OS: "linux", Cwd: "/tmp"}
}

func toolList(names ...string) []provider.Tool {
	out := make([]provider.Tool, len(names))
	for i, n := range names {
		out[i] = provider.Tool{Name: n}
	}
	return out
}

func TestToolbox_NamesOnlyWhatIsRegistered(t *testing.T) {
	got := Toolbox(toolList("read_file", "search", "definition", "references", "fd"), false)

	for _, want := range []string{"definition", "references", "fd"} {
		if !strings.Contains(got, "- "+want+" — ") {
			t.Errorf("expected %q to be described, got:\n%s", want, got)
		}
	}
	for _, absent := range []string{"ast_grep", "web_search", "spawn_agent", "jaq"} {
		if strings.Contains(got, absent) {
			t.Errorf("described %q, which this session does not have:\n%s", absent, got)
		}
	}
}

func TestToolbox_EmptyWithoutOptionalTools(t *testing.T) {
	// The base toolset is described by BuildAgent itself; a session with
	// nothing else has nothing to add.
	if got := Toolbox(toolList("read_file", "list_directory", "glob", "search", "execute_command", "write_file", "edit_file"), false); got != "" {
		t.Errorf("expected no toolbox section, got:\n%s", got)
	}
	if got := Toolbox(nil, false); got != "" {
		t.Errorf("expected no toolbox section for no tools, got:\n%s", got)
	}
}

func TestToolbox_StableOrder(t *testing.T) {
	// Navigation leads: it is where a session wastes the most rounds.
	got := Toolbox(toolList("remember", "fd", "definition"), false)
	def, fd, rem := strings.Index(got, "- definition"), strings.Index(got, "- fd"), strings.Index(got, "- remember")
	if def >= fd || fd >= rem {
		t.Errorf("expected definition < fd < remember, got:\n%s", got)
	}
}

func TestBuildAgent_CarriesTheToolboxAsExtra(t *testing.T) {
	box := Toolbox(toolList("references"), false)
	if box == "" {
		t.Fatal("expected a toolbox section")
	}
	if !strings.Contains(BuildAgent(testShell(), box), "- references — ") {
		t.Error("the toolbox should reach the agent prompt through extra")
	}
}

func TestBuildAgent_TellsItNotToRepeatCalls(t *testing.T) {
	got := BuildAgent(testShell())
	for _, want := range []string{"Never repeat a call", "Batch independent calls", "files_only", "Know when to stop looking"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected the agent prompt to say %q", want)
		}
	}
}

// The rules are load-bearing and were four near-copies that had drifted. Every
// prompt that investigates has to carry them, and from the one source.
func TestFindingThings_ReachesEveryInvestigatingPrompt(t *testing.T) {
	profile := BuildProfile(testShell(), ProfileSpec{
		Name:  "auditor",
		Tools: []string{"read_file", "search"},
	})
	for _, tc := range []struct {
		name, prompt, rules string
	}{
		{"agent", BuildAgent(testShell()), findingThings},
		{"researcher", BuildResearcher(testShell(), WebTools{Fetch: true, Search: true}), findingThingsBrief},
		{"writer", BuildWriter(testShell()), findingThingsBrief},
		{"profile", profile, findingThingsBrief},
	} {
		if !strings.Contains(tc.prompt, tc.rules) {
			t.Errorf("%s prompt does not carry the shared investigation rules", tc.name)
		}
	}
}

// Both forms have to say the three things the harness cannot say for them.
func TestFindingThings_KeepsTheThreeRules(t *testing.T) {
	for name, rules := range map[string]string{"full": findingThings, "brief": findingThingsBrief} {
		for _, want := range []string{"Batch independent", "Never repeat a call", "stop looking"} {
			if !strings.Contains(rules, want) {
				t.Errorf("%s rules dropped %q", name, want)
			}
		}
	}
}

// The two find-and-replace tools have to be told apart. sd never writes a
// file — it previews a replacement across the tree — and the edit tool now
// changes several places in one file in a single call, so a note that still
// described that as a call per site would send a one-file rename to the
// wrong tool.
func TestToolbox_SplitsSdFromABatchedEdit(t *testing.T) {
	got := Toolbox(toolList("sd"), false)
	for _, want := range []string{"spans files", "edits array"} {
		if !strings.Contains(got, want) {
			t.Errorf("the sd note should name the split, missing %q:\n%s", want, got)
		}
	}
}

// The two structured-query tools cover disjoint formats, and a model told
// about only one of them reaches for it with the wrong file. Each note names
// the formats it answers, so "what does the CI workflow run" lands on the
// tool that can parse a workflow.
func TestToolbox_SplitsTheStructuredQueryToolsByFormat(t *testing.T) {
	got := Toolbox(toolList("jaq", "yq"), false)
	if !strings.Contains(got, "- jaq — query JSON") {
		t.Errorf("the jaq note should claim JSON, got:\n%s", got)
	}
	if !strings.Contains(got, "- yq — query YAML and XML") {
		t.Errorf("the yq note should claim YAML and XML, got:\n%s", got)
	}
}

// The structured-query notes say when to take a part of a large file rather
// than read it whole; how to select that part is the tools' own definitions'.
// A session without either tool is told nothing of the kind.
func TestToolbox_SendsAPartOfALargeFileToTheStructuredQueryTools(t *testing.T) {
	for _, name := range []string{"jaq", "yq"} {
		got := Toolbox(toolList(name), false)
		if !strings.Contains(got, "rather than reading a large") || !strings.Contains(got, "needs only part of it") {
			t.Errorf("the %s note should say when to query rather than read a large file, got:\n%s", name, got)
		}
	}
	if got := Toolbox(toolList("fd"), false); strings.Contains(got, "needs only part of it") {
		t.Errorf("a session without jaq or yq should not be told to query part of a file:\n%s", got)
	}
}

// The three questions beyond definition and references exist to replace a
// search and a whole-file read, so the notes have to say so: a model that is
// not told which tool is better than the one it already reaches for keeps
// reaching for the one it already has.
func TestToolbox_SteersTheLanguageServerAheadOfSearchAndRead(t *testing.T) {
	got := Toolbox(toolList("fd", "hover", "document_symbol", "workspace_symbol"), false)
	for _, want := range []string{"where is X declared", "read_file", "without opening the file"} {
		if !strings.Contains(got, want) {
			t.Errorf("the language-server notes should steer with %q, got:\n%s", want, got)
		}
	}
	// Navigation still leads, whatever order they were registered in.
	if strings.Index(got, "- workspace_symbol") >= strings.Index(got, "- fd") {
		t.Errorf("symbol search should be described before fd, got:\n%s", got)
	}
}

// What a repeatedly steered child means is said where the roster that
// reports it is described, and only to a session that can spawn one. What it
// is asked to do about one is the act it now has, named: a message the roster
// prompts and the model has no tool for is a message to the user about a
// child the user was not watching.
func TestToolboxSaysWhatARepeatedlySteeredAgentMeans(t *testing.T) {
	got := Toolbox([]provider.Tool{{Name: "spawn_agent"}, {Name: "agent_report"}, {Name: "agent_steer"}}, false)
	for _, want := range []string{"steered more than once", "agent_steer", "what it should do instead"} {
		if !strings.Contains(got, want) {
			t.Errorf("the toolbox does not state %q:\n%s", want, got)
		}
	}
	// Ending a child stays the user's, and the note says where they do it —
	// an orchestrator told only that it cannot end one goes looking for the
	// tool that would.
	if !strings.Contains(got, "lane") {
		t.Errorf("the toolbox does not say where a child is ended:\n%s", got)
	}
	if got := Toolbox([]provider.Tool{{Name: "read_file"}}, false); strings.Contains(got, "steer") {
		t.Errorf("a session with no agents was told about one:\n%s", got)
	}
}

// The notebook is stated where every other conditional tool is stated. It
// was in the conversation prompt as prose once, which said it to a session
// that might not have registered it and said nothing to a coding session
// that had.
func TestToolboxStatesTheNotebook(t *testing.T) {
	got := Toolbox([]provider.Tool{{Name: "write_note"}, {Name: "read_note"}}, false)
	for _, want := range []string{"write_note —", "read_note —", "shared notebook", "before delegating"} {
		if !strings.Contains(got, want) {
			t.Errorf("the toolbox does not state %q:\n%s", want, got)
		}
	}
	if got := Toolbox([]provider.Tool{{Name: "read_file"}}, false); strings.Contains(got, "notebook") {
		t.Errorf("a session with no notebook was told about one:\n%s", got)
	}
}

// The note used to carry a workaround: ask after any edit that came back
// without a diagnostics block, because a clean check and an unfinished one
// were the same silence. They are not any more — an unfinished check says so
// on the edit's own result — so the note names that answer instead of asking
// for a call after every clean edit.
func TestToolbox_DiagnosticsIsAskedForOnTheUncheckedAnswer(t *testing.T) {
	got := Toolbox(toolList("diagnostics"), false)
	if !strings.Contains(got, "when an edit came back saying the file was not checked yet") {
		t.Errorf("the diagnostics note should name the answer that asks for it:\n%s", got)
	}
	for _, gone := range []string{"carried no diagnostics block", "late reports wait"} {
		if strings.Contains(got, gone) {
			t.Errorf("the note still carries the workaround sentence %q:\n%s", gone, got)
		}
	}
}

// The spawn line carries how a delegation is written whatever the policy,
// and exactly one of the two policy sentences: explicit says a request for
// depth is not a request to delegate, proactive says divisible work is
// divided. See docs/capabilities/subagents.md#spawning-is-a-decision.
func TestToolboxSpawnLineStatesTheDelegationPolicy(t *testing.T) {
	spawn := []provider.Tool{{Name: "spawn_agent"}}
	explicit, proactive := Toolbox(spawn, false), Toolbox(spawn, true)
	for _, got := range []string{explicit, proactive} {
		for _, want := range []string{"one self-contained task", "paths that do not overlap", "one agent_report wait on the set"} {
			if !strings.Contains(got, want) {
				t.Errorf("the spawn line does not state %q:\n%s", want, got)
			}
		}
	}
	if !strings.Contains(explicit, spawnOnRequest) || strings.Contains(explicit, spawnProactive) {
		t.Errorf("the explicit line should carry only the on-request sentence:\n%s", explicit)
	}
	if !strings.Contains(proactive, spawnProactive) || strings.Contains(proactive, spawnOnRequest) {
		t.Errorf("the proactive line should carry only the dividing sentence:\n%s", proactive)
	}
	if got := Toolbox([]provider.Tool{{Name: "fd"}}, true); strings.Contains(got, spawnProactive) {
		t.Errorf("a session with no spawn_agent was told a policy:\n%s", got)
	}
}

// A read-only or plan session is refused a chained or piped git command, so
// what it is told must lead to the git tool where the session has one and to
// no git command at all: the mode paragraphs name no git command, and only
// the toolbox line — present only beside the tool — says to use the tool.
// A read-only child with no execute_command is the case the paragraphs used
// to get most wrong.
func TestReadOnlyAndPlanPromptsSendHistoryReadsToTheGitTool(t *testing.T) {
	toolsets := []struct {
		name  string
		tools []string
		git   bool
	}{
		{"session with git", []string{"read_file", "search", "execute_command", "git"}, true},
		{"session without git", []string{"read_file", "search", "execute_command"}, false},
		{"read-only child with git", []string{"read_file", "list_directory", "search", "glob", "git"}, true},
		{"read-only child without git", []string{"read_file", "list_directory", "search", "glob"}, false},
	}
	modes := map[string]string{"read-only": ReadOnlyModeInstructions, "plan": PlanModeInstructions}

	for mode, block := range modes {
		if strings.Contains(strings.ToLower(block), "git") {
			t.Errorf("the %s paragraph names git, which the session may not have:\n%s", mode, block)
		}
		for _, ts := range toolsets {
			got := BuildAgent(testShell(), Toolbox(toolList(ts.tools...), false)) + "\n\n" + block
			for _, cmd := range []string{"git status", "git diff", "git log"} {
				if strings.Contains(got, cmd) {
					t.Errorf("%s, %s: the prompt suggests %q", mode, ts.name, cmd)
				}
			}
			line := strings.Contains(got, "- git — ") && strings.Contains(got, "Prefer it over Git shell commands and pipelines")
			if line != ts.git {
				t.Errorf("%s, %s: the git toolbox line present = %v, want %v", mode, ts.name, line, ts.git)
			}
		}
	}
}
