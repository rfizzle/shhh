package agent

import (
	"strings"
	"testing"
)

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
		{"plan allows inspection commands", ModePolicy{Mode: ModePlan}, Action{Kind: ActionCommand, Command: "git status"}, Allow, "plan mode inspection"},
		{"plan denies flagged inspection commands", ModePolicy{Mode: ModePlan}, Action{Kind: ActionCommand, Command: "git diff", SafetyFlagged: true}, Deny, "plan mode"},
	}
	for _, c := range cases {
		got, reason := c.policy.Decide(c.action)
		if got != c.want || reason != c.reason {
			t.Errorf("%s: Decide = %v %q, want %v %q", c.name, got, reason, c.want, c.reason)
		}
	}
}

func TestPlanInspectionAllowed(t *testing.T) {
	cases := []struct {
		command string
		want    bool
	}{
		{"git status", true},
		{"git log --oneline -5", true},
		{"ls -la internal", true},
		{"cat go.mod", true},
		{"rg TODO internal", true},
		{"go list ./...", true},
		{"go test ./...", false},            // runs code, not inspection
		{"rm -rf /tmp/x", false},            // not on the list
		{"find . -delete", false},           // can mutate, deliberately excluded
		{"cat go.mod > /tmp/out", false},    // redirection
		{"git status; rm -rf ~", false},     // chaining
		{"ls $(evil)", false},               // substitution
		{"git diff | tee patch.txt", false}, // pipe
	}
	for _, c := range cases {
		if got := PlanInspectionAllowed(c.command); got != c.want {
			t.Errorf("PlanInspectionAllowed(%q) = %v, want %v", c.command, got, c.want)
		}
	}
}

func TestClampMode(t *testing.T) {
	cases := []struct {
		mode, ceiling, want Mode
	}{
		{ModeAuto, ModeAuto, ModeAuto},
		{ModeAuto, ModeAcceptEdits, ModeAcceptEdits},
		{ModeAuto, ModeManual, ModeManual},
		{ModeAuto, ModePlan, ModePlan},
		{ModeAcceptEdits, ModeAuto, ModeAcceptEdits},
		{ModeManual, ModeAcceptEdits, ModeManual},
		{ModePlan, ModeAuto, ModePlan},
		{ModeManual, ModePlan, ModePlan},
	}
	for _, c := range cases {
		if got := ClampMode(c.mode, c.ceiling); got != c.want {
			t.Errorf("ClampMode(%v, %v) = %v, want %v", c.mode, c.ceiling, got, c.want)
		}
	}
}

func TestReadOnlyAllowed_Guards(t *testing.T) {
	cases := []struct {
		command string
		want    bool
	}{
		{"find . -name '*.go'", true},
		{"find . -delete", false},
		{"find . -exec rm {} ;", false},
		{"fd --extension go", true},
		{"fd -x rm", false},
		{"sort go.mod", true},
		{"sort -o out.txt go.mod", false},
		{"tree internal", true},
		{"tree -o out.html", false},
		{"git branch", true},
		{"git branch --list", true},
		{"git branch -D master", false},
		{"env", true},
		{"env FOO=bar rm -rf /", false},
		{"whoami", true},
	}
	for _, c := range cases {
		if got := ReadOnlyAllowed(c.command, nil); got != c.want {
			t.Errorf("ReadOnlyAllowed(%q) = %v, want %v", c.command, got, c.want)
		}
	}
	// Extra entries are the user's own call and bypass the built-in guards.
	if !ReadOnlyAllowed("make lint", []string{"make lint"}) {
		t.Error("extra entries should be honored")
	}
}

func TestDecide_ReadOnlyNeverPrompts(t *testing.T) {
	inspect := Action{Kind: ActionCommand, Command: "git status"}
	for _, mode := range []Mode{ModeManual, ModeAcceptEdits, ModeAuto} {
		p := ModePolicy{Mode: mode}
		if got, reason := p.Decide(inspect); got != Allow || reason != "read-only" {
			t.Errorf("%s: inspection should auto-run, got %v %q", mode, got, reason)
		}
	}
	// Safety-flagged commands still ask, even if they look like inspection.
	flagged := Action{Kind: ActionCommand, Command: "cat /etc/shadow", SafetyFlagged: true}
	if got, _ := (ModePolicy{Mode: ModeManual}).Decide(flagged); got != Ask {
		t.Errorf("safety-flagged inspection should ask, got %v", got)
	}
	// Disabling the built-in list restores prompting.
	off := ModePolicy{Mode: ModeManual, ReadOnlyDisabled: true}
	if got, _ := off.Decide(inspect); got != Ask {
		t.Errorf("read_only_auto=false should prompt, got %v", got)
	}
	// Plan mode grants inspection regardless of the toggle.
	plan := ModePolicy{Mode: ModePlan, ReadOnlyDisabled: true}
	if got, reason := plan.Decide(inspect); got != Allow || reason != "plan mode inspection" {
		t.Errorf("plan mode should still inspect, got %v %q", got, reason)
	}
}

