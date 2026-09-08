package components

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// The column grid (docs/interface/principles.md#one-grid, normative). Widths
// are character cells and match tokens/terminal.css in the design-system
// project; nothing in the transcript may invent a width. The target is the
// only field that grows.
const (
	ptrWidth   = 2 // fold state ▾/▸, focus cursor ❯
	railWidth  = 1 // the mutation rail ▎
	glyphWidth = 2 // the kind of act, or the state that overrides it
	verbWidth  = 8 // closed vocabulary, left-aligned, space-padded
	durWidth   = 6 // right-aligned; blank under 0.5s, — when it never ran
	leadWidth  = ptrWidth + railWidth + glyphWidth + verbWidth

	// detailIndent is the detail body (2 row body / 4 detail body / 6 nested
	// detail); tailIndent is a running command's live tail.
	detailIndent = 4
	tailIndent   = 2

	// minTargetWidth is the narrowest the target is squeezed to before the
	// row starts giving up fields of its own. Twelve columns is a clipped
	// file name — `…proval.go` and a space either side — which is the least
	// that still says which act the row is about.
	minTargetWidth = 12
)

// NoDuration is the duration field for a call that never ran — queued or
// denied.
const NoDuration = "—"

// The closed outcome vocabulary
// (docs/interface/principles.md#closed-vocabularies). Counts (`218 lines`, `3
// matches`, `+12 −4 · 2 hunks`) are the outcome when there is nothing else to
// say and live in ActivityRow.Counts; everything else is one of these.
const (
	OutcomeOK       = "ok"
	OutcomeRunning  = "running…"
	OutcomeQueued   = "queued"
	OutcomeChecking = "checking"
	OutcomeDenied   = "denied"
	// OutcomeBlocked is a rule's no, and the reason it is a second word
	// rather than OutcomeDenied with a different decider: "you said no" and
	// "a rule said no" are different facts, and the reader's next act is
	// different for each — change your mind, or change your configuration
	// (docs/interface/principles.md#two-denials-are-not-one-denial). The
	// rule that said it goes in the account field beside this, so a row
	// narrow enough to drop the account still says which of the two it was.
	OutcomeBlocked  = "blocked"
	OutcomeApproved = "approved"
	// OutcomeAnswered is a question the model asked and the person answered
	// (docs/capabilities/coding-agent.md#the-model-can-ask). It is an
	// outcome and not a count because nothing was found and nothing ran:
	// what happened is that somebody decided.
	OutcomeAnswered = "answered"
	// OutcomeSkipped is an act nobody was put and nobody decided. Two
	// things reach it: a question the turn's spent budget answered instead
	// of the reader (docs/capabilities/coding-agent.md#the-model-can-ask),
	// and a call the queue refused before it could reach a card at all. One
	// word for both, because the row's next field is what tells them apart —
	// what answered the question, or why the call was never put.
	OutcomeSkipped     = "skipped"
	OutcomeAutoAllowed = "auto-allowed"
	// OutcomeAmended is a command the reader rewrote at the card before it
	// ran (docs/capabilities/approvals-and-safety.md#an-amended-command-is-a-new-command).
	// The row's target is the line that ran; this is how it says the line
	// was not the one the call carried, which nothing else on the row could.
	OutcomeAmended = "amended"
	// OutcomeLocal marks a command row whose output stayed out of the
	// conversation — the reader saw it, the model never did.
	OutcomeLocal = "local"
)

// OutcomeExit is the terminal outcome of a shell command.
func OutcomeExit(code int) string { return "exit " + strconv.Itoa(code) }

// OutcomeBy names the decider behind a decision outcome — `denied · you`,
// `approved · you`, `auto-allowed · read-only`. Colour never carries the
// distinction alone (invariant 1), so the word is always there.
func OutcomeBy(outcome, decider string) string {
	if decider == "" {
		return outcome
	}
	return outcome + " · " + decider
}

// ActivityKind selects an activity row's glyph and whether it carries the
// mutation rail. Five different reads share one glyph on purpose: the
// verb, not the colour, says which read it was, and ⚙ never mutates.
type ActivityKind int

