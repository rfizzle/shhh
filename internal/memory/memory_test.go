package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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

	got, err := s.Recall(10, 10_000)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 in-scope entries, got %d", len(got))
	}
	if got[0].Text != "project two" || got[1].Text != "project one" {
		t.Fatalf("project entries should come first, newest first: %q, %q", got[0].Text, got[1].Text)
	}
	if got[2].Scope != GlobalScope {
		t.Fatalf("global entries follow, got scope %q", got[2].Scope)
	}

	// Entry cap.
	got, err = s.Recall(1, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "project two" {
		t.Fatalf("maxEntries=1 should keep only the top project entry, got %+v", got)
	}

	// Token budget: too small for any entry after the header.
	got, err = s.Recall(10, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("a tiny token budget should recall nothing, got %d", len(got))
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
