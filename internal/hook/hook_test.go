package hook

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/observe"
)

// fired is one hook run as the fake exec saw it.
type fired struct {
	command string
	payload Payload
}

// fakeExec answers every hook the same way and records what each was told.
func fakeExec(seen *[]fired, stdout string, code int) Exec {
	return func(_ context.Context, command string, stdin []byte) (string, int, error) {
		var p Payload
		_ = json.Unmarshal(stdin, &p)
		*seen = append(*seen, fired{command: command, payload: p})
		return stdout, code, nil
	}
}

func runnerOf(t *testing.T, exec Exec, entries map[string]Entry) *Runner {
	t.Helper()
	set := Load(entries, "config.toml", "")
	if len(set.Diagnostics) > 0 {
		t.Fatalf("entries should have loaded: %v", set.Diagnostics)
	}
	r := NewRunner(set, exec, time.Second, "/work")
	if r == nil {
		t.Fatal("a set with hooks in it should build a runner")
	}
	r.SetSession("42")
	return r
}

// The words a hook answers with are the words the record keeps for the same
// verdict. Two spellings of "deny" would split every rate that groups by one.
func TestDecisions_AreTheRecordsOwnWords(t *testing.T) {
	for _, c := range [][2]string{
		{DecisionAllow, observe.DecisionAllow},
		{DecisionDeny, observe.DecisionDeny},
		{DecisionAsk, observe.DecisionAsk},
	} {
		if c[0] != c[1] {
			t.Errorf("hook says %q where the record says %q", c[0], c[1])
		}
	}
}

