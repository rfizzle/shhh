package components

// The doctor surface (
// docs/interface/surfaces.md#the-supporting-screens,
// ui_kits/cockpit/Tools.html). `shhh code doctor` printed two paragraphs of
// key/value lines about the sandbox ladder and nothing else; `shhh doctor` is
// the whole setup, and it is re-cut here from parts that already exist: the
// column grid for each check, the closed state vocabulary for what became of
// it, and the failure row's rule that a failure names the fix rather than the
// blame (docs/interface/principles.md#one-grid).
//
// Three rules shape it, and all three come from the screen being re-cut from
// parts that already exist
// (docs/interface/surfaces.md#the-supporting-screens). A check is a tool
// call, so it is the row — glyph, verb, target, outcome, right-aligned
// duration, nothing invented. A failure states its consequence in the
// product's own words, quoted from the surface the reader will actually meet
// it on. And the fix is offered on the row that failed rather than in a
// footer, which is what makes a doctor run something you can act on without
// scrolling back.
//
// It is a passive component like the rest of this package. It owns no
// diagnostic semantics: the host probes the machine, decides what passed,
// writes every sentence and formats every duration, and `[c]` and `[r]`
// resolve to a DoctorCommand it carries out. That is why the screen can draw
// `⚠ provider · no key` without knowing what a provider is.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const (
	// doctorFixIndent is where the lines behind `[f]` sit: the grid's nested
	// detail, one step in from the consequence and the key row that frame them.
	doctorFixIndent = 6
)

// DoctorState is what became of one check, and which glyph says so. The five
// are the outcome vocabulary read for a diagnostic: two of them are terminal
// answers, one is a check that had nothing to look at, and two are states a
// run passes through while it is still going.
type DoctorState int

const (
	// DoctorPassed — ✓ add (10): the check looked and found nothing wrong.
	DoctorPassed DoctorState = iota
	// DoctorWarned — ⚠ accent (214): it works, and something about it will cost
	// the reader later.
	DoctorWarned
	// DoctorFailed — ✗ del (9): it does not work, and the row says what that
	// means for the next session.
	DoctorFailed
	// DoctorSkipped — ⊘ dim (241): there was nothing here to check, and the
	// row says so rather than being left out (invariant 4).
	DoctorSkipped
	// DoctorRunning — ▸ spin (205), the spinner frame while the host ticks.
	DoctorRunning
	// DoctorQueued — · dim (241): accepted, not started, with an em-dash
	// duration.
	DoctorQueued
)

// DoctorCheck is one check, already resolved to what the screen draws. The
// host writes every field: what the check is called, what it found, what that
// costs and how long it took are all readings of the machine, and this is a
// renderer.
type DoctorCheck struct {
	// Name is the grid's verb field: `binary`, `provider`, `sandbox`. Eight
	// columns, so a check named longer than that is the signal that the
	// vocabulary has drifted rather than a field that grows.
	Name string
	// Subject leads the target field in body text — the thing that was checked.
	// It is the only part of the target that is not dim.
	Subject string
	// Detail continues the target in dim, joined by ` · `: the version, the
	// path, the count behind the subject.
	Detail string
	// Outcome is the right-aligned outcome field and never clips: it is the
	// reason to read the row.
	Outcome string
	// Duration is the 6-column field. Blank under half a second like every other
	// row in the product, NoDuration for a check that has not run.
	Duration string
	// Consequence is the line under a check that did not pass: what the reader
	// will see because of it, in the words of the surface they will see it on. A
	// failure that does not say what it costs is a failure the reader has to go
	// and find out about.
	Consequence string
	// Fix are the lines `[f]` reveals — the commands, the config keys, the order
	// to do them in. A check with none of them offers no key.
	Fix []string
	// FixLabel names what `[f]` opens, so the offer says how much is behind it:
	// `show the 3-line fix`.
	FixLabel string
	// State picks the glyph, the outcome's colour, and whether the row is a stop
	// for the pointer.
	State DoctorState
}

