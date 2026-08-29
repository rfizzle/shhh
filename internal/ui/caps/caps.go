// Package caps is what the terminal can do (S-156, DESIGN-TUI.md §10k).
//
// Everything else in shhh adapts to the terminal by reading the environment
// and measuring the window: NO_COLOR, TERM=dumb, the width the ladders drop
// against (§8c). None of that can answer whether the terminal draws inline
// images, whether it will raise a desktop notification, or whether it tells
// shhh when the window loses focus — those are questions only the terminal
// itself can answer, and answering them means asking: writing a query
// sequence out and reading the reply back as an ordinary key-ish event.
//
// So this package is the one place that speaks the protocol. It writes the
// questions (Query), it reads the answers (Update), and every terminal-wire
// type stops here — nothing else in the tree imports ultraviolet's event
// vocabulary or composes an escape sequence to ask something. What leaves is
// a value with plain fields on it.
//
// Ported from Crush's internal/ui/common/capabilities.go, with four places
// where shhh's semantics win:
//
//   - It does not answer for the colour profile. Crush folds
//     tea.ColorProfileMsg into the same value; shhh settled the profile once,
//     from stdout and the environment, and handed that same answer to every
//     program and every direct print (S-155, components.Profile). A second
//     answer to a question already decided is the thing S-155 exists to
//     prevent, so the profile is not a field here and SupportsTrueColor is
//     not a method.
//   - It does not answer for the size either. Crush keeps Columns and Rows
//     beside the pixels; in shhh the width is the layout's — it is what
//     every drop ladder in §8b and §6a reads — and it already has an owner
//     in the host model. So CellSize is handed the cells it should divide
//     by, and this value stays about the terminal rather than about the
//     window.
//   - Silence and never having asked are different answers. Crush's booleans
//     are false in both cases; here Asked and Held say which, because a
//     reader looking at a readout that says "no inline images" is owed the
//     difference between a terminal that said no and one shhh declined to
//     interrupt (§10k).
//   - The reply is read where uv already framed it, not parsed again. Crush
//     runs the OSC 99 reply back through a fresh ansi.Parser; by the time
//     the event arrives the sequence has been decoded once already, so the
//     only thing between the reply and its fields is the framing.
package caps

