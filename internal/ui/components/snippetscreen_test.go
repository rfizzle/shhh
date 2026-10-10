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
	"github.com/rfizzle/shhh/internal/ui/golden"
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
	return &SnippetScreen{Rows: snippetRows(), Subject: "3 snippets", maxLines: 18}
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
	if result.id != "2" || result.Command != "du -ah . | sort -rh | head -10" {
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
	if _, result := pressKey(s, 'd'); result.Do != nil {
		t.Fatalf("x resolved a delete before the confirm: %+v", result.Do)
	}
	view := ansi.Strip(s.View(130))
	if !strings.Contains(view, `Delete the snippet "ports"?`) || !strings.Contains(view, "[y/N]") {
		t.Fatalf("the confirm does not name the snippet:\n%s", view)
	}
	if _, result := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Do != nil {
		t.Fatalf("enter is No, got %+v", result.Do)
	}

	pressKey(s, 'd')
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
	pressKey(s, 'e')
	if view := ansi.Strip(s.View(130)); !strings.Contains(view, "rename ▸ ports") {
		t.Fatalf("the rename row did not open prefilled:\n%s", view)
	}
	typeIntoSnippets(s, "2")
	if _, result := s.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); result.Do != nil {
		t.Fatalf("esc renamed something: %+v", result.Do)
	}

	pressKey(s, 'e')
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
	pressKey(s, 'e')
	if _, result := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Do != nil {
		t.Fatalf("an unchanged name resolved %+v", result.Do)
	}
}

// With the query line open the screen's letters are text, which is the
// reading every list in the product makes of its own row (invariant 5).
func TestSnippetScreen_LettersAreTextWhileFiltering(t *testing.T) {
	s := snippetScreen()
	pressKey(s, '/')
	if _, result := pressKey(s, 'd'); result.Do != nil {
		t.Fatalf("a letter typed into the filter deleted something: %+v", result.Do)
	}
	if s.list.Query != "d" {
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
	for _, key := range []string{"[↑↓/jk]", "[enter]", "[c]", "[e]", "[d]", "[/]", "[esc]"} {
		if !strings.Contains(view, key) {
			t.Fatalf("the key list is missing %s:\n%s", key, view)
		}
	}
}

// Leaving runs nothing, and esc is the one way out: q is a plain letter.
func TestSnippetScreen_LeavingRunsNothing(t *testing.T) {
	s := snippetScreen()
	done, result := s.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !done || !result.canceled || result.Run {
		t.Fatalf("esc left with something to run: %+v", result)
	}
	s = snippetScreen()
	if done, result := s.Update(tea.KeyPressMsg{Code: 'q', Text: "q"}); done || result.Run {
		t.Fatalf("q left the screen: %+v (done=%v)", result, done)
	}
}

// A screen over nothing still renders: the header, the rule and a pane that
// says there is nothing under the pointer.
func TestSnippetScreen_EmptyRenders(t *testing.T) {
	s := &SnippetScreen{Subject: "no snippets", maxLines: 12}
	view := ansi.Strip(s.View(80))
	if !strings.Contains(view, "shhh snippets") || !strings.Contains(view, "no snippet selected") {
		t.Fatalf("an empty screen does not say so:\n%s", view)
	}
	if done, result := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); done || result.Run {
		t.Fatalf("enter over an empty list took something: %+v", result)
	}
}

// manySnippetRows are more snippets than a short screen holds, so the window
// draws its markers.
func manySnippetRows() []SnippetRow {
	rows := snippetRows()
	for _, name := range []string{"logs", "disk", "dns", "certs", "routes", "load", "mem", "temps", "users"} {
		rows = append(rows, SnippetRow{ID: name, Name: name, Description: "check the " + name,
			Command: "echo " + name, Saved: "last week"})
	}
	return rows
}

// TestGolden_SnippetScreen records every state the snippet browser draws: the
// list and its preview, the filter row open, a filter that hid some rows and
// one that hid them all, the rename row, the armed delete, the open register,
// a notice, a window too short for the list, and a store with nothing in it.
func TestGolden_SnippetScreen(t *testing.T) {
	keyed := func(s *SnippetScreen, text string) *SnippetScreen {
		typeIntoSnippets(s, text)
		return s
	}
	states := []struct {
		name, label string
		screen      func() *SnippetScreen
	}{
		{"snippet-listing", "the list, the pointer on the second snippet and its command beside it",
			func() *SnippetScreen { s := snippetScreen(); s.Focus = 1; return s }},
		{"snippet-filter-open", "the filter row opened, saying what it filters by",
			func() *SnippetScreen { return keyed(snippetScreen(), "/") }},
		{"snippet-filtered", "a query that left one snippet, and the line counting what it hid",
			func() *SnippetScreen { return keyed(snippetScreen(), "/xargs") }},
		{"snippet-filter-nothing", "a query that left nothing",
			func() *SnippetScreen { return keyed(snippetScreen(), "/zzz") }},
		{"snippet-rename", "the rename row under the panes, holding the name",
			func() *SnippetScreen { return keyed(snippetScreen(), "e") }},
		{"snippet-confirm", "the delete asked before it is carried out",
			func() *SnippetScreen { return keyed(snippetScreen(), "d") }},
		{"snippet-keys", "the whole register open",
			func() *SnippetScreen { return keyed(snippetScreen(), "?") }},
		{"snippet-notice", "the line the last key left",
			func() *SnippetScreen { s := snippetScreen(); s.Notice = "Copied ports to the clipboard."; return s }},
		{"snippet-windowed", "more snippets than the screen holds",
			func() *SnippetScreen {
				s := &SnippetScreen{Rows: manySnippetRows(), Subject: "12 snippets", maxLines: 12}
				s.Focus = 6
				return s
			}},
		{"snippet-empty", "a store with no snippets",
			func() *SnippetScreen { return &SnippetScreen{Subject: "no snippets", maxLines: 12} }},
	}
	for _, st := range states {
		captureGolden(t, st.name, "the snippet browser", goldenWidths, func(width int) []golden.Panel {
			return []golden.Panel{{Label: st.label, View: st.screen().View(width)}}
		})
	}
}
