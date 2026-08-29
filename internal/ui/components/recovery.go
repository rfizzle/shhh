package components

// Recovery surfaces (S-106, DESIGN-TUI.md §17). A provider failure used to be
// a Go error string on a line of its own, which meant the worst-read line in
// the session was the one you most needed to read. It is an activity row now:
// the same pointer, glyph, verb, target, outcome and duration fields as the
// call that failed, because a failure is part of the turn rather than an
// interruption of it.
//
// Only one thing here is a card, and only because the session cannot continue
// without an answer to it: there is no provider to talk to at all.
//
// These are passive renderers plus one interactive card. What the offered
// keys *do* belongs to the host — internal/ui/chat for a session, internal/ui
// for the one-shot — because the same failure offers different ways out
// depending on what is on screen.

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// KeyOffer is one bracketed key and the words for what it does. Every key the
// interface offers is info (§10a), so a key in any other colour is not an
// offer.
type KeyOffer struct{ Key, Label string }

// TurnKey is the turn close's name for the same thing (S-098); the two were
// separate structs until recovery rows needed the third.
type TurnKey = KeyOffer

// RecoveryState is how the row ended, and which glyph says so. `⚠` is a
// recoverable stall — it will resume, or you can steer it; `✗` is a call that
// failed; `⊘` is a stop you asked for. The distinction is the whole reason
// all three exist (§17a).
type RecoveryState int

const (
	// RecoveryBroken — ✗ del (9): the call failed and will not resume itself.
	RecoveryBroken RecoveryState = iota
	// RecoveryStalled — ⚠ accent (214): recoverable, retryable, or waiting.
	RecoveryStalled
	// RecoveryStopped — ⊘ dim (241): you cancelled it.
	RecoveryStopped
)

// recoveryVerbs are the three §6c verbs that are not tool calls. They occupy
// the same 8-column field, which is what makes a failure read as part of the
// turn.
const (
	VerbModel  = "model"
	VerbStream = "stream"
	VerbRounds = "rounds"
)

// RecoveryRow is one failure on the §6a grid, plus the bounded detail body
// and the one line of keys underneath it.
type RecoveryRow struct {
	State RecoveryState
	// Verb is `model`, `stream` or `rounds` — never a tool verb.
	Verb string
	// Subject leads the target field in body text: the model that failed, or
	// what the stall was. It is the only part of the target that is not dim.
	Subject string
	// Qualifier continues the target field in dim, joined by ` · `: the class
	// and status, the tier that was hit, the tokens that were kept.
	Qualifier string
	// Outcome is the right-aligned field, and never clips — it is the reason
	// to read the row.
	Outcome string
	// Duration is the 6-column field; blank under 0.5s, NoDuration when the
	// call never ran.
	Duration string
	// Detail is the bounded detail body at indent 4: the provider's own
	// words. An unclassified failure is why it exists.
	Detail []string
	// MaxDetail bounds it; zero means the caller already bounded it.
	MaxDetail int
	// Keys are the ways out, in the order they should be tried.
	Keys []KeyOffer
	// Note trails the keys in dim, and is where the row says what survived —
	// `nothing in the turn was lost`. A failure that does not say what it
	// cost is a failure you have to go and check.
	Note string
	// KeysWaiting says the row does not hold the keyboard, so its keys render
	// grey (§7c): `r` and `c` are letters while the draft has it, and "run
	// the tests again" is exactly what gets typed after a failure
	// (invariant 5). A host that claims nothing keeps the live treatment the
	// row always had — the one-shot prints these rows with no draft to
	// protect, and states its way out as a command anyway (§17a).
	KeysWaiting bool
	// Handover is the key that hands the keyboard over, unbracketed, offered
	// live beside the waiting keys. Empty where there is no such key to
	// press from this screen.
	Handover string
}

// glyph is the state's glyph, in the state's colour.
func (r RecoveryRow) glyph() string {
	switch r.State {
	case RecoveryStalled:
		return sty.Accent.Render("⚠") + " "
	case RecoveryStopped:
		return sty.Dim.Render("⊘") + " "
	}
	return sty.Err.Render("✗") + " "
}

