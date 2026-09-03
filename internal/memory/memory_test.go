package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/storage"
)

func testStore(t *testing.T, project string) *Store {
	t.Helper()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db, project)
}

func TestProjectScope(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "internal", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ProjectScope(nested); got != root {
		t.Fatalf("expected repo root %q, got %q", root, got)
	}

	plain := t.TempDir()
	if got := ProjectScope(plain); got != plain {
		t.Fatalf("expected the directory itself outside a repo, got %q", got)
	}
}

func TestStoreAdd_Validation(t *testing.T) {
	s := testStore(t, "/proj")
	cases := []struct {
		name                          string
		scope, kind, text, provenance string
	}{
		{"empty text", "/proj", KindLesson, "   ", ProvenanceUser},
		{"long text", "/proj", KindLesson, strings.Repeat("x", MaxTextLen+1), ProvenanceUser},
		{"bad kind", "/proj", "vibe", "text", ProvenanceUser},
		{"bad provenance", "/proj", KindLesson, "text", "oracle"},
		{"empty scope", "", KindLesson, "text", ProvenanceUser},
	}
	for _, tc := range cases {
		if _, err := s.Add(tc.scope, tc.kind, tc.text, tc.provenance); err == nil {
			t.Errorf("%s: expected an error", tc.name)
		}
	}

	e, err := s.Add(GlobalScope, KindPreference, "  prefers concise output  ", ProvenanceUser)
	if err != nil {
		t.Fatalf("valid add: %v", err)
	}
	if e.Text != "prefers concise output" {
		t.Fatalf("text should be trimmed, got %q", e.Text)
	}
}

func TestRecall_ScopedProjectFirstAndBounded(t *testing.T) {
	s := testStore(t, "/proj")
	if _, err := s.Add(GlobalScope, KindPreference, "global one", ProvenanceUser); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add("/proj", KindConvention, "project one", ProvenanceUser); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add("/other", KindLesson, "other project", ProvenanceUser); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add("/proj", KindLesson, "project two", ProvenanceUser); err != nil {
		t.Fatal(err)
	}

	got, omitted, err := s.Recall(10, 10_000)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 in-scope entries, got %d", len(got))
	}
	if omitted != 0 {
		t.Fatalf("nothing was left out, got %d omitted", omitted)
	}
	if got[0].Text != "project two" || got[1].Text != "project one" {
		t.Fatalf("project entries should come first, newest first: %q, %q", got[0].Text, got[1].Text)
	}
	if got[2].Scope != GlobalScope {
		t.Fatalf("global entries follow, got scope %q", got[2].Scope)
	}

	// Entry cap.
	got, omitted, err = s.Recall(1, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "project two" {
		t.Fatalf("maxEntries=1 should keep only the top project entry, got %+v", got)
	}
	if omitted != 2 {
		t.Fatalf("the two entries the cap left out should be counted, got %d", omitted)
	}

	// Token budget: too small for any entry after the header.
	got, omitted, err = s.Recall(10, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("a tiny token budget should recall nothing, got %d", len(got))
	}
	if omitted != 3 {
		t.Fatalf("every entry that did not fit should be counted, got %d", omitted)
	}
}

// TestRecall_StepsOverAnOversizeEntry is the defect this loop was written
// against: the entries are ordered by scope and age, never by size, so one
// long project note sat in front of every short global preference. Stopping
// at it kept all ten of them out of a prompt with room for all ten.
func TestRecall_StepsOverAnOversizeEntry(t *testing.T) {
	s := testStore(t, "/proj")
	if _, err := s.Add("/proj", KindConvention, strings.Repeat("x", 500), ProvenanceUser); err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		if _, err := s.Add(GlobalScope, KindPreference, "short preference "+strconv.Itoa(i), ProvenanceUser); err != nil {
			t.Fatal(err)
		}
	}

	// A budget with room for the header and every short entry, but not for
	// the 500-character note that sorts ahead of them.
	var short int64
	for _, e := range mustList(t, s) {
		if e.Scope == GlobalScope {
			short += int64(agent.EstimateTokens(EntryLine(e)))
		}
	}
	budget := short + int64(agent.EstimateTokens(promptBlockHeader))

	got, omitted, err := s.Recall(100, budget)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("expected the ten globals to be recalled, got %d", len(got))
	}
	for _, e := range got {
		if e.Scope != GlobalScope {
			t.Fatalf("the oversize project note should have been stepped over, got %q", e.Text)
		}
	}
	if omitted != 1 {
		t.Fatalf("the entry that did not fit should be counted once, got %d", omitted)
	}
}

