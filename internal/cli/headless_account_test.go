package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
)

// accountAnswer answers every request with one account, and keeps what it
// was asked.
type accountAnswer struct {
	answer string
	asked  *[]string
}

func (accountAnswer) Name() string { return "account-test" }

func (p accountAnswer) StreamCompletion(_ context.Context, msgs []provider.Message, _ provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	*p.asked = append(*p.asked, msgs[len(msgs)-1].Content)
	events := make(chan provider.StreamEvent, 1)
	events <- provider.StreamEvent{Token: p.answer, Done: true}
	close(events)
	return events, nil
}

// A headless run revises the slot's standing account from what it did, and
// its save carries the revision, so a run resumed with --resume says what it
// did; a run whose reading fails keeps the account the slot had
// (docs/capabilities/sessions-and-memory.md#a-title-you-did-not-write).
func TestHeadlessSave_CarriesTheRevisedAccount(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "fix the retry flake"},
		{Role: provider.RoleAssistant, Content: "the timer is fixed"},
	}
	var asked []string
	writer := agent.NewAccountant(accountAnswer{answer: "Fixed the retry flake. Left off with the tests green.", asked: &asked},
		agent.AccountConfig{Model: "small"})

	c := &headlessChat{db: db, slot: "run", kind: "code", summary: "Reading the retry code."}
	if _, err := db.ClaimChatSlot("run"); err != nil {
		t.Fatal(err)
	}
	c.reviseAccount(writer, msgs)
	c.save(msgs)
	got, err := db.ChatResume(c.slot)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "Fixed the retry flake. Left off with the tests green." {
		t.Fatalf("the save wrote %q, want the revised account", got.Summary)
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "Reading the retry code.") || !strings.Contains(asked[0], "fix the retry flake") {
		t.Fatalf("the reading did not revise the account it had: %q", asked)
	}

	// A disabled writer asks nothing and leaves the account as it was.
	off := agent.NewAccountant(accountAnswer{answer: "x", asked: &asked}, agent.AccountConfig{Model: "small", Disabled: true})
	c.reviseAccount(off, msgs)
	if len(asked) != 1 || c.summary != "Fixed the retry flake. Left off with the tests green." {
		t.Fatalf("a disabled writer changed the account: %q, asked %d", c.summary, len(asked))
	}
}

// A print run a backlog runner started for a stage of an item writes no
// standing account: the runner deletes the conversation once the item goes
// through, and the stage cannot know whether it will instead block
// (docs/capabilities/todo.md#a-sprint-is-runs-with-a-session-between-them).
func TestHeadlessAccountant_AStageOfABacklogRunTakesNone(t *testing.T) {
	t.Setenv(todoItemEnv, "fix-the-flake")
	if a := headlessAccountant(config.Config{}, nil, nil); a != nil {
		t.Fatalf("a stage process built an accountant: %+v", a)
	}
}

// A served session revises its slot's standing account at a turn's close on
// the interval a session on a screen uses, and once more where it ends when a
// turn has closed since the last reading; the save after each carries it
// (docs/capabilities/headless.md#something-else-can-drive-it).
func TestServedAccount_RevisesOnTheIntervalAndOnceMoreAtTheEnd(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "fix the retry flake"},
		{Role: provider.RoleAssistant, Content: "the timer is fixed"},
	}
	var asked []string
	writer := agent.NewAccountant(accountAnswer{answer: "Fixed the retry flake.", asked: &asked},
		agent.AccountConfig{Model: "small"})
	c := &headlessChat{db: db, slot: "served", kind: "code"}
	if _, err := db.ClaimChatSlot("served"); err != nil {
		t.Fatal(err)
	}
	s := servedAccount{writer: writer, every: 2}

	s.turnClosed(c, msgs)
	if len(asked) != 0 {
		t.Fatalf("the first turn of two asked for a reading: %d", len(asked))
	}
	s.turnClosed(c, msgs)
	if len(asked) != 1 || c.summary != "Fixed the retry flake." {
		t.Fatalf("the second turn did not revise the account: asked %d, account %q", len(asked), c.summary)
	}
	if s.leaving(c, msgs) || len(asked) != 1 {
		t.Fatalf("the end asked again with no turn closed since the last reading: %d", len(asked))
	}
	s.turnClosed(c, msgs)
	if !s.leaving(c, msgs) || len(asked) != 2 {
		t.Fatalf("the end did not account for the turn closed since the last reading: %d", len(asked))
	}
	c.save(msgs)
	got, err := db.ChatResume(c.slot)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "Fixed the retry flake." {
		t.Fatalf("the save wrote %q, want the revised account", got.Summary)
	}

	// Off is off: a zero interval counts nothing and asks nothing.
	off := servedAccount{writer: writer}
	off.turnClosed(c, msgs)
	if off.leaving(c, msgs) || len(asked) != 2 {
		t.Fatalf("an account turned off asked for a reading: %d", len(asked))
	}
}