// target assembles the growing field: the subject in body text, the qualifier
// dim behind it.
func (r RecoveryRow) target() string {
	if r.Qualifier == "" {
		return r.Subject
	}
	if r.Subject == "" {
		return r.Qualifier
	}
	return r.Subject + " · " + r.Qualifier
}

// paintTarget leads the field with the subject in body text and dims the
// class behind it. A field too narrow to hold the subject whole goes dim
// entirely rather than emphasising half a model name.
func (r RecoveryRow) paintTarget(s string) string {
	if r.Subject != "" && strings.HasPrefix(s, r.Subject) {
		return sty.Body.Render(r.Subject) + sty.Dim.Render(strings.TrimPrefix(s, r.Subject))
	}
	return sty.Dim.Render(s)
}

// outcomeField colours the right-aligned field by state: a stall is accent, a
// break is del, a stop is dim. The word is always there, so the colour is
// reinforcement rather than the message (invariant 1).
func (r RecoveryRow) outcomeField() string {
	if r.Outcome == "" {
		return ""
	}
	switch r.State {
	case RecoveryStalled:
		return sty.Accent.Render(r.Outcome)
	case RecoveryStopped:
		return sty.Dim.Render(r.Outcome)
	}
	return sty.Del.Render(r.Outcome)
}

// View renders the row, its detail body and its offered keys at the given
// width. The row is always one line; everything under it indents rather than
// re-gridding (§6a).
func (r RecoveryRow) View(width int) string {
	// The pointer and mutation-rail columns stay blank: a failed request
	// changed nothing, and focus mode supplies its own cursor gutter.
	lead := strings.Repeat(" ", ptrWidth+railWidth) + r.glyph() + verbField(r.Verb)
	lines := []string{gridLineWith(lead, r.target(), r.paintTarget, r.outcomeField(), r.Duration, width)}

	detail := r.Detail
	if r.MaxDetail > 0 && len(detail) > r.MaxDetail {
		detail = detail[:r.MaxDetail]
	}
	for _, d := range detail {
		lines = append(lines, indented(d, detailIndent, width))
	}
	for _, keys := range r.keyLines(max(width-detailIndent, 1)) {
		lines = append(lines, detailLine(keys, width))
	}
	return strings.Join(lines, "\n")
}

// keyLines are the offers and the note under the row. They are one line
// wherever there is room for one, and wrap onto more where there is not: a
// narrow terminal gets more rows, never fewer answers, which is the rule the
// review surface and the pressure card already follow. The note goes last,
// because it annotates the offers rather than competing with them for the
// width.
func (r RecoveryRow) keyLines(width int) []string {
	note := ""
	if r.Note != "" {
		note = sty.Dim.Render(r.Note)
	}
	if len(r.Keys) == 0 {
		if note == "" {
			return nil
		}
		return []string{note}
	}
	if one := r.keyLine(); lipgloss.Width(one) <= width {
		return []string{one}
	}
	rows := wrapOffersIn(r.Keys, width, !r.KeysWaiting)
	// The key that hands the keyboard over keeps a line of its own rather
	// than wrapping in among the keys it makes live: it is the only offer on
	// a row that does not hold the keyboard, and it reads as one (§7c).
	if r.KeysWaiting && r.Handover != "" {
		rows = append(rows, handoverOffer(r.Handover, handoverWords))
	}
	if note != "" {
		rows = append(rows, note)
	}
	return rows
}

// detailLine puts one already-styled line in the detail body's column and
// clips it to the surface. Every line under a recovery row sits here, so the
// row and whatever renders live beneath it stay in one column.
func detailLine(s string, width int) string {
	return strings.Repeat(" ", detailIndent) + clip(s, max(width-detailIndent, 1))
}

// RetryWait is the live block a stalled failure row grows while a bounded
// retry waits out the provider's own countdown (S-107, §17a). The row above
// it is history and does not move; this is the part that drains.
//
// The shape is guidelines/meters-progress and ui_kits/cockpit/Edges.html in
// the shhh Design System project: a draining accent meter with its seconds
// beside it, then the offers, both in the detail body's column.
//
// It is a passive renderer like the row: what the offered keys do, how long
// the wait is and whether there is a cheaper model to offer belong to the
// host.
type RetryWait struct {
	// Pct is how much of the wait is left, 0–100. The meter drains right to
	// left as it runs down (§10c).
	Pct int
	// Text states the wait beside the bar — `retry in 12s`. A bar is a shape;
	// the number is the measurement, and the two turn colour together.
	Text string
	// Note trails the meter in dim: the attempt and the bound. It is what
	// makes "retries are bounded" a thing you can see rather than a promise.
	Note string
	// Keys are the offers while it waits. Esc belongs among them: a wait you
	// cannot stop is a hang with a progress bar.
	Keys []KeyOffer
}

