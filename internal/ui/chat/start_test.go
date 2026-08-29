package chat

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
)

// startNow is the clock the fixtures measure "4m ago" against, so the label
// is a fact of the test rather than a race with the wall clock.
var startNow = time.Date(2026, 8, 26, 11, 0, 0, 0, time.UTC)

// startFixture is a dirty Go checkout with a gate, a project context file and
// a session to pick up — the case the start screen is drawn from.
func startFixture() StartInfo {
	return StartInfo{
		Project: project.Info{
			Display: "~/src/shhh", Language: "go", Toolchain: "1.24",
			Repo: true, Branch: "main", Dirty: 3,
			Packages: 41, Unit: "package",
			ContextFiles: []string{"AGENTS.md"},
		},
		Gate: StartGate{
			Path: ".shhh/quality.json", Suite: "default",
			Checks: []string{"vet", "test"}, Suites: 1,
		},
		Recent: StartRecent{
			Present: true, Name: "(last session)", Turns: 7,
			Updated: startNow.Add(-4 * time.Minute), Cost: 0.42, Priced: true,
		},
		Now: startNow,
	}
}

// startModel is a sized model on its first contact with the fixture project.
func startModel(t *testing.T, info StartInfo) Model {
	t.Helper()
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithStartScreen(info)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	return updated.(Model)
}

func startText(m Model) string { return ansi.Strip(m.renderHistory()) }

