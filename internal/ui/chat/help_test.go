package chat

// /help against the register (
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// Finding 3 of the polish review named the shape of this bug before the
// register existed: "the copy and the handler are in different places and
// nothing enforces that they agree". The first thing this test found when it
// was written was that `/help` had never heard of the handover — the chord
// the mid-sentence rule is built on, the one key a waiting approval answers
// to, and the only way to reach any of the decision keys from a live draft.
//
// So: every key the input frame offers is named in /help's key section. What
// the paragraph beside it says is /help's business — this asserts only that
// the key is there at all, because a key nobody can find is the one failure
// mode a help text has.

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

func TestHelpNamesEveryDraftKey(t *testing.T) {
	m := frameModel(t, 80, 30)
	help := strings.ToLower(helpText(&m))
	section := help[strings.Index(help, "keys:"):]
	if section == "" {
		t.Fatal("/help has no key section")
	}
	for _, b := range keys.Surfaces()[0].Bindings {
		named := strings.Contains(section, strings.ToLower(keys.Shown(b)))
		for _, k := range b.Keys() {
			named = named || strings.Contains(section, strings.ToLower(k))
		}
		if !named {
			t.Errorf("/help never names %q (%s)", keys.Shown(b), keys.Words(b))
		}
	}
}

// The palette's chord is a single byte, and a terminal that sends nothing for
// it would leave the surface unreachable with no way to find that out. So the
// key list names the other door beside it, which is the place a reader whose
// chord did nothing goes looking.
func TestHelpNamesThePalettesOtherDoor(t *testing.T) {
	keysText := helpKeysText()
	i := strings.Index(keysText, keys.Shown(keys.Draft.Palette))
	if i < 0 {
		t.Fatalf("the key list never names %q", keys.Shown(keys.Draft.Palette))
	}
	// The paragraph the chord opens, up to the next key's row.
	para := keysText[i:]
	if j := strings.Index(para, "\n  "+keys.Shown(keys.Draft.Pause)); j > 0 {
		para = para[:j]
	}
	if !strings.Contains(para, "/ on an empty draft") || !strings.Contains(para, "tab") {
		t.Errorf("the palette's row does not name its other door:\n%s", para)
	}
}

// The reading-mode register is reachable from the draft too, because `?` is a
// letter there and /help is the door a reader with a live draft has.
func TestHelpNamesTheKeyRegister(t *testing.T) {
	m := frameModel(t, 80, 30)
	if !strings.Contains(helpText(&m), keys.Shown(keys.Reading.List)+" lists every key") {
		t.Errorf("/help does not say what %q opens in reading mode", keys.Shown(keys.Reading.List))
	}
}

// The key list is rendered from the register, so the two cannot say different
// things about a key's spelling. What they can still disagree about is
// coverage: a binding added to the register with no row here would simply not
// be in the list, and a row naming a binding the register has dropped would
// draw an empty column. So the rows and the register are held to each other
// as sets.
func TestHelpKeyRowsCoverTheRegister(t *testing.T) {
	named := map[string]bool{}
	for _, r := range helpKeyRows {
		for _, b := range r.binds {
			shown := keys.Shown(b)
			if named[shown] {
				t.Errorf("%q has two rows in the key list", shown)
			}
			named[shown] = true
		}
	}
	for _, b := range keys.Surfaces()[0].Bindings {
		if !named[keys.Shown(b)] {
			t.Errorf("the key list has no row for %q (%s)", keys.Shown(b), keys.Words(b))
		}
		delete(named, keys.Shown(b))
	}
	for shown := range named {
		t.Errorf("the key list names %q, which the input frame does not offer", shown)
	}
}

// And the rendered list spells each key the way the register does. A row that
// spells its own column does so because the list reads it differently from a
// one-line hint — the recall arrows are the only pair that does — so that is
// the one thing the check allows, and only where the row said so.
func TestHelpKeyColumnsComeFromTheRegister(t *testing.T) {
	for _, r := range helpKeyRows {
		if r.key != "" || len(r.binds) == 0 {
			continue
		}
		for _, col := range r.column() {
			if !strings.Contains(helpKeysText(), "\n  "+col+" ") {
				t.Errorf("the rendered list has no row headed %q", col)
			}
		}
	}
}

// The rows are the registry's, filtered by what this session has wired. That
// is the whole of the fix: the help, the completion menu and the answer to a
// typed command were three readings of one question, and only the help was
// taken from a list of its own — which is how a conversation offered `/todo`
// and then said `/todo` was not part of the session.
func TestHelp_ListsExactlyTheCommandsThisSessionHas(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(t *testing.T) Model
	}{
		{"a coding session", func(t *testing.T) Model { return frameModel(t, 80, 40) }},
		{"a conversation", func(t *testing.T) Model { return frameModel(t, 80, 40).WithConversation() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.build(t)
			rows := map[string]bool{}
			for _, line := range strings.Split(helpText(&m), "\n") {
				head, _, ok := strings.Cut(strings.TrimPrefix(line, "  "), " ")
				if ok && strings.HasPrefix(head, "/") && strings.HasPrefix(line, "  /") {
					rows[head] = true
				}
			}
			for _, c := range slashCommands() {
				has := c.enabled == nil || c.enabled(&m)
				if has && !rows[c.name] {
					t.Errorf("%s is offered here and has no row in /help", c.name)
				}
				if !has && rows[c.name] {
					t.Errorf("%s has a row in /help and is not offered here", c.name)
				}
				delete(rows, c.name)
			}
			for name := range rows {
				t.Errorf("/help lists %s, which is not a command", name)
			}
		})
	}
}

// Every command in the registry has a paragraph. One added without gets an
// empty row rather than a missing one, which is the failure a reader cannot
// see and a test can.
func TestHelp_EveryCommandHasAParagraph(t *testing.T) {
	named := map[string]bool{}
	for _, c := range slashCommands() {
		if strings.TrimSpace(helpCommands[c.name]) == "" {
			t.Errorf("%s has no paragraph in /help", c.name)
		}
		named[c.name] = true
	}
	for name := range helpCommands {
		if !named[name] {
			t.Errorf("/help keeps a paragraph for %s, which is not a command", name)
		}
	}
}

// The conversation's list is the coding session's minus what it does not
// have, and the backlog is on both.
func TestHelp_AConversationOffersTheBacklogAndNotTheChangeset(t *testing.T) {
	root := todoTestRoot(t)
	m := frameModel(t, 80, 40).WithConversation().WithTodos(Todos{
		Profile: todo.BuiltinCode(), Root: root,
		Manage: func([]string) string { return "" },
		Detail: func(*todo.Store, todo.Item) string { return "" }})
	help := helpText(&m)
	if !strings.Contains(help, "\n  /todo ") && !strings.Contains(help, "\n  /todo  ") {
		t.Errorf("a conversation with a backlog should have a /todo row:\n%s", help)
	}
	for _, gone := range []string{"\n  /diff", "\n  /review", "\n  /undo", "\n  /plan", "\n  /sandbox"} {
		if strings.Contains(help, gone) {
			t.Errorf("a conversation has no %q row", strings.TrimSpace(gone))
		}
	}
}
