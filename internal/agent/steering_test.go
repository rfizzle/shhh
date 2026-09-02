package agent

import (
	"strings"
	"testing"
)

// The built-in set is what a zero Steering runs, so a surface that
// configures nothing is word for word what the package documents — and the
// defaults cannot rot behind the override path, which is the failure a
// configurable wording invites.
func TestSteering_ZeroValueIsTheBuiltInSet(t *testing.T) {
	var s Steering
	if got, want := s.checkInPrompt(12, FinishedInSession), CheckInPrompt(12, FinishedInSession); got != want {
		t.Fatalf("the zero value must ask the built-in check-in:\n%s", got)
	}
	if got, want := s.steerPrompt("build the exporter", "editing elsewhere"),
		SteerPrompt("build the exporter", "editing elsewhere"); got != want {
		t.Fatalf("the zero value must send the built-in steer:\n%s", got)
	}
	if s.doublings() != DefaultCheckInDoublings {
		t.Fatalf("doublings = %d, want the built-in %d", s.doublings(), DefaultCheckInDoublings)
	}
}

// Each threshold overrides its constant, and each unset one keeps it.
func TestSteering_DoublingsOverrideAndDefault(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  int
		want int
	}{
		{"unset keeps the built-in bound", 0, DefaultCheckInDoublings},
		{"a count is taken as written", 4, 4},
		{"a negative fixes the interval", -1, 0},
	} {
		if got := (Steering{CheckInDoublings: tc.set}).doublings(); got != tc.want {
			t.Errorf("%s: doublings = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// The interval widens with the configured count and stops there, and a
// negative count leaves it flat for the whole turn.
func TestCheckInInterval_WidensAsFarAsConfigured(t *testing.T) {
	a := New(nil, nil)
	a.SetSteering(Steering{CheckInInterval: 10, CheckInDoublings: 1})
	if got := a.checkInInterval(); got != 10 {
		t.Fatalf("first interval = %d, want 10", got)
	}
	a.checkIns = 1
	if got := a.checkInInterval(); got != 20 {
		t.Fatalf("after one check-in = %d, want 20", got)
	}
	a.checkIns = 5
	if got := a.checkInInterval(); got != 20 {
		t.Fatalf("the widening stops at the configured count, got %d", got)
	}

	flat := New(nil, nil)
	flat.SetSteering(Steering{CheckInInterval: 10, CheckInDoublings: -1})
	flat.checkIns = 5
	if got := flat.checkInInterval(); got != 10 {
		t.Fatalf("a fixed interval = %d, want 10", got)
	}
}

// The bound on the quoted instruction is the setting's, not the wording's:
// the built-in bound stands unset, a number replaces it, and a negative
// quotes whatever the user typed.
func TestSteering_QuotedTargetBound(t *testing.T) {
	long := strings.Repeat("x", 900)
	for _, tc := range []struct {
		name string
		set  int
		want int
	}{
		{"unset keeps the built-in bound", 0, DefaultSteerTargetChars},
		{"a bound is taken as written", 40, 40},
	} {
		// clampRunes spends the last rune of the budget on the ellipsis.
		got := (Steering{SteerTargetChars: tc.set}).steerPrompt(long, "")
		if !strings.Contains(got, strings.Repeat("x", tc.want-1)+"…") || strings.Contains(got, strings.Repeat("x", tc.want)) {
			t.Errorf("%s: the steer does not quote %d characters of the instruction", tc.name, tc.want)
		}
	}
	whole := (Steering{SteerTargetChars: -1}).steerPrompt(long, "")
	if !strings.Contains(whole, long) {
		t.Error("a negative bound must quote the instruction whole")
	}
}

// An overriding wording is what the turn is sent, with its substitutions
// filled from the same values the built-in one names.
func TestSteering_OverridesCarryTheirValues(t *testing.T) {
	s := Steering{
		CheckIn: "rounds so far: " + PlaceholderRounds + ". " + PlaceholderFinished,
		Steer:   "asked for " + PlaceholderTarget + "; noticed " + PlaceholderReason,
	}
	got := s.checkInPrompt(31, FinishedAsSubAgent)
	if got != "rounds so far: 31. "+FinishedAsSubAgent {
		t.Fatalf("check-in override: %q", got)
	}
	got = s.steerPrompt("  build the exporter  ", " editing elsewhere ")
	if got != "asked for build the exporter; noticed editing elsewhere" {
		t.Fatalf("steer override: %q", got)
	}

	// Every route to a check-in goes through the same wording, including the
	// one a caller with its own reason takes — a child's round cap.
	a := New(nil, nil)
	a.SetSteering(s)
	a.rounds = 31
	if got := a.CheckInMessage(FinishedAsSubAgent); got != "rounds so far: 31. "+FinishedAsSubAgent {
		t.Fatalf("check-in for a caller with its own reason: %q", got)
	}
	if got := a.ForceCheckIn(); got != "rounds so far: 31. "+FinishedInSession {
		t.Fatalf("forced check-in: %q", got)
	}
}

// A misspelled substitution is refused, because it is otherwise invisible:
// it reaches the model as literal braces and the value never arrives.
func TestValidatePlaceholders(t *testing.T) {
	if err := ValidateCheckIn("rounds: " + PlaceholderRounds + " " + PlaceholderFinished); err != nil {
		t.Fatalf("the placeholders a check-in takes must be accepted: %v", err)
	}
	err := ValidateSteer("asked for {{targt}}")
	if err == nil {
		t.Fatal("a placeholder that does not exist must be refused")
	}
	if !strings.Contains(err.Error(), "{{targt}}") || !strings.Contains(err.Error(), PlaceholderTarget) {
		t.Fatalf("the error names what was written and what exists, got %q", err)
	}
	// Each wording takes its own: a steer's placeholder in a check-in is as
	// invisible as a misspelling, and is refused the same way.
	if err := ValidateCheckIn(PlaceholderTarget); err == nil {
		t.Fatal("a check-in must refuse the steer's placeholders")
	}
	if err := ValidateVerbatim("nothing to fill in here"); err != nil {
		t.Fatalf("prose with no placeholders is fine: %v", err)
	}
	if err := ValidateVerbatim(PlaceholderTarget); err == nil {
		t.Fatal("a wording that takes no placeholders must refuse one")
	}
}

// The reading instruction and the classifier's are the built-in literals
// until a file replaces them.
func TestAuxiliaryPromptOverrides(t *testing.T) {
	if got := (SummaryConfig{}).prompt(); got != summaryPrompt {
		t.Error("an unset reading instruction must be the built-in one")
	}
	if got := (SummaryConfig{Prompt: "read it"}).prompt(); got != "read it" {
		t.Errorf("reading instruction override: %q", got)
	}
	if got := (ClassifierConfig{}).prompt(); got != classifierPrompt {
		t.Error("an unset classifier instruction must be the built-in one")
	}
	if got := (ClassifierConfig{Prompt: "decide"}).prompt(); got != "decide" {
		t.Errorf("classifier instruction override: %q", got)
	}
}

// The cooldown is counted in reading intervals, and the count is a setting.
func TestSummaryConfig_CooldownIntervals(t *testing.T) {
	if got := (SummaryConfig{}).CooldownIntervals(); got != DefaultCooldownIntervals {
		t.Errorf("unset cooldown = %d, want %d", got, DefaultCooldownIntervals)
	}
	if got := (SummaryConfig{InterveneCooldownIntervals: 5}).CooldownIntervals(); got != 5 {
		t.Errorf("configured cooldown = %d, want 5", got)
	}
}
