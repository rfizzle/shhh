package worktree

import (
	"fmt"
	"strings"
	"testing"
)

// A patch of two hundred files is a page of the parent's context spent on a
// list it would then have to summarise; the count beside the names says the
// size either way.
func TestAPatchNoteBoundsAVeryLongFileList(t *testing.T) {
	files := make([]string, maxNotedPatchPaths+5)
	for i := range files {
		files[i] = fmt.Sprintf("pkg/file%d.go", i)
	}
	got := PatchPaths(files)
	if !strings.Contains(got, "and 5 more") {
		t.Fatalf("a long list must say how many it left out, got %q", got)
	}
	if strings.Contains(got, files[maxNotedPatchPaths]) {
		t.Fatalf("the list is not bounded: %q", got)
	}
}
