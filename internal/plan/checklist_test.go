package plan

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func tally(l Checklist) string {
	done, total, current := l.Tally()
	return fmt.Sprintf("%d/%d %q", done, total, current)
}

// call reads a steps call written as the model would write it, failing the
// test where the tool would have refused it.
func call(t *testing.T, args string) Checklist {
	t.Helper()
	l, err := ParseStepsCall(json.RawMessage(args))
	if err != nil {
		t.Fatalf("ParseStepsCall(%s): %v", args, err)
	}
	return l
}

// No list is nothing: a checklist nobody declared tallies to zero steps, which
// every surface draws as no block rather than as zero of zero — and an empty
// call is how a list is cleared.
func TestChecklist_NoListIsNothing(t *testing.T) {
	for _, l := range []Checklist{{}, call(t, `{"steps":[]}`)} {
		if got := tally(l); got != `0/0 ""` {
			t.Fatalf("tally = %s", got)
		}
		if l.Encode() != "" {
			t.Fatal("an empty checklist should store as nothing")
		}
	}
	if got := call(t, `{"steps":[]}`).Report(); got != "The working list is cleared." {
		t.Fatalf("an empty call should say the list is cleared, got %q", got)
	}
}

// A call names the whole list: numbered from 1 in the order given, marked
// where it says done, and on the first step it does not.
func TestStepsCall_NamesTheWholeList(t *testing.T) {
	l := call(t, `{"steps":[{"title":"Read the loop","done":true},{"title":"Patch the  limit"},{"title":"Run the tests"}]}`)
	if got := tally(l); got != `1/3 "Patch the limit"` {
		t.Fatalf("tally = %s", got)
	}
	for i, s := range l.Steps {
		if s.Number != i+1 {
			t.Fatalf("step %d is numbered %d", i, s.Number)
		}
	}
}

// The next call replaces the list whole, so a mark is taken back and a step
// reworded or dropped by sending the list as it now stands — the three things
// the text grammar the tool replaced could not do.
func TestStepsCall_ReplacesTheListWhole(t *testing.T) {
	first := call(t, `{"steps":[{"title":"Read","done":true},{"title":"Patch","done":true},{"title":"Test"}]}`)
	second := call(t, `{"steps":[{"title":"Read","done":true},{"title":"Patch the ceiling"},{"title":"Test"}]}`)
	if got := tally(second); got != `1/3 "Patch the ceiling"` {
		t.Fatalf("a taken-back mark and a reworded step should read 1/3 on the step, got %s", got)
	}
	if got := tally(first); got != `2/3 "Test"` {
		t.Fatalf("a later call moved the earlier list: %s", got)
	}
}

// A call the tool refuses says why, in words the model can act on.
func TestStepsCall_RefusesWhatItCannotDraw(t *testing.T) {
	long := `{"steps":[` + strings.TrimSuffix(strings.Repeat(`{"title":"x"},`, MaxWorkingSteps+1), ",") + `]}`
	for args, want := range map[string]string{
		long:                         "holds at most",
		`{"steps":[{"title":"  "}]}`: "step 1 has no title",
		`{"steps":"read the loop"}`:  "invalid arguments",
	} {
		if _, err := ParseStepsCall(json.RawMessage(args)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseStepsCall(%.40s) = %v, want an error saying %q", args, err, want)
		}
	}
}

// The result is the list with the numbers it is drawn by, the step being
// worked on pointed at, so the model carries on from what is stored.
func TestStepsCall_ReportsTheListAsItStands(t *testing.T) {
	got := call(t, `{"steps":[{"title":"Read","done":true},{"title":"Patch"},{"title":"Test"}]}`).Report()
	for _, want := range []string{"1 of 3 done — on: Patch", "  [x] 1. Read", "→ [ ] 2. Patch", "  [ ] 3. Test"} {
		if !strings.Contains(got, want) {
			t.Errorf("the report lacks %q:\n%s", want, got)
		}
	}
}

// What a slot stores is what comes back.
func TestChecklist_EncodeRoundTrips(t *testing.T) {
	l := call(t, `{"steps":[{"title":"Read","done":true},{"title":"Patch","done":true},{"title":"Retest"}]}`)
	back := DecodeChecklist(l.Encode())
	if tally(back) != tally(l) || back.Steps[2].Number != 3 {
		t.Fatalf("round trip = %s %+v, want %s", tally(back), back.Steps, tally(l))
	}
	for _, junk := range []string{"", "{", `{"steps":[]}`} {
		if _, total, _ := DecodeChecklist(junk).Tally(); total != 0 {
			t.Fatalf("%q decoded to a list", junk)
		}
	}
}

// The paths a step said it would touch are kept with it, through a slot as
// well, because the steps screen draws them under the step.
func TestChecklist_KeepsTheStepsPaths(t *testing.T) {
	l := call(t, `{"steps":[{"title":"Patch the loop","paths":["loop.go"," round.go ",""]},{"title":"Test it"}]}`)
	for _, got := range []Checklist{l, DecodeChecklist(l.Encode())} {
		if p := got.Steps[0].Paths; len(p) != 2 || p[0] != "loop.go" || p[1] != "round.go" {
			t.Fatalf("paths = %q", p)
		}
		if len(got.Steps[1].Paths) != 0 {
			t.Fatalf("a step that named no files was given some: %q", got.Steps[1].Paths)
		}
	}
}

// Every other tool passes through the wrap untouched.
func TestWrapStepsExecutor_PassesEveryOtherToolOn(t *testing.T) {
	exec := WrapStepsExecutor(func(name string, _ json.RawMessage) (string, error) { return "ran " + name, nil })
	if got, err := exec("read_file", nil); err != nil || got != "ran read_file" {
		t.Fatalf("read_file = %q, %v", got, err)
	}
	if got, err := exec(StepsToolName, json.RawMessage(`{"steps":[{"title":"Read"}]}`)); err != nil || !strings.Contains(got, "0 of 1 done") {
		t.Fatalf("steps = %q, %v", got, err)
	}
}