// hasFix reports whether the check has anything behind `[f]`, which is also
// what makes it a stop for the pointer: a row with nothing to do on it is not
// somewhere the pointer should be able to stand (invariant 5).
func (c DoctorCheck) hasFix() bool { return len(c.Fix) > 0 }

// DoctorAct is what a key asked the host to do. Neither of them is about one
// check: a report is the whole run, and re-running is the whole run again.
type DoctorAct int

const (
	// DoctorCopy is `[c]`: the report as text, because the next thing that
	// happens to a doctor run is that it gets pasted into an issue.
	DoctorCopy DoctorAct = iota
	// DoctorRerun is `[r]`: run the checks again, which is the key that closes
	// the loop after a fix has been applied.
	DoctorRerun
)

// DoctorCommand is one act the host carries out while the screen stays up.
// The host does it, sets Notice, and hands back fresh Checks.
type DoctorCommand struct{ Act DoctorAct }

// DoctorScreen is `shhh doctor`: a takeover surface, full width, no inspector
// rail, owning the keyboard for as long as it is up.
type DoctorScreen struct {
	// Checks are the checks in the order they run, and the order they are read.
	// The host replaces them as each one answers.
	Checks []DoctorCheck
	// Elapsed is how long the run has taken so far, stated in the header beside
	// the keys — the one number a reader watching a diagnostic wants.
	Elapsed string
	// Running says at least one check has not answered yet, which is what puts
	// the spinner in the header and holds `[r]` back: re-running a run that is
	// still going is not an offer (invariant 5).
	Running bool
	// Spin says the host is ticking and Frame is the frame it is on — one tick
	// source for the header and every running row, which is what the meter
	// guidance asks of anything in motion. A host that does not tick leaves Spin
	// false rather than freezing a braille glyph on screen, because a stopped
	// spinner reads as a hang.
	Spin  bool
	Frame int
	// Notice is the line a key left behind — what was copied, what failed to
	// copy. The host clears it on the next keystroke.
	Notice string
	// MaxLines bounds the screen height; everything pinned around the checks
	// comes off their budget before any of them is drawn. 0 is unbounded.
	MaxLines int
	// Focus is an index into Checks: the row whose `[f]` is live. It survives
	// the host replacing Checks, and lands on a row worth standing on.
	Focus int

	fix  map[int]bool
	keys bool
}

// Update is the screen's whole keyboard. Every key here is live on arrival:
// this surface holds the keyboard for as long as it is up, and there is no
// draft under it for a bare letter to belong to
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func (d *DoctorScreen) Update(msg tea.KeyPressMsg) (done bool, result any) {
	d.sync()
	switch pressed := msg.String(); {
	case pressed == "up", pressed == "k":
		d.move(-1)
	case pressed == "down", pressed == "j":
		d.move(1)
	case keys.Is(pressed, keys.Screen.Fix):
		// A row with nothing behind `[f]` does not offer it, so pressing it there
		// is not a refusal to report — there is simply no key.
		if d.stops() > 0 && d.Checks[d.Focus].hasFix() {
			d.fix[d.Focus] = !d.fix[d.Focus]
		}
	case keys.Is(pressed, keys.Screen.Copy):
		return false, DoctorCommand{Act: DoctorCopy}
	case keys.Is(pressed, keys.Screen.Again):
		if !d.Running {
			return false, DoctorCommand{Act: DoctorRerun}
		}
	case keys.Is(pressed, keys.Screen.List):
		d.keys = !d.keys
	case keys.Is(pressed, keys.Screen.Quit):
		return true, nil
	}
	return false, nil
}

// View renders the screen: the start-screen header and its rule, the checks,
// and the summary row at the foot.
func (d *DoctorScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	d.sync()
	foot := d.footRows(width)
	head := []string{d.headerRow(width), reviewRule(width), ""}

	pinned := len(head) + 1 + len(foot)
	if d.Notice != "" {
		pinned++
	}
	rows := append(head, d.bodyRows(width, d.budget(pinned))...)
	rows = append(rows, "")
	rows = append(rows, foot...)
	if d.Notice != "" {
		rows = append(rows, sty.Dim.Render(clip(d.Notice, width)))
	}
	return strings.Join(rows, "\n")
}

