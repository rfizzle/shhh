package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
)

// draftedDeclaration is a two-line declaration as the host hands it back: the
// loader has read it, and the model said which binary each line provides.
func draftedDeclaration() ToolchainDraft {
	tc := project.Toolchain{
		Install: []string{"go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.5.0"},
		Hosts:   []string{"proxy.golang.org", "sum.golang.org"},
		Check:   []string{"golangci-lint"},
	}
	return ToolchainDraft{
		Content:  tc.Render(),
		Provides: map[string][]string{tc.Install[0]: {"golangci-lint"}},
	}
}

// draftRecorder is the host's half of the flow as a test holds it: what the
// drafting was asked, and what the card's yes wrote.
type draftRecorder struct {
	asked   []bool
	written [][]byte
}

func (r *draftRecorder) toolchain(answer ToolchainDraft) Toolchain {
	return Toolchain{
		Draft: func(_ context.Context, review bool) ToolchainDraft {
			r.asked = append(r.asked, review)
			return answer
		},
		WriteDraft: func(content []byte) (string, error) {
			r.written = append(r.written, content)
			return project.ToolchainFile, nil
		},
	}
}

// declarationModel is a first-contact session in a Go repository with the draft
// wired.
func declarationModel(t testing.TB, width int, tc Toolchain, info StartInfo) Model {
	t.Helper()
	return frameModel(t, width, 40).WithStartScreen(info).
		WithContainment(Containment{Status: "contained: bwrap (workspace profile)", Mechanism: "bwrap", Profile: "workspace", Network: true, Toolchain: tc})
}

// runDraftCmd runs what /toolchain started until the drafting's answer comes
// back, and hands it to the session.
func runDraftCmd(t testing.TB, m Model, cmd tea.Cmd) Model {
	t.Helper()
	pending := []tea.Cmd{cmd}
	for len(pending) > 0 {
		next := pending[0]
		pending = pending[1:]
		if next == nil {
			continue
		}
		switch msg := next().(type) {
		case tea.BatchMsg:
			pending = append(pending, msg...)
		case toolchainDraftMsg:
			updated, _ := m.Update(msg)
			return updated.(Model)
		}
	}
	t.Fatal("/toolchain started no drafting")
	return m
}

// declarationCard is the session with the drafting's answer on its card.
func declarationCard(t testing.TB, width int, tc Toolchain, info StartInfo) Model {
	t.Helper()
	m := declarationModel(t, width, tc, info)
	next, cmd := m.toolchainCommand()
	return runDraftCmd(t, next.(Model), cmd)
}

// The offer takes the read-only slot: beside a session to pick up it is the
// one read-only row, and without one it is the second, where the summary of
// the last commits would have been. Choosing it types the command.
func TestToolchainDraft_TheOfferTakesTheReadOnlySlot(t *testing.T) {
	var r draftRecorder
	m := declarationModel(t, 110, r.toolchain(draftedDeclaration()), startFixture())
	view := ansi.Strip(m.renderHistory())
	if !strings.Contains(view, "draft this checkout's toolchain declaration") || !strings.Contains(view, "reads only, then asks") {
		t.Fatalf("the start screen does not offer the draft:\n%s", view)
	}
	if strings.Contains(view, "explain what changed in the working tree") {
		t.Fatalf("the draft sits beside the read-only row rather than in its slot:\n%s", view)
	}
	m.startFocus = 1
	if action := m.startAction(); action != toolchainCommandName {
		t.Fatalf("the read-only offer types %q, want %q", action, toolchainCommandName)
	}

	info := startFixture()
	info.Recent = StartRecent{}
	m = declarationModel(t, 110, r.toolchain(draftedDeclaration()), info)
	view = ansi.Strip(m.renderHistory())
	if !strings.Contains(view, "explain what changed in the working tree") || strings.Contains(view, "summarise the last ten commits") {
		t.Fatalf("with no session to resume the draft should take the second read-only row:\n%s", view)
	}
	m.startFocus = 1
	if action := m.startAction(); action != toolchainCommandName {
		t.Fatalf("the second offer types %q, want %q", action, toolchainCommandName)
	}
	if len(r.asked) != 0 {
		t.Fatal("drawing the offer started a drafting")
	}
}

// Where a declaration exists the same slot offers a review, and taking it
// asks for one.
func TestToolchainDraft_AFileThatExistsIsOfferedAReview(t *testing.T) {
	var r draftRecorder
	tc := r.toolchain(draftedDeclaration())
	tc.Exists = true
	m := declarationModel(t, 110, tc, startFixture())
	if view := ansi.Strip(m.renderHistory()); !strings.Contains(view, "review this checkout's toolchain declaration") {
		t.Fatalf("a checkout with a declaration is not offered a review:\n%s", view)
	}
	next, cmd := m.toolchainCommand()
	runDraftCmd(t, next.(Model), cmd)
	if len(r.asked) != 1 || !r.asked[0] {
		t.Fatalf("the drafting was asked %v, want one review", r.asked)
	}
}

