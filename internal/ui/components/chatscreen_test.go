package components

// The saved-chat browser (
// docs/interface/surfaces.md#the-supporting-screens,
// docs/capabilities/sessions-and-memory.md#housekeeping). The assertions here
// are about what the screen resolves: which conversation [enter] opens, what
// a row the host refused does instead, and that neither housekeeping key
// reaches the store before it has been answered for.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func chatRows() []ChatRow {
	return []ChatRow{
		{ID: "morning-fix", Name: "morning-fix", Title: "the retry backoff was doubling twice",
			Turns: "12 turns", When: "Sep 6 09:14", Updated: "2026-09-06 09:14:02",
			Deleting: "and its 2 branches"},
		{ID: "held", Name: "held", Title: "reading the sandbox policy",
			Turns: "4 turns", When: "Sep 6 08:02", Updated: "2026-09-06 08:02:41",
			Mark:    "open in another session",
			Refused: `"held" is open in another session — its conversation is still being written there.`},
		{ID: "tuesday", Name: "tuesday", Title: "naming the report pages",
			Turns: "31 turns", When: "Sep 3 17:40", Updated: "2026-09-03 17:40:11"},
	}
}

func chatScreen() *ChatScreen {
	return &ChatScreen{Rows: chatRows(), Subject: "3 conversations", MaxLines: 18}
}

func pressChat(c *ChatScreen, r rune) (bool, ChatResult) {
	return c.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
}

func typeIntoChats(c *ChatScreen, text string) {
	for _, r := range text {
		pressChat(c, r)
	}
}

// The one key that leaves the screen hands the host the slot it chose.
func TestChatScreen_EnterOpensTheOneUnderThePointer(t *testing.T) {
	c := chatScreen()
	c.Focus = 2
	done, result := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !done || !result.Open || result.ID != "tuesday" {
		t.Fatalf("enter = %+v (done=%v), want the pointed conversation", result, done)
	}
}

// A row another session holds says so where it stands rather than closing the
// browser: the reader is on a row they can still rename or delete, and leaving
// to report the refusal would take that row off the screen along with every
// other one (docs/interface/principles.md#fold-never-hide).
func TestChatScreen_ARefusedRowSaysSoAndKeepsTheList(t *testing.T) {
	c := chatScreen()
	c.Focus = 1
	done, result := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if done || result.Open {
		t.Fatalf("a refused row closed the browser: %+v (done=%v)", result, done)
	}
	view := ansi.Strip(c.View(130))
	if !strings.Contains(view, "still being written there") {
		t.Fatalf("the refusal is not on the pane the key was pressed on:\n%s", view)
	}
	// The mark is a word, not a colour, and it is on the row as well as in
	// the sentence (invariant 1).
	if !strings.Contains(view, "open in another session") {
		t.Fatalf("the row does not carry the mark:\n%s", view)
	}
	// The row is still a row: the housekeeping keys reach it.
	if _, result := pressChat(c, 'r'); result.Do != nil || c.rename == nil {
		t.Fatalf("a refused row cannot be renamed: %+v", result.Do)
	}
}

// The key that would open a row the host refused is not offered on it: a key
// that cannot act is not an offer (invariant 5).
func TestChatScreen_ARefusedRowOffersNoOpen(t *testing.T) {
	c := chatScreen()
	if view := ansi.Strip(c.View(130)); !strings.Contains(view, "[enter] open it") {
		t.Fatalf("an ordinary row does not offer to open:\n%s", view)
	}
	c.Focus = 1
	if view := ansi.Strip(c.View(130)); strings.Contains(view, "[enter] open it") {
		t.Fatalf("a refused row offers a key it will not answer:\n%s", view)
	}
}

