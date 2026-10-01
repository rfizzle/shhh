package chat

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
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

// numberedThought is a reasoning block of n numbered lines.
func numberedThought(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

// TestThink_IsProseThatNeverFolds is the shape: what the model thought is
// prose at the body column on bare screen — no glyph, no band, no count and
// no fold — whole at every length, wrapped rather than clipped, and drawn
// the same whatever reading mode presses on it
// (docs/interface/surfaces.md#the-think-row).
func TestThink_IsProseThatNeverFolds(t *testing.T) {
	para := "The user wants the cheaper of the two approaches, and the second one " +
		"reuses the row the transcript already draws, so it costs one field " +
		"rather than a surface of its own, which is the whole argument."
	for _, tc := range []struct {
		name, text string
		want       []string
	}{
		{"a paragraph wraps whole", para, strings.Fields(para)},
		{"a long thought is every line of it", numberedThought(120), []string{"line 1", "line 60", "line 120"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := readyModel(t)
			m.appendEntry(entry{kind: entryThink, text: tc.text})
			const width = 80
			raw := m.renderEntry(m.transcript[0], width)
			view := stripANSI(raw)
			for _, gone := range []string{"✻", "think", " lines", "…", "▎", "[enter]"} {
				if strings.Contains(view, gone) {
					t.Fatalf("a thought carries no %q:\n%s", gone, view)
				}
			}
			for _, w := range tc.want {
				if !strings.Contains(view, w) {
					t.Fatalf("the thought should hold %q whole:\n%s", w, view)
				}
			}
			indent := strings.Repeat(" ", components.GridDetailIndent)
			for _, l := range strings.Split(strings.TrimRight(view, "\n"), "\n") {
				if !strings.HasPrefix(l, indent) || strings.HasPrefix(l, indent+" ") {
					t.Fatalf("every line starts on the body column: %q", l)
				}
				if len([]rune(l)) > width {
					t.Fatalf("a wrapped line still fits the pane: %q", l)
				}
			}
			if !strings.Contains(raw, "\x1b[3") {
				t.Fatalf("a thought is slanted, the mark of model output nobody asked for:\n%q", raw)
			}

			// Enter under the cursor has nothing to open: the passage reads
			// the same after it.
			next, _ := m.enterFocusMode()
			m = next.(Model)
			next, _ = m.updateFocus(tea.KeyPressMsg{Code: tea.KeyEnter})
			m = next.(Model)
			if got := stripANSI(m.renderEntry(m.transcript[0], width)); got != view {
				t.Fatalf("enter on a thought changed it:\n%s", got)
			}
		})
	}
}

// TestThink_ComesBeforeTheRoundsWork is the placement it exists for: the
// model thought, then announced, then called a tool, and the transcript says
// so in that order.
func TestThink_ComesBeforeTheRoundsWork(t *testing.T) {
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
	m.appendEntry(entry{kind: entryTool, toolName: "read_file",
		toolArgs: `{"path":"loop.go"}`, toolResult: "package agent"})

	want := []entryKind{entryThink, entryAssistant, entryTool}
	if got := kindsOf(m.transcript); !slices.Equal(got, want) {
		t.Fatalf("transcript order %v, want %v", got, want)
	}
	view := stripANSI(m.renderHistory())
	think, read := strings.Index(view, "Weighing two approaches."), strings.Index(view, "read")
	if think < 0 || read < 0 {
		t.Fatalf("both should be on screen:\n%s", view)
	}
	if think > read {
		t.Fatalf("the thought belongs above the round's card:\n%s", view)
	}
	// One round, one passage: the terminal event carried the same reasoning
	// the deltas did and must not say it twice.
	if n := strings.Count(view, "Weighing two approaches."); n != 1 {
		t.Fatalf("a round's thought is drawn once, got %d:\n%s", n, view)
	}
}

// TestThink_OnlyWhereThereIsReasoning: a provider that returns none produces
// no passage, and neither does a block the provider redacted.
func TestThink_OnlyWhereThereIsReasoning(t *testing.T) {
	m := streamingModel(t)

	updated, _ := m.Update(tokenMsg{text: "Straight to the answer."})
	m = updated.(Model)
	updated, _ = m.Update(toolCallsMsg{
		calls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"a.go"}`}},
	})
	m = updated.(Model)
	m.thinkIdx = 0
	m.recordReasoning([]provider.ReasoningBlock{{Redacted: "opaque"}})
	if slices.Contains(kindsOf(m.transcript), entryThink) {
		t.Fatal("a round that did not think, or whose words were taken back, has no thought")
	}
}

// TestThink_StreamsOnTheTick: the passage grows as the block arrives, and
// the repaint rides the one tick rather than the chunk (spin.go).
func TestThink_StreamsOnTheTick(t *testing.T) {
	m := streamingModel(t)

	updated, _ := m.Update(tokenMsg{think: "first line\n"})
	m = updated.(Model)
	m.spinning = true
	updated, _ = m.Update(tokenMsg{think: "second line"})
	m = updated.(Model)
	if !m.streamDirty {
		t.Fatal("a chunk that lands while the chain runs owes a repaint, it does not take one")
	}
	if got := len(m.transcript); got != 1 {
		t.Fatalf("every chunk of a round lands on one entry, got %d entries", got)
	}
	view := stripANSI(m.renderHistory())
	for _, want := range []string{"first line", "second line"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the passage should hold what has arrived:\n%s", view)
		}
	}
}

// TestThink_NewRoundNewPassage: reasoning belongs to the round that produced
// it, so the next request's thinking does not extend the last one's.
func TestThink_NewRoundNewPassage(t *testing.T) {
	m := streamingModel(t)
	updated, _ := m.Update(tokenMsg{think: "round one"})
	m = updated.(Model)

	events := make(chan provider.StreamEvent)
	close(events)
	updated, _ = m.Update(streamStartedMsg{events: events})
	m = updated.(Model)
	updated, _ = m.Update(tokenMsg{think: "round two"})
	m = updated.(Model)

	if got := len(m.transcript); got != 2 {
		t.Fatalf("two rounds of thinking are two passages, got %d", got)
	}
	if m.transcript[0].text != "round one" || m.transcript[1].text != "round two" {
		t.Fatalf("each holds its own round's thinking: %q, %q",
			m.transcript[0].text, m.transcript[1].text)
	}
}

// TestThink_NotDuringCompaction: a compaction is housekeeping, and the
// passage would either be wiped with the transcript it summarised or outlive
// it.
func TestThink_NotDuringCompaction(t *testing.T) {
	m := streamingModel(t)
	m.compacting = true

	updated, _ := m.Update(tokenMsg{think: "deciding what to keep"})
	m = updated.(Model)

	if len(m.transcript) != 0 {
		t.Fatalf("a compaction's thinking is not a transcript entry: %v", kindsOf(m.transcript))
	}
}

// TestThink_SurvivesARebuild: /rewind, a compaction's kept turns and a
// resumed conversation all rebuild the transcript from the messages, and the
// reasoning is still being replayed to the model — so the passages come back
// too. Only a round that asked for tools keeps its blocks, so a final answer
// rebuilds with none.
func TestThink_SurvivesARebuild(t *testing.T) {
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
			if got := kindsOf(m.transcript); !slices.Equal(got, tc.want) {
				t.Fatalf("rebuilt transcript %v, want %v", got, tc.want)
			}
		})
	}
}

// TestThink_EndsTheStepItStandsIn: [title][call][think][call][call]. A card
// has no place on its band for prose that is not its body, so the thought
// stands between two cards where it was thought — the titled step's one call
// before it, the two after it a card nothing titled — and folding the first
// card leaves the thought on screen.
func TestThink_EndsTheStepItStandsIn(t *testing.T) {
	m := readyModel(t)
	read := func(path string) entry {
		return entry{kind: entryTool, toolName: "read_file",
			toolArgs: `{"path":"` + path + `"}`, toolResult: "package agent"}
	}
	for _, e := range []entry{
		{kind: entryAssistant, text: "Reading the loop"},
		read("a.go"),
		{kind: entryThink, text: "Now for the other half."},
		read("d.go"),
		read("e.go"),
	} {
		m.appendEntry(e)
		_ = m.renderHistory()
	}

	blocks := m.blocksOf(m.transcript)
	if len(blocks) != 3 || blocks[0].step == nil || blocks[2].step != nil {
		t.Fatalf("a titled step, the thought, a run nothing titled: %+v", blocks)
	}
	if g := blocks[0].step; g.start != 1 || g.end != 2 {
		t.Fatalf("the step ends at the thought (1..2), got %d..%d", g.start, g.end)
	}

	m.transcript[0].stepFold = foldClosed
	m.invalidateRenderCache()
	view := stripANSI(m.renderHistory())
	read1, thought, read2 := strings.Index(view, "read a.go"),
		strings.Index(view, "Now for the other half."), strings.Index(view, "read 2 files")
	if read1 < 0 || thought < 0 || read2 < 0 || read1 > thought || thought > read2 {
		t.Fatalf("card, thought, card, in the order they happened:\n%s", view)
	}
}

// TestThink_TheRungDecides: low draws no thought and offers the reading
// cursor nothing to land on; normal and high draw it whole.
func TestThink_TheRungDecides(t *testing.T) {
	for _, tc := range []struct {
		name      string
		verbosity verbosity
		lines     int
	}{
		{"low drops it", verbosityLow, 0},
		{"normal draws it whole", verbosityNormal, 40},
		{"high draws it whole", verbosityHigh, 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := readyModel(t)
			m.verbosity = tc.verbosity
			m.appendEntry(entry{kind: entryThink, text: numberedThought(40)})
			view := strings.TrimRight(m.renderEntry(m.transcript[0], 80), "\n")
			got := 0
			if view != "" {
				got = len(strings.Split(view, "\n"))
			}
			if got != tc.lines {
				t.Fatalf("%d lines drawn, want %d", got, tc.lines)
			}
			if stops := len(m.expandableIndices()); (stops > 0) != (tc.lines > 0) {
				t.Fatalf("a thought on screen is a stop and one off it is not, got %d stops", stops)
			}
		})
	}
}

// TestThink_StreamOrder drives the whole thing through the stream reader
// the session uses, so the batching and the ordering are asserted together.
func TestThink_StreamOrder(t *testing.T) {
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

// BenchmarkThinkStreaming measures what a thought costs while it fills: 20k
// characters of reasoning arriving in chunks, drawn whole for each. What the
// repaints actually cost is bounded by the tick they ride, which
// TestThink_StreamsOnTheTick is the assertion for.
func BenchmarkThinkStreaming(b *testing.B) {
	var block strings.Builder
	for i := 0; block.Len() < 20_000; i++ {
		fmt.Fprintf(&block, "line %d of the model thinking about the change, at some length\n", i)
	}
	src := block.String()
	var cuts []int
	for i := 1200; i < len(src); i += 1200 {
		cuts = append(cuts, i)
	}
	cuts = append(cuts, len(src))

	m := Model{verbosity: verbosityNormal}
	for b.Loop() {
		for _, c := range cuts {
			m.thinkBlock(src[:c], 80)
		}
	}
}