import (
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// Terminal is what one terminal told shhh it can do. Its zero value is a
// terminal nobody has asked: every capability false, which is also the safe
// reading of a terminal that was asked and stayed silent.
type Terminal struct {
	// Name is how the terminal named itself to XTVERSION — "ghostty 1.2.0",
	// "WezTerm 20240203". Empty when it did not answer or was not asked.
	Name string

	// PixelWidth and PixelHeight are the window in pixels, which is the only
	// way to a cell size (CellSize) and therefore the only way to know how
	// many pixels an inline image may be.
	PixelWidth, PixelHeight int

	// Kitty reports whether the terminal answered the Kitty graphics query.
	Kitty bool
	// Sixel reports whether the terminal listed attribute 4 — Sixel
	// graphics — in its primary device attributes.
	Sixel bool
	// FocusEvents reports whether the terminal knows DECSET 1004, and so can
	// tell shhh when the window stops being the one in front.
	FocusEvents bool
	// Notifications reports whether the terminal answered the OSC 99 query
	// saying it can raise a desktop notification with a title.
	Notifications bool

	// Asked is true once the questions have gone out. Until then every
	// field above is false because nothing has been asked, not because the
	// terminal said no.
	Asked bool
	// Held is why the graphics questions were held back, in words, and empty
	// when they were not. A terminal that prints a query instead of
	// answering it is not asked (held), and neither is one on the far end of
	// an ssh session; both leave Kitty, Sixel, PixelWidth and Name at their
	// zero values for a reason that is not "no".
	Held string
}

// Query writes the questions and records that they went out. environ is the
// program's own environment rather than the process's: over ssh those are
// two different machines, and it is the client's terminal being asked
// (tea.EnvMsg).
//
// It returns nil when there is no terminal to ask — shhh drawing into a pipe,
// a log or a CI run, where a query sequence is garbage in a file and the
// reply never comes. That question was also settled once already: a profile
// of NoTTY is what "there is nothing on the other end" looks like (S-155).
//
// Two tiers go out, and the split is the port's. The first three are
// well-formed requests every terminal either answers or swallows. The last
// three are the ones known to land on screen as text on a terminal that does
// not recognise them, so they are asked only where the answer is worth the
// risk (held).
func (t *Terminal) Query(environ []string) tea.Cmd {
	if components.Profile() == colorprofile.NoTTY {
		t.Held = "shhh is not drawing on a terminal"
		return nil
	}
	env := uv.Environ(environ)
	t.Asked = true
	t.Held = held(env)

	var b strings.Builder
	b.WriteString(ansi.RequestPrimaryDeviceAttributes)
	b.WriteString(ansi.RequestModeFocusEvent)
	b.WriteString(notifyQuery())
	if t.Held == "" {
		b.WriteString(ansi.RequestNameVersion)
		// Window op 14 is the window's size in pixels; the cell size falls
		// out of it and the terminal's own columns and rows (CellSize).
		b.WriteString(ansi.WindowOp(14))
		// A one-pixel image the terminal is asked to acknowledge rather than
		// draw. Inside tmux the sequence has to be told it is passing
		// through, or tmux eats it on the way to the terminal that would
		// answer.
		kitty := ansi.KittyGraphics([]byte("AAAA"), "i=31", "s=1", "v=1", "a=q", "t=d", "f=24")
		if _, inTmux := env.LookupEnv("TMUX"); inTmux {
			kitty = ansi.TmuxPassthrough(kitty)
		}
		b.WriteString(kitty)
	}
	// One write, in one order. tea.Raw is the door for bytes that are a
	// question rather than a picture, and it is the only place in shhh that
	// writes outside a View.
	return tea.Raw(b.String())
}

// Update folds one reply in. The answers arrive as five unrelated message
// types, spread over however long the terminal takes, and each of them is
// nothing else's business — so a host calls this with every message it sees
// and this decides which ones were addressed to it.
//
// A yes is never taken back. A terminal can be asked more than once across a
// session (a resize re-reports its pixels, tmux re-attaches), and a reply
// that does not repeat an earlier yes is a reply about something else, not a
// retraction.
func (t *Terminal) Update(msg tea.Msg) {
	switch m := msg.(type) {
	case uv.PrimaryDeviceAttributesEvent:
		// Attribute 4 is Sixel. The rest of the list describes a VT model
		// nothing here asks about.
		if slices.Contains(m, 4) {
			t.Sixel = true
		}
	case uv.PixelSizeEvent:
		t.PixelWidth, t.PixelHeight = m.Width, m.Height
	case uv.KittyGraphicsEvent:
		// The terminal answered a graphics query at all, which is the whole
		// question — what it said about the one-pixel image does not matter.
		t.Kitty = true
	case tea.TerminalVersionMsg:
		t.Name = m.Name
	case tea.ModeReportMsg:
		if m.Mode == ansi.ModeFocusEvent {
			// Set or reset both mean the terminal knows the mode; only "not
			// recognized" means it does not.
			t.FocusEvents = t.FocusEvents || m.Value.IsSet() || m.Value.IsReset()
		}
	case uv.UnknownOscEvent:
		if notifySupported(string(m)) {
			t.Notifications = true
		}
	}
}

// CellSize is one character cell in pixels, given the terminal's own columns
// and rows — which the host model already holds, and which are not this
// package's to keep a second copy of.
//
// It is (0, 0) when anything it divides by is missing, which is every
// terminal that was not asked and every terminal that did not answer.
func (t Terminal) CellSize(cols, rows int) (width, height int) {
	if cols <= 0 || rows <= 0 || t.PixelWidth <= 0 || t.PixelHeight <= 0 {
		return 0, 0
	}
	return t.PixelWidth / cols, t.PixelHeight / rows
}

// Graphics reports whether the terminal can draw an image at all, by either
// protocol. Kitty is preferred where both are offered; this is the question
// of whether there is any picture to be had.
func (t Terminal) Graphics() bool { return t.Kitty || t.Sixel }

// answering lists the terminals that answer XTVERSION and the graphics
// queries, by the name they put in TERM. They are asked even where the rule
// below would otherwise hold the questions back: a terminal that is known to
// answer cannot print the question instead.
var answering = []string{"alacritty", "ghostty", "kitty", "rio", "wezterm"}

// held is why the graphics questions are not asked here, or empty when they
// are.
//
// Crush writes this as three or-ed clauses; after its own early return for
// Apple Terminal, two of them are the same clause and the whole expression
// reduces to "not over ssh, or a terminal that answers". It is written as
// the one rule it is, in the order the rule is read: a terminal that prints
// questions is never asked, a terminal known to answer always is, and
// everything else is asked unless the reply would have to cross a network.
func held(env uv.Environ) string {
	if prog, ok := env.LookupEnv("TERM_PROGRAM"); ok && strings.Contains(prog, "Apple") {
		return "Apple Terminal prints unknown queries instead of answering them"
	}
	term := env.Getenv("TERM")
	for _, name := range answering {
		if strings.Contains(term, name) {
			return ""
		}
	}
	if _, ok := env.LookupEnv("SSH_TTY"); ok {
		return "the reply would have to come back over ssh"
	}
	return ""
}

// notifyQueryID is the identifier shhh puts on its OSC 99 query so the reply
// can be told from a notification some other program on the same terminal
// asked about.
const notifyQueryID = "shhh-osc99-query"

// notifyQuery asks the terminal what its desktop notifications can carry: an
// OSC 99 with no payload, our identifier, and p=? for "what do you support".
func notifyQuery() string {
	return ansi.DesktopNotification("", "i="+notifyQueryID, "p=?")
}

// notifySupported reads an OSC reply and reports whether it is this query's
// answer, saying the terminal can raise a notification with a title.
//
// The reply is OSC 99 ; <metadata> ; <payload> ST, where the metadata echoes
// the identifier and the query mark and the payload lists what is supported.
// All three have to be there: an OSC 99 that is not ours, or is ours but does
// not list title, is not an answer to this.
func notifySupported(seq string) bool {
	cmd, body, ok := oscBody(seq)
	if !ok || cmd != 99 {
		return false
	}
	metadata, payload, found := strings.Cut(body, ";")
	if !found {
		return false
	}
	var mine, isAnswer bool
	for _, field := range strings.Split(metadata, ":") {
		mine = mine || field == "i="+notifyQueryID
		isAnswer = isAnswer || field == "p=?"
	}
	if !mine || !isAnswer {
		return false
	}
	for _, field := range strings.Split(payload, ":") {
		key, value, cut := strings.Cut(field, "=")
		if !cut || key != "p" {
			continue
		}
		if slices.Contains(strings.Split(value, ","), "title") {
			return true
		}
	}
	return false
}

// oscBody takes the framing off one OSC sequence and returns the command
// number and everything after it.
//
// uv decodes the sequence before handing it over — it found the introducer,
// found the terminator and knew where the event ended — so what is left is
// the frame, not a parse. Crush builds a whole ansi.Parser here and feeds the
// string back through it a byte at a time to reach the same two fields.
func oscBody(seq string) (cmd int, body string, ok bool) {
	switch {
	case strings.HasPrefix(seq, "\x1b]"):
		seq = seq[2:]
	case strings.HasPrefix(seq, "\x9d"):
		seq = seq[len("\x9d"):]
	default:
		return 0, "", false
	}
	for _, terminator := range []string{"\x1b\\", "\a", "\x9c"} {
		if strings.HasSuffix(seq, terminator) {
			seq = seq[:len(seq)-len(terminator)]
			break
		}
	}
	number, rest, found := strings.Cut(seq, ";")
	if !found {
		return 0, "", false
	}
	cmd, err := strconv.Atoi(number)
	if err != nil {
		return 0, "", false
	}
	return cmd, rest, true
}
