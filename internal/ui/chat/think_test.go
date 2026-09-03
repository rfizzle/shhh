package chat

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
)

// thinkingStream is a provider that thinks out loud, then answers, then asks
// for a tool — the order every reasoning model streams in.
func thinkingStream(think, answer string, calls []provider.ToolCall) StreamFunc {
	return func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		ch := make(chan provider.StreamEvent, 3)
		ch <- provider.StreamEvent{Thinking: think}
		ch <- provider.StreamEvent{Token: answer}
		ch <- provider.StreamEvent{
			ToolCalls: calls,
			Reasoning: []provider.ReasoningBlock{{Text: think, Signature: "sig"}},
			Done:      true,
		}
		close(ch)
		_, cancel := context.WithCancel(context.Background())
		return ch, cancel, nil
	}
}

// streamingModel is a ready model with a round in flight, so the messages a
// stream produces are handled the way they are during a turn.
func streamingModel(t *testing.T) Model {
	t.Helper()
	m := readyModel(t)
	m.setTurnState(stateStreaming)
	return m
}

// kindsOf names the transcript's entry kinds, which is what the order
// assertions read.
func kindsOf(es []entry) []entryKind {
	out := make([]entryKind, 0, len(es))
	for _, e := range es {
		out = append(out, e.kind)
	}
	return out
}

// TestThinkRow_ComesBeforeTheRoundsWork is the placement the row exists for:
// the model thought, then announced, then called a tool, and the transcript
// says so in that order.
func TestThinkRow_ComesBeforeTheRoundsWork(t *testing.T) {
	m := streamingModel(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})

	updated, _ := m.Update(tokenMsg{think: "Weighing two approaches.\nThe second is cheaper."})
	m = updated.(Model)
	updated, _ = m.Update(tokenMsg{text: "Reading the loop"})
	m = updated.(Model)
	updated, _ = m.Update(toolCallsMsg{
		calls:     []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"loop.go"}`}},
		reasoning: []provider.ReasoningBlock{{Text: "Weighing two approaches.", Signature: "sig"}},
	})
	m = updated.(Model)
	// The result lands the way the tool loop lands it.
	m.appendEntry(entry{kind: entryTool, toolName: "read_file",
		toolArgs: `{"path":"loop.go"}`, toolResult: "package agent"})

	want := []entryKind{entryThink, entryAssistant, entryTool}
	if got := kindsOf(m.transcript); len(got) != len(want) {
		t.Fatalf("transcript should be think, announcement, call; got %v", got)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("transcript order %v, want %v", got, want)
			}
		}
	}

	view := stripANSI(m.renderHistory())
	think, read := strings.Index(view, "think"), strings.Index(view, "read")
	if think < 0 || read < 0 {
		t.Fatalf("both rows should be on screen:\n%s", view)
	}
	if think > read {
		t.Fatalf("the think row belongs above the round's calls:\n%s", view)
	}
	// One round, one row: the terminal event carried the same reasoning the
	// deltas did and must not say it twice.
	if n := strings.Count(view, "✻"); n != 1 {
		t.Fatalf("a round produces exactly one think row, got %d:\n%s", n, view)
	}
}

// TestThinkRow_OnlyWhereThereIsReasoning: a provider that returns none
// produces no row rather than a fabricated `0 lines`
// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
func TestThinkRow_OnlyWhereThereIsReasoning(t *testing.T) {
	m := streamingModel(t)

	updated, _ := m.Update(tokenMsg{text: "Straight to the answer."})
	m = updated.(Model)
	updated, _ = m.Update(toolCallsMsg{
		calls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"a.go"}`}},
	})
	m = updated.(Model)

	for _, e := range m.transcript {
		if e.kind == entryThink {
			t.Fatal("a round that did not think should produce no think row")
		}
	}
	if view := stripANSI(m.renderHistory()); strings.Contains(view, "✻") {
		t.Fatalf("nothing on screen should claim thinking:\n%s", view)
	}

	// A block the provider redacted has no readable half either, and an empty
	// row would be a row about nothing.
	m.thinkIdx = 0
	m.recordReasoning([]provider.ReasoningBlock{{Redacted: "opaque"}})
	for _, e := range m.transcript {
		if e.kind == entryThink {
			t.Fatal("a redacted block carries no words and so carries no row")
		}
	}
}

