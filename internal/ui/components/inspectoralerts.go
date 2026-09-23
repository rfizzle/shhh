package components

// The rail's ALERTS block: what this session has run that is still broken
// (docs/interface/surfaces.md#the-inspector-rail). It is a block of its own
// rather than rows above the changeset, because it is neither the turn nor
// the session and it answers a different question from both: not what has
// been done to the workspace, but what the workspace is still wrong about.
//
// It is a file of its own for the reason CHANGES is: it is bounded by a fold
// rather than by how much room is left, and the rule that decides which rows
// are behind the marker is the block.

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

// inspectorLiveAlerts is how many live alerts draw as rows. Two, because the
// block is a pointer at what wants answering and not a log: a session that
// has broken five things is not helped by five red rows, and the fifth is
// the one nobody reads. The rest are behind the marker with their count.
const inspectorLiveAlerts = 2

// InspectorAlert is one thing the workspace is still wrong about: a command
// this session ran that came back broken, what it said, and the turn it broke
// in. Alerts outlive their turn and stop being news when the command comes
// back clean or the repository's own suite passes over the tree it failed on
// — a red row that clears itself because a new turn started is the exact
// failure this rail exists to prevent.
type InspectorAlert struct {
	// Label is the command's name rather than its line: three runs of one
	// formatter over three directories are one alert, and a rail that drew
	// each line would be three quarters argument
	// (docs/interface/surfaces.md#the-inspector-rail).
	Label string
	// Note is the last run's outcome, because the last run is what the
	// workspace is currently like.
	Note string
	// Runs is how many runs of this command the row stands for, across every
	// turn it has been broken in. One states nothing: a count of one is a row
	// saying it is a row.
	Runs int
	// Turn is the turn it broke in — the first of them where it has gone on
	// breaking since; zero prints no turn field.
	Turn int64
	// Turns is how many turns it has broken in. More than one is what turns
	// the field into the turn it started in (`since turn 3`), because a row
	// naming only its latest run would date a failure four turns old to a
	// moment ago (docs/interface/surfaces.md#the-inspector-rail).
	Turns int
	// Superseded marks an alert something has since answered: one entry for
	// the whole episode, however many turns it stood in, with the runs and
	// turns it carried while it stood. It is kept and counted rather than
	// dropped — the session did break this, and getting to green is work the
	// block can still account for — but it is never drawn as a row, so an
	// answered failure cannot push a changed file off the rail.
	Superseded bool
}

// InspectorAlerts is the ALERTS block: every command this session has broken,
// in the order they broke, the superseded ones marked rather than dropped.
type InspectorAlerts []InspectorAlert

// Live are the alerts still standing — the block's own news, and what any
// other reader of the session's bad news wants. Nothing else on this surface
// may treat a superseded alert as a current failure.
func (a InspectorAlerts) Live() []InspectorAlert {
	var live []InspectorAlert
	for _, alert := range a {
		if !alert.Superseded {
			live = append(live, alert)
		}
	}
	return live
}

// alertsBlock is the standing bad news, bounded. The two most recent live
// alerts draw and everything else — the older live ones and every superseded
// one — is behind the marker with its count, so the block is four rows at its
// tallest however long the session has been failing at things.
//
// A block whose every alert has been answered is not drawn at all. Its news
// is history by then, the transcript is where history is read, and two rows
// of answered failure on a rail this short are two rows the changeset wanted.
func (r InspectorRail) alertsBlock(width int) (railBlock, bool) {
	live := r.Alerts.Live()
	if len(live) == 0 {
		return railBlock{}, false
	}
	superseded := len(r.Alerts) - len(live)
	b := railBlock{heading: railHeading("ALERTS",
		fmt.Sprintf("%d standing", len(live)), sty.Err, width)}
	// The most recent live alerts are the ones drawn: an alert older than
	// two failures ago is the least likely of them to be what the session is
	// standing in front of now.
	drawFrom := len(live) - inspectorLiveAlerts
	seen := 0
	for _, a := range r.Alerts {
		row := railLine{text: alertRow(a, width), pinned: true}
		if a.Superseded {
			b.hidden = append(b.hidden, row)
			continue
		}
		if seen < drawFrom {
			// A live alert behind the fold is still live, so it is counted
			// with the news rather than with the history.
			b.hidden = append(b.hidden, row)
		} else {
			b.rows = append(b.rows, row)
		}
		seen++
	}
	// The marker reads the superseded count off the block rather than off the
	// rows it was handed, because truncation can put a live alert behind it
	// too: what is hidden and not one of the answered ones is one of those.
	b.fold = func(hidden []railLine) string {
		return alertsFold(len(hidden)-superseded, superseded, width)
	}
	return b, true
}

// alertRow is one alert on one row: the command's name, what its last run
// came to and how many runs are behind that, and the turn it broke in.
//
// The account is dropped whole rather than clipped where the three will not
// fit. The name is what the reader acts on and the account beside it is
// telemetry, so a row that spent its last columns on `· 4 ru…` would have
// bought nothing with the half of a command name it gave up for them
// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
func alertRow(a InspectorAlert, width int) string {
	turn := ""
	if a.Turn > 0 {
		turn = sty.Dim.Render(alertTurn(a))
	}
	left := " " + sty.Err.Render("✗") + " " + sty.Body.Render(a.Label)
	if note := alertNote(a); note != "" {
		stated := left + "  " + sty.Dim.Render(note)
		if lipgloss.Width(stated) <= railRoom(width, turn, inspectorIndent) {
			left = stated
		}
	}
	return railRow(left, turn, width, inspectorIndent)
}

// alertTurn is the row's turn field: the turn it broke in, said as the turn it
// has been broken since where it has gone on breaking in later turns. The
// count of those turns is not a field of its own — what the reader does about
// a failure is the same whether it has stood for two turns or five, and the
// run count beside it already says how much has been thrown at it.
func alertTurn(a InspectorAlert) string {
	if a.Turns > 1 {
		return fmt.Sprintf("since turn %d", a.Turn)
	}
	return fmt.Sprintf("turn %d", a.Turn)
}

// alertNote is the account beside the name: what the last run came to, and
// the run count where the row stands for more than one run.
func alertNote(a InspectorAlert) string {
	switch {
	case a.Runs > 1 && a.Note != "":
		return fmt.Sprintf("%s · %d runs", a.Note, a.Runs)
	case a.Runs > 1:
		return fmt.Sprintf("%d runs", a.Runs)
	}
	return a.Note
}

// alertsFold is the marker the block folds behind. The two counts are two
// different facts — what is still standing and what has been answered — so
// they are two fields rather than one total, and a fold with nothing standing
// behind it leads with the answered count rather than a zero.
func alertsFold(more, superseded int, width int) string {
	if more <= 0 {
		return indentRow(sty.Hint.Render(fmt.Sprintf("… %d superseded", superseded)), width)
	}
	right := ""
	if superseded > 0 {
		right = sty.Dim.Render(fmt.Sprintf("%d superseded", superseded))
	}
	return railRow(sty.Hint.Render(fmt.Sprintf("… %d more", more)), right, width, inspectorIndent)
}
