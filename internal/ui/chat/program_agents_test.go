package chat

// Routes through the session's children: the spawn card, the lanes, a
// child's request, the manager, attaching, the hold, and the notebook
// (program_routes_test.go says what these are for).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/persona"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// children is what each child is answered with, by the name its spawn gave
// it; a child with no script answers once and finishes.
type children map[string][]programTurn

// agentSession is a session over root whose spawn_agent is real: the spawn
// card is the session's own, the supervisor starts each child on a scripted
// provider of its own, and a child's command is routed to the session's
// card and run by a stub that prints one line; a child's own spawn is
// carded the same way. With nb, every child writes
// into the session's notebook under its own name.
func agentSession(t *testing.T, root string, nb *notebook.Store, kids children, turns ...programTurn) (Model, *subagent.Supervisor) {
	t.Helper()
	var sup *subagent.Supervisor
	sup = subagent.New(context.Background(), subagent.Options{Root: root, NewEnv: func(ctx context.Context, spec subagent.Spec) (subagent.Env, error) {
		script := kids[spec.Name]
		if len(script) == 0 {
			script = []programTurn{{text: "Nothing to report."}}
		}
		p := &programProvider{turns: script}
		return subagent.Env{
			SystemPrompt: "sys",
			Stream: func(msgs []provider.Message, choice string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
				sctx, cancel := context.WithCancel(ctx)
				events, err := p.StreamCompletion(sctx, msgs, provider.CompletionOpts{ToolChoice: choice})
				if err != nil {
					cancel()
					return nil, nil, err
				}
				return events, cancel, nil
			},
			// A child may delegate, and its spawn is carded like its
			// commands: both reach the session as a decision of its own.
			Executor:     withNotebook(nb, spec.Name, sup.WrapExecutor(spec.Name, subagent.RootedExecutor(spec.Root, tools.Execute))),
			ExecuteGated: sup.WrapExecutor(spec.Name, subagent.RootedExecutor(spec.Root, tools.Execute)),
			RunCommand:   func(context.Context, string) (string, int) { return "counted", 0 },
			Gated:        map[string]bool{tools.ExecCommandName: true, subagent.SpawnToolName: true},
		}, nil
	}})
	t.Cleanup(sup.Close)
	m := readingSession(root, turns...)
	if nb != nil {
		m = m.WithNotebook(nb)
	}
	m = m.WithToolExecutor(sup.WrapExecutor("", subagent.RootedExecutor(root, tools.Execute))).
		WithGatedTools(map[string]GatedPreviewFunc{subagent.SpawnToolName: spawnPreview}).
		WithSubagents(sup)
	return m, sup
}

// spawns is one round asking for a child per name, all of one role.
func spawns(text, role string, names ...string) programTurn {
	turn := programTurn{text: text}
	for _, name := range names {
		turn.calls = append(turn.calls, call("s-"+name, subagent.SpawnToolName,
			fmt.Sprintf(`{"role":%q,"task":"Say where the round counter is read.","name":%q}`, role, name)))
	}
	return turn
}

// A round asking for children is one spawn card, [y] starts them all, and
// the fan-out block draws a lane per child.
func TestProgram_ASpawnCardStartsTheChildrenAsLanes(t *testing.T) {
	hold, release := quietHold(t)
	root := fixtureDir(t, map[string]string{"loop.go": "package agent\n"})
	lead := spawns("Fanning two readers over the round accounting.\n", "researcher", "reader-1", "reader-2")
	lead.hold = hold
	m, _ := agentSession(t, root, nil, children{
		"reader-1": {{calls: reads("loop.go")}, {text: "The counter is read at the top of the loop."}},
		"reader-2": {{text: "The limit is set where the session builds the agent."}},
	}, lead, programTurn{text: "Both readers have reported."})
	tm := runProgramAt(t, m, 120, 44)

	send(tm, "survey the round accounting")
	release()
	waitForText(t, tm, "Spawn 2 researchers")
	tm.Send(programAllow)
	waitForText(t, tm, "Both readers have reported")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "reader-1", "reader-2", "2 agents")
}

