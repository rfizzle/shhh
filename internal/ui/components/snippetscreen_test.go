package components

// The snippet browser (
// docs/interface/surfaces.md#the-supporting-screens). The assertions here are
// about what the screen resolves rather than what it draws: nothing runs
// until [enter], nothing is deleted until the confirm has been answered, and
// every other key hands the host a command and leaves the screen up.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func snippetRows() []SnippetRow {
	return []SnippetRow{
		{ID: "1", Name: "ports", Description: "what is listening on this machine",
			Command: "lsof -nP -iTCP -sTCP:LISTEN", Saved: "4m ago"},
		{ID: "2", Name: "big-files", Description: "the ten biggest files here",
			Command: "du -ah . | sort -rh | head -10", Saved: "yesterday"},
		{ID: "3", Name: "prune", Description: "drop every merged branch",
			Command: "git branch --merged | grep -v main | xargs git branch -d", Saved: "tue"},
	}
}

func snippetScreen() *SnippetScreen {
	return &SnippetScreen{Rows: snippetRows(), Subject: "3 snippets", MaxLines: 18}
}

func pressKey(s *SnippetScreen, r rune) (bool, SnippetResult) {
	return s.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
}

func typeIntoSnippets(s *SnippetScreen, text string) {
	for _, r := range text {
		pressKey(s, r)
	}
}

// Nothing on this screen runs by itself: the key that runs a snippet is the
// one that closes the screen, and the command travels with the result so the
// host can run it once the terminal is its own again.
func TestSnippetScreen_EnterRunsTheSnippetUnderThePointer(t *testing.T) {
	s := snippetScreen()
	s.Focus = 1
	done, result := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !done || !result.Run {
		t.Fatalf("enter did not close the screen with something to run: %+v", result)
	}
	if result.ID != "2" || result.Command != "du -ah . | sort -rh | head -10" {
		t.Fatalf("the wrong snippet was taken: %+v", result)
	}
	if view := ansi.Strip(snippetScreen().View(130)); !strings.Contains(view, "nothing is run until [enter]") {
		t.Fatalf("the footer does not say what enter is for:\n%s", view)
	}
}

// Every other act is a command the host carries out with the screen still up,
// so the reader stays on the list they came to.
func TestSnippetScreen_CopyResolvesWithoutClosing(t *testing.T) {
	s := snippetScreen()
	done, result := pressKey(s, 'c')
	if done || result.Do == nil || result.Do.Act != SnippetCopy || result.Do.ID != "1" {
		t.Fatalf("copy = %+v (done=%v), want a command against the first row", result.Do, done)
	}
}

// The one key that destroys something asks first, the question names what it
// would take, and enter is No.
func TestSnippetScreen_DeleteAsksFirst(t *testing.T) {
	s := snippetScreen()
	if _, result := pressKey(s, 'x'); result.Do != nil {
		t.Fatalf("x resolved a delete before the confirm: %+v", result.Do)
	}
	view := ansi.Strip(s.View(130))
	if !strings.Contains(view, `Delete the snippet "ports"?`) || !strings.Contains(view, "[y/N]") {
		t.Fatalf("the confirm does not name the snippet:\n%s", view)
	}
	if _, result := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Do != nil {
		t.Fatalf("enter is No, got %+v", result.Do)
	}

	pressKey(s, 'x')
	done, result := pressKey(s, 'y')
	if done || result.Do == nil || result.Do.Act != SnippetDelete || result.Do.ID != "1" {
		t.Fatalf("y should resolve the delete, got %+v (done=%v)", result.Do, done)
	}
}

// The rename row is opened holding the name that is there, commits on enter
// and keeps the name on esc — the reflex key never destroys what was typed
// over (docs/interface/principles.md#esc-is-always-the-safe-answer).
func TestSnippetScreen_RenameCommitsOnEnterAndKeepsOnEsc(t *testing.T) {
	s := snippetScreen()
	pressKey(s, 'r')
	if view := ansi.Strip(s.View(130)); !strings.Contains(view, "rename ▸ ports") {
		t.Fatalf("the rename row did not open prefilled:\n%s", view)
	}
	typeIntoSnippets(s, "2")
	if _, result := s.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); result.Do != nil {
		t.Fatalf("esc renamed something: %+v", result.Do)
	}

	pressKey(s, 'r')
	typeIntoSnippets(s, "2")
	done, result := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if done {
		t.Fatal("committing a rename closed the screen")
	}
	if result.Do == nil || result.Do.Act != SnippetRename || result.Do.Name != "ports2" {
		t.Fatalf("rename = %+v, want the row's new name", result.Do)
	}
}

