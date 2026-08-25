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
	fullCommand := CommandStyle.GetForeground()
	fullError := ErrorStyle.GetForeground()

	components.SetMono(true)
	if CommandStyle.GetForeground() == fullCommand {
		t.Errorf("CommandStyle kept its full-palette foreground %v after the mono swap", fullCommand)
	}
	if ErrorStyle.GetForeground() == fullError {
		t.Errorf("ErrorStyle kept its full-palette foreground %v after the mono swap", fullError)
	}

	// The generate UI's whole surface, not just the two above.
	for name, s := range map[string]lipgloss.Style{
		"CommandStyle":      CommandStyle,
		"ErrorStyle":        ErrorStyle,
		"ActiveStyle":       ActiveStyle,
		"InactiveStyle":     InactiveStyle,
		"EditPromptStyle":   EditPromptStyle,
		"RevisePromptStyle": RevisePromptStyle,
		"ExplainLabelStyle": ExplainLabelStyle,
		"ExplainBodyStyle":  ExplainBodyStyle,
	} {
		if fg := s.GetForeground(); fg != components.MonoFg && fg != components.MonoDim {
			t.Errorf("%s has foreground %v, which is not one of the two greys", name, fg)
		}
	}
	if bg := ActiveStyle.GetBackground(); bg != components.MonoBg {
		t.Errorf("ActiveStyle background is %v, want the mono selection grey", bg)
	}
}