// budget is how many rows the checks may spend: the screen's height less
// everything pinned around them. An unbounded screen drops nothing.
func (d *DoctorScreen) budget(pinned int) int {
	if d.MaxLines <= 0 {
		return 0
	}
	return max(d.MaxLines-pinned, 1)
}

// headerRow is the start-screen header: the command, how many checks it is
// over and whether they are still going, then the elapsed time and the two
// keys every screen offers.
func (d *DoctorScreen) headerRow(width int) string {
	left := brightStyle().Render("shhh doctor")
	if n := len(d.Checks); n > 0 {
		left += sty.Dim.Render(" · " + countChecks(n))
	}
	if d.Running {
		left += sty.Dim.Render(" · ") + sty.SpinText.Render(d.spinGlyph()+" running")
	}
	right := sty.Dim.Render(screenHeaderKeys())
	if d.Elapsed != "" {
		right = sty.Dimmer.Render(d.Elapsed) + sty.Dim.Render(" · [?] keys · [q] quit")
	}
	// It is the left that gives ground, not the keys: a takeover surface that
	// dropped `[q]` would be one with no stated way out of it (invariant 5), and
	// the elapsed time goes with the keys because it is what says the run is
	// still moving. The same trade the metrics header makes.
	if room := width - lipgloss.Width(right) - 2; room > 0 {
		left = clip(left, room)
	}
	if pad := width - lipgloss.Width(left) - lipgloss.Width(right); pad >= 2 {
		return left + strings.Repeat(" ", pad) + right
	}
	return clip(right, width)
}

// spinGlyph is the header's frame, or `▸` for a host that is not ticking —
// the same reading every running glyph in the product makes.
func (d *DoctorScreen) spinGlyph() string {
	if !d.Spin {
		return "▸"
	}
	return Spinner{Frame: d.Frame}.Glyph()
}

// bodyRows is the checks and everything under them, trimmed to the budget.
//
// What gives ground first is what has nothing to say: a check that passed is
// a line of reassurance, and a check that failed is the reason the reader
// opened this screen at all. So passing and skipped checks go before any
// other row does, and only once none of those is left does the list start
// dropping from the bottom. The marker names what went (invariant 4), and the
// summary row at the foot is still counting every check either way.
func (d *DoctorScreen) bodyRows(width, budget int) []string {
	sections := make([][]string, 0, len(d.Checks))
	for i := range d.Checks {
		sections = append(sections, d.checkRows(i, width))
	}
	if budget <= 0 || rowCount(sections) <= budget {
		return flatten(sections)
	}

	kept := append([]int(nil), indexes(len(sections))...)
	for len(kept) > 1 && d.sectionCost(sections, kept)+1 > budget {
		kept = dropOne(kept, d.quietest(kept))
	}
	rows := make([]string, 0, budget)
	for _, i := range kept {
		rows = append(rows, sections[i]...)
	}
	rows = append(rows, indentBy(d.droppedRow(kept, width-ptrWidth), ptrWidth, width))
	// One check whose fix is longer than the whole screen is the case whole
	// sections cannot answer. Its own rows give ground then, and the marker
	// truncRows leaves behind says how many went — the budget is what the
	// terminal has, and overrunning it would push the summary off the bottom.
	return truncRows(rows, budget, width)
}

// sectionCost is what a set of kept checks renders to.
func (d *DoctorScreen) sectionCost(sections [][]string, kept []int) int {
	n := 0
	for _, i := range kept {
		n += len(sections[i])
	}
	return n
}