const (
	ActivityTool     ActivityKind = iota // ⚙ read-only tool
	ActivityCommand                      // $ shell command
	ActivityEdit                         // ✎ edit, write, patch, memory
	ActivitySubagent                     // ◇ sub-agent
	// ActivityRemote is a call to an MCP server the user has not marked
	// read-only: ⇄, with the mutation rail, because shhh cannot know whether
	// the far end wrote something and so assumes it did — the rule commands
	// already follow. A read-only server's call is ⚙ like any other read
	// (docs/capabilities/mcp.md#a-call-is-a-command-unless-you-said-otherwise).
	ActivityRemote
	// ActivityThink is the model's own reasoning: ✻, and the only kind that
	// touched nothing at all. It is drawn dim rather than in the accent every
	// other kind glyph carries, because weight tracks risk and this row is the
	// bottom of that order — it read nothing, wrote nothing and ran nothing.
	// See docs/interface/principles.md#weight-tracks-risk.
	ActivityThink
	// ActivityReport is a published report page: ⛁, a stack with a page on
	// top, because the row's outcome is a link into a store. No mutation
	// rail — the store is shhh's own state, not the workspace
	// (docs/capabilities/reports.md#a-report-outlives-its-session).
	ActivityReport
	// ActivitySummary is a reading of the session by the summariser: ≡, three
	// stacked lines for the digest it is. Like ActivityThink it is drawn dim
	// and carries no rail, and for the same reason — it read nothing of the
	// workspace, wrote nothing and ran nothing. It is the second act that is
	// not a tool, and it gets a glyph of its own rather than borrowing ✻
	// because the two say different things: ✻ is the session's own model
	// working, ≡ is another model reading the session
	// (docs/interface/surfaces.md#the-session-summary).
	ActivitySummary
)

// ActivityState is the row's state. It overrides the kind glyph — but only
// for the states that are worth a glyph of their own: a row that simply
// finished keeps the kind glyph, so `$`, `⚙` and `✎` stay visible on the rows
// that succeeded: the kind glyphs win over the outcome table's `ok → ✓` row.
type ActivityState int

const (
	ActivityDone     ActivityState = iota // finished; the kind glyph stands
	ActivityQueued                        // · accepted, not started
	ActivityRunning                       // ▸ in flight
	ActivityChecking                      // ✦ the classifier is deciding
	ActivityFailed                        // ✗ the call failed
	ActivityDenied                        // ⊘ you said no, or a rule did
)

// ActivityRow is one line of activity on the column grid: pointer, mutation
// rail, glyph, verb, target, outcome, duration. It is a passive transcript
// renderer — reading mode (docs/interface/surfaces.md#reading-mode) owns the
// expansion keys, so the row has no Update.
type ActivityRow struct {
	Kind  ActivityKind
	State ActivityState
	// Verb is the closed verb vocabulary, padded or clipped to 8 columns.
	Verb string
	// Target is the path, command, query or agent name — the only field that
	// grows, and the only one that clips.
	Target string
	// Scope is the tail of Target that says where the act was put rather
	// than what it was about: a search's `./internal` behind its pattern. It
	// is drawn Dim behind the subject so the column reads as one subject and
	// its place, and it is a field rather than a second string because the
	// grid clips Target as one run
	// (docs/interface/principles.md#one-grid). Empty on every row whose
	// target is its subject whole.
	Scope string
	// Outcome and Counts render as one right-aligned field, joined by ` · `. It
	// is the reason to read the row, so it never gives way to the target; the
	// pane is the one thing it does give way to
	// (docs/interface/principles.md#one-grid).
	Outcome string
	Counts  string
	// Allowed is the account of a gated call that ran without the reader
	// being asked — `auto-allowed · auto mode`, `auto-allowed · classifier
	// 2.1s`. It renders inside the outcome field, after what the call did and
	// before what it counted, because the row is the only place the decision
	// is stated: an act and the approval of it are one row, not two. Empty on
	// a call the reader answered themselves and on every call that was never
	// gated.
	//
	// One decision the reader did make is stated here too: `amended · you`,
	// the line they wrote in place of the one the call carried. It is the
	// same fact in the same place — how this act came to be the act it is —
	// and putting it anywhere else would have made two fields out of one
	// question.
	Allowed string
	// Duration is the 6-column right-aligned field. Callers omit it under 0.5s
	// and set NoDuration for a call that never ran.
	Duration string
	// Detail is the bounded detail body shown when Expanded; failed rows
	// auto-expand with error lines first.
	Detail    []string
	MaxDetail int
	// Tail is a running command's last output line, shown live beneath the row.
	Tail string
	// Keys are the keys the row offers (`/mode why`), rendered in info (12)
	// after the outcome — every key the interface offers is info, so a key in
	// any other colour is not an offer.
	Keys string
	// ByRule colours a denial del (9) rather than dim (241): `⊘ denied · you`
	// is a preference, `⊘ denied · auto` is a rule.
	ByRule bool
	// Expanded shows the detail body; Selected draws the focus-mode pointer.
	Expanded bool
	Selected bool
	// Spin says the host is ticking, and Frame is the frame it is on — the same
	// frame the status line and the frame header are drawing, from the
	// one tick source. A running row is `▸` in a still image and the spinner
	// while it animates, which is what the outcome table says; a host that does
	// not tick leaves Spin false rather than freezing a braille glyph on screen,
	// because a stopped spinner reads as a hang.
	Spin  bool
	Frame int
}

