package components

// Fan-out lanes (S-110, DESIGN-TUI.md §9g). Three children streaming their
// rows into one transcript reads as one confused feed, so a spawn of two or
// more collapses into a single block with a lane per child: its name, what it
// was asked to do, how far it has got, what it has cost, and what state it is
// in.
//
// Three rules the block enforces rather than documents. A blocked lane sorts
// to the top and says `⚠ needs you` in words, because the only thing a
// fan-out can need from you is an answer and it must never be the thing you
// scroll past. A lane draws a progress bar only when the spawn declared a
// step count (S-094) — without one it gets the spinner, never a ratio nobody
// supplied. And progress stops being the point once a child finishes: a
// settled lane reports its outcome and the first line of what it found.
//
// This is a passive renderer. The block re-renders from the supervisor's live
// snapshot at whatever width the host has, so a resize costs nothing and no
// layout state lives here.

import (
	"fmt"
	"strings"
)

// FanoutState is one lane's lifecycle state. It mirrors the supervisor's
// child states; the block keeps its own so nothing in components imports the
// orchestration package.
type FanoutState int

const (
	FanoutQueued  FanoutState = iota // · accepted, waiting for a slot
	FanoutRunning                    // working
	FanoutBlocked                    // ⚠ waiting on an answer from you
	FanoutIdle                       // turn cancelled, waiting for steering
	FanoutDone                       // ✓ finished
	FanoutFailed                     // ✗ broke
)

// settled reports whether the lane has stopped moving, which is when its
// progress stops being worth drawing.
func (s FanoutState) settled() bool { return s == FanoutDone || s == FanoutFailed }

// FanoutLane is one child of the batch.
type FanoutLane struct {
	State FanoutState
	// Name is the child's name; it takes the verb column, so a lane lines up
	// with the rows around it.
	Name string
	// Task is the one-line label of what it was asked to do — the only field
	// that grows, and the only one that clips.
	Task string
	// Step and Steps are progress against a declared step count. Steps of
	// zero means none was declared and the lane spins instead.
	Step, Steps int
	// Tools is the call count so far; Spend is pre-formatted by the host
	// (dollars where the pricing table knows the child's model, tokens
	// otherwise); Elapsed is the 6-column duration field.
	Tools   int
	Spend   string
	Elapsed string
	// Summary is the first line of a finished child's report.
	Summary string
	// Waiting names what a blocked child is waiting for, stated under the
	// lane: "needs you" without saying what for sends you looking.
	Waiting string
	// Frame is the spinner frame for a lane with no declared step count; the
	// host ticks it.
	Frame int
}

// FanoutBlock is the whole batch — the header stating how many children are
// running and how many need you, then one lane each.
type FanoutBlock struct {
	Lanes []FanoutLane
	// Elapsed is the batch's own duration field: the longest-lived lane's.
	Elapsed string
	// Keys are the offers the block makes while a child is waiting on you.
	// They render once, under the lanes, and wrap rather than clip on a
	// narrow terminal (S-099's wrapOffers).
	Keys []TurnKey
}

// fanoutLead is the gutter a lane shares with an activity row: the pointer
// column, the mutation rail (a child's progress is a report, never an act),
// the state glyph, and the verb. The verb is `agent` — §6c's name for a
// child's mirrored row in the parent transcript — because a lane is that row,
// one per child. The child's own name goes in the target field, which is the
// only field that grows: a name is not a word from a closed vocabulary and
// must never be clipped to eight columns, where `researcher-1` and
// `researcher-2` become the same string.
func fanoutLead(glyph string) string {
	return strings.Repeat(" ", ptrWidth+railWidth) + glyph + " " + verbField("agent")
}

// headerLead is the same gutter one level out — the block heads its lanes the
// way a step header heads its rows, so the nesting is visible without a rule.
// It still fills leadWidth, so the header's target and duration land in the
// same columns as its lanes': only the gutter moves.
func headerLead() string {
	return strings.Repeat(" ", railWidth) + infoStyle.Render("◇") + " " +
		verbField("fan-out") + strings.Repeat(" ", ptrWidth)
}

// glyph pairs every lane state with a mark of its own, so a monochrome
// terminal keeps them apart (invariant 1).
func (l FanoutLane) glyph() string {
	switch l.State {
	case FanoutQueued:
		return dimStyle.Render("·")
	case FanoutBlocked:
		return errStyle.Render("⚠")
	case FanoutIdle:
		return dimStyle.Render("⊘")
	case FanoutDone:
		return addStyle.Render("✓")
	case FanoutFailed:
		return errStyle.Render("✗")
	default:
		return infoStyle.Render("◇")
	}
}

// progress is the lane's left-hand status field: the meter when the spawn
// declared a step count, the spinner when it did not, and the outcome word
// once the child has settled — a bar against a finished child measures
// nothing.
func (l FanoutLane) progress() string {
	switch l.State {
	case FanoutBlocked:
		return errStyle.Render("⚠ needs you")
	case FanoutQueued:
		return dimStyle.Render("queued")
	case FanoutIdle:
		return dimStyle.Render("idle")
	case FanoutDone:
		return addStyle.Render("done")
	case FanoutFailed:
		return errStyle.Render("failed")
	}
	if m, ok := AgentMeter(l.Step, l.Steps); ok {
		m.Text = fmt.Sprintf("%d/%d", min(max(l.Step, 0), l.Steps), l.Steps)
		return m.View()
	}
	return Spinner{Frame: l.Frame, Label: "working"}.View()
}