// quietest is the check to drop next: the one with least to say, and the last
// of those where several are level. That is the passes and the skips first,
// then the rows still to answer, and the failure last — so a screen with room
// for one check shows the one the reader opened it for.
func (d *DoctorScreen) quietest(kept []int) int {
	at, quietest := len(kept)-1, rank(d.Checks[kept[len(kept)-1]].State)
	for i := len(kept) - 2; i >= 0; i-- {
		if r := rank(d.Checks[kept[i]].State); r < quietest {
			at, quietest = i, r
		}
	}
	return at
}

// droppedRow names the checks that did not fit. A marker that only said "4
// more" would leave the reader guessing which four the screen is sitting on,
// which on a diagnostic is the whole question.
func (d *DoctorScreen) droppedRow(kept []int, width int) string {
	shown := map[int]bool{}
	for _, i := range kept {
		shown[i] = true
	}
	var names []string
	for i, check := range d.Checks {
		if !shown[i] {
			names = append(names, check.Name)
		}
	}
	return sty.Dim.Render(clip(
		fmt.Sprintf("↓ %d more · %s", len(names), strings.Join(names, " · ")), width))
}

// checkRows is one check: its row on the grid, the consequence under a check
// that did not pass, the fix behind `[f]` while it is open, and the key row
// that offers it.
func (d *DoctorScreen) checkRows(i, width int) []string {
	check := d.Checks[i]
	rows := []string{d.checkRow(i, width)}
	if check.Consequence != "" {
		rows = append(rows, detailLine(sty.Dimmer.Render(check.Consequence), width))
	}
	if check.hasFix() && d.fix[i] {
		for _, line := range check.Fix {
			rows = append(rows, indentBy(sty.Body.Render(
				clip(line, max(width-doctorFixIndent, 1))), doctorFixIndent, width))
		}
	}
	if row := d.fixKeyRow(i, width); row != "" {
		rows = append(rows, row)
	}
	return rows
}

// fixKeyRow is the offer under a check that has a fix. The row under the
// pointer offers it live; the others carry the same key grey, because a key
// is inert until the surface that offers it holds the keyboard and on this
// screen that surface is one row
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
// A screen with only one such check therefore never draws a grey key at all.
func (d *DoctorScreen) fixKeyRow(i, width int) string {
	check := d.Checks[i]
	if !check.hasFix() {
		return ""
	}
	label := check.FixLabel
	if label == "" {
		label = fmt.Sprintf("show the %d-line fix", len(check.Fix))
	}
	if d.fix[i] {
		label = "hide it"
	}
	offer := []TurnKey{keyOfferAs(keys.Screen.Fix, label)}
	if i != d.Focus {
		return detailLine(inertOffers(offer), width)
	}
	return detailLine(keyOffers(offer), width)
}

// checkRow is the check on the grid. The mutation-rail column stays blank on
// every row including the failures: a check reports on the machine, it does
// not change it, and the mutation rail means the row did
// (docs/interface/principles.md#weight-tracks-risk).
func (d *DoctorScreen) checkRow(i, width int) string {
	check := d.Checks[i]
	lead := d.pointer(i) + strings.Repeat(" ", railWidth) +
		d.glyph(check.State) + verbField(check.Name)
	return gridLineWith(lead, check.target(), check.paintTarget,
		check.outcomeField(), check.Duration, width)
}

// pointer is the focus cursor in the grid's own gutter — the two columns the
// artboard leaves as an indent. It is drawn only where there is somewhere for
// it to move: a run with nothing to fix has no pointer and no `[↑↓]`.
func (d *DoctorScreen) pointer(i int) string {
	if d.stops() > 0 && i == d.Focus {
		return sty.FocusPointer.Render("❯") + " "
	}
	return strings.Repeat(" ", ptrWidth)
}

// glyph is the state's glyph in the state's colour. The word beside it in the
// outcome field carries the same meaning, so the colour is reinforcement
// rather than the message (invariant 1).
func (d *DoctorScreen) glyph(state DoctorState) string {
	switch state {
	case DoctorWarned:
		return sty.Accent.Render("⚠") + " "
	case DoctorFailed:
		return sty.Err.Render("✗") + " "
	case DoctorSkipped:
		return sty.Dim.Render("⊘") + " "
	case DoctorRunning:
		return sty.SpinText.Render(d.spinGlyph()) + " "
	case DoctorQueued:
		return sty.Dim.Render("·") + " "
	}
	return sty.Add.Render("✓") + " "
}

