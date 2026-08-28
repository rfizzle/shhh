package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
)

// spawnInto starts one child and waits until the supervisor knows about it,
// so a test can look at the batch rather than at a race.
func spawnInto(t *testing.T, sup *subagent.Supervisor, args string) {
	t.Helper()
	exec := sup.WrapExecutor(nil)
	if _, err := exec(subagent.SpawnToolName, json.RawMessage(args)); err != nil {
		t.Fatal(err)
	}
}

// spawnRow is the transcript entry a successful spawn produces before the
// fan-out decides whether it stays a row.
func spawnRowEntry(task string) entry {
	return entry{kind: entryTool, toolName: subagent.SpawnToolName,
		toolArgs: `{"role":"researcher","task":"` + task + `"}`, toolResult: "Spawned it."}
}

// fanoutEntries counts what the transcript holds, which is the whole question
// for a batch: one block, or a row per child.
func fanoutEntries(m Model) (blocks, spawnRows int) {
	for _, e := range m.transcript {
		switch {
		case e.kind == entryFanout:
			blocks++
		case e.kind == entryTool && e.toolName == subagent.SpawnToolName:
			spawnRows++
		}
	}
	return blocks, spawnRows
}

// TestSingleSpawnKeepsItsRow is the criterion that keeps the block for
// genuine fan-out: one child is not a fan-out, and its inline row stands.
func TestSingleSpawnKeepsItsRow(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	m.beginSpawnBatch()
	spawnInto(t, sup, `{"role":"researcher","task":"survey the loop"}`)
	m.appendSpawnEntry(spawnRowEntry("survey the loop"))

	blocks, rows := fanoutEntries(m)
	if blocks != 0 || rows != 1 {
		t.Fatalf("one spawn produced %d blocks and %d rows, want 0 and 1", blocks, rows)
	}
}

// TestTwoSpawnsBecomeOneBlock is the first criterion: a round that spawned
// two or more children renders as one block, in place of their rows, without
// disturbing the entries around it.
func TestTwoSpawnsBecomeOneBlock(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	m.appendEntry(entry{kind: entryAssistant, text: "Document and verify in parallel"})

	m.beginSpawnBatch()
	for _, task := range []string{"survey the loop", "survey the tests", "survey the folds"} {
		spawnInto(t, sup, `{"role":"researcher","task":"`+task+`"}`)
		m.appendSpawnEntry(spawnRowEntry(task))
	}

	blocks, rows := fanoutEntries(m)
	if blocks != 1 || rows != 0 {
		t.Fatalf("three spawns produced %d blocks and %d rows, want 1 and 0", blocks, rows)
	}
	if len(m.transcript) != 2 {
		t.Fatalf("transcript is %d entries, want the announcement and the block", len(m.transcript))
	}
	if m.transcript[0].kind != entryAssistant {
		t.Fatal("the fan-out disturbed the entry before it")
	}

	view := ansi.Strip(m.renderHistory())
	for _, want := range []string{"fan-out", "3 agents", "researcher-1", "researcher-2", "researcher-3"} {
		if !strings.Contains(view, want) {
			t.Fatalf("rendered history missing %q:\n%s", want, view)
		}
	}
}

// TestSecondRoundIsItsOwnBlock keeps two fan-outs apart: a batch is a round,
// so the children of a later round never join an earlier block.
func TestSecondRoundIsItsOwnBlock(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	for range 2 {
		m.beginSpawnBatch()
		for _, task := range []string{"one", "two"} {
			spawnInto(t, sup, `{"role":"researcher","task":"`+task+`"}`)
			m.appendSpawnEntry(spawnRowEntry(task))
		}
	}

	blocks, rows := fanoutEntries(m)
	if blocks != 2 || rows != 0 {
		t.Fatalf("two rounds produced %d blocks and %d rows, want 2 and 0", blocks, rows)
	}
	for _, e := range m.transcript {
		if e.kind != entryFanout {
			continue
		}
		if n := len(m.fanoutBlockFor(e).Lanes); n != 2 {
			t.Fatalf("a block has %d lanes, want the 2 its own round spawned", n)
		}
	}
}

