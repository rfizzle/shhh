package agent

import (
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

func TestCompactSetsTheFoldedTurnsAsideAndKeepsThemAcrossASecondOne(t *testing.T) {
	a := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "one"},
		{Role: provider.RoleAssistant, Content: "a1"},
		{Role: provider.RoleUser, Content: "two"},
		{Role: provider.RoleAssistant, Content: "a2"},
	}, nil)
	a.Compact("first", a.Messages()[3:])
	if got := a.Folded(); len(got) != 2 || got[0].Content != "one" {
		t.Fatalf("first compaction folded %+v", got)
	}
	if got := len(a.Messages()); got != 4 {
		t.Fatalf("the model's list holds %d messages, want system, summary and the kept pair", got)
	}

	a.Compact("second", nil)
	got := a.Folded()
	if len(got) != 5 || got[2].Content != CompactSummaryMessage("first") || got[4].Content != "a2" {
		t.Fatalf("second compaction left %+v", got)
	}
	if len(a.Messages()) != 2 {
		t.Fatalf("the model's list holds %d messages, want system and summary", len(a.Messages()))
	}
}