// target assembles the growing field: what was checked, and the facts behind
// it.
func (c DoctorCheck) target() string {
	switch {
	case c.Detail == "":
		return c.Subject
	case c.Subject == "":
		return c.Detail
	}
	return c.Subject + " · " + c.Detail
}

// paintTarget leads the field with the subject in body text and dims the rest
// behind it, the same reading a recovery row makes. A field too narrow to
// hold the subject whole goes dim entirely rather than emphasising half a
// version number.
func (c DoctorCheck) paintTarget(s string) string {
	if c.Subject != "" && strings.HasPrefix(s, c.Subject) {
		return sty.Body.Render(c.Subject) + sty.Dim.Render(strings.TrimPrefix(s, c.Subject))
	}
	return sty.Dim.Render(s)
}

// outcomeField colours the right-aligned field by state. It never clips.
func (c DoctorCheck) outcomeField() string {
	if c.Outcome == "" {
		return ""
	}
	switch c.State {
	case DoctorWarned:
		return sty.Accent.Render(c.Outcome)
	case DoctorFailed:
		return sty.Del.Render(c.Outcome)
	case DoctorRunning:
		return sty.SpinText.Render(c.Outcome)
	}
	return sty.Dim.Render(c.Outcome)
}

// footRows are the summary and the keys beside it. The doctor screen puts the
// counts on the line and the key in the right-hand field, which is the
// reverse of the other three supporting screens: on a diagnostic the thing to
// read is what the run found, and `[c]` is the annotation.
func (d *DoctorScreen) footRows(width int) []string {
	if d.keys {
		rows := make([]string, 0, len(d.keyList())+1)
		for _, offer := range d.keyList() {
			rows = append(rows, clip(keyOffers([]TurnKey{offer}), width))
		}
		return append(rows, clip(keyOffers([]TurnKey{hideKeysOffer()}), width))
	}
	summary := indentBy(d.summaryRow(), ptrWidth, width)
	offers := d.offers()
	if len(offers) == 0 {
		return []string{summary}
	}
	// The keys annotate the summary where both fit, and take a row of their own
	// where they do not. Nothing on a key row is ever truncated to make room
	// (invariant 4).
	keys := keyOffers(offers)
	if pad := width - lipgloss.Width(summary) - lipgloss.Width(keys); pad >= 2 {
		return []string{summary + strings.Repeat(" ", pad) + keys}
	}
	return append([]string{summary}, wrapOffers(offers, width)...)
}

// summaryRow counts every outcome, including the checks still running, and
// leads with the glyph of the worst one — so a run that failed says so on the
// one line a reader who has scrolled away can still see.
func (d *DoctorScreen) summaryRow() string {
	if len(d.Checks) == 0 {
		return sty.Dim.Render("no checks to run")
	}
	counts := map[DoctorState]int{}
	worst := DoctorPassed
	for _, check := range d.Checks {
		counts[check.State]++
		if rank(check.State) > rank(worst) {
			worst = check.State
		}
	}
	tallies := []struct {
		state DoctorState
		word  string
		style lipgloss.Style
	}{
		{DoctorFailed, "failed", sty.Del},
		{DoctorWarned, "warning", sty.Accent},
		{DoctorPassed, "passed", sty.Body},
		{DoctorSkipped, "not checked", sty.Dim},
		{DoctorRunning, "running", sty.SpinText},
		{DoctorQueued, "queued", sty.Dim},
	}
	var parts []string
	for _, tally := range tallies {
		n := counts[tally.state]
		if n == 0 {
			continue
		}
		word := tally.word
		if tally.state == DoctorWarned && n != 1 {
			word = "warnings"
		}
		parts = append(parts, tally.style.Render(fmt.Sprintf("%d %s", n, word)))
	}
	lead := d.glyph(worst)
	return lead + strings.Join(parts, sty.Dim.Render(" · "))
}