// TestFanoutLanesUpdateInPlace is the second criterion, and the reason the
// entry stores a batch number rather than rendered text: the block re-reads
// the supervisor every render, and a block that is no longer the last thing
// in the transcript still moves.
func TestFanoutLanesUpdateInPlace(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	m.beginSpawnBatch()
	for _, task := range []string{"one", "two"} {
		spawnInto(t, sup, `{"role":"researcher","task":"`+task+`"}`)
		m.appendSpawnEntry(spawnRowEntry(task))
	}
	waitFor(t, func() bool { a, _ := sup.ActiveCounts(); return a == 2 })

	// Rows land after the block, so everything before them would ordinarily
	// freeze into the render cache.
	m.appendEntry(entry{kind: entryAssistant, text: "Now wait for them"})
	m.appendEntry(entry{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"loop.go"}`, toolResult: "x"})
	if running := ansi.Strip(m.renderHistory()); !strings.Contains(running, "2 running") {
		t.Fatalf("live block does not report its running children:\n%s", running)
	}

	m.cancelSubagents()
	waitFor(t, func() bool { a, _ := sup.ActiveCounts(); return a == 0 })

	stopped := ansi.Strip(m.renderHistory())
	if strings.Contains(stopped, "2 running") {
		t.Fatalf("the block froze with its children still shown as running:\n%s", stopped)
	}
	if !strings.Contains(stopped, "failed") {
		t.Fatalf("the block does not report the cancelled children:\n%s", stopped)
	}
}

// TestFanoutBlockFreezesOnceEveryChildStops is the other half of that: the
// render cache is not given up forever, only for as long as the lanes move.
func TestFanoutBlockFreezesOnceEveryChildStops(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	m.beginSpawnBatch()
	for _, task := range []string{"one", "two"} {
		spawnInto(t, sup, `{"role":"researcher","task":"`+task+`"}`)
		m.appendSpawnEntry(spawnRowEntry(task))
	}
	waitFor(t, func() bool { a, _ := sup.ActiveCounts(); return a == 2 })

	blocks := m.blocksOf(m.transcript)
	if got := m.liveFanoutBlock(blocks); got != 0 {
		t.Fatalf("a live fan-out is at block %d, want 0 — nothing from there on may freeze", got)
	}

	m.cancelSubagents()
	waitFor(t, func() bool { a, _ := sup.ActiveCounts(); return a == 0 })
	if got := m.liveFanoutBlock(blocks); got != len(blocks) {
		t.Fatalf("a settled fan-out still blocks the cache at %d of %d", got, len(blocks))
	}
}

// gatedEnv builds children that immediately ask to run a command, so the
// supervisor parks them blocked on the parent user.
func gatedEnv() subagent.EnvFactory {
	return func(ctx context.Context, spec subagent.Spec) (subagent.Env, error) {
		// The same gated call every round, forever: these tests want a child
		// parked on an approval, not one that finishes. Honouring ctx is what
		// makes that safe — a real provider stream is bound to the child's
		// context, so cancelling it ends the tool loop. Without this the mock
		// is an infinite generator that only ever stopped because the round
		// cap stopped it, and a child with no cap (S-144) would spin past
		// Close.
		stream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			ch := make(chan provider.StreamEvent, 1)
			ch <- provider.StreamEvent{ToolCalls: []provider.ToolCall{{
				ID: "c1", Name: tools.ExecCommandName, Arguments: `{"command":"echo hi"}`,
			}}}
			close(ch)
			return ch, func() {}, nil
		}
		return subagent.Env{
			SystemPrompt: "sys",
			Stream:       stream,
			Executor:     func(string, json.RawMessage) (string, error) { return "", errors.New("unused") },
			Gated:        map[string]bool{tools.ExecCommandName: true},
		}, nil
	}
}

// TestFanoutBlockedLaneStatesWhatItNeeds is the third criterion at the host
// boundary: a child parked on an approval reaches the lane as the blocked
// state, with the thing it is waiting for stated under it.
func TestFanoutBlockedLaneStatesWhatItNeeds(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: gatedEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	m.beginSpawnBatch()
	for _, task := range []string{"one", "two"} {
		spawnInto(t, sup, `{"role":"researcher","task":"`+task+`"}`)
		m.appendSpawnEntry(spawnRowEntry(task))
	}
	waitFor(t, func() bool { _, blocked := sup.ActiveCounts(); return blocked == 2 })

	view := ansi.Strip(m.renderHistory())
	if !strings.Contains(view, "⚠ needs you") {
		t.Fatalf("a blocked child's lane does not say it needs you:\n%s", view)
	}
	if !strings.Contains(view, "echo hi") {
		t.Fatalf("a blocked child's lane does not say what it is waiting for:\n%s", view)
	}
	if !strings.Contains(view, "2 needs you") {
		t.Fatalf("the header does not carry the blocked count:\n%s", view)
	}
}

// TestFanoutRerendersOnResize is the last criterion: the block is a passive
// transcript entry, so a resize re-renders it at the new width like anything
// else in the feed.
func TestFanoutRerendersOnResize(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)

	m.beginSpawnBatch()
	for _, task := range []string{"one", "two"} {
		spawnInto(t, sup, `{"role":"researcher","task":"`+task+`"}`)
		m.appendSpawnEntry(spawnRowEntry(task))
	}
	waitFor(t, func() bool { a, _ := sup.ActiveCounts(); return a == 2 })

	wide := ansi.Strip(m.renderHistory())
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 62, Height: 40})
	m = updated.(Model)
	narrow := ansi.Strip(m.renderHistory())

	if wide == narrow {
		t.Fatal("the block did not re-render at the new width")
	}
	for _, line := range strings.Split(narrow, "\n") {
		if len([]rune(line)) > 62 {
			t.Fatalf("a line survived the resize at %d columns: %q", len([]rune(line)), line)
		}
	}
	if !strings.Contains(narrow, "researcher-1") {
		t.Fatalf("the narrow render dropped a child's name:\n%s", narrow)
	}
}
