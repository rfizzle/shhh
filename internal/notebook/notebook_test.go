package notebook

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type fakeBackend struct {
	saved   map[string][]Note
	nextID  int64
	deleted []int64
}

func (f *fakeBackend) SaveNote(session string, n Note) (int64, error) {
	if f.saved == nil {
		f.saved = map[string][]Note{}
	}
	f.nextID++
	n.ID = f.nextID
	f.saved[session] = append(f.saved[session], n)
	return n.ID, nil
}

func (f *fakeBackend) LoadNotes(session string) ([]Note, error) { return f.saved[session], nil }

func (f *fakeBackend) DeleteNote(session string, id int64) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func TestWriteReadAndFind(t *testing.T) {
	s := New(nil)
	if _, _, err := s.Write("assistant", "", "body"); err == nil {
		t.Fatal("empty title accepted")
	}
	if _, _, err := s.Write("assistant", "t", strings.Repeat("x", MaxBodyLen+1)); err == nil {
		t.Fatal("oversized body accepted")
	}
	n, dropped, err := s.Write("assistant", "Pricing source", "The table is at docs/pricing.md")
	if err != nil || dropped != "" {
		t.Fatalf("write: %v dropped=%q", err, dropped)
	}
	if n.ID != 1 || n.Author != "assistant" {
		t.Fatalf("note = %+v", n)
	}
	_, _, _ = s.Write("researcher-1", "Latency", "p99 is 400ms per the dashboard")
	if got := s.Find("pricing"); len(got) != 1 || got[0].Title != "Pricing source" {
		t.Fatalf("find pricing = %+v", got)
	}
	if got := s.Find("400ms researcher"); len(got) != 1 {
		t.Fatalf("multi-word find = %+v", got)
	}
	if got := s.Find(""); len(got) != 2 {
		t.Fatalf("empty query lists all, got %d", len(got))
	}
	if !strings.Contains(Format(s.List()), "[n2] Latency — researcher-1") {
		t.Fatalf("format: %q", Format(s.List()))
	}
	block := PromptBlock(s.List())
	if !strings.Contains(block, "- [n1] Pricing source (assistant)") {
		t.Fatalf("prompt block: %q", block)
	}
	// An empty notebook still gets a block: a child that is not told the
	// notebook exists cannot read it before it re-finds something.
	if empty := PromptBlock(nil); !strings.Contains(empty, "It is empty so far.") {
		t.Fatalf("empty notebook block: %q", empty)
	}
}

