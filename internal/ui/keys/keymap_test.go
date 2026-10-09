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
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
copy = "x"

[draft]
history_search = ["ctrl+r", "alt+r"]
`)
	if err := Load(path); err != nil {
		t.Fatalf("a valid keymap was refused: %v", err)
	}
	if !Is("x", Reading.Copy) || Is("y", Reading.Copy) {
		t.Errorf("the copy key answers %v, want x alone", Reading.Copy.Keys())
	}
	if got := Shown(Reading.Copy); got != "x" {
		t.Errorf("the hint prints %q, want x", got)
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
			if Words(b) == was && Shown(b) != "x" {
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
next = "ctrl+]"

[select]
MoveJK = ["up", "down", "j", "k"]
`)
	if err := Load(path); err != nil {
		t.Fatalf("a valid keymap was refused: %v", err)
	}
	if !Is("ctrl+]", Select.Palette.Next) {
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

// The input's own rule, asked of a file: a bare key at the draft is a letter
// of whatever is being typed, so a file that put the palette on `p` would
// take that letter out of every prompt the reader ever writes.
func TestLoad_RefusesABareKeyAtTheDraft(t *testing.T) {
	restoreRegister(t)
	path := keymapFile(t, "[draft]\npalette = \"p\"\n")
	err := Load(path)
	if err == nil {
		t.Fatal("a bare key at the draft should be refused")
	}
	for _, want := range []string{"\"p\"", "the command palette", "chord"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
	if !Is("ctrl+/", Draft.Palette) || Is("p", Draft.Palette) {
		t.Errorf("a refused file left the register at %v", Draft.Palette.Keys())
	}
}

// The same rule on the editor pane, where every letter is the file's: a file
// that put the save on `s` would take that letter out of every file a person
// edits there.
func TestLoad_RefusesABareKeyInTheEditor(t *testing.T) {
	restoreRegister(t)
	err := Load(keymapFile(t, "[editor]\nsave = \"s\"\n"))
	if err == nil {
		t.Fatal("a bare key in the editor pane should be refused")
	}
	for _, want := range []string{"\"s\"", "the editor pane", "chord"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
	if !Is("ctrl+s", Editor.Save) {
		t.Errorf("a refused file left the save at %v", Editor.Save.Keys())
	}
}

// And the two the rule excepts are still a file's to spend. Esc is the input's
// own already, so the file has to move it off the key it is on first — which
// is the whole demonstration: the refusal above is about letters, and esc is
// refused nowhere.
func TestLoad_TakesEscAtTheDraft(t *testing.T) {
	restoreRegister(t)
	path := keymapFile(t, "[draft]\nclear = \"f4\"\nnewline = [\"esc\"]\n")
	if err := Load(path); err != nil {
		t.Fatalf("esc at the draft was refused: %v", err)
	}
	if !Is("esc", Draft.Newline) {
		t.Errorf("the newline answers %v", Draft.Newline.Keys())
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

// The same rule on the decision card, which is the surface where a pair of
// answers and their noted spellings sit one shift apart: a file that moved
// one of them onto the other's letter would be a card whose no ran the
// command, and the first case of a switch would decide which.
func TestLoad_RefusesANotedAnswerOnItsPlainAnswersKey(t *testing.T) {
	restoreRegister(t)
	path := keymapFile(t, "[decision]\ndeny_noted = \"y\"\n")
	err := Load(path)
	if err == nil {
		t.Fatal("a noted answer bound to the other answer's key should be refused")
	}
	for _, want := range []string{"the approval card", "\"y\"", "allow"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
	if !Is("N", Decision.DenyNoted) || Is("y", Decision.DenyNoted) {
		t.Errorf("a refused file left the register at %v", Decision.DenyNoted.Keys())
	}
}

// A file is refused whole rather than in part: a keyboard half a file long
// is one nobody has ever seen, and the reader would be debugging it against
// a document that describes neither their file nor the register.
func TestLoad_RefusesTheWholeFileNotTheBadLine(t *testing.T) {
	restoreRegister(t)
	path := keymapFile(t, "[reading]\ncopy = \"x\"\n\n[agent]\nkill = \"j\"\n")
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
	present := keymapFile(t, "[reading]\ncopy = \"x\"\n")
	if err := Load(missing, present); err != nil {
		t.Fatalf("the second path should have been read: %v", err)
	}
	if !Is("x", Reading.Copy) {
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
		"PreviewKeys", "PasteKeys", "ScreenKeys", "OneShotKeys", "SetupKeys",
		"PlanKeys", "QueryKeys",
		"SourcesKeys", "NotesKeys", "BacklogKeys", "SprintKeys", "CommitKeys", "RewindKeys",
	} {
		if !named[want] {
			t.Errorf("%s is declared and no keymap file can reach it", want)
		}
	}
	for _, g := range fixed() {
		t.Errorf("%s is listed as fixed; a group is fixed only where a rule forbids moving it", g.name)
	}
}

// The six screens and cards a file could not reach before are moved like any
// other group: one key from each lands on the register, and the rules hold
// them the way they hold the rest — a pair stays a pair, and a key that
// deletes something stays off a movement key.
func TestLoad_TheScreenGroupsMove(t *testing.T) {
	restoreRegister(t)
	path := keymapFile(t, `
[sources]
list = "i"
[notes]
drop = "x"
[backlog]
new = "a"
[sprint]
goal = "G"
[commit]
edit = "m"
[rewind]
talk = "a"
`)
	if err := Load(path); err != nil {
		t.Fatalf("a file moving one key from each group was refused: %v", err)
	}
	for _, c := range []struct {
		name string
		b    Binding
		key  string
	}{
		{"sources.list", Sources.List, "i"},
		{"notes.drop", Notes.Drop, "x"},
		{"backlog.new", Backlog.New, "a"},
		{"sprint.goal", Sprint.Goal, "G"},
		{"commit.edit", Commit.Edit, "m"},
		{"rewind.talk", Rewind.Talk, "a"},
	} {
		if !slices.Equal(c.b.Keys(), []string{c.key}) {
			t.Errorf("%s answers %v, want [%s]", c.name, c.b.Keys(), c.key)
		}
	}

	for _, refused := range []struct{ body, says string }{
		{"[backlog]\nmove = \"up\"\n", "pairs"},
		{"[backlog]\ndrop = \"j\"\n", "moves the cursor"},
	} {
		if err := Load(keymapFile(t, refused.body)); err == nil || !strings.Contains(err.Error(), refused.says) {
			t.Errorf("%q: want a refusal saying %q, got %v", refused.body, refused.says, err)
		}
	}
}

// A pair with one half is a keyboard that moves one way and never the other,
// and nothing on the screen would say so: the hint would print the key the
// file asked for and the pointer would only ever go back. So the file is
// refused, whole, the way every other refusal here is.
func TestLoad_RefusesAHalfOfAPair(t *testing.T) {
	restoreRegister(t)
	err := Load(keymapFile(t, "[screen]\nmove = \"shift+up\"\n"))
	if err == nil {
		t.Fatal("a two-directional key with one keystroke should be refused")
	}
	if !strings.Contains(err.Error(), "pairs") {
		t.Errorf("the refusal does not say what shape it wanted: %v", err)
	}
	if !Is("j", Screen.Move) {
		t.Errorf("a refused file left the register at %v", Screen.Move.Keys())
	}
}

// And a whole pair lands, on both halves and in the order the handlers read.
// This is the move the keymap document offers as its example, checked here
// against the register and in internal/ui/components against the two screens
// that answer it.
func TestLoad_AMovedPairKeepsItsDirections(t *testing.T) {
	restoreRegister(t)
	path := keymapFile(t, "[screen]\nmove = [\"shift+up\", \"shift+down\"]\n")
	if err := Load(path); err != nil {
		t.Fatalf("a valid keymap was refused: %v", err)
	}
	if got := Step("shift+up", Screen.Move); got != -1 {
		t.Errorf("shift+up steps %d, want -1", got)
	}
	if got := Step("shift+down", Screen.Move); got != 1 {
		t.Errorf("shift+down steps %d, want 1", got)
	}
	if Is("j", Screen.Move) {
		t.Errorf("the old keystrokes are still answered: %v", Screen.Move.Keys())
	}
}

// The scaffold is the shipped keyboard written out, and as written it moves
// nothing: every line is commented. Uncommenting every one of them is the
// shipped keyboard stated by hand, which the file's own rules must accept —
// a scaffold that was refused the moment somebody uncommented it whole would
// be a starting point nobody could start from.
func TestScaffold_IsANoOpUntilALineIsUncommented(t *testing.T) {
	restoreRegister(t)
	before := Keyboard()
	text := Scaffold()
	if err := Load(keymapFile(t, text)); err != nil {
		t.Fatalf("the scaffold as written was refused: %v", err)
	}
	if !reflect.DeepEqual(Keyboard(), before) {
		t.Fatal("the scaffold as written moved a key")
	}
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(line, "# "); ok && strings.Contains(rest, " = ") {
			line = rest
		}
		lines = append(lines, line)
	}
	if err := Load(keymapFile(t, strings.Join(lines, "\n"))); err != nil {
		t.Fatalf("the scaffold with every line uncommented was refused: %v", err)
	}
	for _, g := range Keyboard() {
		for _, a := range g.Acts {
			if a.Moved() {
				t.Errorf("%s answers %v after the shipped keys were stated, want %v", a.Name, a.Keys, a.Shipped)
			}
		}
	}
}

// Every line the scaffold writes names a key a file can reach, and every key
// it lists as fixed is one a file cannot: the scaffold and binding() read
// the register through two walks, and they have to agree on every name.
func TestScaffold_NamesWhatAFileCanReach(t *testing.T) {
	text := Scaffold()
	for _, g := range Keyboard() {
		for _, a := range g.Acts {
			if got := binding(a.Name) != nil; got != a.Movable {
				t.Errorf("%s: a file reaches it %v, the listing says %v", a.Name, got, a.Movable)
			}
			if !strings.Contains(text, a.Name[strings.LastIndex(a.Name, ".")+1:]) {
				t.Errorf("the scaffold does not name %s", a.Name)
			}
		}
	}
	for _, rule := range []string{"two acts", "destructive", "bare key", "pair", "desktop or the terminal", "configuration.md#the-keymap-file"} {
		if !strings.Contains(text, rule) {
			t.Errorf("the scaffold's opening does not state %q", rule)
		}
	}
}

// Every group the package declares is either one a file can write or one the
// listing names as fixed, so a group added later reaches the scaffold and the
// reference one way or the other rather than being left out of both.
func TestEveryDeclaredGroupIsListed(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "keys.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, g := range append(movable(), fixed()...) {
		listed[g.value.Type().Name()] = true
	}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			for _, v := range spec.(*ast.ValueSpec).Values {
				lit, ok := v.(*ast.CompositeLit)
				if !ok {
					continue
				}
				if id, ok := lit.Type.(*ast.Ident); ok && strings.HasSuffix(id.Name, "Keys") && !listed[id.Name] {
					t.Errorf("%s is declared and neither a file's group nor listed as fixed", id.Name)
				}
			}
		}
	}
}

// Check judges a file against the shipped keyboard and leaves the register
// as the process held it, whatever the file said.
func TestCheck_LeavesTheRegisterAsItWas(t *testing.T) {
	restoreRegister(t)
	if err := Load(keymapFile(t, "[reading]\ncopy = \"x\"\n")); err != nil {
		t.Fatal(err)
	}
	held := Keyboard()

	good := keymapFile(t, "[reading]\ncopy = \"alt+pgup\"\n")
	if path, err := Check(good); err != nil || path != good {
		t.Fatalf("a valid file: %q, %v", path, err)
	}
	if path, err := Check(keymapFile(t, "[draft]\npalette = \"p\"\n")); err == nil || path == "" {
		t.Fatalf("a refused file came back %q, %v", path, err)
	}
	if path, err := Check(filepath.Join(t.TempDir(), "none.toml")); err != nil || path != "" {
		t.Fatalf("no file is not a refusal: %q, %v", path, err)
	}
	if !reflect.DeepEqual(Keyboard(), held) {
		t.Fatal("Check left the register moved")
	}
}

// The listing marks what a file moved, beside what it shipped as, and Load
// keeps what it read and what it said for the surfaces that ask later.
func TestKeyboard_MarksAMovedKey(t *testing.T) {
	restoreRegister(t)
	path := keymapFile(t, "[reading]\ncopy = \"x\"\n")
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if got, err := Applied(); got != path || err != nil {
		t.Fatalf("Applied is %q, %v", got, err)
	}
	moved := 0
	for _, g := range Keyboard() {
		for _, a := range g.Acts {
			if !a.Moved() {
				continue
			}
			moved++
			if a.Name != "reading.copy" || !slices.Equal(a.Keys, []string{"x"}) || !slices.Equal(a.Shipped, []string{"y"}) {
				t.Errorf("the mark is on %s: %v shipped as %v", a.Name, a.Keys, a.Shipped)
			}
		}
	}
	if moved != 1 {
		t.Errorf("%d keys are marked moved, want 1", moved)
	}
}

// A line naming a key the register has since given up is read and does
// nothing, and is named, rather than costing the rest of the file: the file
// was right when it was written. A name nobody ever declared is still a
// refusal, which is what keeps a typo from being quietly ignored.
func TestLoad_ReadsAFileNamingARetiredKey(t *testing.T) {
	restoreRegister(t)
	path := keymapFile(t, "[row]\nUndo = \"u\"\n\n[rowchord]\ncommit = \"alt+g\"\nretry = \"alt+r\"\n\n[draft]\nopen_paste = \"alt+v\"\n\n[reading]\ncopy = \"x\"\n")
	if err := Load(path); err != nil {
		t.Fatalf("a file naming a retired key was refused: %v", err)
	}
	if !Is("x", Reading.Copy) {
		t.Errorf("the file's live line did not apply: copy answers %v", Reading.Copy.Keys())
	}
	if got, want := Dead(), []string{"draft.open_paste", "row.Undo", "rowchord.commit", "rowchord.retry"}; !slices.Equal(got, want) {
		t.Errorf("Dead() = %v, want %v", got, want)
	}
	if err := Load(keymapFile(t, "[row]\nundoo = \"u\"\n")); err == nil {
		t.Error("a name the register never declared should still be refused")
	}
}
