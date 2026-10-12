package tools

import "testing"

func TestShellEdit_ReadsAnInPlaceEditAndNothingElse(t *testing.T) {
	scope := ShellScope{Workspace: "/work/proj", Scratch: []string{"/tmp", "/work/proj/.cache"}}
	cases := []struct {
		line string
		want bool
	}{
		{"sed -i 's/a/b/' main.go", true},
		{"sed -i'' 's/a/b/' main.go", true},
		{"sed -i '' 's/a/b/' main.go", true},
		{"sed -i.bak 's/a/b/' main.go", true},
		{"sed -n -i 's/a/b/' main.go", true},
		{"sed --in-place 's/a/b/' main.go", true},
		{"sed 's/a/b/' main.go", false},
		{"sed -n '1,5p' main.go", false},
		{"sed -e 's/i/x/' main.go", false},
		{"perl -i -pe 's/a/b/' main.go", true},
		{"perl -pi -e 's/a/b/' main.go", true},
		{"perl -pie 's/a/b/' main.go", true},
		{"perl -i.bak -pe 's/a/b/' main.go", true},
		{"perl -e 'print 1'", false},
		{"echo hi > notes.txt", true},
		{"echo hi >> notes.txt", true},
		{"echo hi >notes.txt", true},
		{"echo hi > /work/proj/sub/notes.txt", true},
		{"cat > main.go <<EOF\npackage main\nEOF", true},
		{"echo hi | tee notes.txt", true},
		{"echo hi | tee -a notes.txt", true},
		{"echo hi | tee", false},
		{"find . -name '*.go' | xargs sed -i 's/a/b/'", true},
		{"find . -name '*.go' -exec sed -i 's/a/b/' {} +", true},
		{"echo hi > /dev/null", false},
		{"go test ./... 2>&1", false},
		{"go test ./... 2>&1 | tail", false},
		{"go test ./... 2>err.log", false},
		{"echo 'a > b' | wc", false},
		{"echo '>' notes.txt", false},
		{"echo hi > /etc/hosts", false},
		{"echo hi > ../elsewhere.txt", false},
		{"echo hi > /tmp/out.txt", false},
		{"echo hi > .cache/out.txt", false},
		{"echo hi > $HOME/out.txt", false},
		{"cat main.go | grep x", false},
		{"python - <<EOF\nif a > b:\n  pass\nEOF", false},
		{"echo 'unterminated > x", false},
		{"", false},
	}
	for _, c := range cases {
		if got := ShellEdit(c.line, scope); got != c.want {
			t.Errorf("ShellEdit(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

func TestShellEdit_ASessionWithNoWorkspaceReadsOnlyTheFlags(t *testing.T) {
	if !ShellEdit("sed -i s/a/b/ f", ShellScope{}) {
		t.Error("sed -i not read with no workspace")
	}
	if ShellEdit("echo hi > f", ShellScope{}) {
		t.Error("a redirection was read with no workspace to be inside of")
	}
}

func TestShellEdit_AScratchDirectoryHoldingTheWorkspaceIsNotScratch(t *testing.T) {
	scope := ShellScope{Workspace: "/tmp/checkout", Scratch: []string{"/tmp"}}
	if !ShellEdit("echo hi > notes.txt", scope) {
		t.Error("a workspace under /tmp had every path read as scratch")
	}
}
