package keys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeKeymap(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keybindings.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The scaffold as written is current: nothing is missing and the update
// hands the text back as it was.
func TestKeymapUpdated_TheScaffoldIsCurrent(t *testing.T) {
	path := writeKeymap(t, Scaffold())
	b, err := KeymapOutdated(path)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Listed || len(b.Missing) != 0 {
		t.Fatalf("the scaffold reads as behind: %+v", b)
	}
	text, added, err := KeymapUpdated(path)
	if err != nil || added != 0 || text != Scaffold() {
		t.Fatalf("a current file was changed: added %d, err %v", added, err)
	}
}

// A file written before a key and a whole group arrived gets both back as
// the scaffold writes them — the key under the table it belongs to, the
// group as a table of its own at the end — and the binding the person set
// stays exactly as they wrote it.
func TestKeymapUpdated_AddsWhatArrivedAndKeepsTheBindings(t *testing.T) {
	var movable []Group
	for _, g := range keyboard(true) {
		if g.Movable {
			movable = append(movable, g)
		}
	}
	if len(movable) < 2 {
		t.Fatal("the register has too few groups to test with")
	}
	first, last := movable[0], movable[len(movable)-1]
	set := first.Acts[0]
	arrived := first.Acts[len(first.Acts)-1]
	if tableOf(arrived.Name) != tableOf(set.Name) || arrived.Name == set.Name {
		t.Fatalf("the first group's first and last keys are not rows of one table: %s, %s", set.Name, arrived.Name)
	}
	lastTable := "\n[" + tableOf(last.Acts[0].Name) + "]\n"
	if at := strings.Index(Scaffold(), lastTable); at < 0 || strings.Contains(Scaffold()[at+1:], "\n[") {
		t.Fatalf("the last group is not one table at the end of the scaffold: %s", lastTable)
	}

	// What the person has: the scaffold of the day, one key bound, without
	// the key and the group that arrived since.
	current := strings.Replace(Scaffold(), scaffoldRow(tableOf(set.Name), set),
		strings.TrimPrefix(scaffoldRow(tableOf(set.Name), set), "# "), 1)
	older := strings.Replace(current, scaffoldRow(tableOf(arrived.Name), arrived), "", 1)
	older = older[:strings.Index(older, lastTable)]

	path := writeKeymap(t, older)
	b, err := KeymapOutdated(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := 1 + len(last.Acts); b.KeysBehind() != want || b.Missing[0] != arrived.Name {
		t.Fatalf("behind by %d (%v), want %d starting at %s", b.KeysBehind(), b.Missing, want, arrived.Name)
	}
	text, added, err := KeymapUpdated(path)
	if err != nil {
		t.Fatal(err)
	}
	if added != 1+len(last.Acts) {
		t.Errorf("added %d keys, want %d", added, 1+len(last.Acts))
	}
	if text != current {
		t.Fatalf("the updated file is not the scaffold with the person's binding:\n%s", text)
	}
}

// A file of a few lines written on purpose lists nothing, so nothing counts
// against it; the update still adds every key when asked, and the person's
// lines stay the file's first bytes.
func TestKeymapOutdated_AHandWrittenFileIsBehindByNothing(t *testing.T) {
	const own = "# my keys\n[reading]\ncopy = \"c\"\n"
	path := writeKeymap(t, own)
	b, err := KeymapOutdated(path)
	if err != nil {
		t.Fatal(err)
	}
	if b.Listed || b.KeysBehind() != 0 || len(b.Missing) == 0 {
		t.Fatalf("a hand-written file reads as %+v", b)
	}
	text, added, err := KeymapUpdated(path)
	if err != nil || added != len(b.Missing) {
		t.Fatalf("added %d of %d: %v", added, len(b.Missing), err)
	}
	if !strings.HasPrefix(text, "# my keys\n[reading]\ncopy = \"c\"\n") {
		t.Fatalf("the person's lines moved:\n%s", text)
	}
}

// A file that does not read is refused rather than guessed at, and a file
// that is not there is behind by nothing.
func TestKeymapUpdated_RefusesAFileThatDoesNotRead(t *testing.T) {
	if _, _, err := KeymapUpdated(writeKeymap(t, "[reading\n")); err == nil {
		t.Fatal("a file that does not parse was updated")
	}
	b, err := KeymapOutdated(filepath.Join(t.TempDir(), "keybindings.toml"))
	if err != nil || b.Listed || len(b.Missing) != 0 {
		t.Fatalf("a missing file reads as %+v, %v", b, err)
	}
}
