package provider

import "testing"

func TestParseEffort(t *testing.T) {
	cases := map[string]Effort{
		"":        DefaultEffort,
		"off":     EffortOff,
		"none":    EffortOff,
		"minimal": EffortLow,
		"  LOW ":  EffortLow,
		"med":     EffortMedium,
		"medium":  EffortMedium,
		"High":    EffortHigh,
		"xhigh":   EffortXHigh,
		"x-high":  EffortXHigh,
		"max":     EffortMax,
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
	want := []Effort{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax, EffortOff}
	cur := EffortOff
	for i, expect := range want {
		cur = NextEffort(cur, nil)
		if cur != expect {
			t.Fatalf("step %d: got %v, want %v", i, cur, expect)
		}
	}
	// A model's own rungs are the walk; a level off the list restarts it.
	levels := Levels(Capabilities{Known: true, Reasoning: true})
	if got := NextEffort(EffortHigh, levels); got != EffortOff {
		t.Errorf("after high on a low/medium/high model: got %v, want off", got)
	}
	if got := NextEffort(EffortMax, levels); got != EffortOff {
		t.Errorf("a level the model lacks restarts the walk: got %v", got)
	}
}

func TestEffort_FitLowersToWhatTheModelHas(t *testing.T) {
	cases := []struct {
		name string
		in   Effort
		caps Capabilities
		want Effort
	}{
		{"unknown model is left alone", EffortMax, Capabilities{}, EffortMax},
		{"off stays off", EffortOff, Capabilities{Known: true, Reasoning: true, Max: true}, EffortOff},
		{"no reasoning knob means nothing sent", EffortHigh, Capabilities{Known: true}, EffortOff},
		{"max on a model with max", EffortMax, Capabilities{Known: true, Reasoning: true, XHigh: true, Max: true}, EffortMax},
		{"max without max falls to xhigh", EffortMax, Capabilities{Known: true, Reasoning: true, XHigh: true}, EffortXHigh},
		{"max without either falls to high", EffortMax, Capabilities{Known: true, Reasoning: true}, EffortHigh},
		{"xhigh without xhigh falls to high", EffortXHigh, Capabilities{Known: true, Reasoning: true, Max: true}, EffortHigh},
		{"medium is everywhere", EffortMedium, Capabilities{Known: true, Reasoning: true}, EffortMedium},
	}
	for _, tc := range cases {
		if got := tc.in.Fit(tc.caps); got != tc.want {
			t.Errorf("%s: Fit(%v) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestLevels_AreTheModelsRungs(t *testing.T) {
	got := Levels(Capabilities{Known: true, Reasoning: true, Max: true})
	want := []Effort{EffortOff, EffortLow, EffortMedium, EffortHigh, EffortMax}
	if len(got) != len(want) {
		t.Fatalf("levels = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("levels = %v, want %v", got, want)
		}
	}
	if got := Levels(Capabilities{}); len(got) != len(EffortCycle()) {
		t.Errorf("an unknown model offers every level, got %v", got)
	}
	if got := Levels(Capabilities{Known: true}); len(got) != 1 || got[0] != EffortOff {
		t.Errorf("a model without reasoning offers only off, got %v", got)
	}
}

func TestEffort_OpenAIEffortSpellsMaxAsXHigh(t *testing.T) {
	if got := EffortMax.OpenAIEffort(); got != "xhigh" {
		t.Errorf("max = %q, want xhigh — the API has no max", got)
	}
	if got := EffortXHigh.OpenAIEffort(); got != "xhigh" {
		t.Errorf("xhigh = %q", got)
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
