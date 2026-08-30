package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
)

// titleProvider answers every title request with a scripted title or an
// error, and counts how many were asked for.
type titleProvider struct {
	title string
	err   error
	calls int
}

func (p *titleProvider) StreamCompletion(ctx context.Context, msgs []provider.Message, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{
		ToolCalls: []provider.ToolCall{{ID: "t1", Name: agent.TitleToolName, Arguments: `{"title":"` + p.title + `"}`}},
		Usage:     &provider.Usage{PromptTokens: 200, CompletionTokens: 6},
		Done:      true,
	}
	close(ch)
	return ch, nil
}

func (p *titleProvider) Name() string { return "titles" }

// titledModel is a session over a store with a titler on the given provider.
func titledModel(t *testing.T, p provider.Provider) (Model, *storage.DB) {
	t.Helper()
	db := rewindTestDB(t)
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, multiTokenStream("hi there")).WithDB(db).
		WithTitler(agent.NewTitler(p, agent.TitleConfig{Model: "fast"}), true)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	return updated.(Model), db
}

// closeTurn runs one exchange to its close and returns the model and the
// commands the close produced — the autosave, and the title reading if one
// was asked for.
func closeTurn(t *testing.T, m Model, text string) (Model, tea.Cmd) {
	t.Helper()
	m = sendText(t, m, text)
	updated, _ := m.Update(tokenMsg{text: "hi there"})
	m = updated.(Model)
	updated, cmd := m.Update(doneMsg{})
	return updated.(Model), cmd
}

// driveTitle runs the close's commands and returns the title reading among
// them, or false when none was asked for.
func driveTitle(t *testing.T, cmd tea.Cmd) (titleDoneMsg, bool) {
	t.Helper()
	if cmd == nil {
		return titleDoneMsg{}, false
	}
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(titleDoneMsg); ok {
			return msg, true
		}
	}
	return titleDoneMsg{}, false
}

func TestTitle_FirstTurnTitlesTheSlot(t *testing.T) {
	p := &titleProvider{title: "Greeting the tests"}
	m, db := titledModel(t, p)

	m, cmd := closeTurn(t, m, "hello")
	msg, ok := driveTitle(t, cmd)
	if !ok {
		t.Fatal("the first turn's close should ask for a title")
	}
	if !m.titles.inFlight || m.titles.attempts != 1 {
		t.Fatalf("one reading should be out, got %+v", m.titles)
	}
	updated, write := m.Update(msg)
	m = updated.(Model)
	if write == nil {
		t.Fatal("a good reading should write the slot")
	}
	write()
	if m.titles.title != "Greeting the tests" {
		t.Fatalf("the model should keep the title, got %q", m.titles.title)
	}
	if got, _ := db.ChatTitle(m.sessionName); got != "Greeting the tests" {
		t.Fatalf("the slot should carry the title, got %q", got)
	}

	// The next turn asks for nothing: the slot is titled.
	_, cmd = closeTurn(t, m, "again")
	if _, ok := driveTitle(t, cmd); ok || p.calls != 1 {
		t.Fatalf("a titled slot is not asked again, calls=%d", p.calls)
	}
}

func TestTitle_FailureLeavesTheRowAndRetriesOnce(t *testing.T) {
	p := &titleProvider{err: errors.New("down")}
	m, db := titledModel(t, p)

	m, cmd := closeTurn(t, m, "hello")
	msg, ok := driveTitle(t, cmd)
	if !ok {
		t.Fatal("the first close should ask")
	}
	updated, write := m.Update(msg)
	m = updated.(Model)
	if write != nil {
		t.Fatal("a failed reading writes nothing")
	}
	if got, _ := db.ChatTitle(m.sessionName); got != "" || m.titles.title != "" {
		t.Fatalf("a failed reading leaves the row untitled, got %q", got)
	}

	m, cmd = closeTurn(t, m, "second")
	msg, ok = driveTitle(t, cmd)
	if !ok || p.calls != 2 {
		t.Fatalf("exactly one retry after the next turn, calls=%d", p.calls)
	}
	updated, _ = m.Update(msg)
	m = updated.(Model)

	_, cmd = closeTurn(t, m, "third")
	if _, ok := driveTitle(t, cmd); ok || p.calls != 2 {
		t.Fatalf("no third attempt, calls=%d", p.calls)
	}
}

func TestTitle_NamedChatIsNotAsked(t *testing.T) {
	p := &titleProvider{title: "Nope"}
	m, _ := titledModel(t, p)
	m.sessionName = "my notes"

	_, cmd := closeTurn(t, m, "hello")
	if _, ok := driveTitle(t, cmd); ok || p.calls != 0 {
		t.Fatalf("a chat the user named is never titled, calls=%d", p.calls)
	}
}

