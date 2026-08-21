package agent

import "testing"

func TestParseMode(t *testing.T) {
	cases := []struct {
		in   string
		want Mode
	}{
		{"manual", ModeManual},
		{"Manual", ModeManual},
		{" accept-edits ", ModeAcceptEdits},
		{"accept_edits", ModeAcceptEdits},
		{"auto", ModeAuto},
		{"plan", ModePlan},
	}
	for _, c := range cases {
		got, err := ParseMode(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseMode(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	if _, err := ParseMode("yolo"); err == nil {
		t.Error("ParseMode should reject unknown names")
	}
	for _, m := range DefaultCycle() {
		round, err := ParseMode(m.String())
		if err != nil || round != m {
			t.Errorf("ParseMode(%q) did not round-trip: %v, %v", m.String(), round, err)
		}
	}
}

func TestParseCycle(t *testing.T) {
	cycle, err := ParseCycle([]string{"manual", "auto"})
	if err != nil || len(cycle) != 2 || cycle[0] != ModeManual || cycle[1] != ModeAuto {
		t.Fatalf("ParseCycle = %v, %v", cycle, err)
	}
	if got, err := ParseCycle(nil); err != nil || got != nil {
		t.Fatalf("empty cycle should parse to nil, got %v, %v", got, err)
	}
	if _, err := ParseCycle([]string{"manual", "bogus"}); err == nil {
		t.Fatal("ParseCycle should reject unknown names")
	}
}

func TestNextMode(t *testing.T) {
	if got := NextMode(nil, ModeManual); got != ModeAcceptEdits {
		t.Errorf("default cycle after manual = %v, want accept-edits", got)
	}
	if got := NextMode(nil, ModePlan); got != ModeManual {
		t.Errorf("default cycle should wrap plan → manual, got %v", got)
	}
	custom := []Mode{ModeManual, ModeAuto}
	if got := NextMode(custom, ModeAuto); got != ModeManual {
		t.Errorf("custom cycle should wrap, got %v", got)
	}
	// A mode outside the cycle enters it at the start.
	if got := NextMode(custom, ModePlan); got != ModeManual {
		t.Errorf("out-of-cycle mode should reset to cycle start, got %v", got)
	}
}

func TestModePolicy_Decide(t *testing.T) {
	edit := Action{Kind: ActionEdit}
	cmd := Action{Kind: ActionCommand, Command: "go test ./..."}
	flagged := Action{Kind: ActionCommand, Command: "git reset --hard", SafetyFlagged: true}
	other := Action{Kind: ActionOther}

	cases := []struct {
		name   string
		policy ModePolicy
		action Action
		want   Decision
		reason string
	}{
		{"manual asks for edits", ModePolicy{Mode: ModeManual}, edit, Ask, ""},
		{"manual asks for commands", ModePolicy{Mode: ModeManual}, cmd, Ask, ""},
		{"manual session grant allows edits", ModePolicy{Mode: ModeManual, AllowEdits: true}, edit, Allow, "session policy"},
		{"manual session grant allows commands", ModePolicy{Mode: ModeManual, AllowCommands: true}, cmd, Allow, "session policy"},
		{"manual allowlist allows commands", ModePolicy{Mode: ModeManual, CommandAllowlist: []string{"go test"}}, cmd, Allow, "allowlist"},
		{"accept-edits allows edits", ModePolicy{Mode: ModeAcceptEdits}, edit, Allow, "accept-edits mode"},
		{"accept-edits asks for commands", ModePolicy{Mode: ModeAcceptEdits}, cmd, Ask, ""},
		{"accept-edits asks for other tools", ModePolicy{Mode: ModeAcceptEdits}, other, Ask, ""},
		{"auto allows edits", ModePolicy{Mode: ModeAuto}, edit, Allow, "auto mode"},
		{"auto allowlist allows commands", ModePolicy{Mode: ModeAuto, CommandAllowlist: []string{"go test"}}, cmd, Allow, "allowlist"},
		{"auto asks for unlisted commands", ModePolicy{Mode: ModeAuto}, cmd, Ask, ""},
		{"auto asks for other tools", ModePolicy{Mode: ModeAuto}, other, Ask, ""},
		{"flagged command asks in auto", ModePolicy{Mode: ModeAuto, AllowCommands: true, CommandAllowlist: []string{"git reset"}}, flagged, Ask, ""},
		{"flagged command asks in accept-edits", ModePolicy{Mode: ModeAcceptEdits, AllowCommands: true}, flagged, Ask, ""},
		{"flagged command asks in manual", ModePolicy{Mode: ModeManual, AllowCommands: true}, flagged, Ask, ""},
		{"plan denies edits", ModePolicy{Mode: ModePlan, AllowEdits: true}, edit, Deny, "plan mode"},
		{"plan denies commands", ModePolicy{Mode: ModePlan, AllowCommands: true, CommandAllowlist: []string{"go test"}}, cmd, Deny, "plan mode"},
		{"plan denies flagged commands", ModePolicy{Mode: ModePlan}, flagged, Deny, "plan mode"},
		{"plan denies other tools", ModePolicy{Mode: ModePlan}, other, Deny, "plan mode"},
	}
	for _, c := range cases {
		got, reason := c.policy.Decide(c.action)
		if got != c.want || reason != c.reason {
			t.Errorf("%s: Decide = %v %q, want %v %q", c.name, got, reason, c.want, c.reason)
		}
	}
}
