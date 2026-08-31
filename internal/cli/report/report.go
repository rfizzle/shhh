// Package report is the shape every non-interactive shhh command prints in.
//
// Help, the line a mistyped flag prints and what is left in the scrollback all
// obey one set of rules: sectioned rather than dumped, a failure a labelled
// block naming one thing and one way out, labels that are words so a terminal
// with no colour loses the tint and no distinction
// (docs/interface/surfaces.md#outside-the-tui). Eleven listing commands each
// invented a table of their own before this package existed, which left a
// reader eleven shapes to learn instead of one.
//
// A report is data until it is rendered. Render produces plain bytes at a
// width; colour is added on the way out and only where the destination can
// show it, so a pipe, TERM=dumb and NO_COLOR all produce the same bytes and
// colour is never carrying anything a glyph or a word is not already carrying
// (docs/interface/principles.md#colour-never-carries-meaning-alone).
package report

import (
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// indent is how far the whole report sits from the left edge. It is fang's
// own margin on `shhh --help`, and the point of matching it is that a listing
// and the help that names it should look like one product.
const indent = 2

// FallbackWidth is what a report is drawn at when the destination has no
// width to give — a pipe, a file, a closed terminal. It is the same 80 the
// exit banner falls back to.
const FallbackWidth = 80

// NameCap is the widest a sized name column grows to. A section sizes its
// name column to its longest name so a model id is not clipped to the
// doctor's eight columns, but a name past this is a name that belongs in the
// subject field.
const NameCap = 20

// State is what a row found, in the closed vocabulary the doctor's report and
// the transcript already use. The glyph is what carries it; colour only
// reinforces it (docs/interface/principles.md#colour-never-carries-meaning-alone).
type State int

const (
	// Pass — ✓: it is there and it works.
	Pass State = iota
	// Warn — ⚠: it works, and something about it will bite.
	Warn
	// Fail — ✗: it does not work.
	Fail
	// Skip — ⊘: there was nothing here, or it was deliberately left out.
	Skip
	// Run — ▸: it is happening, or it is the thing to do next.
	Run
	// Queue — ·: accepted, not started.
	Queue
)

// Glyph is the state's mark. The table is the doctor's, because a report
// pasted somewhere else should still read as this product's.
func (s State) Glyph() string {
	switch s {
	case Warn:
		return "⚠"
	case Fail:
		return "✗"
	case Skip:
		return "⊘"
	case Run:
		return "▸"
	case Queue:
		return "·"
	}
	return "✓"
}

// StateOf reads a doctor check's state as a report state. The two vocabularies
// are the same one; this is the seam that keeps the report package from
// depending on the doctor screen's enum in its own signatures.
func StateOf(d components.DoctorState) State {
	switch d {
	case components.DoctorWarned:
		return Warn
	case components.DoctorFailed:
		return Fail
	case components.DoctorSkipped:
		return Skip
	case components.DoctorRunning:
		return Run
	case components.DoctorQueued:
		return Queue
	}
	return Pass
}

// Row is one line of a listing: a glyph for what it found, a name, what the
// row is about, and the outcome that is the reason to read it.
type Row struct {
	State State
	// Name is the row's left field — `openai`, `binary`, `m3`. A section
	// sizes the column to its longest name, so a vocabulary that drifts wide
	// shows up as a wide column rather than as clipped names.
	Name string
	// Subject leads the target field: the thing the row is about.
	Subject string
	// Detail continues the target after ` · `: the path, the version, the
	// count behind the subject. It is the dim half.
	Detail string
	// Outcome is the bracketed field at the end and never clips: a row that
	// cannot fit gives up its target, because the outcome is why the row is
	// on the screen.
	Outcome string
	// Consequence is the line under a row that did not pass: what the reader
	// will see because of it. A failure that does not say what it costs is a
	// failure the reader has to go and find out about.
	Consequence string
	// Body are the lines a row carries because they are the thing itself and
	// not a field of it — the command a history entry generated, the text a
	// memory holds. They wrap rather than clip: the row's target is a label
	// for them, so cutting them would leave the label pointing at nothing.
	Body []string
	// Fix are the lines beneath that: the commands, the config keys, the
	// order to do them in.
	Fix []string
}

// Pair is one line of a key/value block, aligned on the colon.
type Pair struct {
	Key, Value string
}

// Note is a warning or a diagnostic that belongs to the whole report rather
// than to any one row — a catalog entry that would not load, a profile file
// that could not be read. Before this block those lines were printed bare
// after the report and read as a second voice.
type Note struct {
	State State
	Text  string
}

// Section is one labelled block: rows, a key/value block, or a preview of a
// file, under a header drawn the way fang draws USAGE and COMMANDS.
type Section struct {
	// Header is the section's name. It is upper-cased on the way out, so
	// callers write it as the word it is.
	Header string
	Rows   []Row
	Pairs  []Pair
	// Body is a preview: a file about to be written, a command as saved. It
	// is printed verbatim under the header, indented past the rows, and is
	// never clipped — a preview clipped to the terminal is not a preview.
	Body string
	// NameWidth pins the name column instead of sizing it to the section. The
	// doctor and mcp reports pin it to eight, the width their closed
	// vocabularies are disciplined by; everything else leaves this zero and
	// gets a column sized to its own longest name.
	NameWidth int
}

// Report is one command's output.
type Report struct {
	// Title is the command, and Subject the tally of nouns beside it:
	// `shhh providers — 6 built in · 1 profile`.
	Title    string
	Subject  string
	Sections []Section
	Notes    []Note
	// Tally is the closing line: `1 failed · 1 passed · 1 not checked`.
	Tally string
}

// Empty is the row a command with nothing to list prints: what is absent, and
// one way out the reader can type at the prompt they are already at. Every
// empty state in the CLI is this row, so an empty screen is information
// rather than a shrug.
func Empty(absent, wayOut string) Row {
	return Row{State: Skip, Subject: absent, Detail: wayOut}
}

// Done is the row a command prints after it writes something: `✓ saved
// snippet ports`. The verb is what happened and the thing is what it happened
// to, which is the whole sentence a confirmation owes the reader.
func Done(verb, thing string) Row {
	return Row{State: Pass, Subject: verb + " " + thing}
}

// Render is the report as plain bytes at a width. Nothing here is styled:
// this is the text a pipe gets, and it is the text the painted render is
// measured and clipped as before any colour is added.
func (r Report) Render(width int) string {
	return r.render(width, plain)
}

// String renders for a caller with no stream to measure — a slash command's
// answer, which goes into the transcript rather than to a file descriptor.
//
// It takes the fallback width rather than a generous one. A row is laid out so
// that the target clips and the outcome never does, and that rule only holds
// while the width it was drawn at is at most the width it is displayed at:
// rendered wider, nothing clips but the surface soft-wraps the row and the
// outcome lands on a line of its own, which is the failure the layout exists
// to prevent. Eighty is the width almost no terminal is under, so it is the
// one that keeps the rule.
func (r Report) String() string { return r.Render(FallbackWidth) }

func (r Report) render(width int, t theme) string {
	if width < indent+minRowWidth {
		width = indent + minRowWidth
	}
	var lines []string
	if r.Title != "" {
		lines = append(lines, pad(t.title(joinDetail(r.Title, r.Subject, " — "))), "")
	}
	for _, s := range r.Sections {
		lines = append(lines, s.render(width, t)...)
	}
	if len(r.Notes) > 0 {
		lines = append(lines, "")
		for _, n := range r.Notes {
			lines = append(lines, note(n, width, t)...)
		}
	}
	if r.Tally != "" {
		lines = append(lines, "", pad(t.tally(r.Tally)))
	}
	return strings.TrimRight(strings.Join(squeeze(lines), "\n"), "\n ")
}

// minRowWidth is the narrowest a row is drawn at: a glyph, a space and enough
// of a target to be worth reading. Below it the terminal is not one.
const minRowWidth = 12

func (s Section) render(width int, t theme) []string {
	// Every section stands off from what came before it, header or not; the
	// blank line a section leaves above itself and the one the block before it
	// left below are reconciled once, in squeeze.
	lines := []string{""}
	if s.Header != "" {
		lines = append(lines, pad(t.header(s.Header)), "")
	}
	nameW := s.NameWidth
	if nameW == 0 {
		nameW = sizeNames(s.Rows)
	}
	// The key/value block comes first: it is what the section is about, and
	// the rows are what it holds.
	if len(s.Pairs) > 0 {
		lines = append(lines, pairs(s.Pairs, t)...)
	}
	if len(s.Rows) > 0 && len(s.Pairs) > 0 {
		lines = append(lines, "")
	}
	for _, row := range s.Rows {
		lines = append(lines, row.render(width, nameW, t)...)
	}
	if s.Body != "" {
		if len(s.Rows) > 0 || len(s.Pairs) > 0 {
			lines = append(lines, "")
		}
		for _, line := range strings.Split(strings.TrimRight(s.Body, "\n"), "\n") {
			lines = append(lines, strings.TrimRight(strings.Repeat(" ", indent+2)+t.body(line), " "))
		}
	}
	return lines
}

// sizeNames is the name column: the longest name in the section, capped, so
// the targets line up under each other without a name being clipped to a
// width chosen somewhere else.
func sizeNames(rows []Row) int {
	w := 0
	for _, row := range rows {
		if n := lipgloss.Width(row.Name); n > w {
			w = n
		}
	}
	return min(w, NameCap)
}

// render lays out one row. The outcome is measured first and the target is
// clipped to what is left, which is the grid's own rule: the outcome is why
// the row is being read, so it is the last thing to go.
func (r Row) render(width, nameW int, t theme) []string {
	name := ""
	if nameW > 0 {
		name = fit(r.Name, nameW) + "  "
	}
	outcome := ""
	if r.Outcome != "" {
		outcome = "  [" + r.Outcome + "]"
	}
	// The glyph's own width rather than a constant: every glyph in the
	// vocabulary is one column today, and a wide one arriving should move the
	// target rather than push the row past the terminal.
	lead := r.State.Glyph() + " "
	target := clip(joinDetail(r.Subject, r.Detail, " · "),
		width-indent-lipgloss.Width(lead)-lipgloss.Width(name)-lipgloss.Width(outcome))

	line := strings.Repeat(" ", indent) + t.glyph(r.State, r.State.Glyph()) + " "
	if name != "" {
		line += t.name(fit(r.Name, nameW)) + "  "
	}
	line += t.target(r.Subject, target)
	if outcome != "" {
		line += "  " + t.outcome(r.State, "["+r.Outcome+"]")
	}
	lines := []string{strings.TrimRight(line, " ")}
	if r.Consequence != "" {
		lines = append(lines, wrapAt(indent+4, r.Consequence, width, t.consequence)...)
	}
	for _, b := range r.Body {
		lines = append(lines, wrapAt(indent+4, b, width, t.body)...)
	}
	for _, f := range r.Fix {
		lines = append(lines, wrapAt(indent+6, f, width, t.body)...)
	}
	return lines
}

// pairs lays out a key/value block aligned on the colon: the one shape a
// listing of settings takes, rather than a tabwriter each time.
func pairs(ps []Pair, t theme) []string {
	w := 0
	for _, p := range ps {
		if n := lipgloss.Width(p.Key); n > w {
			w = n
		}
	}
	lines := make([]string, 0, len(ps))
	for _, p := range ps {
		key := p.Key + ":" + strings.Repeat(" ", w-lipgloss.Width(p.Key)+2)
		lines = append(lines, strings.TrimRight(strings.Repeat(" ", indent)+t.key(key)+t.body(p.Value), " "))
	}
	return lines
}

// note wraps one warning to the width. A diagnostic clipped is a diagnostic
// that cannot be acted on, so notes wrap where rows clip.
func note(n Note, width int, t theme) []string {
	body := wrapAt(indent+2, n.Text, width, t.body)
	if len(body) == 0 {
		return nil
	}
	first := strings.TrimLeft(body[0], " ")
	body[0] = strings.Repeat(" ", indent) + t.glyph(n.State, n.State.Glyph()) + " " + first
	return body
}

// wrapAt wraps text to the width at a left margin, continuation lines under
// the first.
func wrapAt(at int, s string, width int, paint func(string) string) []string {
	room := max(width-at, minRowWidth)
	var lines []string
	for _, line := range strings.Split(ansi.Wordwrap(s, room, ""), "\n") {
		lines = append(lines, strings.TrimRight(strings.Repeat(" ", at)+paint(line), " "))
	}
	return lines
}

// squeeze drops the blank line a leading section would put above the first
// thing in the report, and collapses the pair a section header leaves under
// the title. Blocks each declare the space they want above themselves, which
// is why they have to be reconciled once at the end rather than by every
// block knowing what came before it.
func squeeze(lines []string) []string {
	out := lines[:0]
	for _, line := range lines {
		if line == "" && (len(out) == 0 || out[len(out)-1] == "") {
			continue
		}
		out = append(out, line)
	}
	return out
}

func pad(s string) string { return strings.TrimRight(strings.Repeat(" ", indent)+s, " ") }

// fit pads a name out to the column. A name wider than the column keeps every
// character and pushes its own target right: the column is a floor the targets
// line up on, not a budget a name is cut to. It is what the doctor's report
// has always done, and a clipped name would be the one field a reader cannot
// reconstruct.
func fit(s string, w int) string {
	return s + strings.Repeat(" ", max(w-lipgloss.Width(s), 0))
}

// clip truncates to a display width, ending with … when anything was dropped.
// It is the grid's rule on plain text: display width rather than bytes,
// because a glyph is one column and several bytes.
func clip(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return ansi.Truncate(s, width, "…")
}

// joinDetail joins two halves of a field with a separator, and stands the one
// that exists alone where the other does not.
func joinDetail(head, tail, sep string) string {
	switch {
	case head == "":
		return tail
	case tail == "":
		return head
	}
	return head + sep + tail
}

// Fprint writes a report to a stream: measured at the stream's own width, and
// painted only where the stream can show colour.
func Fprint(w io.Writer, r Report) error {
	_, err := fmt.Fprintln(w, r.render(Width(w), themeFor(w)))
	return err
}

// Fprintln writes bare rows — a confirmation, an empty state, a warning —
// with no title and no tally. It is the same row every listing draws, so a
// one-line answer and a listing are the same shape at different lengths.
func Fprintln(w io.Writer, rows ...Row) error {
	return Fprint(w, Report{Sections: []Section{{Rows: rows}}})
}

// Width is how wide a report written to this stream should be. It measures
// the stream it is about to write to rather than stdout, because a command
// whose listing is redirected and whose warnings are not is writing to two
// terminals of different widths.
func Width(w io.Writer) int {
	f, ok := w.(interface{ Fd() uintptr })
	if !ok {
		return FallbackWidth
	}
	width, _, err := term.GetSize(f.Fd())
	if err != nil || width <= 0 {
		return FallbackWidth
	}
	return width
}

// theme is how each field of a report is painted. Every function is handed
// text that has already been measured and clipped, so the column arithmetic
// never sees an escape code — and the plain theme, whose functions all return
// their argument, therefore produces the bytes the painted one is built from.
type theme struct {
	title       func(string) string
	header      func(string) string
	name        func(string) string
	target      func(subject, clipped string) string
	outcome     func(State, string) string
	glyph       func(State, string) string
	consequence func(string) string
	key         func(string) string
	body        func(string) string
	tally       func(string) string
}

var same = func(s string) string { return s }

// plain is the report with no colour at all: what a pipe, a dumb terminal and
// NO_COLOR get, and what every width calculation is done against.
var plain = theme{
	title:       same,
	header:      same,
	name:        same,
	target:      func(_, clipped string) string { return clipped },
	outcome:     func(_ State, s string) string { return s },
	glyph:       func(_ State, s string) string { return s },
	consequence: same,
	key:         same,
	body:        same,
	tally:       same,
}

// themeFor decides how a stream is written to. Anything at or below ASCII —
// a pipe, TERM=dumb, NO_COLOR — is written plain rather than downsampled,
// because downsampling keeps bold and leaves a reset escape behind, and the
// bytes a script reads should be the bytes a reader reads.
func themeFor(w io.Writer) theme {
	if components.DetectProfile(w, os.Environ()) <= colorprofile.ASCII {
		return plain
	}
	return painted()
}

// painted is the report in the product's own palette. Colour is additive: it
// says again what the glyph and the words have already said.
func painted() theme {
	p := components.Palette
	fg := func(t components.Token) func(string) string {
		s := lipgloss.NewStyle().Foreground(t.Color())
		return func(text string) string { return s.Render(text) }
	}
	dim, body := fg(p.Dim), fg(p.Body)
	tone := map[State]func(string) string{
		Pass:  fg(p.Add),
		Warn:  fg(p.Accent),
		Fail:  fg(p.Del),
		Skip:  dim,
		Run:   fg(p.Spin),
		Queue: dim,
	}
	head := lipgloss.NewStyle().Bold(true).Foreground(p.Info.Color())
	headline := func(text string) string { return head.Render(text) }
	return theme{
		title:  headline,
		header: headline,
		name:   body,
		target: func(subject, clipped string) string {
			if subject != "" && strings.HasPrefix(clipped, subject) {
				return body(subject) + dim(clipped[len(subject):])
			}
			return body(clipped)
		},
		outcome:     func(s State, text string) string { return tone[s](text) },
		glyph:       func(s State, g string) string { return tone[s](g) },
		consequence: fg(p.Accent),
		key:         dim,
		body:        body,
		tally:       dim,
	}
}
