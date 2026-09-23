package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/hook"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/rfizzle/shhh/internal/web"
	"github.com/spf13/cobra"
)

func TestAgentProfilesReaders(t *testing.T) {
	all := &agentProfiles{profiles: subagent.BuiltinProfiles()}
	all.profiles["scribe"] = subagent.Profile{Name: "scribe", Writes: true}
	all.profiles["analyst"] = subagent.Profile{Name: "analyst"}
	got := all.readers()
	for _, want := range []subagent.Role{subagent.RoleResearcher, subagent.RoleReviewer, "analyst"} {
		if _, ok := got.profiles[want]; !ok {
			t.Errorf("readers dropped %q", want)
		}
	}
	for _, absent := range []subagent.Role{subagent.RoleWriter, "scribe"} {
		if _, ok := got.profiles[absent]; ok {
			t.Errorf("readers kept writer %q", absent)
		}
	}
}

// A drafted reviewer is the one people actually write, so the bounded-review
// contract has to survive the trip from a profile file to the supervisor.
func TestProfileFromDefinitionCarriesTheReviewContract(t *testing.T) {
	p, err := profileFromDefinition(config.AgentDefinition{
		Name: "critic", Reviews: true, MaxTokens: 300_000, MaxRounds: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Reviews || p.Writes {
		t.Fatalf("a drafted reviewer must be handed its paths, not claim them: %+v", p)
	}
	if p, err := profileFromDefinition(config.AgentDefinition{Name: "scribe"}); err != nil || p.Reviews {
		t.Fatalf("an ordinary profile reviews nothing: %+v %v", p, err)
	}
}

// The runtime hands a reviewing profile the change and stops it at its cap
// for a report; its permissions grant only reading. Left to those, the words
// it reads are the reader's — gather facts, report findings — and the child
// is run under one contract and instructed in another.
func TestAReviewingProfileIsInstructedAsAReviewer(t *testing.T) {
	info := shell.Info{OS: "linux", Cwd: "/w"}
	def := config.AgentDefinition{
		Name: "critic", Description: "audits diffs", Reviews: true,
		Prompt: "Audit ruthlessly across three axes.",
	}
	got := composed(profileEnv(def, subagent.Spec{}, info, "", nil, nil, map[string]bool{}))

	// Reading the evidence first, ranking, the bounded pass, the verdict:
	// the four things the reader's prompt never says.
	for _, want := range []string{
		"arrives ahead of your task", "Rank by severity",
		"inspection pass is bounded by a round cap", "`Verdict: <word>`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("a reviewing profile's prompt lacks %q:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, "\n\nAudit ruthlessly across three axes.") {
		t.Errorf("the profile's own instructions did not follow the reviewer's:\n%s", got)
	}
	if strings.Contains(got, "the findings, the evidence") {
		t.Errorf("a reviewing profile still ends on the reader's final-report contract:\n%s", got)
	}
	// And it is still this profile: the reviewing contract costs it neither
	// its name nor its purpose.
	for _, want := range []string{`You are the "critic" review sub-agent`, "Your purpose: audits diffs."} {
		if !strings.Contains(got, want) {
			t.Errorf("a reviewing profile's prompt lacks %q:\n%s", want, got)
		}
	}
	// The flag is what selects it: the same file without it is a reader,
	// and the reviewing contract is the built-in role's own words rather
	// than a fork of them.
	plain := def
	plain.Reviews = false
	reader := composed(profileEnv(plain, subagent.Spec{}, info, "", nil, nil, map[string]bool{}))
	if !strings.Contains(reader, `"critic" sub-agent`) || strings.Contains(reader, "Rank by severity") {
		t.Errorf("a profile that does not review should get the generic reader's prompt:\n%s", reader)
	}
	builtin := prompt.BuildReviewer(info, prompt.ProfileSpec{})
	_, contract, ok := strings.Cut(builtin, "\n\n# Reviewing\n")
	if !ok || !strings.Contains(got, "\n\n# Reviewing\n"+contract) {
		t.Errorf("the reviewing profile forked the built-in reviewer's contract:\n%s", got)
	}
	// prompt_mode = "replace" still owns the whole prompt, review or not.
	replaced := def
	replaced.PromptMode = config.PromptReplace
	if own := composed(profileEnv(replaced, subagent.Spec{}, info, "", nil, nil, map[string]bool{})); own != def.Prompt {
		t.Errorf("replace no longer sends the profile's instructions alone:\n%s", own)
	}
}

// A role that must never run an arbitrary command still has to be able to
// say whether the change compiles and its tests pass. The gate is how, and
// the read tier is enough for it: what it can run was settled by whoever
// trusted the checkout, so it is not the execute tier's concern.
func TestReadOnlyProfileIsOfferedTheQualityGate(t *testing.T) {
	gate := &quality.Runner{Workspace: t.TempDir()}
	def := config.AgentDefinition{Name: "critic"}
	_, defs, exec := profileEnv(def, subagent.Spec{}, shell.Info{}, "", nil, gate, map[string]bool{})

	if !containsString(toolsetNames(defs), config.QualityGateTool) {
		t.Fatalf("a read-only profile was not offered %s: %v", config.QualityGateTool, toolsetNames(defs))
	}
	// The executor has to reach the session's runner, not fall through to
	// the built-in dispatcher, which would answer an unknown tool.
	out, err := exec(config.QualityGateTool, json.RawMessage(`{"action":"result"}`))
	if err != nil || !strings.Contains(out, "No gate runs this session yet") {
		t.Fatalf("the call did not reach the session's runner: %q %v", out, err)
	}
	// A trusted checkout with no suites in it answers the child the way it
	// answers the session — a verdict of blocked saying where suites are
	// defined — rather than an error a reader would take for a refusal.
	out, err = exec(config.QualityGateTool, json.RawMessage(`{"action":"run"}`))
	if err != nil {
		t.Fatalf("an unconfigured checkout must answer, not refuse: %v", err)
	}
	if !strings.Contains(out, "BLOCKED") || !strings.Contains(out, "no quality config") {
		t.Errorf("an unconfigured checkout should say so: %q", out)
	}
}

// A reviewing profile's prompt is built from the toolset it was registered
// with, and the gate is registered after the prompt's other tiers are
// settled. A reviewer holding it and told it can run nothing judges the
// build from the diff instead of running the checks.
func TestAReviewingProfilesPromptNamesTheGateItHolds(t *testing.T) {
	def := config.AgentDefinition{Name: "critic", Description: "audits diffs", Reviews: true}
	info := shell.Info{OS: "linux", Cwd: "/w"}

	held := composed(profileEnv(def, subagent.Spec{}, info, "", nil, &quality.Runner{Workspace: t.TempDir()}, map[string]bool{}))
	if !strings.Contains(held, config.QualityGateTool) {
		t.Errorf("a reviewing profile holding the gate is not told so:\n%s", held)
	}
	if strings.Contains(held, "You cannot edit files or run commands.") {
		t.Errorf("a reviewing profile holding the gate is told it can run nothing:\n%s", held)
	}
	// No runner in the session is no gate in the toolset, and the prompt
	// says the same.
	without := composed(profileEnv(def, subagent.Spec{}, info, "", nil, nil, map[string]bool{}))
	if strings.Contains(without, config.QualityGateTool) {
		t.Errorf("a reviewing profile without the gate was told it has one:\n%s", without)
	}
	if !strings.Contains(without, "You cannot edit files or run commands.") {
		t.Errorf("a reviewing profile without the gate lost the boundary sentence:\n%s", without)
	}
}

// The same holds for a read-only profile that does not review: the gate
// sentence follows the toolset, so it is there when the runner reached the
// child and absent when an untrusted checkout opened none.
func TestAReadOnlyProfilesPromptNamesTheGateItHolds(t *testing.T) {
	def := config.AgentDefinition{Name: "auditor", Description: "reads the tree"}
	info := shell.Info{OS: "linux", Cwd: "/w"}

	held := composed(profileEnv(def, subagent.Spec{}, info, "", nil, &quality.Runner{Workspace: t.TempDir()}, map[string]bool{}))
	if !strings.Contains(held, "the gate is the only command you can run") {
		t.Errorf("a read-only profile holding the gate is not told so:\n%s", held)
	}
	if strings.Contains(held, "You cannot edit files or run commands") {
		t.Errorf("a read-only profile holding the gate is told it can run nothing:\n%s", held)
	}
	without := composed(profileEnv(def, subagent.Spec{}, info, "", nil, nil, map[string]bool{}))
	if strings.Contains(without, config.QualityGateTool) {
		t.Errorf("a read-only profile without the gate was told it has one:\n%s", without)
	}
	if !strings.Contains(without, "You cannot edit files or run commands") {
		t.Errorf("a read-only profile without the gate lost the boundary sentence:\n%s", without)
	}
}

// Who does not get it, and why each one is a different reason: an untrusted
// checkout opened no runner for anybody, an allowlist that omits the gate
// meant to omit it, and a profile that writes would be handed a verdict on
// the checkout its worktree was copied from — a tree with none of its own
// changes in it.
func TestTheQualityGateReachesOnlyProfilesEntitledToIt(t *testing.T) {
	gate := &quality.Runner{Workspace: t.TempDir()}
	cases := []struct {
		name string
		def  config.AgentDefinition
		gate *quality.Runner
	}{
		{"untrusted checkout", config.AgentDefinition{Name: "critic"}, nil},
		{"allowlist without it", config.AgentDefinition{Name: "critic", Tools: []string{"read_file", "search"}}, gate},
		{"a profile that writes", config.AgentDefinition{Name: "fixer", Permissions: []string{config.PermissionWrite}}, gate},
		{"a profile that executes", config.AgentDefinition{Name: "runner", Permissions: []string{config.PermissionExecute}}, gate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, defs, _ := profileEnv(tc.def, subagent.Spec{}, shell.Info{}, "", nil, tc.gate, map[string]bool{})
			if containsString(toolsetNames(defs), config.QualityGateTool) {
				t.Errorf("%s was offered %s: %v", tc.name, config.QualityGateTool, toolsetNames(defs))
			}
		})
	}
	// Naming it is how a narrowed profile keeps it.
	named := config.AgentDefinition{Name: "critic", Tools: []string{"read_file", config.QualityGateTool}}
	_, defs, _ := profileEnv(named, subagent.Spec{}, shell.Info{}, "", nil, gate, map[string]bool{})
	if !containsString(toolsetNames(defs), config.QualityGateTool) {
		t.Errorf("a profile that named the gate did not get it: %v", toolsetNames(defs))
	}
}

// The runner a child is offered is the session's own — the registration is
// the only place one is opened, so a child is never given a second opinion
// about the checkout. Both halves are checked: an untrusted checkout that
// opened none hands a child none, and a trusted one hands over the same
// object the session dispatches through.
func TestTheSessionsGateIsTheOneAChildIsOffered(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	sc, err := sessionScope(config.Config{}, nil)
	if err != nil {
		t.Fatalf("session scope: %v", err)
	}
	for _, tc := range []struct {
		name    string
		trust   project.Trust
		offered bool
	}{
		{"untrusted", project.Trust{Root: "/repo", Present: []project.Kind{project.KindGate}}, false},
		{"trusted", project.Trust{Root: "/repo", Granted: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withProjectTrust(t, tc.trust)
			session := codeToolset()
			ts, err := buildToolset(toolsetCmd(t), &session, "code", toolsetOpts{scope: sc})
			if err != nil {
				t.Fatalf("session registration: %v", err)
			}
			defer ts.close()
			if session.gateRunner != ts.gate {
				t.Fatalf("a child would be offered a different gate from the session's: %p vs %p", session.gateRunner, ts.gate)
			}
			_, defs, _ := profileEnv(config.AgentDefinition{Name: "critic"}, subagent.Spec{}, shell.Info{}, "",
				nil, session.gateRunner, map[string]bool{})
			if got := containsString(toolsetNames(defs), config.QualityGateTool); got != tc.offered {
				t.Errorf("child offered the gate = %v, want %v", got, tc.offered)
			}
		})
	}
}

// The web branch replaces the child's executor rather than wrapping it, so a
// gate wrap installed before it disappears without a compile error and
// without a failing call — the gate simply answers as an unknown tool.
func TestTheQualityGateSurvivesTheWebToolset(t *testing.T) {
	session := codeToolset()
	def := config.AgentDefinition{Name: "critic", Permissions: []string{config.PermissionWeb}}
	gate := &quality.Runner{Workspace: t.TempDir()}
	_, _, exec := profileEnv(def, subagent.Spec{}, shell.Info{}, "", session.web, gate, map[string]bool{})

	if out, err := exec(config.QualityGateTool, json.RawMessage(`{"action":"result"}`)); err != nil ||
		!strings.Contains(out, "No gate runs this session yet") {
		t.Errorf("the gate was wrapped away by the web toolset: %q %v", out, err)
	}
	if _, err := exec(web.FetchToolName, json.RawMessage(`{"url":"http://169.254.169.254/latest/meta-data/"}`)); err == nil ||
		!strings.Contains(err.Error(), "metadata") {
		t.Errorf("the web toolset was wrapped away by the gate: %v", err)
	}
}

// A writer starts from the parent's tree, and the half of that tree git
// cannot describe is the files the session made itself. The session's own
// record is what names them: a checkout is full of untracked files nobody in
// the conversation put there, and copying those into every writer's worktree
// would carry the person's desk rather than their work. A file the session
// created and then deleted has nothing left to carry.
func TestSessionUntracked(t *testing.T) {
	store := changeset.New(changeset.DefaultMaxBytes)
	add := func(turn int64, path string, track changeset.Tracking, after string, exists bool) {
		store.Add(turn, changeset.Record{
			Path: path, After: after, AfterExists: exists,
			Agent: changeset.MainAgent, Track: track,
		})
	}
	add(1, "internal/new.go", changeset.TrackUntracked, "package internal\n", true)
	add(1, "internal/old.go", changeset.TrackTracked, "package internal\n", true)
	add(2, "internal/new.go", changeset.TrackUntracked, "package internal\n\nvar x = 1\n", true)
	add(2, "scratch.txt", changeset.TrackUntracked, "", false)
	add(3, "notes/todo.md", changeset.TrackUntracked, "one\n", true)

	got := sessionUntracked(store)
	want := []string{"internal/new.go", "notes/todo.md"}
	if len(got) != len(want) {
		t.Fatalf("sessionUntracked = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sessionUntracked = %v, want %v", got, want)
		}
	}
	if paths := sessionUntracked(nil); paths != nil {
		t.Fatalf("a session with no changeset carries nothing, got %v", paths)
	}
}

