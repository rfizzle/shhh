package notebook

import (
	"encoding/json"
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
	if PromptBlock(nil) != "" {
		t.Fatal("empty notebook has a prompt block")
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