// rank orders the states by how much they want the reader's attention, which
// is what decides the glyph the summary leads with.
func rank(state DoctorState) int {
	switch state {
	case DoctorFailed:
		return 5
	case DoctorWarned:
		return 4
	case DoctorRunning:
		return 3
	case DoctorQueued:
		return 2
	case DoctorSkipped:
		return 1
	}
	return 0
}

// offers is the key row beside the summary. `[r]` is not offered while the
// run is still going — re-running a run that has not finished is not an offer
// (invariant 5) — and `[↑↓]` only appears where there is more than one row to
// move between.
func (d *DoctorScreen) offers() []TurnKey {
	var offers []TurnKey
	if d.stops() > 1 {
		offers = append(offers, keyOffer(keys.Select.Move))
	}
	if len(d.Checks) > 0 {
		offers = append(offers, keyOfferAs(keys.Screen.Copy, "copy the report"))
	}
	if !d.Running {
		offers = append(offers, keyOffer(keys.Screen.Again))
	}
	return offers
}

// keyList is every key the screen has, for `[?]`.
func (d *DoctorScreen) keyList() []TurnKey {
	list := []TurnKey{}
	if d.stops() > 0 {
		list = append(list,
			keyOfferAs(keys.Screen.Move, "move between the checks that need something"),
			keyOfferAs(keys.Screen.Fix, "show the fix for the check under the pointer"))
	}
	list = append(list,
		keyOfferAs(keys.Screen.Copy, "copy the whole report as text"),
		keyOfferAs(keys.Screen.Again, "run every check again"),
		keyOfferAs(keys.Select.Cancel, "back to the shell"),
		keyOfferAs(keys.Screen.Quit, "back to the shell"))
	return list
}

// sync keeps the pointer on a row worth standing on. It runs before every
// Update and every View because the host replaces Checks as each one answers,
// and a pointer left on a check that has since passed would be pointing at a
// row with no key on it.
func (d *DoctorScreen) sync() {
	if d.fix == nil {
		d.fix = map[int]bool{}
	}
	if d.stops() == 0 {
		d.Focus = 0
		return
	}
	if d.Focus >= 0 && d.Focus < len(d.Checks) && d.Checks[d.Focus].hasFix() {
		return
	}
	d.Focus = d.firstStop()
}

// stops is how many checks the pointer can stand on.
func (d *DoctorScreen) stops() int {
	n := 0
	for _, check := range d.Checks {
		if check.hasFix() {
			n++
		}
	}
	return n
}

// firstStop is the first check with something to do on it.
func (d *DoctorScreen) firstStop() int {
	for i, check := range d.Checks {
		if check.hasFix() {
			return i
		}
	}
	return 0
}

// move steps the pointer to the next check that has a fix, stopping at either
// end rather than wrapping — the same reading every list in the product
// makes.
func (d *DoctorScreen) move(delta int) {
	stops := make([]int, 0, len(d.Checks))
	for i, check := range d.Checks {
		if check.hasFix() {
			stops = append(stops, i)
		}
	}
	if len(stops) == 0 {
		return
	}
	at := 0
	for i, stop := range stops {
		if stop == d.Focus {
			at = i
			break
		}
	}
	d.Focus = stops[min(max(at+delta, 0), len(stops)-1)]
}

// countChecks counts checks, in the header's own words.
func countChecks(n int) string {
	if n == 1 {
		return "1 check"
	}
	return fmt.Sprintf("%d checks", n)
}

// indexes is 0..n-1, the order the checks were run in.
func indexes(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// dropOne is the list with one position removed.
func dropOne(list []int, at int) []int {
	out := make([]int, 0, len(list)-1)
	out = append(out, list[:at]...)
	return append(out, list[at+1:]...)
}