// A conversation's colleague named from the @ menu: the menu offers the role
// beside the files with its description, tab writes `@name` into the
// sentence, and the sentence is what the model reads. The spawn the model
// makes under that hint is an ordinary one, put on the ordinary card
// (docs/capabilities/chat.md#colleagues-not-workers).
func TestProgram_ANamedColleagueIsAHintAndItsSpawnIsCarded(t *testing.T) {
	hold, release := quietHold(t)
	root := fixtureDir(t, map[string]string{"auth.go": "package auth\n"})
	lead := spawns("Asking the security reviewer.\n", "security-reviewer", "sec-1")
	lead.hold = hold
	m, sup := agentSession(t, root, nil, children{
		"sec-1": {{text: "The token check compares in constant time."}},
	}, lead, programTurn{text: "The reviewer has reported."})
	sup.AddProfile(subagent.Profile{Name: "security-reviewer", Description: "reads a change for what it exposes"})
	// The card is the one every spawn gets; only the roles it is judged
	// against are the supervisor's, which is where the colleague lives.
	m = m.WithGatedTools(map[string]GatedPreviewFunc{subagent.SpawnToolName: func(raw json.RawMessage) (GatedPreview, error) {
		plan, err := subagent.SpawnPlan(sup.Profiles(), raw)
		if err != nil {
			return GatedPreview{}, err
		}
		return GatedPreview{Action: "spawn", Summary: "start a " + string(plan.Role), Title: "spawn " + plan.Name,
			Spawn: &components.SpawnRow{Role: string(plan.Role), Name: plan.Name, About: plan.About, Task: plan.Task}}, nil
	}})
	m = m.WithPersonas(Personas{Kind: persona.KindChat, Roles: func() []SpawnableRole {
		return []SpawnableRole{{Name: "security-reviewer", Description: "reads a change for what it exposes"}}
	}})
	tm := runProgramAt(t, m, 120, 44)

	tm.Type("ask @sec")
	waitForText(t, tm, "reads a change for what it exposes")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyTab})
	tm.Type("about the token check")
	tm.Send(programEnter)
	waitForText(t, tm, "ask @security-reviewer about the token check")
	release()
	waitForText(t, tm, "Spawn security-reviewer")
	tm.Send(programAllow)
	waitForText(t, tm, "The reviewer has reported")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "ask @security-reviewer about the token check", "sec-1")
}

// startChildren sends the line that makes the session spawn, answers the
// spawn card with [y] once the keyboard is quiet, and waits for the session's
// own turn to end over the running children.
func startChildren(t *testing.T, tm *program, release func(), card, settled string) {
	t.Helper()
	send(tm, "survey the round accounting")
	release()
	waitForText(t, tm, card)
	tm.Send(programAllow)
	waitForText(t, tm, settled)
}

// A child's command is the session's decision: its card is routed to the
// session under the child's name, the handover gives it the keyboard, [y]
// runs the command in the child, and the child finishes.
func TestProgram_AChildsRequestIsAnsweredFromItsCard(t *testing.T) {
	hold, release := quietHold(t)
	root := programRepo(t, nil)
	lead := spawns("One writer on the round accounting.\n", "writer", "writer-1")
	lead.hold = hold
	m, _ := agentSession(t, root, nil, children{
		"writer-1": {
			{calls: []provider.ToolCall{call("w1", tools.ExecCommandName, `{"command":"echo counted"}`)}},
			{text: "The counter is read at the top of the loop, and nowhere else."},
		},
	}, lead, programTurn{text: "The writer is on it."})
	tm := runProgramAt(t, m, 120, 52)

	startChildren(t, tm, release, "Spawn writer", "The writer is on it")
	waitForText(t, tm, "writer-1 ▸ Approve command")
	tm.Send(programHandover)
	tm.Send(programAllow)
	waitForText(t, tm, "Agent writer-1: done")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "writer-1")
	if strings.Contains(frame, "Approve command") {
		t.Fatalf("the answered child's card is still up:\n%s", frame)
	}
}