func TestStartScreen_StatesTheProjectItOpenedIn(t *testing.T) {
	view := startText(startModel(t, startFixture()))
	for _, want := range []string{
		"~/src/shhh", "go 1.24", "git main", "3 files changed", "41 packages",
		"AGENTS.md",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("the screen never says %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Type a message") {
		t.Fatalf("the welcome line survived the start screen:\n%s", view)
	}
}

func TestStartScreen_NamesTheGateInEffect(t *testing.T) {
	view := startText(startModel(t, startFixture()))
	// The gate governs what runs without an approval, which is why it is on
	// the screen at all — so it has to say both parts.
	if !strings.Contains(view, "default") || !strings.Contains(view, "runs without asking") {
		t.Fatalf("the gate line is incomplete:\n%s", view)
	}

	info := startFixture()
	info.Gate = StartGate{Path: ".shhh/quality.json"}
	view = startText(startModel(t, info))
	if !strings.Contains(view, "not configured") || !strings.Contains(view, ".shhh/quality.json") {
		t.Fatalf("an absent gate should name the file it looked for:\n%s", view)
	}

	info.Gate = StartGate{Path: ".shhh/quality.json", Err: "suite \"default\" has no checks"}
	view = startText(startModel(t, info))
	if !strings.Contains(view, "unreadable") || !strings.Contains(view, "no checks") {
		t.Fatalf("a broken gate is not an absent one:\n%s", view)
	}
}

func TestStartScreen_OffersThreeThingsIncludingTheSessionToPickUp(t *testing.T) {
	view := startText(startModel(t, startFixture()))
	if !strings.Contains(view, "pick up (last session)") {
		t.Fatalf("the resume offer is missing:\n%s", view)
	}
	if !strings.Contains(view, "7 turns · $0.42 · 4m ago") {
		t.Fatalf("the resume offer is unpriced:\n%s", view)
	}
	if n := countOffers(view); n != 3 {
		t.Fatalf("offers = %d, want 3:\n%s", n, view)
	}

	// Nothing saved: still three, and the row the resume would have taken
	// goes to a second read-only offer rather than being left empty.
	info := startFixture()
	info.Recent = StartRecent{}
	view = startText(startModel(t, info))
	if strings.Contains(view, "pick up") {
		t.Fatalf("a resume was offered with nothing saved:\n%s", view)
	}
	if n := countOffers(view); n != 3 {
		t.Fatalf("offers = %d without a saved session, want 3:\n%s", n, view)
	}
}

func TestStartScreen_UnpricedSessionKeepsTheOfferAndDropsThePrice(t *testing.T) {
	info := startFixture()
	info.Recent.Priced, info.Recent.Cost = false, 0
	view := startText(startModel(t, info))
	if !strings.Contains(view, "7 turns · 4m ago") {
		t.Fatalf("expected turns and age with no price:\n%s", view)
	}
	if strings.Contains(view, "$0.00") {
		t.Fatalf("an unrecorded session was priced at zero:\n%s", view)
	}
}

func TestStartScreen_VerifyOfferFollowsTheProject(t *testing.T) {
	info := startFixture()
	if view := startText(startModel(t, info)); !strings.Contains(view, "run the default quality gate") {
		t.Fatalf("a configured gate should be the offer:\n%s", view)
	}

	info.Gate = StartGate{Path: ".shhh/quality.json"}
	if view := startText(startModel(t, info)); !strings.Contains(view, "go test ./...") {
		t.Fatalf("without a gate the toolchain's own tests should be offered:\n%s", view)
	}

	// An unrecognised project is asked for its tests in words rather than
	// handed a command that may not exist.
	info.Project.Language = ""
	view := startText(startModel(t, info))
	if strings.Contains(view, "go test") || !strings.Contains(view, "find this project's tests") {
		t.Fatalf("a command was invented for an unrecognised project:\n%s", view)
	}
}

func TestStartScreen_GoesOnTheFirstTurnAndDoesNotComeBackOnClear(t *testing.T) {
	m := startModel(t, startFixture())
	if !m.startScreenShowing() {
		t.Fatal("the screen should be showing on an empty session")
	}

	next, _ := m.sendUserMessage("fix the round limit")
	m = next.(Model)
	if m.startScreenShowing() {
		t.Fatal("the screen survived the first turn")
	}

	m.clearConversation()
	if len(m.transcript) != 0 {
		t.Fatal("/clear should empty the transcript")
	}
	if m.startScreenShowing() {
		t.Fatal("the screen came back on /clear after a turn had run")
	}
	// The turn is still notionally in flight after sendUserMessage; back at
	// rest, the empty transcript falls through to the plain welcome line.
	m.setTurnState(stateInput)
	if !strings.Contains(startText(m), "Type a message") {
		t.Fatalf("a cleared session should fall back to the welcome line:\n%s", startText(m))
	}
}

func TestStartScreen_ClearOfAGenuinelyEmptySessionKeepsIt(t *testing.T) {
	m := startModel(t, startFixture())
	m.clearConversation()
	if !m.startScreenShowing() {
		t.Fatal("nothing had been said, so the session is still new")
	}
}

func TestStartScreen_LoadedConversationSpendsIt(t *testing.T) {
	m := startModel(t, startFixture())
	m.loadConversation([]provider.Message{
		{Role: provider.RoleUser, Content: "hi"},
		{Role: provider.RoleAssistant, Content: "hello"},
	})
	m.clearConversation()
	if m.startScreenShowing() {
		t.Fatal("a session with a past is not new again once it is cleared")
	}
}

func TestStartScreen_ArrowsMoveThePointerAndEnterRunsTheOffer(t *testing.T) {
	m := startModel(t, startFixture())
	if m.startFocus != 0 {
		t.Fatalf("focus starts at %d, want 0", m.startFocus)
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	if m.startFocus != 1 {
		t.Fatalf("down moved focus to %d, want 1", m.startFocus)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = updated.(Model)
	if m.startFocus != 0 {
		t.Fatalf("up moved focus to %d, want 0", m.startFocus)
	}
	// The pointer does not run off either end of the list.
	for range 5 {
		updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
		m = updated.(Model)
	}
	if m.startFocus != 0 {
		t.Fatalf("focus ran off the top to %d", m.startFocus)
	}
	for range 9 {
		updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = updated.(Model)
	}
	if m.startFocus != 2 {
		t.Fatalf("focus ran off the bottom to %d", m.startFocus)
	}

	// Enter on a read-only offer sends it as an ordinary message, through the
	// same submit path typing it would take.
	m.startFocus = 1
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if len(m.transcript) == 0 || m.transcript[0].kind != entryUser {
		t.Fatalf("enter did not send the offer: %+v", m.transcript)
	}
	if !strings.Contains(m.transcript[0].text, "explain what changed in the working tree") {
		t.Fatalf("enter sent %q", m.transcript[0].text)
	}
}

func TestStartScreen_EnterOnTheResumeOfferLoadsThatSession(t *testing.T) {
	db, err := storage.OpenPath(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	saved := []provider.Message{
		{Role: provider.RoleUser, Content: "where were we"},
		{Role: provider.RoleAssistant, Content: "right here"},
	}
	if err := db.SaveChat("loop refactor", saved); err != nil {
		t.Fatal(err)
	}
	info := startFixture()
	info.Recent.Name = "loop refactor"

	m := startModel(t, info).WithDB(db)
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	view := startText(m)
	if !strings.Contains(view, "right here") {
		t.Fatalf("the saved session was not loaded:\n%s", view)
	}
	if m.startScreenShowing() {
		t.Fatal("loading a session should spend the start screen")
	}
}

func TestStartScreen_TypingDismissesTheListAndGivesTheKeysBack(t *testing.T) {
	m := startModel(t, startFixture())
	m.input.SetValue("what does this project do")

	if m.startChoosing() {
		t.Fatal("the list still claims the keys with a draft in the input")
	}
	view := startText(m)
	if strings.Contains(view, "[↑↓] choose") || strings.Contains(view, "worth doing first") {
		t.Fatalf("the dismissed list left its chrome behind:\n%s", view)
	}
	if !strings.Contains(view, "~/src/shhh") {
		t.Fatalf("dismissing the list took the facts with it:\n%s", view)
	}
	// Enter is the input's again, so the draft is sent rather than an offer.
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if len(m.transcript) == 0 || m.transcript[0].text != "what does this project do" {
		t.Fatalf("enter did not send the draft: %+v", m.transcript)
	}
}

func TestStartScreen_HistoryKeepsTheArrowsOnceThereIsHistory(t *testing.T) {
	m := startModel(t, startFixture())
	m.recordInput("an earlier message")
	m.spendStartScreen()

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = updated.(Model)
	if m.input.Value() != "an earlier message" {
		t.Fatalf("up should browse the input history, got %q", m.input.Value())
	}
}

func TestStartScreen_AbsentSurveyKeepsThePlainWelcome(t *testing.T) {
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream)
	m.width = 80
	if !strings.Contains(ansi.Strip(m.renderHistory()), "Type a message") {
		t.Fatal("a model with no survey should keep the welcome line")
	}
}

func TestAgoLabel_CoarsestUnitThatIsStillTrue(t *testing.T) {
	now := startNow
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{20 * time.Second, "just now"},
		{4 * time.Minute, "4m ago"},
		{90 * time.Minute, "1h ago"},
		{50 * time.Hour, "2d ago"},
	} {
		if got := agoLabel(now.Add(-tc.d), now); got != tc.want {
			t.Fatalf("agoLabel(-%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// countOffers counts the suggestion rows in a rendered screen: every row that
// opens with the pointer or the two-space gutter and then a glyph.
func countOffers(view string) int {
	n := 0
	for _, line := range strings.Split(view, "\n") {
		for _, prefix := range []string{"❯ ▸ ", "❯ ⚙ ", "  ▸ ", "  ⚙ "} {
			if strings.HasPrefix(line, prefix) {
				n++
				break
			}
		}
	}
	return n
}