// The working scope is a second question the mode does not answer:
// a permissive mode was granted over the work, not over the whole disk.
func TestDecideAsksForPathsOutsideTheWorkingScope(t *testing.T) {
	edit := Action{Kind: ActionEdit, Path: "/elsewhere/config.toml", OutOfScope: []string{"/elsewhere"}}
	for _, mode := range []Mode{ModeAcceptEdits, ModeAuto} {
		p := ModePolicy{Mode: mode}
		if got, _ := p.Decide(edit); got != Ask {
			t.Errorf("%v mode allowed an edit outside the working scope (%v)", mode, got)
		}
	}
	// The same edit inside the scope is the one the mode does answer.
	inScope := Action{Kind: ActionEdit, Path: "/work/main.go"}
	if got, _ := (ModePolicy{Mode: ModeAcceptEdits}).Decide(inScope); got != Allow {
		t.Errorf("accept-edits should still allow an in-scope edit, got %v", got)
	}
}

func TestDecideAsksForCommandsOutsideTheWorkingScopeDespiteTheAllowlist(t *testing.T) {
	p := ModePolicy{Mode: ModeManual, CommandAllowlist: []string{"cp"}}
	a := Action{Kind: ActionCommand, Command: "cp a.txt /elsewhere/a.txt", OutOfScope: []string{"/elsewhere"}}
	if got, _ := p.Decide(a); got != Ask {
		t.Errorf("an allowlisted command writing outside the scope should ask, got %v", got)
	}
	if got, _ := p.Decide(Action{Kind: ActionCommand, Command: "cp a.txt b.txt"}); got != Allow {
		t.Error("the allowlist still answers for a command that stays in scope")
	}
}

func TestDecideRefusesMaskedPathsInEveryMode(t *testing.T) {
	a := Action{
		Kind: ActionEdit, Path: "/home/u/.ssh/config",
		OutOfScope: []string{"/home/u/.ssh"}, ScopeRefused: true,
		ScopeReason: "contained commands mask /home/u/.ssh, and the mask cannot be disabled",
	}
	for _, mode := range []Mode{ModeManual, ModeAcceptEdits, ModeAuto} {
		decision, reason := (ModePolicy{Mode: mode, AllowEdits: true}).Decide(a)
		if decision != Deny {
			t.Errorf("%v mode = %v for a masked path; want Deny", mode, decision)
		}
		if !strings.Contains(reason, "mask") {
			t.Errorf("the refusal should say why, got %q", reason)
		}
	}
	if result := ScopeRefusedResult("masked"); !strings.HasPrefix(result, "error:") || !strings.Contains(result, "/add-dir") {
		t.Errorf("the tool result should name the boundary and the way to widen it, got %q", result)
	}
}

func TestResolveAutoWillNotWidenTheScopeOnTheUsersBehalf(t *testing.T) {
	allow := ClassifierVerdict{Decision: Allow, Reason: "routine"}
	sensitive := Action{Kind: ActionCommand, Command: "cp x ~/.kube/config",
		OutOfScope: []string{"/home/u/.kube"}, ScopeSensitive: true, ScopeReason: "/home/u/.kube holds credentials"}
	if got, reason := ResolveAuto(sensitive, allow); got != Ask || reason == "" {
		t.Errorf("a classifier allow over a sensitive directory = %v (%q); want Ask with a reason", got, reason)
	}
	refused := Action{Kind: ActionCommand, ScopeRefused: true, ScopeReason: "masked"}
	if got, _ := ResolveAuto(refused, allow); got != Deny {
		t.Errorf("a classifier allow over a masked path = %v; want Deny", got)
	}
	ordinary := Action{Kind: ActionCommand, Command: "go build", OutOfScope: []string{"/elsewhere"}}
	if got, _ := ResolveAuto(ordinary, allow); got != Allow {
		t.Error("an ordinary directory is exactly what auto mode may answer for")
	}
}

// The deny list is the one answer no mode changes, and it is read before
// anything that could allow: a permissive mode, a blanket session grant, the
// allowlist, and the built-in read-only list are all downstream of it.
func TestDecideRefusesADeniedCommandInEveryMode(t *testing.T) {
	p := ModePolicy{
		CommandDenylist:  []string{"git push", "terraform apply"},
		CommandAllowlist: []string{"git push"},
		AllowCommands:    true,
	}
	commands := []string{
		"git push origin main",
		"git push --force",
		"sudo terraform apply -auto-approve",
		"go build && git push",
		"echo done; git push",
		"terraform apply",
	}
	for _, mode := range []Mode{ModeManual, ModeAcceptEdits, ModeAuto, ModePlan} {
		p.Mode = mode
		for _, command := range commands {
			decision, reason := p.Decide(Action{Kind: ActionCommand, Command: command})
			if decision != Deny {
				t.Errorf("%v mode, %q = %v; want Deny", mode, command, decision)
			}
			if reason != DenyReasonDenylist {
				t.Errorf("%v mode, %q gave reason %q; want the deny list named", mode, command, reason)
			}
		}
	}
}

