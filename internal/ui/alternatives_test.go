package ui

// The commands the generator did not pick. The key row counts them,
// `[a]` opens the select card on them, and choosing one hands the surface
// back to the key row with the new command armed exactly as the first one
// was. A response without them is the ordinary result surface, unchanged.

import (
	"context"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// withAlternatives streams a structured response to completion and hands back
// the model on the result surface.
func withAlternatives(t *testing.T, response string) GenerateModel {
	t.Helper()
	m := NewGenerateModel(makeEvents(response), noopCancel, nil, nil, nil, "").
		WithExplain(ExplainNone)
	return drainStream(m, 2)
}

const twoOthers = `lsof -nP -iTCP -sTCP:LISTEN
--- alternatives
netstat -anv -p tcp | grep LISTEN
# faster · no process names
ss -ltn
# fastest · Linux only`

func TestAlternatives_TheKeyRowCountsThem(t *testing.T) {
	view := withAlternatives(t, twoOthers).View().Content
	if !strings.Contains(view, "[a] 2 others") {
		t.Errorf("the key row does not offer the alternatives:\n%s", view)
	}
	// The section is read, not shown: the command is the command.
	if strings.Contains(view, "--- alternatives") || strings.Contains(view, "ss -ltn") {
		t.Errorf("the alternatives section leaked onto the result surface:\n%s", view)
	}
	if !strings.Contains(view, "lsof -nP -iTCP -sTCP:LISTEN") {
		t.Errorf("the command is not on the surface:\n%s", view)
	}
}

func TestAlternatives_OneOfThemIsCountedInTheSingular(t *testing.T) {
	view := withAlternatives(t, "ls -la\n--- alternatives\nls -lah").View().Content
	if !strings.Contains(view, "[a] 1 other") {
		t.Errorf("one alternative should read as one:\n%s", view)
	}
}

func TestAlternatives_TheirAbsenceChangesNothing(t *testing.T) {
	plain := armed(t, "ls -la", nil).View().Content
	if strings.Contains(plain, "[a]") {
		t.Errorf("a response with no alternatives offered a key for them:\n%s", plain)
	}
	// The key that is not there is not a gap in the row either.
	if !strings.Contains(plain, "[↵] run") || !strings.Contains(plain, "[esc] quit") {
		t.Errorf("the row is not the row it was:\n%s", plain)
	}
	m := press(t, armed(t, "ls -la", nil), "a")
	if m.Phase() != phaseAction {
		t.Errorf("`a` opened something with nothing in it: phase %v", m.Phase())
	}
}

func TestAlternatives_ThePickerMarksTheCommandOnScreen(t *testing.T) {
	m := press(t, withAlternatives(t, twoOthers), "a")
	if m.Phase() != phasePick {
		t.Fatalf("`a` did not open the picker: phase %v", m.Phase())
	}
	view := m.View().Content
	if !strings.Contains(view, "◆ lsof -nP -iTCP -sTCP:LISTEN") {
		t.Errorf("the command on screen is not marked in the list:\n%s", view)
	}
	for _, want := range []string{"netstat -anv -p tcp | grep LISTEN", "ss -ltn"} {
		if !strings.Contains(view, want) {
			t.Errorf("the picker is missing %q:\n%s", want, view)
		}
	}
	// The tradeoff is the whole reason a second command is worth reading; the
	// focused row's is on screen with it.
	if !strings.Contains(view, "the command on screen") {
		t.Errorf("the marked row does not say what it is:\n%s", view)
	}
}

func TestAlternatives_ChoosingOneMakesItTheCommand(t *testing.T) {
	m := press(t, withAlternatives(t, twoOthers), "a")
	m = press(t, m, "down")
	// The focused row's tradeoff is what the choice is being made on.
	if !strings.Contains(m.View().Content, "faster · no process names") {
		t.Errorf("the focused alternative did not state its tradeoff:\n%s", m.View().Content)
	}
	m = press(t, m, "enter")

	if m.Phase() != phaseAction {
		t.Fatalf("choosing an alternative did not return to the key row: phase %v", m.Phase())
	}
	view := m.View().Content
	if !strings.Contains(view, "netstat -anv -p tcp | grep LISTEN") {
		t.Errorf("the chosen command is not on the surface:\n%s", view)
	}
	if strings.Contains(view, "lsof -nP") {
		t.Errorf("the command it replaced is still showing:\n%s", view)
	}
	// It is still an offer of three, with the mark moved.
	if !strings.Contains(view, "[a] 2 others") {
		t.Errorf("the offers did not survive the choice:\n%s", view)
	}
	if !strings.Contains(press(t, m, "a").View().Content, "◆ netstat") {
		t.Errorf("the picker still marks the old command:\n%s", press(t, m, "a").View().Content)
	}
}

func TestAlternatives_TheChosenCommandIsArmedLikeTheFirstOne(t *testing.T) {
	// The alternative here is destructive and the command it replaces is not:
	// everything the surface states about a command has to be re-read, not
	// carried over.
	m := withAlternatives(t, "find ./build -mindepth 1 -delete\n--- alternatives\nrm -rf ./build\n# one call · not reversible")
	if strings.Contains(m.View().Content, "[y] run it") {
		t.Fatalf("the first command should not have moved the default:\n%s", m.View().Content)
	}
	m = press(t, press(t, press(t, m, "a"), "down"), "enter")

	view := m.View().Content
	if !strings.Contains(view, "[↵] show what it would affect") || !strings.Contains(view, "[y] run it") {
		t.Errorf("the safe default did not move with the chosen command:\n%s", view)
	}
	if got := m.Reach().Reach(); !strings.Contains(view, "⛨ "+got) {
		t.Errorf("the containment line was not re-resolved:\n%s", view)
	}
}

func TestAlternatives_BackingOutKeepsTheCommand(t *testing.T) {
	m := withAlternatives(t, twoOthers)
	m = press(t, press(t, press(t, m, "a"), "down"), "esc")
	if m.Phase() != phaseAction {
		t.Fatalf("esc did not return to the key row: phase %v", m.Phase())
	}
	if !strings.Contains(m.View().Content, "lsof -nP") {
		t.Errorf("backing out of the picker changed the command:\n%s", m.View().Content)
	}
}

func TestAlternatives_RunningTheChosenCommandRunsThatOne(t *testing.T) {
	m := press(t, withAlternatives(t, twoOthers), "a")
	m = press(t, press(t, m, "down"), "enter")
	m = press(t, m, "enter")
	if got := m.Result().Command; got != "netstat -anv -p tcp | grep LISTEN" {
		t.Errorf("the result carries %q", got)
	}
}

func TestAlternatives_AnEditRewritesTheChoiceItStartedFrom(t *testing.T) {
	m := press(t, withAlternatives(t, twoOthers), "e")
	m.editInput.SetValue("lsof -nP -iTCP")
	m = press(t, m, "enter")
	if !strings.Contains(m.View().Content, "[a] 2 others") {
		t.Errorf("an edit dropped the alternatives to the request:\n%s", m.View().Content)
	}
	view := press(t, m, "a").View().Content
	if !strings.Contains(view, "◆ lsof -nP -iTCP\n") && !strings.Contains(view, "◆ lsof -nP -iTCP ") {
		t.Errorf("the picker still lists the command as it was before the edit:\n%s", view)
	}
}

func TestAlternatives_TheStreamNeverShowsTheSection(t *testing.T) {
	// The response arrives token by token, and the section is the tail of it.
	m := NewGenerateModel(
		makeEvents("lsof -nP", "\n--- alter", "natives\nss -ltn"),
		noopCancel, nil, nil, nil, "").WithExplain(ExplainNone)
	for i := 0; i < 3; i++ {
		m = drainStream(m, 1)
		if view := m.View().Content; strings.Contains(view, "-") && strings.Contains(view, "alter") {
			t.Errorf("the alternatives section rendered mid-stream:\n%s", view)
		}
		if strings.Contains(m.View().Content, "ss -ltn") {
			t.Errorf("an alternative rendered as part of the command:\n%s", m.View().Content)
		}
	}
	m = drainStream(m, 1)
	if !strings.Contains(m.View().Content, "[a] 1 other") {
		t.Errorf("the section that never showed did not become the offer:\n%s", m.View().Content)
	}
}

func TestAlternatives_SteppingBackRestoresTheOffersWithTheCommand(t *testing.T) {
	// The revise answers with one command and no section, so the offers on
	// screen after it are the revised command's own — none.
	newStream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		return makeEvents("ps -ef | grep -w LISTEN"), noopCancel, nil
	}
	m := NewGenerateModel(makeEvents(twoOthers), noopCancel, nil, newStream, nil, "").
		WithExplain(ExplainNone)
	m = drainStream(m, 2)
	m = press(t, m, "r")
	for _, r := range "only mine" {
		m = press(t, m, string(r))
	}
	m = press(t, m, "enter")
	m = drainStream(m, 2)

	if strings.Contains(m.View().Content, "[a]") {
		t.Fatalf("the revised command inherited the old offers:\n%s", m.View().Content)
	}
	m = press(t, m, "u")
	if !strings.Contains(m.View().Content, "[a] 2 others") {
		t.Errorf("stepping back did not bring the offers back with the command:\n%s", m.View().Content)
	}
	if back := press(t, m, "a").View().Content; !strings.Contains(back, "◆ lsof -nP") {
		t.Errorf("the restored picker does not mark the restored command:\n%s", back)
	}
}