// heldChildren is a session over two running readers held mid-turn until
// the test lets them go, so the surfaces over running children can be
// reached while there are some. With open, the session's own turn after the
// spawn is held open too — the state a hold is pressed in.
func heldChildren(t *testing.T, open bool) (*program, func()) {
	t.Helper()
	hold, release := quietHold(t)
	busy := make(chan struct{})
	letGo := sync.OnceFunc(func() { close(busy) })
	t.Cleanup(letGo)
	var after programTurn
	settled := "The readers are on it"
	if open {
		after.hold = make(chan struct{})
		t.Cleanup(sync.OnceFunc(func() { close(after.hold) }))
		settled = "2 agents"
	}
	after.text = "The readers are on it."
	root := fixtureDir(t, map[string]string{"loop.go": "package agent\n"})
	lead := spawns("Fanning two readers over the round accounting.\n", "researcher", "reader-1", "reader-2")
	lead.hold = hold
	m, _ := agentSession(t, root, nil, children{
		"reader-1": {{hold: busy, calls: reads("loop.go")}, {text: "The counter is read at the top of the loop."}},
		"reader-2": {{hold: busy, calls: reads("loop.go")}, {text: "The limit is set where the session builds the agent."}},
	}, lead, after)
	tm := runProgramAt(t, m, 130, 44)
	startChildren(t, tm, release, "Spawn 2 researchers", settled)
	return tm, letGo
}

// The manager opens over the running children; a lane's steer is typed in
// place and delivered, and the kill asks before it stops a child.
func TestProgram_TheManagerSteersAndKillsAChild(t *testing.T) {
	tm, _ := heldChildren(t, false)

	programPress(t, tm, "alt+a")
	waitForText(t, tm, "[enter] attach")
	programPress(t, tm, "j", "s")
	waitForText(t, tm, "steer reader-1")
	tm.Send(tea.PasteMsg{Content: "read the exit condition too"})
	programPress(t, tm, "enter")
	waitForText(t, tm, "steered from lane")
	programPress(t, tm, "j", "X")
	waitForText(t, tm, "Its turn stops")
	programPress(t, tm, "y")
	waitForText(t, tm, "cancelled")

	frameHas(t, finalFrame(t, tm), "reader-2", "cancelled")
}

// A child that has answered is asked again from its row: over a finished
// child the steer key is a follow-up, what is typed reaches the child's own
// conversation, and the row goes back to running with the question under it.
func TestProgram_TheManagerAsksAFinishedChildAFollowUp(t *testing.T) {
	hold, release := quietHold(t)
	// The follow-up's answer is held, so the row is still running on the
	// question when the frame is read.
	busy := make(chan struct{})
	t.Cleanup(sync.OnceFunc(func() { close(busy) }))
	root := fixtureDir(t, map[string]string{"loop.go": "package agent\n"})
	lead := spawns("One reader on the round accounting.\n", "researcher", "reader-1")
	lead.hold = hold
	m, _ := agentSession(t, root, nil, children{
		"reader-1": {{text: "The counter is read at the top of the loop."},
			{hold: busy, text: "The limit is read where the loop is set up."}},
	}, lead, programTurn{text: "The reader is on it."})
	tm := runProgramAt(t, m, 110, 44)

	startChildren(t, tm, release, "Spawn researcher", "The reader is on it")
	waitForText(t, tm, "Agent reader-1: done")
	programPress(t, tm, "alt+a")
	waitForText(t, tm, "[enter] attach")
	programPress(t, tm, "j")
	waitForText(t, tm, "[s] follow up")
	programPress(t, tm, "s")
	waitForText(t, tm, "follow up reader-1")
	tm.Send(tea.PasteMsg{Content: "and where is the limit read"})
	programPress(t, tm, "enter")
	waitForText(t, tm, "follow-up · and where is the limit read")

	frameHas(t, finalFrame(t, tm), "reader-1", "working")
}

