package plan

import (
	"slices"
	"testing"
)

func TestProgress_ReadsEveryMarkInOrder(t *testing.T) {
	text := "Step one is in.\nprogress: 1\n\n- progress: step 2 done\n**progress:** 3\nPROGRESS: #4\nmy progress: 5 is not a line of its own"
	if got, want := Progress(text), []int{1, 2, 3, 4}; !slices.Equal(got, want) {
		t.Fatalf("Progress = %v, want %v", got, want)
	}
}

func TestProgress_ALineWithNoNumberMarksNothing(t *testing.T) {
	for _, text := range []string{"progress: done", "progress:", "progress: step three", "no marks here"} {
		if got := Progress(text); len(got) != 0 {
			t.Errorf("Progress(%q) = %v, want nothing", text, got)
		}
	}
}

// TestProgress_APlanWithItsMarksStillParses holds the two halves of the
// grammar apart: a progress line under a step is a continuation line Parse
// ignores, so a plan and its first mark in one message are still the plan.
func TestProgress_APlanWithItsMarksStillParses(t *testing.T) {
	text := "1. Read the loop\n2. Change it\nprogress: 1"
	p := Parse(text)
	if len(p.Steps) != 2 || p.Steps[1].Title != "Change it" {
		t.Fatalf("steps = %+v, want the two steps", p.Steps)
	}
	if got := Progress(text); !slices.Equal(got, []int{1}) {
		t.Fatalf("Progress = %v, want [1]", got)
	}
}