// Failed reports whether the row broke, for callers deciding what to
// auto-expand.
func (r ActivityRow) Failed() bool { return r.State == ActivityFailed }

// mutated reports whether the row carries the mutation rail
// (docs/interface/principles.md#weight-tracks-risk): it wrote to disk, ran a
// command, or was denied. Read-only rows leave the gutter blank, and a
// sub-agent's mirrored row is a status report, not an act — but a row that
// failed keeps a rail whatever it was, so scrolling back finds the break
// without hunting for it.
func (r ActivityRow) mutated() bool {
	switch r.State {
	case ActivityFailed, ActivityDenied:
		return true
	}
	return r.Kind == ActivityCommand || r.Kind == ActivityEdit || r.Kind == ActivityRemote
}

// railCell renders gutter column 3: accent for a mutation, del for a break.
func (r ActivityRow) railCell() string {
	if !r.mutated() {
		return strings.Repeat(" ", railWidth)
	}
	if r.State == ActivityFailed {
		return sty.Del.Render("▎")
	}
	return sty.Accent.Render("▎")
}

// pointer renders gutter columns 1–2: the focus cursor today, fold state once
// steps land.
func (r ActivityRow) pointer() string {
	if r.Selected {
		// The pointer is a glyph in its own column, not part of the highlight
		// behind the row.
		return sty.FocusPointer.Render("❯") + " "
	}
	return strings.Repeat(" ", ptrWidth)
}

// runningGlyph is `▸` where the row is a still image and the host's current
// spinner frame where it is animating. The frame is passed in rather than
// counted here, so this row, the turn status line and the frame header show
// the same one.
func (r ActivityRow) runningGlyph() string {
	if !r.Spin {
		return "▸"
	}
	return Spinner{Frame: r.Frame}.Glyph()
}

// glyph renders the 2-column glyph field: the state where it overrides, the
// kind of act otherwise.
func (r ActivityRow) glyph() string {
	var g string
	switch r.State {
	case ActivityQueued:
		g = sty.Dim.Render("·")
	case ActivityRunning:
		g = sty.SpinText.Render(r.runningGlyph())
	case ActivityChecking:
		g = sty.SpinText.Render("✦")
	case ActivityFailed:
		g = sty.Err.Render("✗")
	case ActivityDenied:
		if r.ByRule {
			g = sty.Del.Render("⊘")
		} else {
			g = sty.Dim.Render("⊘")
		}
	default:
		switch r.Kind {
		case ActivityCommand:
			g = sty.Accent.Render("$")
		case ActivityEdit:
			g = sty.Accent.Render("✎")
		case ActivitySubagent:
			g = sty.Info.Render("◇")
		case ActivityRemote:
			g = sty.Accent.Render("⇄")
		case ActivityThink:
			g = sty.Dim.Render("✻")
		case ActivityReport:
			g = sty.Accent.Render("⛁")
		case ActivitySummary:
			g = sty.Dim.Render("≡")
		default:
			g = sty.Accent.Render("⚙")
		}
	}
	return g + " "
}

