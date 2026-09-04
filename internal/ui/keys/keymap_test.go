package keys

// The rebinding layer against the register it rewrites.
//
// Three things are worth asserting. That a move lands where every surface
// and every hint already reads — which is the whole claim of declaring a key
// once. That the two refusals fire and say which key and which acts, because
// a keymap that silently did something else is worse than one that did
// nothing. And that a refusal leaves nothing behind: the register a refused
// file was applied to is the register shhh declared, keystroke for keystroke.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// restoreRegister puts the declarations back after a test has moved them.
// The register is package data on purpose — that is what lets a hint and a
// handler read one fact — so a test that moves a key moves it for the whole
// package until this runs.
func restoreRegister(t *testing.T) {
	t.Helper()
	saved := map[string]reflect.Value{}
	for name, group := range groups() {
		was := reflect.New(group.Type()).Elem()
		was.Set(group)
		saved[name] = was
	}
	t.Cleanup(func() {
		for name, group := range groups() {
			group.Set(saved[name])
		}
	})
}

// keymapFile writes one keymap into a scratch directory and gives back its
// path.
func keymapFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keybindings.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A move lands on the declaration itself, so the handler that matches the
// key and the hint that prints it are both the moved one. The words do not
// move with it: a key that changed is still the same act.
func TestLoad_AValidMoveReachesTheRegister(t *testing.T) {
	restoreRegister(t)
	was := Words(Reading.Copy)
	path := keymapFile(t, `
[reading]
copy = "c"

[draft]
history_search = ["ctrl+r", "alt+r"]
`)
	if err := Load(path); err != nil {
		t.Fatalf("a valid keymap was refused: %v", err)
	}
	if !Is("c", Reading.Copy) || Is("y", Reading.Copy) {
		t.Errorf("the copy key answers %v, want c alone", Reading.Copy.Keys())
	}
	if got := Shown(Reading.Copy); got != "c" {
		t.Errorf("the hint prints %q, want c", got)
	}
	if got := Words(Reading.Copy); got != was {
		t.Errorf("the words moved with the key: %q, want %q", got, was)
	}
	// Several keystrokes are one binding, spelled the way the register
	// spells its own pairs.
	if !Is("ctrl+r", Draft.HistorySearch) || !Is("alt+r", Draft.HistorySearch) {
		t.Errorf("the search answers %v", Draft.HistorySearch.Keys())
	}
	if got := Shown(Draft.HistorySearch); got != "ctrl+r/alt+r" {
		t.Errorf("the hint prints %q", got)
	}
	// And the register the surfaces read is the moved one, which is the
	// point of moving the declaration rather than a copy of it.
	for _, s := range Surfaces() {
		if s.Name != "reading mode" {
			continue
		}
		for _, b := range s.Bindings {
			if Words(b) == was && Shown(b) != "c" {
				t.Errorf("reading mode still offers %q", Shown(b))
			}
		}
	}
}

// A key nested inside a group is reached the way the register nests it, and
// a name may be written however a person guesses it.
func TestLoad_ReachesANestedKeyByEitherSpelling(t *testing.T) {
	restoreRegister(t)
	path := keymapFile(t, `
[select.palette]
next = "ctrl+j"

[select]
MoveJK = ["up", "down", "j", "k"]
`)
	if err := Load(path); err != nil {
		t.Fatalf("a valid keymap was refused: %v", err)
	}
	if !Is("ctrl+j", Select.Palette.Next) {
		t.Errorf("the palette's next answers %v", Select.Palette.Next.Keys())
	}
	if !Is("j", Select.MoveJK) {
		t.Errorf("MoveJK answers %v", Select.MoveJK.Keys())
	}
}

