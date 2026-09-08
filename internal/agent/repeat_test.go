package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/provider"
)

func TestRepeatDetector_CountsIdenticalInteractions(t *testing.T) {
	d := NewRepeatDetector()
	args := json.RawMessage(`{"pattern":"foo"}`)

	if n := d.Note("search", args, "a.go:1: foo"); n != 1 {
		t.Errorf("first call should count once, got %d", n)
	}
	if n := d.Note("search", args, "a.go:1: foo"); n != 2 {
		t.Errorf("the same call again should count twice, got %d", n)
	}
	if n := d.Note("search", args, "a.go:1: foo"); n != 3 {
		t.Errorf("and again three times, got %d", n)
	}
}

func TestRepeatDetector_ChangedOutputIsNotARepeat(t *testing.T) {
	// The whole point of keying on the output too: `go test` run again after
	// a fix is a different interaction, and the one case where running the
	// same command twice is exactly right.
	d := NewRepeatDetector()
	args := json.RawMessage(`{"command":"go test ./..."}`)

	d.Note("execute_command", args, "FAIL")
	if n := d.Note("execute_command", args, "ok"); n != 1 {
		t.Errorf("a different result is a different interaction, got %d", n)
	}
}

func TestRepeatDetector_ArgumentsAreCanonicalised(t *testing.T) {
	d := NewRepeatDetector()
	d.Note("search", json.RawMessage(`{"pattern":"foo","path":"internal"}`), "out")
	n := d.Note("search", json.RawMessage(`{ "path": "internal",  "pattern": "foo" }`), "out")
	if n != 2 {
		t.Errorf("the same call written differently is still the same call, got %d", n)
	}
}

func TestRepeatDetector_WindowForgets(t *testing.T) {
	d := NewRepeatDetector()
	args := json.RawMessage(`{"pattern":"foo"}`)
	d.Note("search", args, "out")
	for i := 0; i < repeatWindow; i++ {
		d.Note("search", json.RawMessage(fmt.Sprintf(`{"pattern":"other%d"}`, i)), "out")
	}
	if n := d.Note("search", args, "out"); n != 1 {
		t.Errorf("a call older than the window should be forgotten, got %d", n)
	}
}