// A child that is told nothing about the checkout has to spend rounds on git
// to learn what the session was handed for free, and a writer that is told
// only the parent's facts finds its own git disagreeing with them.
func TestChildExtraCarriesTheWorkspaceAndWhereTheChildStands(t *testing.T) {
	workspace := project.PromptBlock(project.Info{Dir: "/work", Repo: true, Branch: "side", Dirty: 2})

	reader := childExtra("", "# Project\nbe helpful", "", workspace, false)
	if !strings.Contains(reader, "Git branch: side") || !strings.Contains(reader, "2 uncommitted paths") {
		t.Errorf("a child should be handed the checkout the session was handed:\n%s", reader)
	}
	if !strings.Contains(reader, "be helpful") {
		t.Errorf("the project's own instructions still come with it:\n%s", reader)
	}
	if strings.Contains(reader, "isolated copy") {
		t.Errorf("a reader stands in the parent's own directory:\n%s", reader)
	}

	writer := childExtra("", "", "", workspace, true)
	if !strings.Contains(writer, "Git branch: side") {
		t.Errorf("a writer is told the branch too:\n%s", writer)
	}
	if !strings.Contains(writer, "isolated copy") || !strings.Contains(writer, "belongs to no branch") {
		t.Errorf("a writer whose git contradicts the block has to be told why:\n%s", writer)
	}
	if strings.Index(writer, "isolated copy") < strings.Index(writer, "Git branch: side") {
		t.Errorf("the correction follows the facts it corrects:\n%s", writer)
	}
}

