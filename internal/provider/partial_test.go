package provider

// What a broken stream keeps. The question every case here asks is
// the same one: is this call whole enough to run, or is it a fragment of a
// decision the model never finished making.

import (
	"errors"
	"io"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestCompletedToolCalls_KeepsOnlyWholeCalls(t *testing.T) {
	got := CompletedToolCalls([]ToolCall{
		{ID: "1", Name: "read_file", Arguments: `{"path":"a.go"}`},
		{ID: "2", Name: "edit_file", Arguments: `{"path":"b.go","old`}, // cut mid-write
		{ID: "", Name: "read_file", Arguments: `{}`},                   // no id to answer
		{ID: "4", Name: "", Arguments: `{}`},                           // no name to dispatch
		{ID: "5", Name: "list_dir", Arguments: ""},                     // no arguments is whole
	})
	if len(got) != 2 {
		t.Fatalf("kept %d calls, want the two that were finished: %+v", len(got), got)
	}
	if got[0].ID != "1" || got[1].ID != "5" {
		t.Errorf("kept the wrong calls: %+v", got)
	}
}

func TestCompletedToolCalls_EmptyIsNil(t *testing.T) {
	if got := CompletedToolCalls(nil); got != nil {
		t.Errorf("nothing in, nothing out, got %+v", got)
	}
}

func TestToolCallSet_AssemblesEveryCallItOpened(t *testing.T) {
	// A stream that broke mid-accumulation leaves calls in both states: one
	// the model finished, and one opened by an index whose id never arrived.
	// Both are assembled, in the order they were opened, and which of them is
	// whole enough to run is judged once, afterwards.
	set := newToolCallSet()
	first, second := 1, 2
	for _, chunk := range []openai.ToolCall{
		{Index: &first, ID: "a", Function: openai.FunctionCall{Name: "read_file", Arguments: "{}"}},
		{Index: &second, Function: openai.FunctionCall{Arguments: `{"path":`}},
	} {
		if _, err := set.accumulate(chunk); err != nil {
			t.Fatalf("accumulating %+v: %v", chunk, err)
		}
	}

	calls := set.calls()
	if len(calls) != 2 || calls[0].ID != "a" || calls[1].ID != "" {
		t.Fatalf("both calls should survive the assembly in order, got %+v", calls)
	}
	if whole := CompletedToolCalls(calls); len(whole) != 1 || whole[0].ID != "a" {
		t.Fatalf("only the finished call is runnable, got %+v", whole)
	}
}

// errReader fails partway through, standing in for a connection that dropped.
type errReader struct {
	data []byte
	err  error
	sent bool
}

func (r *errReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		n := copy(p, r.data)
		return n, nil
	}
	return 0, r.err
}

func (r *errReader) Close() error { return nil }

func TestStreamResponses_BrokenStreamKeepsTheFinishedCalls(t *testing.T) {
	// One completed function-call item, then the connection dies.
	body := &errReader{
		data: []byte(`data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"a.go\"}"}}` + "\n\n"),
		err:  io.ErrUnexpectedEOF,
	}
	classify := newClassifier("openai", "OPENAI_API_KEY", "sk-xxxx1234")

	var last StreamEvent
	for ev := range streamResponses(body, classify, &idleWatch{}) {
		last = ev
	}
	if last.Err == nil {
		t.Fatal("a dropped connection is still a failure")
	}
	if len(last.ToolCalls) != 1 || last.ToolCalls[0].Name != "read_file" {
		t.Fatalf("the finished call should travel with the failure, got %+v", last.ToolCalls)
	}
	f, ok := AsFailure(last.Err)
	if !ok {
		t.Fatalf("the failure should still be classified, got %T", last.Err)
	}
	if !errors.Is(f, ErrNetwork) {
		t.Errorf("class = %q, want network", f.Class)
	}
}