// View renders the draining meter and the offers, in the detail body's column
// under the row that stalled.
func (w RetryWait) View(width int) string {
	meter := Meter{Pct: w.Pct, Cells: MeterCellsCountdown, Tone: MeterCountdown, Text: w.Text}
	head := meter.View()
	if w.Note != "" {
		head += sty.Dim.Render(" · " + w.Note)
	}
	lines := []string{detailLine(head, width)}
	if keys := keyOffers(w.Keys); keys != "" {
		lines = append(lines, detailLine(keys, width))
	}
	return strings.Join(lines, "\n")
}

// keyLine renders the offers and the note as one line: the keys in info, the
// words for them and the note in dim — or, where the row does not hold the
// keyboard, the keys grey beside the one key that hands it over (§7c).
func (r RecoveryRow) keyLine() string {
	var parts []string
	if offers := keyRun(r.Keys, r.KeysWaiting, r.Handover); offers != "" {
		parts = append(parts, offers)
	}
	if r.Note != "" {
		parts = append(parts, sty.Dim.Render(r.Note))
	}
	return strings.Join(parts, sty.Dim.Render(" · "))
}

// Keys returns just the keystrokes the row offers, for a host deciding
// whether a key press belongs to it.
func (r RecoveryRow) KeyStrokes() []string {
	out := make([]string, 0, len(r.Keys))
	for _, k := range r.Keys {
		out = append(out, strings.Trim(k.Key, "[]"))
	}
	return out
}

// providerPlaceWidth is the label field on the provider card's search list —
// wide enough for `profiles`, the longest place there is, so the findings
// line up down the card.
const providerPlaceWidth = 10

// ProviderPlace is one place shhh looked for a provider, and what it found
// there. Found says which glyph leads the row: a place that had something is
// not the same as a place that was empty, and the card exists to make that
// difference legible.
type ProviderPlace struct {
	// Label is the place: `env`, `config`, `profiles`, `local`.
	Label string
	// Detail is what was there — the variables that were unset, the file that
	// had no provider block, the endpoint that answered.
	Detail string
	// Emphasis is the leading part of Detail rendered in body text rather
	// than dim: the finding, where there was one.
	Emphasis string
	Found    bool
}

// ProviderCard is the one card a missing provider earns (§17b). It names
// every place shhh looked and what it found there, then says which one is the
// likely fix — a missing-key message that does not say where it looked is a
// message that cannot be acted on.
type ProviderCard struct {
	// Places are the search order, first looked at first.
	Places []ProviderPlace
	// Likely is the sentence under the list: which of the places is the fix.
	// Empty when nothing found was close enough to point at.
	Likely string
	// Keys are the offers on the action bar.
	Keys []KeyOffer
}

// Update resolves on any offered key, and on esc, which declines. The result
// is the chosen keystroke, or "" for a decline — esc always dismisses, never
// destroys.
func (c *ProviderCard) Update(msg tea.KeyMsg) (done bool, result any) {
	pressed := msg.String()
	switch pressed {
	case "esc", "ctrl+c", "q":
		return true, ""
	}
	for _, k := range c.Keys {
		if strings.Trim(k.Key, "[]") == pressed {
			return true, pressed
		}
	}
	return false, nil
}

// View renders the card at the given width.
func (c ProviderCard) View(width int) string {
	rows := []string{sty.Dim.Render(c.lookedIn())}
	for _, p := range c.Places {
		rows = append(rows, c.placeRow(p))
	}
	if c.Likely != "" {
		rows = append(rows, "", sty.Dim.Render(c.Likely))
	}
	if len(c.Keys) > 0 {
		rows = append(rows, cardRule, keyOffers(c.Keys))
	}
	// Accent, not the default gray: this is the one card that stops the
	// session, and the border says so the way a gated mode does
	// (ui_kits/cockpit/Edges.html in the shhh Design System project).
	border := sty.Accent
	return renderChromeCard(cardChrome{
		title: "No model provider configured",
		style: &border,
	}, rows, width)
}