// Nothing here to read a toolchain out of — no repository, no build file
// the survey knew — and no session to put the card to: no offer either way.
func TestToolchainDraft_OfferedOnlyWhereThereIsSomethingToRead(t *testing.T) {
	var r draftRecorder
	bare := startFixture()
	bare.Project.Repo, bare.Project.Language = false, ""
	bareModel := declarationModel(t, 110, r.toolchain(draftedDeclaration()), bare)
	if view := ansi.Strip(bareModel.renderHistory()); strings.Contains(view, "toolchain declaration") {
		t.Fatalf("a directory with no repository and no build file was offered the draft:\n%s", view)
	}
	buildOnly := bare
	buildOnly.Project.Language = "python"
	buildModel := declarationModel(t, 110, r.toolchain(draftedDeclaration()), buildOnly)
	if view := ansi.Strip(buildModel.renderHistory()); !strings.Contains(view, "toolchain declaration") {
		t.Fatalf("a checkout holding a build file the survey knows was not offered the draft:\n%s", view)
	}
	unwired := declarationModel(t, 110, Toolchain{}, startFixture())
	if view := ansi.Strip(unwired.renderHistory()); strings.Contains(view, "toolchain declaration") {
		t.Fatalf("a session with nobody to put the card to was offered the draft:\n%s", view)
	}
	if next, _ := unwired.toolchainCommand(); next.(Model).state == stateToolchainDraft {
		t.Fatal("/toolchain opened a card in a session that cannot draft")
	}
}

// The card shows the file, each line with what it provides and the hosts it
// reaches; esc leaves it waiting, [n] drops it, and only the yes writes — the
// exact bytes the loader read.
func TestToolchainDraft_NothingIsWrittenUntilTheYes(t *testing.T) {
	var r draftRecorder
	answer := draftedDeclaration()
	m := declarationCard(t, 110, r.toolchain(answer), startFixture())
	if m.state != stateToolchainDraft {
		t.Fatalf("state = %v, want the draft card", m.state)
	}
	card := ansi.Strip(strings.Join(m.toolchainDraftLines(), "\n"))
	for _, want := range []string{"Approve toolchain declaration", "write .shhh/toolchain.toml", "provides golangci-lint",
		"proxy.golang.org, sum.golang.org", "[y] write it", "[e] edit first", "[n] nothing written"} {
		if !strings.Contains(card, want) {
			t.Errorf("the card never says %q:\n%s", want, card)
		}
	}
	if len(r.written) != 0 {
		t.Fatal("the draft was written before the card was answered")
	}

	m = press(t, m, "esc")
	if m.state == stateToolchainDraft || len(r.written) != 0 {
		t.Fatalf("esc should leave with nothing written (state %v, %d writes)", m.state, len(r.written))
	}
	next, _ := m.toolchainCommand()
	m = next.(Model)
	if m.state != stateToolchainDraft || len(r.asked) != 1 {
		t.Fatalf("/toolchain after esc should reopen the waiting draft without asking again (state %v, asked %d)", m.state, len(r.asked))
	}

	m = press(t, m, "n")
	if m.state == stateToolchainDraft || len(r.written) != 0 || m.toolchain().drafting != nil {
		t.Fatal("[n] should drop the draft with nothing written")
	}

	next, cmd := m.toolchainCommand()
	m = runDraftCmd(t, next.(Model), cmd)
	m = press(t, m, "y")
	if len(r.written) != 1 || string(r.written[0]) != string(answer.Content) {
		t.Fatalf("the yes wrote %q, want the drafted file", r.written)
	}
	if !strings.Contains(ansi.Strip(m.renderHistory()), "wrote .shhh/toolchain.toml") {
		t.Fatal("the write left no row saying what it wrote")
	}
}

// A checkout nobody has trusted gets the file written and the card says the
// loader will not read it until it is — with the command that trusts it.
func TestToolchainDraft_AnUntrustedCheckoutIsToldTheFileWaitsOnTrust(t *testing.T) {
	var r draftRecorder
	tc := r.toolchain(draftedDeclaration())
	tc.Untrusted = true
	m := declarationCard(t, 110, tc, startFixture())
	card := ansi.Strip(strings.Join(m.toolchainDraftLines(), "\n"))
	if !strings.Contains(card, "once trusted: shhh trust") {
		t.Fatalf("an untrusted checkout's card does not say the file waits on trust:\n%s", card)
	}
}

