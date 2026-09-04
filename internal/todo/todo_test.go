package todo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `---
title: Show open items in the rail
kind: story
priority: high        # the rail is the point
size: m
depends_on: [item-store, rail-block]
created: 2026-08-30
owner: me
---

**As a** user, **I want** the list visible.

## Acceptance criteria
- [ ] A block appears
`

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParse_ReadsKnownFieldsAndKeepsTheRest(t *testing.T) {
	it, err := Parse(BuiltinCode(), "/x/rail-todo-block.md", sample)
	if err != nil {
		t.Fatal(err)
	}
	if it.Slug != "rail-todo-block" || it.Title != "Show open items in the rail" {
		t.Errorf("slug/title = %q/%q", it.Slug, it.Title)
	}
	if it.Priority != PriorityHigh || it.Grade() != "M" || it.Status != StatusOpen || it.Fields["kind"] != "story" {
		t.Errorf("fields = %+v", it)
	}
	if strings.Join(it.DependsOn, ",") != "item-store,rail-block" {
		t.Errorf("deps = %v", it.DependsOn)
	}
	if len(it.Extra) != 1 || it.Extra[0].Key != "owner" || it.Extra[0].Value != "me" {
		t.Errorf("extra = %v", it.Extra)
	}
	if !strings.HasPrefix(it.Body, "\n**As a**") || !strings.Contains(it.Body, "- [ ] A block appears") {
		t.Errorf("body = %q", it.Body)
	}
	if len(it.Warnings) != 0 {
		t.Errorf("warnings = %v", it.Warnings)
	}
}

func TestParse_LenientWhereUsableStrictWhereNot(t *testing.T) {
	cases := []struct {
		name, content string
		wantErr       string
		wantWarn      string
	}{
		{"no header", "# just notes\n", "no header", ""},
		{"no title", "---\nstatus: open\n---\n", "no title", ""},
		{"bad status", "---\ntitle: x\nstatus: later\n---\n", "unknown status", ""},
		{"bad priority", "---\ntitle: x\npriority: urgent\n---\n", "", "unknown priority"},
		{"bad size", "---\ntitle: x\nsize: XL\n---\n", "", "unknown size"},
		{"bad kind", "---\ntitle: x\nkind: epic\n---\n", "", "unknown kind"},
		{"bad line", "---\ntitle: x\nnonsense\n---\n", "expected key: value", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			it, err := Parse(BuiltinCode(), "/x/a-thing.md", c.content)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(it.Warnings) != 1 || !strings.Contains(it.Warnings[0], c.wantWarn) {
				t.Errorf("warnings = %v, want %q", it.Warnings, c.wantWarn)
			}
			if it.Priority != PriorityMedium {
				t.Errorf("priority fallback = %q", it.Priority)
			}
		})
	}
}

func TestSlugs(t *testing.T) {
	for _, s := range []string{"a", "add-todo-runner", "v2-api", "x-1234"} {
		if err := ValidSlug(s); err != nil {
			t.Errorf("ValidSlug(%q) = %v", s, err)
		}
	}
	for _, s := range []string{"", "Add-Runner", "a--b", "-a", "a-", "x-060", "a_b", strings.Repeat("a", 49)} {
		if ValidSlug(s) == nil {
			t.Errorf("ValidSlug(%q) accepted", s)
		}
	}
	cases := map[string]string{
		"Show open items in the rail!": "show-open-items-in-the-rail",
		"  Über  cool_thing  ":         "ber-cool-thing",
		"":                             "item",
		"x-060":                        "item",
		strings.Repeat("word ", 20):    "word-word-word-word-word-word-word-word-word",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
		if err := ValidSlug(Slugify(in)); err != nil {
			t.Errorf("Slugify(%q) invalid: %v", in, err)
		}
	}
}

func TestRender_RoundTrips(t *testing.T) {
	it := Item{Slug: "x", Title: "A: title with colon", Priority: PriorityLow,
		Fields:    map[string]string{"kind": "bug", "size": "S"},
		DependsOn: []string{"a", "b"}, Created: "2026-08-30", Extra: []Unknown{{"owner", "me"}}, Body: "## Notes\nhi"}
	back, err := Parse(BuiltinCode(), "/x/x.md", Render(BuiltinCode(), it))
	if err != nil {
		t.Fatal(err)
	}
	if back.Title != it.Title || back.Fields["kind"] != "bug" || back.Priority != it.Priority || back.Grade() != "S" ||
		back.Status != StatusOpen || strings.Join(back.DependsOn, ",") != "a,b" || back.Created != it.Created ||
		len(back.Extra) != 1 || back.Extra[0] != it.Extra[0] || strings.TrimSpace(back.Body) != it.Body {
		t.Errorf("round trip lost something: %+v", back)
	}
}