// The one key that destroys something asks first, and the question names the
// branches that would go with the conversation.
func TestChatScreen_DeleteAsksAndNamesTheBranches(t *testing.T) {
	c := chatScreen()
	if _, result := pressChat(c, 'x'); result.Do != nil {
		t.Fatalf("x resolved a delete before the confirm: %+v", result.Do)
	}
	view := ansi.Strip(c.View(130))
	if !strings.Contains(view, `Delete "morning-fix" and its 2 branches?`) {
		t.Fatalf("the confirm does not name what goes with it:\n%s", view)
	}
	if _, result := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Do != nil {
		t.Fatalf("enter is No, got %+v", result.Do)
	}
	pressChat(c, 'x')
	done, result := pressChat(c, 'y')
	if done || result.Do == nil || result.Do.Act != ChatDelete || result.Do.ID != "morning-fix" {
		t.Fatalf("y should resolve the delete, got %+v (done=%v)", result.Do, done)
	}
}

// The rename row opens holding the name that is there, commits on enter and
// keeps the name on esc (docs/interface/principles.md#esc-is-always-the-safe-answer).
func TestChatScreen_RenameCommitsOnEnterAndKeepsOnEsc(t *testing.T) {
	c := chatScreen()
	pressChat(c, 'r')
	if view := ansi.Strip(c.View(130)); !strings.Contains(view, "rename ▸ morning-fix") {
		t.Fatalf("the rename row did not open prefilled:\n%s", view)
	}
	typeIntoChats(c, "-2")
	if _, result := c.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); result.Do != nil {
		t.Fatalf("esc renamed something: %+v", result.Do)
	}

	pressChat(c, 'r')
	typeIntoChats(c, "-2")
	done, result := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if done {
		t.Fatal("committing a rename closed the browser")
	}
	if result.Do == nil || result.Do.Act != ChatRename || result.Do.Name != "morning-fix-2" {
		t.Fatalf("rename = %+v, want the row's new name", result.Do)
	}
}

// With the query line open the screen's letters are text (invariant 5), and a
// conversation is found by its name or by what it was about.
func TestChatScreen_FilterMatchesTheTitleAndTakesTheLetters(t *testing.T) {
	c := chatScreen()
	pressChat(c, '/')
	typeIntoChats(c, "x")
	if c.confirm != nil {
		t.Fatal("a letter typed into the filter armed the delete confirm")
	}
	c.list.Query = ""
	typeIntoChats(c, "backoff")
	if len(c.shown) != 1 || c.Rows[c.shown[0]].ID != "morning-fix" {
		t.Fatalf("the filter did not match the title: %v", c.shown)
	}
	if head := ansi.Strip(strings.SplitN(c.View(130), "\n", 2)[0]); !strings.Contains(head, `filtered by "backoff"`) {
		t.Fatalf("the header does not state the filter: %q", head)
	}
}

// `?` lists every key the screen answers.
func TestChatScreen_KeyListIsComplete(t *testing.T) {
	c := chatScreen()
	pressChat(c, '?')
	view := ansi.Strip(c.View(130))
	for _, key := range []string{"[↑↓/jk]", "[enter]", "[r]", "[x]", "[/]", "[ctrl+u]", "[q]"} {
		if !strings.Contains(view, key) {
			t.Fatalf("the key list is missing %s:\n%s", key, view)
		}
	}
}

// Leaving opens nothing, whichever of the two ways out was taken.
func TestChatScreen_LeavingOpensNothing(t *testing.T) {
	for _, press := range []tea.KeyPressMsg{
		{Code: tea.KeyEscape},
		{Code: 'q', Text: "q"},
	} {
		c := chatScreen()
		done, result := c.Update(press)
		if !done || !result.Canceled || result.Open {
			t.Fatalf("%v left with something open: %+v", press, result)
		}
	}
}

// A screen over nothing still renders, and enter over it takes nothing.
func TestChatScreen_EmptyRenders(t *testing.T) {
	c := &ChatScreen{Subject: "no conversations", MaxLines: 12}
	view := ansi.Strip(c.View(80))
	if !strings.Contains(view, "shhh chats") || !strings.Contains(view, "no conversation selected") {
		t.Fatalf("an empty screen does not say so:\n%s", view)
	}
	if done, result := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); done || result.Open {
		t.Fatalf("enter over an empty list took something: %+v", result)
	}
}