// A review's card is the diff against the file as it stands and each change
// with its reason.
func TestToolchainDraft_AReviewShowsItsChangesAndTheirReasons(t *testing.T) {
	var r draftRecorder
	answer := draftedDeclaration()
	answer.Previous = []byte("check = [\"golangci-lint\"]\n")
	answer.Changes = []ToolchainChange{{Change: "add the golangci-lint install line", Reason: "Makefile's lint target runs it"}}
	tc := r.toolchain(answer)
	tc.Exists = true
	m := declarationCard(t, 130, tc, startFixture())
	card := ansi.Strip(strings.Join(m.toolchainDraftLines(), "\n"))
	for _, want := range []string{"rewrite .shhh/toolchain.toml", "add the golangci-lint install line", "Makefile's lint target runs it"} {
		if !strings.Contains(card, want) {
			t.Errorf("the review card never says %q:\n%s", want, card)
		}
	}
}

// A drafting that lands while the reader is on another surface waits for
// /toolchain rather than taking the keyboard from it.
func TestToolchainDraft_ADraftThatLandsOnABusyScreenWaits(t *testing.T) {
	var r draftRecorder
	m := declarationModel(t, 110, r.toolchain(draftedDeclaration()), startFixture())
	next, cmd := m.toolchainCommand()
	m = next.(Model)
	m.enterSurface(stateKeyList)
	m = runDraftCmd(t, m, cmd)
	if m.state != stateKeyList {
		t.Fatalf("the card took the screen from the key list (state %v)", m.state)
	}
	if m.toolchain().drafting == nil || m.toolchain().drafting.draft == nil {
		t.Fatal("the draft was lost rather than kept for /toolchain")
	}
}

// A failed drafting and a review with nothing to change each leave a row and
// no card.
func TestToolchainDraft_NoCardWithoutADeclarationToWrite(t *testing.T) {
	for _, answer := range []ToolchainDraft{{Err: "the loader refused the draft twice"}, {Unchanged: true}} {
		var r draftRecorder
		m := declarationCard(t, 110, r.toolchain(answer), startFixture())
		if m.state == stateToolchainDraft || m.toolchain().drafting != nil {
			t.Fatalf("%+v opened a card", answer)
		}
	}
}

// What comes back from the editor is read by the loader: an edit it refuses
// leaves the card on the draft before it, and one it takes replaces it.
func TestToolchainDraft_AnEditIsReadByTheLoader(t *testing.T) {
	var r draftRecorder
	answer := draftedDeclaration()
	m := declarationCard(t, 110, r.toolchain(answer), startFixture())
	edit := func(text string) Model {
		path := filepath.Join(t.TempDir(), "draft.toml")
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		next, _ := m.toolchainEditorFinished(toolchainEditorDoneMsg{path: path})
		return next.(Model)
	}
	refused := edit("install = [\"go install example.com/cmd/tool@latest\"]\n")
	if got := string(refused.toolchain().drafting.draft.Content); got != string(answer.Content) {
		t.Fatalf("a refused edit replaced the draft with %q", got)
	}
	if !strings.Contains(ansi.Strip(refused.renderHistory()), "would not load") {
		t.Fatal("a refused edit did not say why in the loader's words")
	}
	taken := edit("check = [\"gosec\"]\n")
	if got := string(taken.toolchain().drafting.draft.Content); got != "check = [\"gosec\"]\n" || taken.state != stateToolchainDraft {
		t.Fatalf("an edit the loader takes should be the card's file (state %v, %q)", taken.state, got)
	}
}

// The session boundary drops a drafting in flight: its answer arriving in the
// new session opens nothing.
func TestToolchainDraft_TheSessionBoundaryDropsIt(t *testing.T) {
	var r draftRecorder
	m := declarationModel(t, 110, r.toolchain(draftedDeclaration()), startFixture())
	next, cmd := m.toolchainCommand()
	m = next.(Model)
	m.startNewSession()
	m = runDraftCmd(t, m, cmd)
	if m.state == stateToolchainDraft {
		t.Fatal("a drafting from the session left behind opened its card in the new one")
	}
}

// Typing the command reaches the same drafting.
func TestToolchainDraft_TypingTheCommandStartsIt(t *testing.T) {
	var r draftRecorder
	m := declarationModel(t, 110, r.toolchain(draftedDeclaration()), startFixture())
	m.input.SetValue(toolchainCommandName)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = runDraftCmd(t, updated.(Model), cmd)
	if m.state != stateToolchainDraft {
		t.Fatalf("typing %s did not reach the card (state %v)", toolchainCommandName, m.state)
	}
	_ = provider.RoleSystem
}
