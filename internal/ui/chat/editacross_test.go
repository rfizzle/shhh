package chat

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
)

// acrossFiles writes three files the session has read, and the call that
// renames old() to renamed() in each.
func acrossFiles(t *testing.T) (paths []string, call provider.ToolCall) {
	t.Helper()
	dir := t.TempDir()
	for i, body := range []string{"old()\n", "x\nold()\n", "old() y\n"} {
		p := filepath.Join(dir, []string{"a.go", "b.go", "c.go"}[i])
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]string{"path": p})
		if _, err := tools.Execute(context.Background(), tools.ReadFileName, raw); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	args, _ := json.Marshal(map[string]any{"paths": paths, "old_text": `old\(\)`, "new_text": "renamed()", "regex": true})
	return paths, provider.ToolCall{ID: "call_x", Name: tools.EditFileName, Arguments: string(args)}
}

// approve answers the card in front of m with y and lands what it ran.
func approve(t *testing.T, m Model) Model {
	t.Helper()
	m = handover(t, m)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(approvedToolDoneMsg); ok {
			updated, _ = m.Update(msg)
			m = updated.(Model)
		}
	}
	return m
}

// An edit across files is one card for the whole call: the act names the
// count, the rule rides the card as written with whether it is a pattern in
// words, each file is under its own row, and no grant is offered.
func TestEditAcrossFiles_IsOneCard(t *testing.T) {
	m := gatedModel(t, nil, nil)
	paths, call := acrossFiles(t)
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{call}})
	m = updated.(Model)
	req := m.approval.request
	if req == nil || req.kind != approvalDiff || len(req.files) != 3 || req.path != "" {
		t.Fatalf("request = %+v", req)
	}
	card := m.approvalCard()
	if card.Act != "edit 3 files" || len(card.Files) != 3 || card.AllowAlways || card.FullDiff {
		t.Errorf("card: act %q, %d files, always %v, full %v", card.Act, len(card.Files), card.AllowAlways, card.FullDiff)
	}
	// The body unbounded, so what is asserted is the card's content and not
	// how tall the test's terminal is.
	card.MaxLines = 0
	view := card.View(110)
	for _, want := range []string{`find      old\(\)`, "replace   renamed()", "match     regular expression", filepath.Base(paths[1]) + " · 1 hunk", "3 hunks in 3 files"} {
		if !strings.Contains(view, want) {
			t.Errorf("card lacks %q:\n%s", want, view)
		}
	}
	if offers, _ := m.grantOffers(req); offers != nil {
		t.Errorf("a grant was offered from an edit across files: %+v", offers)
	}
	a := m.approvalAction(req)
	if len(a.Paths) != 3 || a.Path != paths[0] {
		t.Errorf("the policy is asked about %+v", a)
	}
}

// Approved, each file is its own record in the turn, and taking one back
// leaves the other two as the edit left them.
func TestEditAcrossFiles_RecordsAndUndoesEachFileOnItsOwn(t *testing.T) {
	m := gatedModel(t, nil, nil)
	m.turnCount = 1
	paths, call := acrossFiles(t)
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{call}})
	m = approve(t, updated.(Model))
	turn, ok := m.changes.Turn(1)
	if !ok || turn.Files() != 3 {
		t.Fatalf("records = %+v", turn)
	}
	for _, p := range paths {
		r, ok := turn.Record(p)
		if !ok || strings.Contains(r.After, "old()") || !strings.Contains(r.After, "renamed()") || !strings.Contains(r.Before, "old()") {
			t.Errorf("%s record = %+v", p, r)
		}
	}
	out := changeset.PlanUndo(turn, []string{paths[1]}).Apply(false)
	if len(out.Records) != 1 || len(out.Failed) != 0 {
		t.Fatalf("undo of one file = %+v", out)
	}
	for i, p := range paths {
		data, _ := os.ReadFile(p)
		if restored := strings.Contains(string(data), "old()"); restored != (i == 1) {
			t.Errorf("%s after undoing b.go alone: %q", p, data)
		}
	}
}

// A write that fails part-way: the file before it is changed and recorded,
// the failed file and the one after it are neither, and the model reads
// which is which.
func TestEditAcrossFiles_AFailureMidListRecordsWhatLanded(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes a read-only file")
	}
	m := gatedModel(t, nil, nil)
	m.turnCount = 1
	paths, call := acrossFiles(t)
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{call}})
	m = updated.(Model)
	if err := os.Chmod(paths[1], 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(paths[1], 0o644) })
	m = approve(t, m)
	turn, _ := m.changes.Turn(1)
	if turn.Files() != 1 {
		t.Fatalf("recorded %d files, want the one that landed", turn.Files())
	}
	if _, ok := turn.Record(paths[0]); !ok {
		t.Error("the file written before the failure is not recorded")
	}
	last := m.Messages()[len(m.Messages())-1]
	for _, want := range []string{"Already edited and kept: " + paths[0], "not reached: " + paths[2]} {
		if !strings.Contains(last.Content, want) {
			t.Errorf("result lacks %q: %q", want, last.Content)
		}
	}
}