// verbField pads the verb to its 8 columns; an over-long verb clips, which is
// the signal that the verb table is stale. Recovery rows share it, which is
// what puts `model` in the same column as `read`.
//
// The verb is body text, because it is half of what the row is about: the
// verb says which act and the target says what it was done to, and a column
// left in the terminal's default foreground was the one field on the grid
// with no token at all (docs/interface/principles.md#one-grid). The padding
// stays unpainted — it is spacing, not text.
func verbField(verb string) string { return verbFieldIn(verb, sty.Body) }

// verbFieldIn is verbField in the tone a row's state asks for: bright while
// it is happening, dim before it started and after a refusal that means it
// never will.
func verbFieldIn(verb string, style lipgloss.Style) string {
	v := Clip(verb, verbWidth)
	pad := strings.Repeat(" ", verbWidth-lipgloss.Width(v))
	if v == "" {
		return pad
	}
	return style.Render(v) + pad
}

// subjectStyle is the tone the verb and the target share — the row's subject,
// which is one statement in two fields and so is never painted in two tones.
// Body is the resting state; the exceptions are the states where the row is
// making a claim about itself: it is happening now, or it never happened.
func (r ActivityRow) subjectStyle() lipgloss.Style {
	switch {
	case r.State == ActivityRunning:
		return sty.Bright
	case r.State == ActivityQueued:
		return sty.Dim
	case r.State == ActivityDenied && !r.ByRule:
		// Your own refusal is a preference, and a preference is the quietest
		// thing on the grid. A rule's is not: that row keeps body text and
		// says `blocked` in del beside it.
		return sty.Dim
	}
	return sty.Body
}

// paintTarget leads the growing field with the subject in the state's tone
// and dims the place behind it, the way a recovery row dims the class behind
// a model name. A field clipped past the scope goes wholly to the subject,
// which is what is left of it.
func (r ActivityRow) paintTarget(s string) string {
	style := r.subjectStyle()
	if r.Scope != "" {
		if head, ok := strings.CutSuffix(s, " "+r.Scope); ok {
			return style.Render(head) + sty.Dim.Render(" "+r.Scope)
		}
	}
	return style.Render(s)
}

// outcomeStyle is the token the outcome word takes: the state's own, so a
// reader dropping down the outcome column tells finished from running from
// failed without reading a word. The word is always there as well
// (invariant 1) — the colour is what makes the column scannable, never what
// carries the fact.
func (r ActivityRow) outcomeStyle() lipgloss.Style {
	switch r.State {
	case ActivityRunning, ActivityChecking:
		return sty.SpinText
	case ActivityFailed:
		return sty.Del
	case ActivityQueued:
		return sty.Dim
	case ActivityDenied:
		if r.ByRule {
			return sty.Del
		}
		return sty.Dim
	}
	// The two kinds that are not acts have no act to have succeeded. A
	// reading of the session states a verdict in this column — `⚠ off
	// target`, `· target unclear` — and add would paint the bad news as
	// good; thinking states no outcome at all. Both sit at the bottom of the
	// weight order, which is where their kind glyphs already are
	// (docs/interface/principles.md#weight-tracks-risk), and the glyph in
	// front of the verdict is what tells the readings apart on a terminal
	// with no colour at all.
	if r.Kind == ActivityThink || r.Kind == ActivitySummary {
		return sty.Dim
	}
	return sty.Add
}

// outcomeField joins outcome, account, counts and keys into the one
// right-aligned field. Four tones, one per job: what came of the call in the
// state's token, what allowed it dim, what it counted dimmer, and the keys it
// offers in info.
func (r ActivityRow) outcomeField() string {
	var parts []string
	if r.Outcome != "" {
		parts = append(parts, r.outcomeStyle().Render(r.Outcome))
	}
	if r.Allowed != "" {
		parts = append(parts, sty.Dim.Render(r.Allowed))
	}
	if r.Counts != "" {
		parts = append(parts, paintCounts(r.Counts, sty.Dimmer))
	}
	if r.Keys != "" {
		parts = append(parts, sty.Info.Render(r.Keys))
	}
	return strings.Join(parts, sty.Dim.Render(" · "))
}

