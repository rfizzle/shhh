package provider

import "testing"

func TestParseEffort(t *testing.T) {
	cases := map[string]Effort{
		"":        EffortOff,
		"off":     EffortOff,
		"none":    EffortOff,
		"minimal": EffortOff,
		"  LOW ":  EffortLow,
		"med":     EffortMedium,
		"medium":  EffortMedium,
		"High":    EffortHigh,
		"max":     EffortHigh,
	}
	for in, want := range cases {
		got, err := ParseEffort(in)
		if err != nil {
			t.Fatalf("ParseEffort(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("ParseEffort(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := ParseEffort("ultra"); err == nil {
		t.Error("expected an error for an unknown level, not a silent off")
	}
}

func TestNextEffort_Cycles(t *testing.T) {
	want := []Effort{EffortLow, EffortMedium, EffortHigh, EffortOff}
	cur := EffortOff
	for i, expect := range want {
		cur = NextEffort(cur)
		if cur != expect {
			t.Fatalf("step %d: got %v, want %v", i, cur, expect)
		}
	}
}

func TestEffort_OpenAIEffortOffSendsNothing(t *testing.T) {
	if got := EffortOff.OpenAIEffort(); got != "" {
		t.Errorf("off must send no effort field, got %q", got)
	}
	if got := EffortHigh.OpenAIEffort(); got != "high" {
		t.Errorf("high = %q", got)
	}
}

func TestEffort_ThinkingBudget(t *testing.T) {
	if got := EffortOff.ThinkingBudget(0); got != 0 {
		t.Errorf("off must ask for no budget, got %d", got)
	}
	if got := EffortHigh.ThinkingBudget(0); got != 24576 {
		t.Errorf("uncapped high = %d", got)
	}
	// A ceiling clamps rather than overshoots.
	if got := EffortHigh.ThinkingBudget(8000); got != 8000 {
		t.Errorf("clamped high = %d, want the ceiling", got)
	}
	// A ceiling below the API's minimum means no thinking at all, not a
	// budget the API would refuse.
	if got := EffortHigh.ThinkingBudget(MinThinkingBudget - 1); got != 0 {
		t.Errorf("ceiling under the minimum = %d, want 0", got)
	}
}
