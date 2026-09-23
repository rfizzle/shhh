package chat

// Public progress has two halves, and they are checked apart because they can
// fail apart. The schedule is when a note is owed and what is asked for; the
// presentation is the rung the answer is drawn at and the bound it is folded
// to. A change to the second must be visible only on the screen — the same
// requests, the same rounds, the same conversation — which is what the middle
// test here is for.

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// checkpointNote is a status written to the template the request states:
// objective, evidence, next action. It is deliberately longer than a step
// title, which is the note the story is about — a short one is drawn as the
// step's own header and is one row already.
const checkpointNote = "The objective is the round accounting: where the tool-round counter moves " +
	"and who reads the ceiling. The evidence is that the counter moves in the agent and the " +
	"ceiling is the session's, so a raise for one turn never reaches the agent. Next I will " +
	"read the pause row and the offer beside it."

// progressModel is a session whose public-status clocks are short enough for
// a test to reach: two silent calls, and a wall clock nothing waits on.
func progressModel(t *testing.T, stream StreamFunc) Model {
	t.Helper()
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, stream).
		WithProgressIntervals(2, time.Hour)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m = updated.(Model)
	updated, _ = m.sendUserMessage("trace the checkpoint")
	m = updated.(Model)
	m.setTurnState(stateStreaming)
	return m
}

// The request is the agent's to schedule and this is the surface's half of
// it: it joins the conversation as a machine message at a round boundary,
// costs no round, and writes no row — the reader is owed the answer, not the
// question the session asked on their behalf.
func TestProgress_TheRequestJoinsTheConversationAtARoundBoundary(t *testing.T) {
	m := progressModel(t, mockStream)
	m = advanceRounds(m, 2)

	rounds, entries := m.agent.Rounds(), len(m.transcript)
	m.injectProgressCheckpoint()

	if got := lastUserMessage(m); got != agent.ProgressPrompt {
		t.Errorf("the conversation should carry the public-status request, got:\n%s", got)
	}
	for _, want := range []string{"objective", "evidence", "next action"} {
		if !strings.Contains(lastUserMessage(m), want) {
			t.Errorf("the request no longer asks for %q:\n%s", want, lastUserMessage(m))
		}
	}
	if m.agent.Rounds() != rounds {
		t.Errorf("asking for public status moved the round counter: %d → %d", rounds, m.agent.Rounds())
	}
	if len(m.transcript) != entries {
		t.Errorf("the request wrote %d rows; it is a question the reader never asked",
			len(m.transcript)-entries)
	}
}

// An overlay owns the screen, so a request made behind one would be answered
// into a transcript nobody is reading.
func TestProgress_ASurfaceDefersTheRequest(t *testing.T) {
	m := progressModel(t, mockStream)
	m = advanceRounds(m, 2)
	m.state = statePick

	m.injectProgressCheckpoint()

	if lastUserMessage(m) == agent.ProgressPrompt {
		t.Error("the request reached the conversation from behind a surface")
	}
}

// roundOutcome is what one tool round cost the turn: the requests it sent,
// the rounds it moved and the rows it left.
type roundOutcome struct {
	requests, rounds, entries int
}

// runProgressRound drives one round that opens with prose — with the public
// status outstanding, or without — and reports what the round cost.
func runProgressRound(t *testing.T, pending bool) (roundOutcome, entry) {
	t.Helper()
	requests := 0
	m := progressModel(t, StreamFunc(func(msgs []provider.Message, choice string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		requests++
		return mockStream(msgs, choice)
	}))
	m = advanceRounds(m, 2)
	if pending {
		m.injectProgressCheckpoint()
		if !m.agent.ProgressPending() {
			t.Fatal("the round was set up with a checkpoint owed and none is")
		}
	}

	before := roundOutcome{requests, m.agent.Rounds(), len(m.transcript)}
	m.streaming = checkpointNote
	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call-1", Name: "read_file", Arguments: `{"path":"internal/agent/round.go"}`},
	}})
	m = updated.(Model)
	// The batch runs, its results come back, and the round resumes — which is
	// the one request a round of tool calls earns.
	for _, c := range unwrapBatch(cmd) {
		results, ok := c().(toolResultsMsg)
		if !ok {
			continue
		}
		updated, cmd = m.Update(results)
		m = updated.(Model)
		for _, c := range unwrapBatch(cmd) {
			c()
		}
	}

	var note entry
	for _, e := range m.transcript {
		if e.kind == entryAssistant {
			note = e
		}
	}
	return roundOutcome{
		requests - before.requests,
		m.agent.Rounds() - before.rounds,
		len(m.transcript) - before.entries,
	}, note
}

