package scope

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/sandbox"
)

// newScope is the test constructor: a scope rooted at dir with no problems,
// which is what every case here starts from.
func newScope(t *testing.T, root string, dirs ...string) *Scope {
	t.Helper()
	s, problems := New(root, dirs...)
	if s == nil {
		t.Fatalf("New(%q) returned no scope: %v", root, problems)
	}
	return s
}

func TestContainsRootAndAddedDirs(t *testing.T) {
	root := t.TempDir()
	side := t.TempDir()
	s := newScope(t, root)

	if !s.Contains(filepath.Join(root, "cmd", "main.go")) {
		t.Error("a path under the root must be in scope even before it exists")
	}
	if s.Contains(filepath.Join(side, "notes.md")) {
		t.Error("a path in another tree must not be in scope")
	}
	if _, err := s.Add(side); err != nil {
		t.Fatalf("Add(%q) = %v", side, err)
	}
	if !s.Contains(filepath.Join(side, "notes.md")) {
		t.Error("a granted directory must put its contents in scope")
	}
}

func TestAddReportsAlreadyInScope(t *testing.T) {
	root := t.TempDir()
	s := newScope(t, root)
	if _, err := s.Add(root); err != ErrAlreadyInScope {
		t.Fatalf("adding the root again = %v, want ErrAlreadyInScope", err)
	}
	side := t.TempDir()
	if _, err := s.Add(side); err != nil {
		t.Fatalf("Add = %v", err)
	}
	if _, err := s.Add(side); err != ErrAlreadyInScope {
		t.Fatalf("adding a granted directory twice = %v, want ErrAlreadyInScope", err)
	}
	if got := len(s.Dirs()); got != 1 {
		t.Fatalf("the scope holds %d directories, want 1", got)
	}
}

func TestAddRefusesFiles(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "notes.md")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newScope(t, t.TempDir())
	if _, err := s.Add(file); err == nil {
		t.Fatal("a file is not a directory and must not be added")
	}
}

func TestAddRefusesMaskedPaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory on this host")
	}
	ssh := filepath.Join(home, ".ssh")
	if _, err := os.Stat(ssh); err != nil {
		t.Skip("no ~/.ssh on this host")
	}
	s := newScope(t, t.TempDir())
	if _, err := s.Add(ssh); err == nil {
		t.Fatal("a directory behind the containment deny mask must never be added")
	}
	if class, reason := Classify(ssh); class != Refused || reason == "" {
		t.Fatalf("Classify(~/.ssh) = %v, %q; want Refused with a reason", class, reason)
	}
}

// The split between the two credential classes, which is a judgement about
// writing rather than about reading: a password store is somewhere nothing
// legitimate writes, so it is refused outright and there is nothing to ask
// about; a kubeconfig is somewhere a person may genuinely be working, so it
// is theirs to grant — and that grant is also what lets a contained command
// read it, which is why the reason says so.
func TestClassifySplitsTheCredentialStoresByWhetherAGrantIsEverHonest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range []string{".gnupg", ".password-store", ".secrets"} {
		dir := filepath.Join(home, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if class, reason := Classify(dir); class != Refused || reason == "" {
			t.Errorf("Classify(~/%s) = %v, %q; want Refused with a reason", name, class, reason)
		}
		if _, err := newScope(t, t.TempDir()).Add(dir); err == nil {
			t.Errorf("~/%s must never be granted", name)
		}
	}
	for _, name := range []string{".kube", ".docker", ".azure", ".gem"} {
		dir := filepath.Join(home, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if class, reason := Classify(dir); class != Sensitive || reason == "" {
			t.Errorf("Classify(~/%s) = %v, %q; want Sensitive with a reason", name, class, reason)
		}
		if _, err := newScope(t, t.TempDir()).Add(dir); err != nil {
			t.Errorf("~/%s must stay grantable by the person whose credentials it is: %v", name, err)
		}
	}
}

func TestClassifyMakesShhhDirectoriesSensitiveButGrantable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	for _, dir := range sandbox.ShhhPaths() {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if class, reason := Classify(dir); class != Sensitive || !strings.Contains(reason, "shhh's own") {
			t.Errorf("Classify(%s) = %v, %q; want sensitive shhh directory", dir, class, reason)
		}
		if _, err := newScope(t, t.TempDir()).Add(dir); err != nil {
			t.Errorf("a person must be able to grant %s while developing shhh: %v", dir, err)
		}
	}
}