// TestThinkRow_StreamsOnTheTick: the count grows as the block arrives, and
// the repaint rides the one tick rather than the chunk (spin.go).
func TestThinkRow_StreamsOnTheTick(t *testing.T) {
	m := streamingModel(t)

	updated, _ := m.Update(tokenMsg{think: "first line\n"})
	m = updated.(Model)
	if view := stripANSI(m.renderHistory()); !strings.Contains(view, "1 line") {
		t.Fatalf("the row should count what has arrived:\n%s", view)
	}
	if !m.transcript[0].thinkStreaming {
		t.Fatal("a row still being written is still running")
	}

	m.spinning = true
	updated, _ = m.Update(tokenMsg{think: "second line\nthird line"})
	m = updated.(Model)
	if !m.streamDirty {
		t.Fatal("a chunk that lands while the chain runs owes a repaint, it does not take one")
	}
	if got := len(m.transcript); got != 1 {
		t.Fatalf("every chunk of a round lands on one row, got %d entries", got)
	}
	if view := stripANSI(m.renderHistory()); !strings.Contains(view, "3 lines") {
		t.Fatalf("the count should have grown with the block:\n%s", view)
	}

	// The answer starting is what settles the row: a model that is writing
	// has stopped thinking.
	updated, _ = m.Update(tokenMsg{text: "Here is the plan."})
	m = updated.(Model)
	if m.transcript[0].thinkStreaming {
		t.Fatal("the first answer token settles the think row")
	}
	if view := stripANSI(m.renderHistory()); strings.Contains(view, "running…") {
		t.Fatalf("a settled row reports no outcome of its own:\n%s", view)
	}
}

// TestThinkRow_NewRoundNewRow: reasoning belongs to the round that produced
// it, so the next request's thinking does not extend the last one's row.
func TestThinkRow_NewRoundNewRow(t *testing.T) {
	m := streamingModel(t)
	updated, _ := m.Update(tokenMsg{think: "round one"})
	m = updated.(Model)

	events := make(chan provider.StreamEvent)
	close(events)
	updated, _ = m.Update(streamStartedMsg{events: events})
	m = updated.(Model)
	if m.transcript[0].thinkStreaming {
		t.Fatal("the previous round's row stops when the next request opens")
	}
	updated, _ = m.Update(tokenMsg{think: "round two"})
	m = updated.(Model)

	if got := len(m.transcript); got != 2 {
		t.Fatalf("two rounds of thinking are two rows, got %d", got)
	}
	if m.transcript[0].text != "round one" || m.transcript[1].text != "round two" {
		t.Fatalf("each row holds its own round's thinking: %q, %q",
			m.transcript[0].text, m.transcript[1].text)
	}
}

// TestThinkRow_NotDuringCompaction: a compaction is housekeeping, and the row
// would either be wiped with the transcript it summarised or outlive it.
func TestThinkRow_NotDuringCompaction(t *testing.T) {
	m := streamingModel(t)
	m.compacting = true

	updated, _ := m.Update(tokenMsg{think: "deciding what to keep"})
	m = updated.(Model)

	if len(m.transcript) != 0 {
		t.Fatalf("a compaction's thinking is not a transcript row: %v", kindsOf(m.transcript))
	}
}

// TestThinkRow_SurvivesARebuild: /rewind, a compaction's kept turns and a
// resumed conversation all rebuild the transcript from the messages, and the
// reasoning is still being replayed to the model — so the rows come back too.
//
// The row follows what is replayed and nothing else, which is why the second
// case has none: only a round that asked for tools keeps its blocks (the
// agent drops the latch on a round that ended in text), and a rebuilt
// transcript that drew a row there would be claiming something the request no
// longer carries.
func TestThinkRow_SurvivesARebuild(t *testing.T) {
	for _, tc := range []struct {
		name string
		msgs []provider.Message
		want []entryKind
	}{
		{"a round whose reasoning is replayed", []provider.Message{
			{Role: provider.RoleUser, Content: "make it cheaper"},
			{Role: provider.RoleAssistant, Content: "Reading the loop",
				Reasoning: []provider.ReasoningBlock{{Text: "Two ways to do this.", Signature: "sig"}}},
		}, []entryKind{entryUser, entryThink, entryAssistant}},
		{"a final answer, whose blocks were dropped", []provider.Message{
			{Role: provider.RoleUser, Content: "make it cheaper"},
			{Role: provider.RoleAssistant, Content: "Done — the loop is two calls shorter."},
		}, []entryKind{entryUser, entryAssistant}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := readyModel(t)
			m.appendMessageEntries(tc.msgs)

			got := kindsOf(m.transcript)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("rebuilt transcript %v, want %v", got, tc.want)
			}
			for _, e := range m.transcript {
				if e.kind == entryThink && e.thinkStreaming {
					t.Fatal("a rebuilt row is history, not a round in flight")
				}
			}
		})
	}
}