// A child is handed the project's instructions against its own budget, not
// the session's. A checkout whose instruction file runs to a few hundred
// kilobytes — this one's does — otherwise put a sixth of a child's whole
// token budget into its system prompt before it had read a line of code, and
// again for every child in a fan-out.
func TestChildInstructionsAreCutToTheChildsOwnBudget(t *testing.T) {
	dir := t.TempDir()
	var text strings.Builder
	text.WriteString("# Fixture\n\n## Overview\n\n")
	for text.Len() < 200<<10 {
		text.WriteString("a line about how this project builds\n")
	}
	text.WriteString("\n## Gotchas\n\nthe rule nobody guesses\n")
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(text.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	files := project.Instructions(dir, "")
	child := project.InstructionBlock(files, prompt.ChildInstructionBudget)
	session := project.InstructionBlock(files, prompt.InstructionBudget)

	if len(child) >= len(session) {
		t.Fatalf("a child's block (%d bytes) is not smaller than the session's (%d)", len(child), len(session))
	}
	extra := childExtra("", child, "", "", false)
	if len(extra) > prompt.ChildInstructionBudget+2000 {
		t.Fatalf("a child's standing context came to %d bytes against a budget of %d",
			len(extra), prompt.ChildInstructionBudget)
	}
	if !strings.Contains(extra, "the rule nobody guesses") {
		t.Fatalf("the end of the instruction file did not survive the cut:\n%s", extra[:2000])
	}
}

// A session assembled without a survey says nothing about the tree rather
// than stopping the child that asked.
func TestSessionEnvWorkspaceBlockIsOptional(t *testing.T) {
	if got := (&sessionEnv{}).workspaceBlock(); got != "" {
		t.Errorf("no reading of the tree is no block, got %q", got)
	}
	env := &sessionEnv{workspace: func() string { return "# Workspace\n- Git branch: side" }}
	if got := env.workspaceBlock(); got != "# Workspace\n- Git branch: side" {
		t.Errorf("the block should come through as it was built, got %q", got)
	}
}

// Every child is wired into the parent's notebook the same way, and a
// writer is the case that has to be said out loud: it works in an isolated
// copy of the checkout and still writes into the session's notebook, under
// its own name, because the notebook belongs to the session and not to a
// tree. It can add and read; there is no tool that removes anything.
func TestChildWritesIntoTheParentsNotebook(t *testing.T) {
	nb := notebook.New(nil)
	nb.SetTurn(4)
	_, _, _ = nb.Write(notebook.Orchestrator, "The gate lives in mode.go", "policy.Decide reads the deny list")

	passed := errors.New("passed on")
	next := func(string, json.RawMessage) (string, error) { return "", passed }

	for _, child := range []string{"writer-1", "researcher-1", "reviewer-1"} {
		defs, exec, sysPrompt := withNotebook(nb, child, tools.Definitions(), next, "# Environment")
		var have []string
		for _, d := range defs {
			have = append(have, d.Name)
		}
		for _, want := range []string{notebook.WriteToolName, notebook.ReadToolName} {
			if !slices.Contains(have, want) {
				t.Errorf("%s was not given %s", child, want)
			}
		}
		if !strings.Contains(sysPrompt, "The gate lives in mode.go") {
			t.Errorf("%s was not told what the notebook already holds:\n%s", child, sysPrompt)
		}
		if !strings.Contains(sysPrompt, "# Environment") {
			t.Errorf("%s lost the prompt the block was added to:\n%s", child, sysPrompt)
		}
		args := json.RawMessage(`{"title":"` + child + ` found it","body":"in internal/cli"}`)
		if _, err := exec(notebook.WriteToolName, args); err != nil {
			t.Fatalf("%s could not write: %v", child, err)
		}
		// Nothing a child can call removes a note; an unknown name is
		// passed on down the chain rather than answered here.
		if _, err := exec("delete_note", json.RawMessage(`{"id":1}`)); !errors.Is(err, passed) {
			t.Errorf("%s reached something that deletes", child)
		}
	}

	notes := notebook.WrittenIn(nb.List(), 4, notebook.Orchestrator)
	if len(notes) != 3 {
		t.Fatalf("the parent's notebook holds %d of its children's notes", len(notes))
	}
	for i, want := range []string{"writer-1", "researcher-1", "reviewer-1"} {
		if notes[i].Author != want {
			t.Errorf("note %d signed %q, want %q", i, notes[i].Author, want)
		}
	}
	// The orchestrator's own note is still there and is not counted as a
	// child's.
	if nb.Len() != 4 {
		t.Fatalf("the notebook holds %d notes", nb.Len())
	}
}

// A descendant signs with its lineage from the root child down, so a note
// read weeks later says which agent wrote it and under whose task; a child
// of the session signs with its bare name, which is the whole of its
// lineage. The spec each signature is built from is the one the runtime
// hands the environment factory, and the parent links are the supervisor's
// own, so this is the name the notebook really sees.
func TestAGrandchildSignsItsNotesWithItsLineage(t *testing.T) {
	var mu sync.Mutex
	specs := map[string]subagent.Spec{}
	scripted := (&scriptedChildren{steps: []childStep{
		{text: "the exporter is fine"}, {text: "and so is the reading of it"}}}).factory()
	sup := subagent.New(t.Context(), subagent.Options{
		Root:     t.TempDir(),
		MaxDepth: 3,
		NewEnv: func(ctx context.Context, spec subagent.Spec) (subagent.Env, error) {
			mu.Lock()
			specs[spec.Name] = spec
			mu.Unlock()
			return scripted(ctx, spec)
		},
	})
	t.Cleanup(sup.Close)

	spawn := func(caller, args string) {
		t.Helper()
		exec := sup.WrapExecutor(caller, func(name string, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unexpected passthrough: %s", name)
		})
		if _, err := exec(subagent.SpawnToolName, json.RawMessage(args)); err != nil {
			t.Fatalf("%q spawning %s: %v", caller, args, err)
		}
	}
	spawn("", `{"role":"researcher","task":"survey the exporter","name":"researcher-1"}`)
	spawn("researcher-1", `{"role":"reviewer","task":"read what it found","name":"reviewer-1a"}`)
	spec := func(name string) subagent.Spec {
		t.Helper()
		var got subagent.Spec
		waitFor(t, "the runtime to build "+name+"'s environment", func() bool {
			mu.Lock()
			defer mu.Unlock()
			var ok bool
			got, ok = specs[name]
			return ok
		})
		return got
	}
	researcher, reviewer := spec("researcher-1"), spec("reviewer-1a")

	nb := notebook.New(nil)
	next := func(string, json.RawMessage) (string, error) { return "", errors.New("passed on") }
	for _, s := range []subagent.Spec{researcher, reviewer} {
		_, exec, _ := withNotebook(nb, notebookSignature(sup, s), tools.Definitions(), next, "# Environment")
		args := json.RawMessage(`{"title":"` + s.Name + ` found it","body":"in internal/cli"}`)
		if _, err := exec(notebook.WriteToolName, args); err != nil {
			t.Fatalf("%s could not write: %v", s.Name, err)
		}
	}

	notes := nb.List()
	if len(notes) != 2 {
		t.Fatalf("the notebook holds %d notes", len(notes))
	}
	for i, want := range []string{"researcher-1", "researcher-1/reviewer-1a"} {
		if notes[i].Author != want {
			t.Errorf("note %d is signed %q, want %q", i, notes[i].Author, want)
		}
	}
	// And the grandchild's note is filed under the child the session spawned,
	// which is what puts it beside the work it was delegated out of.
	if got := notebook.RootAuthor(notes[1].Author); got != "researcher-1" {
		t.Errorf("the grandchild's note groups under %q, want researcher-1", got)
	}
}

// A session with no notebook is left exactly as it was: the block is what
// tells a child the tools exist, so it must never be added without them.
func TestChildWithoutANotebookIsUnchanged(t *testing.T) {
	base := agent.ToolExecutor(func(string, json.RawMessage) (string, error) { return "ok", nil })
	defs, exec, sysPrompt := withNotebook(nil, "writer-1", tools.Definitions(), base, "# Environment")
	if len(defs) != len(tools.Definitions()) || sysPrompt != "# Environment" {
		t.Errorf("a session with no notebook still handed one out: %d defs, prompt %q", len(defs), sysPrompt)
	}
	if out, err := exec(notebook.WriteToolName, nil); err != nil || out != "ok" {
		t.Errorf("the chain was wrapped anyway: %q %v", out, err)
	}
}

// A child gets the navigation toolset the session has and the block that
// explains it. A researcher without `references` is worse at searching than
// the session that delegated the search to it, which is the one thing a
// delegated search must not be; until this it also had no way to read git
// history at all, having neither the git tool nor a command to run one with.
func TestAChildGetsTheNavigationToolsetAndTheBlockThatExplainsIt(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	session := codeToolset()
	session.lsp = lsp.NewToolset(lsp.NewManager(cwd, nil, lsp.Options{}))
	session.structural = structural.NewToolset(cwd)
	if session.structural == nil {
		t.Skip("the workspace root could not be resolved")
	}
	// A coding session's own toolset writes to git, which is the half a
	// child must not inherit.
	session.structural.AllowWrites(structural.Writes{Files: func() []string { return nil }})
	red := openEvidence()
	if red == nil {
		t.Skip("no evidence store, so no evidence tool to be told about")
	}

	defs, exec, sysPrompt, _ := withSessionTools(
		session, red, "researcher-1", cwd, tools.Definitions(), tools.Execute, fixedPrompt("# Environment"))
	names := toolsetNames(defs)

	for _, want := range []string{
		lsp.DefinitionToolName, lsp.ReferencesToolName, lsp.WorkspaceSymbolToolName,
		lsp.DocumentSymbolToolName, lsp.HoverToolName, lsp.DiagnosticsToolName, evidence.ToolName,
	} {
		if !slices.Contains(names, want) {
			t.Errorf("a child was not given %s; it has %v", want, names)
		}
	}
	// git is registered by whether this is a repository, so a child gets it
	// exactly when the session did.
	if session.structural.Has(structural.GitToolName) != slices.Contains(names, structural.GitToolName) {
		t.Errorf("the child's git differs from the session's; it has %v", names)
	}
	// And never the writing half, which the session above does have: a child
	// has no card of its own, so a commit it asked for would have nobody to
	// ask.
	if slices.Contains(names, structural.GitWriteToolName) {
		t.Errorf("a child was given the writing half of git: %v", names)
	}

	// The block names them, over the set the registration actually finished
	// with — the evidence tool included, whose whole job is to answer a
	// reduction notice the child was otherwise never told about.
	for _, want := range []string{"# Toolbox", "- references — ", "- evidence — "} {
		if !strings.Contains(sysPrompt, want) {
			t.Errorf("the child's prompt does not carry %q:\n%s", want, sysPrompt)
		}
	}
	if !strings.Contains(sysPrompt, "# Environment") {
		t.Errorf("the child lost the prompt the blocks were added to:\n%s", sysPrompt)
	}

	// The chain dispatches them too: a malformed question is refused by the
	// language server's own toolset, where a name that fell through to the
	// base executor would come back unknown.
	if _, err := exec(lsp.DefinitionToolName, json.RawMessage(`{"line":1,"symbol":"x"}`)); err == nil ||
		!strings.Contains(err.Error(), "path is required") {
		t.Errorf("the language server's tools were registered but not wired: %v", err)
	}
}

// The structural toolset a child gets is its own, contained to where the
// child is standing. A writer stands in an isolated copy of the checkout, and
// one reading the parent's tree would be reading the code it is not editing.
func TestAChildsStructuralToolsAreContainedToItsOwnWorkspace(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	session := structural.NewToolset(cwd)
	if session == nil {
		t.Skip("the workspace root could not be resolved")
	}
	child := childStructural(session, cwd)
	if child == nil {
		t.Fatal("a session with structural tools handed its children none")
	}
	if !child.Has(structural.GitToolName) {
		t.Skip("not inside a git repository, so the git tool was not registered")
	}
	// This directory, not the checkout above it: a path that climbs out is
	// refused by the root the toolset was built with.
	if _, err := child.Execute(structural.GitToolName,
		json.RawMessage(`{"verb":"status","paths":["../../go.mod"]}`)); err == nil ||
		!strings.Contains(err.Error(), "outside the workspace") {
		t.Errorf("a path outside the child's workspace was not refused: %v", err)
	}
	// And a session that registered none hands out none, so a conversation's
	// children are left exactly as they were.
	if got := childStructural(nil, cwd); got != nil {
		t.Errorf("a session with no structural tools handed its children some: %+v", got)
	}
}

// What the child's window-recovery step measures against, and what it counts
// as spent before its first message. A child is routinely routed to a model
// the session is not on, so the answer has to be about the child's model.
func TestAChildsWindowIsTheTablesAnswerForItsOwnModel(t *testing.T) {
	if got := childWindow(nil, "some-model"); got != 0 {
		t.Errorf("a session with no price table answered %d; the family floor is the fallback", got)
	}
	if got := childToolTokens(nil); got != 0 {
		t.Errorf("no definitions cost %d tokens", got)
	}
	if got := childToolTokens(tools.Definitions()); got <= 0 {
		t.Errorf("the base read-only toolset was costed at %d tokens", got)
	}
}

// fakeLSPEnv turns this test binary into the language server the test below
// spawns. The client's transport seam is package-private to internal/lsp, so
// the only server reachable from here is a real process — this binary again,
// answering on stdio instead of running a suite. TestMain reads the variable
// before it prepares anything (logs_test.go).
const fakeLSPEnv = "SHHH_TEST_FAKE_LSP"

// serveFakeLSP answers the handshake and publishes one error diagnostic for
// every file it is told changed, which is the whole of what an after-edit
// check asks of a server.
func serveFakeLSP(in io.Reader, out io.Writer) {
	r := bufio.NewReader(in)
	var mu sync.Mutex
	write := func(body string) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(out, "Content-Length: %d\r\n\r\n%s", len(body), body)
	}
	diagnose := func(uri string) {
		write(`{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{"uri":` +
			strconv.Quote(uri) +
			`,"diagnostics":[{"range":{"start":{"line":3,"character":1},"end":{"line":3,"character":8}},` +
			`"severity":1,"source":"fake","message":"undefined: greeet"}]}}`)
	}
	for {
		body, err := readLSPFrame(r)
		if err != nil {
			return
		}
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			} `json:"params"`
		}
		if json.Unmarshal(body, &msg) != nil {
			continue
		}
		switch msg.Method {
		case "initialize":
			write(`{"jsonrpc":"2.0","id":` + string(msg.ID) + `,"result":{"capabilities":{}}}`)
		case "shutdown":
			write(`{"jsonrpc":"2.0","id":` + string(msg.ID) + `,"result":null}`)
		case "exit":
			return
		case "textDocument/didOpen", "textDocument/didChange":
			// A file named for it is answered after the client has stopped
			// waiting, which is the case the held queue exists for and the
			// one a child must not leave behind.
			if strings.Contains(msg.Params.TextDocument.URI, "late") {
				uri := msg.Params.TextDocument.URI
				go func() {
					time.Sleep(150 * time.Millisecond)
					diagnose(uri)
				}()
				continue
			}
			diagnose(msg.Params.TextDocument.URI)
		}
	}
}

