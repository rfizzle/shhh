package todo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleSprint = `---
name: caching
status: open
created: 2026-09-04
session: amber-lake
owner: me
---
Make the provider cache trustworthy end to end.

## Items
- cache-ttl
- cache-invalidate
`

// sprintRoot is a backlog with three items, one archived, and whatever
// sprint file the test hands it.
func sprintRoot(t *testing.T, sprint string) string {
	t.Helper()
	root := t.TempDir()
	dir := Dir(root)
	write(t, dir, "cache-ttl.md", "---\ntitle: Give the cache a lifetime\npriority: high\nsize: S\n---\n")
	write(t, dir, "cache-invalidate.md", "---\ntitle: Invalidate on write\nsize: M\ndepends_on: [cache-ttl]\n---\n")
	write(t, dir, "cache-metrics.md", "---\ntitle: Count the hits\npriority: low\nsize: S\n---\n")
	write(t, filepath.Join(dir, DoneSubdir), "cache-keys.md", "---\ntitle: Key the cache\nstatus: done\n---\n\n## Report\nSummary: the key is the request digest.\n")
	if sprint != "" {
		write(t, dir, SprintFile, sprint)
	}
	return root
}

// itemSlugs names a list of items, in order, for a one-line comparison.
func itemSlugs(items []Item) string {
	var out []string
	for _, it := range items {
		out = append(out, it.Slug)
	}
	return strings.Join(out, " ")
}

func TestParseSprint_ReadsTheHeaderGoalAndOrder(t *testing.T) {
	sp, err := ParseSprint("/x/sprint.md", sampleSprint)
	if err != nil {
		t.Fatal(err)
	}
	if sp.Name != "caching" || sp.Status != SprintOpen || sp.Created != "2026-09-04" || sp.Session != "amber-lake" {
		t.Errorf("header = %+v", sp)
	}
	if len(sp.Extra) != 1 || sp.Extra[0].Key != "owner" {
		t.Errorf("extra = %v", sp.Extra)
	}
	if sp.Goal != "Make the provider cache trustworthy end to end." {
		t.Errorf("goal = %q", sp.Goal)
	}
	if strings.Join(sp.Slugs, ",") != "cache-ttl,cache-invalidate" {
		t.Errorf("slugs = %v", sp.Slugs)
	}
}

func TestParseSprint_StrictWhereItCannotBePlaced(t *testing.T) {
	cases := []struct{ name, content, wantErr, wantWarn string }{
		{"no name", "---\nstatus: open\n---\n", "no name", ""},
		{"name is not a slug", "---\nname: Caching Week\n---\n", "name:", ""},
		{"unknown status", "---\nname: caching\nstatus: later\n---\n", "unknown status", ""},
		{"no list", "---\nname: caching\n---\njust a goal\n", "", "names no items"},
		{"bad slug", "---\nname: caching\n---\n\n## Items\n- Not A Slug\n", "", "must be lowercase"},
		{"repeated slug", "---\nname: caching\n---\n\n## Items\n- a\n- a\n", "", "listed twice"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sp, err := ParseSprint("/x/sprint.md", c.content)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(sp.Warnings, " "), c.wantWarn) {
				t.Fatalf("warnings = %v, want %q", sp.Warnings, c.wantWarn)
			}
		})
	}
}

func TestParseSprint_ReadsOnlyTheItemList(t *testing.T) {
	sp, err := ParseSprint("/x/sprint.md", "---\nname: caching\n---\ngoal\n\n## Items\n- cache-ttl\n\n## Notes\n- not a slug at all\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(sp.Slugs, ",") != "cache-ttl" {
		t.Fatalf("slugs = %v; a bullet under another heading is not one of the set", sp.Slugs)
	}
}