// stats is what the lane cost so far: the calls it made and the money it
// spent, each left out when there is nothing to report rather than stated as
// a zero.
func (l FanoutLane) stats() string {
	var parts []string
	if l.Tools > 0 {
		parts = append(parts, plural(l.Tools, "tool"))
	}
	if l.Spend != "" {
		parts = append(parts, l.Spend)
	}
	if len(parts) == 0 {
		return ""
	}
	return dimmerStyle.Render(strings.Join(parts, " · "))
}

// outcomeField joins the lane's progress and its stats into the one
// right-aligned field, the way an activity row joins outcome and counts.
func (l FanoutLane) outcomeField() string {
	progress, stats := l.progress(), l.stats()
	switch {
	case progress == "":
		return stats
	case stats == "":
		return progress
	}
	return progress + dimStyle.Render(" · ") + stats
}

// target is the lane's growing field: the child's name, then what it was
// asked to do.
func (l FanoutLane) target() string {
	if l.Task == "" {
		return l.Name
	}
	return l.Name + "  " + l.Task
}

// paintTarget leads the field with the name in body text and dims the task
// behind it. A field too narrow to hold the name whole goes dim entirely
// rather than emphasising half a name — the same rule the recovery rows keep.
func (l FanoutLane) paintTarget(s string) string {
	if l.Name != "" && strings.HasPrefix(s, l.Name) {
		return bodyStyle.Render(l.Name) + dimStyle.Render(strings.TrimPrefix(s, l.Name))
	}
	return dimStyle.Render(s)
}

// View renders one lane plus whatever it has to say underneath: what a
// blocked child is waiting for, or the first line of a finished child's
// report.
func (l FanoutLane) View(width int) string {
	lines := []string{gridLineWith(fanoutLead(l.glyph()), l.target(), l.paintTarget,
		l.outcomeField(), l.Elapsed, width)}
	if note := l.note(); note != "" {
		lines = append(lines, indented(note, detailIndent, width))
	}
	return strings.Join(lines, "\n")
}

// note is the line under the lane: a blocked child's reason, or a finished
// child's result. A running child has nothing to add that the lane does not
// already say.
func (l FanoutLane) note() string {
	if l.State == FanoutBlocked {
		return l.Waiting
	}
	if l.State.settled() {
		return l.Summary
	}
	return ""
}

// sorted returns the lanes in render order: blocked first, in the order they
// were spawned, then everything else in that same order. A child that needs
// an answer is the only thing in a fan-out that cannot wait, so it is never
// below one that can.
func (b FanoutBlock) sorted() []FanoutLane {
	var blocked, rest []FanoutLane
	for _, l := range b.Lanes {
		if l.State == FanoutBlocked {
			blocked = append(blocked, l)
			continue
		}
		rest = append(rest, l)
	}
	return append(blocked, rest...)
}

// counts tallies the batch for its header.
func (b FanoutBlock) counts() (running, blocked, done, failed int) {
	for _, l := range b.Lanes {
		switch l.State {
		case FanoutBlocked:
			blocked++
		case FanoutDone:
			done++
		case FanoutFailed:
			failed++
		default:
			running++
		}
	}
	return running, blocked, done, failed
}

// headerOutcome states what the batch still owes you. Whoever needs an answer
// is said first and in del, because it is the only part of the line that asks
// anything of you; the batch's tally of finished children is left to the
// lanes until nothing is running, when it becomes the whole story. The field
// never clips (§6a), so it says two things at most.
func (b FanoutBlock) headerOutcome() string {
	running, blocked, done, failed := b.counts()
	var parts []string
	if blocked > 0 {
		parts = append(parts, errStyle.Render(fmt.Sprintf("%d needs you", blocked)))
	}
	if running > 0 {
		parts = append(parts, spinTextStyle.Render(fmt.Sprintf("%d running", running)))
	}
	if len(parts) == 0 {
		if done > 0 {
			parts = append(parts, addStyle.Render(fmt.Sprintf("%d done", done)))
		}
		if failed > 0 {
			parts = append(parts, errStyle.Render(fmt.Sprintf("%d failed", failed)))
		}
	}
	return strings.Join(parts, dimStyle.Render(" · "))
}

// View renders the block at the given width: the header, then every lane in
// sort order, then the offers a blocked lane makes.
func (b FanoutBlock) View(width int) string {
	if len(b.Lanes) == 0 {
		return ""
	}
	lanes := b.sorted()
	lines := []string{gridLine(
		headerLead(),
		plural(len(lanes), "agent"),
		b.headerOutcome(), b.Elapsed, width)}
	for _, l := range lanes {
		lines = append(lines, l.View(width))
	}
	if _, blocked, _, _ := b.counts(); blocked > 0 {
		for _, keys := range wrapOffers(b.Keys, max(width-detailIndent, 1)) {
			lines = append(lines, detailLine(keys, width))
		}
	}
	return strings.Join(lines, "\n")
}
