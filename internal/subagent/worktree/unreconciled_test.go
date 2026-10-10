package worktree

import (
	"context"
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
