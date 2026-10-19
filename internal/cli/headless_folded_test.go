package cli

import (
	"testing"

	"github.com/rfizzle/agentcore/agent"
	"github.com/rfizzle/llm"
)

// A headless continuation keeps the turns the slot's compaction folded, and a
// compaction in the run adds to them, so a later reopen draws the whole record.
func TestHeadlessContinue_AFoldedTurnSurvivesTheRun(t *testing.T) {
	db := printStore(t)
	stored := []llm.Message{
		{Role: llm.RoleSystem, Content: "old prompt"},
		{Role: llm.RoleUser, Content: "summary of the start", Machine: true},
		{Role: llm.RoleUser, Content: "second ask"},
		{Role: llm.RoleAssistant, Content: "second answer"},
	}
	if err := db.SaveChat("left", stored); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveChatFolded("left", []llm.Message{
		{Role: llm.RoleUser, Content: "first ask"},
		{Role: llm.RoleAssistant, Content: "first answer"},
	}); err != nil {
		t.Fatal(err)
	}

	saved, msgs, err := openHeadlessChat(db, chatSession{continueLast: true}, headlessSystem(), "prompt")
	if err != nil {
		t.Fatal(err)
	}
	a := agent.New(msgs, nil)
	a.SetFolded(saved.folded)
	if got := a.Folded(); len(got) != 2 || got[0].Content != "first ask" {
		t.Fatalf("the run was not handed the slot's folded turns: %+v", got)
	}

	// The run compacts: the second exchange folds onto the first.
	all := a.Messages()
	a.Compact("both summarised", all[len(all)-1:])
	saved.keepFolded(a.Folded())
	saved.save(a.Messages())

	got, err := db.LoadChatFolded(saved.slot)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 3 || got[0].Content != "first ask" {
		t.Fatalf("the slot's fold after the run = %+v, want the first exchange kept and the run's added", got)
	}
}