// readLSPFrame reads one Content-Length-framed message.
func readLSPFrame(r *bufio.Reader) ([]byte, error) {
	length := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if name, value, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			if length, err = strconv.Atoi(strings.TrimSpace(value)); err != nil {
				return nil, err
			}
		}
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

// A writer's applied edit comes back carrying the language server's verdict
// on the file it just wrote, as one applied on the session's own screen does.
// Without it a child learns what it broke by running the build, which is a
// round and an approval for something the server had already answered.
func TestAWritersEditCarriesTheLanguageServersVerdict(t *testing.T) {
	t.Setenv(fakeLSPEnv, "1")
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {\n\tgreeet()\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ts := lsp.NewToolset(lsp.NewManager(root, []lsp.ServerSpec{{
		Name:       "fake",
		Command:    os.Args[0],
		Extensions: []string{".go"},
	}}, lsp.Options{RequestTimeout: 10 * time.Second, DiagnosticsTimeout: 10 * time.Second}))
	defer ts.Close()

	const applied = "Applied 1 edit"
	calls := 0
	exec := withDiagnostics(lspMutationHook(ts), func(name string, args json.RawMessage) (string, error) {
		calls++
		if name == "read_file" {
			return "package main", nil
		}
		return applied, nil
	})

	edit := json.RawMessage(`{"path":` + strconv.Quote(path) + `}`)
	out, err := exec(tools.EditFileName, edit)
	if err != nil {
		t.Fatalf("the edit failed: %v", err)
	}
	if !strings.HasPrefix(out, applied) {
		t.Errorf("the result the child asked for was replaced rather than added to: %q", out)
	}
	if !strings.Contains(out, "undefined: greeet") {
		t.Fatalf("the edit came back without the server's verdict: %q", out)
	}

	// A read is not an edit, and nothing is asked about one.
	if out, err := exec("read_file", edit); err != nil || out != "package main" {
		t.Errorf("a read was sent to the language server: %q %v", out, err)
	}
	if calls != 2 {
		t.Errorf("the chain ran the call %d times", calls)
	}
	// No server detected is the executor unchanged, not a wrap that asks
	// nothing.
	plain := agent.ToolExecutor(func(string, json.RawMessage) (string, error) { return applied, nil })
	if got := withDiagnostics(childMutationHook(nil), plain); got == nil {
		t.Error("a session with no language server lost its executor")
	}
}

// The language server is the session's, and so is its queue of answers that
// arrived after the edit that asked stopped waiting. A child neither reads
// that queue — a verdict about the session's file, or a sibling's, in front
// of its own result — nor leaves anything in it, which would put a verdict
// about a file inside a worktree in front of the person's next edit.
func TestAChildLeavesTheSessionsLateAnswersAlone(t *testing.T) {
	t.Setenv(fakeLSPEnv, "1")
	root := t.TempDir()
	write := func(name string) string {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("package main\n\nfunc main() {\n\tgreeet()\n}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	// A wait short enough that the fake's late answer never makes it, so
	// every edit below leaves an open question behind.
	ts := lsp.NewToolset(lsp.NewManager(root, []lsp.ServerSpec{{
		Name:       "fake",
		Command:    os.Args[0],
		Extensions: []string{".go"},
	}}, lsp.Options{RequestTimeout: 10 * time.Second, DiagnosticsTimeout: 20 * time.Millisecond}))
	defer ts.Close()

	edit := func(hook chat.MutationHook, path string) string {
		exec := withDiagnostics(hook, func(string, json.RawMessage) (string, error) { return "Applied 1 edit", nil })
		out, err := exec(tools.EditFileName, json.RawMessage(`{"path":`+strconv.Quote(path)+`}`))
		if err != nil {
			t.Fatalf("the edit failed: %v", err)
		}
		return out
	}

	// The session's own hook leaves its question open, which is what lets a
	// late answer reach the model at all.
	session := write("late-session.go")
	if out := edit(lspMutationHook(ts), session); strings.Contains(out, "undefined") {
		t.Fatalf("the answer arrived inside the wait, so nothing was held: %q", out)
	}
	// A child's does not, and takes nothing from the queue either.
	child := write("late-child.go")
	if out := edit(childMutationHook(ts), child); strings.Contains(out, "undefined") {
		t.Fatalf("a child's edit carried a verdict it should not have: %q", out)
	}
	// Both answers have landed by now. Only the session's is waiting.
	time.Sleep(250 * time.Millisecond)
	held := ts.Manager.TakeHeldDiagnostics()
	if !strings.Contains(held, filepath.Base(session)) {
		t.Errorf("the session's own late answer was lost: %q", held)
	}
	if strings.Contains(held, filepath.Base(child)) {
		t.Errorf("a child left a question behind for the session to collect: %q", held)
	}
}

// What a run with nobody in front of it answers a child's routed request
// with. The patch is the one yes, and it is a yes exactly where the run may
// change the tree at all — a run that could spawn a writer and could not take
// what the writer wrote would spend the whole fan-out for nothing, and the
// child's own report would say the user declined it about a run with no user.
func TestAnswerChildAsk_ThePatchIsTheOneRequestARunAnswers(t *testing.T) {
	patch := subagent.NewAsk("writer-1", subagent.AskPatch, "apply 3 files")
	clashing := subagent.NewAsk("writer-2", subagent.AskPatch, "apply 1 file")
	clashing.Warnings = []string{"overwrites a.go, already changed by writer-1"}

	for name, tc := range map[string]struct {
		ask    *subagent.Ask
		writes bool
		want   bool
	}{
		"a patch from a run that may write":      {patch, true, true},
		"a patch from a run that may not":        {patch, false, false},
		"a patch that overlaps one already made": {clashing, true, false},
		"a command the child stopped to ask on":  {subagent.NewAsk("w", subagent.AskCommand, "run rm -rf /"), true, false},
		"an edit inside the child's own tree":    {subagent.NewAsk("w", subagent.AskEdit, "write a.go"), true, false},
		"anything else a child routes":           {subagent.NewAsk("w", subagent.AskGeneric, "use a tool"), true, false},
	} {
		if got := answerChildAsk(tc.ask, tc.writes); got != tc.want {
			t.Errorf("%s = %v, want %v", name, got, tc.want)
		}
	}
}

// The two flags that give a run something to say beyond a refusal, and the
// one predicate both the delegate offer and the patch answer read.
func TestPrintOptsAnswered_EitherFlagIsAnAnswer(t *testing.T) {
	for name, tc := range map[string]struct {
		opts printOpts
		want bool
	}{
		"nothing given":  {printOpts{}, false},
		"--yes":          {printOpts{yes: true}, true},
		"--mode auto":    {printOpts{autoMode: true}, true},
		"both":           {printOpts{yes: true, autoMode: true}, true},
		"--allow alone":  {printOpts{allow: []string{"go test"}}, false},
		"--sandbox only": {printOpts{sandbox: true}, false},
	} {
		if got := tc.opts.answered(); got != tc.want {
			t.Errorf("%s = %v, want %v", name, got, tc.want)
		}
	}
}

// hookRunnerSaying is a runner whose one pre_tool hook answers with the given
// stdout and exit code, and records the payloads it was handed.
func hookRunnerSaying(t *testing.T, stdout string, code int, seen *[]hook.Payload) *hook.Runner {
	t.Helper()
	set := hook.Load(map[string]hook.Entry{
		"guard": {Event: hook.PreTool, Command: "guard"},
	}, "config.toml", "")
	exec := func(_ context.Context, _ string, stdin []byte) (string, int, error) {
		var p hook.Payload
		if err := json.Unmarshal(stdin, &p); err != nil {
			t.Errorf("a hook was handed something that is not the payload: %v", err)
		}
		*seen = append(*seen, p)
		return stdout, code, nil
	}
	return hook.NewRunner(set, exec, time.Second, "/work")
}

// childSeam is the seam a running child hands a wrap, with somewhere to read
// each half of it back from.
type childSeam struct {
	notes     []string
	decisions []string
}

func (c *childSeam) seam() subagent.Seam {
	return subagent.Seam{
		At:     func() observe.Pos { return observe.Pos{Turn: 2, Round: 7} },
		Note:   func(text string) { c.notes = append(c.notes, text) },
		Record: func(decision, code string) { c.decisions = append(c.decisions, decision+"/"+code) },
	}
}

// A rule the person wrote holds on a child's calls as it holds on the
// session's. It has to: a deny hook that stopped at the orchestrator would be
// a deny hook anybody could walk around by delegating the act, and the model
// asking for the call is the one deciding what to delegate.
func TestAPreToolDenyHookRefusesAChildsCommand(t *testing.T) {
	var payloads []hook.Payload
	r := hookRunnerSaying(t, "", hook.DenyExit, &payloads)

	seam := &childSeam{}
	ran := false
	resolve := childHookGated(r)(seam.seam(),
		func(provider.ToolCall) string { ran = true; return "ran" })

	got := resolve(provider.ToolCall{ID: "c1", Name: tools.ExecCommandName,
		Arguments: `{"command":"rm -rf vendor"}`})
	if ran {
		t.Fatal("the child's approval path ran a call a hook refused")
	}
	if got != hook.DeniedResult("guard") {
		t.Fatalf("a child is told something other than what a session is told:\n%s", got)
	}
	// The seam is in front of the command branch, so the hook is told about
	// the command rather than about a result it never produced.
	if len(payloads) != 1 || payloads[0].Tool != tools.ExecCommandName {
		t.Fatalf("the hook was not told about the command: %+v", payloads)
	}
	// And where the child is, which a child can answer and an unattended run
	// cannot: it counts its own turns and rounds.
	if payloads[0].Turn != 2 || payloads[0].Round != 7 {
		t.Errorf("the hook was told the wrong position: turn %d round %d", payloads[0].Turn, payloads[0].Round)
	}
	if len(seam.decisions) != 1 || !strings.HasPrefix(seam.decisions[0], observe.DecisionDeny) {
		t.Errorf("a child's refusal was not recorded the way a session's is: %v", seam.decisions)
	}
}

// What a hook says about a child goes on the child's own transcript. A child
// has no screen, and the only one it could reach belongs to the session that
// spawned it — a line printed there would be drawn over a running TUI.
func TestAHooksLineAboutAChildGoesOnTheChildsTranscript(t *testing.T) {
	var payloads []hook.Payload
	r := hookRunnerSaying(t, `{"note":"vendor is off limits"}`, 0, &payloads)

	seam := &childSeam{}
	resolve := childHookGated(r)(seam.seam(), func(provider.ToolCall) string { return "ran" })
	resolve(provider.ToolCall{Name: tools.ExecCommandName, Arguments: `{"command":"go build"}`})

	if len(seam.notes) == 0 || !strings.Contains(seam.notes[0], "vendor is off limits") {
		t.Fatalf("the hook's line did not reach the child's transcript: %v", seam.notes)
	}
}

// The auto-run tier is the other dispatcher, and a hook sits inside it the
// same way. A read is where most of a fan-out's calls are.
func TestAChildsAutoRunCallsMeetTheHookSeam(t *testing.T) {
	var payloads []hook.Payload
	r := hookRunnerSaying(t, "", hook.DenyExit, &payloads)

	seam := &childSeam{}
	ran := false
	exec := childHookAuto(r)(seam.seam(), func(string, json.RawMessage) (string, error) {
		ran = true
		return "contents", nil
	})

	got, err := exec(tools.ReadFileName, json.RawMessage(`{"path":"vendor/x.go"}`))
	if err != nil {
		t.Fatalf("a refusal is a result and never an error: %v", err)
	}
	if ran {
		t.Fatal("the child's read dispatcher ran a call a hook refused")
	}
	if got != hook.DeniedResult("guard") {
		t.Fatalf("a child is told something other than what a session is told:\n%s", got)
	}
}

// A session that runs no hooks hands its children no wraps at all, so the
// dispatchers they get are exactly the ones the Env built.
func TestASessionWithNoHooksWrapsNothingOnItsChildren(t *testing.T) {
	if childHookAuto(nil) != nil || childHookGated(nil) != nil {
		t.Fatal("a session with no hooks put a wrap on its children anyway")
	}
}

// A child reasons from the memories the session recalled. The parent read
// them once for the whole fan-out, and a child querying the table for itself
// could be working from something the session it serves was never told.
func TestChildExtraCarriesTheParentsRecalledMemory(t *testing.T) {
	block := memory.PromptBlock([]memory.Entry{
		{ID: 4, Scope: "proj", Kind: memory.KindConvention, Text: "tests go beside the code"},
	})
	if block == "" {
		t.Fatal("no block to hand over")
	}
	extra := childExtra("", "# Project", block, "", false)
	if !strings.Contains(extra, "tests go beside the code") {
		t.Fatalf("a child was not handed what the session recalled:\n%s", extra)
	}
	if !strings.Contains(extra, "m4") {
		t.Fatalf("the citation went missing, so the child cannot name what it is following:\n%s", extra)
	}
}

// The reading a child watches its workspace with is the session's own,
// pointed at where the child is standing rather than at the parent's
// directory — a writer edits an isolated copy, and a reading taken in the
// checkout it was copied from would report the wrong tree entirely.
func TestAChildsTreeReadingIsTakenWhereTheChildStands(t *testing.T) {
	c := childTree(config.Config{}, sessionSibling{}, "/work/wt-1", true)
	if c == nil {
		t.Fatal("the reading is on by default and a child got none")
	}
	if c.Dir != "/work/wt-1" {
		t.Errorf("the reading is taken in %q, not where the child is standing", c.Dir)
	}
	if c.ReadChanged == nil {
		t.Error("a child was given no record of what has been shown to it")
	}
	// A worktree is a directory nobody else has open, so there is no other
	// session to name in it.
	if c.Sibling != nil {
		t.Error("a writer's own copy of the checkout was told another session is in it")
	}

	reader := childTree(config.Config{}, sessionSibling{read: func() (time.Time, bool) {
		return time.Now(), true
	}}, "/work", false)
	if reader == nil || reader.Sibling == nil || !reader.Sibling() {
		t.Error("a child standing in the parent's own checkout should be told who else is in it")
	}

	off := false
	cfg := config.Config{}
	cfg.Behavior.TreeCheck = &off
	if c := childTree(cfg, sessionSibling{}, "/work", false); c != nil {
		t.Errorf("a reading the config turned off reached a child anyway: %+v", c)
	}
}

// One recall for every surface that opens a conversation. A preference the
// person stated once is about the work, not about which door they came in by,
// and the block a session puts in front of the model is the block an
// unattended run and a served session put there too — and the one a child is
// handed rather than querying for itself.
func TestRecallMemory_IsOneAnswerForEverySurface(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	store := openMemoryStore(db)
	if store == nil {
		t.Fatal("no memory store to recall from")
	}
	if _, err := store.Add(store.Project(), memory.KindConvention,
		"commit straight to master", memory.ProvenanceUser); err != nil {
		t.Fatalf("add: %v", err)
	}

	cmd := &cobra.Command{}
	cmd.SetContext(withConfig(context.Background(), config.Config{}))

	var session chatSession
	if got := recallMemory(cmd, &session, db); got == nil {
		t.Fatal("the surface was given no store to propose or forget with")
	}
	if !strings.Contains(session.memoryBlock, "commit straight to master") {
		t.Fatalf("nothing was recalled:\n%s", session.memoryBlock)
	}
	// The block goes into the prompt as well as being kept, because the
	// session is the party that reasons from it.
	if !strings.Contains(session.promptExtra, session.memoryBlock) {
		t.Fatalf("the recalled block never reached the prompt:\n%s", session.promptExtra)
	}

	// And nothing at all where the person turned memory off, on every
	// surface at once.
	off := config.Config{}
	off.Behavior.MemoryDisabled = true
	cmd.SetContext(withConfig(context.Background(), off))
	var silent chatSession
	if got := recallMemory(cmd, &silent, db); got != nil || silent.memoryBlock != "" {
		t.Fatalf("memory.disabled still recalled: %q", silent.memoryBlock)
	}
}

// A child's write is put to one hook once. It is the case the two guards
// exist for: a write is the one gated call a child resolves through its own
// dispatcher, so the seam behind it is on the mutation chain and the approver
// steps over it — and either guard missing would have one edit fire one
// person's formatter twice.
func TestAChildsWriteIsPutToTheSeamBehindItExactlyOnce(t *testing.T) {
	set := hook.Load(map[string]hook.Entry{
		"before": {Event: hook.PreTool, Command: "before"},
		"after":  {Event: hook.PostTool, Command: "after"},
	}, "config.toml", "")
	var mu sync.Mutex
	var events []string
	r := hook.NewRunner(set, func(_ context.Context, _ string, stdin []byte) (string, int, error) {
		var p hook.Payload
		if err := json.Unmarshal(stdin, &p); err != nil {
			t.Errorf("a hook was handed something that is not the payload: %v", err)
		}
		mu.Lock()
		events = append(events, p.Event+":"+p.Tool)
		mu.Unlock()
		return "", 0, nil
	}, time.Second, "/work")

	// The child's gated dispatcher as buildSupervisor assembles it: the
	// mutation chain over the executor, and the approver's wrap around the
	// resolution that reaches it.
	gatedExec := withDiagnostics(chainMutation(nil, childPostMutation(r)),
		func(name string, _ json.RawMessage) (string, error) { return "wrote " + name, nil })
	seam := &childSeam{}
	resolve := childHookGated(r)(seam.seam(), func(tc provider.ToolCall) string {
		out, err := gatedExec(tc.Name, json.RawMessage(tc.Arguments))
		if err != nil {
			return "error: " + err.Error()
		}
		return out
	})

	resolve(provider.ToolCall{Name: tools.WriteFileName, Arguments: `{"path":"a.go","content":"package a\n"}`})

	mu.Lock()
	defer mu.Unlock()
	want := []string{hook.PreTool + ":" + tools.WriteFileName, hook.PostTool + ":" + tools.WriteFileName}
	if len(events) != len(want) {
		t.Fatalf("a child's write met the seams %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("a child's write met the seams %v, want %v", events, want)
		}
	}
}

// And a gated call that is not a write meets the seam behind it once too,
// on the approver rather than the mutation chain — which is the half the
// guard in childPostMutation is for.
func TestAChildsFetchDoesNotMeetTheMutationSeam(t *testing.T) {
	post := childPostMutation(hook.NewRunner(
		hook.Load(map[string]hook.Entry{"after": {Event: hook.PostTool, Command: "after"}}, "config.toml", ""),
		func(context.Context, string, []byte) (string, int, error) {
			t.Error("a fetch was put to the mutation seam")
			return "", 0, nil
		}, time.Second, "/work"))
	if got := post(web.FetchToolName, json.RawMessage(`{"url":"https://example.com"}`), "a page"); got != "a page" {
		t.Fatalf("the result was rewritten by a seam that should not have fired: %q", got)
	}
}

// A child is handed the orchestration tools only where it has a level below
// it, so what it can reach for is what the depth limit will actually let it
// do rather than a schema it pays for and is always refused.
func TestDelegationToolsReachAChildWithALevelBelowIt(t *testing.T) {
	sup := subagent.New(t.Context(), subagent.Options{
		Root:     t.TempDir(),
		MaxDepth: 3,
		NewEnv: func(context.Context, subagent.Spec) (subagent.Env, error) {
			return subagent.Env{}, nil
		},
	})
	t.Cleanup(sup.Close)
	agents := &agentProfiles{profiles: subagent.BuiltinProfiles()}

	for _, tc := range []struct {
		name    string
		depth   int
		def     config.AgentDefinition
		offered bool
	}{
		{"a child of the session", 2, config.AgentDefinition{Name: "critic"}, true},
		{"the deepest level", 3, config.AgentDefinition{Name: "critic"}, false},
		{"a profile that named the spawn", 2,
			config.AgentDefinition{Name: "critic", Tools: []string{"read_file", config.SpawnAgentTool}}, true},
		{"a profile that named the collection", 2,
			config.AgentDefinition{Name: "critic", Tools: []string{"read_file", config.ReportAgentTool}}, true},
		{"a profile whose allowlist leaves them out", 2,
			config.AgentDefinition{Name: "critic", Tools: []string{"read_file", "search"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gated := map[string]bool{}
			spec := subagent.Spec{Name: "critic-1", Depth: tc.depth}
			defs, exec := withDelegation(sup, agents, tc.def, spec, nil, nil, gated)
			names := toolsetNames(defs)
			if got := containsString(names, subagent.SpawnToolName); got != tc.offered {
				t.Fatalf("%s was offered the spawn = %v, want %v (%v)", tc.name, got, tc.offered, names)
			}
			if !tc.offered {
				if exec != nil {
					t.Error("an agent with nothing to delegate to had its executor wrapped")
				}
				if gated[subagent.SpawnToolName] {
					t.Error("an agent with no spawn had it gated")
				}
				return
			}
			// The four travel together: an agent that can start a child can
			// collect, redirect and re-run it.
			for _, want := range []string{subagent.SpawnToolName, subagent.ReportToolName,
				subagent.SteerToolName, subagent.RetryToolName} {
				if !containsString(names, want) {
					t.Errorf("%s is missing from a delegating agent's toolset: %v", want, names)
				}
			}
			// Starting an agent is a decision wherever it is taken; the other
			// three start nothing and are auto-run.
			if !gated[subagent.SpawnToolName] {
				t.Error("a child's spawn is not gated, so it would start an agent nobody approved")
			}
			for _, auto := range []string{subagent.ReportToolName, subagent.SteerToolName, subagent.RetryToolName} {
				if gated[auto] {
					t.Errorf("%s was gated; it starts nothing and has no card to put to anyone", auto)
				}
			}
			// And the wrap is the supervisor's, under this child's own name:
			// a call through it reaches the roster rather than falling through
			// to a dispatcher that has never heard of the tool.
			out, err := exec(subagent.ReportToolName, json.RawMessage(`{}`))
			if err != nil {
				t.Fatalf("the delegation wrap did not reach the supervisor: %v", err)
			}
			if !strings.Contains(out, "spawned no agents") {
				t.Errorf("the roster a fresh child reads is %q", out)
			}
		})
	}
}

// The profile format names the two tools in `config`, which cannot import
// `subagent` — it is a leaf and the tool names belong to the package that
// registers them. So the two spellings are held equal here, in the package
// that has both: a profile allowlist naming a tool the child never gets
// would be a file that validates and does nothing.
func TestTheProfileFormatSpellsTheDelegationToolsTheWayTheyAreRegistered(t *testing.T) {
	if config.SpawnAgentTool != subagent.SpawnToolName {
		t.Errorf("a profile names %q and the supervisor registers %q", config.SpawnAgentTool, subagent.SpawnToolName)
	}
	if config.ReportAgentTool != subagent.ReportToolName {
		t.Errorf("a profile names %q and the supervisor registers %q", config.ReportAgentTool, subagent.ReportToolName)
	}
}

// A supervisor is never nil in a session, but a surface that builds none
// hands a child no way to delegate rather than a panic.
func TestDelegationToolsAreAbsentWithoutASupervisor(t *testing.T) {
	defs, exec := withDelegation(nil, &agentProfiles{}, config.AgentDefinition{Name: "critic"},
		subagent.Spec{Name: "critic-1", Depth: 2}, nil, nil, map[string]bool{})
	if len(defs) != 0 || exec != nil {
		t.Fatalf("a session with no supervisor offered %v", toolsetNames(defs))
	}
}

// The model an agent runs on, layer by layer, through the function the
// supervisor actually asks.
func TestModelForLayersTheCallTheRoleTheDepthAndTheSession(t *testing.T) {
	cfg := config.Config{}
	cfg.Agents.Model = "agents-default"
	cfg.Agents.Depths = map[string]config.AgentDepth{"3": {Model: "grandchildren-here"}}
	agents := &agentProfiles{definitions: map[string]config.AgentDefinition{
		"critic": {Name: "critic", Model: "the-critics-own"},
	}}

	for _, tc := range []struct {
		name      string
		role      string
		depth     int
		requested string
		want      string
	}{
		{"the call outranks everything", "critic", 3, "asked-for", "asked-for"},
		{"a profile file's model at any depth", "critic", 3, "", "the-critics-own"},
		{"the depth's own default", "writer", 3, "", "grandchildren-here"},
		{"a depth with no entry falls to the agents default", "writer", 2, "", "agents-default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := agents.modelFor(cfg, subagent.Role(tc.role), tc.depth, tc.requested, "session-model")
			if got != tc.want {
				t.Errorf("modelFor(%s, depth %d) = %q, want %q", tc.role, tc.depth, got, tc.want)
			}
		})
	}
	// And with nothing configured anywhere, the session's own.
	bare := &agentProfiles{}
	if got := bare.modelFor(config.Config{}, subagent.RoleWriter, 2, "", "session-model"); got != "session-model" {
		t.Errorf("an unconfigured child runs on %q, want the session model", got)
	}
}

// composed is a profile's prompt over the profile's own grants: what
// profileEnv's composer says before the session's shared tools are added.
func composed(compose func([]string) string, defs []provider.Tool, _ agent.ToolExecutor) string {
	return compose(toolsetNames(defs))
}

// fakeMCPEnv turns this test binary into a stdio MCP server with one tool,
// the way fakeLSPEnv turns it into a language server: the toolset's
// constructors are package-private to internal/mcp, so a server reachable
// from here is a real process. TestMain reads the variable (logs_test.go).
const fakeMCPEnv = "SHHH_TEST_FAKE_MCP"

func serveFakeMCP() {
	server := sdk.NewServer(&sdk.Implementation{Name: "docs", Version: "1"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "lookup", Description: "Look a page up."},
		func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "a page"}}}, nil, nil
		})
	if err := server.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		os.Exit(1)
	}
}

