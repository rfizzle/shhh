package chat

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
)

// accountProvider answers every account request with the next scripted
// account, and keeps the evidence it was handed.
type accountProvider struct {
	answers []string
	asked   []string
}

func (p *accountProvider) Name() string { return "accounts" }

func (p *accountProvider) StreamCompletion(_ context.Context, msgs []provider.Message, _ provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	p.asked = append(p.asked, msgs[len(msgs)-1].Content)
	answer := p.answers[min(len(p.asked), len(p.answers))-1]
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{
		ToolCalls: []provider.ToolCall{{ID: "a1", Name: agent.AccountToolName, Arguments: `{"account":"` + answer + `"}`}},
		Done:      true,
	}
	close(ch)
	return ch, nil
}

// accountedModel is a session over a store whose standing account is revised
// every `every` turns.
func accountedModel(t *testing.T, p provider.Provider, every int) (Model, *storage.DB) {
	t.Helper()
	db := rewindTestDB(t)
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, multiTokenStream("hi there"), Wiring{
		DB:           db,
		Accountant:   agent.NewAccountant(p, agent.AccountConfig{Model: "fast"}),
		AccountEvery: every,
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	return updated.(Model), db
}

// driveAccount runs a close's commands and returns the account reading among
// them, or false when none was asked for.
func driveAccount(t *testing.T, cmd tea.Cmd) (accountDoneMsg, bool) {
	t.Helper()
	if cmd == nil {
		return accountDoneMsg{}, false
	}
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(accountDoneMsg); ok {
			return msg, true
		}
	}
	return accountDoneMsg{}, false
}

// The account is asked no more often than every interval's worth of closed
// turns, is written to the slot's summary column, and the next reading
// revises it rather than starting again.
func TestAccount_EveryIntervalRevisesTheSlotsAccount(t *testing.T) {
	p := &accountProvider{answers: []string{"Greeting the tests.", "Greeting the tests. Left off saying goodbye."}}
	m, db := accountedModel(t, p, 2)

	m, cmd := closeTurn(t, m, "hello")
	if _, ok := driveAccount(t, cmd); ok {
		t.Fatal("one closed turn is under the interval and asked anyway")
	}
	m, cmd = closeTurn(t, m, "again")
	msg, ok := driveAccount(t, cmd)
	if !ok {
		t.Fatal("the interval's second turn should ask for the account")
	}
	updated, write := m.Update(msg)
	m = updated.(Model)
	if write == nil {
		t.Fatal("a good reading should write the slot")
	}
	write()
	if m.compactSummary != "Greeting the tests." {
		t.Fatalf("the session should carry the account, got %q", m.compactSummary)
	}
	if got, _ := db.ChatResume(m.sessionName); got.Summary != "Greeting the tests." {
		t.Fatalf("the slot should carry the account, got %q", got.Summary)
	}

	m, _ = closeTurn(t, m, "third")
	m, cmd = closeTurn(t, m, "goodbye")
	msg, ok = driveAccount(t, cmd)
	if !ok {
		t.Fatal("the next interval should ask again")
	}
	if !strings.Contains(p.asked[1], "Greeting the tests.") || !strings.Contains(p.asked[1], "goodbye") {
		t.Fatalf("the second reading should revise the first over the newer turns: %s", p.asked[1])
	}
	updated, _ = m.Update(msg)
	if got := updated.(Model).compactSummary; got != "Greeting the tests. Left off saying goodbye." {
		t.Fatalf("the revision did not stand: %q", got)
	}
}

// Whichever was written later stands: a reading that lands after a
// compaction replaced the account it revised is dropped, not written over
// the newer handoff.
func TestAccount_AReadingOlderThanACompactionIsDropped(t *testing.T) {
	p := &accountProvider{answers: []string{"Stale view."}}
	m, _ := accountedModel(t, p, 1)
	m, cmd := closeTurn(t, m, "hello")
	msg, ok := driveAccount(t, cmd)
	if !ok {
		t.Fatal("the close should ask")
	}
	m.compactSummary = "the handoff a compaction wrote"
	updated, write := m.Update(msg)
	if write != nil || updated.(Model).compactSummary != "the handoff a compaction wrote" {
		t.Fatalf("a stale reading replaced the newer handoff: %q", updated.(Model).compactSummary)
	}
	if updated.(Model).account.inFlight {
		t.Fatal("the dropped reading left the account marked in flight")
	}
}

// Quitting with a turn closed since the last reading asks once more, and the
// save on the way out carries the answer; quitting with none owed asks
// nothing.
func TestAccount_QuitCarriesTheClosingAccount(t *testing.T) {
	p := &accountProvider{answers: []string{"Said hello. Left off before quitting."}}
	m, db := accountedModel(t, p, 5)
	m, _ = closeTurn(t, m, "hello")

	for _, c := range sequenceCmds(m.quitCmd()) {
		if _, quit := c().(tea.QuitMsg); quit {
			break
		}
	}
	if len(p.asked) != 1 {
		t.Fatalf("quitting with a turn owed should ask once, asked %d", len(p.asked))
	}
	if got, _ := db.ChatResume(m.sessionName); got.Summary != "Said hello. Left off before quitting." {
		t.Fatalf("the save on the way out should carry the account, got %q", got.Summary)
	}

	fresh, _ := accountedModel(t, p, 5)
	if fresh.closingAccount() != nil {
		t.Fatal("a session with no closed turn owes no account")
	}
}

// sequenceCmds is the commands a tea.Sequence runs, in order. The message it
// carries is unexported, so it is read as the slice of commands it is.
func sequenceCmds(cmd tea.Cmd) []tea.Cmd {
	msg := cmd()
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice {
		return []tea.Cmd{func() tea.Msg { return msg }}
	}
	out := make([]tea.Cmd, 0, v.Len())
	for i := range v.Len() {
		out = append(out, v.Index(i).Interface().(tea.Cmd))
	}
	return out
}
