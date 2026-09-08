package eval

// The spawn, report and patch loop, run against a child whose model is a
// script.
//
// A child is the one thing a session starts that nobody watches. Its work
// reaches the parent as the strings its own tool calls answer with — what the
// spawn said, what a later agent_report said about that child, and what the
// overview beside it says about every child — and everything the parent model
// can act on has to be in one of them: that the child finished, what it found,
// what its patch did to the workspace, which files it claimed, how the last
// reading of its work came out. A field that reaches none of them is a field
// the parent cannot see, and nothing in the repository could say so: every
// test of this loop asserts a field on a Status, which is a structure the
// model never receives.
//
// So the case runs the real loop — a real supervisor, a real worktree, a real
// patch computed by git and applied to a real checkout — and puts the strings
// the parent was handed to a labelled row. What is scripted is the child's
// model and the person's answer to the patch card, because those are the two
// things a suite cannot have and still be a suite.
// See docs/capabilities/evals.md#a-scripted-case-measures-the-harness.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
)

// spawnSeedFile is the one file the scripted checkout starts from. A writer's
// worktree is cut from a commit, so the repository cannot be empty; it is
// also what the scripted child reads, so the round it spends reading is a
// round that read something.
const spawnSeedFile = "# the workspace a scripted child is given\n"

// spawnChildName is the name every spawn row's child is given. It is fixed
// rather than generated so the row's report call can name it, and so a row
// that requires the name to reach the parent can say which name it means.
const spawnChildName = "child"

// spawnReadingWait bounds how long the parent waits for a reading that is
// still in flight before it asks for the overview.
//
// A run ends on a reading of how it ended, and that one is started as the
// child's last round finishes and never waited for: it reaches the
// supervisor whenever it comes back, which is routinely after the report
// call that collected the child has already answered. A row about the
// verdict has to ask after it has landed or it would be measuring the race
// rather than the mechanism. Five seconds is a ceiling on a reading that is
// never coming, not a wait anything reaches — the scripted reader answers at
// once.
const spawnReadingWait = 5 * time.Second

// spawnRoundDelay is how long one scripted round takes to answer. Rounds
// here are otherwise instant, which no provider is, and the summarizer's own
// interval is measured against a loop that takes some time to go round.
const spawnRoundDelay = 3 * time.Millisecond