// A name that was not changed asks the host for nothing: a store told to
// rename something to what it is already called is a write nobody made.
func TestSnippetScreen_UnchangedRenameResolvesNothing(t *testing.T) {
	s := snippetScreen()
	pressKey(s, 'r')
	if _, result := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Do != nil {
		t.Fatalf("an unchanged name resolved %+v", result.Do)
	}
}

// With the query line open the screen's letters are text, which is the
// reading every list in the product makes of its own row (invariant 5).
func TestSnippetScreen_LettersAreTextWhileFiltering(t *testing.T) {
	s := snippetScreen()
	pressKey(s, '/')
	if _, result := pressKey(s, 'x'); result.Do != nil {
		t.Fatalf("a letter typed into the filter deleted something: %+v", result.Do)
	}
	if s.list.Query != "x" {
		t.Fatalf("query = %q, want the letter as text", s.list.Query)
	}
}

// A snippet is found by its name, by what it is for, or by the command
// itself — and the header says the list is filtered, since the count under it
// is a count of what the query left.
func TestSnippetScreen_FilterMatchesTheCommandAndSaysSo(t *testing.T) {
	s := snippetScreen()
	pressKey(s, '/')
	typeIntoSnippets(s, "xargs")
	if len(s.shown) != 1 || s.Rows[s.shown[0]].Name != "prune" {
		t.Fatalf("the filter did not match the command: %v", s.shown)
	}
	head := ansi.Strip(strings.SplitN(s.View(130), "\n", 2)[0])
	if !strings.Contains(head, `filtered by "xargs"`) {
		t.Fatalf("the header does not state the filter: %q", head)
	}
	if !strings.Contains(ansi.Strip(s.View(130)), "hidden by the filter") {
		t.Fatal("the screen does not count what the filter hid")
	}
}

// The preview is the pointed snippet, and it carries the command in full —
// the command is the thing a snippet is.
func TestSnippetScreen_PreviewFollowsThePointer(t *testing.T) {
	s := snippetScreen()
	if view := ansi.Strip(s.View(130)); !strings.Contains(view, "lsof -nP -iTCP -sTCP:LISTEN") {
		t.Fatalf("the preview is not the first row's:\n%s", view)
	}
	pressKey(s, 'j')
	view := ansi.Strip(s.View(130))
	if !strings.Contains(view, "du -ah . | sort -rh | head -10") {
		t.Fatalf("the preview did not follow the pointer:\n%s", view)
	}
	if !strings.Contains(view, "the ten biggest files here") {
		t.Fatalf("the preview dropped what the snippet is for:\n%s", view)
	}
}

// `?` lists every key the screen answers, which is what makes the compact row
// a summary rather than the whole truth.
func TestSnippetScreen_KeyListIsComplete(t *testing.T) {
	s := snippetScreen()
	pressKey(s, '?')
	view := ansi.Strip(s.View(130))
	for _, key := range []string{"[↑↓/jk]", "[enter]", "[c]", "[r]", "[x]", "[/]", "[ctrl+u]", "[q]"} {
		if !strings.Contains(view, key) {
			t.Fatalf("the key list is missing %s:\n%s", key, view)
		}
	}
}

// Leaving runs nothing, whichever of the two ways out was taken.
func TestSnippetScreen_LeavingRunsNothing(t *testing.T) {
	for _, press := range []tea.KeyPressMsg{
		{Code: tea.KeyEscape},
		{Code: 'q', Text: "q"},
	} {
		s := snippetScreen()
		done, result := s.Update(press)
		if !done || !result.Canceled || result.Run {
			t.Fatalf("%v left with something to run: %+v", press, result)
		}
	}
}

// A screen over nothing still renders: the header, the rule and a pane that
// says there is nothing under the pointer.
func TestSnippetScreen_EmptyRenders(t *testing.T) {
	s := &SnippetScreen{Subject: "no snippets", MaxLines: 12}
	view := ansi.Strip(s.View(80))
	if !strings.Contains(view, "shhh snippets") || !strings.Contains(view, "no snippet selected") {
		t.Fatalf("an empty screen does not say so:\n%s", view)
	}
	if done, result := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); done || result.Run {
		t.Fatalf("enter over an empty list took something: %+v", result)
	}
}
