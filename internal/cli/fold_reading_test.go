package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
)

// The reading a continuation splices in front of the conversation is built
// from the checkout each time, so a compaction that folds it away does not
// keep it: the slot's fold holds what was said and not what was read.
func TestHeadlessContinue_AFoldNeverHoldsTheReading(t *testing.T) {
	db := printStore(t)
	if err := db.SaveChat("left", []provider.Message{
		{Role: provider.RoleSystem, Content: "old prompt"},
		{Role: provider.RoleUser, Content: "an ask"},
		{Role: provider.RoleAssistant, Content: "an answer"},
	}); err != nil {
		t.Fatal(err)
	}
	// A fold an earlier build wrote with a reading at its head.
	if err := db.SaveChatFolded("left", []provider.Message{
		{Role: provider.RoleUser, Content: agent.ResumeMessagePrefix + "branch master · 0 changed]\nold"},
		{Role: provider.RoleUser, Content: "first ask"},
	}); err != nil {
		t.Fatal(err)
	}

	saved, msgs, err := openHeadlessChat(db, chatSession{continueLast: true}, headlessSystem(), "prompt")
	if err != nil {
		t.Fatal(err)
	}
	a := agent.New(msgs, nil)
	saved.adopt(a)
	// The run compacts everything, the spliced reading among it.
	a.Compact("summarised", nil)
	saved.keepFolded(a.Folded())
	saved.save(a.Messages())

	got, err := db.LoadChatFolded(saved.slot)
	if err != nil {
		t.Fatal(err)
	}
	var said []string
	for _, m := range got {
		if strings.HasPrefix(m.Content, agent.ResumeMessagePrefix) {
			t.Fatalf("the fold holds a reading: %+v", got)
		}
		said = append(said, m.Content)
	}
	if !slices.Contains(said, "first ask") || !slices.Contains(said, "an ask") {
		t.Fatalf("the fold lost what was said: %v", said)
	}
}

// A served session hands its slot's fold to its agent and writes the fold a
// compaction grew, the way a `--continue` run does.
func TestServe_ACompactionKeepsTheFold(t *testing.T) {
	db := printStore(t)
	if err := db.SaveChat("left", []provider.Message{
		{Role: provider.RoleSystem, Content: "old prompt"},
		{Role: provider.RoleUser, Content: "second ask"},
		{Role: provider.RoleAssistant, Content: "second answer"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveChatFolded("left", []provider.Message{
		{Role: provider.RoleUser, Content: "first ask"},
	}); err != nil {
		t.Fatal(err)
	}
	saved, msgs, err := openHeadlessChat(db, chatSession{continueLast: true}, headlessSystem(), "prompt")
	if err != nil {
		t.Fatal(err)
	}
	a := agent.New(msgs, nil)
	saved.adopt(a)
	l := &serveLoop{agent: a, saved: saved}
	a.Compact("summarised", nil)
	l.saveConversation()

	got, err := db.LoadChatFolded(saved.slot)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 || got[0].Content != "first ask" {
		t.Fatalf("the served slot's fold = %+v, want the first ask kept and the compaction's added", got)
	}
}