// askSpawn runs one row's child and puts what the parent was told to it.
func askSpawn(ctx context.Context, row Row) Answer {
	dir, err := os.MkdirTemp("", "shhh-eval-spawn-")
	if err != nil {
		return Answer{Err: "cannot make a workspace: " + err.Error()}
	}
	defer func() { _ = os.RemoveAll(dir) }()
	// A writer's worktree is cut from the parent's checkout, so the parent's
	// checkout has to be one — with something committed in it, since a
	// worktree is cut from a commit and an empty repository has none.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(spawnSeedFile), 0o644); err != nil {
		return Answer{Err: "cannot seed the workspace: " + err.Error()}
	}
	if err := initRepo(dir); err != nil {
		return Answer{Err: err.Error()}
	}

	env := &scriptedChild{row: row}
	if row.State != "" {
		env.reader = &scriptedReader{state: row.State, reason: row.Reason}
	}
	ctx, cancel := context.WithCancel(ctx)
	sup := subagent.New(ctx, subagent.Options{Root: dir, NewEnv: env.factory()})
	// Cancelled first and closed second, which is the order they are declared
	// in reversed: the goroutine below is reading the supervisor's events, and
	// a supervisor closed while it still had a reader would leave it spinning
	// on a shut channel until the cancel it is waiting for arrived.
	defer sup.Close()
	defer cancel()
	env.sup = sup

	// The person's answer to the patch card. It is given from a goroutine
	// because the child is blocked on it: the supervisor emits the card and
	// waits, and the report call below is what the parent is doing at the
	// same moment.
	go func() {
		for {
			select {
			case ev := <-sup.Events():
				if ev.Kind == subagent.EventAsk {
					ev.Ask.Respond(!row.Decline)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	exec := sup.WrapExecutor(func(name string, _ json.RawMessage) (string, error) {
		return "", fmt.Errorf("%s is not a tool a spawn case registers", name)
	})
	spawned, err := exec(subagent.SpawnToolName, scriptedArgs(map[string]any{
		"role": row.Role, "task": row.Task, "name": spawnChildName, "paths": row.Paths,
	}))
	if err != nil {
		return Answer{Label: LabelBroken, Reason: "the spawn was refused: " + firstLine(err.Error())}
	}
	// The report call is what waits for the child, exactly as a parent's
	// does; the overview beside it is the other half of what a parent can
	// ask, and the only place a reading of the child's work is spoken.
	report, err := exec(subagent.ReportToolName, scriptedArgs(map[string]any{"name": spawnChildName}))
	if err != nil {
		return Answer{Label: LabelBroken, Reason: "the report was refused: " + firstLine(err.Error())}
	}
	if env.reader != nil {
		env.awaitVerdict()
	}
	overview, err := exec(subagent.ReportToolName, scriptedArgs(map[string]any{}))
	if err != nil {
		return Answer{Label: LabelBroken, Reason: "the overview was refused: " + firstLine(err.Error())}
	}

	told := strings.Join([]string{spawned, report, overview}, "\n\n")
	dropSavedPatch(told)
	if gone := missing(told, row.needs()); gone != "" {
		return Answer{Label: LabelIncomplete, Reason: "the parent was never told " + quoteFragment(gone)}
	}
	return Answer{Label: LabelReported, Reason: firstLine(report)}
}

// savedPatchPrefix opens the note a refused patch is kept under. A patch the
// person declined is written to a file so they can still get at it, which is
// right in a session and is litter here — nobody is coming back for a
// scripted child's work — so the case takes it away again.
const savedPatchPrefix = "(patch saved to "

// dropSavedPatch removes the file such a note names.
//
// The path is read out of what the parent was told, which is the only place
// it exists, and it is deleted only where it is what it claims to be: a
// temporary file, under this machine's temporary directory, ending in the
// extension the note promises. A wording that changes leaves the file behind
// rather than deleting something else.
func dropSavedPatch(told string) {
	at := strings.Index(told, savedPatchPrefix)
	if at < 0 {
		return
	}
	rest := told[at+len(savedPatchPrefix):]
	end := strings.Index(rest, ")")
	if end < 0 {
		return
	}
	path := rest[:end]
	if filepath.Ext(path) != ".patch" || !strings.HasPrefix(path, os.TempDir()) {
		return
	}
	_ = os.Remove(path)
}

// scriptedChild is the child's model: a fixed sequence of rounds, and the
// tool executor that carries out what they ask for.
type scriptedChild struct {
	row    Row
	reader *scriptedReader
	// sup is the supervisor this child belongs to, read to know whether a
	// reading of its work has come back yet (awaitVerdict).
	sup *subagent.Supervisor

	mu   sync.Mutex
	step int
}

// writeToolName is what a scripted child calls to change a file. It is the
// session's own name for it so that a case file reads like a transcript, and
// the executor below is what a session's rooted executor is: the thing that
// carries the call out inside the child's own workspace.
const writeToolName = "write_file"

// spawnReadRounds is how many rounds a scripted child spends reading before
// it answers. A reading is not taken before the third round — a turn shorter
// than that has nothing worth judging — so a child that worked in one round
// could never be judged at all, and a row about the verdict would be
// measuring the floor rather than the mechanism.
const spawnReadRounds = 3

// steps is the script: the work, the reading rounds, then the report.
func (s *scriptedChild) steps() []scriptedStep {
	var out []scriptedStep
	if s.row.WritePath != "" {
		out = append(out, scriptedStep{call: &provider.ToolCall{
			ID: "w1", Name: writeToolName,
			Arguments: string(scriptedArgs(map[string]any{"path": s.row.WritePath, "content": s.row.WriteBody})),
		}})
	}
	for i := range spawnReadRounds {
		out = append(out, scriptedStep{call: &provider.ToolCall{
			ID: fmt.Sprintf("r%d", i+1), Name: "read_file", Arguments: `{"path":"README.md"}`,
		}})
	}
	out = append(out, scriptedStep{text: s.row.Reply})
	return out
}

// scriptedStep is one round of the child's script: a tool call, or the text
// that ends the turn.
type scriptedStep struct {
	call *provider.ToolCall
	text string
}

func (s *scriptedChild) factory() subagent.EnvFactory {
	return func(ctx context.Context, spec subagent.Spec) (subagent.Env, error) {
		script := s.steps()
		root := spec.Root
		stream := func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			s.mu.Lock()
			at := s.step
			s.step++
			s.mu.Unlock()
			if at >= len(script) {
				return nil, nil, errors.New("the script ran out before the child stopped")
			}
			step := script[at]
			time.Sleep(spawnRoundDelay)

			ch := make(chan provider.StreamEvent, 2)
			if step.text != "" {
				ch <- provider.StreamEvent{Token: step.text}
			}
			if step.call != nil {
				ch <- provider.StreamEvent{ToolCalls: []provider.ToolCall{*step.call}}
			} else {
				ch <- provider.StreamEvent{Done: true}
			}
			close(ch)
			// The whole reply is already in the channel, so there is nothing
			// a cancel could stop; the contract asks for one anyway, and
			// this is what it means here.
			return ch, func() {}, nil
		}

		env := subagent.Env{
			SystemPrompt: "a scripted child, for measuring the loop around it",
			Stream:       stream,
			Executor: func(name string, args json.RawMessage) (string, error) {
				return runScriptedCall(root, name, args)
			},
			ExecuteGated: func(name string, args json.RawMessage) (string, error) {
				return runScriptedCall(root, name, args)
			},
			RunCommand: func(context.Context, string) (string, int) { return "", 0 },
		}
		if s.reader != nil {
			// The interval is one round and the wall-clock floor is off: the
			// child runs three rounds in as many milliseconds, and a reader
			// held back by either would never be asked at all.
			env.Summarizer = agent.NewSummarizer(s.reader, agent.SummaryConfig{
				Model: "scripted", IntervalRounds: 1, MinGap: -1})
		}
		return env, nil
	}
}

// awaitVerdict holds until the child has been read once, or until the
// ceiling says the reading is not coming.
func (s *scriptedChild) awaitVerdict() {
	deadline := time.Now().Add(spawnReadingWait)
	for time.Now().Before(deadline) {
		if st, ok := s.sup.Get(spawnChildName); ok && st.Verdict != "" {
			return
		}
		time.Sleep(spawnRoundDelay)
	}
}

// runScriptedCall carries out one of the child's calls inside its own
// workspace, which is what a session's rooted executor does with them.
func runScriptedCall(root, name string, args json.RawMessage) (string, error) {
	if name != writeToolName {
		return "read", nil
	}
	var call struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &call); err != nil {
		return "", err
	}
	path := filepath.Join(root, filepath.FromSlash(call.Path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(call.Content), 0o644); err != nil {
		return "", err
	}
	return "wrote " + call.Path, nil
}

// scriptedReader is the model behind the child's readings: it answers with
// the state the row wrote down, every time it is asked.
type scriptedReader struct {
	state  string
	reason string
}

func (r *scriptedReader) Name() string { return "scripted" }

func (r *scriptedReader) StreamCompletion(context.Context, []provider.Message, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	args := scriptedArgs(map[string]any{
		"summary": "a reading of the scripted child", "state": r.state, "reason": r.reason,
	})
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{
		ToolCalls: []provider.ToolCall{{ID: "s1", Name: agent.SummaryToolName, Arguments: string(args)}},
		Done:      true,
	}
	close(ch)
	return ch, nil
}