// Attaching by name moves the keyboard into a child: the frame is the
// child's, a line typed there is the child's steering, and esc gives the
// keyboard back to the session.
func TestProgram_AttachingMovesTheKeyboardIntoAChild(t *testing.T) {
	tm, _ := heldChildren(t, false)

	send(tm, "/attach reader-1")
	waitForText(t, tm, "[esc] detach")
	send(tm, "read round.go as well")
	waitForText(t, tm, "queued steering: 1")
	programPress(t, tm, "esc")
	waitForText(t, tm, "[enter] send")

	frame := finalFrame(t, tm)
	if strings.Contains(frame, "[esc] detach") {
		t.Fatalf("esc did not bring the keyboard back to the session:\n%s", frame)
	}
}

// The hold reaches the whole fan-out: every running lane parks and the tally
// over the lanes says how many, and the same key lets them go.
func TestProgram_AHoldReachesTheFanOut(t *testing.T) {
	tm, letGo := heldChildren(t, true)

	programPress(t, tm, "ctrl+p")
	waitForText(t, tm, "holding after this round")
	letGo()
	waitForText(t, tm, "2 held")
	programPress(t, tm, "ctrl+p")
	waitForGone(t, tm, "⏸ held")

	frameHas(t, finalFrame(t, tm), "reader-1", "reader-2")
}

// The next-agent chord walks the rail's map from the session into each
// child and back round to the session.
func TestProgram_TheNextAgentChordWalksTheMap(t *testing.T) {
	tm, _ := heldChildren(t, false)

	programPress(t, tm, "alt+]")
	waitForText(t, tm, "[esc] detach")
	programPress(t, tm, "alt+]", "alt+]")
	waitForGone(t, tm, "[esc] detach")

	frameHas(t, finalFrame(t, tm), "[enter] send")
}

// The manager answers a child's request in place: the pointer on the
// waiting child, its answer key, and the card that opens over the list.
func TestProgram_TheManagerAnswersAChildInPlace(t *testing.T) {
	hold, release := quietHold(t)
	root := programRepo(t, nil)
	lead := spawns("One writer on the round accounting.\n", "writer", "writer-1")
	lead.hold = hold
	m, _ := agentSession(t, root, nil, children{
		"writer-1": {
			{calls: []provider.ToolCall{call("w1", tools.ExecCommandName, `{"command":"echo counted"}`)}},
			{text: "The counter is read at the top of the loop."},
		},
	}, lead, programTurn{text: "The writer is on it."})
	tm := runProgramAt(t, m, 110, 44)

	startChildren(t, tm, release, "Spawn writer", "The writer is on it")
	waitForText(t, tm, "writer-1 ▸ Approve command")
	programPress(t, tm, "esc")
	programPress(t, tm, "alt+a")
	waitForText(t, tm, "[enter] attach")
	programPress(t, tm, "j")
	waitForText(t, tm, "answer without attaching")
	programPress(t, tm, "a")
	waitForText(t, tm, "back to the agents")
	programPress(t, tm, "n")
	waitForText(t, tm, "Declined writer-1")

	frameHas(t, finalFrame(t, tm), "Declined writer-1")
}

// A settled lane folds open on the child's own report from reading mode.
func TestProgram_ASettledLaneOpensOnItsReport(t *testing.T) {
	hold, release := quietHold(t)
	root := fixtureDir(t, map[string]string{"loop.go": "package agent\n"})
	lead := spawns("Two readers on the round accounting.\n", "researcher", "reader-1", "reader-2")
	lead.hold = hold
	// The session's own turn stays open over the child, as a fan-out's does
	// while it waits on the lanes, so the block is on the grid unfolded.
	open := programTurn{hold: make(chan struct{}), text: "The reader has reported."}
	t.Cleanup(sync.OnceFunc(func() { close(open.hold) }))
	m, _ := agentSession(t, root, nil, children{
		"reader-1": {{text: "The counter is read at the top of the loop.\n\n## Assumptions\n\n- The limit is the one in loop.go.\n- Nothing else reads the counter."}},
		"reader-2": {{text: "The limit is set where the session builds the agent."}},
	}, lead, open)
	tm := runProgramAt(t, m, 120, 52)

	startChildren(t, tm, release, "Spawn 2 researchers", "▸ report ·")
	programPress(t, tm, "ctrl+o", "k", "j", "enter")
	waitForText(t, tm, "▾ report ·")

	frameHas(t, finalFrame(t, tm), "Nothing else reads the counter.")
}