// Every seam fires once, and what it puts on stdin says which seam it was,
// which session, and what the seam is about.
func TestRunner_EverySeamFiresOnceWithItsPayload(t *testing.T) {
	entries := map[string]Entry{}
	for _, e := range Events() {
		entries[e] = Entry{Event: e, Command: "true"}
	}
	var seen []fired
	r := runnerOf(t, fakeExec(&seen, "", 0), entries)
	ctx, at := context.Background(), Pos{Turn: 3, Round: 7}
	call := Call{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`}

	r.SessionStart(ctx)
	r.PreTool(ctx, at, call, false)
	r.PostTool(ctx, at, call, "one line", observe.OutcomeOK)
	r.TurnClose(ctx, at, "done")
	r.Stop(ctx, at, "done")

	if len(seen) != len(Events()) {
		t.Fatalf("want one run per seam, got %d: %+v", len(seen), seen)
	}
	for i, want := range []string{SessionStart, PreTool, PostTool, TurnClose, Stop} {
		got := seen[i].payload
		if got.Event != want {
			t.Errorf("run %d says event %q, want %q", i, got.Event, want)
		}
		if got.Session != "42" || got.CWD != "/work" {
			t.Errorf("%s carried session %q cwd %q", want, got.Session, got.CWD)
		}
	}
	if got := seen[1].payload; got.Tool != "read_file" || got.Arguments != `{"path":"a.go"}` || got.ID != "call_1" {
		t.Errorf("pre_tool should carry the call: %+v", got)
	}
	if got := seen[2].payload; got.Result != "one line" || got.Outcome != observe.OutcomeOK {
		t.Errorf("post_tool should carry the result: %+v", got)
	}
	if got := seen[3].payload; got.Final != "done" || got.Turn != 3 || got.Round != 7 {
		t.Errorf("turn_close should carry the answer and the position: %+v", got)
	}
}

// A matcher selects tools by name and is anchored, so a name written for one
// tool does not also take the tool whose name contains it.
func TestSet_MatcherSelectsByWholeToolName(t *testing.T) {
	set := Load(map[string]Entry{
		"fmt":  {Event: PostTool, Matcher: "edit_file|write_file", Command: "gofmt -l ."},
		"all":  {Event: PostTool, Command: "true"},
		"read": {Event: PreTool, Matcher: "read", Command: "true"},
	}, "config.toml", "")
	if len(set.Diagnostics) > 0 {
		t.Fatalf("all three should load: %v", set.Diagnostics)
	}
	if got := len(set.For(PostTool, "edit_file")); got != 2 {
		t.Errorf("edit_file should match the matcher and the bare hook, got %d", got)
	}
	if got := len(set.For(PostTool, "read_file")); got != 1 {
		t.Errorf("read_file should match only the bare hook, got %d", got)
	}
	if got := len(set.For(PreTool, "read_file")); got != 0 {
		t.Errorf("a matcher of %q must not take read_file, got %d", "read", got)
	}
	if got := set.Events(); len(got) != 2 || got[0] != PreTool || got[1] != PostTool {
		t.Errorf("Events should name the seams that have a hook, in listing order: %v", got)
	}
}

// An entry that cannot load is named and left out. Ignoring a matcher that
// will not compile would leave a hook matching everything where the person
// wrote one matching a few.
func TestLoad_RefusesAnEntryItCannotRead(t *testing.T) {
	set := Load(map[string]Entry{
		"noevent": {Event: "after_lunch", Command: "true"},
		"nocmd":   {Event: PreTool},
		"badre":   {Event: PreTool, Matcher: "(", Command: "true"},
		"nomatch": {Event: Stop, Matcher: "read_file", Command: "true"},
		"good":    {Event: PreTool, Command: "true"},
	}, "config.toml", "")
	if set.Len() != 1 {
		t.Fatalf("only the sound entry should load, got %d", set.Len())
	}
	if len(set.Diagnostics) != 4 {
		t.Fatalf("every refused entry should be named: %v", set.Diagnostics)
	}
	for _, name := range []string{"noevent", "nocmd", "badre", "nomatch"} {
		found := false
		for _, d := range set.Diagnostics {
			if strings.Contains(d, name) {
				found = true
			}
		}
		if !found {
			t.Errorf("no diagnostic names %s: %v", name, set.Diagnostics)
		}
	}
}

// A checkout's file is read where the caller says it may be, and shadows a
// user entry of the same name.
func TestLoad_ProjectFileShadowsTheUsersOwn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	body := `{"hooks":{"fmt":{"event":"post_tool","command":"theirs"}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	user := map[string]Entry{"fmt": {Event: PostTool, Command: "mine"}}

	withheld := Load(user, "config.toml", "")
	if got := withheld.All(); len(got) != 1 || got[0].Command != "mine" {
		t.Fatalf("with no project file the user's own hook stands: %+v", got)
	}
	set := Load(user, "config.toml", path)
	if got := set.All(); len(got) != 1 || got[0].Command != "theirs" || got[0].Source != path {
		t.Fatalf("the checkout's entry should shadow the user's: %+v", got)
	}
}

// Exit 2 refuses. Any other non-zero exit is a failure, and a failure asks
// where there is somebody to ask and does nothing where there is not.
func TestRunner_ExitTwoDeniesAndOtherFailuresNeverApprove(t *testing.T) {
	ctx, at := context.Background(), Pos{}
	call := Call{Name: "read_file"}

	var denied []fired
	r := runnerOf(t, fakeExec(&denied, "vendor is off limits\n", 2),
		map[string]Entry{"guard": {Event: PreTool, Command: "guard"}})
	v := r.PreTool(ctx, at, call, false)
	if !v.Denied() || v.Reason != "guard" {
		t.Fatalf("exit 2 should refuse and name the hook: %+v", v)
	}
	if len(v.Notes) == 0 || !strings.Contains(v.Notes[0], "vendor is off limits") {
		t.Fatalf("a refusal should carry what the hook said: %+v", v.Notes)
	}

	var broke []fired
	r = runnerOf(t, fakeExec(&broke, "boom\n", 1),
		map[string]Entry{"guard": {Event: PreTool, Command: "guard"}})
	if v := r.PreTool(ctx, at, call, true); !v.Asked() || !v.Failed {
		t.Fatalf("a gated call should be asked about when a hook fails: %+v", v)
	}
	v = r.PreTool(ctx, at, call, false)
	if v.Decision != "" || !v.Failed {
		t.Fatalf("a read has nobody to ask, so it runs with a note: %+v", v)
	}
	if len(v.Notes) == 0 || !strings.Contains(v.Notes[0], "exited 1") {
		t.Fatalf("the note should say the hook failed: %+v", v.Notes)
	}
}

// A hook's allow is the absence of an objection. Nothing a config file names
// turns a card into a call that never asked.
func TestRunner_AllowDecidesNothing(t *testing.T) {
	var seen []fired
	r := runnerOf(t, fakeExec(&seen, `{"decision":"allow"}`, 0),
		map[string]Entry{"nod": {Event: PreTool, Command: "nod"}})
	if v := r.PreTool(context.Background(), Pos{}, Call{Name: "write_file"}, true); v.Decision != "" {
		t.Fatalf("allow should leave the decision where it found it, got %q", v.Decision)
	}
}

// A word this build has no meaning for is read as nothing said, and stdout
// that is not an answer at all is the hook printing for its own sake.
func TestRunner_ReadsOnlyAnAnswerItUnderstands(t *testing.T) {
	for _, out := range []string{`{"decision":"maybe"}`, "reformatted 3 files\n", ""} {
		var seen []fired
		r := runnerOf(t, fakeExec(&seen, out, 0),
			map[string]Entry{"fmt": {Event: PostTool, Command: "fmt"}})
		v := r.PostTool(context.Background(), Pos{}, Call{Name: "edit_file"}, "ok", observe.OutcomeOK)
		if v.Decision != "" || v.Failed || len(v.Notes) != 0 {
			t.Errorf("stdout %q should have said nothing: %+v", out, v)
		}
	}
}

// updated_input carries arguments and nothing else, and each hook is told the
// arguments the one before it left.
func TestRunner_UpdatedInputRewritesTheArgumentsAndComposes(t *testing.T) {
	var seen []fired
	exec := func(_ context.Context, command string, stdin []byte) (string, int, error) {
		var p Payload
		_ = json.Unmarshal(stdin, &p)
		seen = append(seen, fired{command: command, payload: p})
		return `{"updated_input":{"path":"` + command + `"}}`, 0, nil
	}
	r := runnerOf(t, exec, map[string]Entry{
		"a": {Event: PreTool, Command: "first"},
		"b": {Event: PreTool, Command: "second"},
	})
	v := r.PreTool(context.Background(), Pos{}, Call{Name: "read_file", Arguments: `{"path":"a.go"}`}, false)
	if got := string(v.Input); got != `{"path":"second"}` {
		t.Fatalf("the last rewrite should stand, got %s", got)
	}
	if len(seen) != 2 || seen[1].payload.Arguments != `{"path":"first"}` {
		t.Fatalf("the second hook should be told what the first left: %+v", seen)
	}
}

// A hook's answer has no field that could name a tool, which is the whole of
// the tier rule: there is nothing to move a call with rather than a check
// that could be got around.
func TestResponse_CarriesNoToolName(t *testing.T) {
	body, err := json.Marshal(Response{Decision: DecisionAllow, Context: "c", Note: "n",
		UpdatedInput: json.RawMessage(`{"path":"a"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	for name := range fields {
		switch name {
		case "decision", "updated_input", "context", "note":
		default:
			t.Errorf("a hook's answer gained a %q field; only arguments may change", name)
		}
	}
	// And what a hook writes there is never read as a tool: a response that
	// names one leaves no trace on the verdict.
	if got := parse(`{"decision":"allow","tool":"write_file"}`); got.Decision != DecisionAllow {
		t.Fatalf("a tool name in the answer should be ignored, not honoured: %+v", got)
	}
}

// A hook that runs past its ceiling has failed. The seam has to answer, and
// waiting longer is the one thing it cannot do.
func TestRunner_ATimeoutIsAFailure(t *testing.T) {
	exec := func(ctx context.Context, _ string, _ []byte) (string, int, error) {
		<-ctx.Done()
		return "", -1, ctx.Err()
	}
	set := Load(map[string]Entry{"slow": {Event: PreTool, Command: "sleep 60"}}, "config.toml", "")
	r := NewRunner(set, exec, 10*time.Millisecond, "/work")
	v := r.PreTool(context.Background(), Pos{}, Call{Name: "read_file"}, true)
	if !v.Failed || !v.Asked() {
		t.Fatalf("a hook past its ceiling should fail and ask: %+v", v)
	}
	if len(v.Notes) == 0 || !strings.Contains(v.Notes[0], "did not finish") {
		t.Fatalf("the note should say it ran too long: %+v", v.Notes)
	}
}

// A hook's own timeout may be shorter than the session's ceiling and never
// longer: the reason a command is bounded at all is that nobody is watching.
func TestRunner_AHooksOwnTimeoutOnlyShortens(t *testing.T) {
	var deadlines []time.Duration
	exec := func(ctx context.Context, _ string, _ []byte) (string, int, error) {
		dl, _ := ctx.Deadline()
		deadlines = append(deadlines, time.Until(dl).Round(time.Second))
		return "", 0, nil
	}
	set := Load(map[string]Entry{
		"quick": {Event: PreTool, Command: "a", Timeout: 2},
		"slow":  {Event: PreTool, Command: "b", Timeout: 600},
	}, "config.toml", "")
	r := NewRunner(set, exec, 10*time.Second, "/work")
	r.PreTool(context.Background(), Pos{}, Call{Name: "read_file"}, false)
	if len(deadlines) != 2 || deadlines[0] != 2*time.Second || deadlines[1] != 10*time.Second {
		t.Fatalf("the ceiling caps a hook's own timeout: %v", deadlines)
	}
}

// A refusal stops the hooks behind it: the call is not going to happen, and
// running the rest would be side effects nobody asked for.
func TestRunner_ARefusalStopsTheHooksBehindIt(t *testing.T) {
	var seen []fired
	r := runnerOf(t, fakeExec(&seen, "", 2), map[string]Entry{
		"a": {Event: PreTool, Command: "first"},
		"b": {Event: PreTool, Command: "second"},
	})
	if v := r.PreTool(context.Background(), Pos{}, Call{Name: "read_file"}, false); !v.Denied() {
		t.Fatalf("want a refusal, got %+v", v)
	}
	if len(seen) != 1 {
		t.Fatalf("only the refusing hook should have run: %+v", seen)
	}
}

// A post-tool hook has nothing left to decide, so its decision is dropped and
// its context and note are what it is for.
func TestRunner_PostToolDecidesNothing(t *testing.T) {
	var seen []fired
	r := runnerOf(t, fakeExec(&seen, `{"decision":"deny","context":"still failing","updated_input":{"x":1}}`, 0),
		map[string]Entry{"check": {Event: PostTool, Command: "check"}})
	v := r.PostTool(context.Background(), Pos{}, Call{Name: "edit_file"}, "ok", observe.OutcomeOK)
	if v.Decision != "" || v.Input != nil {
		t.Fatalf("a call that has run cannot be refused or rewritten: %+v", v)
	}
	if v.Context != "still failing" {
		t.Fatalf("its context is what it is for: %+v", v)
	}
}

// A session with no hooks, and a runner nothing built, answer every seam with
// the same empty verdict — so no surface has to check before it asks.
func TestRunner_NilIsSafeAtEverySeam(t *testing.T) {
	var r *Runner
	ctx := context.Background()
	for _, v := range []Verdict{
		r.SessionStart(ctx),
		r.PreTool(ctx, Pos{}, Call{Name: "read_file"}, true),
		r.PostTool(ctx, Pos{}, Call{Name: "read_file"}, "", ""),
		r.TurnClose(ctx, Pos{}, ""),
		r.Stop(ctx, Pos{}, ""),
	} {
		if v.Decision != "" || v.Failed || len(v.Notes) != 0 {
			t.Fatalf("a runner with nothing to fire decided something: %+v", v)
		}
	}
	if r.Has(PreTool, "read_file") || r.Set().Len() != 0 {
		t.Fatal("a nil runner holds no hooks")
	}
	if NewRunner(nil, fakeExec(nil, "", 0), time.Second, "") != nil {
		t.Fatal("an empty set should build no runner")
	}
}

// The refusal the model reads names the hook and no file: the hooks are the
// person's, and a refusal carrying the instructions for editing one would be
// handing over the way around it.
func TestDeniedResult_NamesTheHookAndNothingElse(t *testing.T) {
	got := DeniedResult("vendor-guard")
	if !strings.Contains(got, "vendor-guard") {
		t.Errorf("the refusal should name the hook: %q", got)
	}
	for _, leak := range []string{"hooks.json", "config.toml", "hooks.entries"} {
		if strings.Contains(got, leak) {
			t.Errorf("the refusal names %q, which is the way around it: %q", leak, got)
		}
	}
	if !strings.Contains(DeniedResult(""), "a hook") {
		t.Errorf("an unnamed hook still refuses in words: %q", DeniedResult(""))
	}
}

// A note is written for the person and a failure has to reach the model. One
// hook failing beside another that wrote a note must not send that note.
func TestVerdict_LeadCarriesFailuresAndNotNotes(t *testing.T) {
	var seen []fired
	exec := func(_ context.Context, command string, stdin []byte) (string, int, error) {
		var p Payload
		_ = json.Unmarshal(stdin, &p)
		seen = append(seen, fired{command: command, payload: p})
		if command == "chatty" {
			return `{"note":"for your eyes only"}`, 0, nil
		}
		return "boom\n", 1, nil
	}
	r := runnerOf(t, exec, map[string]Entry{
		"a-chatty": {Event: PostTool, Command: "chatty"},
		"b-broken": {Event: PostTool, Command: "broken"},
	})
	v := r.PostTool(context.Background(), Pos{}, Call{Name: "edit_file"}, "wrote it", OutcomeOK)
	if len(v.Notes) != 2 {
		t.Fatalf("the person hears from both hooks: %+v", v.Notes)
	}
	lead := v.Lead("wrote it")
	if !strings.Contains(lead, "exited 1") {
		t.Errorf("the model should be told the hook broke: %q", lead)
	}
	if strings.Contains(lead, "for your eyes only") {
		t.Errorf("a note is not sent to the model: %q", lead)
	}
	if !strings.HasSuffix(lead, "wrote it") {
		t.Errorf("the result should still be there, last: %q", lead)
	}
}

// The wrap is one function both surfaces use, and it holds the tier rule: a
// gated call skips the seam in front of it, which its own approval path owns,
// and keeps the one behind it.
func TestRunner_WrapExecutorSkipsOnlyTheSeamInFront(t *testing.T) {
	var seen []fired
	var dispatched []string
	next := func(name string, args json.RawMessage) (string, error) {
		dispatched = append(dispatched, name+" "+string(args))
		return "one line", nil
	}
	r := runnerOf(t, fakeExec(&seen, `{"updated_input":{"path":"b.go"}}`, 0), map[string]Entry{
		"before": {Event: PreTool, Command: "before"},
		"after":  {Event: PostTool, Command: "after"},
	})
	gated := func(name string, _ json.RawMessage) bool { return name == "write_file" }
	exec := r.WrapExecutor(func() Pos { return Pos{Round: 2} }, gated, next)

	if _, err := exec("read_file", json.RawMessage(`{"path":"a.go"}`)); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0].payload.Event != PreTool || seen[1].payload.Event != PostTool {
		t.Fatalf("a read meets both seams: %+v", seen)
	}
	if len(dispatched) != 1 || !strings.HasPrefix(dispatched[0], "read_file ") || !strings.Contains(dispatched[0], "b.go") {
		t.Fatalf("a rewrite changes the arguments and never the tool: %v", dispatched)
	}

	seen = nil
	if _, err := exec("write_file", json.RawMessage(`{"path":"a.go"}`)); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0].payload.Event != PostTool {
		t.Fatalf("a gated call meets only the seam behind it: %+v", seen)
	}
}

// The seam behind a call is told what the call produced, not what the seam in
// front of it prepended: an error with a note above it is still an error.
func TestRunner_WrapExecutorReadsTheOutcomeOffTheCall(t *testing.T) {
	var seen []fired
	next := func(string, json.RawMessage) (string, error) { return "error: no such file", nil }
	r := runnerOf(t, fakeExec(&seen, `{"context":"look in internal/"}`, 0), map[string]Entry{
		"a-hint":  {Event: PreTool, Command: "hint"},
		"b-after": {Event: PostTool, Command: "after"},
	})
	out, err := r.WrapExecutor(func() Pos { return Pos{} }, nil, next)("read_file", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	post := seen[1].payload
	if post.Outcome != OutcomeError {
		t.Errorf("the outcome is the call's, not the annotated text's: %q", post.Outcome)
	}
	if post.Result != "error: no such file" {
		t.Errorf("the result a post-tool hook reads is the call's: %q", post.Result)
	}
	if !strings.Contains(out, "look in internal/") || !strings.Contains(out, "error: no such file") {
		t.Errorf("both contexts and the result should reach the model: %q", out)
	}
}

// The outcome words are the record's, held here because this package declares
// its own rather than importing the package that owns them.
func TestOutcome_IsTheRecordsOwnWords(t *testing.T) {
	if OutcomeOK != observe.OutcomeOK || OutcomeError != observe.OutcomeError {
		t.Fatalf("hook says %q/%q where the record says %q/%q",
			OutcomeOK, OutcomeError, observe.OutcomeOK, observe.OutcomeError)
	}
	for result, want := range map[string]string{
		"one line":            OutcomeOK,
		"error: no such file": OutcomeError,
		"":                    OutcomeOK,
	} {
		if got := Outcome(result); got != want {
			t.Errorf("Outcome(%q) = %q, want %q", result, got, want)
		}
	}
}

// An ask an unattended run cannot put to anybody is not a refusal, and what
// the model reads has to say which it was: one is permanent and the other is
// a card nobody was there to draw.
func TestAskedResult_IsNotARefusal(t *testing.T) {
	asked, denied := AskedResult("guard"), DeniedResult("guard")
	if asked == denied {
		t.Fatal("an ask and a refusal should not read the same")
	}
	if strings.Contains(asked, "no approval can allow it") {
		t.Errorf("an ask is not permanent: %q", asked)
	}
	if !strings.Contains(asked, "guard") {
		t.Errorf("it should still name the hook: %q", asked)
	}
}
