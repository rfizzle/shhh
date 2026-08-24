package chat

import (
	"strings"
	"testing"
)

func TestGate_SlashCommand(t *testing.T) {
	var got []string
	m := gatedModel(t, nil, nil).WithGate(Gate{
		Manage: func(args []string) string {
			got = append(got, strings.Join(args, " "))
			return "gate says"
		},
	})
	handled, result := m.handleSlashCommand("/gate result")
	if !handled || result != "gate says" {
		t.Fatalf("/gate result = %v %q", handled, result)
	}
	m.handleSlashCommand("/gate run smoke")
	if len(got) != 2 || got[0] != "result" || got[1] != "run smoke" {
		t.Fatalf("manage args = %v", got)
	}
}

func TestGate_SlashCommandUnavailable(t *testing.T) {
	m := gatedModel(t, nil, nil)
	handled, result := m.handleSlashCommand("/gate run")
	if !handled || !strings.Contains(result, "unavailable") {
		t.Fatalf("/gate without a runner = %v %q", handled, result)
	}
}

func TestGate_HelpMentionsCommand(t *testing.T) {
	if !strings.Contains(helpText(), "/gate") {
		t.Fatal("/help must list /gate")
	}
}