func TestBindLoadsAndWritesThrough(t *testing.T) {
	b := &fakeBackend{}
	_, _ = b.SaveNote("slot", Note{Author: "assistant", Title: "Earlier", Body: "left on Monday"})

	s := New(b)
	// Written before the slot is known: kept in memory, then written through.
	_, _, _ = s.Write("assistant", "Pending", "before bind")
	if err := s.Bind("slot"); err != nil {
		t.Fatal(err)
	}
	notes := s.List()
	if len(notes) != 2 || notes[0].Title != "Earlier" || notes[1].Title != "Pending" {
		t.Fatalf("after bind = %+v", notes)
	}
	if len(b.saved["slot"]) != 2 {
		t.Fatalf("pending note not written through: %+v", b.saved["slot"])
	}
	_, _, _ = s.Write("researcher-1", "Later", "after bind")
	if len(b.saved["slot"]) != 3 {
		t.Fatal("note after bind not persisted")
	}
	if err := s.Delete(notes[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(b.deleted) != 1 || s.Len() != 2 {
		t.Fatalf("delete: backend=%v len=%d", b.deleted, s.Len())
	}
	// Rebinding to the same slot is a no-op.
	if err := s.Bind("slot"); err != nil || s.Len() != 2 {
		t.Fatalf("rebind: %v len=%d", err, s.Len())
	}
}

func TestCapDropsOldest(t *testing.T) {
	s := New(nil)
	for i := 0; i < MaxNotes; i++ {
		_, _, _ = s.Write("a", "n", "b")
	}
	_, dropped, err := s.Write("a", "last", "b")
	if err != nil || dropped != "n" || s.Len() != MaxNotes {
		t.Fatalf("cap: dropped=%q err=%v len=%d", dropped, err, s.Len())
	}
}

func TestToolsRouteAndSign(t *testing.T) {
	s := New(nil)
	next := func(name string, _ json.RawMessage) (string, error) { return "passed:" + name, nil }
	child := s.WrapExecutor("researcher-2", next)
	out, err := child(WriteToolName, json.RawMessage(`{"title":"Found it","body":"see README"}`))
	if err != nil || !strings.Contains(out, "[n1]") {
		t.Fatalf("write via tool: %q %v", out, err)
	}
	if s.List()[0].Author != "researcher-2" {
		t.Fatal("note not signed by the wrapping agent")
	}
	out, _ = child(ReadToolName, nil)
	if !strings.Contains(out, "Found it") {
		t.Fatalf("read all: %q", out)
	}
	out, _ = child(ReadToolName, json.RawMessage(`{"id":1}`))
	if !strings.Contains(out, "see README") {
		t.Fatalf("read by id: %q", out)
	}
	out, _ = child(ReadToolName, json.RawMessage(`{"query":"nothing here"}`))
	if !strings.Contains(out, "No note matches") {
		t.Fatalf("miss: %q", out)
	}
	if _, err := child(ReadToolName, json.RawMessage(`{"id":9}`)); err == nil {
		t.Fatal("missing id did not error")
	}
	if out, _ := child("read_file", nil); out != "passed:read_file" {
		t.Fatalf("passthrough: %q", out)
	}
}

// A vaulted value never reaches the backend. The store is what outlives the
// turn, so the scrub is installed on it rather than wrapped around it: what
// the writer is told, what /notes prints and what the backend kept are one
// text.
func TestScrubRunsBeforeTheNoteIsKept(t *testing.T) {
	b := &fakeBackend{}
	s := New(b)
	s.SetScrub(func(in string) string { return strings.ReplaceAll(in, "hunter2", "${API_KEY}") })
	if err := s.Bind("slot"); err != nil {
		t.Fatal(err)
	}
	n, _, err := s.Write("researcher-1", "The key is hunter2", "curl -H 'Bearer hunter2' …")
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range []string{n.Title, n.Body, Format(s.List()), FormatByAuthor(s.List())} {
		if strings.Contains(got, "hunter2") {
			t.Errorf("the value survived in %q", got)
		}
	}
	kept := b.saved["slot"]
	if len(kept) != 1 {
		t.Fatalf("backend kept %d notes", len(kept))
	}
	if strings.Contains(kept[0].Title+kept[0].Body, "hunter2") {
		t.Errorf("the value reached the backend: %+v", kept[0])
	}
	if !strings.Contains(kept[0].Body, "${API_KEY}") {
		t.Errorf("the placeholder did not reach the backend: %+v", kept[0])
	}
}

// A note carries the turn it was written in, and a store nobody told stamps
// zero rather than turn one.
func TestNotesCarryTheirTurn(t *testing.T) {
	s := New(nil)
	before, _, _ := s.Write("assistant", "Untold", "written before a turn was named")
	if before.Turn != 0 {
		t.Errorf("a note written before any turn was named = turn %d", before.Turn)
	}
	s.SetTurn(7)
	_, _, _ = s.Write("reviewer", "One", "a")
	_, _, _ = s.Write("reviewer", "Two", "b")
	_, _, _ = s.Write(Orchestrator, "Mine", "c")
	s.SetTurn(8)
	_, _, _ = s.Write("researcher-1", "Later", "d")

	got := WrittenIn(s.List(), 7, Orchestrator)
	if len(got) != 2 || got[0].Title != "One" || got[1].Title != "Two" {
		t.Fatalf("turn 7's delegate notes = %+v", got)
	}
	if n := WrittenIn(s.List(), 0, Orchestrator); n != nil {
		t.Errorf("turn zero claimed %d notes", len(n))
	}
}

// The two tools are the whole vocabulary: an agent adds and reads, and no
// argument to either of them removes anything. Deleting is the person's.
func TestNoAgentCanDelete(t *testing.T) {
	names := map[string]bool{}
	for _, d := range Definitions() {
		names[d.Name] = true
	}
	if len(names) != 2 || !names[WriteToolName] || !names[ReadToolName] {
		t.Fatalf("the notebook offers %v", names)
	}
	s := New(nil)
	_, _, _ = s.Write("assistant", "Kept", "the fact")
	next := func(string, json.RawMessage) (string, error) { return "", errNotMine }
	child := s.WrapExecutor("writer-1", next)
	for _, name := range []string{"delete_note", "drop_note", "notebook_delete"} {
		if _, err := child(name, json.RawMessage(`{"id":1}`)); err != errNotMine {
			t.Errorf("%s was answered by the notebook rather than passed on", name)
		}
	}
	if s.Len() != 1 {
		t.Fatalf("the notebook lost a note to an agent: %d left", s.Len())
	}
}

var errNotMine = errors.New("passed on")

// The titles block is what a child pays for at spawn, so it is bounded: a
// full notebook lists its most recent titles and says how many it did not.
func TestPromptBlockIsCapped(t *testing.T) {
	s := New(nil)
	for i := 0; i < MaxPromptTitles+10; i++ {
		if _, _, err := s.Write("assistant", fmt.Sprintf("Note %d", i), "body"); err != nil {
			t.Fatal(err)
		}
	}
	block := PromptBlock(s.List())
	if n := strings.Count(block, "\n- ["); n != MaxPromptTitles {
		t.Fatalf("the block listed %d titles", n)
	}
	if !strings.Contains(block, fmt.Sprintf("It already holds %d notes", MaxPromptTitles+10)) {
		t.Fatalf("the block does not say how many it left out: %q", block)
	}
	// The newest are the ones kept.
	if !strings.Contains(block, "Note 49") || strings.Contains(block, "Note 0 (") {
		t.Fatalf("the block kept the wrong end: %q", block)
	}
}

// The person's listing groups by the agent that wrote each note; the
// model's stays in the session's own order.
func TestFormatByAuthorGroups(t *testing.T) {
	s := New(nil)
	_, _, _ = s.Write("researcher-1", "First", "a")
	_, _, _ = s.Write("reviewer-1", "Second", "b")
	_, _, _ = s.Write("researcher-1", "Third", "c")
	got := FormatByAuthor(s.List())
	if strings.Index(got, "# researcher-1") > strings.Index(got, "# reviewer-1") {
		t.Fatalf("authors out of first-write order: %q", got)
	}
	if strings.Count(got, "# researcher-1") != 1 {
		t.Fatalf("an author was listed twice: %q", got)
	}
	if strings.Index(got, "Third") > strings.Index(got, "Second") {
		t.Fatalf("a note was not filed under its author: %q", got)
	}
	// The author is on the heading, so it is not repeated on every note.
	if strings.Contains(got, "— researcher-1") {
		t.Fatalf("the author is stated twice: %q", got)
	}
	if FormatByAuthor(nil) != "The notebook is empty." {
		t.Fatal("an empty notebook did not say so")
	}
}
