package chat

// The @ file mention: a menu over the palette's FILES source, opened from
// the draft, inserting a path rather than running anything.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
)

// mentionModel is a ready session whose file walk is a fixed list.
func mentionModel(t *testing.T) Model {
	t.Helper()
	m := readyModel(t)
	m.recentFiles = func() []project.RecentFile {
		return []project.RecentFile{
			{Path: "go.mod", Mod: time.Now().Add(-time.Minute)},
			{Path: "internal/ui/chat/model.go", Mod: time.Now().Add(-2 * time.Minute)},
			{Path: "README.md", Mod: time.Now().Add(-time.Hour)},
		}
	}
	return m
}

func TestMention_AtOpensTheMenuRanked(t *testing.T) {
	m := mentionModel(t)
	m.input.SetValue("@mod")
	m.syncCompletions()

	if !m.completionActive() || !m.complete.files {
		t.Fatal("@ should open the file-mention menu")
	}
	var names []string
	for _, c := range m.complete.items {
		names = append(names, c.name)
	}
	if len(names) != 2 || names[0] != "internal/ui/chat/model.go" || names[1] != "go.mod" {
		t.Fatalf("expected model.go (name prefix) then go.mod (name substring), got %v", names)
	}
}

func TestMention_TabInsertsThePath(t *testing.T) {
	m := mentionModel(t)
	m.input.SetValue("look at @mod")
	m.syncCompletions()
	if !m.completionActive() {
		t.Fatal("@ after whitespace should open the menu")
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next := updated.(Model)

	if got := next.input.Value(); got != "look at internal/ui/chat/model.go " {
		t.Fatalf("tab should replace the @token with the path, got %q", got)
	}
	if len(next.attachments) != 0 {
		t.Fatal("a mentioned source file must not be staged")
	}
}

func TestMention_EnterInsertsWithoutSending(t *testing.T) {
	m := mentionModel(t)
	m.input.SetValue("@mod")
	m.syncCompletions()

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	next := updated.(Model)

	if next.state != stateInput || len(next.transcript) != 0 {
		t.Fatal("enter on a mention row must insert, never send")
	}
	if got := next.input.Value(); got != "internal/ui/chat/model.go " {
		t.Fatalf("expected the path in the draft, got %q", got)
	}
}

func TestMention_MidWordAtIsALetter(t *testing.T) {
	m := mentionModel(t)
	m.input.SetValue("foo@bar")
	m.syncCompletions()
	if m.completionActive() {
		t.Fatal("an @ inside a word must not open the menu")
	}
}

func TestMention_SpaceClosesTheMenu(t *testing.T) {
	m := mentionModel(t)
	m.input.SetValue("@mod ")
	m.syncCompletions()
	if m.completionActive() {
		t.Fatal("a completed token must not keep the menu open")
	}
}

func TestMention_EscKeepsTheTypedText(t *testing.T) {
	m := mentionModel(t)
	m.input.SetValue("@mod")
	m.syncCompletions()

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	next := updated.(Model)

	if next.completionActive() {
		t.Fatal("esc should dismiss the menu")
	}
	if next.input.Value() != "@mod" {
		t.Fatalf("esc must keep the @text, got %q", next.input.Value())
	}
}

func TestMention_ImageIsStaged(t *testing.T) {
	dir := t.TempDir()
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, make([]byte, 64)...)
	if err := os.WriteFile(filepath.Join(dir, "shot.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	m := mentionModel(t).WithWorkspace(dir)
	m.recentFiles = func() []project.RecentFile {
		return []project.RecentFile{{Path: "shot.png", Mod: time.Now()}}
	}
	m.input.SetValue("@shot")
	m.syncCompletions()

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	next := updated.(Model)
	if cmd == nil {
		t.Fatal("expected the attach command for a mentioned image")
	}
	msg := cmdMsg(t, cmd)
	attached, ok := msg.(attachedFileMsg)
	if !ok {
		t.Fatalf("expected attachedFileMsg, got %#v", msg)
	}
	updated, _ = next.Update(attached)
	next = updated.(Model)

	if len(next.attachments) != 1 || next.attachments[0].Kind != provider.AttachmentImage {
		t.Fatalf("expected one staged image chip, got %v", next.attachments)
	}
	if !strings.Contains(next.input.Value(), "shot.png") {
		t.Fatalf("the path should still be in the sentence, got %q", next.input.Value())
	}
}

func TestMention_CursorBeforeTheAtClosesTheMenu(t *testing.T) {
	m := mentionModel(t)
	m.input.SetValue("@mod")
	m.syncCompletions()
	if !m.completionActive() {
		t.Fatal("the menu should be open at the token's end")
	}
	m.input.SetCursorColumn(0)
	m.syncCompletions()
	if m.completionActive() {
		t.Fatal("a cursor moved before the @ must close the menu")
	}
}

func TestMention_WalkRunsOncePerDraft(t *testing.T) {
	m := readyModel(t)
	walks := 0
	m.recentFiles = func() []project.RecentFile {
		walks++
		return []project.RecentFile{{Path: "go.mod", Mod: time.Now()}}
	}
	for _, val := range []string{"@z", "@zq", "@zqx", "@g", "@go"} {
		m.input.SetValue(val)
		m.syncCompletions()
	}
	if walks != 1 {
		t.Fatalf("the file walk must run once per @ draft, ran %d times", walks)
	}
	m.input.SetValue("plain text")
	m.syncCompletions()
	if m.complete.mentionCache != nil {
		t.Fatal("a draft that stopped being a mention should drop the cache")
	}
}
