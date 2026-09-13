package plan

import (
	"strings"
	"testing"
)

const recordPlan = `## Plan: split read-only from plan

1. Add the mode
   files: internal/agent/mode.go
   action: edit
   note: beside the existing one
2. Draw its word
   files: internal/ui/chat/render.go
   action: edit
3. Read the goldens
   action: read
`

// A record is the plan and what was asked for, and nothing a tool printed.
func TestNewRecord_CarriesThePlanAndItsScope(t *testing.T) {
	rec := NewRecord("split read-only from plan mode", Parse(recordPlan), []string{"ev-00112233445566aa"})
	if rec.Task != "split read-only from plan mode" {
		t.Errorf("task = %q", rec.Task)
	}
	if rec.Title != "split read-only from plan" {
		t.Errorf("title = %q", rec.Title)
	}
	if len(rec.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(rec.Steps))
	}
	if rec.Steps[0].Action != "edit" || rec.Steps[0].Note != "beside the existing one" {
		t.Errorf("first step = %+v", rec.Steps[0])
	}
	want := []string{"internal/agent/mode.go", "internal/ui/chat/render.go"}
	if len(rec.Scope) != len(want) {
		t.Fatalf("scope = %v, want %v", rec.Scope, want)
	}
	for i, path := range want {
		if rec.Scope[i] != path {
			t.Errorf("scope[%d] = %q, want %q", i, rec.Scope[i], path)
		}
	}
	// A plan in the step shape keeps no prose: the steps are the plan, and
	// the text beside them would be the same plan twice in every seed.
	if rec.Text != "" {
		t.Errorf("a plan with steps should carry no prose, got %q", rec.Text)
	}
}

// A plan that never adopted the step shape is still a plan, and the record is
// the prose — bounded, because a model that answered with a file's contents
// would otherwise put them in the seed of every session the record opens.
func TestNewRecord_KeepsProseForAnUnstructuredPlan(t *testing.T) {
	rec := NewRecord("what would you do", Parse("I would rename the thing and then move it."), nil)
	if len(rec.Steps) != 0 || rec.Text == "" {
		t.Fatalf("unstructured record = %+v", rec)
	}
	long := NewRecord("q", Plan{Text: strings.Repeat("x", maxRecordText+500)}, nil)
	if len(long.Text) > maxRecordText+len("…") {
		t.Errorf("prose ran to %d bytes, past the bound", len(long.Text))
	}
}

// A response with no plan in it writes nothing down. A handle that promised a
// plan and answered with a task line would seed a session told it was
// carrying one.
func TestRecord_EmptyIsNotWrittenDown(t *testing.T) {
	if !NewRecord("q", Plan{}, nil).Empty() {
		t.Error("a plan with no steps and no prose should be empty")
	}
	if NewRecord("q", Parse(recordPlan), nil).Empty() {
		t.Error("a plan with steps is not empty")
	}
}

// The round trip is what the store depends on, and a record that could not
// seed a session is refused at the read rather than handed over half made.
func TestRecordRoundTrip(t *testing.T) {
	rec := NewRecord("split read-only from plan mode", Parse(recordPlan), []string{"ev-00112233445566aa"})
	data, err := MarshalRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	back, err := UnmarshalRecord(data)
	if err != nil {
		t.Fatal(err)
	}
	if back.Task != rec.Task || len(back.Steps) != len(rec.Steps) || back.Evidence[0] != rec.Evidence[0] {
		t.Fatalf("round trip lost content: %+v", back)
	}
	for _, bad := range [][]byte{[]byte(`{"steps":[{"number":1,"title":"x"}]}`), []byte(`{"task":"q"}`)} {
		if _, err := UnmarshalRecord(bad); err == nil {
			t.Errorf("a record that cannot seed a session should be refused: %s", bad)
		}
	}
}

// The prologue is the whole of what a fresh session is handed, so it has to
// say what was asked for, what was approved, and where the evidence is —
// without asking for the plan it is already carrying.
func TestRecordPrologue(t *testing.T) {
	rec := NewRecord("split read-only from plan mode", Parse(recordPlan), []string{"ev-00112233445566aa"})
	rec.Handle = "plan-0011223344556677"
	out := rec.Prologue()
	for _, want := range []string{
		"split read-only from plan mode",
		"1. Add the mode",
		"files: internal/agent/mode.go",
		"3. Read the goldens",
		"ev-00112233445566aa",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the prologue does not carry %q:\n%s", want, out)
		}
	}
	if strings.Contains(strings.ToLower(out), "present the plan") {
		t.Errorf("the prologue asks for a plan it is already carrying:\n%s", out)
	}
}
