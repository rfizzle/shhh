package chat

import (
	"testing"

	"github.com/rfizzle/shhh/internal/changeset"
)

// The reviewer's diff is the unified diff internal/diff renders, byte for
// byte, with /dev/null standing for a side that does not exist.
func TestRecordDiffSpellsTheUnifiedDiffExactly(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    changeset.Record
		want string
	}{
		{"edited", changeset.Record{Before: "one\ntwo\n", After: "one\nTWO\n", BeforeExists: true, AfterExists: true},
			"--- a/x.go\n+++ b/x.go\n@@ -1,2 +1,2 @@\n one\n-two\n+TWO\n"},
		{"created", changeset.Record{After: "new\n", AfterExists: true},
			"--- /dev/null\n+++ b/x.go\n@@ -0,0 +1,1 @@\n+new\n"},
		{"deleted", changeset.Record{Before: "old\n", BeforeExists: true},
			"--- a/x.go\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-old\n"},
	} {
		if got := recordDiff("x.go", tc.r); got != tc.want {
			t.Errorf("%s: recordDiff =\n%q\nwant\n%q", tc.name, got, tc.want)
		}
	}
}