// Drawing the note differently is a render and nothing else. The round that
// answers a checkpoint asks the provider for exactly what an ordinary round
// asks it for, moves the counter by the same one, and leaves the same rows:
// the only difference is on the entry, where the transcript reads it.
func TestProgress_AnsweredStatusCostsNoRequestAndNoRound(t *testing.T) {
	answered, note := runProgressRound(t, true)
	ordinary, plain := runProgressRound(t, false)

	if answered != ordinary {
		t.Errorf("a round that answered a checkpoint cost %+v; an ordinary one cost %+v", answered, ordinary)
	}
	if answered.rounds != 1 {
		t.Errorf("the round moved the counter by %d, not 1", answered.rounds)
	}
	if !note.checkpoint {
		t.Error("the prose that answered the request is not marked as the status it is")
	}
	if plain.checkpoint {
		t.Error("prose nobody asked for was marked as public status")
	}
	if note.text != checkpointNote {
		t.Errorf("the note's own words were rewritten: %q", note.text)
	}
}

// A reply that answers the request and then asks for nothing has stopped
// working, so it is the turn's answer and is drawn as one. The request is
// made at a round boundary and asks for status on work still in flight;
// reporting and concluding are different acts, and the rung is what tells
// them apart (docs/interface/surfaces.md#the-progress-checkpoint).
func TestProgress_AConcludingReplyIsTheAnswerAndNotANote(t *testing.T) {
	m := progressModel(t, mockStream)
	m = advanceRounds(m, 2)
	m.injectProgressCheckpoint()
	if !m.agent.ProgressPending() {
		t.Fatal("the turn was set up with a checkpoint owed and none is")
	}

	m.streaming = checkpointNote
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)

	var last entry
	for _, e := range m.transcript {
		if e.kind == entryAssistant {
			last = e
		}
	}
	if last.text != checkpointNote {
		t.Fatalf("the reply never reached the transcript: %q", last.text)
	}
	if last.checkpoint {
		t.Error("a reply that ended the turn was drawn as a note about work still in flight")
	}
	// It still settles the request: the person was written to, and the run
	// that was asked to speak did.
	if m.agent.ProgressPending() {
		t.Error("the answer left the request outstanding, so the next round would ask again")
	}
	// And the conversation records it the same way, so reopening the session
	// cannot promote or demote it either.
	msgs := m.agent.Messages()
	for _, msg := range msgs {
		if msg.Role == provider.RoleAssistant && msg.Checkpoint {
			t.Errorf("the stored answer is marked as public status: %q", msg.Content)
		}
	}
}

// The mark is on the conversation, not on the frame, so a session reopened
// from the store draws the notes it was written with — and retires all but
// the last of them the way the live transcript did. Left to the frame alone,
// every note in a run's history would come back promoted to an answer.
func TestProgress_TheMarkSurvivesAReopenedConversation(t *testing.T) {
	m := progressModel(t, mockStream)
	m = advanceRounds(m, 2)
	m.injectProgressCheckpoint()
	m.streaming = checkpointNote
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call-1", Name: "read_file", Arguments: `{"path":"internal/agent/round.go"}`},
	}})
	m = updated.(Model)

	msgs := m.agent.Messages()
	var marked int
	for _, msg := range msgs {
		if msg.Checkpoint {
			marked++
			if msg.Content != checkpointNote {
				t.Errorf("the wrong message carries the mark: %q", msg.Content)
			}
		}
	}
	if marked != 1 {
		t.Fatalf("the conversation carries %d marked messages, want 1", marked)
	}

	// The same conversation with a second note in it, rebuilt from the
	// messages alone the way every path back to a stored conversation does.
	msgs = append(msgs,
		provider.Message{Role: provider.RoleTool, ToolCallID: "call-1", Content: "lines"},
		provider.Message{Role: provider.RoleAssistant, Content: checkpointNote, Checkpoint: true,
			ToolCalls: []provider.ToolCall{{ID: "call-2", Name: "read_file"}}},
		provider.Message{Role: provider.RoleAssistant, Content: "The ceiling is the session's."})

	next := frameModel(t, 110, 40)
	next.loadConversation(msgs)

	var notes, answers []entry
	for _, e := range next.transcript {
		if e.kind != entryAssistant {
			continue
		}
		if e.checkpoint {
			notes = append(notes, e)
		} else {
			answers = append(answers, e)
		}
	}
	if len(notes) != 2 {
		t.Fatalf("the reopened transcript has %d notes, want 2", len(notes))
	}
	if len(answers) != 1 || answers[0].text != "The ceiling is the session's." {
		t.Fatalf("the reopened transcript has the wrong answers: %+v", answers)
	}
	if !notes[0].checkpointReplaced {
		t.Error("the reopened transcript did not retire the note the later one replaced")
	}
	if notes[1].checkpointReplaced {
		t.Error("the reopened transcript retired the note nothing has replaced")
	}
}