// Two children write into the session's notebook, the turn's close says so,
// and /notes opens the screen where a note is pointed at and dropped.
func TestProgram_TheNotebookIsReadAndCorrectedOnItsScreen(t *testing.T) {
	hold, release := quietHold(t)
	root := fixtureDir(t, map[string]string{"loop.go": "package agent\n"})
	lead := spawns("Two readers on the round accounting.\n", "researcher", "reader-1", "reader-2")
	lead.hold = hold
	note := func(title string) []programTurn {
		return []programTurn{
			{calls: []provider.ToolCall{call("n-"+title, notebook.WriteToolName, fmt.Sprintf(`{"title":%q,"body":"what the reader found"}`, title))}},
			{text: "Noted."},
		}
	}
	nb := notebook.New(nil)
	// The session's own turn after the spawn is held until both notes are
	// in, so its close is the one that counts them.
	written := make(chan struct{})
	go func() {
		for nb.Len() < 2 {
			time.Sleep(5 * time.Millisecond)
		}
		close(written)
	}()
	m, _ := agentSession(t, root, nb, children{
		"reader-1": note("The counter is read at the top of the loop"),
		"reader-2": note("The limit is set from the profile"),
	}, lead, programTurn{hold: written, text: "Both of them left a note."})
	tm := runProgramAt(t, m, 120, 44)

	send(tm, "survey the round accounting")
	release()
	waitForText(t, tm, "Spawn 2 researchers")
	tm.Send(programAllow)
	waitForText(t, tm, "2 notes from")
	send(tm, "/notes")
	waitForText(t, tm, "2 notes · 2 agents")
	programPress(t, tm, "k", "d")
	waitForText(t, tm, "Drop \"")
	programPress(t, tm, "y")
	waitForText(t, tm, "Dropped note")

	frameHas(t, finalFrame(t, tm), "Dropped note")
}

// A child that delegates asks the session first: its spawn is a card routed
// from the child, [y] starts the grandchild, and the grandchild is on the
// session's map one level down.
func TestProgram_AChildsSpawnIsTheSessionsDecision(t *testing.T) {
	hold, release := quietHold(t)
	root := fixtureDir(t, map[string]string{"loop.go": "package agent\n"})
	lead := spawns("One reader on the round accounting.\n", "researcher", "reader-1")
	lead.hold = hold
	m, _ := agentSession(t, root, nil, children{
		"reader-1": {
			spawns("", "researcher", "reader-1a"),
			{text: "The exit condition lives in the agent package."},
		},
		"reader-1a": {{text: "The loop exits on the counter reaching the limit."}},
	}, lead, programTurn{text: "The reader is on it."})
	tm := runProgramAt(t, m, 130, 44)

	startChildren(t, tm, release, "Spawn researcher", "The reader is on it")
	waitForText(t, tm, "reader-1 ▸")
	tm.Send(programHandover)
	tm.Send(programAllow)
	waitForText(t, tm, "Agent reader-1a: done")

	frameHas(t, finalFrame(t, tm), "Approved reader-1 ▸ use spawn_agent", "└◇ reader-1a")
}

// withNotebook signs a child's notebook calls with its name when the session
// has a notebook, and is the child's executor untouched when it has none.
func withNotebook(nb *notebook.Store, name string, next agent.ToolExecutor) agent.ToolExecutor {
	if nb == nil {
		return next
	}
	return nb.WrapExecutor(name, next)
}
