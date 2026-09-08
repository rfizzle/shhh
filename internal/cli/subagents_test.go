package cli

import (
	"bufio"
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

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/chat"
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

	reader := childExtra("", "# Project\nbe helpful", workspace, false)
	if !strings.Contains(reader, "Git branch: side") || !strings.Contains(reader, "2 uncommitted paths") {
		t.Errorf("a child should be handed the checkout the session was handed:\n%s", reader)
	}
	if !strings.Contains(reader, "be helpful") {
		t.Errorf("the project's own instructions still come with it:\n%s", reader)
	}
	if strings.Contains(reader, "isolated copy") {
		t.Errorf("a reader stands in the parent's own directory:\n%s", reader)
	}

	writer := childExtra("", "", workspace, true)
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
	extra := childExtra("", child, "", false)
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
		session, red, "researcher-1", cwd, tools.Definitions(), tools.Execute, "# Environment")
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