// The request is the session's question, and live it draws no row: the note
// that answers it is the row. A reopened transcript is rebuilt from the
// messages, so it has to leave the request out the same way — drawn, it would
// sit above the note as a prompt the reader never saw asked. A machine message
// that did have a row live keeps one, which is the other half of the rule.
func TestProgress_AReopenedTranscriptDrawsNoRowForTheRequest(t *testing.T) {
	const notice = "The tree moved under the session."
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "system"},
		{Role: provider.RoleUser, Content: "trace the checkpoint"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "read_file"}}},
		{Role: provider.RoleTool, ToolCallID: "call-1", Content: "lines"},
		{Role: provider.RoleUser, Content: agent.ProgressPrompt, Machine: true},
		{Role: provider.RoleAssistant, Content: checkpointNote, Checkpoint: true,
			ToolCalls: []provider.ToolCall{{ID: "call-2", Name: "read_file"}}},
		{Role: provider.RoleTool, ToolCallID: "call-2", Content: "lines"},
		{Role: provider.RoleUser, Content: notice, Machine: true},
		{Role: provider.RoleAssistant, Content: "The ceiling is the session's."},
	}

	m := frameModel(t, 110, 40)
	m.loadConversation(msgs)

	note := -1
	var systems []string
	for i, e := range m.transcript {
		if e.kind == entryAssistant && e.checkpoint {
			note = i
		}
		if e.kind == entrySystem {
			systems = append(systems, e.text)
			if note < 0 {
				t.Errorf("a system row is drawn above the note: %q", e.text)
			}
		}
	}
	if note < 0 {
		t.Fatal("the reopened transcript lost the note")
	}
	if len(systems) != 1 || systems[0] != notice {
		t.Fatalf("the reopened transcript's system rows are %q, want only the tree notice", systems)
	}
	// The request is still in the conversation — the row is what is left
	// out, not the message — and the rewind list is read off the messages,
	// so the reader's one turn is still the one checkpoint at its own index.
	if got := m.agent.Messages()[4].Content; got != agent.ProgressPrompt {
		t.Errorf("the request left the conversation: %q", got)
	}
	if len(m.checkpoints) != 1 || m.checkpoints[0].index != 1 {
		t.Errorf("rewind checkpoints = %+v, want the one turn at index 1", m.checkpoints)
	}
}

// A checkpoint round can be the one the wire drops, and continuing it is the
// one path that turns partial prose into a round without the round having
// ended. The note has to keep its mark across that — and the request has to
// be settled by it, or the next round's ordinary prose would be marked as the
// status this one already wrote.
func TestProgress_ContinuingADroppedNoteKeepsItsMark(t *testing.T) {
	m := progressModel(t, mockStream)
	m = advanceRounds(m, 2)
	m.injectProgressCheckpoint()

	// The stream broke after the note and the call it was leading were both
	// written, which is what the drop row offers to continue from.
	m.streaming = checkpointNote
	updated, _ := m.Update(streamErrMsg{
		err:   &provider.Failure{Class: provider.ClassNetwork},
		calls: []provider.ToolCall{{ID: "call-1", Name: "read_file", Arguments: `{"path":"round.go"}`}},
	})
	m = updated.(Model)

	var drop entry
	for _, e := range m.transcript {
		if e.kind == entryStreamDrop {
			drop = e
		}
	}
	if drop.resume == nil {
		t.Fatalf("the drop left nothing to continue from: %+v", kindsOf(m.transcript))
	}
	updated, _ = m.continueStream(drop.resume)
	m = updated.(Model)

	var note entry
	for _, e := range m.transcript {
		if e.kind == entryAssistant {
			note = e
		}
	}
	if !note.checkpoint {
		t.Error("the note the drop kept came back as an ordinary answer")
	}
	var marked []string
	for _, msg := range m.agent.Messages() {
		if msg.Checkpoint {
			marked = append(marked, msg.Content)
		}
	}
	if len(marked) != 1 || marked[0] != checkpointNote {
		t.Errorf("the continued conversation marks %q as public status, want just the note", marked)
	}
	// And the request is settled, so the round the reader continues into
	// writes ordinary prose and is read as ordinary prose.
	if m.agent.ProgressPending() {
		t.Error("continuing the note left the request outstanding, so a later round would claim it")
	}
}