// paintCounts paints a ` · `-joined count label, giving an edit's line counts
// the two tokens they mean everywhere else: `+12` is add and `−4` is del on a
// row, on a collapsed diff and at the turn's close, so one reader learns one
// pair of colours. Everything else in the label keeps the tone the field is
// otherwise in.
func paintCounts(label string, rest lipgloss.Style) string {
	if label == "" {
		return ""
	}
	parts := strings.Split(label, " · ")
	for i, p := range parts {
		if painted, ok := paintLineCounts(p); ok {
			parts[i] = painted
			continue
		}
		parts[i] = rest.Render(p)
	}
	return strings.Join(parts, rest.Render(" · "))
}

// paintLineCounts paints `+12 −4`, or either half alone, and reports whether
// the segment was one. Anything else is not a line count and is left to the
// caller: the tokens are for what an edit added and removed, not for every
// number that happens to carry a sign.
func paintLineCounts(seg string) (string, bool) {
	fields := strings.Fields(seg)
	if len(fields) == 0 || len(fields) > 2 || seg != strings.Join(fields, " ") {
		return "", false
	}
	painted := make([]string, 0, len(fields))
	for _, f := range fields {
		switch {
		case signedCount(f, "+"):
			painted = append(painted, sty.Add.Render(f))
		case signedCount(f, "−"):
			painted = append(painted, sty.Del.Render(f))
		default:
			return "", false
		}
	}
	return strings.Join(painted, " "), true
}

// signedCount reports whether s is the given sign followed by digits and
// nothing else.
func signedCount(s, sign string) bool {
	rest, ok := strings.CutPrefix(s, sign)
	if !ok || rest == "" {
		return false
	}
	return strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' }) < 0
}

// fittedOutcome is the outcome field as much of it as this width can carry.
// The account of who allowed the call is the one part the row gives up, and
// it gives it up rather than squeeze the target past minTargetWidth: what an
// act was done to is why the row is read, while who allowed it is also on
// the frame and in the mode. Nothing else in the field is ever dropped —
// what the act did and what it counted have nowhere else to be said.
func (r ActivityRow) fittedOutcome(width int) string {
	field := r.outcomeField()
	if r.Allowed == "" {
		return field
	}
	if width-leadWidth-durWidth-lipgloss.Width(field)-2 >= minTargetWidth {
		return field
	}
	bare := r
	bare.Allowed = ""
	return bare.outcomeField()
}

// durationField right-aligns the duration in its 6 columns. The field is
// reserved even when blank so outcomes line up down the transcript; the
// trailing blank is trimmed off the rendered line.
//
// Dim rather than dimmer, which is what the step outline's own duration has
// always been: the column is scanned as one column, and one column is one
// grey. Dimmer is for content — a tool's output, a count of what it found —
// and how long something took is chrome about the row, not what the row
// found.
func durationField(d string) string {
	d = Clip(d, durWidth)
	pad := durWidth - lipgloss.Width(d)
	if pad < 0 {
		pad = 0
	}
	if d == "" {
		return strings.Repeat(" ", durWidth)
	}
	return strings.Repeat(" ", pad) + sty.Dim.Render(d)
}

// gridLine assembles one line on the grid: a lead already padded to
// leadWidth, then the target, the outcome field and the duration. The target
// grows into whatever the fixed fields leave and clips with … so the outcome
// does not have to — it is the reason to read the line, and it gives way to
// nothing but the pane. Both the activity row and the folded group row are
// this shape, which is why they line up.
func gridLine(lead, target, outcome, duration string, width int) string {
	return gridLineWith(lead, target, func(s string) string { return sty.Dim.Render(s) }, outcome, duration, width)
}