func mustList(t *testing.T, s *Store) []Entry {
	t.Helper()
	entries, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return entries
}

func TestUpdate_RoundTripsAndBumpsUpdatedAt(t *testing.T) {
	s := testStore(t, "/proj")
	e, err := s.Add("/proj", KindConvention, strings.Repeat("x", 400), ProvenanceUser)
	if err != nil {
		t.Fatal(err)
	}

	edited, err := s.Update(e.ID, "  keep the note short  ")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if edited.Text != "keep the note short" {
		t.Fatalf("text should be trimmed and replaced, got %q", edited.Text)
	}
	if edited.Scope != e.Scope || edited.Kind != e.Kind || edited.Provenance != e.Provenance {
		t.Fatalf("rewording an entry restates none of its other fields, got %+v", edited)
	}
	if !edited.UpdatedAt.After(e.UpdatedAt) {
		t.Fatalf("updated_at should be bumped: %s not after %s", edited.UpdatedAt, e.UpdatedAt)
	}
	if edited.CreatedAt != e.CreatedAt {
		t.Fatalf("created_at should be left alone, got %s want %s", edited.CreatedAt, e.CreatedAt)
	}

	got, err := s.Get(e.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Text != "keep the note short" {
		t.Fatalf("the edit should be durable, got %q", got.Text)
	}

	if _, err := s.Update(e.ID, "   "); err == nil {
		t.Fatal("an empty rewrite should be refused")
	}
	if _, err := s.Update(e.ID, strings.Repeat("y", MaxTextLen+1)); err == nil {
		t.Fatal("a rewrite past the text bound should be refused")
	}
	if _, err := s.Update(e.ID+999, "nothing here"); err == nil {
		t.Fatal("rewriting an entry that does not exist should be refused")
	}
}

func TestPromptBlock_CitesIDs(t *testing.T) {
	s := testStore(t, "/proj")
	e, err := s.Add("/proj", KindCorrection, "use %w when wrapping errors", ProvenanceAgent)
	if err != nil {
		t.Fatal(err)
	}
	block := PromptBlock([]Entry{e})
	if !strings.Contains(block, "# Memory") {
		t.Fatal("block should carry the Memory header")
	}
	if want := "[m" + strconv.FormatInt(e.ID, 10) + "]"; !strings.Contains(block, want) {
		t.Fatalf("block should cite the entry id %s:\n%s", want, block)
	}
	if !strings.Contains(block, "(project correction)") {
		t.Fatalf("block should show scope and kind:\n%s", block)
	}
	if PromptBlock(nil) != "" {
		t.Fatal("no entries should render nothing")
	}
}

func TestParseRemember(t *testing.T) {
	if _, err := ParseRemember(json.RawMessage(`{`)); err == nil {
		t.Fatal("bad JSON should error")
	}
	if _, err := ParseRemember(json.RawMessage(`{"kind":"lesson"}`)); err == nil {
		t.Fatal("missing text should error")
	}
	if _, err := ParseRemember(json.RawMessage(`{"text":"x","kind":"vibe"}`)); err == nil {
		t.Fatal("bad kind should error")
	}
	if _, err := ParseRemember(json.RawMessage(`{"text":"x","kind":"lesson","scope":"universe"}`)); err == nil {
		t.Fatal("bad scope should error")
	}
	long := strings.Repeat("x", MaxTextLen+1)
	if _, err := ParseRemember(json.RawMessage(`{"text":"` + long + `","kind":"lesson"}`)); err == nil {
		t.Fatal("overlong text should error")
	}

	d, err := ParseRemember(json.RawMessage(`{"text":"tests are table-driven","kind":"convention"}`))
	if err != nil {
		t.Fatalf("valid call: %v", err)
	}
	if d.Scope != "project" {
		t.Fatalf("scope should default to project, got %q", d.Scope)
	}
	if d.Kind != KindConvention || d.Text != "tests are table-driven" {
		t.Fatalf("draft mismatch: %+v", d)
	}
}

func TestParseID(t *testing.T) {
	for _, ok := range []string{"m12", "12", " m3 "} {
		if _, err := ParseID(ok); err != nil {
			t.Errorf("%q should parse: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "m", "x7", "m-1", "0", "1.5"} {
		if _, err := ParseID(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
	id, err := ParseID("m42")
	if err != nil || id != 42 {
		t.Fatalf("expected 42, got %d (%v)", id, err)
	}
}