// ansiCodes is every escape a render sets, as the payloads themselves. It is
// how a block is read for the rung it was drawn at without asserting about
// one palette's numbers.
func ansiCodes(s string) map[string]bool {
	out := map[string]bool{}
	for _, m := range regexp.MustCompile("\x1b\\[([0-9;]*)m").FindAllStringSubmatch(s, -1) {
		if m[1] != "" && m[1] != "0" {
			out[m[1]] = true
		}
	}
	return out
}

func ansi256(t *testing.T) {
	t.Helper()
	was := components.Profile()
	components.SetProfile(colorprofile.ANSI256)
	t.Cleanup(func() { components.SetProfile(was) })
}

// The note is drawn in the tones a quoted body is drawn in and in no others,
// while the work it is about keeps every tone the grid gives it. A status
// note that arrived in the accent, the spinner's colour or a heading's weight
// would be wayfinding drawn heavier than the road.
func TestProgress_TheNoteNeverOutranksTheWork(t *testing.T) {
	ansi256(t)
	m := frameModel(t, 110, 40)

	quiet := ansiCodes(sty.Checkpoint.Render("x") + sty.SystemMsg.Render("x"))
	block := m.renderEntry(entry{kind: entryAssistant, text: checkpointNote, checkpoint: true}, 110)
	for code := range ansiCodes(block) {
		if !quiet[code] {
			t.Errorf("the note is drawn with %q, which is not a tone a quoted body wears:\n%s", code, block)
		}
	}

	// The same words as an answer, which is the rung it is being held under.
	answer := m.renderEntry(entry{kind: entryAssistant, text: checkpointNote}, 110)
	if ansiCodes(answer)[""] {
		t.Fatal("the answer render carries no colour to compare against")
	}
	var louder bool
	for code := range ansiCodes(answer) {
		louder = louder || !quiet[code]
	}
	if !louder {
		t.Error("an answer and a status note are drawn in the same tones; the note outranks nothing and is distinguished by nothing")
	}

	// And the live command it sits beside keeps its own.
	live := m.renderEntry(entry{kind: entryCommand, toolName: "execute_command",
		toolArgs: `{"command":"go test ./internal/agent"}`}, 110)
	var keeps bool
	for code := range ansiCodes(live) {
		keeps = keeps || !quiet[code]
	}
	if !keeps {
		t.Error("a command row beside the note is drawn in nothing the note is not")
	}
}

