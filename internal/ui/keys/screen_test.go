package keys

import (
	"slices"
	"strings"
	"testing"
	"unicode"
)

// ownLetters are the letters each program adds to the shared Screen block
// (docs/interface/surfaces.md#the-supporting-screens). A program may add three
// and no more, and the ones it does add are the register's own spellings of
// the act: c copies, d deletes, e renames, r runs again, a applies.
var ownLetters = map[string]string{
	"shhh config":                 "",
	"a setting's picker or field": "",
	"shhh history":                "c d",
	"shhh doctor":                 "a c r",
	"shhh metrics":                "",
	"shhh rate":                   "n s y",
	"shhh snippets":               "c d e",
	"the saved-chat browser":      "d e",
	"a rename row":                "",
}

// One block, read by every program: the shared acts have one binding apiece,
// the programs add the letters above and nothing else, and none of them
// spells the ways out that the host answers (q, x, ctrl+c).
func TestScreen_EveryProgramSharesTheRegister(t *testing.T) {
	// One binding per act: no two bindings of the block answer one keystroke.
	block := append(Screen.Shared(), Screen.Copy, Screen.Delete, Screen.Rename,
		Screen.Again, Screen.Apply, Screen.Worked, Screen.Failed, Screen.Skip)
	seen := map[string]string{}
	for _, b := range block {
		for _, k := range b.Keys() {
			if was, dup := seen[k]; dup {
				t.Errorf("%q is bound twice in the Screen block: %q and %q", k, was, Words(b))
			}
			seen[k] = Words(b)
		}
	}

	shared := Screen.Shared()
	seenProgram := map[string]bool{}
	for _, s := range Programs() {
		want, listed := ownLetters[s.Name]
		if !listed {
			continue
		}
		seenProgram[s.Name] = true
		var own []string
		for _, b := range s.Bindings {
			for _, k := range b.Keys() {
				switch k {
				case "q", "x", "ctrl+c":
					t.Errorf("%s answers %q; esc is the way out and the host answers ctrl+c", s.Name, k)
				}
			}
			if slices.ContainsFunc(shared, func(o Binding) bool { return Shown(o) == Shown(b) && Words(o) == Words(b) }) ||
				Shown(b) == Shown(Query.Rub) || Shown(b) == Shown(Select.Move) || Shown(b) == Shown(Select.Alt) {
				continue
			}
			for _, k := range b.Keys() {
				if r := []rune(k); len(r) == 1 && unicode.IsLetter(r[0]) && !slices.Contains(own, k) {
					own = append(own, k)
				}
			}
		}
		slices.Sort(own)
		if got := strings.Join(own, " "); got != want {
			t.Errorf("%s adds the letters %q to the Screen block, want %q", s.Name, got, want)
		}
		if len(own) > 3 {
			t.Errorf("%s adds %d letters of its own; a program may add three", s.Name, len(own))
		}
	}
	for name := range ownLetters {
		if !seenProgram[name] {
			t.Errorf("%s is listed here and is not a program in the register", name)
		}
	}
}
