package cli

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/storage"
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
