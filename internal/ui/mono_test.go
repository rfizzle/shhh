package ui

// The one-shot generate UI honours mono through the same shared palette the
// chat TUI does (S-095): its styles are rebuilt on a palette swap rather than
// captured once at init.

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/ui/components"
)

func TestMonoReachesTheGenerateStyles(t *testing.T) {
	was := components.Mono()
	t.Cleanup(func() { components.SetMono(was) })

	components.SetMono(false)
	fullCommand := sty.Command.GetForeground()
	fullError := sty.Error.GetForeground()

	components.SetMono(true)
	if sty.Command.GetForeground() == fullCommand {
		t.Errorf("sty.Command kept its full-palette foreground %v after the mono swap", fullCommand)
	}
	if sty.Error.GetForeground() == fullError {
		t.Errorf("sty.Error kept its full-palette foreground %v after the mono swap", fullError)
	}

	// The generate UI's whole surface, not just the two above.
	for name, s := range map[string]lipgloss.Style{
		"sty.Command":      sty.Command,
		"sty.Error":        sty.Error,
		"sty.EditPrompt":   sty.EditPrompt,
		"sty.RevisePrompt": sty.RevisePrompt,
		"sty.ExplainLabel": sty.ExplainLabel,
		"sty.ExplainBody":  sty.ExplainBody,
		"sty.Key":          sty.Key,
		"sty.KeyLabel":     sty.KeyLabel,
		"sty.PrimaryKey":   sty.PrimaryKey,
		"sty.DangerKey":    sty.DangerKey,
		"sty.Reach":        sty.Reach,
		"sty.Risk":         sty.Risk,
		"sty.Dim":          sty.Dim,
		"sty.PastCommand":  sty.PastCommand,
	} {
		if fg := s.GetForeground(); fg != components.MonoFg && fg != components.MonoDim {
			t.Errorf("%s has foreground %v, which is not one of the two greys", name, fg)
		}
	}
}
