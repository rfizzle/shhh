package radius

import (
	"os"
	"path/filepath"
	"testing"
)

// ProvenInside is the reading a bounded clean-up is judged on, and it must
// answer yes only where every target is proven below the workspace root: a
// link out of it, an unresolved word, or the root itself is a no.
func TestDestruction_ProvenInside(t *testing.T) {
	base := t.TempDir()
	home, ws := filepath.Join(base, "home"), filepath.Join(base, "ws")
	for _, d := range []string{home, filepath.Join(ws, ".tmp", "test-build")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(home, filepath.Join(ws, ".tmp", "out")); err != nil {
		t.Fatal(err)
	}
	w := Where{Dir: ws, Root: ws, Home: home}
	cases := []struct {
		command string
		want    bool
	}{
		{"rm -rf .tmp/test-build", true},
		{"rm -rf ./.tmp/test-build .tmp/new", true},
		{"rm -rf .tmp/out/", false},
		{"rm -rf .tmp/test-build $X", false},
		{"rm -rf .", false},
		{"rm -rf ~", false},
		{"ls .tmp", false},
	}
	for _, c := range cases {
		if got := Destroys(c.command, w).ProvenInside(ws); got != c.want {
			t.Errorf("ProvenInside(%q) = %v, want %v", c.command, got, c.want)
		}
	}
}

// A relative target is measured from the directory the command runs in, and
// a process started somewhere the reading is not told proves nothing by one.
func TestDestroys_ARelativeTargetNeedsItsDirectory(t *testing.T) {
	ws := t.TempDir()
	d := Destroys("rm -rf .", Where{Root: ws})
	if d.Refusal() != "" || len(d.Unresolved) == 0 {
		t.Fatalf("a relative target with no directory proved something: %+v", d)
	}
	if got := Destroys("rm -rf .", Where{Dir: ws, Root: ws}).Refusal(); got == "" {
		t.Fatal("the same target from the workspace root was not refused")
	}
}