// The departure the agent manager records is the rule: it gave up the
// lower-case letter because a movement key that also kills a process is the
// worst kind of false offer, and a file may not take it back.
func TestLoad_RefusesADestructiveActOnAMovementKey(t *testing.T) {
	restoreRegister(t)
	path := keymapFile(t, "[agent]\nkill = \"k\"\n")
	err := Load(path)
	if err == nil {
		t.Fatal("a movement key bound to kill should be refused")
	}
	for _, want := range []string{"\"k\"", "kill"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
	if !Is("X", Agent.Kill) || Is("k", Agent.Kill) {
		t.Errorf("a refused file left the register at %v", Agent.Kill.Keys())
	}
}

// The register's own rule, asked of a file: a surface that answered one
// keystroke with two acts is a surface where the first case of a switch
// silently wins.
func TestLoad_RefusesTwoActsOnOneKeystrokeOnOneSurface(t *testing.T) {
	restoreRegister(t)
	path := keymapFile(t, "[agent]\nattach = \"a\"\n")
	err := Load(path)
	if err == nil {
		t.Fatal("two acts on one key should be refused")
	}
	for _, want := range []string{"the agent manager", "\"a\"", "attach", "answer"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
	if !Is("enter", Agent.Attach) || Is("a", Agent.Attach) {
		t.Errorf("a refused file left the register at %v", Agent.Attach.Keys())
	}
}

// A file is refused whole rather than in part: a keyboard half a file long
// is one nobody has ever seen, and the reader would be debugging it against
// a document that describes neither their file nor the register.
func TestLoad_RefusesTheWholeFileNotTheBadLine(t *testing.T) {
	restoreRegister(t)
	path := keymapFile(t, "[reading]\ncopy = \"c\"\n\n[agent]\nkill = \"j\"\n")
	if err := Load(path); err == nil {
		t.Fatal("the file should be refused")
	}
	if !Is("y", Reading.Copy) {
		t.Errorf("the good half of a refused file was kept: %v", Reading.Copy.Keys())
	}
}

// A name the register does not have is a mistake worth saying out loud. It
// is the same reading config.toml gives a key no setting reads: the loosest
// file must not be the one somebody wrote by hand.
func TestLoad_RefusesAKeyTheRegisterDoesNotHave(t *testing.T) {
	restoreRegister(t)
	for _, body := range []string{
		"[draft]\nteleport = \"ctrl+q\"\n",
		"[nosuchsurface]\nmove = \"ctrl+q\"\n",
		"[draft]\nsend = []\n",
	} {
		err := Load(keymapFile(t, body))
		if err == nil {
			t.Errorf("%q should be refused", body)
		}
	}
	if !Is("enter", Draft.Send) {
		t.Errorf("a refused file left the register at %v", Draft.Send.Keys())
	}
}

// No file is the answer for most people, and it is not an error. The first
// file that exists wins, the way the config paths already resolve.
func TestLoad_NoFileIsNotAnError(t *testing.T) {
	restoreRegister(t)
	missing := filepath.Join(t.TempDir(), "keybindings.toml")
	if err := Load(missing); err != nil {
		t.Errorf("a machine with no keymap is not an error: %v", err)
	}
	present := keymapFile(t, "[reading]\ncopy = \"c\"\n")
	if err := Load(missing, present); err != nil {
		t.Fatalf("the second path should have been read: %v", err)
	}
	if !Is("c", Reading.Copy) {
		t.Errorf("the file that exists was not applied: %v", Reading.Copy.Keys())
	}
}

// Every group the register declares is reachable from a file. A group left
// out of the table would be a set of keys nobody can move, and nothing about
// the file's shape would say why.
func TestEveryDeclaredGroupIsReachable(t *testing.T) {
	named := map[string]bool{}
	for _, group := range groups() {
		named[group.Type().Name()] = true
	}
	for _, want := range []string{
		"DraftKeys", "SearchKeys", "ReadingKeys", "FindKeys", "ContextKeys",
		"RowKeys", "DecisionKeys", "ConfirmKeys", "SelectKeys", "ReviewKeys",
		"AgentKeys", "ProfileKeys", "WaitKeys", "DiffKeys", "OutputKeys",
		"PreviewKeys", "ScreenKeys", "OneShotKeys", "SetupKeys", "BrowseKeys",
	} {
		if !named[want] {
			t.Errorf("%s is declared and no keymap file can reach it", want)
		}
	}
}
