// Package hostgittest builds the repository a contained command could leave
// behind for the host's next git call, so every package that runs git on the
// host can hold its own call to running nothing from it.
package hostgittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// SubmoduleName is the path, under the root PlantedSubmodule returns, of the
// submodule it stages.
const SubmoduleName = "sub"

// Planted is a scratch superproject holding a gitlink whose submodule store
// names a clean filter that leaves Marker behind when anything runs it.
type Planted struct {
	Root   string
	Marker string
}

// PlantedSubmodule builds the repository: a superproject with one commit, a
// repository made inside it and staged as a gitlink, and in that
// repository's own store a clean filter every path is given. It is what a
// contained command can make with nothing but write access to the working
// tree, since a submodule's store is not the superproject's and no mask over
// the superproject's names it. Skips where git is not on PATH.
func PlantedSubmodule(t testing.TB) Planted {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	sub := filepath.Join(root, SubmoduleName)

	write(t, filepath.Join(root, "main.go"), "package main\n")
	run(t, root, "init", "-q")
	run(t, root, "add", "main.go")
	run(t, root, "commit", "-q", "-m", "init")

	write(t, filepath.Join(sub, "a.txt"), "a\n")
	run(t, sub, "init", "-q")
	run(t, sub, "add", "a.txt")
	run(t, sub, "commit", "-q", "-m", "sub")
	// .gitmodules is a working-tree file, and its ignore = none outranks
	// diff.ignoreSubmodules in any configuration: without it, a guard that
	// only set the key would pass here and fail against a real attempt.
	write(t, filepath.Join(root, ".gitmodules"),
		"[submodule \""+SubmoduleName+"\"]\n\tpath = "+SubmoduleName+"\n\turl = ./"+SubmoduleName+"\n\tignore = none\n")
	run(t, root, "add", SubmoduleName, ".gitmodules")
	run(t, root, "commit", "-q", "-m", "gitlink")

	run(t, sub, "config", "filter.planted.clean", "sh -c 'touch "+marker+"; cat'")
	write(t, filepath.Join(sub, ".gitattributes"), "* filter=planted\n")
	p := Planted{Root: root, Marker: marker}
	p.Stir(t)
	return p
}

// Stir gives the submodule's file a modification time its index does not
// record, with the content unchanged, so the next git that asks the
// submodule whether it is dirty has to hash the file — through the filter —
// to find out. It also clears the marker, so Ran answers for what came after.
func (p Planted) Stir(t testing.TB) {
	t.Helper()
	// Two seconds back from wherever it stands, rather than to a fixed
	// time: git compares whole seconds where it was built without
	// nanosecond stamps, and two stirs inside one second would otherwise
	// leave the file as the last reading's refresh recorded it.
	path := filepath.Join(p.Root, SubmoduleName, "a.txt")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	at := info.ModTime().Add(-2 * time.Second)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p.Marker); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// Ran reports whether the planted filter has run since the last Stir.
func (p Planted) Ran() bool {
	_, err := os.Stat(p.Marker)
	return err == nil
}

// Control runs git in the superproject with none of shhh's hygiene, and
// fails the test unless the filter ran: a fixture that no bare git falls for
// proves nothing about the calls that are guarded against it.
func (p Planted) Control(t testing.TB, args ...string) {
	t.Helper()
	p.Stir(t)
	cmd := exec.Command("git", append([]string{"-C", p.Root}, args...)...)
	cmd.Env = env()
	_ = cmd.Run()
	if !p.Ran() {
		t.Fatalf("a bare git %s should have run the planted filter; the fixture proves nothing as it stands", strings.Join(args, " "))
	}
}

func run(t testing.TB, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = env()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

// env is this process's environment with the person's configuration and any
// inherited override set aside, and an author for the fixture's commits.
func env() []string {
	var kept []string
	for _, pair := range os.Environ() {
		if !strings.HasPrefix(pair, "GIT_") {
			kept = append(kept, pair)
		}
	}
	return append(kept,
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
}

func write(t testing.TB, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