func TestSetStatus_ChangesOneLineOnly(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "rail-todo-block.md", sample)
	if err := SetStatus(p, StatusBlocked); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	want := strings.Replace(sample, "owner: me\n", "owner: me\nstatus: blocked\n", 1)
	if string(got) != want {
		t.Errorf("file after SetStatus:\n%s\nwant:\n%s", got, want)
	}
	// A second identical set leaves the file untouched.
	before, _ := os.Stat(p)
	if err := SetStatus(p, StatusBlocked); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(p)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("no-op SetStatus rewrote the file")
	}
	// An existing status line is edited in place, and its comment survives.
	p2 := write(t, dir, "other.md", "---\ntitle: t\nstatus: open   # hand set\n---\nbody\n")
	if err := SetStatus(p2, StatusInProgress); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(p2)
	if string(got) != "---\ntitle: t\nstatus: in-progress # hand set\n---\nbody\n" {
		t.Errorf("in-place edit = %q", got)
	}
}

func TestStore_ReadyAndOrder(t *testing.T) {
	root := t.TempDir()
	dir := Dir(root)
	write(t, dir, "b-high-old.md", "---\ntitle: b\npriority: high\ncreated: 2026-01-01\n---\n")
	write(t, dir, "a-high-new.md", "---\ntitle: a\npriority: high\ncreated: 2026-02-01\n---\n")
	write(t, dir, "c-low.md", "---\ntitle: c\npriority: low\n---\n")
	write(t, dir, "d-waits.md", "---\ntitle: d\npriority: high\ndepends_on: [c-low, done-one]\n---\n")
	write(t, dir, "e-done-dep.md", "---\ntitle: e\ndepends_on: [done-one]\n---\n")
	write(t, dir, "f-blocked.md", "---\ntitle: f\nstatus: blocked\n---\n")
	write(t, dir, "g-missing-dep.md", "---\ntitle: g\ndepends_on: [nope]\n---\n")
	write(t, dir, "broken.md", "no header\n")
	write(t, filepath.Join(dir, DoneSubdir), "done-one.md", "---\ntitle: done\nstatus: done\n---\n")
	write(t, dir, "README.txt", "not an item")

	s := Load(BuiltinCode(), root)
	slugs := func(items []Item) string {
		var out []string
		for _, it := range items {
			out = append(out, it.Slug)
		}
		return strings.Join(out, " ")
	}
	if got := slugs(s.Items); got != "b-high-old a-high-new d-waits e-done-dep f-blocked g-missing-dep c-low" {
		t.Errorf("order = %q", got)
	}
	if got := slugs(s.Ready()); got != "b-high-old a-high-new e-done-dep c-low" {
		t.Errorf("ready = %q", got)
	}
	if next, ok := s.Next(); !ok || next.Slug != "b-high-old" {
		t.Errorf("next = %v %v", next, ok)
	}
	if len(s.Diagnostics) != 1 || !strings.Contains(s.Diagnostics[0], "broken.md: skipped: no header") {
		t.Errorf("diagnostics = %v", s.Diagnostics)
	}
	d, _ := s.Find("d-waits")
	if got := strings.Join(s.Waiting(d), ","); got != "c-low" {
		t.Errorf("waiting = %q", got)
	}
	g, _ := s.Find("g-missing-dep")
	if len(g.Warnings) != 1 || !strings.Contains(g.Warnings[0], `"nope"`) {
		t.Errorf("missing dep warning = %v", g.Warnings)
	}
	if _, ok := s.Find("done-one"); !ok {
		t.Error("archived item not findable")
	}
	if s.Count(StatusBlocked) != 1 || s.Count(StatusOpen) != 6 {
		t.Errorf("counts = %d blocked, %d open", s.Count(StatusBlocked), s.Count(StatusOpen))
	}
}

func TestStore_EmptyRootIsEmpty(t *testing.T) {
	s := Load(BuiltinCode(), t.TempDir())
	if s.Len() != 0 || len(s.Diagnostics) != 0 || len(s.Ready()) != 0 {
		t.Errorf("empty root loaded %+v", s)
	}
	var nilStore *Store
	if nilStore.Len() != 0 || nilStore.Ready() != nil {
		t.Error("nil store is not empty")
	}
}

