package plan

import (
	"reflect"
	"strings"
	"testing"
)

// structured is the shape internal/prompt asks plan mode to emit, which is
// the case every other test here is a departure from.
const structured = `I looked at how rounds are counted and here is what I would do.

## Plan: make the round limit recoverable

1. Locate the round accounting
   files: internal/agent/loop.go, internal/agent/round.go
   action: read
2. Add a RoundsExhausted sentinel
   files: internal/agent/errors.go
   action: create
   note: new type, no signature changes
3. Return it from runRound and handle it in Run
   files: internal/agent/loop.go
4. Offer more rounds in the chat model
   files: internal/ui/chat/model.go
   action: edit
`

func TestParse_Structured(t *testing.T) {
	p := Parse(structured)
	if !p.Structured() {
		t.Fatal("the documented shape should parse to steps")
	}
	if p.Title != "make the round limit recoverable" {
		t.Errorf("title = %q", p.Title)
	}
	if len(p.Steps) != 4 {
		t.Fatalf("want 4 steps, got %d: %+v", len(p.Steps), p.Steps)
	}
	if got := p.Steps[0]; got.Action != Read || got.Title != "Locate the round accounting" {
		t.Errorf("step 1 = %+v", got)
	}
	if got := p.Steps[0].Paths; !reflect.DeepEqual(got, []string{"internal/agent/loop.go", "internal/agent/round.go"}) {
		t.Errorf("step 1 paths = %v", got)
	}
	if got := p.Steps[1]; got.Action != Create || got.Note != "new type, no signature changes" {
		t.Errorf("step 2 = %+v", got)
	}
	// A step that named files and no action is an edit — the default is
	// about what the given paths are for, never about inventing them.
	if got := p.Steps[2].Action; got != Edit {
		t.Errorf("step 3 action = %v, want edit", got)
	}
	if p.Text != structured {
		t.Error("Text should keep the response exactly as written")
	}
}

func TestParse_RadiusAggregates(t *testing.T) {
	p := Parse(structured)
	// loop.go is named by two steps and counted once; round.go is only read,
	// so it is not a write target at all.
	want := []string{"internal/agent/errors.go", "internal/agent/loop.go", "internal/ui/chat/model.go"}
	if got := p.WritePaths(); !reflect.DeepEqual(got, want) {
		t.Errorf("WritePaths() = %v, want %v", got, want)
	}
	if got := p.DeletePaths(); len(got) != 0 {
		t.Errorf("DeletePaths() = %v, want none", got)
	}
	if p.NeedsNetwork() || p.Runs() {
		t.Error("no step claimed the network or a command")
	}
}

func TestParse_DeletesRunsAndNetwork(t *testing.T) {
	p := Parse(`1. Drop the old shim
   files: internal/agent/shim.go
   action: delete
2. Pull the schema
   action: fetch
3. Run the suite
   action: test
`)
	if got := p.DeletePaths(); !reflect.DeepEqual(got, []string{"internal/agent/shim.go"}) {
		t.Errorf("DeletePaths() = %v", got)
	}
	// A delete is also a write: it is one of the things the plan does to the
	// workspace, and the touched count would be lying without it.
	if got := p.WritePaths(); len(got) != 1 {
		t.Errorf("WritePaths() = %v, want the deleted file", got)
	}
	if !p.NeedsNetwork() {
		t.Error("a fetch step needs the network")
	}
	if !p.Runs() {
		t.Error("a test step runs something")
	}
}

func TestParse_PathDecorations(t *testing.T) {
	p := Parse("1. Touch things\n   files: `a.go`, **b.go** · \"c.go\", none\n")
	want := []string{"a.go", "b.go", "c.go"}
	if got := p.Steps[0].Paths; !reflect.DeepEqual(got, want) {
		t.Errorf("paths = %v, want %v", got, want)
	}
}

func TestParse_MissingPathsAreOmittedNotGuessed(t *testing.T) {
	// The step names a file in its prose. Nothing may pick it up: a path the
	// model did not put in `files:` is not a path the model committed to.
	p := Parse("1. Rework the `internal/agent/loop.go` round counter\n   action: edit\n")
	if got := p.Steps[0].Paths; len(got) != 0 {
		t.Errorf("paths = %v, want none — a path in prose is not a supplied path", got)
	}
	if got := p.WritePaths(); len(got) != 0 {
		t.Errorf("WritePaths() = %v, want none", got)
	}
}

