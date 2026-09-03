package cli

// The root generates nothing: what used to be `shhh <prompt>` is `shhh cmd`,
// and both ways of arriving at the root with a prompt in hand have to say so.
// A bare `shhh` on a terminal still prints the tree, which help_test.go
// covers.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/proposal"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/raw"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui"
)

func TestBarePromptNamesTheCommandThatGenerates(t *testing.T) {
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"list open ports"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("`shhh \"list open ports\"` should be refused, not generated")
	}
	if !strings.Contains(err.Error(), `shhh cmd "list open ports"`) {
		t.Errorf("the refusal should spell the command that would have run it, got %q", err)
	}
}

// A prompt on stdin used to be the whole no-TTY contract, and a script that
// still pipes into the root gets a failure rather than the help page: help on
// stdout is what `echo … | shhh | sh` would then run.
func TestPipedPromptNamesTheCommandThatReadsIt(t *testing.T) {
	// fang resolves its width once per process, so a render here asks for
	// the same one every other help render in this package does.
	t.Setenv("__FANG_TEST_WIDTH", "80")
	t.Setenv("NO_COLOR", "1")
	// `go test` hands the process a stdin that is not a terminal, which is
	// the condition the root branches on.
	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(nil)
	if err := execute(context.Background(), cmd); err == nil {
		t.Fatal("a piped `shhh` should be refused, not answered with the help page")
	}
	if !strings.Contains(stderr.String(), "shhh cmd") {
		t.Errorf("the refusal should name `shhh cmd`, got:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("nothing may reach the pipe on stdout, got:\n%s", stdout.String())
	}
}

// The one-shot sends two different prompts, and which one a run gets is the
// whole of the pipe contract. The interactive prompt asks for the sentence
// under the command and the alternatives beside it; the piped one asks for
// neither, because its stdout is read by another program and a section it
// cannot see would be a command that program would run.
func TestPipedPromptAsksForNothingItCannotPrint(t *testing.T) {
	info := shell.Info{Shell: "zsh", OS: "darwin", Cwd: "/tmp"}
	piped := raw.SystemPrompt(info, "")
	for _, sentinel := range []string{proposal.Sentinel, proposal.ExplainSentinel} {
		if strings.Contains(piped, sentinel) {
			t.Errorf("the piped one-shot invited a section its stdout cannot carry:\n%s", piped)
		}
	}
	interactive := prompt.BuildAlternatives(info, "")
	for _, sentinel := range []string{proposal.Sentinel, proposal.ExplainSentinel} {
		if !strings.Contains(interactive, sentinel) {
			t.Errorf("the interactive one-shot stopped asking for what its surface shows:\n%s", interactive)
		}
	}
}

// The one-shot's turn ends in that same set. Walking away from a command is
// cancelled and not done: the request was answered and the answer refused,
// and an outcome mix that could not tell those apart is the one figure this
// record exists to make readable.
func TestOneShotOutcome(t *testing.T) {
	for _, c := range []struct {
		name   string
		result ui.GenerateResult
		want   string
	}{
		{"ran it", ui.GenerateResult{Action: ui.ActionRun}, observe.TurnDone},
		{"copied it", ui.GenerateResult{Action: ui.ActionCopy}, observe.TurnDone},
		{"escaped the card", ui.GenerateResult{Action: ui.ActionCancel}, observe.TurnCancelled},
		{"cancelled the stream", ui.GenerateResult{Cancelled: true}, observe.TurnCancelled},
		// A failure outranks the action it failed on: a request that never
		// produced a command is not a command the user declined.
		{"failed", ui.GenerateResult{Action: ui.ActionCancel, Err: errors.New("no key")}, observe.TurnFailed},
	} {
		if got := oneShotOutcome(c.result); got != c.want {
			t.Errorf("%s: oneShotOutcome = %q, want %q", c.name, got, c.want)
		}
	}
}

// The one-shot joins the record as what it is: one request, so one turn, with
// no rounds and no tool mix. Both of those are true rather than missing.
func TestOneShotSessionIsOneTurn(t *testing.T) {
	db := fixtureStore(t)
	rec := startObserveRecorder(db, "cmd", "anthropic", "test-model", nil)
	rec.stamp("the one-shot prompt", 0, "/repo", storage.AgentSettings{})
	rec.usagePriced(1, 900, 120, 0.004, true)
	rec.turn(1, 0, 2*time.Second, observe.TurnDone)
	rec.end()

	assertShapes(t, shapesOf(t, db, rec.sessionID()), []eventShape{
		{kind: storage.AgentEventTurn, outcome: observe.TurnDone, turn: 1, timed: true},
	})

	s, ok, err := db.AgentSession(rec.sessionID())
	if err != nil || !ok {
		t.Fatalf("session: %v (found=%v)", err, ok)
	}
	if s.Kind != "cmd" {
		t.Fatalf("kind = %q, want cmd", s.Kind)
	}
	if s.Turns != 1 || s.TokensIn != 900 || s.TokensOut != 120 || s.Cost != 0.004 {
		t.Fatalf("unexpected totals: %+v", s)
	}
	if s.PromptHash != fingerprint("the one-shot prompt") || s.Project != fingerprint("/repo") {
		t.Fatalf("one-shot was not stamped: %+v", s)
	}
	if s.EndedAt == nil {
		t.Fatal("the one-shot's row was never ended")
	}
}

// oneShotFixture points a one-shot at a machine of its own: a store, a model
// data cache seeded from the snapshot so nothing reaches the network, and a
// provider that answers with answer. It hands back the store's directory and
// the times at which each request was made.
func oneShotFixture(tb testing.TB, answer string) (string, *[]time.Time) {
	tb.Helper()
	data := tb.TempDir()
	cache := tb.TempDir()
	tb.Setenv("XDG_DATA_HOME", data)
	tb.Setenv("XDG_CACHE_HOME", cache)
	// A refresh is a download, and a cache file younger than a day is what
	// stops one. The snapshot is the same document.
	snapshot, err := os.ReadFile(filepath.Join("..", "pricing", "models.json"))
	if err != nil {
		tb.Fatalf("read the price snapshot: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cache, "shhh"), 0o700); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "shhh", "model_prices.json"), snapshot, 0o600); err != nil {
		tb.Fatal(err)
	}
	// The one-shot reads stdin whenever it is not a terminal, and a stdin
	// held open by whatever started the test — a pipe with no writer at the
	// other end — is a read that never returns. This one is empty and
	// already at end.
	empty, err := os.Open(os.DevNull)
	if err != nil {
		tb.Fatal(err)
	}
	stdin := os.Stdin
	os.Stdin = empty
	tb.Cleanup(func() { os.Stdin = stdin; empty.Close() })

	// The resolution reads the environment above the config, and this test
	// says what the config says.
	tb.Setenv("SHHH_PROVIDER", "")
	tb.Setenv("SHHH_MODEL", "")
	tb.Setenv("SHHH_REASONING", "")

	asked := &[]time.Time{}
	provider.Register(oneShotProviderName, func(provider.ResolveOpts) (provider.Provider, error) {
		return oneShotProvider{answer: answer, asked: asked}, nil
	})
	return data, asked
}

const oneShotProviderName = "one-shot-test"

// oneShotProvider answers a one-shot without a network, and records when it
// was asked so a benchmark can time the walk from the process starting to the
// request going out.
type oneShotProvider struct {
	answer string
	asked  *[]time.Time
}

func (p oneShotProvider) Name() string { return oneShotProviderName }

func (p oneShotProvider) StreamCompletion(context.Context, []provider.Message, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	*p.asked = append(*p.asked, time.Now())
	if p.answer == "" {
		return nil, errors.New("no answer")
	}
	events := make(chan provider.StreamEvent, 2)
	events <- provider.StreamEvent{Token: p.answer}
	events <- provider.StreamEvent{Done: true, Usage: &provider.Usage{PromptTokens: 40, CompletionTokens: 6}}
	close(events)
	return events, nil
}

// oneShotConfig is the config a fixture's run is given.
func oneShotConfig() config.Config {
	var cfg config.Config
	cfg.Provider.Default = oneShotProviderName
	cfg.Provider.Model = "test-model"
	return cfg
}

// runOneShot runs the command with args and returns what reached the pipe.
func runOneShot(tb testing.TB, args ...string) (string, error) {
	tb.Helper()
	pipe, err := os.CreateTemp(tb.TempDir(), "stdout")
	if err != nil {
		tb.Fatal(err)
	}
	defer pipe.Close()
	stdout := os.Stdout
	os.Stdout = pipe
	defer func() { os.Stdout = stdout }()

	cmd := newCmdCmd()
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	runErr := cmd.ExecuteContext(withConfig(context.Background(), oneShotConfig()))

	written, err := os.ReadFile(pipe.Name())
	if err != nil {
		tb.Fatal(err)
	}
	return string(written), runErr
}

// oneShotSessions is every session row the fixture's store holds. A store
// that was never opened holds none, which is a real answer and not an error:
// opening one here to ask would create the file the question is about.
func oneShotSessions(t *testing.T, data string) []storage.AgentSessionSummary {
	t.Helper()
	path := filepath.Join(data, "shhh", "shhh.db")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	db, err := storage.OpenPath(path)
	if err != nil {
		t.Fatalf("open the store the run wrote to: %v", err)
	}
	defer db.Close()
	sessions, err := db.AgentSessions(time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("read the sessions back: %v", err)
	}
	return sessions
}

// The store is opened beside the request now rather than in front of it, and
// the piped run is the composition that proves the row survived the move: it
// has nobody in front of it, it takes an exit path of its own, and it is the
// one that once spent money and left nothing behind.
func TestOneShotPipedRunIsStillRecorded(t *testing.T) {
	data, _ := oneShotFixture(t, "ls -la")

	out, err := runOneShot(t, "--raw", "list files")
	if err != nil {
		t.Fatalf("the piped one-shot failed: %v", err)
	}
	if strings.TrimSpace(out) != "ls -la" {
		t.Fatalf("the pipe carried %q", out)
	}

	sessions := oneShotSessions(t, data)
	if len(sessions) != 1 {
		t.Fatalf("a piped run wrote %d session rows, want 1", len(sessions))
	}
	s := sessions[0]
	if s.Kind != "cmd" {
		t.Errorf("kind = %q, want cmd", s.Kind)
	}
	if s.PromptHash == "" || s.Project == "" {
		t.Errorf("the piped run was never stamped: %+v", s)
	}
	if s.Turns != 1 {
		t.Errorf("turns = %d, want 1", s.Turns)
	}
	if s.EndedAt == nil {
		t.Error("the piped run's row was never closed")
	}
	if want := observe.SessionOutcome(observe.TurnDone); s.Outcome != want {
		t.Errorf("outcome = %q, want %q", s.Outcome, want)
	}
}

// The interactive one-shot opens its row from the same place, and a request
// that never opens is the one path through it that reaches no terminal. It is
// still a run that happened, and it is recorded as the failure it was.
func TestOneShotRecordsARequestThatNeverOpened(t *testing.T) {
	data, _ := oneShotFixture(t, "")

	if _, err := runOneShot(t, "list files"); err == nil {
		t.Fatal("a provider that refuses the request should fail the run")
	}

	sessions := oneShotSessions(t, data)
	if len(sessions) != 1 {
		t.Fatalf("the run wrote %d session rows, want 1", len(sessions))
	}
	s := sessions[0]
	if s.PromptHash == "" {
		t.Errorf("the run was never stamped: %+v", s)
	}
	if s.EndedAt == nil {
		t.Error("the row was never closed")
	}
	if want := observe.SessionOutcome(observe.TurnFailed); s.Outcome != want {
		t.Errorf("outcome = %q, want %q", s.Outcome, want)
	}
}

// A level the flag cannot spell is a refused flag, not a run: nothing is
// asked of a provider and nothing is spent, so nothing is written down. The
// record is for runs that happened.
func TestOneShotWritesNothingForARefusedFlag(t *testing.T) {
	data, asked := oneShotFixture(t, "ls -la")

	if _, err := runOneShot(t, "--reasoning", "sideways", "list files"); err == nil {
		t.Fatal("a level nothing spells should be refused")
	}
	if len(*asked) != 0 {
		t.Fatalf("a refused flag reached the provider %d times", len(*asked))
	}
	if sessions := oneShotSessions(t, data); len(sessions) != 0 {
		t.Fatalf("a refused flag wrote %d session rows: %+v", len(sessions), sessions)
	}
}

// Nothing local stands between the process starting and the request going
// out: the store is opened beside it. A held opener is what says so without
// racing — the caller is past the point the store used to block it at while
// the store has not answered yet.
func TestPendingRecordDoesNotStandInFrontOfTheRequest(t *testing.T) {
	release := make(chan struct{})
	db := fixtureStore(t)
	pending := startRecord(func() (*storage.DB, *observeRecorder) {
		<-release
		return db, startObserveRecorder(db, "cmd", "test", "test-model", nil)
	})

	select {
	case <-pending.done:
		t.Fatal("the caller waited for the store before it could send anything")
	default:
	}

	close(release)
	gotDB, rec := pending.wait()
	if gotDB != db || rec == nil {
		t.Fatalf("wait handed back %v and %v", gotDB, rec)
	}
	again, recAgain := pending.wait()
	if again != gotDB || recAgain != rec {
		t.Error("a second wait answered differently from the first")
	}
}

// A store that will not open disables the record for the run rather than
// ending it, and every path that writes has to survive the nil that says so.
func TestPendingRecordSurvivesAStoreThatWillNotOpen(t *testing.T) {
	pending := startRecord(func() (*storage.DB, *observeRecorder) {
		return nil, startObserveRecorder(nil, "cmd", "test", "test-model", nil)
	})
	db, rec := pending.wait()
	if db != nil || rec != nil {
		t.Fatalf("a store that would not open produced %v and %v", db, rec)
	}
	// The nil recorder is the one every writing path goes through.
	rec.stamp("prompt", 0, "/repo", storage.AgentSettings{})
	rec.turn(1, 0, time.Second, observe.TurnDone)
	rec.end()
}

// BenchmarkOneShotTimeToRequest is the walk this whole thing is about: the
// time from the command starting to the provider being asked for the first
// token, with the answer faked so that nothing but local work is in it.
func BenchmarkOneShotTimeToRequest(b *testing.B) {
	_, asked := oneShotFixture(b, "ls -la")

	sink, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		b.Fatal(err)
	}
	defer sink.Close()
	stdout := os.Stdout
	os.Stdout = sink
	defer func() { os.Stdout = stdout }()

	var total time.Duration
	runs := 0
	for b.Loop() {
		*asked = (*asked)[:0]
		cmd := newCmdCmd()
		cmd.SetArgs([]string{"--raw", "list files"})
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		start := time.Now()
		if err := cmd.ExecuteContext(withConfig(context.Background(), oneShotConfig())); err != nil {
			b.Fatal(err)
		}
		if len(*asked) != 1 {
			b.Fatalf("the provider was asked %d times", len(*asked))
		}
		total += (*asked)[0].Sub(start)
		runs++
	}
	b.ReportMetric(float64(total.Nanoseconds())/float64(runs)/1e6, "ms/to-request")
}
