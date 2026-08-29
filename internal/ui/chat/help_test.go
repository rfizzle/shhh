package chat

// /help against the register (S-153, DESIGN-TUI.md §7d).
//
// Finding 3 of the polish review named the shape of this bug before the
// register existed: "the copy and the handler are in different places and
// nothing enforces that they agree". The first thing this test found when it
// was written was that `/help` had never heard of ctrl+g — the chord §7b is
// built on, the one key a waiting approval answers to, and the only way to
// reach any of the decision keys from a live draft.
//
// So: every key the input frame offers is named in /help's key section. What
// the paragraph beside it says is /help's business — this asserts only that
// the key is there at all, because a key nobody can find is the one failure
// mode a help text has.

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/ui/keys"
)

func TestHelpNamesEveryDraftKey(t *testing.T) {
	help := strings.ToLower(helpText())
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

// The reading-mode register is reachable from the draft too, because `?` is a
// letter there (§7c) and /help is the door a reader with a live draft has.
func TestHelpNamesTheKeyRegister(t *testing.T) {
	if !strings.Contains(helpText(), keys.Shown(keys.Reading.List)+" lists every key") {
		t.Errorf("/help does not say what %q opens in reading mode", keys.Shown(keys.Reading.List))
	}
}
