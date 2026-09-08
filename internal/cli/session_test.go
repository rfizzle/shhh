package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// endpointProvider is a provider whose endpoint reports the context length it
// serves each model at. The embedded interface is nil: nothing under test
// streams or names it.
type endpointProvider struct {
	provider.Provider
	windows map[string]int64
	err     error
}

func (e endpointProvider) ModelWindows(context.Context) (map[string]int64, error) {
	return e.windows, e.err
}

// await polls the lookup until the background query lands, which is how the
// session reads it: on a later frame, not on the one that asked. The wait is
// what a passing test costs when the answer never comes, so the callers that
// expect no answer pass a short one.
func await(t *testing.T, lookup func(string) (int64, bool), model string, wait time.Duration) (int64, bool) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for {
		if w, ok := lookup(model); ok {
			return w, true
		}
		if time.Now().After(deadline) {
			return 0, false
		}
		time.Sleep(time.Millisecond)
	}
}

func TestEndpointWindowsFor(t *testing.T) {
	lookup := endpointWindowsFor(endpointProvider{windows: map[string]int64{"qwen3:8b": 262_144}})
	if lookup == nil {
		t.Fatal("a provider that can report its windows should get a lookup")
	}
	// The catalog's ids are lower-cased, so the session's own spelling of the
	// model still finds them.
	if w, ok := await(t, lookup, "Qwen3:8B", 2*time.Second); !ok || w != 262_144 {
		t.Fatalf("window = %d, %v; want 262144, true", w, ok)
	}
	if _, ok := lookup("claude-opus-5"); ok {
		t.Error("a model the endpoint did not describe must fall through to the table")
	}
}

// A failed probe is silent: the session reads the table, which is what it did
// before anything was asked.
func TestEndpointWindowsFor_FailureLeavesTheLookupEmpty(t *testing.T) {
	lookup := endpointWindowsFor(endpointProvider{err: errors.New("no catalog here")})
	if lookup == nil {
		t.Fatal("expected a lookup")
	}
	if _, ok := await(t, lookup, "qwen3:8b", 250*time.Millisecond); ok {
		t.Error("a failed query must not answer")
	}
}

func TestEndpointWindowsFor_ProviderWithoutTheCapability(t *testing.T) {
	if lookup := endpointWindowsFor(struct{ provider.Provider }{}); lookup != nil {
		t.Error("a provider whose endpoint cannot answer should get no lookup")
	}
}

// The prompt a session opens on is assembled in one place because a session
// boundary assembles it again: the config's standing addition and everything
// the session gathered for itself have to reach the second build the way they
// reached the first, and the second build has to be a build — a cached string
// would hand the new conversation the checkout as it stood when the process
// started.
func TestSessionPrompt_BuiltAgainWithBothExtrasEveryTime(t *testing.T) {
	var extras []string
	s := chatSession{
		promptExtra: "what the session gathered",
		buildPrompt: func(_ shell.Info, extra ...string) string {
			extras = append(extras, strings.Join(extra, "\n"))
			return "system prompt"
		},
	}

	if text, _, _ := s.systemPrompt("what the config says"); text != "system prompt" {
		t.Fatalf("the prompt is the builder's answer, got %q", text)
	}
	s.systemPrompt("what the config says")

	if len(extras) != 2 {
		t.Fatalf("every call should build, got %d builds", len(extras))
	}
	for i, got := range extras {
		if !strings.Contains(got, "what the config says") || !strings.Contains(got, "what the session gathered") {
			t.Fatalf("build %d lost an extra: %q", i, got)
		}
	}
}