func TestCreateAndArchive(t *testing.T) {
	root := t.TempDir()
	p, err := Create(BuiltinCode(), root, Item{Slug: "first-thing", Title: "First", Body: "## Notes\nx"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(Dir(root), ".gitignore")); err != nil {
		t.Error("ignore file not written")
	}
	if _, err := Create(BuiltinCode(), root, Item{Slug: "first-thing", Title: "Again"}); err == nil {
		t.Error("duplicate slug accepted")
	}
	if _, err := Create(BuiltinCode(), root, Item{Slug: "Bad Slug", Title: "x"}); err == nil {
		t.Error("invalid slug accepted")
	}
	if _, err := Create(BuiltinCode(), root, Item{Slug: "no-title"}); err == nil {
		t.Error("missing title accepted")
	}

	to, err := Archive(root, "first-thing", "## Report\ndone it")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("active file still present after archive")
	}
	data, _ := os.ReadFile(to)
	if !strings.Contains(string(data), "status: done") || !strings.HasSuffix(string(data), "## Notes\nx\n\n## Report\ndone it\n") {
		t.Errorf("archived file = %q", data)
	}
	if _, err := Create(BuiltinCode(), root, Item{Slug: "first-thing", Title: "Again"}); err == nil {
		t.Error("archived slug reusable")
	}
	if _, err := Archive(root, "first-thing", ""); err == nil {
		t.Error("archiving twice succeeded")
	}
	s := Load(BuiltinCode(), root)
	if len(s.Done) != 1 || s.Len() != 0 {
		t.Errorf("store after archive: %d active, %d done", s.Len(), len(s.Done))
	}
}

func TestCreate_RefusesTheOldLayout(t *testing.T) {
	root := t.TempDir()
	write(t, root, StateDir, "old context file")
	_, err := Create(BuiltinCode(), root, Item{Slug: "x", Title: "x"})
	if err == nil || !strings.Contains(err.Error(), "shhh doctor") {
		t.Errorf("err = %v", err)
	}
}

func TestRoot_FindsTheRepository(t *testing.T) {
	repo := t.TempDir()
	sub := filepath.Join(repo, "a", "b")
	for _, d := range []string{filepath.Join(repo, ".git"), sub} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := Root(sub); got != repo {
		t.Errorf("Root = %q, want %q", got, repo)
	}
	plain := t.TempDir()
	if got := Root(plain); got != plain {
		t.Errorf("Root without .git = %q, want %q", got, plain)
	}
}

func TestEditHeader_KeepsLineEndingsBOMAndComments(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "win.md", "\uFEFF---\r\ntitle: t\r\nstatus: open  # by hand\r\n---\r\nbody\r\nmore\r\n")
	if err := SetStatus(p, StatusBlocked); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	want := "\uFEFF---\r\ntitle: t\r\nstatus: blocked # by hand\r\n---\r\nbody\r\nmore\r\n"
	if string(got) != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestParse_TitleWithHashRoundTrips(t *testing.T) {
	for _, title := range []string{"Fix #12 in the parser", `Say "hi" # not a comment`, "[bracketed] first", "  padded  "} {
		back, err := Parse(BuiltinCode(), "/x/x.md", Render(BuiltinCode(), Item{Slug: "x", Title: title}))
		if err != nil {
			t.Fatal(err)
		}
		if back.Title != title {
			t.Errorf("title %q came back as %q", title, back.Title)
		}
	}
	it, err := Parse(BuiltinCode(), "/x/x.md", "---\ntitle: \"quoted\" # comment\nsize: 'S' # c\n---\n")
	if err != nil {
		t.Fatal(err)
	}
	if it.Title != "quoted" || it.Grade() != "S" {
		t.Errorf("quoted then comment: %+v", it)
	}
}

func TestParseList_QuotedAndCommented(t *testing.T) {
	got := strings.Join(parseList(unquote(`[a, "b c", 'd']  # note`)), "|")
	if got != "a|b c|d" {
		t.Errorf("parseList = %q", got)
	}
}

func TestArchive_RefusesBeforeTouchingTheItem(t *testing.T) {
	root := t.TempDir()
	write(t, Dir(root), "x.md", "---\ntitle: t\n---\n")
	write(t, filepath.Join(Dir(root), DoneSubdir), "x.md", "---\ntitle: old\nstatus: done\n---\n")
	if _, err := Archive(root, "x", "report"); err == nil {
		t.Fatal("archived over an existing archive entry")
	}
	got, _ := os.ReadFile(filepath.Join(Dir(root), "x.md"))
	if string(got) != "---\ntitle: t\n---\n" {
		t.Errorf("active item was changed by a refused archive: %q", got)
	}
	if _, err := Archive(root, "../x", ""); err == nil {
		t.Error("invalid slug accepted")
	}
}