func TestTitle_SaveCarriesTheTitleAndStopsAsking(t *testing.T) {
	p := &titleProvider{title: "Greeting the tests"}
	m, db := titledModel(t, p)
	m, cmd := closeTurn(t, m, "hello")
	msg, _ := driveTitle(t, cmd)
	updated, write := m.Update(msg)
	m = updated.(Model)
	write()

	m = sendText(t, m, "/save kept")
	if m.sessionName != "kept" {
		t.Fatalf("/save should move the session, got %q", m.sessionName)
	}
	if got, _ := db.ChatTitle("kept"); got != "Greeting the tests" {
		t.Fatalf("the title should travel into the named slot, got %q", got)
	}
	entries, _ := db.ListChats()
	for _, e := range entries {
		if e.Name == "kept" && chatDesc(e) != "Greeting the tests · "+sessionDesc(e.Turns, e.UpdatedAt) {
			t.Fatalf("the listing shows the name first and the title beside it, got %q", chatDesc(e))
		}
	}
}

func TestTitle_AutosaveCarriesTheTitleAndLoadReadsItBack(t *testing.T) {
	p := &titleProvider{title: "Greeting the tests"}
	m, db := titledModel(t, p)
	m, cmd := closeTurn(t, m, "hello")
	msg, _ := driveTitle(t, cmd)
	updated, _ := m.Update(msg)
	m = updated.(Model)
	// The reading's own write is skipped: the autosave alone has to carry
	// the title, since a slot the store has not seen yet cannot take it.
	m.sessionName = "2026-01-01 00:00:00"
	m.autosaveCmd()()
	if got, _ := db.ChatTitle("2026-01-01 00:00:00"); got != "Greeting the tests" {
		t.Fatalf("the autosave should stamp the title on the slot, got %q", got)
	}

	other := m
	other.resetTitle()
	other.loadChatByName("2026-01-01 00:00:00")
	if other.titles.title != "Greeting the tests" {
		t.Fatalf("loading a chat should read its title back, got %q", other.titles.title)
	}
	if b := other.ExitBanner("shhh chat --continue"); b.Title != "Greeting the tests" {
		t.Fatalf("the exit banner should carry it, got %+v", b)
	}
}

func TestTitle_SaveWhileAReadingIsOutLandsOnTheNewName(t *testing.T) {
	p := &titleProvider{title: "Greeting the tests"}
	m, db := titledModel(t, p)
	m, cmd := closeTurn(t, m, "hello")
	msg, _ := driveTitle(t, cmd)
	m = sendText(t, m, "/save kept")
	updated, write := m.Update(msg)
	m = updated.(Model)
	if write == nil || m.titles.title != "Greeting the tests" {
		t.Fatalf("the reading is this conversation's, got %+v", m.titles)
	}
	write()
	if got, _ := db.ChatTitle("kept"); got != "Greeting the tests" {
		t.Fatalf("the named slot should receive the title, got %q", got)
	}
}

func TestTitle_ClearDropsAReadingInFlight(t *testing.T) {
	p := &titleProvider{title: "Stale"}
	m, _ := titledModel(t, p)
	m, cmd := closeTurn(t, m, "hello")
	msg, _ := driveTitle(t, cmd)
	m = sendText(t, m, "/clear")
	if m.titles.inFlight || m.titles.readFor != "" {
		t.Fatalf("/clear should forget the reading, got %+v", m.titles)
	}
	updated, write := m.Update(msg)
	m = updated.(Model)
	if write != nil || m.titles.title != "" {
		t.Fatalf("a reading for the old slot must not title the new one, got %+v", m.titles)
	}
}

func TestTitle_UICommandTogglesAndReports(t *testing.T) {
	p := &titleProvider{title: "Nope"}
	m, _ := titledModel(t, p)

	if out := m.uiCommand([]string{"/ui", "title"}); !strings.Contains(out, "Session titles: on (fast)") {
		t.Fatalf("the readout should say on and with what, got %q", out)
	}
	if out := m.uiCommand([]string{"/ui", "title", "off"}); !strings.Contains(out, "off") {
		t.Fatalf("off should be acknowledged, got %q", out)
	}
	_, cmd := closeTurn(t, m, "hello")
	if _, ok := driveTitle(t, cmd); ok || p.calls != 0 {
		t.Fatalf("off means no request, calls=%d", p.calls)
	}
	if out := m.uiCommand([]string{"/ui"}); !strings.Contains(out, "Session titles: off") {
		t.Fatalf("the bare /ui should name the setting, got %q", out)
	}
	if out := m.uiCommand([]string{"/ui", "title", "sideways"}); !strings.Contains(out, "Error") {
		t.Fatalf("an unknown value is an error, got %q", out)
	}

	unconfigured := m.WithTitler(agent.NewTitler(p, agent.TitleConfig{}), false)
	if out := unconfigured.uiCommand([]string{"/ui", "title"}); !strings.Contains(out, "no summary model is configured") {
		t.Fatalf("off for want of a model should say so, got %q", out)
	}
}

func TestIsAutosaveSlot(t *testing.T) {
	cases := map[string]bool{
		"2026-08-31 14:02:11":     true,
		"2026-08-31 14:02:11 (2)": true,
		"release notes":           false,
		"2026-08-31 14:02:11 x":   false,
		"":                        false,
	}
	for name, want := range cases {
		if got := isAutosaveSlot(name); got != want {
			t.Errorf("isAutosaveSlot(%q) = %v, want %v", name, got, want)
		}
	}
}
