package worktree

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/hostgit"
)

const mergingBase = "package main\n\nconst modelFields = 192\n\n// one\n// two\n// three\n// four\n// five\n// six\n\nvar list = []string{\n\t\"a\",\n}\n\nvar y = 0\n"

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	if _, err := hostgit.Output(context.Background(), dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-am", msg); err != nil {
		t.Fatal(err)
	}
}

// mergingPair is a patch landed from one copy and a second copy, both taken
// from the same base, the second having already made its own change.
func mergingPair(t *testing.T, landedEdit, laneEdit func(string) string) (string, WorktreeHandle) {
	t.Helper()
	return mergingPairOn(t, mergingBase, landedEdit, laneEdit)
}

// mergingPairOn is mergingPair over a base of its own.
func mergingPairOn(t *testing.T, base string, landedEdit, laneEdit func(string) string) (string, WorktreeHandle) {
	t.Helper()
	repo := initTestRepo(t)
	writeInto(t, repo, "main.go", base)
	commitAll(t, repo, "base")
	other, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { RemoveWorktree(other.RepoTop, other.Dir) })
	lane, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { RemoveWorktree(lane.RepoTop, lane.Dir) })
	writeInto(t, other.Root, "main.go", landedEdit(base))
	landed, err := WorktreePatch(other.Dir)
	if err != nil {
		t.Fatal(err)
	}
	writeInto(t, lane.Root, "main.go", laneEdit(base))
	return landed, lane
}

func raise(to string) func(string) string {
	return func(s string) string { return strings.Replace(s, "= 192", "= "+to, 1) }
}

func appendEntry(entry string) func(string) string {
	return func(s string) string { return strings.Replace(s, "\t\"a\",\n", "\t\"a\",\n\t\""+entry+"\",\n", 1) }
}

func editThree(to string) func(string) string {
	return func(s string) string { return strings.Replace(s, "// three", "// "+to, 1) }
}

func TestReseedMerging_BothRaisedOneCount(t *testing.T) {
	landed, lane := mergingPair(t, raise("193"), raise("194"))
	rec, err := ReseedMerging(context.Background(), lane.Dir, landed, nil, "landed-item", "lane-item")
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Unsettled) != 0 || len(rec.Settled) != 1 {
		t.Fatalf("the sum should settle the file: %+v", rec)
	}
	if got := readFrom(t, lane.Root, "main.go"); !strings.Contains(got, "const modelFields = 195\n") || strings.Contains(got, "<<<<") {
		t.Fatalf("both deltas should be summed:\n%s", got)
	}
}

func TestReseedMerging_BothAppendedAtOnePlace(t *testing.T) {
	landed, lane := mergingPair(t, appendEntry("landed"), appendEntry("lane"))
	rec, err := ReseedMerging(context.Background(), lane.Dir, landed, nil, "landed-item", "lane-item")
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Unsettled) != 0 {
		t.Fatalf("both insertions should be kept: %+v", rec)
	}
	if got := readFrom(t, lane.Root, "main.go"); !strings.Contains(got, "\t\"a\",\n\t\"landed\",\n\t\"lane\",\n}") {
		t.Fatalf("the landed entry should come first:\n%s", got)
	}
}

func TestReseedMerging_TheBaseMovesAndTheLanesWorkStaysItsOwn(t *testing.T) {
	landed, lane := mergingPair(t, raise("193"), func(s string) string {
		return raise("194")(strings.Replace(s, "var y = 0", "var y = 1", 1))
	})
	if _, err := ReseedMerging(context.Background(), lane.Dir, landed, nil, "a", "b"); err != nil {
		t.Fatal(err)
	}
	patch, err := WorktreePatch(lane.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "-const modelFields = 193") || !strings.Contains(patch, "+const modelFields = 195") || !strings.Contains(patch, "+var y = 1") {
		t.Fatalf("the patch should be the lane's own work over the moved base:\n%s", patch)
	}
}

func TestReseedMerging_AnEditedRegionIsMarkedAndNamed(t *testing.T) {
	landed, lane := mergingPair(t, editThree("landed words"), editThree("lane words"))
	rec, err := ReseedMerging(context.Background(), lane.Dir, landed, nil, "landed-item", "lane-item")
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Unsettled) != 1 || rec.Unsettled[0] != "main.go" {
		t.Fatalf("the file should be unsettled: %+v", rec)
	}
	got := readFrom(t, lane.Root, "main.go")
	for _, want := range []string{mark("<", "landed-item") + "\n// landed words\n", mark("|", "base") + "\n// three\n", mark("=", "") + "\n// lane words\n" + mark(">", "lane-item") + "\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(rec.Evidence, "@@ line 7\n") || !strings.Contains(rec.Evidence, "// lane words") {
		t.Fatalf("the regions should be quoted at their lines:\n%s", rec.Evidence)
	}
	if rec.Seeded["main.go"].Text != got || rec.Sides["main.go"][0] == rec.Sides["main.go"][1] {
		t.Fatalf("the seeded text and the sides should be kept: %+v", rec)
	}
}

func TestReseedMerging_ABaseMismatchIsNotACollisionOfWork(t *testing.T) {
	landed, lane := mergingPair(t, raise("193"), raise("194"))
	// The copy's base is no longer the one the patch was written against.
	writeInto(t, lane.Root, "main.go", raise("200")(mergingBase))
	commitAll(t, lane.Dir, "moved")
	writeInto(t, lane.Root, "main.go", raise("201")(mergingBase))
	before := readFrom(t, lane.Root, "main.go")
	_, err := ReseedMerging(context.Background(), lane.Dir, landed, nil, "a", "b")
	var clash *ReseedCollision
	if !errors.As(err, &clash) {
		t.Fatalf("want a *ReseedCollision, got %v", err)
	}
	if readFrom(t, lane.Root, "main.go") != before {
		t.Fatal("the copy should be as it was")
	}
}

