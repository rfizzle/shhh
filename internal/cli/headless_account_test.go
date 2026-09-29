package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
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
