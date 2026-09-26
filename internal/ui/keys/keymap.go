package keys

// The rebinding layer: a file that moves a key inside the register
// (docs/capabilities/configuration.md#the-keymap-file).
//
// The register declares each key once and every hint and every handler reads
// that one declaration, which is what makes a move possible at all: a file
// changes the declaration, and the surface that answers the key and the row
// that offers it change together because they were never two facts.
//
// It is applied before anything draws. A keymap read halfway through a
// session would be a screen offering keys it no longer answers, so the whole
// of it happens once, at the top of the process, and the register is
// ordinary package data from then on.
//
// Five things a file may not do, and each is a refusal of the whole file
// rather than of a line. It may not move a key onto a chord the desktop, the
// terminal or a multiplexer takes before shhh sees it (reserved.go): a hint
// offering such a chord is a false offer on the machine the reader is
// holding. It may not put a bare key on the input, where the draft can take
// text and a bare key is a letter of the sentence
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
// It may not leave a surface answering one keystroke
// with two acts — that is the register's own rule, the one the list exists
// to make checkable
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard),
// and a file that broke it would put the first case of a switch silently in
// front of the second. It may not give a key that moves both ways one half
// of its pair: the keystrokes are read back first, then on, and a single one
// is a pointer that walks up and never down. And it may not move a
// destructive act onto a movement key: the reason the agent manager kills on a capital in the first place
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard),
// which a keymap would otherwise be a way around.
//
// Refusing the whole file is the point of refusing at all. A file half
// applied is a keyboard nobody has ever seen, and the reader would be
// debugging it against a document describing neither their file nor the
// register.

import (
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"slices"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/key"
	"github.com/BurntSushi/toml"
)

// Load applies the first of paths that exists, and returns the reason it
// refused one. A refused file leaves the register exactly as it was
// declared, so a session always runs a keyboard that is either the user's or
// shhh's and never half of each.
//
// No file is not an error: most people never write one, and the register is
// the answer for all of them.
func Load(paths ...string) error {
	appliedPath, appliedErr = load(paths...)
	return appliedErr
}

// load is Load without the record: the path it read, "" where none of them
// exists, and the refusal.
func load(paths ...string) (string, error) {
	for _, path := range paths {
		moves, err := readKeymap(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err == nil {
			err = apply(moves)
		}
		if err != nil {
			return path, fmt.Errorf("%s: %w", path, err)
		}
		return path, nil
	}
	return "", nil
}

// appliedPath and appliedErr are what the last Load read and what it said,
// which in the binary is the one call at the top of the process. They are
// kept because the refusal is said once, on stderr, before anything draws —
// and a person whose keyboard reverted to the shipped one finds out why
// later, from the doctor and from `shhh keys`, which read this rather than
// applying the file again under a screen that is drawing from the register.
var (
	appliedPath string
	appliedErr  error
)

// Applied is the file this process's keyboard was read from and the reason
// it was refused, if it was. An empty path is a machine with no file.
func Applied() (path string, err error) { return appliedPath, appliedErr }

// Check is whether a session started now would run the file at path, or the
// first of paths that exists: the path it read ("" where none exists) and
// the refusal a session would print. It is asked of the keyboard shhh ships,
// not of the one this process is already running, so a file is judged the way
// a fresh start judges it.
//
// The register is package data, so the file is applied to it and the
// register this process held is put back before Check returns, whatever the
// file said. It is for a process that draws nothing — `shhh keys check` —
// and never for one with a screen up: a surface drawing while Check ran would
// read the file's keys for as long as it took.
func Check(paths ...string) (string, error) {
	held := snapshot()
	defer settle(held)
	settle(declared)
	return load(paths...)
}

// readKeymap reads one file into the moves it asks for: the dotted name of a
// binding in the register against the keystrokes it should answer to.
//
// A value is one keystroke as a string, or several as an array, which is the
// shape TOML already has for "one or many" and so needs nothing explained.
func readKeymap(path string) (map[string][]string, error) {
	var raw map[string]any
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		return nil, err
	}
	moves := map[string][]string{}
	if err := flatten("", raw, moves); err != nil {
		return nil, err
	}
	return moves, nil
}