func TestParse_Unstructured(t *testing.T) {
	prose := "I'd add a sentinel error to the agent package and return it from the\nround loop, then handle it in the chat model."
	p := Parse(prose)
	if p.Structured() {
		t.Fatalf("prose should parse to no steps, got %+v", p.Steps)
	}
	if p.Text != prose {
		t.Error("the prose must survive for the card's fallback")
	}
	if p.WritePaths() != nil || p.NeedsNetwork() || p.Runs() {
		t.Error("an unstructured plan claims nothing")
	}
}

func TestParse_NumberedProseIsNotAStepList(t *testing.T) {
	// A list that does not start at 1 is something the model was counting,
	// not the plan.
	p := Parse("Three options here:\n\n2. the second one is cheapest\n3. the third is safest\n")
	if p.Structured() {
		t.Fatalf("a list starting at 2 should not be read as steps, got %+v", p.Steps)
	}
}

func TestParse_NestedListIsNotAStep(t *testing.T) {
	p := Parse(`1. Rework the loop
   files: internal/agent/loop.go
   1. first the counter
   2. then the sentinel
2. Handle it in chat
   files: internal/ui/chat/model.go
`)
	if len(p.Steps) != 2 {
		t.Fatalf("the indented sub-list must not become steps, got %d: %+v", len(p.Steps), p.Steps)
	}
	if p.Steps[1].Title != "Handle it in chat" {
		t.Errorf("step 2 = %q", p.Steps[1].Title)
	}
}

func TestParse_TitleOnlyFromAPlanHeading(t *testing.T) {
	cases := []struct{ in, want string }{
		{"## Plan: make it recoverable\n\n1. do the thing\n", "make it recoverable"},
		{"# Plan — make it recoverable\n\n1. do the thing\n", "make it recoverable"},
		{"## Findings\n\n1. do the thing\n", ""},
		{"1. do the thing\n", ""},
	}
	for _, c := range cases {
		if got := Parse(c.in).Title; got != c.want {
			t.Errorf("Parse(%q).Title = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParse_StepDecorations(t *testing.T) {
	p := Parse("1. **Add the sentinel:**\n2) Handle it\n")
	if len(p.Steps) != 2 {
		t.Fatalf("want 2 steps, got %+v", p.Steps)
	}
	if p.Steps[0].Title != "Add the sentinel" {
		t.Errorf("step 1 title = %q", p.Steps[0].Title)
	}
	if p.Steps[1].Number != 2 {
		t.Errorf("a `2)` step should keep its number, got %d", p.Steps[1].Number)
	}
}

func TestParse_UnknownActionLeavesTheDefault(t *testing.T) {
	// A word the parser does not know must not be forced into the nearest
	// match; the file default still applies.
	p := Parse("1. Do something\n   files: a.go\n   action: contemplate\n")
	if got := p.Steps[0].Action; got != Edit {
		t.Errorf("action = %v, want the files default (edit)", got)
	}
}

func TestParse_ExplicitReadKeepsItsFiles(t *testing.T) {
	p := Parse("1. Read them\n   files: a.go\n   action: read\n")
	if got := p.Steps[0].Action; got != Read {
		t.Errorf("action = %v, want read", got)
	}
	if got := p.WritePaths(); len(got) != 0 {
		t.Errorf("a read step's files are not write targets, got %v", got)
	}
}

func TestActionStrings(t *testing.T) {
	for _, c := range []struct {
		a      Action
		name   string
		writes bool
	}{
		{Read, "read", false},
		{Edit, "edit", true},
		{Create, "create", true},
		{Delete, "delete", true},
		{Run, "run", false},
		{Network, "network", false},
	} {
		if got := c.a.String(); got != c.name {
			t.Errorf("%d.String() = %q, want %q", c.a, got, c.name)
		}
		if got := c.a.Writes(); got != c.writes {
			t.Errorf("%s.Writes() = %v, want %v", c.name, got, c.writes)
		}
	}
}

func TestParse_EmptyResponse(t *testing.T) {
	p := Parse("")
	if p.Structured() || p.Title != "" || strings.TrimSpace(p.Text) != "" {
		t.Errorf("an empty response should parse to an empty plan, got %+v", p)
	}
}