func TestAppend_SeparatesFromAnUnterminatedFile(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "x.md", "---\ntitle: t\n---\nbody")
	if err := Append(p, "## Blocked\nwhy\n\n"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "---\ntitle: t\n---\nbody\n\n## Blocked\nwhy\n" {
		t.Errorf("appended = %q", got)
	}
	if err := Append(p, "  \n"); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(p); string(again) != string(got) {
		t.Error("blank text appended something")
	}
}

func TestLess_UndatedSortsAfterDated(t *testing.T) {
	dated := Item{Slug: "z", Created: "2026-01-01"}
	undated := Item{Slug: "a"}
	if !Less(dated, undated) || Less(undated, dated) {
		t.Error("an undated item claimed to be older than a dated one")
	}
	if Less(undated, undated) {
		t.Error("Less is not irreflexive")
	}
}

// A file that will not parse is kept as an entry rather than only counted in
// a sentence: a surface that dropped the row would say the item is gone, and
// the file is still there.
func TestStore_KeepsWhatWouldNotParse(t *testing.T) {
	root := t.TempDir()
	dir := Dir(root)
	write(t, dir, "good-one.md", "---\ntitle: Fine\n---\n")
	write(t, dir, "half-written.md", "no header at all\n")
	write(t, filepath.Join(dir, DoneSubdir), "old-mess.md", "---\nkind: story\n---\n")

	s := Load(BuiltinCode(), root)
	if s.Len() != 1 || len(s.Done) != 0 {
		t.Fatalf("store read %d active and %d done", s.Len(), len(s.Done))
	}
	if len(s.Unreadable) != 2 {
		t.Fatalf("unreadable = %+v", s.Unreadable)
	}
	active, archived := s.Unreadable[0], s.Unreadable[1]
	if active.Slug != "half-written" || active.Archived || active.Reason == "" {
		t.Errorf("the active one = %+v", active)
	}
	if archived.Slug != "old-mess" || !archived.Archived {
		t.Errorf("the archived one = %+v", archived)
	}
	// The listing still says the same thing it always said.
	if len(s.Diagnostics) != 2 || !strings.Contains(s.Diagnostics[0], "half-written.md: skipped:") {
		t.Errorf("diagnostics = %q", s.Diagnostics)
	}
}

// Reopening is the way back out of the archive, and it keeps the report:
// what a run wrote about the work is the account of why it was thought done,
// and an item coming back says nothing about that.
func TestReopen_BringsAnArchivedItemBackWithItsReport(t *testing.T) {
	root := t.TempDir()
	if _, err := Create(BuiltinCode(), root, Item{Slug: "first-thing", Title: "First", Body: "## Notes\nx"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Archive(root, "first-thing", "## Report\ndone it"); err != nil {
		t.Fatal(err)
	}
	to, err := Reopen(root, "first-thing")
	if err != nil {
		t.Fatal(err)
	}
	if to != filepath.Join(Dir(root), "first-thing.md") {
		t.Errorf("reopened to %q", to)
	}
	data, _ := os.ReadFile(to)
	if !strings.Contains(string(data), "status: open") || !strings.Contains(string(data), "## Report\ndone it") {
		t.Errorf("reopened file = %q", data)
	}
	s := Load(BuiltinCode(), root)
	if s.Len() != 1 || len(s.Done) != 0 {
		t.Errorf("store after reopen: %d active, %d done", s.Len(), len(s.Done))
	}
	if _, err := Reopen(root, "first-thing"); err == nil {
		t.Error("reopening an item that is not archived succeeded")
	}
}

// The move is checked before the file is touched, so a refusal leaves an
// archived item that is still archived rather than one marked open where
// nothing lists it.
func TestReopen_RefusesBeforeTouchingTheItem(t *testing.T) {
	root := t.TempDir()
	if _, err := Create(BuiltinCode(), root, Item{Slug: "same-name", Title: "First"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Archive(root, "same-name", ""); err != nil {
		t.Fatal(err)
	}
	// A new active item takes the name the archived one would come back to.
	write(t, Dir(root), "same-name.md", "---\ntitle: Second\n---\n")
	if _, err := Reopen(root, "same-name"); err == nil {
		t.Fatal("reopening onto an existing file succeeded")
	}
	archived, err := LoadFile(BuiltinCode(), filepath.Join(Dir(root), DoneSubdir, "same-name.md"))
	if err != nil || archived.Status != StatusDone {
		t.Errorf("the archived item was changed by a refusal: %+v (%v)", archived, err)
	}
}
