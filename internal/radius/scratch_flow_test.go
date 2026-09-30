package radius

import "testing"

// The scratch reading takes a flow word for what it is now that both readings
// read past one: a delete of scratch behind `!` or inside a group is the same
// delete, a delete of tracked work behind one is still seen, and a
// conditional or a loop keeps its card for the words that close it.
func TestScratchDelete_AFlowWordIsReadPast(t *testing.T) {
	_, w := scratchRepo(t)
	cases := []struct {
		command string
		want    bool
	}{
		{"! rm -rf .tmp/test-build", true},
		{"{ rm -rf .tmp/test-build; }", true},
		{"! rm -rf src", false},
		{"{ rm -rf .tmp/test-build; rm -rf src; }", false},
		{"if true; then rm -rf .tmp/test-build; fi", false},
		{"while false; do rm -rf .tmp/test-build; done", false},
		{"nice -n 5 rm -rf src", false},
		{"timeout 5 rm -rf src; rm -rf .tmp/test-build", false},
	}
	for _, c := range cases {
		if got := ScratchDelete(c.command, w); got != c.want {
			t.Errorf("ScratchDelete(%q) = %v, want %v", c.command, got, c.want)
		}
	}
}
