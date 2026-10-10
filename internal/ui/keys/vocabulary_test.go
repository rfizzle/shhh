package keys

// One register vocabulary (docs/interface/principles.md#esc-is-always-the-safe-answer).
//
// A key means the same act on every surface that answers it, and the act has
// one spelling and one word, so a reader learns the keyboard once. The table
// below is the vocabulary and the acts that spell it; a binding that adds a
// second spelling of an act, or spends a shared key on something else, fails
// here — which is the point: the next surface to do it has to change this
// file, beside the reason.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode"
)

// actSpelling is one act of the shared vocabulary: what a hint prints, the
// keystrokes it answers, and its word. A binding's words are the act's word,
// or the word and what it acts on ("write and leave").
type actSpelling struct {
	shown   string
	presses []string
	word    string
}

var vocabulary = map[string]actSpelling{
	"move":   {MoveShown, []string{"up", "down", "k", "j"}, "move"},
	"move↑↓": {MoveTypedShown, []string{"up", "down"}, "move"},
	"open":   {"enter", []string{"enter"}, "open"},
	"back":   {"esc", []string{"esc"}, "back"},
	"filter": {"/", []string{"/"}, "filter"},
	"write":  {SaveChord, []string{SaveChord}, "write"},
	"reset":  {"ctrl+r", []string{"ctrl+r"}, "reset"},
	"delete": {"d", []string{"d"}, "delete"},
	"copy":   {"c", []string{"c"}, "copy"},
	"edit":   {"e", []string{"e"}, "edit"},
	"rename": {"e", []string{"e"}, "rename"},
	"retry":  {"r", []string{"r"}, "retry"},
	"run":    {"r", []string{"r"}, "run"},
	"new":    {"n", []string{"n"}, "new"},
	"match":  {"n/N", []string{"N", "n"}, ""},
	"toggle": {"space", []string{" ", "space"}, "toggle"},
	"keys":   {"?", []string{"?"}, "keys"},
}

// acts is which act each binding spells, by the name a keymap file writes
// it. Anything not named here is a surface's own, which is held to the
// letter budget below.
var acts = map[string]string{
	"reading.move": "move", "reading.copy": "copy", "reading.match": "match",
	"reading.list": "keys", "reading.back": "back",
	"staged.drop": "delete", "staged.back": "back", "staged.open": "open",
	"queue.move": "move", "queue.cancel": "delete", "queue.back": "back",
	"keylist.move": "move↑↓", "keylist.close": "back",
	"find.clear": "back", "search.cancel": "back",
	"paste.scroll": "move", "paste.remove": "delete", "paste.back": "back",
	"context.move": "move", "context.list": "keys", "context.back": "back",
	"sources.move": "move", "sources.open": "open", "sources.list": "keys", "sources.back": "back",
	"notes.move": "move", "notes.read": "open", "notes.drop": "delete",
	"notes.list": "keys", "notes.back": "back",
	"backlog.move": "move", "backlog.read": "open", "backlog.filter": "filter",
	"backlog.edit": "edit", "backlog.new": "new", "backlog.drop": "delete",
	"backlog.sprint": "toggle", "backlog.run": "run", "backlog.list": "keys", "backlog.back": "back",
	"sprint.move": "move", "sprint.toggle": "toggle", "sprint.take": "write", "sprint.cancel": "back",
	"commit.edit": "edit", "commit.cancel": "back",
	"select.move": "move↑↓", "select.move_jk": "move", "select.filter": "filter",
	"select.toggle": "toggle", "select.delete": "delete", "select.rename": "rename",
	"select.cancel":    "back",
	"review.move_file": "move", "review.move_hunk": "match", "review.back": "back",
	"agent.move": "move", "agent.edit": "edit", "agent.retry": "retry",
	"agent.back": "back", "agent.detach": "back",
	"profile.move": "move↑↓", "profile.edit": "edit", "profile.clear": "delete",
	"profile.back": "back", "profile.save": "write",
	"editor.save": "write", "editor.save_leave": "write", "editor.back": "back", "editor.keep": "back",
	"wait.stop": "back", "wait.keep_going": "back", "wait.keep_key": "back",
	"diff.scroll": "move", "diff.hunk": "match", "diff.back": "back",
	"output.scroll": "move", "output.back": "back",
	"preview.remove": "delete", "preview.back": "back",
	"screen.move": "move", "screen.filter": "filter", "screen.list": "keys", "screen.quit": "back",
	"screen.reset": "reset", "screen.write": "write", "screen.keep": "back",
	"screen.copy": "copy", "screen.snippet": "write", "screen.delete": "delete",
	"screen.rename": "rename", "screen.again": "retry",
	"oneshot.edit": "edit", "oneshot.copy": "copy", "oneshot.save": "write", "oneshot.quit": "back",
	"plan.save":     "write",
	"rewind.cancel": "back",
}

// apart are the blocks the vocabulary does not speak for. The draft spends
// chords only, by its own rule, and its history search is a mode of it; a
// transcript row's offers are answered through the handover and keep their
// letters.
var apart = []string{"draft.", "search.older", "search.keep", "row."}

// answers are a card's own y, n and e — the answer to the question the card
// asks, not a key of the screen — and the inline confirm's.
var answers = []string{
	"decision.allow", "decision.deny", "decision.allow_noted", "decision.deny_noted",
	"decision.accept", "decision.refuse", "decision.amend", "decision.revise",
	"confirm.yes", "confirm.no", "confirm.force",
	"proposal.write", "proposal.later", "proposal.never",
	"screen.worked", "screen.failed", "screen.skip", "editor.discard", "oneshot.confirm",
}