// lookedIn is the card's first line, counting the places rather than naming
// them — the list below names them.
func (c ProviderCard) lookedIn() string {
	noun := "places"
	if len(c.Places) == 1 {
		noun = "place"
	}
	return "shhh looked in " + spellNumber(len(c.Places)) + " " + noun + ":"
}

// placeRow is one row of the search list: the glyph for whether anything was
// there, the place in a fixed field so the details line up, then the finding.
func (c ProviderCard) placeRow(p ProviderPlace) string {
	glyph := sty.Del.Render("✗")
	if p.Found {
		glyph = sty.Add.Render("✓")
	}
	label := p.Label
	if pad := providerPlaceWidth - len([]rune(label)); pad > 0 {
		label += strings.Repeat(" ", pad)
	}
	detail := sty.Dim.Render(p.Detail)
	if p.Emphasis != "" {
		detail = sty.Body.Render(p.Emphasis)
		if p.Detail != "" {
			detail += sty.Dim.Render(" — " + p.Detail)
		}
	}
	return "  " + glyph + " " + sty.Body.Render(label) + detail
}

// spellNumber writes the small counts as words, because "shhh looked in 4
// places" reads like a log line and "in four places" reads like a sentence.
func spellNumber(n int) string {
	words := []string{"no", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"}
	if n >= 0 && n < len(words) {
		return words[n]
	}
	return strconv.Itoa(n)
}

// SecretPrompt is the masked one-line entry an auth failure's [k] opens
// (S-106). It is a component rather than a reuse of the input textarea for
// one reason: what is typed here must not reach the input history, the
// transcript, or the recorded-input path that every other keystroke in the
// session goes through.
//
// It echoes a bullet per rune and never renders the value. Esc declines and
// resolves to "", which leaves the key that was already there in place — esc
// never destroys.
type SecretPrompt struct {
	// Prompt is the question, e.g. `Paste a key for openai`.
	Prompt string
	// Hint names the variables the key stands in for, so the reader knows
	// which one they are replacing for this session.
	Hint string
	// Replace is the last four characters of the key being replaced, or ""
	// when there was none.
	Replace string

	value []rune
}

// Update accumulates the key. Enter resolves to what was typed, esc to "";
// backspace deletes; every other printable rune is appended. Paste arrives as
// a run of runes, which is the ordinary case here.
func (s *SecretPrompt) Update(msg tea.KeyMsg) (done bool, result any) {
	switch msg.Type {
	case tea.KeyEnter:
		return true, strings.TrimSpace(string(s.value))
	case tea.KeyEsc, tea.KeyCtrlC:
		return true, ""
	case tea.KeyBackspace:
		if len(s.value) > 0 {
			s.value = s.value[:len(s.value)-1]
		}
		return false, nil
	case tea.KeyRunes, tea.KeySpace:
		s.value = append(s.value, msg.Runes...)
		if msg.Type == tea.KeySpace {
			s.value = append(s.value, ' ')
		}
		return false, nil
	}
	return false, nil
}

// Len is how many characters have been entered, for a caller that wants to
// know whether anything has been.
func (s *SecretPrompt) Len() int { return len(s.value) }

// View renders the prompt, the mask, and the two keys it offers.
func (s SecretPrompt) View(width int) string {
	head := sty.Body.Render(s.Prompt)
	if s.Replace != "" {
		head += sty.Dim.Render(" · replacing ···" + s.Replace)
	} else if s.Hint != "" {
		head += sty.Dim.Render(" · stands in for " + s.Hint)
	}
	mask := strings.Repeat("•", min(len(s.value), max(width-4, 1)))
	entry := sty.Dim.Render("▸ ") + sty.Accent.Render(mask) + sty.FocusRow.Render(" ")
	keys := keyOffers([]KeyOffer{
		{Key: "[enter]", Label: "use it for this session"},
		{Key: "[esc]", Label: "keep the current key"},
	})
	return strings.Join([]string{
		clip(head, width),
		clip(entry, width),
		clip(keys, width),
	}, "\n")
}
