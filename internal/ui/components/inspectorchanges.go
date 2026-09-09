package components

// The rail's CHANGES block: every path the session has written, the counts
// beside them, and the workspace alerts that ride with them. It is a file of
// its own because it is the block that has to fold — the list outgrows the
// rail and the marker it hides rows behind is part of the same decision.

import (
	"fmt"
)

// InspectorFile is one changed path in the CHANGES block: the session's net
// change to it, however many turns produced that.
type InspectorFile struct {
	Path           string
	Added, Removed int
	// Turns is how many turns edited this path. Above one it is stated as
	// `3t` beside the counts, because repeat edits collapse to one row and
	// the row should say that it did.
	Turns int
	// ThisTurn marks a path the running turn touched. Those rows are the last
	// the fold takes, so the turn in front of you keeps its rows while the
	// session's older ones go behind `… N more`.
	ThisTurn bool
	// Mode states a change of permissions the session made to this file,
	// already worded by whoever knows the two modes. A file whose whole
	// change is its mode has no lines to count, so this stands where the
	// counts would: the row would otherwise say `+0 −0` about a real change,
	// which is a zero nothing measured
	// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
	// A file that changed lines as well states its counts and nothing else
	// here — there is one field's room on the rail and the counts are what
	// it is for; the mode is on that file's review row and in its diff
	// header, both of which have the columns to say both.
	Mode string
}

// InspectorAlert is one thing the workspace is still wrong about: a command
// whose last run in this session came back broken, what it said, and the turn
// that ran it. Alerts outlive their turn and clear when the workspace is
// clean — a red row that clears itself because a new turn started is the
// exact failure this rail exists to prevent.
type InspectorAlert struct {
	Label string
	Note  string
	// Turn is the turn that ran it; zero prints no turn field.
	Turn int64
}

// InspectorCommit is what this session has banked: the sha it landed on, how
// many files went with it, and where that leaves the branch. It is one row
// and it is pinned, because the question it answers — is any of this safe
// yet — is the reason a reader looks at this block at all.
type InspectorCommit struct {
	SHA   string
	Files int
	// Ahead is the branch's distance from its upstream in words, e.g.
	// `2 ahead of origin/main`. Empty where the branch tracks nothing: a
	// distance from nowhere is not a fact
	// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
	Ahead string
}

// InspectorChanges is the CHANGES block: what this session has written to the
// workspace, what of it has been banked, and what about it is still broken.
type InspectorChanges struct {
	Files          []InspectorFile
	Added, Removed int
	// Alerts are the failing commands still standing, oldest first. They are
	// drawn above the file rows and are the last thing truncation takes.
	Alerts []InspectorAlert
	// Committed is the session's own commit, where it has made one.
	Committed *InspectorCommit
	// Foreign are the paths in the tree that this session did not write and
	// has never staged — the reader's own uncommitted work, sitting beside
	// the agent's. They are named rather than counted because the promise a
	// commit card makes about them is a promise about particular files, and
	// a reader checking it wants to see the file
	// (docs/capabilities/approvals-and-safety.md#the-writing-half-of-git-is-a-tool-too).
	Foreign []string
}

