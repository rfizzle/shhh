package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
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

func TestRepeatDetector_WrapExecutorLeavesFailuresAlone(t *testing.T) {
	d := NewRepeatDetector()
	exec := d.WrapExecutor(func(string, json.RawMessage) (string, error) {
		return "", errors.New("boom")
	})
	args := json.RawMessage(`{"pattern":"foo"}`)
	_, _ = exec("search", args)
	out, err := exec("search", args)
	if err == nil {
		t.Fatal("expected the underlying error to pass through")
	}
	if strings.Contains(out, "[repeat:") {
		t.Errorf("a failing call is the executor's error to report, got %q", out)
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
}