// Deny beats allow, and it beats the read-only list too: a command a person
// has refused is refused however innocent the verb in front of it reads.
func TestDecideDenyBeatsEveryGrant(t *testing.T) {
	p := ModePolicy{
		Mode:             ModeAuto,
		CommandDenylist:  []string{"git", "ls"},
		CommandAllowlist: []string{"git status"},
		AllowCommands:    true,
	}
	for _, command := range []string{"git status", "ls -la"} {
		if decision, _ := p.Decide(Action{Kind: ActionCommand, Command: command}); decision != Deny {
			t.Errorf("%q = %v; want Deny", command, decision)
		}
	}
	// An empty list refuses nothing — auto mode hands the same command to
	// the classifier, which is the question the list exists to skip.
	open := ModePolicy{Mode: ModeAuto}
	if decision, _ := open.Decide(Action{Kind: ActionCommand, Command: "git push"}); decision != Ask {
		t.Errorf("an empty deny list refused a command anyway: %v", decision)
	}
	// An edit is not a command, and the command list does not answer for one.
	edit := ModePolicy{Mode: ModeAcceptEdits, CommandDenylist: []string{"rm"}}
	if decision, _ := edit.Decide(Action{Kind: ActionEdit, Path: "rm/notes.md"}); decision != Allow {
		t.Errorf("the command deny list answered for an edit: %v", decision)
	}
}

// The list matches a command wherever it sits in a chain, and it reads the
// escalated spelling as the command it escalates. An allowlist refuses to
// match a chain and the reader is asked; a deny list that did the same would
// be walked around with two characters.
func TestDenylistMatchesReadsEveryCommandInTheLine(t *testing.T) {
	deny := []string{"git push", "terraform apply"}
	matched := []string{
		"git push",
		"git push origin main",
		"go test ./... && git push",
		"go test ./...; git push",
		"go test ./... || git push",
		"echo $(git push)",
		"sudo terraform apply",
		"env TF_LOG=debug terraform apply",
		"true\ngit push",
		"go build | git push",
	}
	for _, command := range matched {
		if !DenylistMatches(deny, command) {
			t.Errorf("DenylistMatches(%q) = false, want true", command)
		}
	}
	clear := []string{
		"git status",
		"git pushall",
		"go test ./...",
		"terraform plan",
		"echo git push is denied here",
	}
	for _, command := range clear {
		if DenylistMatches(deny, command) {
			t.Errorf("DenylistMatches(%q) = true, want false", command)
		}
	}
	if DenylistMatches(nil, "git push") {
		t.Error("an empty deny list matched something")
	}
}

// The refusal the model reads names no key and points at no file: the list
// is the user's, and a refusal carrying the instructions for editing it
// would be handing the model the way around it.
func TestDenylistResultTellsTheModelToStopRatherThanHowToEditTheList(t *testing.T) {
	if !strings.HasPrefix(DenylistResult, "error:") {
		t.Errorf("the tool result should read as an error, got %q", DenylistResult)
	}
	for _, leak := range []string{"command_denylist", "config.toml", "behavior."} {
		if strings.Contains(DenylistResult, leak) {
			t.Errorf("the tool result names %q, which the model can act on: %q", leak, DenylistResult)
		}
	}
}

// The two spellings a chain-aware split alone would miss: a command reached
// by its path, and a command handed to an interpreter as an argument.
func TestDenylistMatchesReadsAPathAndAnInterpretersArgument(t *testing.T) {
	deny := []string{"git push", "terraform apply", "./scripts/deploy.sh"}
	matched := []string{
		"/usr/bin/git push origin main",
		"sudo /usr/local/bin/terraform apply",
		`sh -c "git push"`,
		"bash -c 'terraform apply -auto-approve'",
		`/bin/bash -lc "cd /tmp && git push"`,
		"./scripts/deploy.sh --prod",
		// An escalation carries its own options, and shhh cannot tell the
		// value of one from the command behind it without knowing sudo's
		// table — so the list is offered every word after the escalation.
		"sudo -E git push",
		"sudo -u deploy terraform apply",
		"env -i git push",
		// A search hands the rest of the line to another program.
		`find . -name '*.tf' -exec terraform apply {} \;`,
		`eval "git push"`,
	}
	for _, command := range matched {
		if !DenylistMatches(deny, command) {
			t.Errorf("DenylistMatches(%q) = false, want true", command)
		}
	}
	// Quoting is ignored only inside an interpreter's arguments: everywhere
	// else a quoted mention of a denied command is a mention.
	clear := []string{
		`git commit -m "do not git push yet"`,
		"grep -rn terraform apply-notes.md",
	}
	for _, command := range clear {
		if DenylistMatches(deny, command) {
			t.Errorf("DenylistMatches(%q) = true, want false", command)
		}
	}
}
