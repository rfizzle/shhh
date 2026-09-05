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
// Three things a file may not do, and each is a refusal of the whole file
// rather than of a line. It may not move a key onto a chord the desktop, the
// terminal or a multiplexer takes before shhh sees it (reserved.go): a hint
// offering such a chord is a false offer on the machine the reader is
// holding. It may not leave a surface answering one keystroke
// with two acts — that is the register's own rule, the one the list exists
// to make checkable
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard),
// and a file that broke it would put the first case of a switch silently in
// front of the second. And it may not move a destructive act onto a movement
// key: the reason the agent manager kills on a capital in the first place
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
	for _, path := range paths {
		moves, err := readKeymap(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err == nil {
			err = apply(moves)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		return nil
	}
	return nil
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
	if err := checkReserved(); err != nil {
		return err
	}
	return checkOneKeystrokeOnce()
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
		Agent.Cancel, Agent.Kill,
		Select.Delete, Browse.Delete, Screen.Delete,
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

// groups is the register's declarations, by the name a keymap file calls
// them. Reflection over the structs rather than a table of every field: a
// table would be a third place each key is written down, and the failure it
// invites is a key that can be moved on one surface and not on another
// because somebody adding a binding did not know there was a list to add it
// to.
func groups() map[string]reflect.Value {
	return map[string]reflect.Value{
		"draft":    reflect.ValueOf(&Draft).Elem(),
		"search":   reflect.ValueOf(&Search).Elem(),
		"reading":  reflect.ValueOf(&Reading).Elem(),
		"find":     reflect.ValueOf(&Find).Elem(),
		"context":  reflect.ValueOf(&Context).Elem(),
		"row":      reflect.ValueOf(&Row).Elem(),
		"decision": reflect.ValueOf(&Decision).Elem(),
		"confirm":  reflect.ValueOf(&Confirm).Elem(),
		"select":   reflect.ValueOf(&Select).Elem(),
		"review":   reflect.ValueOf(&Review).Elem(),
		"agent":    reflect.ValueOf(&Agent).Elem(),
		"profile":  reflect.ValueOf(&Profile).Elem(),
		"wait":     reflect.ValueOf(&Wait).Elem(),
		"diff":     reflect.ValueOf(&Diff).Elem(),
		"output":   reflect.ValueOf(&Output).Elem(),
		"preview":  reflect.ValueOf(&Preview).Elem(),
		"screen":   reflect.ValueOf(&Screen).Elem(),
		"oneshot":  reflect.ValueOf(&OneShot).Elem(),
		"setup":    reflect.ValueOf(&Setup).Elem(),
		"browse":   reflect.ValueOf(&Browse).Elem(),
	}
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
