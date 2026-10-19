package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/rfizzle/agentcore/repeat"
	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/tools"
)

func TestRepeatShapes_AreAllSet(t *testing.T) {
	s := repeatShapes()
	if s.Question == nil || s.SearchScope == nil || s.Wrote == nil {
		t.Fatalf("a nil shape silently turns a signal off: %+v", s)
	}
}

func TestRepeatShapes_OneQuestionAskedTwiceIsOneQuestion(t *testing.T) {
	d := repeat.NewRepeatDetector(repeatShapes())
	q := json.RawMessage(`{"question":"Which store should the cache use?","shape":"choose",` +
		`"options":[{"label":"Postgres"},{"label":"SQLite"}]}`)
	if got := d.AskedBefore(ask.ToolName, q); got != "" {
		t.Fatalf("the first asking is not a repeat, got %q", got)
	}
	if got := d.AskedBefore(ask.ToolName, q); !strings.Contains(got, "2 times") {
		t.Errorf("the same question put twice should say so, got %q", got)
	}
	// What came back is no part of a question's key.
	if n := d.Note(ask.ToolName, q, `{"answered":"on the card"}`); n != 3 {
		t.Errorf("count = %d, want 3", n)
	}
	reworded := json.RawMessage(`{"question":"Which store should the cache use?","options":[{"label":"X"}]}`)
	if n := d.Note(ask.ToolName, reworded, ""); n != 4 {
		t.Errorf("a re-ask with the options reworded is the same question, count = %d", n)
	}
	other := json.RawMessage(`{"question":"Should the migration be reversible?"}`)
	if got := d.AskedBefore(ask.ToolName, other); got != "" {
		t.Errorf("a different question is not a repeat, got %q", got)
	}
}

func TestRepeatShapes_WhatWritesEndsASweep(t *testing.T) {
	s := repeatShapes()
	for _, c := range []struct {
		tool, args string
		want       bool
	}{
		{tools.ExecCommandName, `{"command":"ls"}`, true},
		{structural.GitWriteToolName, `{"verb":"commit"}`, true},
		{"write_file", `{"path":"a.go","content":"x"}`, true},
		{"read_file", `{"path":"a.go"}`, false},
		{ask.ToolName, `{"question":"Which store?"}`, false},
	} {
		if got := s.Wrote(c.tool, c.args); got != c.want {
			t.Errorf("Wrote(%s) = %v, want %v", c.tool, got, c.want)
		}
	}
}

func TestRepeatShapes_AQuestionNeitherEndsASweepNorStartsOne(t *testing.T) {
	d := repeat.NewRepeatDetector(repeatShapes())
	var last string
	for i := 1; i <= 12; i++ {
		if i == 6 {
			d.AskedBefore(ask.ToolName, json.RawMessage(`{"question":"Which store?"}`))
		}
		last = d.Notice("search",
			json.RawMessage(fmt.Sprintf(`{"pattern":"needle%d","path":"internal/ui/chat"}`, i)),
			fmt.Sprintf("a.go:%d: needle%d", i, i))
	}
	if !strings.HasPrefix(last, "[sweep:") {
		t.Errorf("a question in the middle of a sweep ends it:\n%s", last)
	}
	if got := d.Sweeps(); len(got) != 1 {
		t.Errorf("the question is part of no sweep of its own, got %v", got)
	}
}

func TestRepeatShapes_AGitWriteEndsASweep(t *testing.T) {
	d := repeat.NewRepeatDetector(repeatShapes())
	for i := 1; i <= 20; i++ {
		if i == 11 {
			d.Notice(structural.GitWriteToolName, json.RawMessage(`{"verb":"commit","message":"fix"}`), "committed")
		}
		out := d.Notice("search",
			json.RawMessage(fmt.Sprintf(`{"pattern":"needle%d","path":"internal/agent"}`, i)),
			fmt.Sprintf("out%d", i))
		if strings.HasPrefix(out, "[sweep:") {
			t.Fatalf("call %d: a commit starts the count again:\n%s", i, out)
		}
	}
}
