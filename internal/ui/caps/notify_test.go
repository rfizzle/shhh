package caps

// The summons against the terminals it will reach (S-157,
// docs/interface/surfaces.md#when-you-are-not-there).
//
// Three things are worth asserting. That the dialect follows the answer —
// OSC 99 where the terminal said it can carry a title, OSC 777 where it did
// not, nothing at all where there is no terminal. That the parts of one OSC
// 99 notification are tied together and closed. And that what shhh did not
// write — a command the model asked to run — cannot reach the terminal as
// anything but text.

import (
	"strings"
	"testing"
)

func TestNotify_UsesOSC99WhenTheTerminalAnswered(t *testing.T) {
	term := Terminal{Asked: true, Notifications: true}
	seq := raw(t, term.Notify("shhh · Approve command", "Assistant wants to run: go test ./..."))

	for _, want := range []string{
		"i=" + notifyID,
		"p=title",
		"p=body",
		"a=" + notifyApp,
		"d=1",
		"shhh · Approve command",
		"Assistant wants to run: go test ./...",
	} {
		if !strings.Contains(seq, want) {
			t.Errorf("the OSC 99 notification does not carry %q:\n%q", want, seq)
		}
	}
	if strings.Contains(seq, "777") {
		t.Errorf("a terminal that answered the query was sent the blind fallback:\n%q", seq)
	}
	// The three writes are one notification, and the last one is what raises
	// it: without d=1 the terminal is still waiting for more parts.
	if n := strings.Count(seq, "i="+notifyID); n != 3 {
		t.Errorf("expected the title, the body and the done mark to share one identifier, got %d parts", n)
	}
}

func TestNotify_OmitsTheBodyPartWhenThereIsNoBody(t *testing.T) {
	term := Terminal{Asked: true, Notifications: true}
	seq := raw(t, term.Notify("shhh · Turn done", ""))

	if strings.Contains(seq, "p=body") {
		t.Errorf("an empty body was sent as a body part:\n%q", seq)
	}
	if !strings.Contains(seq, "d=1") {
		t.Errorf("the notification was never closed:\n%q", seq)
	}
}

func TestNotify_FallsBackToOSC777WhenTheTerminalDidNotAnswer(t *testing.T) {
	term := Terminal{Asked: true}
	seq := raw(t, term.Notify("shhh · Turn done", "Done · 3 steps"))

	if !strings.HasPrefix(seq, "\x1b]777;notify;") {
		t.Errorf("expected the urxvt extension, got %q", seq)
	}
	if strings.Contains(seq, "p=title") {
		t.Errorf("a terminal that never said it speaks OSC 99 was sent it:\n%q", seq)
	}
}

func TestNotify_TakesTheOne777CannotCarryOutOfTheText(t *testing.T) {
	term := Terminal{Asked: true}
	seq := raw(t, term.Notify("shhh · Approve command", "Assistant wants to run: cd src; make"))

	// Three fields: the extension name, the title and the body. A semicolon
	// left in the text would make four, and the body would arrive truncated
	// at "cd src".
	body := strings.TrimRight(strings.TrimPrefix(seq, "\x1b]777;"), "\a")
	if n := strings.Count(body, ";"); n != 2 {
		t.Errorf("expected notify;title;body, got %d separators: %q", n, body)
	}
	if !strings.Contains(seq, "cd src make") {
		t.Errorf("the command lost more than its separator:\n%q", seq)
	}
}

func TestNotify_WritesNothingWithoutATerminal(t *testing.T) {
	var term Terminal // never asked: Query returns early on a NoTTY profile
	if cmd := term.Notify("shhh · Turn done", "Done"); cmd != nil {
		t.Error("shhh wrote a notification into something that is not a terminal")
	}
}

func TestNotifyPlain_StripsWhatShhhDidNotWrite(t *testing.T) {
	// The body of an approval notification is a command the model asked to
	// run. It is going out as bytes the terminal reads, so a sequence that
	// survived the trip would be a sequence the model wrote to the terminal.
	got := notifyPlain("run \x1b[31mred\x1b[0m\x07 and\nthen\tstop", notifyBodyMax)
	if want := "run red and then stop"; got != want {
		t.Errorf("notifyPlain = %q, want %q", got, want)
	}
	for _, r := range got {
		if r < 0x20 {
			t.Fatalf("a control character survived: %q", got)
		}
	}
}

func TestNotifyPlain_CutsALongLineAndSaysSo(t *testing.T) {
	got := notifyPlain(strings.Repeat("a", 200), notifyBodyMax)
	if n := len([]rune(got)); n != notifyBodyMax {
		t.Errorf("expected %d runes, got %d", notifyBodyMax, n)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a cut line does not say it was cut: %q", got)
	}
}

func TestNotify_SaysNothingWithoutATitle(t *testing.T) {
	term := Terminal{Asked: true, Notifications: true}
	if cmd := term.Notify("  \x1b[0m ", "a body with no title"); cmd != nil {
		t.Error("expected no notification when the title reduces to nothing")
	}
}