func TestStoreReady_ScopedToTheSprintInFileOrder(t *testing.T) {
	// The sprint states the reverse of the backlog's own order and leaves
	// one ready item out; a waiting slug is skipped rather than offered.
	root := sprintRoot(t, "---\nname: caching\n---\ngoal\n\n## Items\n- cache-invalidate\n- cache-ttl\n")
	s := Load(root)
	if got := itemSlugs(s.Ready()); got != "cache-ttl" {
		t.Fatalf("ready = %q; the sprint's order stands and a waiting slug is skipped", got)
	}
	next, ok := s.Next()
	if !ok || next.Slug != "cache-ttl" {
		t.Fatalf("next = %v %v", next.Slug, ok)
	}
	var waiting SprintEntry
	for _, e := range s.SprintEntries() {
		if e.Slug == "cache-invalidate" {
			waiting = e
		}
	}
	if waiting.State != SprintItemWaiting || strings.Join(waiting.Waiting, ",") != "cache-ttl" {
		t.Fatalf("skipped entry = %+v; the reason has to be on the row", waiting)
	}
}

func TestStoreReady_UnchangedWithNoSprintFile(t *testing.T) {
	s := Load(sprintRoot(t, ""))
	if s.Sprint != nil {
		t.Fatalf("sprint = %+v, want none", s.Sprint)
	}
	if got := itemSlugs(s.Ready()); got != "cache-ttl cache-metrics" {
		t.Fatalf("ready = %q, want the whole backlog in its own order", got)
	}
}

func TestStoreReady_UnscopedByAClosedSprint(t *testing.T) {
	s := Load(sprintRoot(t, "---\nname: caching\nstatus: closed\n---\ngoal\n\n## Items\n- cache-ttl\n"))
	if got := itemSlugs(s.Ready()); got != "cache-ttl cache-metrics" {
		t.Fatalf("ready = %q; a closed sprint scopes nothing", got)
	}
}

func TestStoreLoad_ASprintThatWillNotParseIsADiagnostic(t *testing.T) {
	s := Load(sprintRoot(t, "---\nstatus: open\n---\n"))
	if s.Sprint != nil {
		t.Fatalf("sprint = %+v, want none", s.Sprint)
	}
	if len(s.Diagnostics) != 1 || !strings.Contains(s.Diagnostics[0], "no name") {
		t.Fatalf("diagnostics = %v", s.Diagnostics)
	}
	if got := itemSlugs(s.Ready()); got != "cache-ttl cache-metrics" {
		t.Fatalf("ready = %q; a broken sprint must not look like a finished one", got)
	}
}

func TestStoreLoad_TheSprintIsNotReadAsAnItem(t *testing.T) {
	s := Load(sprintRoot(t, sampleSprint))
	if len(s.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", s.Diagnostics)
	}
	if _, ok := s.Find("sprint"); ok {
		t.Fatal("the sprint file was loaded as a backlog item")
	}
}

func TestParseSprintBudget_RefusesWhatItCannotSpend(t *testing.T) {
	for _, spec := range []string{"S", "XL=1", "S=x", "S=-1"} {
		if _, err := ParseSprintBudget(spec); err == nil {
			t.Errorf("%q was accepted", spec)
		}
	}
}

func TestSprintEdits_ChangeOneLineAndKeepTheRest(t *testing.T) {
	root := sprintRoot(t, sampleSprint)
	path := SprintPath(root)

	if err := SprintAdd(path, "cache-metrics"); err != nil {
		t.Fatal(err)
	}
	if err := SprintDrop(path, "cache-invalidate"); err != nil {
		t.Fatal(err)
	}
	if err := SprintSetGoal(path, "Make it fast, then make it provable."); err != nil {
		t.Fatal(err)
	}
	if err := SprintSetStatus(path, SprintClosed); err != nil {
		t.Fatal(err)
	}
	sp, err := LoadSprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(sp.Slugs, ",") != "cache-ttl,cache-metrics" {
		t.Errorf("slugs = %v", sp.Slugs)
	}
	if sp.Goal != "Make it fast, then make it provable." {
		t.Errorf("goal = %q", sp.Goal)
	}
	if sp.Status != SprintClosed {
		t.Errorf("status = %q", sp.Status)
	}
	if len(sp.Extra) != 1 || sp.Extra[0].Key != "owner" || sp.Extra[0].Value != "me" {
		t.Errorf("extra = %v; an unknown field has to survive every write", sp.Extra)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"name: caching", "created: 2026-09-04", "session: amber-lake"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("%q is gone from the file:\n%s", want, data)
		}
	}
}