// A profile's tool section is read off the names it is handed, and a child
// holds more than its grants: the notebook and a server the person marked
// read-only go on with everything the session shares. Composed over the
// grants alone, the section told the child it had neither while the toolbox
// under it listed the notebook.
func TestAProfilesToolSectionNamesTheSharedToolsItHolds(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ts := mcp.Connect(t.Context(), &mcp.Catalog{Servers: []mcp.Definition{{
		Name: "docs", Scope: mcp.ScopeUser, Transport: mcp.TransportStdio,
		Command: exe, Env: map[string]string{fakeMCPEnv: "1"}, ReadOnly: true,
	}}}, mcp.Options{Timeout: 20 * time.Second})
	t.Cleanup(ts.Close)
	reads := ts.ReadOnlyDefinitions()
	if len(reads) == 0 {
		t.Fatalf("the fake server offered no read: %+v", ts.Reports)
	}
	session := chatSession{notebook: notebook.New(nil), mcpTools: ts}

	info := shell.Info{OS: "linux", Cwd: "/w"}
	def := config.AgentDefinition{Name: "auditor", Description: "reads the tree"}
	compose, defs, base := profileEnv(def, subagent.Spec{}, info, "", nil, nil, map[string]bool{})
	grantsOnly := composed(compose, defs, base)
	_, _, sysPrompt, _ := withSessionTools(session, nil, "auditor-1", t.TempDir(), defs, base, compose)

	section := func(p string) string {
		_, rest, ok := strings.Cut(p, "\n\n# Tools\n")
		if !ok {
			t.Fatalf("no tool section in:\n%s", p)
		}
		body, _, _ := strings.Cut(rest, "\n\n#")
		return body
	}
	want := []string{notebook.WriteToolName, notebook.ReadToolName}
	for _, d := range reads {
		want = append(want, d.Name)
	}
	got := section(sysPrompt)
	for _, name := range want {
		if !strings.Contains(got, name) {
			t.Errorf("the tool section does not name %s:\n%s", name, got)
		}
		// The grants alone are not these, which is why the section is
		// composed over the finished set.
		if strings.Contains(section(grantsOnly), name) {
			t.Errorf("the grants-only section names %s, which only the session adds", name)
		}
	}
	// The blocks describing them still follow the role's own prompt.
	if !strings.HasPrefix(sysPrompt, `You are the "auditor" sub-agent`) || !strings.Contains(sysPrompt, "# Toolbox") {
		t.Errorf("the role's prompt no longer leads the blocks:\n%s", sysPrompt)
	}
}