// flatten walks the file's tables into dotted names. The nesting is the
// register's own — a group of keys is a table, and the palette's four keys
// are a table inside the selector family's — so a file reads the way the
// register is written rather than as one long list of dotted strings.
func flatten(prefix string, table map[string]any, into map[string][]string) error {
	for _, name := range sorted(table) {
		value := table[name]
		full := name
		if prefix != "" {
			full = prefix + "." + name
		}
		switch v := value.(type) {
		case map[string]any:
			if err := flatten(full, v, into); err != nil {
				return err
			}
		case string:
			into[full] = []string{v}
		case []any:
			presses := make([]string, 0, len(v))
			for _, press := range v {
				s, ok := press.(string)
				if !ok {
					return fmt.Errorf("%s: a keystroke is text, got %T", full, press)
				}
				presses = append(presses, s)
			}
			into[full] = presses
		default:
			return fmt.Errorf("%s: a binding is one keystroke or a list of them, got %T", full, value)
		}
	}
	return nil
}

// apply moves every binding the file names and then asks the register
// whether what came out is still a register. Nothing is left behind on a
// refusal: the declarations are put back before the error is returned, so a
// caller that carries on runs the keyboard shhh declared.
func apply(moves map[string][]string) error {
	if len(moves) == 0 {
		return nil
	}
	restore := map[*Binding]Binding{}
	for _, name := range sorted(moves) {
		b := binding(name)
		if b == nil {
			undo(restore)
			return fmt.Errorf("%s names no key in the register", name)
		}
		presses := moves[name]
		if len(presses) == 0 {
			undo(restore)
			return fmt.Errorf("%s: a key with no keystrokes answers to nothing", name)
		}
		if _, seen := restore[b]; !seen {
			restore[b] = *b
		}
		*b = key.NewBinding(key.WithKeys(presses...), key.WithHelp(spelling(presses), Words(*b)))
	}
	if err := check(); err != nil {
		undo(restore)
		return err
	}
	return nil
}

func undo(restore map[*Binding]Binding) {
	for b, was := range restore {
		*b = was
	}
}

