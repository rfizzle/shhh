package chat

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
)

// scaffoldFixturePaths is what the host hands the card. The chat side never
// derives them — it lists what it is given — so the fixture states them
// rather than computing them a second way.
func scaffoldFixturePaths() []string {
	return []string{project.StateDir + "/", project.ContextFile}
}

// scaffoldModel is a first-contact model in a checkout that could take the
// scaffold, with the write and the refusal recorded rather than performed.
func scaffoldModel(t *testing.T, wrote *string, declined *bool) Model {
	t.Helper()
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithStartScreen(startFixture()).
		WithScaffold(Scaffold{
			Offer: true,
			Paths: scaffoldFixturePaths(),
			Write: func() (string, error) {
				*wrote = project.ContextFile
				return project.ContextFile, nil
			},
			Decline: func() error { *declined = true; return nil },
		})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	return updated.(Model)
}

func TestScaffold_IsTheThirdOfferWhenTheCheckoutHasNoStateDirectory(t *testing.T) {
	var wrote string
	var declined bool
	m := scaffoldModel(t, &wrote, &declined)
	view := ansi.Strip(m.renderHistory())
	if !strings.Contains(view, "scaffold .shhh/") || !strings.Contains(view, "one approval") {
		t.Fatalf("the scaffold offer is missing:\n%s", view)
	}
	if strings.Contains(view, "quality gate and triage") {
		t.Fatalf("the scaffold offer should have taken the third row:\n%s", view)
	}
	if n := countOffers(view); n != 3 {
		t.Fatalf("offers = %d, want 3:\n%s", n, view)
	}
}

// The offer is a line of input like every other row on that screen, so
// choosing it can never reach somewhere typing could not.
func TestScaffold_TheOfferIsTheCommandBehindIt(t *testing.T) {
	var wrote string
	var declined bool
	m := scaffoldModel(t, &wrote, &declined)
	m.startFocus = 2
	if action := m.startAction(); action != scaffoldCommandName {
		t.Fatalf("the third offer types %q, want %q", action, scaffoldCommandName)
	}
}

func TestScaffold_AbsentWithoutTheOffer(t *testing.T) {
	var wrote string
	var declined bool
	m := scaffoldModel(t, &wrote, &declined)
	m.scaffold.Offer = false
	view := ansi.Strip(m.renderHistory())
	if strings.Contains(view, "scaffold .shhh/") {
		t.Fatalf("a checkout that already refused was offered again:\n%s", view)
	}
	if !strings.Contains(view, "quality gate and triage") {
		t.Fatalf("without the scaffold the verify offer should be back:\n%s", view)
	}
}

// The card is the point of the offer: it names every path before it asks,
// so nothing is written on the strength of a one-line row.
func TestScaffold_CardListsWhatItWouldWrite(t *testing.T) {
	var wrote string
	var declined bool
	m := scaffoldModel(t, &wrote, &declined)
	next, _ := m.scaffoldCommand()
	m = next.(Model)
	if m.state != stateScaffold {
		t.Fatalf("state = %v, want the scaffold card", m.state)
	}
	view := ansi.Strip(strings.Join(m.scaffoldLines(), "\n"))
	for _, want := range append(scaffoldFixturePaths(), "undo", "network") {
		if !strings.Contains(view, want) {
			t.Fatalf("the card never says %q:\n%s", want, view)
		}
	}
	if wrote != "" {
		t.Fatal("the card wrote a file just by opening")
	}
}

func TestScaffold_ApprovingWrites(t *testing.T) {
	var wrote string
	var declined bool
	m := scaffoldModel(t, &wrote, &declined)
	next, _ := m.scaffoldCommand()
	m = press(t, next.(Model), "y")
	if wrote != project.ContextFile {
		t.Fatalf("wrote = %q, want %q", wrote, project.ContextFile)
	}
	if declined {
		t.Fatal("an approval recorded a refusal")
	}
	if m.state == stateScaffold {
		t.Fatal("the card stayed up after it was answered")
	}
	if !strings.Contains(lastNote(m), project.ContextFile) {
		t.Fatalf("the answer does not name the file: %q", lastNote(m))
	}
}

// Declining is remembered for the repository, so the offer is made once.
func TestScaffold_DecliningIsRememberedAndTheOfferGoes(t *testing.T) {
	var wrote string
	var declined bool
	m := scaffoldModel(t, &wrote, &declined)
	next, _ := m.scaffoldCommand()
	m = press(t, next.(Model), "n")
	if !declined {
		t.Fatal("the refusal was not recorded")
	}
	if wrote != "" {
		t.Fatalf("a refusal wrote %q", wrote)
	}
	if m.scaffoldOffered() {
		t.Fatal("the offer survived being refused in this session")
	}
	if !strings.Contains(lastNote(m), scaffoldCommandName) {
		t.Fatalf("the refusal should name the command that still works: %q", lastNote(m))
	}
}

