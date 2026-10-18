package agent

import (
	"testing"

	"github.com/rfizzle/llm"
)

// The handoff is the first half of a reopening's block: stripped with the
// survey behind it, and left alone without one.
func TestStripResumeContext_TakesTheHandoffWithTheReading(t *testing.T) {
	handoff := llm.Message{Role: llm.RoleUser, Content: ResumeHandoffPrefix + "\n\nRetry backoff half done"}
	survey := llm.Message{Role: llm.RoleUser, Content: ResumeMessagePrefix + "branch main · 0 changed]\nThis is the checkout."}
	turn := llm.Message{Role: llm.RoleUser, Content: "go on"}
	sys := llm.Message{Role: llm.RoleSystem, Content: "sys"}
	got := StripResumeContext([]llm.Message{sys, handoff, survey, turn})
	if len(got) != 2 || got[1].Content != "go on" {
		t.Fatalf("the handoff and the survey should both go, got %+v", got)
	}
	alone := StripResumeContext([]llm.Message{sys, handoff, turn})
	if len(alone) != 3 {
		t.Fatalf("a handoff with no survey behind it is somebody's turn and stays, got %+v", alone)
	}
}