// gridLineWith is gridLine with the target's painting under the caller's
// control. A recovery row leads its target with the model in body text and
// dims only the class behind it, which one style over the whole field cannot
// express; paint is handed the already-clipped text so the column arithmetic
// stays in one place.
func gridLineWith(lead, target string, paint func(string) string, outcome, duration string, width int) string {
	outW := lipgloss.Width(outcome)
	sep := 0
	if outW > 0 {
		sep = 2
	}
	// The outcome never gives way to the target, and this is the one thing
	// it does give way to: the pane itself. Where the lead, the outcome and
	// the duration are already wider than the terminal there is nothing left
	// to spend, and a field that kept its whole length would run past the
	// right edge and wrap into the row below — which costs the reader the
	// column the grid exists to give them
	// (docs/interface/principles.md#one-grid). So the tail clips with …, and
	// the head of the field — the outcome word itself — is what survives.
	if room := width - leadWidth - durWidth - sep; outW > room {
		outcome = Clip(outcome, room)
		outW = lipgloss.Width(outcome)
		if outW == 0 {
			sep = 0
		}
	}
	target = Clip(target, width-leadWidth-durWidth-outW-sep)
	// The gap is at least the separator, and the separator is never less
	// than nothing — a pane narrower than the fixed fields leaves this
	// arithmetic negative, and a negative run of spaces is a panic rather
	// than a short line.
	pad := max(width-leadWidth-lipgloss.Width(target)-outW-durWidth, sep)
	line := strings.TrimRight(lead+paint(target)+strings.Repeat(" ", pad)+outcome+durationField(duration), " ")
	// The last word on the width, over fields that have already given up what
	// they can. Below about twenty columns the fixed fields alone are wider
	// than the pane and there is nothing left to take from them, and a row
	// that overflowed there would be broken by the terminal rather than by
	// the grid (docs/interface/principles.md#one-grid). Above it this is one
	// measurement and no change.
	return Clip(line, width)
}

// View renders the row (plus tail and detail lines) at the given width.
func (r ActivityRow) View(width int) string {
	lead := r.pointer() + r.railCell() + r.glyph() + verbFieldIn(r.Verb, r.subjectStyle())
	first := gridLineWith(lead, r.Target, r.paintTarget, r.fittedOutcome(width), r.Duration, width)
	if r.Selected {
		// The reading cursor lights the row it is on: the background runs the row's
		// width and its words go bright, while the rail and the glyph keep the
		// colours that say what the row did. The pointer stays outside it.
		first = LitRow(first, ptrWidth, width)
	}
	lines := []string{first}

	if r.State == ActivityRunning && r.Tail != "" {
		lines = append(lines, indented(r.Tail, tailIndent, width))
	}
	// Failed rows auto-expand to their bounded detail; successful rows stay
	// collapsed until focus mode expands them.
	if (r.Expanded || r.State == ActivityFailed) && len(r.Detail) > 0 {
		detail := r.Detail
		dropped := 0
		if r.MaxDetail > 0 && len(detail) > r.MaxDetail {
			detail = detail[:r.MaxDetail]
			dropped = len(r.Detail) - len(detail)
		}
		for _, d := range detail {
			lines = append(lines, indented(d, detailIndent, width))
		}
		if dropped > 0 {
			// The bound is a fold, so it counts what it swallowed
			// (docs/interface/principles.md#fold-never-hide); reading mode's
			// [enter] on the row is how the rest is reached.
			lines = append(lines, strings.Repeat(" ", detailIndent)+
				sty.Dim.Render(Clip(countedTail(dropped), max(width-detailIndent, 1))))
		}
	}
	return strings.Join(lines, "\n")
}

// countedTail is the row under a bounded body: what the cap swallowed, in
// the units the body is made of. (moreLines in attachmentview.go is the
// preview's own foot; the transcript's tail leads with the ellipsis the
// folded rows already use.)
func countedTail(n int) string {
	if n == 1 {
		return "… 1 more line"
	}
	return "… " + strconv.Itoa(n) + " more lines"
}

// indented renders one detail line at the given indent in dimmer (245).
// Detail bodies indent, they do not re-grid.
//
// It is also the one door foreign bytes come through — a command's output, a
// live tail, a provider's error body — so the line is re-painted into the
// palette before it is measured. A line shhh wrote itself has nothing to
// re-paint and comes back unchanged; the clip runs afterwards because a
// cursor sequence the terminal would never have shown still counts against
// ansi.Truncate's width.
func indented(s string, indent, width int) string {
	pad, inner := strings.Repeat(" ", indent), max(width-indent, 1)
	if painted, ok := repaint(s, Palette.Dimmer); ok {
		// A re-painted line already carries the ground, run by run; a second
		// wrapper around it would only spend bytes the first reset throws away.
		return pad + Clip(painted, inner)
	}
	return pad + sty.Dimmer.Render(Clip(s, inner))
}

// GroupExpandKey is what a folded group row says opens it. It is drawn in the
// hint treatment rather than in info, the way the collapsed diff row's has
// always been: enter belongs to the draft below until reading mode takes the
// keyboard, so on a transcript row this is a label for what the row does
// under the cursor, not an offer standing open
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
var GroupExpandKey = keys.Bracket(keys.Reading.Expand) + " " + keys.Words(keys.Reading.Expand)