// The blast radius a person answering the card has to be told about: the
// mask is per store and cannot be given a hole, so a grant of something
// inside one exposes everything beside it. A reason that named only the
// directory asked for would undersell that by a whole kubeconfig.
func TestClassifyWarnsThatAGrantInsideAStoreExposesTheWholeStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	kube := filepath.Join(home, ".kube")
	if err := os.MkdirAll(filepath.Join(kube, "cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	class, reason := Classify(filepath.Join(kube, "cache"))
	if class != Sensitive {
		t.Fatalf("Classify(~/.kube/cache) = %v, want Sensitive", class)
	}
	if !strings.Contains(reason, "whole store") || !strings.Contains(reason, kube) {
		t.Errorf("the reason must name the store and say the grant reaches all of it, got %q", reason)
	}
}

func TestClassifySensitiveAndOrdinary(t *testing.T) {
	if class, _ := Classify(t.TempDir()); class != Ordinary {
		t.Errorf("a fresh directory should be ordinary, got %v", class)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory on this host")
	}
	if class, reason := Classify(home); class != Sensitive || reason == "" {
		t.Errorf("Classify(home) = %v, %q; want Sensitive with a reason", class, reason)
	}
	if class, _ := Classify(string(filepath.Separator)); class != Sensitive {
		t.Error("the filesystem root should be sensitive")
	}
}

func TestOutsideNamesDirectoriesOnceInOrder(t *testing.T) {
	root := t.TempDir()
	a, b := t.TempDir(), t.TempDir()
	s := newScope(t, root)

	got := s.Outside(
		filepath.Join(root, "in-scope.txt"),
		filepath.Join(a, "one.txt"),
		filepath.Join(a, "two.txt"),
		filepath.Join(b, "three.txt"),
	)
	if len(got) != 2 {
		t.Fatalf("Outside = %v, want two directories", got)
	}
	if got[0] != resolved(t, a) || got[1] != resolved(t, b) {
		t.Fatalf("Outside = %v, want %s then %s in the order the paths named them", got, a, b)
	}
}

func TestOutsideAnswersForPathsThatDoNotExistYet(t *testing.T) {
	root := t.TempDir()
	side := t.TempDir()
	s := newScope(t, root)
	// The nearest existing directory answers for a file the command creates.
	if got := s.Outside(filepath.Join(side, "new", "file.txt")); len(got) != 1 {
		t.Fatalf("Outside = %v, want the existing ancestor", got)
	}
	if got := s.Outside(filepath.Join(root, "new", "file.txt")); len(got) != 0 {
		t.Fatalf("Outside = %v, want nothing: the ancestor is the root", got)
	}
}

func TestSymlinkCannotSmuggleAPathIntoScope(t *testing.T) {
	root := t.TempDir()
	side := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(side, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	s := newScope(t, root)
	if s.Contains(filepath.Join(link, "file.txt")) {
		t.Error("a link out of the root must not put its target in scope")
	}
}

func TestDropTakesADirectoryBackAndKeepsTheRoot(t *testing.T) {
	root := t.TempDir()
	side := t.TempDir()
	s := newScope(t, root, side)
	if len(s.Dirs()) != 1 {
		t.Fatalf("New should have granted the listed directory, got %v", s.Dirs())
	}
	if _, ok := s.Drop(side); !ok {
		t.Fatal("Drop should report the directory it removed")
	}
	if s.Contains(filepath.Join(side, "x")) {
		t.Error("a dropped directory must leave the scope")
	}
	if _, ok := s.Drop(root); ok {
		t.Error("the root is not droppable")
	}
	if !s.Contains(filepath.Join(root, "x")) {
		t.Error("the root must stay in scope")
	}
}

func TestNewReportsUnusableDirectoriesWithoutLosingTheScope(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(t.TempDir(), "gone")
	s, problems := New(root, missing)
	if s == nil {
		t.Fatal("a bad entry must not cost the session its scope")
	}
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want one", problems)
	}
	if len(s.Dirs()) != 0 {
		t.Fatalf("nothing should have been granted, got %v", s.Dirs())
	}
}

func TestNilScopeAnswersRatherThanPanicking(t *testing.T) {
	var s *Scope
	if s.Contains("/anywhere") || s.Dirs() != nil || s.All() != nil || s.Outside("/x") != nil {
		t.Error("a nil scope answers empty")
	}
	if _, err := s.Add("/tmp"); err == nil {
		t.Error("a nil scope cannot grant anything")
	}
}

func resolved(t *testing.T, dir string) string {
	t.Helper()
	out, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