// shared is which act owns a keystroke outright: a binding that answers one
// of these is that act, or it is a second meaning of a key a reader already
// knows.
var shared = map[string]string{
	"esc": "back", "/": "filter", "ctrl+r": "reset", "ctrl+s": "write",
	"d": "delete", "c": "copy", "e": "edit", "r": "retry",
	"n": "match", "N": "match", " ": "toggle", "space": "toggle", "?": "keys",
	"k": "move", "j": "move",
}

// owed is every own key that still breaks the vocabulary, by binding: a
// shared letter spent on another act, or the x that delete has replaced
// everywhere else. Each is a screen whose own set of verbs is to be folded;
// the list only ever shrinks, and the test fails if an entry is fixed
// without being taken off it.
var owed = map[string]string{
	"agent.cancel":     "d",
	"oneshot.revise":   "r",
	"oneshot.explain":  "x",
	"decision.explain": "x",
	"rewind.code":      "c",
	"wait.new_session": "n",
	"reading.search":   "/",
}

// over is every surface carrying more than three letters of its own, with
// those letters. Like owed it only shrinks.
var over = map[string]string{
	"the agent manager":                      "K X a d m p s",
	"the approval card and the /run confirm": "A V a g t v x",
	"the one-shot's action bar":              "a p r t u x",
	"the profile draft":                      "R m",
}

func isApart(name string, list []string) bool {
	for _, p := range list {
		if name == p || (strings.HasSuffix(p, ".") && strings.HasPrefix(name, p)) {
			return true
		}
	}
	return false
}

func TestRegister_OneSpellingPerAct(t *testing.T) {
	names := map[string]Act{}
	for _, g := range Keyboard() {
		for _, a := range g.Acts {
			names[a.Name] = a
		}
	}
	for name, act := range acts {
		a, ok := names[name]
		if !ok {
			t.Errorf("%s spells %s, but the register has no such key", name, act)
			continue
		}
		want := vocabulary[act]
		if a.Shown != want.shown || !sameSet(a.Keys, want.presses) {
			t.Errorf("%s spells %s as [%s] %v, want [%s] %v", name, act, a.Shown, a.Keys, want.shown, want.presses)
		}
		if want.word != "" && a.Words != want.word && !strings.HasPrefix(a.Words, want.word+" ") {
			t.Errorf("%s says %q for %s, want %q", name, a.Words, act, want.word)
		}
	}
	// Digits take a numbered row wherever the rows are numbered: one binding,
	// the run from 1, printed as its range.
	for _, b := range []Binding{Select.Jump, Plan.Jump} {
		ks := b.Keys()
		for i, k := range ks {
			if k != fmt.Sprint(i+1) {
				t.Errorf("%q answers %v; a jump is the digits from 1 in order", Shown(b), ks)
				break
			}
		}
		if want := "1–" + ks[len(ks)-1]; Shown(b) != want {
			t.Errorf("a jump over %v prints %q, want %q", ks, Shown(b), want)
		}
	}

	stillOwed := map[string]bool{}
	for name, a := range names {
		if _, spelled := acts[name]; spelled || isApart(name, apart) || isApart(name, answers) {
			continue
		}
		for _, k := range a.Keys {
			switch {
			case k == "ctrl+c":
				t.Errorf("%s answers ctrl+c; the one destructive chord is the draft's cancel alone", name)
			case k == "q":
				t.Errorf("%s answers q; esc is the way back", name)
			case k == "ctrl+u" && name != "reading.half":
				t.Errorf("%s answers ctrl+u; esc clears a query", name)
			}
			act, ok := shared[k]
			if k == "x" {
				act, ok = "delete", true
			}
			if !ok {
				continue
			}
			if owed[name] == k {
				stillOwed[name] = true
				continue
			}
			t.Errorf("%s spends %q on %q, which is the shared %s", name, k, a.Words, act)
		}
	}
	for name := range owed {
		if !stillOwed[name] {
			t.Errorf("%s no longer breaks the vocabulary; take it off owed", name)
		}
	}

	// A screen may add a few letters of its own and no more.
	for _, s := range all() {
		var own []string
		for _, b := range s.Bindings {
			name := nameOf(b, names)
			if _, spelled := acts[name]; spelled || isApart(name, apart) || isApart(name, answers) {
				continue
			}
			for _, k := range b.Keys() {
				if r := []rune(k); len(r) == 1 && unicode.IsLetter(r[0]) && !slices.Contains(own, k) {
					own = append(own, k)
				}
			}
		}
		slices.Sort(own)
		got := strings.Join(own, " ")
		if want, listed := over[s.Name]; listed {
			if got != want {
				t.Errorf("%s has its own letters %q, and over says %q", s.Name, got, want)
			}
			continue
		}
		if len(own) > 3 {
			t.Errorf("%s has %d letters of its own (%s); a screen may add three", s.Name, len(own), got)
		}
	}
}

// nameOf is the keymap name of a binding a surface holds, read back from the
// register by what the binding is.
func nameOf(b Binding, names map[string]Act) string {
	for name, a := range names {
		if a.Shown == Shown(b) && a.Words == Words(b) && slices.Equal(a.Keys, b.Keys()) {
			return name
		}
	}
	return ""
}

func sameSet(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}