func TestReseedMerging_PutBackRestoresTheLanesPatch(t *testing.T) {
	landed, lane := mergingPair(t, editThree("landed words"), editThree("lane words"))
	writeInto(t, lane.Root, "extra.go", "package main\n")
	before, err := WorktreePatch(lane.Dir)
	if err != nil {
		t.Fatal(err)
	}
	head := func() string {
		out, err := hostgit.Output(context.Background(), lane.Dir, "rev-parse", "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	oldHead := head()
	rec, err := ReseedMerging(context.Background(), lane.Dir, landed, nil, "a", "b")
	if err != nil {
		t.Fatal(err)
	}
	if head() == oldHead {
		t.Fatal("the base should have moved")
	}
	if err := rec.PutBack(); err != nil {
		t.Fatal(err)
	}
	after, err := WorktreePatch(lane.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if after != before || head() != oldHead {
		t.Fatalf("the copy should be the lane's patch on its old base\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestSettleRegions_ARegionNeitherRuleFitsStaysMarked(t *testing.T) {
	region := func(ours, base, theirs string) string {
		return mark("<", "a") + "\n" + ours + mark("|", baseLabel) + "\n" + base + mark("=", "") + "\n" + theirs + mark(">", "b") + "\n"
	}
	for name, text := range map[string]string{
		"opposite directions": region("n = 3\n", "n = 2\n", "n = 1\n"),
		"a shared line":       region("x\ny\n", "", "x\nz\n"),
		"a changed comment":   region("n = 3 // a\n", "n = 2\n", "n = 4\n"),
		"digits in a hash":    region("sha 4f9a\n", "sha 3f9a\n", "sha 5f9a\n"),
		"digits in a name":    region("Phase3\n", "Phase2\n", "Phase4\n"),
		"a sum below zero":    region("n = 0\n", "n = 2\n", "n = 1\n"),
		"two integers":        region("a 3 b 1\n", "a 2 b 1\n", "a 4 b 1\n"),
		"a setext underline":  region("-------\n", "=======\n", "~~~~~~~\n"),
	} {
		if got, left := settleRegions(text, "a", "b"); left != 1 || got != text {
			t.Errorf("%s: want the region kept, got %d left:\n%s", name, left, got)
		}
	}
}

// A file of CRLF lines is marked with CRLF marks, and the rules read them: a
// count both raised is settled and keeps its line endings, and a region no
// rule fits is unsettled with the marks in the file, never carried as settled.
func TestReseedMerging_ACrlfFileIsSettledOrMarkedNeverHalfOf(t *testing.T) {
	landed, lane := mergingPairOn(t, "n = 2\r\n", func(string) string { return "n = 3\r\n" }, func(string) string { return "n = 4\r\n" })
	rec, err := ReseedMerging(context.Background(), lane.Dir, landed, nil, "a", "b")
	if err != nil || len(rec.Unsettled) != 0 {
		t.Fatalf("the sum should settle a CRLF file: %v %+v", err, rec)
	}
	if got := readFrom(t, lane.Root, "main.go"); got != "n = 5\r\n" {
		t.Fatalf("the settled file keeps its line endings: %q", got)
	}

	landed, lane = mergingPairOn(t, "x one\r\n", func(string) string { return "x two\r\n" }, func(string) string { return "x three\r\n" })
	rec, err = ReseedMerging(context.Background(), lane.Dir, landed, nil, "a", "b")
	if err != nil || len(rec.Unsettled) != 1 {
		t.Fatalf("a CRLF region no rule fits is unsettled: %v %+v", err, rec)
	}
	if got := readFrom(t, lane.Root, "main.go"); !strings.Contains(got, "x two") || !strings.Contains(got, "x three") {
		t.Fatalf("both sides stay in the marked file: %q", got)
	}
}

// A line of the text that looks like a mark is not one, so a setext underline
// in the base does not split the region and no side is dropped.
func TestReseedMerging_ALineThatLooksLikeAMarkIsNotOne(t *testing.T) {
	landed, lane := mergingPairOn(t, "Title\n=======\n",
		func(s string) string { return strings.Replace(s, "=======", "-------", 1) },
		func(s string) string { return strings.Replace(s, "=======", "~~~~~~~", 1) })
	rec, err := ReseedMerging(context.Background(), lane.Dir, landed, nil, "a", "b")
	if err != nil || len(rec.Unsettled) != 1 {
		t.Fatalf("both edits of one line are unsettled: %v %+v", err, rec)
	}
	if got := readFrom(t, lane.Root, "main.go"); !strings.Contains(got, "-------\n") || !strings.Contains(got, "~~~~~~~\n") || !strings.Contains(got, "=======\n") {
		t.Fatalf("all three sides should be in the marked file: %q", got)
	}
}

// A file that ends without a newline still does after a settled merge.
func TestReseedMerging_ASettledFileKeepsItsMissingFinalNewline(t *testing.T) {
	landed, lane := mergingPairOn(t, "n = 2", func(string) string { return "n = 3" }, func(string) string { return "n = 4" })
	rec, err := ReseedMerging(context.Background(), lane.Dir, landed, nil, "a", "b")
	if err != nil || len(rec.Unsettled) != 0 {
		t.Fatalf("the sum should settle: %v %+v", err, rec)
	}
	if got := readFrom(t, lane.Root, "main.go"); got != "n = 5" {
		t.Fatalf("no newline was there and none is added: %q", got)
	}
}
