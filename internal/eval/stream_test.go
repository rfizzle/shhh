package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/observe/record"
)

// streamOf builds a trace from lines written as maps, so a test states only the
// fields it is about.
func streamOf(t *testing.T, lines ...map[string]any) trace {
	t.Helper()
	var sb strings.Builder
	for _, l := range lines {
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	tr, err := decodeStream(strings.NewReader(sb.String()))
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func callLine(round int, id, tool, args string) map[string]any {
	return map[string]any{"kind": record.EventToolCall, "turn": 1, "round": round, "id": id, "tool": tool, "arguments": args}
}

func resultLine(round int, id, tool, result string) map[string]any {
	return map[string]any{"kind": record.EventToolResult, "turn": 1, "round": round, "id": id, "tool": tool, "result": result, "outcome": "ok"}
}

func unknownResultLine(round int, id, tool string) map[string]any {
	l := resultLine(round, id, tool, "no such tool")
	l["class"] = record.ClassUnknown
	return l
}

func decisionLine(round int, decision, reason string) map[string]any {
	return map[string]any{"kind": record.EventDecision, "turn": 1, "round": round, "decision": decision, "reason": reason}
}

// legacyTranscript is the `--json` reading this package used to take, kept
// here as the reference the stream's numbers are held to: the same run written
// as both shapes has to come out the same.
type legacyTranscript struct {
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Messages []struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		ToolCalls []struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"tool_calls"`
	} `json:"messages"`
}

func (t legacyTranscript) rounds() (rounds, calls int) {
	for _, m := range t.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			rounds++
			calls += len(m.ToolCalls)
		}
	}
	return rounds, calls
}

func (t legacyTranscript) behaviour() (b Behaviour) {
	seenMutation := false
	for _, m := range t.Messages {
		if m.Role == "tool" {
			b.ResultBytes += len(m.Content)
		}
		if m.Role != "assistant" {
			continue
		}
		for _, call := range m.ToolCalls {
			switch call.Name {
			case "write_file", "edit_file", "git_write":
				b.MutationsAttempted++
				seenMutation = true
			case "quality_gate":
				b.ValidationAttempts++
			case "execute_command":
				b.ShellCalls++
				if validationCommand(call.Arguments) {
					b.ValidationAttempts++
				}
			}
			if !seenMutation {
				b.CallsBeforeFirstMutation++
			}
		}
	}
	return b
}

// The port is held to the numbers the transcript gave: one run, written in both
// shapes, reads the same rounds, calls, usage and behaviour.
func TestStreamReadsTheSameNumbersTheTranscriptDid(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "stream", "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var old legacyTranscript
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join("testdata", "stream", "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr, err := decodeStream(f)
	if err != nil {
		t.Fatal(err)
	}

	wantRounds, wantCalls := old.rounds()
	if rounds, calls := tr.rounds(); rounds != wantRounds || calls != wantCalls {
		t.Errorf("rounds/calls = %d/%d, the transcript said %d/%d", rounds, calls, wantRounds, wantCalls)
	}
	if in, out := tr.usage(); in != old.Usage.PromptTokens || out != old.Usage.CompletionTokens {
		t.Errorf("usage = %d/%d, the transcript said %d/%d", in, out, old.Usage.PromptTokens, old.Usage.CompletionTokens)
	}
	if got, want := tr.behaviour(), old.behaviour(); got != want {
		t.Errorf("behaviour = %+v, the transcript said %+v", got, want)
	}
	if got := tr.behaviour(); got.ShellCalls != 1 || got.ValidationAttempts != 1 || got.MutationsAttempted != 1 {
		t.Errorf("the fixture should exercise the shell, a validation and a mutation: %+v", got)
	}
}

// A round is a position the loop reported a call at, so two calls in one round
// are one round, and a message that asked for nothing is none.
func TestRoundsAreDistinctPositionsNotMessages(t *testing.T) {
	tr := streamOf(t,
		callLine(1, "a", "read_file", `{}`), callLine(1, "b", "search", `{}`),
		map[string]any{"kind": record.EventText, "turn": 1, "round": 2, "text": "thinking"},
		callLine(3, "c", "read_file", `{}`),
		map[string]any{"kind": record.EventToolCall, "turn": 2, "round": 1, "id": "d", "tool": "search", "arguments": "{}"},
	)
	rounds, calls := tr.rounds()
	if rounds != 3 || calls != 4 {
		t.Errorf("rounds/calls = %d/%d, want 3/4: two calls at one position are one round, and a turn restarts the count", rounds, calls)
	}
}

// The close line states the run's totals; a run that died before closing is
// read off its last usage line.
func TestUsageComesOffTheCloseLine(t *testing.T) {
	usage := func(in, out int) map[string]any {
		return map[string]any{"prompt_tokens": in, "completion_tokens": out}
	}
	closed := streamOf(t,
		map[string]any{"kind": record.EventUsage, "usage": usage(10, 1)},
		map[string]any{"kind": record.EventUsage, "usage": usage(20, 2)},
		map[string]any{"kind": record.EventClose, "outcome": "completed", "exit": 0, "usage": usage(99, 9)},
	)
	if in, out := closed.usage(); in != 99 || out != 9 {
		t.Errorf("usage = %d/%d, want the close line's 99/9", in, out)
	}
	cut := streamOf(t,
		map[string]any{"kind": record.EventUsage, "usage": usage(10, 1)},
		map[string]any{"kind": record.EventUsage, "usage": usage(20, 2)},
	)
	if in, out := cut.usage(); in != 20 || out != 2 {
		t.Errorf("usage = %d/%d, want the last usage line's 20/2 for a run that never closed", in, out)
	}
}

// A line that is not an event is an error that names it, and the lines before
// it are kept: a session that died mid-line still did the work before.
func TestAStreamLineThatIsNotAnEventIsNamed(t *testing.T) {
	tr, err := decodeStream(strings.NewReader("{\"kind\":\"text\"}\n\n{\"kind\":\n"))
	if err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("err = %v, want one naming line 3", err)
	}
	if len(tr.events) != 1 {
		t.Errorf("kept %d events, want the 1 before the bad line", len(tr.events))
	}
}

// A result is joined to its call by id, and a result whose call never appeared
// is not a call.
func TestCallsAreJoinedToTheirResultsById(t *testing.T) {
	tr := streamOf(t,
		callLine(1, "a", "read_file", `{"path":"x"}`), callLine(1, "b", "search", `{}`),
		resultLine(1, "b", "search", "found"), resultLine(1, "a", "read_file", "body"),
		resultLine(1, "orphan", "search", "?"),
	)
	got := tr.calls()
	if len(got) != 2 || got[0].Result != "body" || got[1].Result != "found" || !got[0].answered {
		t.Errorf("calls = %+v", got)
	}
}