// ActivityGroup is the folded group row: the one line a run of consecutive
// read-only calls collapses into at normal verbosity. It folds, it never
// hides (invariant 4) — the label states what it swallowed and the duration
// states what that cost, so nothing is dropped to save space.
//
// It sits on the same grid as a row, shifted by one field: the fold state
// takes the glyph column and the kind glyph ⚙ takes the verb column, so the
// line reads as chrome about rows rather than as a call of its own.
type ActivityGroup struct {
	// Label counts the swallowed rows by kind, e.g. "6 reads · 2 searches".
	Label string
	// Duration is the summed 6-column field, blank under 0.5s like a row's.
	Duration string
}

// View renders the group row at the given width.
func (g ActivityGroup) View(width int) string {
	lead := strings.Repeat(" ", ptrWidth+railWidth) +
		sty.Dim.Render("▸") + " " +
		sty.Dim.Render("⚙") + strings.Repeat(" ", verbWidth-1)
	return gridLine(lead, g.Label, sty.Hint.Render(GroupExpandKey), g.Duration, width)
}

// ActivityNotice is a line the session wrote about itself rather than an act
// it took — a conversation reopened, a new one started. It sits on the grid
// beside the acts: the verb it opens with in the verb column, what it is
// about in the growing target, and what came of it right-aligned where every
// other outcome is (docs/interface/principles.md#one-grid). Before this, the
// session's own lines were sentences at column 0 that ran past the pane and
// were broken by the terminal wherever they happened to run out.
//
// The glyph column is blank, the way the folded group row's fields are
// shifted: the glyph says which kind of act a row was, and this is not one.
// It is dim throughout for the same reason — the session's bookkeeping is
// chrome about the transcript rather than something that touched the
// machine, which is the bottom of the weight order
// (docs/interface/principles.md#weight-tracks-risk).
//
// A notice with no verb from a closed vocabulary to open with is prose, and
// its host wraps it to the pane rather than laying it on the grid.
type ActivityNotice struct {
	// Verb is the closed verb vocabulary, padded or clipped to 8 columns.
	Verb string
	// Subject is the growing field: what the notice is about.
	Subject string
	// Outcome is the right-aligned field: what came of it.
	Outcome string
	// Detail is the body a reader opens the notice for, already wrapped by
	// the host — a notice's body is a sentence, and a sentence clipped is
	// worse than one that costs two lines.
	Detail   []string
	Expanded bool
}

// View renders the notice at the given width.
func (n ActivityNotice) View(width int) string {
	lead := strings.Repeat(" ", ptrWidth+railWidth+glyphWidth) + verbFieldIn(n.Verb, sty.Dim)
	// Rendered only when there is something to render: an empty field put
	// through a style is escape bytes with no width, which the line's own
	// trailing trim then cannot see to remove.
	outcome := ""
	if n.Outcome != "" {
		outcome = sty.Dim.Render(n.Outcome)
	}
	lines := []string{gridLineWith(lead, n.Subject, func(s string) string {
		if s == "" {
			return ""
		}
		return sty.Dim.Render(s)
	}, outcome, "", width)}
	if n.Expanded {
		for _, d := range n.Detail {
			lines = append(lines, indented(d, detailIndent, width))
		}
	}
	return strings.Join(lines, "\n")
}

// The step outline draws its headers on this same grid but lives in
// internal/ui/chat, because it groups history rather than rendering a widget
// (see AGENTS.md). These are the fields it needs; the widths stay declared
// here so a grid change remains a one-line change.
const (
	// GridPointerWidth is the fold-state/focus column.
	GridPointerWidth = ptrWidth
	// GridVerbColumn is where a row's verb — and a step's title — starts.
	GridVerbColumn = ptrWidth + railWidth + glyphWidth
	// GridDurationWidth is the right-aligned duration field.
	GridDurationWidth = durWidth
	// GridDetailIndent is where a detail body starts. A caller that has to
	// wrap prose before handing it over as Detail lines needs the same number
	// this file indents them by; measuring it twice is how they drift.
	GridDetailIndent = detailIndent
)