func TestSprintEdits_RefuseWhatWouldChangeNothing(t *testing.T) {
	root := sprintRoot(t, sampleSprint)
	path := SprintPath(root)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := SprintAdd(path, "cache-ttl"); err == nil {
		t.Error("adding a slug already in the sprint was accepted")
	}
	if err := SprintDrop(path, "cache-metrics"); err == nil {
		t.Error("dropping a slug not in the sprint was accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("a refused edit rewrote the file:\n%s", after)
	}
}

func TestCreateSprint_RefusesASecondOne(t *testing.T) {
	root := sprintRoot(t, "")
	sp := Sprint{Name: "caching", Created: "2026-09-04", Goal: "One goal.", Slugs: []string{"cache-ttl"}}
	if _, err := CreateSprint(root, sp); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateSprint(root, sp); err == nil {
		t.Fatal("a second sprint was written over the first")
	}
	got, err := LoadSprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "caching" || got.Goal != "One goal." || strings.Join(got.Slugs, ",") != "cache-ttl" {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestCloseSprintIfDone_ArchivesTheLastSlugWithTheReports(t *testing.T) {
	root := sprintRoot(t, "---\nname: caching\n---\ngoal\n\n## Items\n- cache-ttl\n- cache-keys\n- cache-gone\n")
	// cache-gone is in the sprint and in no directory: a slug dropped from
	// the backlog is accounted for rather than holding the set open.
	if to, err := CloseSprintIfDone(root); err != nil || to != "" {
		t.Fatalf("closed early: %q %v", to, err)
	}
	if _, err := Archive(root, "cache-ttl", "## Report\nSummary: the lifetime is a duration on the entry.\n"); err != nil {
		t.Fatal(err)
	}
	to, err := CloseSprintIfDone(root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(Dir(root), DoneSubdir, SprintsSubdir, "caching.md")
	if to != want {
		t.Fatalf("archived to %q, want %q", to, want)
	}
	if _, err := os.Stat(SprintPath(root)); !os.IsNotExist(err) {
		t.Error("the sprint file is still in place")
	}
	data, err := os.ReadFile(to)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"status: closed",
		"### cache-ttl",
		"Summary: the lifetime is a duration on the entry.",
		"### cache-keys",
		"Summary: the key is the request digest.",
		"### cache-gone",
		"Dropped from the backlog",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the archived sprint lacks %q:\n%s", want, text)
		}
	}
	if Load(root).Sprint != nil {
		t.Error("the backlog still reads a sprint")
	}
}

func TestCloseSprint_EarlyListsWhatWasLeft(t *testing.T) {
	root := sprintRoot(t, "---\nname: caching\n---\ngoal\n\n## Items\n- cache-ttl\n- cache-metrics\n")
	to, err := CloseSprint(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(to)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "Deferred:") ||
		!strings.Contains(text, "(cache-ttl) — ready, back in the backlog") ||
		!strings.Contains(text, "(cache-metrics) — ready, back in the backlog") {
		t.Errorf("an early close has to list what was left:\n%s", text)
	}
}

// A slug dropped from the backlog leaves the set: it is neither work done
// nor work outstanding, so it is out of both halves of n of m — and it does
// not hold the sprint open either.
func TestSprintProgress_LeavesOutASlugTheBacklogNoLongerHolds(t *testing.T) {
	root := sprintRoot(t, "---\nname: caching\n---\ngoal\n\n## Items\n- cache-keys\n- cache-gone\n- cache-ttl\n")
	s := Load(root)
	done, total := s.SprintProgress()
	if done != 1 || total != 2 {
		t.Fatalf("progress = %d of %d, want 1 of 2", done, total)
	}
	if s.SprintFinished() {
		t.Error("a sprint with an unfinished slug reported itself finished")
	}
}

func TestItemReport_IsTheReportSectionAlone(t *testing.T) {
	it := Item{Body: "\nprose\n\n## Report\nSummary: it works.\n\n## Notes\nnot the report\n"}
	if got := ItemReport(it); got != "Summary: it works." {
		t.Fatalf("report = %q", got)
	}
	if got := ItemReport(Item{Body: "\nno report here\n"}); got != "" {
		t.Fatalf("report = %q, want none", got)
	}
}

func TestSprintAdd_TouchesExactlyOneLine(t *testing.T) {
	root := sprintRoot(t, sampleSprint)
	path := SprintPath(root)
	if err := SprintAdd(path, "cache-metrics"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	before, after := strings.Split(sampleSprint, "\n"), strings.Split(string(data), "\n")
	if len(after) != len(before)+1 {
		t.Fatalf("%d lines, want %d:\n%s", len(after), len(before)+1, data)
	}
	var added []string
	for i, j := 0, 0; i < len(before); i, j = i+1, j+1 {
		if before[i] != after[j] {
			added = append(added, after[j])
			j++
			if j >= len(after) || before[i] != after[j] {
				t.Fatalf("line %d changed rather than a line being inserted:\n%s", i+1, data)
			}
		}
	}
	if len(added) != 1 || added[0] != "- cache-metrics" {
		t.Fatalf("inserted %v", added)
	}
}

// A sprint that names nothing is what "ready" means, so it means nothing
// is ready. That has to be said on the file rather than left to be
// discovered as a backlog with no next item.
func TestParseSprint_AnEmptyListIsAWarning(t *testing.T) {
	root := sprintRoot(t, "---\nname: caching\n---\ngoal\n\n## Items\n")
	s := Load(root)
	if len(s.Ready()) != 0 {
		t.Fatalf("ready = %v; an open sprint that names nothing scopes to nothing", itemSlugs(s.Ready()))
	}
	if !strings.Contains(strings.Join(s.Sprint.Warnings, " "), "item list is empty") {
		t.Fatalf("warnings = %v", s.Sprint.Warnings)
	}
}

// The sprint's file name is reserved: an item written there would be
// skipped by the loader and would break the sprint reader too.
func TestCreate_RefusesTheSprintsFileName(t *testing.T) {
	root := sprintRoot(t, "")
	_, err := Create(root, Item{Slug: strings.TrimSuffix(SprintFile, ".md"), Title: "Not an item"})
	if err == nil || !strings.Contains(err.Error(), "names the sprint file") {
		t.Fatalf("err = %v", err)
	}
}

// A sprint has to be free to close under its own name. Creating one whose
// name the archive already holds is refused at creation, because the same
// refusal at close time would leave an open sprint with every slug done
// scoping the ready list to nothing, with no item left to unstick it.
func TestCreateSprint_RefusesANameTheArchiveHolds(t *testing.T) {
	root := sprintRoot(t, "")
	sp := Sprint{Name: "caching", Created: "2026-09-04", Goal: "One.", Slugs: []string{"cache-ttl"}}
	if _, err := CreateSprint(root, sp); err != nil {
		t.Fatal(err)
	}
	if _, err := CloseSprint(root); err != nil {
		t.Fatal(err)
	}
	if !SprintNameTaken(root, "caching") {
		t.Fatal("the archive should hold the closed sprint's name")
	}
	_, err := CreateSprint(root, sp)
	if err == nil || !strings.Contains(err.Error(), "free to close under its own name") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(SprintPath(root)); !os.IsNotExist(statErr) {
		t.Fatal("a refused creation wrote the sprint file")
	}
}
