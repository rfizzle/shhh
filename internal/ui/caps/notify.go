package caps

// The other half of the OSC 99 question (S-157,
// docs/interface/surfaces.md#when-you-are-not-there).
//
// Query asks the terminal whether it can raise a desktop notification; this
// is shhh raising one. It lives here because the two are one protocol read in
// two directions — the reply that says which dialect this terminal speaks is
// the only thing that decides which dialect goes out — and because §10k's
// rule is that terminal sequences stop at this package. A notification
// composed anywhere else would be the second place in the tree that speaks
// the wire, which is the thing that rule exists to prevent.
//
// There is no native backend. Crush picks between the escape sequence and the
// operating system's own notification daemon; shhh cannot, because the
// machine running shhh is not always the machine the reader is sitting at.
// Over ssh a native notification is raised on the server, where nobody is;
// the escape sequence travels back down the connection to the terminal that
// is actually in front of them. One dialect that is right everywhere beats
// two that are each right somewhere, and it is also one fewer dependency.

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// notifyID groups the sequences of one notification: OSC 99 sends the title,
// the body and the "that is all" mark as three writes, and the identifier is
// what says they are one notification rather than three.
//
// It is a constant rather than a counter because shhh only ever has one thing
// to say. It says "come back" when the session stops needing shhh, and it is
// not saying it again until the reader has come back (§10l). Where a terminal
// treats a repeated identifier as an update, that is the right behaviour too:
// the second summons replaces the first rather than stacking behind it, which
// is what someone returning to four notifications would have wanted.
const notifyID = "shhh-notify"

// notifyApp names shhh to terminals that group notifications by application.
// OSC 777 has no field for it, which is why the title says the name as well.
const notifyApp = "shhh"

// notifyTitleMax and notifyBodyMax bound what goes out. A notification is a
// summons, not a document — the card with the whole command on it is on the
// screen the reader is being called back to — and a title longer than a
// notification panel is a title the panel truncates on its own terms.
const (
	notifyTitleMax = 64
	notifyBodyMax  = 160
)

// Notify raises one desktop notification, in whichever dialect this terminal
// answered for.
//
//   - OSC 99 where the terminal said it can carry a title (Notifications):
//     the extensible protocol, sent as the three writes it wants.
//   - OSC 777 otherwise — the urxvt extension, which has no query and no
//     reply, so it is either understood or swallowed. That is why §10k calls
//     it the blind fallback: silence from the OSC 99 query is not "this
//     terminal cannot notify", it is "this terminal did not say", and the
//     answer to a terminal that did not say is to try the older thing quietly
//     rather than to give up.
//
// It writes nothing when there is no terminal on the other end. Asked is
// false in exactly that case (Query returns early on a NoTTY profile), so the
// guard is the same one that stops the questions going into a pipe.
func (t Terminal) Notify(title, body string) tea.Cmd {
	if !t.Asked {
		return nil
	}
	title, body = notifyPlain(title, notifyTitleMax), notifyPlain(body, notifyBodyMax)
	if title == "" {
		return nil
	}
	if !t.Notifications {
		return tea.Raw(ansi.URxvtExt("notify", notify777(title), notify777(body)))
	}
	var b strings.Builder
	b.WriteString(ansi.DesktopNotification(title, "i="+notifyID, "d=0", "p=title", "a="+notifyApp))
	if body != "" {
		b.WriteString(ansi.DesktopNotification(body, "i="+notifyID, "d=0", "p=body", "a="+notifyApp))
	}
	// d=1 is "that is the whole notification"; the terminal raises it here.
	b.WriteString(ansi.DesktopNotification("", "i="+notifyID, "d=1", "a="+notifyApp))
	return tea.Raw(b.String())
}

// notifyPlain reduces one line of shhh's own screen text to something safe to
// hand a terminal as a notification.
//
// This is not tidying. What goes into a notification is a command the model
// asked to run and a path it asked to write — text shhh did not author — and
// it is going out as bytes the terminal reads rather than as a string a View
// draws. An escape sequence that survived the trip would be a sequence the
// model got to write straight to the terminal, so the sequences come off
// first and every remaining control character with them. What is left is
// folded onto one line, because a notification panel is one.
func notifyPlain(s string, max int) string {
	s = ansi.Strip(s)
	s = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		// The ellipsis says the sentence was cut rather than that it ended.
		return strings.TrimRight(string(r[:max-1]), " ") + "…"
	}
	return s
}

// notify777 takes the one character OSC 777 cannot carry out of a field: its
// parameters are separated by `;`, so one inside the text would push the body
// into a field the extension does not have and the reader would be shown half
// a command. It leaves a space rather than nothing, so `cd src; make` still
// reads as two commands rather than as one that does not exist, and the line
// is re-folded so the substitution does not show as a double space.
func notify777(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, ";", " ")), " ")
}
