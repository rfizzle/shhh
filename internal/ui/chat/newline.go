package chat

// Shift+Enter as a newline (S-134). Enter sends, so the draft needed a
// second key that does not; alt+enter was it, and nobody found it. Shift
// +Enter is the key people reach for, and it is the one key a terminal is
// least likely to report: in the legacy encoding Enter is a bare CR with
// nowhere to put a modifier, so a terminal that has not been asked to say
// more sends the same byte for both and the distinction never reaches us.
//
// Two halves make it work. RequestEnhancedKeys asks the terminal for xterm's
// modifyOtherKeys level 1, which reports exactly the modified keys that the
// legacy encoding cannot express and leaves every other key — esc and the
// ctrl chords this surface is built on — encoded as before. newlineKey then
// recognises what comes back, in both the shapes terminals send it in.
//
// Neither half is guaranteed: a terminal that ignores the request keeps
// sending a bare CR, and no amount of parsing invents the shift. So
// alt+enter stays bound, ctrl+j joins it, and the rail names shift+enter
// because that is what works nearly everywhere and the other two are the
// fallback rather than the instruction.

import (
	"io"
	"reflect"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	// modifyOtherKeysOn asks for level 1: report the modified keys that have
	// no unmodified representation. Level 2 would re-encode ordinary keys
	// too, which bubbletea v1's parser does not read.
	modifyOtherKeysOn = "\x1b[>4;1m"
	// modifyOtherKeysOff puts the terminal back the way it was found.
	modifyOtherKeysOff = "\x1b[>4;0m"
)

// RequestEnhancedKeys asks the terminal to report modified keys, and returns
// the function that puts it back. A terminal that does not know the sequence
// ignores it, so this is safe to call unconditionally.
func RequestEnhancedKeys(w io.Writer) func() {
	if w == nil {
		return func() {}
	}
	io.WriteString(w, modifyOtherKeysOn)
	return func() { io.WriteString(w, modifyOtherKeysOff) }
}

// newlineKey reports whether a message is Enter with any modifier on it —
// shift, ctrl or alt. Bubbletea v1 has no name for those, so they arrive as
// the unrecognised CSI sequence the terminal sent, which is the one place
// the modifier survives.
//
// Every modified Enter means the same thing here: put a line break in the
// draft, do not send it. Nothing on this surface wants ctrl+enter for
// anything else, so treating them alike costs nothing and spares the user
// having to know which encoding their terminal speaks.
func newlineKey(msg tea.Msg) bool {
	seq := unknownCSI(msg)
	if seq == "" {
		return false
	}
	params, final := splitCSI(seq)
	switch final {
	case 'u':
		// CSI 13 ; mods u — the CSI-u form (kitty, Ghostty, WezTerm,
		// iTerm2). Sub-parameters after a colon carry the event type and
		// the shifted key; only the leading number of each field matters.
		return len(params) >= 2 && leadingNum(params[0]) == 13 && leadingNum(params[1]) > 1
	case '~':
		// CSI 27 ; mods ; 13 ~ — xterm's modifyOtherKeys form.
		return len(params) >= 3 && leadingNum(params[0]) == 27 &&
			leadingNum(params[2]) == 13 && leadingNum(params[1]) > 1
	}
	return false
}

// altEnter is the key the textarea's newline binding listens for. A modified
// Enter is rewritten to it rather than handled here, so there is one newline
// path through the surface instead of two.
var altEnter = tea.KeyMsg{Type: tea.KeyEnter, Alt: true}

// unknownCSI returns the raw escape sequence behind a message bubbletea
// could not name, or "" for anything else. The message type is unexported by
// bubbletea, so it is recognised by its shape — a byte slice starting with
// CSI — rather than by a type assertion it will not admit.
func unknownCSI(msg tea.Msg) string {
	switch msg.(type) {
	case tea.KeyMsg, tea.MouseMsg, tea.WindowSizeMsg, nil:
		return ""
	}
	v := reflect.ValueOf(msg)
	if !v.IsValid() || v.Kind() != reflect.Slice || v.Type().Elem().Kind() != reflect.Uint8 {
		return ""
	}
	b := v.Bytes()
	if len(b) < 3 || b[0] != 0x1b || b[1] != '[' {
		return ""
	}
	return string(b)
}

// splitCSI breaks `ESC [ params final` into its semicolon-separated
// parameters and its final byte.
func splitCSI(seq string) ([]string, byte) {
	body := seq[2:]
	if body == "" {
		return nil, 0
	}
	final := body[len(body)-1]
	return strings.Split(body[:len(body)-1], ";"), final
}

// leadingNum reads the number at the head of one CSI parameter, ignoring any
// colon-separated sub-parameters after it. An unreadable field is 0, which
// matches no case above.
func leadingNum(param string) int {
	if i := strings.IndexByte(param, ':'); i >= 0 {
		param = param[:i]
	}
	n, err := strconv.Atoi(param)
	if err != nil {
		return 0
	}
	return n
}
