package worktree

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A turn given a marked file is judged on what it left: a marker, the file
// as it was seeded, or either side of the merge whole is not a
// reconciliation, and a file that keeps both sides' changes is.
func TestUnreconciled_APickedSideIsNotAReconciliation(t *testing.T) {
	for _, c := range []struct {
		name string
		left func(rec *Reconciliation) string
		want bool
	}{
		{"a marker left", func(rec *Reconciliation) string {
			return strings.Replace(rec.Seeded["main.go"].Text, "// lane words\n", "// both words\n", 1)
		}, true},
		{"the file as it was seeded", func(rec *Reconciliation) string { return rec.Seeded["main.go"].Text }, true},
		{"the landed side whole", func(rec *Reconciliation) string { return rec.Sides["main.go"][0].Text }, true},
		{"the lane's side whole", func(rec *Reconciliation) string { return rec.Sides["main.go"][1].Text }, true},
		{"both sides kept", func(*Reconciliation) string {
			return strings.Replace(mergingBase, "// three", "// landed words, lane words", 1)
		}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			landed, lane := mergingPair(t, editThree("landed words"), editThree("lane words"))
			rec, err := ReseedMerging(context.Background(), lane.Dir, landed, nil, "landed-item", "lane-item")
			if err != nil {
				t.Fatal(err)
			}
			writeInto(t, lane.Root, "main.go", c.left(rec))
			patch, err := WorktreePatch(lane.Dir)
			if err != nil {
				t.Fatal(err)
			}
			files := append(Unreconciled(lane.Dir, patch, rec.Unsettled, rec.Seeded), PickedSide(lane.Dir, rec.Sides)...)
			if got := slices.Contains(files, "main.go"); got != c.want {
				t.Fatalf("judged unreconciled %v, want %v: %v", got, c.want, files)
			}
		})
	}
}

// A turn that deletes a conflicting file keeps neither side's change, so it is
// not a reconciliation; where one side of the merge was itself the file's
// deletion, leaving it absent is that side rather than a file made to vanish.
func TestUnreconciled_ADeletedFileIsNotAReconciliation(t *testing.T) {
	for _, c := range []struct {
		name       string
		landedGone bool
		want       bool
	}{
		{"both sides kept the file", false, true},
		{"the landed side deleted it", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			landed, lane := mergingPair(t, editThree("landed words"), editThree("lane words"))
			if c.landedGone {
				landed = deletedIn(t, lane.RepoTop)
			}
			rec, err := ReseedMerging(context.Background(), lane.Dir, landed, nil, "landed-item", "lane-item")
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(rec.Unsettled, "main.go") || rec.Seeded["main.go"].SideDeleted != c.landedGone {
				t.Fatalf("main.go is unsettled, its seed saying whether a side deleted it: %+v", rec.Seeded)
			}
			if err := os.Remove(filepath.Join(lane.Root, "main.go")); err != nil {
				t.Fatal(err)
			}
			patch, err := WorktreePatch(lane.Dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := slices.Contains(Unreconciled(lane.Dir, patch, rec.Unsettled, rec.Seeded), "main.go"); got != c.want {
				t.Fatalf("the deleted file judged unreconciled %v, want %v", got, c.want)
			}
		})
	}
}

// deletedIn is a patch, made in a copy of its own, that deletes main.go.
func deletedIn(t *testing.T, repo string) string {
	t.Helper()
	other, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { RemoveWorktree(other.RepoTop, other.Dir) })
	if err := os.Remove(filepath.Join(other.Root, "main.go")); err != nil {
		t.Fatal(err)
	}
	patch, err := WorktreePatch(other.Dir)
	if err != nil {
		t.Fatal(err)
	}
	return patch
}