// sorted is a table's names in a fixed order, so that a file with two
// mistakes in it names the same one every time it is read. A map's order is
// not an order, and an error message that moves is one nobody can act on.
func sorted[V any](table map[string]V) []string {
	names := make([]string, 0, len(table))
	for name := range table {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// spelling is what a hint prints for a moved key: the keystrokes it now
// answers, joined the way the register's own pairs are. The words stay the
// binding's — a key that moved still does the same thing, and it is the act
// the words are about.
func spelling(presses []string) string { return strings.Join(presses, "/") }

// check is the register's own rules asked of the register as it now stands.
func check() error {
	if err := checkDestructive(); err != nil {
		return err
	}
	if err := checkBareAtTheDraft(); err != nil {
		return err
	}
	if err := checkPairs(); err != nil {
		return err
	}
	if err := checkReserved(); err != nil {
		return err
	}
	return checkOneKeystrokeOnce()
}

// pairs is the bindings that are one offer in two directions: `j/k`, `n/p`,
// `↑↓`. Their keystrokes are declared in pairs, the half that goes back
// first, and Step reads the direction from that order rather than from the
// spelling — which is what lets a handler answer `shift+↑` the way it
// answered `up` without knowing either.
//
// Listed rather than derived, for the reason destructive is: nothing in a
// binding says which of its keys go back, and a spelling is not a shape a
// file has to keep — `screen.move = ["a", "b"]` is a legitimate move and
// says nothing at all.
func pairs() []Binding {
	return []Binding{
		Reading.Move, Reading.Match, Reading.Half,
		Context.Move, Backlog.Move, Backlog.Page, Sprint.Move,
		Select.Move, Select.MoveJK, Select.Tab,
		Review.MoveFile, Review.MoveHunk,
		Agent.Move, Profile.Move,
		Diff.Scroll, Diff.Hunk, Output.Scroll,
		Screen.Move,
	}
}

// checkPairs refuses a file that leaves a two-directional key with an odd
// number of keystrokes. A pair with one half is not a narrower keyboard, it
// is a keyboard where the surface moves one way and never the other, and
// nothing on the screen would say so: the hint would print the key the file
// asked for and the pointer would only ever go back.
func checkPairs() error {
	for _, b := range pairs() {
		if n := len(b.Keys()); n == 0 || n%2 != 0 {
			return fmt.Errorf("%q moves both ways, so it needs its keystrokes in pairs — back first; %v is %d of them",
				Words(b), b.Keys(), n)
		}
	}
	return nil
}

// checkOneKeystrokeOnce refuses a surface answering one keystroke with two
// acts, naming the surface, the keystroke and both acts. It is the check the
// register's list exists to make possible, asked here of a file rather than
// of a commit.
func checkOneKeystrokeOnce() error {
	for _, s := range append(Surfaces(), Programs()...) {
		seen := map[string]string{}
		for _, b := range s.Bindings {
			for _, k := range b.Keys() {
				if prev, ok := seen[k]; ok {
					return fmt.Errorf("on %s, %q would be both %q and %q", s.Name, k, prev, Words(b))
				}
				seen[k] = Words(b)
			}
		}
	}
	return nil
}

// movement is the keystrokes that move a cursor somewhere in this product.
// They are the ones a reader presses without reading the row first, which is
// what makes an act underneath one a false offer rather than a mistake.
var movement = []string{
	"j", "k", "h", "l",
	"up", "down", "left", "right",
	"pgup", "pgdown", "home", "end",
}

// destructive is the acts that end something a person cannot get back: a
// running process, a saved chat, a stored command, a turn's edits. They are
// listed rather than derived because nothing in a binding says what it does
// — the register fixes a key and its words, and which of those words mean
// "gone" is a judgement this file makes once.
func destructive() []Binding {
	return []Binding{
		Agent.Cancel, Agent.Kill, Agent.KillAll,
		Select.Delete, Screen.Delete,
		Confirm.Force,
	}
}

// checkDestructive holds the rule the agent manager's capital already
// records: a movement key may not also end something. The manager gave up
// the lower-case letter for it, and a file that took it back would undo the
// decision from outside the program
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func checkDestructive() error {
	for _, b := range destructive() {
		for _, k := range b.Keys() {
			if slices.Contains(movement, k) {
				return fmt.Errorf("%q moves the cursor, so it cannot also be %q", k, Words(b))
			}
		}
	}
	return nil
}

// checkBareAtTheDraft holds a file to the rule the input's own keyboard is
// built on: the draft can take text nearly all the time, so a key that is
// live there is a chord and never a letter of the sentence being typed
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
// A file that moved the palette onto `p` would take that letter out of every
// prompt the reader ever writes, and nothing on the screen would say why.
//
// Enter and esc are the rule's own two exceptions and neither is a keystroke
// a sentence produces, so Typed is the whole of the test — the same question
// a surface being typed into asks of its own movement keys.
//
// It is the input's rule and not every surface's. A takeover holds the
// keyboard exclusively, so its letters are live because nothing else is
// listening, and a surface beside the draft answers nothing at all until the
// handover: both are free to spend letters, and the register's own test is
// what holds them to that.
func checkBareAtTheDraft() error {
	for _, s := range append(Surfaces(), Programs()...) {
		if s.Position != Home {
			continue
		}
		for _, b := range s.Bindings {
			for _, k := range b.Keys() {
				if Typed(k) {
					return fmt.Errorf("%q is a letter while the draft can take text, so it cannot also be %q on %s; a key live at the input is a chord",
						k, Words(b), s.Name)
				}
			}
		}
	}
	return nil
}

// groups is the register's declarations, by the name a keymap file calls
// them. Reflection over the structs rather than a table of every field: a
// table would be a third place each key is written down, and the failure it
// invites is a key that can be moved on one surface and not on another
// because somebody adding a binding did not know there was a list to add it
// to.
func groups() map[string]reflect.Value {
	out := map[string]reflect.Value{}
	for _, g := range movable() {
		out[g.name] = g.value
	}
	return out
}

// namedGroup is one group of the register under the name a file writes it
// by.
type namedGroup struct {
	name  string
	value reflect.Value
}

// movable is groups in the order a reader meets them, which is the order the
// scaffold and the reference are written in: the input, then what takes the
// keyboard from it, then the rows and the cards, then the screens and the
// programs of their own.
func movable() []namedGroup {
	return []namedGroup{
		{"draft", reflect.ValueOf(&Draft).Elem()},
		{"search", reflect.ValueOf(&Search).Elem()},
		{"reading", reflect.ValueOf(&Reading).Elem()},
		{"find", reflect.ValueOf(&Find).Elem()},
		{"paste", reflect.ValueOf(&Paste).Elem()},
		{"context", reflect.ValueOf(&Context).Elem()},
		{"row", reflect.ValueOf(&Row).Elem()},
		{"rowchord", reflect.ValueOf(&RowChord).Elem()},
		{"decision", reflect.ValueOf(&Decision).Elem()},
		{"confirm", reflect.ValueOf(&Confirm).Elem()},
		{"select", reflect.ValueOf(&Select).Elem()},
		{"review", reflect.ValueOf(&Review).Elem()},
		{"agent", reflect.ValueOf(&Agent).Elem()},
		{"profile", reflect.ValueOf(&Profile).Elem()},
		{"wait", reflect.ValueOf(&Wait).Elem()},
		{"diff", reflect.ValueOf(&Diff).Elem()},
		{"output", reflect.ValueOf(&Output).Elem()},
		{"preview", reflect.ValueOf(&Preview).Elem()},
		{"screen", reflect.ValueOf(&Screen).Elem()},
		{"plan", reflect.ValueOf(&Plan).Elem()},
		{"query", reflect.ValueOf(&Query).Elem()},
		{"oneshot", reflect.ValueOf(&OneShot).Elem()},
		{"setup", reflect.ValueOf(&Setup).Elem()},
	}
}

// fixed is the groups the register declares and no file can name: binding()
// does not resolve them, so a line for one of their keys is refused as naming
// no key. They are listed so the scaffold and the reference can say so
// rather than leave a reader to find it out from a refusal.
func fixed() []namedGroup {
	return []namedGroup{
		{"sources", reflect.ValueOf(&Sources).Elem()},
		{"notes", reflect.ValueOf(&Notes).Elem()},
		{"backlog", reflect.ValueOf(&Backlog).Elem()},
		{"sprint", reflect.ValueOf(&Sprint).Elem()},
		{"commit", reflect.ValueOf(&Commit).Elem()},
		{"rewind", reflect.ValueOf(&Rewind).Elem()},
	}
}

// snapshot copies every group a file can move, so the copy can be put back
// with settle. A move replaces a whole Binding and never edits the one it
// replaced, so a copy of the structs is a copy of the keyboard.
func snapshot() []reflect.Value {
	var out []reflect.Value
	for _, g := range movable() {
		was := reflect.New(g.value.Type()).Elem()
		was.Set(g.value)
		out = append(out, was)
	}
	return out
}

// settle puts a snapshot back over the register.
func settle(saved []reflect.Value) {
	for i, g := range movable() {
		g.value.Set(saved[i])
	}
}

// declared is the register as shhh ships it, taken before any file is read,
// which is what a listing measures a moved key against and what Check
// applies a file to.
var declared = snapshot()

// Act is one key as a listing names it.
type Act struct {
	// Name is the dotted name a file writes it by: `reading.copy`.
	Name string
	// Words are what the key does, the words beside it in every hint.
	Words string
	// Keys are the keystrokes it answers in this process, and Shipped the
	// ones it was declared with.
	Keys    []string
	Shipped []string
	// Movable says a file may name it at all.
	Movable bool
}

// Moved says a file changed what the key answers.
func (a Act) Moved() bool { return !slices.Equal(a.Keys, a.Shipped) }

// Group is one group of the register's keys, in the order it declares them.
type Group struct {
	Name    string
	Movable bool
	Acts    []Act
}

// Keyboard is the register as this process holds it, one group per table a
// file can write and then the groups it cannot, each key beside the
// keystrokes it shipped with.
func Keyboard() []Group { return keyboard(false) }

// keyboard reads the register as this process holds it, or as it was
// declared: the reference and the scaffold are the shipped keyboard whatever
// file the process that writes them was started under, and reading the
// declared copy means neither has to move the register to get it.
func keyboard(asShipped bool) []Group {
	shipped := map[string][]string{}
	for i, g := range movable() {
		walk(g.name, declared[i], func(name string, b Binding) { shipped[name] = b.Keys() })
	}
	var out []Group
	for i, g := range movable() {
		v := g.value
		if asShipped {
			v = declared[i]
		}
		group := Group{Name: g.name, Movable: true}
		walk(g.name, v, func(name string, b Binding) {
			group.Acts = append(group.Acts, Act{
				Name: name, Words: Words(b), Keys: b.Keys(), Shipped: shipped[name], Movable: true,
			})
		})
		out = append(out, group)
	}
	for _, g := range fixed() {
		group := Group{Name: g.name}
		walk(g.name, g.value, func(name string, b Binding) {
			group.Acts = append(group.Acts, Act{Name: name, Words: Words(b), Keys: b.Keys(), Shipped: b.Keys()})
		})
		out = append(out, group)
	}
	return out
}

// walk visits every binding under a group, by the dotted name a file writes
// it as: the group's own keys in the order the struct declares them, then
// each nested group as a table of its own — which is the order a TOML file
// has to put them in, since a key after a sub-table belongs to the sub-table.
func walk(prefix string, v reflect.Value, visit func(name string, b Binding)) {
	t := v.Type()
	var nested []int
	for i := range t.NumField() {
		if !t.Field(i).IsExported() {
			continue
		}
		f := v.Field(i)
		if f.Type() == reflect.TypeOf(Binding{}) {
			visit(prefix+"."+snake(t.Field(i).Name), f.Interface().(Binding))
			continue
		}
		if f.Kind() == reflect.Struct {
			nested = append(nested, i)
		}
	}
	for _, i := range nested {
		walk(prefix+"."+snake(t.Field(i).Name), v.Field(i), visit)
	}
}

// snake is a Go field name as a file would write it — HistoryPrev as
// history_prev, MoveJK as move_jk — which fieldNamed reads back.
func snake(name string) string {
	r := []rune(name)
	var b strings.Builder
	for i, c := range r {
		if unicode.IsUpper(c) && i > 0 {
			afterLower := unicode.IsLower(r[i-1])
			endsAcronym := unicode.IsUpper(r[i-1]) && i+1 < len(r) && unicode.IsLower(r[i+1])
			if afterLower || endsAcronym {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToLower(c))
	}
	return b.String()
}

// binding resolves a dotted name from a keymap file to the declaration it
// names, and nil where the register has no such key. The pointer is into the
// package's own var, which is what makes a move one edit rather than a copy
// the surfaces do not read.
func binding(name string) *Binding {
	parts := strings.Split(name, ".")
	v, ok := groups()[parts[0]]
	if !ok {
		return nil
	}
	for _, part := range parts[1:] {
		if v.Kind() != reflect.Struct {
			return nil
		}
		field := fieldNamed(v, part)
		if !field.IsValid() {
			return nil
		}
		v = field
	}
	// CanInterface as well as CanAddr: a binding reached through an
	// unexported field is one reflect will hand back and then panic on, and
	// a file naming one should get the same "no such key" as a typo.
	if v.Type() != reflect.TypeOf(Binding{}) || !v.CanAddr() || !v.CanInterface() {
		return nil
	}
	return v.Addr().Interface().(*Binding)
}

// fieldNamed finds a struct field by the name a file would write it as. The
// Go names are compounds — HistoryPrev, ClearQ, MoveJK — and a file may say
// `history_prev` or `historyprev` or `HistoryPrev` for any of them, because
// which of those a person guesses is not a thing worth being right about.
func fieldNamed(v reflect.Value, name string) reflect.Value {
	want := strings.ReplaceAll(name, "_", "")
	t := v.Type()
	for i := range t.NumField() {
		if strings.EqualFold(t.Field(i).Name, want) {
			return v.Field(i)
		}
	}
	return reflect.Value{}
}
