package todo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The item every test here reads: a header with a dependency and a size, and
// a body making the four kinds of claim a grooming grades — a reference, a
// sentence about what happens today, and two acceptance criteria.
const groomable = `---
title: Give the cache a lifetime
kind: story
priority: high
size: M
depends_on: [cache-store]
created: 2026-08-01
owner: me
---

**As a** user, **I want** entries to expire.

## Acceptance criteria
- [ ] The reader drops an entry past its age
- [ ] internal/cache/store.go:88 takes the lifetime from the config

## Notes
Today the reader serves a stale entry rather than refusing.
`

func groomedItem(t *testing.T, dir string) Item {
	t.Helper()
	path := write(t, dir, "cache-ttl.md", groomable)
	it, err := LoadFile(BuiltinCode(), path)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

// A reference that moved yields a moved verdict, and accepting it rewrites
// exactly that line and no other.
func TestGroom_AMovedReferenceRewritesOnlyItsOwnLine(t *testing.T) {
	dir := t.TempDir()
	it := groomedItem(t, dir)
	r, err := Groom(it, `
claim: - [ ] internal/cache/store.go:88 takes the lifetime from the config
verdict: moved
now: - [ ] internal/cache/reader.go:120 takes the lifetime from the config
evidence: the constructor moved to reader.go in 9f2a11c
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Findings) != 1 {
		t.Fatalf("findings = %+v", r.Findings)
	}
	f := r.Findings[0]
	if f.Verdict != VerdictMoved || !f.Edits() || !f.Criterion {
		t.Errorf("finding = %+v", f)
	}
	before, err := os.ReadFile(it.Path)
	if err != nil {
		t.Fatal(err)
	}
	n, _, err := Accept(it.Path, r.Changes(), "")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("changed %d lines, want 1", n)
	}
	after := readFile(t, it.Path)
	if !strings.Contains(after, "reader.go:120") || strings.Contains(after, "store.go:88") {
		t.Errorf("the line was not rewritten:\n%s", after)
	}
	if diffLines(string(before), after) != 1 {
		t.Errorf("more than the named line changed:\n%s", after)
	}
}

// An unknown header field survives a write, the way it survives every other
// write this package makes, and the stamp lands as its own line.
func TestGroom_AcceptingWritesTheStampAndKeepsAnUnknownField(t *testing.T) {
	dir := t.TempDir()
	it := groomedItem(t, dir)
	r, err := Groom(it, `
claim: size: M
verdict: changed
now: size: L
evidence: the config reader and its two callers are all in scope now
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Accept(it.Path, r.Changes(), "2026-09-04 @ abc1234"); err != nil {
		t.Fatal(err)
	}
	out := readFile(t, it.Path)
	for _, want := range []string{"size: L", "owner: me", "groomed: 2026-09-04 @ abc1234"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	again, err := LoadFile(BuiltinCode(), it.Path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Grade() != "L" || again.Groomed != "2026-09-04 @ abc1234" {
		t.Errorf("reread = %+v", again)
	}
}

// Declining is the whole card: nothing accepted writes nothing at all, the
// stamp included.
func TestGroom_AcceptingNothingWritesNothing(t *testing.T) {
	dir := t.TempDir()
	it := groomedItem(t, dir)
	r, err := Groom(it, "claim: size: M\nverdict: changed\nnow: size: L\nevidence: bigger now\n")
	if err != nil {
		t.Fatal(err)
	}
	n, _, err := Accept(it.Path, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || readFile(t, it.Path) != groomable {
		t.Errorf("declining wrote something: %d lines\n%s", n, readFile(t, it.Path))
	}
	if len(r.Changes()) != 1 {
		t.Errorf("the reading itself should still hold its change: %+v", r.Changes())
	}
}

// A dependency the archive already holds comes out of the list, which is one
// line of the header and not a rewrite of it.
func TestGroom_AFinishedDependencyLeavesTheList(t *testing.T) {
	dir := t.TempDir()
	it := groomedItem(t, dir)
	r, err := Groom(it, `
claim: depends_on: [cache-store]
verdict: already done
now: depends_on: []
evidence: cache-store is in the archive as of 4d1e00b
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Accept(it.Path, r.Changes(), ""); err != nil {
		t.Fatal(err)
	}
	again, err := LoadFile(BuiltinCode(), it.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.DependsOn) != 0 {
		t.Errorf("depends_on = %v", again.DependsOn)
	}
}

// Every criterion satisfied by the tree is the one reading that proposes an
// archive, and the evidence is the report it proposes it with.
func TestGroom_EveryCriterionDoneIsProposedForArchiving(t *testing.T) {
	dir := t.TempDir()
	it := groomedItem(t, dir)
	r, err := Groom(it, `
claim: - [ ] The reader drops an entry past its age
verdict: already done
now: - [x] The reader drops an entry past its age (2f9c0aa)
evidence: reader.go:44 checks the age, added in 2f9c0aa

claim: - [ ] internal/cache/store.go:88 takes the lifetime from the config
verdict: already done
now: - [x] internal/cache/reader.go:120 takes the lifetime from the config (2f9c0aa)
evidence: reader.go:120 reads cache.ttl

claim: Today the reader serves a stale entry rather than refusing.
verdict: changed
now: Today the reader refuses a stale entry.
evidence: reader.go:52 returns ErrStale
`)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Finished() {
		t.Fatalf("not proposed for archiving: %+v", r.Findings)
	}
	if report := r.Report(); !strings.Contains(report, "## Report") || !strings.Contains(report, "reader.go:44") {
		t.Errorf("report = %q", report)
	}
	// The sentence that merely changed is not a criterion, so it cannot be
	// what makes an item finished.
	for _, f := range r.Findings {
		if f.Verdict == VerdictChanged && f.Criterion {
			t.Errorf("a note counted as a criterion: %+v", f)
		}
	}
}

// A claim that holds and one the reading could not settle propose nothing:
// a card built from the changes has neither row on it.
func TestGroom_HoldsAndUnknownProposeNoLine(t *testing.T) {
	dir := t.TempDir()
	it := groomedItem(t, dir)
	r, err := Groom(it, `
claim: - [ ] The reader drops an entry past its age
verdict: holds
evidence: reader.go:44 still does it

claim: Today the reader serves a stale entry rather than refusing.
verdict: unknown
now: Today the reader might refuse.
evidence: the path is behind a build tag this checkout does not build
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes()) != 0 {
		t.Errorf("changes = %+v", r.Changes())
	}
	if r.Count(VerdictHolds) != 1 || r.Count(VerdictUnknown) != 1 {
		t.Errorf("counts = %+v", r.Findings)
	}
}

// A verdict word off the set is not a verdict, and a block without one is
// dropped rather than guessed at.
func TestGroom_AVerdictOffTheSetIsDropped(t *testing.T) {
	dir := t.TempDir()
	it := groomedItem(t, dir)
	r, err := Groom(it, "claim: size: M\nverdict: probably fine\nnow: size: L\nevidence: a feeling\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Findings) != 0 {
		t.Errorf("findings = %+v", r.Findings)
	}
}

// The stamp names the commit, because staleness is measured in commits.
func TestGroom_TheStampCarriesTheShortHead(t *testing.T) {
	r := Reading{Head: "1a2b3c4d5e6f", Read: time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)}
	if got := r.Stamp(); got != "2026-09-04 @ 1a2b3c4" {
		t.Errorf("stamp = %q", got)
	}
	if got := (Reading{Read: r.Read}).Stamp(); got != "2026-09-04" {
		t.Errorf("stamp without a head = %q", got)
	}
	if head := GroomedHead(Item{Groomed: "2026-09-04 @ 1a2b3c4"}); head != "1a2b3c4" {
		t.Errorf("head = %q", head)
	}
	if head := GroomedHead(Item{}); head != "" {
		t.Errorf("an item nobody groomed has no head, got %q", head)
	}
}

// The count draws past the threshold and not at it, and never for an item
// nobody has read against the tree.
func TestGroom_StaleDrawsPastTheThresholdAndNotBelow(t *testing.T) {
	root := groomRepo(t, 4)
	head := gitHead(t, root, 4)
	items := []Item{
		{Slug: "read", Groomed: "2026-09-04 @ " + head},
		{Slug: "never"},
	}
	if stale := Stale(root, items, 4); len(stale) != 0 {
		t.Errorf("at the threshold: %v", stale)
	}
	if stale := Stale(root, items, 3); stale["read"] != 4 || len(stale) != 1 {
		t.Errorf("past the threshold: %v", stale)
	}
	if stale := Stale(root, items, -1); len(stale) != 0 {
		t.Errorf("turned off: %v", stale)
	}
}

// The reading a run is handed is the accepted one, and only while the person
// has not edited the item past it.
func TestGroom_TheResearchBlockIsDroppedOnceTheItemMovesPastIt(t *testing.T) {
	root := t.TempDir()
	dir := Dir(root)
	it := groomedItem(t, dir)
	r := Reading{Slug: it.Slug, Head: "1a2b3c4d", Read: time.Now(), Findings: []Finding{
		{Verdict: VerdictMoved, Claim: "store.go:88", Now: "reader.go:120", Evidence: "moved in 9f2a11c"},
	}}
	if err := SaveReading(root, r); err != nil {
		t.Fatal(err)
	}
	block := GroomingBlock(root, it.Slug)
	if !strings.Contains(block, "1a2b3c4") || !strings.Contains(block, "moved in 9f2a11c") {
		t.Errorf("block = %q", block)
	}
	// An edit to the item after the reading makes the reading the older
	// account, and the older account is not what a stage is told.
	touch(t, it.Path, time.Now().Add(time.Minute))
	if block := GroomingBlock(root, it.Slug); block != "" {
		t.Errorf("a reading older than the file was still handed over: %q", block)
	}
}

// groomRepo is a repository with n commits past its first.
func groomRepo(t *testing.T, n int) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	ws := t.TempDir()
	for i := 0; i <= n; i++ {
		if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte(strings.Repeat("x", i+1)), 0o644); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			runGit(t, ws, "init", "-q")
		}
		runGit(t, ws, "add", ".")
		runGit(t, ws, "commit", "-q", "-m", "c")
	}
	return ws
}

// gitHead is the commit n back from HEAD.
func gitHead(t *testing.T, ws string, back int) string {
	t.Helper()
	out, err := exec.Command("git", "-C", ws, "rev-parse", "HEAD~"+strconv.Itoa(back)).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func runGit(t *testing.T, ws string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", ws}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func touch(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// diffLines is how many lines two versions of a file differ on.
func diffLines(before, after string) int {
	a, b := strings.Split(before, "\n"), strings.Split(after, "\n")
	n := 0
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y string
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			n++
		}
	}
	return n
}

// A verdict whose claim quotes a line the file does not hold cannot be a
// line edit, and is counted rather than dropped: a reading that quietly lost
// one would read as a reading that found nothing there.
func TestGroom_AClaimThatMatchesNoLineIsCountedNotDropped(t *testing.T) {
	dir := t.TempDir()
	it := groomedItem(t, dir)
	r, err := Groom(it, `
claim: - [ ] Something the item never said
verdict: gone
evidence: nothing in the tree and nothing in the item
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Findings) != 1 || r.Findings[0].Line != 0 {
		t.Fatalf("findings = %+v", r.Findings)
	}
	if len(r.Changes()) != 0 || r.Unplaced() != 1 {
		t.Errorf("changes = %d, unplaced = %d", len(r.Changes()), r.Unplaced())
	}
	if _, _, err := Accept(it.Path, r.Findings, ""); err != nil {
		t.Fatal(err)
	}
	if readFile(t, it.Path) != groomable {
		t.Errorf("a claim with nowhere to land wrote something:\n%s", readFile(t, it.Path))
	}
}

// The file the person edited while the reading was in flight keeps its own
// line: a finding names a line by number and by what was on it, and the
// number alone would write the correction over whatever moved there.
func TestGroom_ALineEditedUnderTheReadingIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	it := groomedItem(t, dir)
	r, err := Groom(it, `
claim: Today the reader serves a stale entry rather than refusing.
verdict: changed
now: Today the reader refuses a stale entry.
evidence: reader.go:52 returns ErrStale
`)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(groomable,
		"Today the reader serves a stale entry rather than refusing.",
		"Today the reader logs and serves a stale entry.", 1)
	if err := os.WriteFile(it.Path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	n, _, err := Accept(it.Path, r.Changes(), "")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || readFile(t, it.Path) != edited {
		t.Errorf("the reading wrote over an edit it never read: %d lines\n%s", n, readFile(t, it.Path))
	}
}

// Two finished dependencies are two claims about one physical line, and each
// one's replacement was written against the line as it originally stood.
// Only the first can land, and the second comes back named rather than
// written over: a correction the person accepted and cannot find in the file
// afterwards is worse than one that was refused out loud.
func TestGroom_TwoCorrectionsOfOneLineTakeTheFirstAndNameTheRest(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "cache-ttl.md",
		"---\ntitle: Give the cache a lifetime\ndepends_on: [a, b, c]\n---\n\n## Notes\nhi\n")
	it, err := LoadFile(BuiltinCode(), path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Groom(it, `
claim: depends_on: [a, b, c]
verdict: already done
now: depends_on: [b, c]
evidence: a is in the archive

claim: depends_on: [a, b, c]
verdict: already done
now: depends_on: [a, c]
evidence: b is in the archive
`)
	if err != nil {
		t.Fatal(err)
	}
	n, skipped, err := Accept(path, r.Changes(), "")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("changed %d lines, want 1: one line takes one change", n)
	}
	if len(skipped) != 1 || skipped[0].Why != WhyLineTaken {
		t.Fatalf("skipped = %+v", skipped)
	}
	again, err := LoadFile(BuiltinCode(), path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(again.DependsOn, ",") != "b,c" {
		t.Errorf("depends_on = %v, want the first accepted change", again.DependsOn)
	}
}

// A replacement line that renames the header field is two edits with one of
// them unstated, so the file keeps its line and the reader is told.
func TestGroom_AHeaderEditThatRenamesTheFieldIsNamedNotWritten(t *testing.T) {
	dir := t.TempDir()
	it := groomedItem(t, dir)
	r, err := Groom(it, "claim: size: M\nverdict: changed\nnow: grade: L\nevidence: it is bigger now\n")
	if err != nil {
		t.Fatal(err)
	}
	n, skipped, err := Accept(it.Path, r.Changes(), "")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || len(skipped) != 1 || skipped[0].Why != WhyOtherField {
		t.Fatalf("changed = %d, skipped = %+v", n, skipped)
	}
	if readFile(t, it.Path) != groomable {
		t.Errorf("the file was written:\n%s", readFile(t, it.Path))
	}
}

// An item that leaves the backlog takes its reading with it: the scratch
// file is about work that no longer exists.
func TestGroom_ArchivingAndDroppingDiscardTheReading(t *testing.T) {
	for _, tc := range []struct {
		name string
		gone func(root, slug string) error
	}{
		{"archived", func(root, slug string) error { _, err := Archive(root, slug, ""); return err }},
		{"dropped", Remove},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			it := groomedItem(t, Dir(root))
			if err := SaveReading(root, Reading{Slug: it.Slug, Read: time.Now()}); err != nil {
				t.Fatal(err)
			}
			if _, ok := LoadReading(root, it.Slug); !ok {
				t.Fatal("the reading was not written")
			}
			if err := tc.gone(root, it.Slug); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(groomPath(root, it.Slug)); !os.IsNotExist(err) {
				t.Errorf("the reading outlived the item: %v", err)
			}
		})
	}
}
