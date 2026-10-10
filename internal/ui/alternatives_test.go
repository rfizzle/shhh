package ui

// The commands the generator did not pick. They are rows of the view enter
// opens, and choosing one hands the surface back to the key row with the new
// command armed exactly as the first one was. A response without them is the ordinary result surface, unchanged.

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/keys"
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

// hasOthers is whether the view lists the alternatives, which is the only
// place the surface offers them.
func hasOthers(t *testing.T, m GenerateModel) bool {
	t.Helper()
	return strings.Contains(press(t, m, "enter").View().Content, "ss -ltn")
}

func TestAlternatives_TheViewListsThemAndTheBarDoesNot(t *testing.T) {
	view := withAlternatives(t, twoOthers).View().Content
	if strings.Contains(view, "others") || strings.Contains(view, "[a]") {
		t.Errorf("the key row still offers the alternatives:\n%s", view)
	}
	if !hasOthers(t, withAlternatives(t, twoOthers)) {
		t.Errorf("the view does not list the alternatives")
	}
	// The section is read, not shown: the command is the command.
	if strings.Contains(view, "--- alternatives") || strings.Contains(view, "ss -ltn") {
		t.Errorf("the alternatives section leaked onto the result surface:\n%s", view)
	}
	if !strings.Contains(view, "lsof -nP -iTCP -sTCP:LISTEN") {
		t.Errorf("the command is not on the surface:\n%s", view)
	}
}

func TestAlternatives_TheirAbsenceChangesNothing(t *testing.T) {
	m := press(t, armed(t, "ls -la", nil), "enter")
	if m.Phase() != phaseView {
		t.Fatalf("enter did not open the view: phase %v", m.Phase())
	}
	if view := m.View().Content; strings.Contains(view, "From here") {
		t.Errorf("a response with no alternatives drew a card of rows:\n%s", view)
	}
}

func TestAlternatives_ThePickerMarksTheCommandOnScreen(t *testing.T) {
	m := press(t, withAlternatives(t, twoOthers), "enter")
	if m.Phase() != phaseView {
		t.Fatalf("enter did not open the view: phase %v", m.Phase())
	}
	view := m.View().Content
	if !strings.Contains(view, "● lsof -nP -iTCP -sTCP:LISTEN") {
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

// The picker's key row is the register's spellings in the notation every
// other surface writes a live key in, so a keymap file that moves one moves
// the offer with it
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// The row is read back off the rendered screen rather than off the field it
// was built from, because what the check is worth is that the notation held
// all the way to the terminal: every bracketed run on it has to be a
// declaration's own spelling, and the three the card means have to be there.
func TestAlternatives_ThePickerOffersTheRegistersKeys(t *testing.T) {
	view := press(t, withAlternatives(t, twoOthers), "enter").View().Content
	for _, want := range []string{
		keys.Bracket(keys.Select.Move) + " move",
		keys.Bracket(keys.Select.Take) + " choose",
		keys.Bracket(keys.Select.Cancel) + " back",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the picker does not offer %q:\n%s", want, view)
		}
	}
	shown := map[string]bool{}
	for _, s := range append(keys.Surfaces(), keys.Programs()...) {
		for _, b := range s.Bindings {
			shown[keys.Shown(b)] = true
		}
	}
	row := ansi.Strip(view)
	row = row[strings.Index(row, keys.Bracket(keys.Select.Move)):]
	row, _, _ = strings.Cut(row, "\n")
	for _, m := range regexp.MustCompile(`\[([^\]]+)\]`).FindAllStringSubmatch(row, -1) {
		if !shown[m[1]] {
			t.Errorf("the picker offers %q on %q, which no binding is spelled", m[1], row)
		}
	}
}

func TestAlternatives_ChoosingOneMakesItTheCommand(t *testing.T) {
	m := press(t, withAlternatives(t, twoOthers), "enter")
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
	if got := press(t, m, "enter").View().Content; !strings.Contains(got, "● netstat") {
		t.Errorf("the picker still marks the old command:\n%s", got)
	}
}

func TestAlternatives_TheChosenCommandIsArmedLikeTheFirstOne(t *testing.T) {
	// The alternative here is destructive and the command it replaces is not:
	// everything the surface states about a command has to be re-read, not
	// carried over. The first one measures the directory rather than
	// emptying it — a find with -delete reads as a gentler spelling and is
	// the same act, which is why the safety list knows it.
	m := withAlternatives(t, "du -sh ./build\n--- alternatives\nrm -rf ./build\n# one call · not reversible")
	if m.danger {
		t.Fatalf("the first command should not be rated destructive:\n%s", m.View().Content)
	}
	m = press(t, press(t, press(t, m, "enter"), "down"), "enter")

	view := m.View().Content
	if !m.danger {
		t.Errorf("the chosen command was not re-rated:\n%s", view)
	}
	if got := m.Reach().Reach(); !strings.Contains(view, "⛨ "+got) {
		t.Errorf("the containment line was not re-resolved:\n%s", view)
	}
}

func TestAlternatives_BackingOutKeepsTheCommand(t *testing.T) {
	m := withAlternatives(t, twoOthers)
	m = press(t, press(t, press(t, m, "enter"), "down"), "esc")
	if m.Phase() != phaseAction {
		t.Fatalf("esc did not return to the key row: phase %v", m.Phase())
	}
	if !strings.Contains(m.View().Content, "lsof -nP") {
		t.Errorf("backing out of the picker changed the command:\n%s", m.View().Content)
	}
}

func TestAlternatives_RunningTheChosenCommandRunsThatOne(t *testing.T) {
	m := press(t, withAlternatives(t, twoOthers), "enter")
	m = press(t, press(t, m, "down"), "enter")
	m = press(t, m, "y")
	if got := m.Result().Command; got != "netstat -anv -p tcp | grep LISTEN" {
		t.Errorf("the result carries %q", got)
	}
}

func TestAlternatives_AnEditRewritesTheChoiceItStartedFrom(t *testing.T) {
	m := press(t, withAlternatives(t, twoOthers), "e")
	m.editInput.SetValue("lsof -nP -iTCP")
	m = press(t, m, "enter")
	view := press(t, m, "enter").View().Content
	if !strings.Contains(view, "ss -ltn") {
		t.Errorf("an edit dropped the alternatives to the request:\n%s", view)
	}
	if !strings.Contains(view, "● lsof -nP -iTCP\n") && !strings.Contains(view, "● lsof -nP -iTCP ") {
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
	if !strings.Contains(press(t, m, "enter").View().Content, "ss -ltn") {
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

	if strings.Contains(press(t, m, "enter").View().Content, "ss -ltn") {
		t.Fatalf("the revised command inherited the old offers:\n%s", m.View().Content)
	}
	m = press(t, press(t, m, "enter"), "enter")
	if !strings.Contains(m.View().Content, "lsof -nP") {
		t.Errorf("stepping back did not bring the command back:\n%s", m.View().Content)
	}
	if back := press(t, m, "enter").View().Content; !strings.Contains(back, "● lsof -nP") {
		t.Errorf("the restored picker does not mark the restored command:\n%s", back)
	}
}