// Quieter has to stay readable, and readable is a ratio rather than a taste.
// Every shipped table is asked the same two questions: the note clears the
// floor a body of text clears against that table's own ground, and it clears
// it by less than an answer does.
func TestProgress_TheRungIsReadableInEveryPalette(t *testing.T) {
	themeRestore(t)
	components.SetMono(false)
	// Truecolor, so a token resolves to the hex the table wrote down rather
	// than to an index a ratio cannot be computed from.
	wasProfile := components.Profile()
	components.SetProfile(colorprofile.TrueColor)
	t.Cleanup(func() { components.SetProfile(wasProfile) })
	// The ground a table was chosen against is the thing a ratio is computed
	// from; painting it is how the test gets to read it, and is off again
	// before the next test draws anything.
	was := components.GroundPainted()
	components.PaintGround(true)
	t.Cleanup(func() { components.PaintGround(was) })
	for _, name := range []string{components.ThemeDark, components.ThemeLight, components.ThemeCharm} {
		if err := components.SetTheme(name); err != nil {
			t.Fatal(err)
		}
		ground := components.GroundColor()
		if ground == nil {
			t.Fatalf("%s: the table has no ground to be read against", name)
		}
		note := sty.Checkpoint.GetForeground()
		if note != components.Palette.Dimmer.Color() {
			t.Errorf("%s: the note is drawn in %v, not the table's quoted-body tone", name, note)
		}
		if sty.Checkpoint.GetBold() {
			t.Errorf("%s: the note is bold, which is a heading's weight", name)
		}
		if !sty.Checkpoint.GetItalic() {
			t.Errorf("%s: the note is upright; the slant is what says these are the model's words", name)
		}
		if got := contrast(t, note, ground); got < 3 {
			t.Errorf("%s: the note is %.2f:1 against the ground and has to be at least 3:1", name, got)
		}
		body := contrast(t, components.Palette.Body.Color(), ground)
		if got := contrast(t, note, ground); got >= body {
			t.Errorf("%s: the note is %.2f:1 and an answer is %.2f:1; the note is not the quieter of the two",
				name, got, body)
		}
	}
}

// The note is bounded and says what the bound held back, and the key that
// opens every other body in the transcript gives it back.
func TestProgress_TheNoteIsBoundedAndCountsWhatItFolded(t *testing.T) {
	m := frameModel(t, 60, 40)
	e := entry{kind: entryAssistant, text: checkpointNote, checkpoint: true}

	whole := m.checkpointLines(e.text, 60)
	if len(whole) <= checkpointBound {
		t.Fatalf("the fixture is %d lines at this width and the bound is %d; it proves nothing",
			len(whole), checkpointBound)
	}
	lines := strings.Split(strings.TrimRight(stripANSI(m.renderEntry(e, 60)), "\n"), "\n")
	if len(lines) != checkpointBound+1 {
		t.Fatalf("a bounded note is %d lines and its foot; got %d:\n%s",
			checkpointBound, len(lines), strings.Join(lines, "\n"))
	}
	if want := "… " + plural(len(whole)-checkpointBound, "more line"); !strings.Contains(lines[len(lines)-1], want) {
		t.Errorf("the foot should count what it folded (%q), got %q", want, lines[len(lines)-1])
	}
	if !expandable(e) {
		t.Error("a bounded note offers no way back; the fold is a hide")
	}

	e.expanded = true
	opened := strings.Split(strings.TrimRight(stripANSI(m.renderEntry(e, 60)), "\n"), "\n")
	if len(opened) != len(whole) {
		t.Fatalf("opening the note gives back %d of its %d lines", len(opened), len(whole))
	}
	if strings.Contains(opened[len(opened)-1], "more line") {
		t.Error("an opened note still says it is holding something back")
	}
}

// A run earns three notes on one objective before it earns a second
// objective, so the one that is current is the last one written and the ones
// before it keep the sentence that says where the run was.
func TestProgress_ALaterNoteRetiresTheOneBeforeIt(t *testing.T) {
	m := frameModel(t, 60, 40)
	m.appendEntry(m.markCheckpoint(entry{kind: entryAssistant, text: checkpointNote}))
	m.appendEntry(entry{kind: entryTool, toolName: "read_file", toolResult: "ok"})
	m.appendEntry(m.markCheckpoint(entry{kind: entryAssistant, text: checkpointNote}))

	first, last := m.transcript[0], m.transcript[2]
	if !first.checkpointReplaced {
		t.Error("the first note was not retired by the one that replaced it")
	}
	if last.checkpointReplaced {
		t.Error("the newest note was retired by nothing")
	}
	retired := strings.Split(strings.TrimRight(stripANSI(m.renderEntry(first, 60)), "\n"), "\n")
	if len(retired) != checkpointReplacedBound+1 {
		t.Errorf("a retired note is its first line and its foot; got %d lines:\n%s",
			len(retired), strings.Join(retired, "\n"))
	}
	current := strings.Split(strings.TrimRight(stripANSI(m.renderEntry(last, 60)), "\n"), "\n")
	if len(current) <= len(retired) {
		t.Errorf("the current note is drawn to %d lines and the retired one to %d",
			len(current), len(retired))
	}
}