// resumeStore is a store of this test's own, on a path nothing else writes.
func resumeStore(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// A conversation that was named is the one that opens. "The newest slot" is
// the answer to --continue's question, and answering this one with it opens
// somebody else's conversation under the name the person typed.
func TestResumeChat_ANamedChatIsNotTheMostRecent(t *testing.T) {
	db := resumeStore(t)
	if err := db.SaveChat("the one I want", []provider.Message{
		{Role: provider.RoleUser, Content: "the widget"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := db.SaveChat("something else", []provider.Message{
		{Role: provider.RoleUser, Content: "the other thing"}}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := chatSession{resumeName: "the one I want"}.resumeChat(db)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got.slot != "the one I want" {
		t.Fatalf("slot = %q, want the one that was named", got.slot)
	}
	if len(got.messages) != 1 || got.messages[0].Content != "the widget" {
		t.Fatalf("messages = %+v, want that conversation's", got.messages)
	}
}

// --continue asks the store which slot is newest, because every session
// autosaves to one of its own and "the last session" is a query.
func TestResumeChat_ContinueTakesTheNewestSlot(t *testing.T) {
	db := resumeStore(t)
	if err := db.SaveChat("older", []provider.Message{
		{Role: provider.RoleUser, Content: "then"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := db.SaveChat("newer", []provider.Message{
		{Role: provider.RoleUser, Content: "now"}}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := chatSession{continueLast: true}.resumeChat(db)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got.slot != "newer" {
		t.Fatalf("slot = %q, want the newest", got.slot)
	}
}

// --continue on a machine with no history is a first run, not a mistake: it
// starts, and says it started.
func TestResumeChat_ContinueWithNothingSavedStartsFresh(t *testing.T) {
	got, err := chatSession{continueLast: true}.resumeChat(resumeStore(t))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got.slot != "" || got.cancelled {
		t.Fatalf("got %+v, want a fresh conversation", got)
	}
}

// A conversation named and not found is worth stopping for: carrying on would
// run the prompt against nothing under a name the person chose.
func TestResumeChat_ANameThatIsNotThereStops(t *testing.T) {
	_, err := chatSession{resumeName: "never saved"}.resumeChat(resumeStore(t))
	if err == nil {
		t.Fatal("a named conversation that is missing must stop the run")
	}
}

// Without a store there is nothing to resume, and saying so beats starting a
// conversation the flag said would be continued.
func TestResumeChat_WithoutAStore(t *testing.T) {
	_, err := chatSession{continueLast: true}.resumeChat(nil)
	if err == nil || !strings.Contains(err.Error(), "cannot resume") {
		t.Fatalf("err = %v, want one naming what is unavailable", err)
	}
}

// heldChat saves name and hands the slot to another running process, the way
// a second session's autosave leaves it. The parent is the one process a
// test can name portably and still know is alive.
func heldChat(t *testing.T, db *storage.DB, name string) {
	t.Helper()
	if err := db.SaveChat(name, []provider.Message{
		{Role: provider.RoleUser, Content: "theirs"}}); err != nil {
		t.Fatalf("save %s: %v", name, err)
	}
	id, err := db.StartAgentSession("code", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start the other session: %v", err)
	}
	if err := db.LinkAgentSession(id, name); err != nil {
		t.Fatalf("link %s: %v", name, err)
	}
	if _, err := db.SQL().Exec(
		`UPDATE agent_sessions SET pid = ? WHERE id = ?`, os.Getppid(), id); err != nil {
		t.Fatalf("place the other session: %v", err)
	}
}

// --continue asks for the last session, and the last session can be a slot
// another process is still writing into. The flag is an instruction, so it
// is refused by name rather than answered with the conversation before it —
// and the name is the way through, since a slot asked for by name opens
// whoever holds it.
func TestResumeChat_ContinueRefusesASlotSomebodyElseHolds(t *testing.T) {
	db := resumeStore(t)
	if err := db.SaveChat("older", []provider.Message{
		{Role: provider.RoleUser, Content: "mine"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	heldChat(t, db, "newest")

	_, err := chatSession{continueLast: true}.resumeChat(db)
	if err == nil {
		t.Fatal("--continue opened a slot another session is writing into")
	}
	if !strings.Contains(err.Error(), "newest") ||
		!strings.Contains(err.Error(), "open in another session") {
		t.Fatalf("err = %v, want one naming the slot and why", err)
	}

	got, err := chatSession{resumeName: "newest"}.resumeChat(db)
	if err != nil {
		t.Fatalf("resume by name: %v", err)
	}
	if got.slot != "newest" {
		t.Fatalf("slot = %q, want the one that was named", got.slot)
	}
}

// One mark, two pickers. The picker `shhh code --resume` shows marks a slot
// another running session is autosaving into and refuses to open it, in the
// words the picker inside a session says, while the row stays a row that can
// be read, renamed or deleted. A slot nobody holds opens as it always did.
func TestChatBrowseRows_ASlotSomebodyElseHoldsRefusesToOpen(t *testing.T) {
	db := resumeStore(t)
	if err := db.SaveChat("mine", []provider.Message{
		{Role: provider.RoleUser, Content: "ours"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	heldChat(t, db, "theirs")

	entries, err := db.ListChats()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	rows := map[string]components.ChatRow{}
	for _, row := range chatBrowseRows(db, entries) {
		rows[row.ID] = row
	}
	if len(rows) != 2 {
		t.Fatalf("both slots are listed, got %d", len(rows))
	}

	held := rows["theirs"]
	if held.Mark != "open in another session" {
		t.Fatalf("mark = %q, want the words the row states", held.Mark)
	}
	if !strings.Contains(held.Refused, `"theirs"`) ||
		!strings.Contains(held.Refused, "open in another session") ||
		!strings.Contains(held.Refused, "still being written there") {
		t.Fatalf("refused = %q, want the words the picker inside a session says", held.Refused)
	}
	if free := rows["mine"]; free.Refused != "" || free.Mark != "" {
		t.Fatalf("a slot nobody holds opens, got %+v", free)
	}
}

// The host half of the browser: what the screen closed with is what the
// command opens, and a housekeeping key reaches the store and comes back as a
// notice with the screen still up.
func TestChatsModel_TheScreensAnswerReachesTheCommand(t *testing.T) {
	db := resumeStore(t)
	for _, name := range []string{"alpha", "beta"} {
		if err := db.SaveChat(name, []provider.Message{
			{Role: provider.RoleUser, Content: "hello"}}); err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
	}
	entries, err := db.ListChats()
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	m := newChatsModel(db, entries)
	if len(m.screen.Rows) != 2 || m.screen.Subject != "2 conversations" {
		t.Fatalf("the screen was not filled from the store: %+v", m.screen.Subject)
	}

	// A rename with the screen still up reaches the store and the rows are
	// read back from it rather than patched in place.
	if cmd := m.answer(false, components.ChatResult{
		Do: &components.ChatCommand{Act: components.ChatRename, ID: "alpha", Name: "gamma"},
	}); cmd != nil {
		t.Fatal("housekeeping closed the browser")
	}
	if !strings.Contains(m.screen.Notice, `renamed "alpha" to "gamma"`) {
		t.Fatalf("notice = %q, want what the key did", m.screen.Notice)
	}
	if _, err := db.LoadChat("gamma"); err != nil {
		t.Fatalf("the rename did not reach the store: %v", err)
	}

	if cmd := m.answer(true, components.ChatResult{Open: true, ID: "gamma"}); cmd == nil {
		t.Fatal("the screen closing did not end the program")
	}
	if !m.result.Open || m.result.ID != "gamma" {
		t.Fatalf("result = %+v, want the conversation the screen chose", m.result)
	}
}

// The card a git write asks through states the boundaries of the act. Two of
// them are stated on every verb, because the question a person asks when an
// agent touches git is what it can reach, and an answer that appears on some
// cards and not others is one they have to go looking for. The other two are
// stated on the one verb that cannot be taken back.
func TestGitWriteGatedPreview_StatesTheBoundariesOfTheAct(t *testing.T) {
	// A real repository, because the write tool exists only where git found
	// a history to write to.
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git init failed (%v): %s", err, out)
	}
	st := structural.NewToolset(root)
	if st == nil {
		t.Skip("no workspace root")
	}
	st.AllowWrites(structural.Writes{Files: func() []string { return nil }})
	if !st.Has(structural.GitWriteToolName) {
		t.Skip("git is not available here")
	}

	labels := func(p chat.GatedPreview) map[string]chat.GatedField {
		m := map[string]chat.GatedField{}
		for _, f := range p.Fields {
			m[f.Label] = f
		}
		return m
	}

	staging, err := gitWriteGatedPreview(st, json.RawMessage(`{"verb":"add","paths":["a.go"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !staging.Write || staging.DenyLine != "git add" || staging.Title != "stage 1 file" {
		t.Fatalf("a staging card: %+v", staging)
	}
	fields := labels(staging)
	if fields["push"].Value != "no" || fields["stages"].Value == "" {
		t.Fatalf("every card states what it stages and that nothing is pushed: %+v", staging.Fields)
	}
	if _, ok := fields["undo"]; ok {
		t.Fatalf("only a commit needs the undo line: %+v", staging.Fields)
	}

	commit, err := gitWriteGatedPreview(st, json.RawMessage(`{"verb":"commit","message":"feat: do it"}`))
	if err != nil {
		t.Fatal(err)
	}
	if commit.DenyLine != "git commit" {
		t.Fatalf("the card carries the line the deny list answers: %q", commit.DenyLine)
	}
	fields = labels(commit)
	if fields["hooks"].Value != "skipped" {
		t.Fatalf("an untrusted checkout runs no hooks: %+v", commit.Fields)
	}
	if fields["undo"].Value != "git revert" || fields["undo"].Detail != components.CommitUndoNote {
		t.Fatalf("the commit card says what the way back is: %+v", commit.Fields)
	}

	st.AllowWrites(structural.Writes{Files: func() []string { return nil }, Hooks: true})
	trusted, err := gitWriteGatedPreview(st, json.RawMessage(`{"verb":"commit","message":"feat: do it"}`))
	if err != nil {
		t.Fatal(err)
	}
	if labels(trusted)["hooks"].Value != "run" {
		t.Fatalf("a trusted checkout runs its hooks: %+v", trusted.Fields)
	}

	if _, err := gitWriteGatedPreview(st, json.RawMessage(`{"verb":"push"}`)); err == nil {
		t.Fatal("a verb outside the set must not produce a card")
	}
}

// The interactive assembly, built and read back.
//
// runChatSession is the only place sub-agents, durable memory, the permission
// classifier, the titler, the changeset, the hooks and the store the window
// trim recovers what it elides from are wired together, and every one of them
// is wired behind a condition. Nothing else builds that set: the headless
// runner assembles its own smaller one, and the chat model's own tests
// construct a model directly and hand it exactly what they mean to test. So a
// condition that quietly stops being true drops a mechanism out of every
// interactive session and nothing in this repository fails.
//
// These build the assembly the way the command does — the real root command,
// the real config, a provider registered for the occasion — and stop where
// the model is finished, which is as far as anything can go without a
// terminal.

// assemblyProvider resolves and is never asked anything: the assembly builds
// a classifier, a summarizer and a titler on a provider and stops before any
// of them makes a request.
type assemblyProvider struct{}

func (assemblyProvider) Name() string { return "assembly-test" }

func (assemblyProvider) StreamCompletion(context.Context, []provider.Message, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, nil
}

// buildSession runs one session command as far as its model, and hands back
// what that model was given.
func buildSession(t *testing.T, args ...string) chat.Wiring {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	// The resolution reads the environment above the config, and what this
	// session runs on is what the flags below say.
	t.Setenv("SHHH_PROVIDER", "")
	t.Setenv("SHHH_MODEL", "")

	// The session is assembled where the test runs, which is this checkout:
	// nothing here writes to it, and the working directory is not something
	// a test may move — a process-wide chdir would leak into every other
	// test in the package and cost this one its cached result.
	//
	// A hook runner is built only where the person wrote a hook, so the
	// config says one. It is a pre-tool hook because no tool is called here:
	// what is under test is that the runner reached the model, not that a
	// command ran.
	cfgDir := filepath.Join(home, "config", "shhh")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"),
		[]byte("[hooks.entries.assembly]\nevent = \"pre_tool\"\ncommand = \"true\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	provider.Register("assembly-test", func(provider.ResolveOpts) (provider.Provider, error) {
		return assemblyProvider{}, nil
	})

	var got chat.Wiring
	var built bool
	assembled = func(m chat.Model) error {
		got, built = m.Wiring(), true
		return nil
	}
	t.Cleanup(func() { assembled = nil })

	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append(args, "--provider", "assembly-test", "--model", "assembly"))
	if err := execute(context.Background(), cmd); err != nil {
		t.Fatalf("shhh %v: %v\n%s", args, err, out.String())
	}
	if !built {
		t.Fatalf("shhh %v returned before it built a session", args)
	}
	return got
}

// Every mechanism here is one whose absence a person discovers by working
// without it: a session that never spawns a child, a memory nothing recalls,
// an auto mode that asks about every command because the classifier is nil, a
// trim that costs what it elides. Each is named so a failure says which went.
func TestCodeSessionWiresItsMechanisms(t *testing.T) {
	w := buildSession(t, "code")
	for _, c := range []struct {
		name string
		got  bool
	}{
		{"sub-agents", w.Subagents},
		{"durable memory", w.Memory},
		{"the permission classifier", w.Classifier},
		{"the titler", w.Titler},
		{"the changeset", w.Changeset},
		{"the hooks", w.Hooks},
		{"the recoverable trim", w.RecoverableTrim},
		{"the working scope", w.Scope},
		{"the process supervisor", w.Processes},
		{"the notebook", w.Notebook},
		{"the backlog", w.Todos},
	} {
		if !c.got {
			t.Errorf("a coding session was assembled without %s", c.name)
		}
	}
}

// A conversation is the same assembly with the acting taken out, and the
// changeset is what says so: there is no edit to review, to undo, or to hand
// a writer child to start from. Asserting it is what stops the test above
// from passing on a build where every session is a coding one.
func TestConversationIsTheAssemblyWithoutTheActing(t *testing.T) {
	w := buildSession(t, "chat")
	if w.Changeset {
		t.Error("a conversation was given the changeset a coding turn is reviewed and undone through")
	}
	if w.Processes {
		t.Error("a conversation was given a process supervisor, and it runs no commands")
	}
	for _, c := range []struct {
		name string
		got  bool
	}{
		{"sub-agents", w.Subagents},
		{"durable memory", w.Memory},
		{"the titler", w.Titler},
		{"the recoverable trim", w.RecoverableTrim},
		{"the backlog", w.Todos},
	} {
		if !c.got {
			t.Errorf("a conversation was assembled without %s", c.name)
		}
	}
}