// A refusal that cannot be recorded is reported rather than swallowed: the
// person has been told the offer will not come back, so a store that did not
// take the answer is worth saying out loud.
func TestScaffold_AFailedRefusalIsReported(t *testing.T) {
	var wrote string
	m := scaffoldModel(t, &wrote, new(bool))
	m.scaffold.Decline = func() error { return errors.New("database is locked") }
	next, _ := m.scaffoldCommand()
	m = press(t, next.(Model), "n")
	if !strings.Contains(lastNote(m), "database is locked") {
		t.Fatalf("a failed refusal was swallowed: %q", lastNote(m))
	}
}

func TestScaffold_AFailedWriteIsReported(t *testing.T) {
	m := scaffoldModel(t, new(string), new(bool))
	m.scaffold.Write = func() (string, error) { return "", errors.New("permission denied") }
	next, _ := m.scaffoldCommand()
	m = press(t, next.(Model), "y")
	if !strings.Contains(lastNote(m), "permission denied") {
		t.Fatalf("a failed write was swallowed: %q", lastNote(m))
	}
}

// Esc is the way out of a screen the reader opened, so it settles nothing:
// the offer is still there afterwards, and nothing was recorded.
func TestScaffold_EscLeavesTheOfferStanding(t *testing.T) {
	var wrote string
	var declined bool
	m := scaffoldModel(t, &wrote, &declined)
	next, _ := m.scaffoldCommand()
	m = press(t, next.(Model), "esc")
	if m.state == stateScaffold {
		t.Fatal("esc did not close the card")
	}
	if declined {
		t.Fatal("esc recorded a refusal that outlives the session")
	}
	if wrote != "" {
		t.Fatalf("esc wrote %q", wrote)
	}
	if !m.scaffoldOffered() {
		t.Fatal("esc silenced the offer it was only supposed to step away from")
	}
	// And the card says as much, because the two ways of not writing differ
	// in what they leave behind.
	view := ansi.Strip(strings.Join(m.scaffoldLines(), "\n"))
	if !strings.Contains(view, "the offer stays") {
		t.Fatalf("the card never says what esc leaves:\n%s", view)
	}
}

// A conversation has no checkout to scaffold, and the command table answers
// for that before the surface ever does.
func TestScaffold_CommandIsNotPartOfAConversation(t *testing.T) {
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).WithConversation()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m = updated.(Model)
	if !m.unavailableCommand(scaffoldCommandName) {
		t.Fatalf("%s should not be part of a conversation", scaffoldCommandName)
	}
	next, _ := m.runCommand(scaffoldCommandName, scaffoldCommandName)
	m = next.(Model)
	if m.state == stateScaffold {
		t.Fatal("a conversation opened the scaffold card")
	}
	if !strings.Contains(lastNote(m), "not part of this session") {
		t.Fatalf("the refusal does not say why: %q", lastNote(m))
	}
}

func TestScaffold_UnwiredSessionSaysSoRatherThanOpeningACard(t *testing.T) {
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	next, _ := updated.(Model).scaffoldCommand()
	m = next.(Model)
	if m.state == stateScaffold {
		t.Fatal("a session with no checkout opened the card anyway")
	}
	if !strings.Contains(lastNote(m), "no checkout") {
		t.Fatalf("the refusal does not say why: %q", lastNote(m))
	}
}

// The command is reachable the three ways every command is: typed,
// completed, and listed.
func TestScaffold_CommandIsOfferedInTheRegistryAndTheHelp(t *testing.T) {
	found := false
	for _, c := range slashCommands {
		if c.name == scaffoldCommandName {
			found = true
		}
	}
	if !found {
		t.Errorf("%s is not in the command registry, so the palette cannot offer it", scaffoldCommandName)
	}
	if !strings.Contains(helpText(), scaffoldCommandName) {
		t.Errorf("/help does not list %s", scaffoldCommandName)
	}
}

// A card that answers a keystroke answers the pointer on the key that
// stands for it, the way every other card on this surface does.
func TestScaffold_CardAnswersThePointer(t *testing.T) {
	var wrote string
	m := scaffoldModel(t, &wrote, new(bool))
	next, _ := m.scaffoldCommand()
	m = next.(Model)
	card := m.decisionCard()
	if card == nil {
		t.Fatal("the scaffold card is invisible to the pointer")
	}
	lines := strings.Split(m.screen(), "\n")
	answered := false
	for y, line := range lines {
		for x := range len(ansi.Strip(line)) {
			if key, ok := card.KeyAt(line, x); ok && key == "y" {
				out, _ := m.clickKey(x, y)
				m, answered = out.(Model), true
				break
			}
		}
		if answered {
			break
		}
	}
	if !answered {
		t.Fatal("the card draws no [y] the pointer can land on")
	}
	if wrote == "" {
		t.Fatal("a click on [y] did not write")
	}
}

// End to end from the screen: the pointer on the third row, enter, and the
// card is up with nothing yet written.
func TestScaffold_ChoosingTheOfferOpensTheCard(t *testing.T) {
	var wrote string
	m := scaffoldModel(t, &wrote, new(bool))
	m.startFocus = 2
	m = press(t, m, "enter")
	if m.state != stateScaffold {
		t.Fatalf("state = %v, want the scaffold card", m.state)
	}
	if wrote != "" {
		t.Fatal("choosing the offer wrote a file without asking")
	}
}
