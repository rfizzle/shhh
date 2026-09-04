package chat

// The blast-radius block on approval cards (
// docs/interface/surfaces.md#the-approval-card). An approval that only says
// what the action *is* asks the reader to do the risk assessment themselves,
// at speed, twenty times a session. Every card states three things before the
// keys: what it touches, whether shhh can put it back, and whether the
// network is open.
//
// The block is resolved once, when the decision is armed, and stashed on the
// model — resolving it inside View would stat the filesystem and shell out to
// git on every frame. internal/radius does the reading; this file turns what
// it found into the card's rows, and it is the only place that decides what
// shhh is willing to claim about reversibility.

import (
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/radius"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// blastRadius is one decision's resolved context: everything the card shows
// between the headline and the keys, plus the two pieces of chrome that
// reinforce it.
type blastRadius struct {
	severity components.Severity
	risks    []string
	fields   []components.CardField
	// chip is the containment state folded into the title rail;
	// uncontained promotes ⚠ UNCONTAINED there instead.
	chip        string
	uncontained bool
	// safe names the safe answer in words, for the cards where the keys do
	// not make it obvious.
	safe string
	// footnote says why a key that could be offered is not.
	footnote string
	// reversibility rides the edit card's stats line, where it costs no row
	// the diff would otherwise have had.
	reversibility string
}

// cardContainment is the containment one card reports: whether the action is
// the assistant's — /run is never contained and never claims to be — and the
// mechanism in force on the path that will actually run it. The two command
// paths are wired from one policy but are not the same code, so a card names
// the one it is about rather than the one it is beside.
type cardContainment struct {
	assistant bool
	mechanism string
}

// resolveRadius builds the block for the approval about to be shown. A nil
// request is /run — the user's own command, uncontained by design.
func (m Model) resolveRadius(req *approvalRequest) blastRadius {
	if req == nil {
		return m.commandRadius(m.pendingRun, cardContainment{})
	}
	switch req.kind {
	case approvalExec:
		return m.commandRadius(req.command, cardContainment{assistant: true, mechanism: m.containment.Mechanism})
	case approvalDiff:
		return m.editRadius(req)
	case approvalMemory:
		return blastRadius{}
	}
	return m.genericRadius(req)
}

// commandRadius resolves a shell command: what it writes, whether git could
// put those paths back, and what the containment profile allows it to reach.
// The containment it is handed distinguishes the agent's commands, which run
// contained, from /run, which is the user's own and never is.
func (m Model) commandRadius(command string, contain cardContainment) blastRadius {
	res := radius.Resolve(m.workspace, command)
	b := blastRadius{severity: severityOf(res.Level), risks: res.Risks}

	value, detail := res.Touches()
	b.fields = append(b.fields, components.CardField{
		Label: "touches", Value: value, Detail: detail, Tone: touchTone(res),
	})
	b.fields = append(b.fields, m.undoField(res))
	if f, ok := scopeField(m.pendingScope); ok {
		b.fields = append(b.fields, f)
		if b.severity < components.SeverityMedium {
			b.severity = components.SeverityMedium
		}
	}
	if contain.assistant {
		b.chip, b.uncontained = m.containmentChip(contain.mechanism)
		b.fields = append(b.fields, m.networkField(contain.mechanism))
	}
	if len(res.Risks) > 0 {
		b.footnote = "[a] always — not offered: a safety-flagged command is never pre-approved"
	}
	if b.severity == components.SeverityHigh || b.uncontained {
		// [n], not esc: on a gated card esc hands the keyboard back and
		// leaves the decision waiting rather than answering it.
		b.safe = "[n] deny — the safe answer"
	}
	if b.uncontained {
		b.fields = append(b.fields, components.CardField{
			Label: "⛨", Value: "no sandbox", Detail: uncontainedDetail(m.containment.Detail),
			Tone: components.ToneRisk,
		})
		b.footnote = "containment is off for this session · /sandbox doctor explains why"
	}
	return b
}

// undoField is the reversibility line for a command. shhh cannot undo a
// command — it records file edits, not processes — so the honest answer is
// what git could do about the paths it resolved, and "unknown" whenever the
// paths themselves are.
func (m Model) undoField(res radius.Command) components.CardField {
	f := components.CardField{Label: "undo"}
	switch {
	case len(res.Writes) == 0 && len(res.Unresolved) > 0:
		f.Value, f.Detail = "unknown", "shhh could not resolve what this writes"
		f.Tone = components.ToneRisk
		return f
	case len(res.Writes) == 0:
		f.Value, f.Detail = "n/a", "no workspace file is modified"
		f.Tone = components.ToneSafe
		return f
	}
	tracked, untracked := 0, 0
	for _, w := range res.Writes {
		switch m.tracker.Track(w.Path) {
		case changeset.TrackTracked:
			tracked++
		case changeset.TrackUntracked:
			untracked++
		}
	}
	switch {
	case !m.tracker.Repo():
		f.Value, f.Detail = "none", "this is not a git work tree and shhh does not record commands"
		f.Tone = components.ToneRisk
	case untracked == 0 && tracked == len(res.Writes):
		f.Value, f.Detail = "git", "every path it writes is tracked, so git can restore them"
		f.Tone = components.ToneSafe
	case tracked == 0:
		f.Value, f.Detail = "none", "nothing it writes is tracked in git"
		f.Tone = components.ToneRisk
	default:
		f.Value = "partial"
		f.Detail = fmt.Sprintf("%d of %d paths tracked in git", tracked, len(res.Writes))
		f.Tone = components.ToneNeutral
	}
	if len(res.Unresolved) > 0 && f.Value != "unknown" {
		f.Value += ", plus unknown"
		f.Tone = components.ToneRisk
	}
	return f
}

// networkField reports what the containment profile allows, not what the
// command appears to want: the profile is the thing actually in force.
func (m Model) networkField(mechanism string) components.CardField {
	f := components.CardField{Label: "network"}
	switch {
	case mechanism == "":
		f.Value, f.Detail = "open", "nothing contains this command, so nothing limits what it reaches"
		f.Tone = components.ToneOpen
	case m.containment.Network:
		f.Value, f.Detail = "open", "the "+m.containment.Profile+" profile allows network access"
		f.Tone = components.ToneOpen
	default:
		f.Value, f.Detail = "closed", "the "+m.containment.Profile+" profile removes it"
		f.Tone = components.ToneSafe
	}
	return f
}

// containmentChip folds the containment state into the title rail. An
// uncontained session gets the promoted ⚠ UNCONTAINED chip instead, which the
// card colours the border to match.
func (m Model) containmentChip(mechanism string) (chip string, uncontained bool) {
	if m.containment.Status == "" {
		// /run and sessions with no containment wiring say nothing rather
		// than claiming either state.
		return "", false
	}
	if mechanism == "" {
		return "", true
	}
	return "⛨ " + m.containmentWords(mechanism), false
}

// uncontainedDetail explains the missing mechanism, falling back to a plain
// statement when the detector had nothing to say.
func uncontainedDetail(detail string) string {
	if strings.TrimSpace(detail) == "" {
		return "no containment mechanism is available; the command runs as you"
	}
	return detail + "; the command runs as you"
}

// editRadius is the block for a file edit. An edit needs no `touches` row —
// the diff below is the blast radius, in full, which is more than a path and
// a byte count would say — so the only fact left to add is reversibility, and
// it rides the stats line the card already draws rather than costing a row
// the diff would otherwise have had.
//
// This is the one action shhh can genuinely take back: the changeset store
// records the file on both sides of the call, so undo restores it
// whether or not git ever knew about it.
func (m Model) editRadius(req *approvalRequest) blastRadius {
	b := blastRadius{severity: components.SeverityMedium}
	// An edit outside the working scope is the one thing an edit card cannot
	// say with a diff: the diff shows what changes, not that it changes
	// something the session was never scoped to.
	if f, ok := scopeField(m.pendingScope); ok {
		b.fields = append(b.fields, f)
		if m.pendingScope.class != scope.Ordinary {
			b.severity = components.SeverityHigh
			b.safe = "[n] deny — the safe answer"
		}
	}
	switch {
	case m.changes == nil:
		b.reversibility = "undo none — this session records no changeset"
	case m.tracker.Track(req.path) == changeset.TrackTracked:
		b.reversibility = "undo yes — recorded, and git has this file"
	case m.tracker.Repo():
		b.reversibility = "undo yes — recorded; git does not have this file yet"
	default:
		b.reversibility = "undo yes — recorded, and it needs no git to restore"
	}
	return b
}

// genericRadius is the block for a tool that is neither a command nor an
// edit. A tool that described its own radius (GatedPreview.Fields) carries
// that; a generic approval carrying a command — a process start — is
// resolved as the command it is.
func (m Model) genericRadius(req *approvalRequest) blastRadius {
	if req.command != "" {
		return m.commandRadius(req.command, cardContainment{
			assistant: true, mechanism: m.processContainment(),
		})
	}
	b := blastRadius{severity: components.SeverityLow}
	for _, f := range req.fields {
		tone := components.ToneNeutral
		if f.Open {
			tone = components.ToneOpen
			b.severity = components.SeverityMedium
		}
		b.fields = append(b.fields, components.CardField{
			Label: f.Label, Value: f.Value, Detail: f.Detail, Tone: tone,
		})
	}
	return b
}

// processContainment is the mechanism a process start would run under. The
// supervisor answers rather than the runner: a start the supervisor is not
// wrapping must not draw a card naming the mechanism the ordinary command
// path uses, which is the whole of what "what is reported is what is in
// force" means here.
// See docs/capabilities/containment.md#a-started-process-is-contained-too.
func (m Model) processContainment() string {
	if m.processes.Contained == nil {
		return ""
	}
	return m.processes.Contained()
}

// scopeField is the `scope` row: which directory the action reaches that the
// session was not scoped to, and what answering yes would do about it. It is
// the row that turns "this edit is somewhere else" from something the reader
// has to notice in a path into something the card states.
func scopeField(reach scopeReach) (components.CardField, bool) {
	if !reach.any() {
		return components.CardField{}, false
	}
	value := displayDir(reach.first())
	if n := len(reach.dirs) - 1; n > 0 {
		value += fmt.Sprintf(" and %d more", n)
	}
	f := components.CardField{Label: "scope", Value: value, Tone: components.ToneOpen}
	switch reach.class {
	case scope.Refused:
		f.Value = "refused — " + value
		f.Detail = reach.reason
		f.Tone = components.ToneRisk
	case scope.Sensitive:
		f.Detail = "outside the working scope, and sensitive — " + reach.reason + "; approving adds it for this session"
		f.Tone = components.ToneRisk
	default:
		f.Detail = "outside the working scope; approving adds it for this session"
	}
	return f, true
}

// severityOf maps the resolver's level onto the card's.
func severityOf(l radius.Level) components.Severity {
	switch l {
	case radius.High:
		return components.SeverityHigh
	case radius.Medium:
		return components.SeverityMedium
	}
	return components.SeverityLow
}

// touchTone colours the touches value: a resolved write is a fact, an
// unresolved command is the one that should give the reader pause.
func touchTone(res radius.Command) components.FieldTone {
	switch {
	case len(res.Writes) == 0 && len(res.Unresolved) > 0:
		return components.ToneRisk
	case len(res.Writes) == 0:
		return components.ToneSafe
	}
	return components.ToneNeutral
}
