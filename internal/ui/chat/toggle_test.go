package chat

import "testing"

// Every switch in the session reads its argument through parseToggle, so this
// table is the whole vocabulary: change it and `/ui mouse`, `/ui mono`,
// `/ui ground`, `/ui notify`, `/ui title`, `/ui window` and `/gate` all
// change together, which is the point of there being one.
func TestParseToggle(t *testing.T) {
	for _, tc := range []struct {
		word   string
		on, ok bool
	}{
		{word: "on", on: true, ok: true},
		{word: "true", on: true, ok: true},
		{word: "yes", on: true, ok: true},
		{word: "off", on: false, ok: true},
		{word: "false", on: false, ok: true},
		{word: "no", on: false, ok: true},
		{word: ""},
		{word: "On"},
		{word: "1"},
		{word: "enable"},
		{word: "run"},
	} {
		on, ok := parseToggle(tc.word)
		if on != tc.on || ok != tc.ok {
			t.Errorf("parseToggle(%q) = %v %v, want %v %v", tc.word, on, ok, tc.on, tc.ok)
		}
	}
}

// A word that names neither side leaves the command to say so in its own
// words, naming the setting the reader was trying to set.
func TestToggleCommandsRefuseAnUnknownWord(t *testing.T) {
	m := New(nil, mockStream)
	for _, tc := range []struct {
		line string
		want string
	}{
		{line: "/ui mouse maybe", want: `Error: unknown mouse setting "maybe" (on, off)`},
		{line: "/ui mono maybe", want: `Error: unknown mono setting "maybe" (on, off)`},
		{line: "/ui ground maybe", want: `Error: unknown ground setting "maybe" (on, off)`},
		{line: "/ui notify maybe", want: `Error: unknown notify setting "maybe" (on, off)`},
		{line: "/ui title maybe", want: `Error: unknown title setting "maybe" (on, off)`},
		{line: "/ui window maybe", want: `Error: unknown window setting "maybe" (on, off)`},
	} {
		handled, note := m.handleSlashCommand(tc.line)
		if !handled || note != tc.want {
			t.Errorf("%s = %v %q, want %q", tc.line, handled, note, tc.want)
		}
	}
}