// thinkBlock is a reasoning block of n numbered lines.
func thinkBlock(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

// bodyLines is how many lines a rendered row spends below its own.
func bodyLines(view string) int {
	return len(strings.Split(strings.TrimRight(view, "\n"), "\n")) - 1
}

// TestThinkRow_FoldCycle: three depths for a block that needs them, two for
// one that does not — a tail window showing every line the next press would
// show is a press that changes nothing.
func TestThinkRow_FoldCycle(t *testing.T) {
	tests := []struct {
		name  string
		lines int
		want  []int // body lines after each press, ending back where it started
	}{
		{"fits in the cap", 5, []int{5, 0}},
		{"needs a window", 500, []int{maxToolResultLines, 500, 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := readyModel(t)
			m.appendEntry(entry{kind: entryThink, text: thinkBlock(tc.lines)})

			if got := bodyLines(m.renderEntry(m.transcript[0], 80)); got != 0 {
				t.Fatalf("a row starts closed, got %d body lines", got)
			}
			for i, want := range tc.want {
				m.cycleThink(0)
				if got := bodyLines(m.renderEntry(m.transcript[0], 80)); got != want {
					t.Fatalf("press %d: %d body lines, want %d", i+1, got, want)
				}
			}
		})
	}
}

// TestThinkRow_TailIsTheEnd: the middle depth keeps the end of the thought,
// which is the part the reader is looking for.
func TestThinkRow_TailIsTheEnd(t *testing.T) {
	m := readyModel(t)
	m.appendEntry(entry{kind: entryThink, text: thinkBlock(40), thinkDepth: thinkTail})

	view := stripANSI(m.renderEntry(m.transcript[0], 80))
	if !strings.Contains(view, "line 40") {
		t.Fatalf("the tail window ends on the last line thought:\n%s", view)
	}
	if strings.Contains(view, "line 1\n") {
		t.Fatalf("the tail window is a window, not the block:\n%s", view)
	}
	if !strings.Contains(view, "40 lines") {
		t.Fatalf("an opened row still states what it holds:\n%s", view)
	}
}

// TestThinkRow_WrapsRatherThanClips: reasoning arrives as prose, and a
// paragraph is one physical line hundreds of characters long. A detail body
// clips, which is right for a log line and wrong for a sentence — an opened
// row that showed one clipped line would be the fold keeping what it said it
// had (docs/interface/principles.md#fold-never-hide).
func TestThinkRow_WrapsRatherThanClips(t *testing.T) {
	m := readyModel(t)
	para := "The user wants the cheaper of the two approaches, and the second one " +
		"reuses the row the transcript already draws, so it costs one field " +
		"rather than a surface of its own, which is the whole argument."
	m.appendEntry(entry{kind: entryThink, text: para, thinkDepth: thinkFull})

	const width = 80
	view := stripANSI(m.renderEntry(m.transcript[0], width))
	if strings.Contains(view, "…") {
		t.Fatalf("an opened row wraps its prose, it does not clip it:\n%s", view)
	}
	body := strings.Split(strings.TrimRight(view, "\n"), "\n")[1:]
	if len(body) < 3 {
		t.Fatalf("a paragraph this long is several lines at %d columns:\n%s", width, view)
	}
	for _, line := range body {
		if len([]rune(line)) > width {
			t.Fatalf("a wrapped line still fits the pane: %q", line)
		}
	}
	// Every word survives the wrap, which is the thing clipping lost.
	joined := strings.Join(strings.Fields(strings.Join(body, " ")), " ")
	if joined != para {
		t.Fatalf("the body should hold the whole thought:\n%s", joined)
	}
	// And the count is of the lines the reader gets, so a closed row states
	// what opening it costs.
	m.transcript[0].thinkDepth = thinkClosed
	closed := stripANSI(m.renderEntry(m.transcript[0], width))
	if !strings.Contains(closed, lineCounts(len(body))) {
		t.Fatalf("the folded row should count the %d lines it is holding:\n%s", len(body), closed)
	}
}

// TestThinkRow_EndsTheStepAboveIt: the model stopped to think, so what
// follows belongs to the round it thought for. Left inside the step, the row
// would split the read-only run around it into two runs too short to fold and
// then vanish behind the step's own fold with nothing counting it.
func TestThinkRow_EndsTheStepAboveIt(t *testing.T) {
	m := readyModel(t)
	m.appendEntry(entry{kind: entryAssistant, text: "Reading the loop"})
	read := func(path string) entry {
		return entry{kind: entryTool, toolName: "read_file",
			toolArgs: `{"path":"` + path + `"}`, toolResult: "package agent"}
	}
	m.appendEntry(read("a.go"))
	m.appendEntry(read("b.go"))
	m.appendEntry(read("c.go"))
	m.appendEntry(entry{kind: entryThink, text: "Now for the other half."})
	m.appendEntry(read("d.go"))
	m.appendEntry(read("e.go"))
	m.appendEntry(read("f.go"))

	blocks := m.blocksOf(m.transcript)
	if len(blocks) == 0 || blocks[0].step == nil {
		t.Fatalf("the announcement should still title a step: %+v", blocks)
	}
	if got := blocks[0].step.end; got != 4 {
		t.Fatalf("the step should end at the think row (4), got %d", got)
	}
	for _, blk := range blocks[1:] {
		if blk.step != nil {
			t.Fatal("the rounds after a think row are not part of the step above it")
		}
	}
	// The step is open, so its rows are on screen: the run above the think row
	// is whole and still folds into one counted group. The run below does not
	// — it is in no step, and a step is a titled group
	// (docs/interface/surfaces.md#the-think-row). That trade is deliberate:
	// the alternative split the run in two and then hid the think row behind
	// the step's fold with nothing counting it. It is pinned here rather than
	// left to be re-argued.
	m.transcript[0].stepFold = foldOpen
	view := stripANSI(m.renderHistory())
	if !strings.Contains(view, "3 reads") {
		t.Fatalf("the run above the row is untouched and still folds:\n%s", view)
	}
	for _, path := range []string{"d.go", "e.go", "f.go"} {
		if !strings.Contains(view, path) {
			t.Fatalf("the calls after the row stand as their own rows:\n%s", view)
		}
	}
	// The row is still on screen and still reachable once the step folds.
	m.transcript[0].stepFold = foldClosed
	m.invalidateRenderCache()
	view = stripANSI(m.renderHistory())
	if !strings.Contains(view, "✻") {
		t.Fatalf("a folded step must not swallow the think row below it:\n%s", view)
	}
	if !slices.Contains(m.expandableIndices(), 4) {
		t.Fatalf("the row the fold left on screen is still selectable: %v", m.expandableIndices())
	}
}

// TestThinkRow_CycleClosesAtHighVerbosity: the depth is an override, so a
// reader outranks the verbosity rather than cycling a row they can never
// close.
func TestThinkRow_CycleClosesAtHighVerbosity(t *testing.T) {
	m := readyModel(t)
	m.verbosity = verbosityHigh
	m.appendEntry(entry{kind: entryThink, text: thinkBlock(40)})

	if got := bodyLines(m.renderEntry(m.transcript[0], 80)); got != maxToolResultLines {
		t.Fatalf("high verbosity opens the bounded body, got %d lines", got)
	}
	// [-] is not offered on a row the reader did not open, exactly as it is
	// not on a tool row high verbosity expanded.
	if m.focusIdx = 0; m.focusedRowOpen() {
		t.Fatal("a row the verbosity opened is not one [-] has anything to close")
	}
	for i, want := range []int{40, 0, maxToolResultLines} {
		m.cycleThink(0)
		if got := bodyLines(m.renderEntry(m.transcript[0], 80)); got != want {
			t.Fatalf("press %d: %d body lines, want %d", i+1, got, want)
		}
	}
}

// TestThinkRow_Verbosity: low draws no row at all and offers the reading
// cursor nothing to land on; high opens it to the bounded body, as it does
// every other row.
func TestThinkRow_Verbosity(t *testing.T) {
	m := readyModel(t)
	m.appendEntry(entry{kind: entryThink, text: thinkBlock(40)})

	m.verbosity = verbosityNormal
	if view := stripANSI(m.renderEntry(m.transcript[0], 80)); !strings.Contains(view, "think") {
		t.Fatalf("the default shows the row folded:\n%s", view)
	} else if bodyLines(view) != 0 {
		t.Fatalf("the default shows it folded, not open:\n%s", view)
	}
	if len(m.expandableIndices()) != 1 {
		t.Fatal("a drawn row is a row the reading cursor can reach")
	}

	m.verbosity = verbosityHigh
	if got := bodyLines(m.renderEntry(m.transcript[0], 80)); got != maxToolResultLines {
		t.Fatalf("high verbosity opens the bounded body, got %d lines", got)
	}

	m.verbosity = verbosityLow
	if view := m.renderEntry(m.transcript[0], 80); view != "" {
		t.Fatalf("low verbosity draws no think row at all: %q", view)
	}
	if got := len(m.expandableIndices()); got != 0 {
		t.Fatalf("a row nobody can see is a row the cursor cannot land on, got %d targets", got)
	}
}

// TestThinkRow_ReadingKeys: [enter] under the cursor cycles the depths and
// [-] closes whatever they opened, the same two keys every other row answers.
func TestThinkRow_ReadingKeys(t *testing.T) {
	m := readyModel(t)
	m.appendEntry(entry{kind: entryThink, text: thinkBlock(40)})
	next, _ := m.enterFocusMode()
	m = next.(Model)
	if m.focusIdx != 0 {
		t.Fatalf("reading mode should land on the row, got %d", m.focusIdx)
	}

	next, _ = m.updateFocus(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.transcript[0].thinkDepth != thinkTail {
		t.Fatalf("enter opens the tail window, got depth %d", m.transcript[0].thinkDepth)
	}
	if !m.focusedRowOpen() {
		t.Fatal("an opened row reports itself open, which is what puts [-] on the bar")
	}
	next, _ = m.updateFocus(tea.KeyPressMsg{Code: '-', Text: "-"})
	m = next.(Model)
	if m.transcript[0].thinkDepth != thinkClosed {
		t.Fatalf("[-] closes the row, got depth %d", m.transcript[0].thinkDepth)
	}
}

// TestThinkRow_MonoTellsItFromATool is the first invariant on this row: with
// the palette stripped to two greys the glyph and the verb still say which
// row this is (docs/interface/principles.md#colour-never-carries-meaning-alone).
func TestThinkRow_MonoTellsItFromATool(t *testing.T) {
	m := readyModel(t)
	think := stripANSI(m.renderEntry(entry{kind: entryThink, text: "a thought"}, 80))
	tool := stripANSI(m.renderEntry(entry{kind: entryTool, toolName: "read_file",
		toolArgs: `{"path":"a.go"}`, toolResult: "x"}, 80))

	if !strings.Contains(think, "✻") || !strings.Contains(think, "think") {
		t.Fatalf("the think row carries its own glyph and verb:\n%s", think)
	}
	if strings.Contains(think, "⚙") || strings.Contains(think, "▎") {
		t.Fatalf("thinking is not a tool call and changed nothing:\n%s", think)
	}
	if think == tool {
		t.Fatal("the two rows must not be distinguishable by colour alone")
	}
}

// TestThinkRow_StreamOrder drives the whole thing through the stream reader
// the session uses, so the batching and the ordering are asserted together.
func TestThinkRow_StreamOrder(t *testing.T) {
	events, cancel, err := thinkingStream("thought one\nthought two", "Answering.",
		[]provider.ToolCall{{ID: "c1", Name: "search", Arguments: `{"pattern":"x"}`}})(nil, provider.ToolChoiceAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	msg := waitForEvent(events)()
	tm, ok := msg.(tokenMsg)
	if !ok {
		t.Fatalf("expected a token message, got %T", msg)
	}
	if tm.think != "thought one\nthought two" {
		t.Fatalf("reasoning batches on its own string, got %q", tm.think)
	}
	if tm.text != "Answering." {
		t.Fatalf("the answer batches on its own, got %q", tm.text)
	}
	if _, ok := tm.final.(toolCallsMsg); !ok {
		t.Fatalf("the terminal event rides the batch, got %T", tm.final)
	}
}

// BenchmarkThinkStreaming measures what the row costs while it fills: 20k
// characters of reasoning arriving in chunks, with the row drawn for each,
// closed as it is while it streams and open as a reader watching it would
// leave it. A row that re-parsed its block every chunk would show as the
// quadratic the answer's own render was fixed for (streammd.go); what the
// repaints actually cost is bounded by the tick they ride, which
// TestThinkRow_StreamsOnTheTick is the assertion for.
func BenchmarkThinkStreaming(b *testing.B) {
	var block strings.Builder
	for i := 0; block.Len() < 20_000; i++ {
		fmt.Fprintf(&block, "line %d of the model thinking about the change, at some length\n", i)
	}
	src := block.String()
	var cuts []int
	for i := 12; i < len(src); i += 12 {
		cuts = append(cuts, i)
	}
	cuts = append(cuts, len(src))

	m := Model{verbosity: verbosityNormal}
	for _, tc := range []struct {
		name  string
		depth thinkDepth
	}{{"closed", thinkAuto}, {"tail", thinkTail}} {
		b.Run(tc.name, func(b *testing.B) {
			for b.Loop() {
				for _, c := range cuts {
					m.thinkRowFor(entry{kind: entryThink, text: src[:c], thinkDepth: tc.depth}, 80).View(80)
				}
			}
		})
	}
}
