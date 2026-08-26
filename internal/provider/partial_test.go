package provider

// What a broken stream keeps (S-107). The question every case here asks is
// the same one: is this call whole enough to run, or is it a fragment of a
// decision the model never finished making.

import (
	"errors"
	"io"
	"testing"
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

func TestBuildToolCalls_SkipsGapsInAPartialMap(t *testing.T) {
	// A stream that broke mid-accumulation can leave the index map with a
	// hole in it; the assembly walks past it rather than dereferencing it.
	calls := buildToolCalls(map[int]*toolCallAccumulator{
		0: {id: "a", name: "read_file", args: "{}"},
		2: {id: "c", name: "list_dir", args: "{}"},
	})
	if len(calls) != 1 || calls[0].ID != "a" {
		t.Fatalf("assembly should stop at the gap, got %+v", calls)
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
	for ev := range streamResponses(body, classify) {
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
