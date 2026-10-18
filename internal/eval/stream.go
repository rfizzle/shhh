package eval

// Reading a session off its own event stream.
//
// A workspace attempt runs `code --print --output jsonl` and decodes the lines
// into one in-memory trace. Everything an attempt reports — rounds, calls,
// usage, the behaviour block, the declared assertions — is computed from it.
//
// The stream is the record as it happens plus the content the record leaves
// out: the arguments of a call and the bytes it returned. It is read here and
// dropped with the attempt. Nothing of it is written anywhere, because a
// written session is a persisted tool output and the scrub that owes it is the
// session's to apply, not this package's. What a report or a baseline may
// carry is a tool name, an assertion word, a reason code and a count.
// See docs/capabilities/evals.md#a-case-can-say-how-the-session-should-have-gone.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/rfizzle/shhh/internal/observe/record"
	"github.com/rfizzle/shhh/internal/tools"
)

// event is one line of the stream, the subset this package reads. It is
// deliberately a subset for the reason the transcript type was: the emitter
// owns the format, and a decoder that insisted on every field would break on a
// field being added to it.
type event struct {
	Kind  string `json:"kind"`
	Turn  int64  `json:"turn"`
	Round int64  `json:"round"`

	ID        string `json:"id"`
	Tool      string `json:"tool"`
	Arguments string `json:"arguments"`
	Result    string `json:"result"`
	Outcome   string `json:"outcome"`
	Class     string `json:"class"`
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`

	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`

	// Exit and Final belong to the close line. Final is raw because a run that
	// was asked for a checked answer states it as JSON in the text's place.
	Exit  *int            `json:"exit"`
	Final json.RawMessage `json:"final"`
	Error string          `json:"error"`
}

// trace is a whole session's lines, in the order they arrived.
type trace struct{ events []event }

// decodeStream reads one event per line. A line that is not an event is an
// error naming its number, and the trace read up to it is returned beside the
// error: a session that died mid-line still did the work before it.
func decodeStream(r io.Reader) (trace, error) {
	var t trace
	br := bufio.NewReader(r)
	for n := 1; ; n++ {
		line, err := br.ReadBytes('\n')
		if text := strings.TrimSpace(string(line)); text != "" {
			ev, jerr := decodeEvent([]byte(text))
			if jerr != nil {
				return t, fmt.Errorf("stream line %d is not an event: %w", n, jerr)
			}
			t.events = append(t.events, ev)
		}
		if errors.Is(err, io.EOF) {
			return t, nil
		}
		if err != nil {
			return t, err
		}
	}
}

// decodeEvent reads one line of the stream. It is the one decoder: a printed
// run's lines and a served conversation's are the same lines, and two readers
// of them could come to count the same session two ways.
func decodeEvent(line []byte) (event, error) {
	var ev event
	err := json.Unmarshal(line, &ev)
	return ev, err
}

// rounds and calls are the shape of the work: a round is a distinct
// (turn, round) position holding a tool call, whatever it asked for in it, and
// a call is one tool-call line.
func (t trace) rounds() (rounds, calls int) {
	type pos struct{ turn, round int64 }
	seen := map[pos]bool{}
	for _, ev := range t.events {
		if ev.Kind != record.EventToolCall {
			continue
		}
		calls++
		seen[pos{ev.Turn, ev.Round}] = true
	}
	return len(seen), calls
}

// usage is the close line's totals, or the last usage line when the run died
// before it could close.
func (t trace) usage() (in, out int) {
	for i := len(t.events) - 1; i >= 0; i-- {
		if ev := t.events[i]; ev.Kind == record.EventClose && ev.Usage != nil {
			return ev.Usage.PromptTokens, ev.Usage.CompletionTokens
		}
	}
	for i := len(t.events) - 1; i >= 0; i-- {
		if ev := t.events[i]; ev.Kind == record.EventUsage && ev.Usage != nil {
			return ev.Usage.PromptTokens, ev.Usage.CompletionTokens
		}
	}
	return 0, 0
}

// closeLine is the stream's last line: how the run ended, in the record's own
// word, and the error it ended with. ok is false for a stream that never
// closed.
func (t trace) closeLine() (outcome, errText string, exit int, ok bool) {
	for i := len(t.events) - 1; i >= 0; i-- {
		if ev := t.events[i]; ev.Kind == record.EventClose {
			if ev.Exit != nil {
				exit = *ev.Exit
			}
			return ev.Outcome, ev.Error, exit, true
		}
	}
	return "", "", 0, false
}

// call is a tool-call line joined to the tool-result that answered it.
type call struct {
	ID, Tool, Arguments string
	// Result and Class are the answer's, and answered says there was one: a
	// call the run was cut off inside has none.
	Result, Outcome, Class string
	answered               bool
}

// calls joins each tool-call to its tool-result by id, in the order the calls
// were made. A result whose call never appeared is not a call and is dropped.
func (t trace) calls() []call {
	var out []call
	at := map[string]int{}
	for _, ev := range t.events {
		switch ev.Kind {
		case record.EventToolCall:
			if ev.ID != "" {
				at[ev.ID] = len(out)
			}
			out = append(out, call{ID: ev.ID, Tool: ev.Tool, Arguments: ev.Arguments})
		case record.EventToolResult:
			if i, ok := at[ev.ID]; ok && ev.ID != "" {
				out[i].Result, out[i].Outcome, out[i].Class, out[i].answered = ev.Result, ev.Outcome, ev.Class, true
			}
		}
	}
	return out
}

// behaviour reports the calls that changed or checked a workspace. It reads
// the trace only as operational evidence; the case's check remains the sole
// verdict on whether the requested work was done.
func (t trace) behaviour() (b Behaviour) {
	seenMutation := false
	for _, ev := range t.events {
		switch ev.Kind {
		case record.EventToolResult:
			b.ResultBytes += len(ev.Result)
		case record.EventToolCall:
			switch ev.Tool {
			case tools.WriteFileName, tools.EditFileName, "git_write":
				b.MutationsAttempted++
				seenMutation = true
			case "quality_gate":
				b.ValidationAttempts++
			case tools.ExecCommandName:
				b.ShellCalls++
				if validationCommand(ev.Arguments) {
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

// validationCommand recognises the commands a coding session normally uses to
// validate a change. It deliberately does not treat every command as evidence
// of verification: a status or a reproduction command belongs to investigation.
func validationCommand(arguments string) bool {
	var args struct {
		Command string `json:"command"`
	}
	if json.Unmarshal([]byte(arguments), &args) != nil {
		return false
	}
	for _, word := range strings.Fields(strings.ToLower(args.Command)) {
		switch word {
		case "test", "tests", "check", "lint", "vet", "build", "compile", "verify":
			return true
		}
	}
	return false
}