// changesBlock is the session's own diff: every path it has
// touched since it opened, one row each, with the alerts still standing above
// them. The heading says "session" in words because THIS TURN counts files
// too, and a rail that printed two bare counts would read as a contradiction.
func (r InspectorRail) changesBlock(width int) (railBlock, bool) {
	c := r.Changes
	if c == nil || (len(c.Files) == 0 && len(c.Alerts) == 0 && len(c.Foreign) == 0) {
		return railBlock{}, false
	}
	meta := ""
	if len(c.Files) > 0 {
		meta = sty.Dim.Render("session · ") + DiffStat(c.Added, c.Removed)
		if c.Added == 0 && c.Removed == 0 {
			// Every file below changed its permissions and nothing else, so
			// there are no lines to total. The heading keeps its scope and
			// drops the pair of zeros nothing measured
			// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out);
			// the rows say what did change.
			meta = sty.Dim.Render("session")
		}
	}
	b := railBlock{heading: railHeading("CHANGES", meta, sty.Dim, width)}
	// The alerts come first and are pinned: they are what the block exists to
	// keep on screen, and the turn that caused one is part of the fact.
	for _, a := range c.Alerts {
		turn := ""
		if a.Turn > 0 {
			turn = sty.Dim.Render(fmt.Sprintf("turn %d", a.Turn))
		}
		b.pin(railRow(" "+sty.Err.Render("✗")+" "+sty.Body.Render(a.Label), turn, width, inspectorIndent))
		if a.Note != "" {
			b.pin(railRow(sty.Dim.Render(a.Note), "", width, inspectorIndent+2))
		}
	}
	// What has been banked, above the paths that have not. It is pinned for
	// the reason the alerts are: it is the one row in the block that says
	// some of this work is now somewhere a session ending cannot lose it.
	if cm := c.Committed; cm != nil {
		stated := sty.Body.Render("committed "+cm.SHA) +
			sty.Dim.Render(" · "+plural(cm.Files, "file"))
		if cm.Ahead != "" {
			stated += sty.Dim.Render(" · " + cm.Ahead)
		}
		b.pin(railRow(" "+sty.Add.Render("✓")+" "+stated, "", width, inspectorIndent))
	}
	for _, f := range c.Files {
		// The changed-file row carries the mutation rail and the edit glyph,
		// so the close of a turn looks like the rows that produced it.
		lead := sty.Accent.Render("▎") + sty.Accent.Render("✎") + " "
		stats := DiffStat(f.Added, f.Removed)
		counted := true
		if f.Mode != "" && f.Added == 0 && f.Removed == 0 {
			// Nothing counted this row, so it states the change it does have
			// rather than the zero it does not, and it brings no counts to
			// the fold's total.
			stats, counted = sty.Dim.Render(f.Mode), false
		}
		if f.Turns > 1 {
			// Repeat edits collapsed to one row, so the row says how many
			// turns are behind its counts.
			stats += " " + sty.Dim.Render(fmt.Sprintf("%dt", f.Turns))
		}
		b.rows = append(b.rows, railLine{
			text:    railRow(lead+sty.Body.Render(f.Path), stats, width, inspectorIndent),
			pinned:  f.ThisTurn,
			counted: counted,
			added:   f.Added,
			removed: f.Removed,
			// The row already knows the path it is about, so a host answering
			// a pointer never has to read it back out of the styled text —
			// which carries a clipped path and a stats field besides.
			target: RailTarget{Kind: RailTargetFile, Name: f.Path},
		})
	}
	// And what the session did not write, under what it did. The glyph
	// column is a bare `·`: none of the acts it names happened to these
	// files here, and an empty column would read as a row of the list above
	// that lost its mark.
	for _, path := range c.Foreign {
		// "yours" is the reader's uncommitted work beside a commit; a
		// resume names a path the session no longer owns as drifted,
		// which is the other reason a file sits in this list.
		label := "yours"
		if c.Committed == nil {
			label = "drifted"
		}
		b.add(railRow(" "+sty.Dim.Render("·")+" "+sty.Dimmer.Render(path),
			sty.Dim.Render(label), width, inspectorIndent))
	}
	b.fold = func(hidden []railLine) string { return changesFold(hidden, width) }
	return b, true
}

// changesFold is the marker the file list folds behind when the rail is
// shorter than it. It carries its own counts, so the rows it swallowed are
// still accounted for (invariant 4); rows with no counts of their own — a
// truncated alert — fold behind a bare marker rather than a fabricated zero.
func changesFold(hidden []railLine, width int) string {
	var added, removed, counted int
	for _, h := range hidden {
		if !h.counted {
			continue
		}
		counted++
		added += h.added
		removed += h.removed
	}
	left := sty.Hint.Render(fmt.Sprintf("… %d more", len(hidden)))
	if counted == 0 {
		return indentRow(left, width)
	}
	return railRow(left, DiffStat(added, removed), width, inspectorIndent)
}