func TestRepeatDetector_WrapExecutorAnnotatesTheRepeat(t *testing.T) {
	d := NewRepeatDetector()
	exec := d.WrapExecutor(func(string, json.RawMessage) (string, error) {
		return "a.go:1: foo", nil
	})
	args := json.RawMessage(`{"pattern":"foo"}`)

	first, err := exec("search", args)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(first, "[repeat:") {
		t.Errorf("the first call is not a repeat: %q", first)
	}

	second, err := exec("search", args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(second, "[repeat:") {
		t.Errorf("the notice should lead the result, got %q", second)
	}
	if !strings.Contains(second, "search") || !strings.Contains(second, "2 times") {
		t.Errorf("the notice should name the tool and the count, got %q", second)
	}
	if !strings.HasSuffix(second, "a.go:1: foo") {
		t.Errorf("the result itself must survive the notice, got %q", second)
	}
}

func TestRepeatDetector_WrapExecutorNotesTheSameFailureTwice(t *testing.T) {
	// A call that fails the same way every time is circling as surely as one
	// that succeeds identically, and it is the shape a stuck turn usually
	// takes: a path that is not there, an argument the tool will not accept.
	d := NewRepeatDetector()
	exec := d.WrapExecutor(func(string, json.RawMessage) (string, error) {
		return "", errors.New("no such file or directory")
	})
	args := json.RawMessage(`{"path":"internal/nope.go"}`)

	if _, err := exec("read_file", args); err == nil {
		t.Fatal("expected the underlying error to pass through")
	}
	_, err := exec("read_file", args)
	if err == nil {
		t.Fatal("the second failure is still a failure")
	}
	result := ExecuteWith(exec, provider.ToolCall{Name: "read_file", Arguments: string(args)})
	if !strings.HasPrefix(result, "error: [repeat:") {
		t.Errorf("the notice goes behind the error prefix, got %q", result)
	}
	if !IsRepeatNotice(result) {
		t.Error("a surface counting repeats has to see an errored one")
	}
	if !strings.Contains(result, "no such file") {
		t.Errorf("the failure itself must survive the notice, got %q", result)
	}
	if strings.Contains(result, "widen or narrow the search") {
		t.Errorf("a way out written for an unwanted answer is nonsense for a failure, got %q", result)
	}
}

func TestRepeatDetector_WrapResolverAnnotatesTheRepeat(t *testing.T) {
	// The gated tier: a command is resolved rather than dispatched, so this
	// is the only place the detector can see the call its own package
	// comment is written about.
	d := NewRepeatDetector()
	resolve := d.WrapResolver(func(provider.ToolCall) string {
		return "exit code: 1\noutput:\nFAIL\tinternal/calc"
	})
	call := provider.ToolCall{Name: "execute_command", Arguments: `{"command":"go test ./internal/calc"}`}

	if first := resolve(call); strings.Contains(first, "[repeat:") {
		t.Errorf("the first run is not a repeat: %q", first)
	}
	second := resolve(call)
	if !strings.HasPrefix(second, "[repeat:") {
		t.Errorf("the notice should lead the result, got %q", second)
	}
	if !strings.Contains(second, "execute_command") || !strings.Contains(second, "2 times") {
		t.Errorf("the notice should name the tool and the count, got %q", second)
	}
	if !strings.HasSuffix(second, "FAIL\tinternal/calc") {
		t.Errorf("the output itself must survive the notice, got %q", second)
	}
}

func TestRepeatDetector_ARepeatedRefusalIsStillAFailure(t *testing.T) {
	// Everything that reads a result tells a failure by its "error:" head —
	// the digest's outcome word, the record's class, the transcript row — so
	// the notice goes behind it rather than in front of it.
	d := NewRepeatDetector()
	resolve := d.WrapResolver(func(provider.ToolCall) string {
		return "error: file modification not approved: headless mode denies edits by default (run with --yes)"
	})
	call := provider.ToolCall{Name: "edit_file", Arguments: `{"path":"a.go","old":"x","new":"y"}`}

	_ = resolve(call)
	second := resolve(call)
	if !strings.HasPrefix(second, "error: ") {
		t.Errorf("a refused call stays a refusal, got %q", second)
	}
	if digest.Outcome(second) != digest.OutcomeError {
		t.Errorf("the digest must still read it as a failure, got %q", digest.Outcome(second))
	}
	if !IsRepeatNotice(second) {
		t.Errorf("the notice should be in there, got %q", second)
	}
}

func TestRepeatDetector_OneDetectorIsOneHistoryAcrossBothTiers(t *testing.T) {
	// The auto chain and the gated one share a window on purpose: a session
	// that re-runs a command it once had approved is the same circle whether
	// the second attempt was answered by a policy or by a person.
	d := NewRepeatDetector()
	exec := d.WrapExecutor(func(string, json.RawMessage) (string, error) { return "same output", nil })
	resolve := d.WrapResolver(func(provider.ToolCall) string { return "same output" })
	args := `{"command":"ls"}`

	_, _ = exec("execute_command", json.RawMessage(args))
	if got := resolve(provider.ToolCall{Name: "execute_command", Arguments: args}); !IsRepeatNotice(got) {
		t.Errorf("the second tier should see the first tier's call, got %q", got)
	}
}

func TestRepeatDetector_NilIsSafe(t *testing.T) {
	var d *RepeatDetector
	if n := d.Note("search", json.RawMessage(`{}`), "out"); n != 0 {
		t.Errorf("a nil detector counts nothing, got %d", n)
	}
	exec := d.WrapExecutor(func(string, json.RawMessage) (string, error) { return "out", nil })
	if got, _ := exec("search", json.RawMessage(`{}`)); got != "out" {
		t.Errorf("a nil detector wraps nothing, got %q", got)
	}
	resolve := d.WrapResolver(func(provider.ToolCall) string { return "out" })
	if got := resolve(provider.ToolCall{Name: "execute_command"}); got != "out" {
		t.Errorf("a nil detector resolves nothing, got %q", got)
	}
	if got := d.Notice("search", json.RawMessage(`{}`), "out"); got != "out" {
		t.Errorf("a nil detector notices nothing, got %q", got)
	}
}
