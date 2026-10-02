package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/logs"
	"github.com/rfizzle/shhh/internal/prompt"
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
		{"read-only", ModeReadOnly},
		{"read_only", ModeReadOnly},
		{"Read Only", ModeReadOnly},
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

// Five modes, five names, five sentences, and the cycle walks them in one
// direction. The name is the whole of the vocabulary now — the frame's mark
// carries the class — so two modes under one word would be two states behind
// one name on the segment read before every keystroke.
func TestModeNamesAreFiveDistinctWords(t *testing.T) {
	cycle := DefaultCycle()
	want := []string{"manual", "accept-edits", "auto", "read-only", "plan"}
	if len(cycle) != len(want) {
		t.Fatalf("DefaultCycle has %d modes, want %d", len(cycle), len(want))
	}
	// Word is what every surface that draws a mode spells it with. It is
	// String but for the one name a row has the space to write as the two
	// words it is; read-only's hyphen stays, because split it says something
	// else.
	words := []string{"manual", "accept edits", "auto", "read-only", "plan"}
	seen := map[string]Mode{}
	for i, m := range cycle {
		if m.String() != want[i] {
			t.Errorf("cycle position %d is %q, want %q", i, m.String(), want[i])
		}
		if m.Word() != words[i] {
			t.Errorf("%v.Word() = %q, want %q", m, m.Word(), words[i])
		}
		if strings.TrimSpace(m.Describe()) == "" {
			t.Errorf("%v has no one-line description; the picker would list it blank", m)
		}
		if other, dup := seen[m.String()]; dup {
			t.Errorf("%v and %v are both called %q", other, m, m.String())
		}
		seen[m.String()] = m
	}
}

// Read-only and plan are two modes and one policy, and ReadOnly is the
// question every reader asks. A surface that tested for plan mode alone would
// let a write through in the mode whose whole content is that it does not.
func TestModeReadOnly(t *testing.T) {
	for _, m := range DefaultCycle() {
		want := m == ModeReadOnly || m == ModePlan
		if got := m.ReadOnly(); got != want {
			t.Errorf("%v.ReadOnly() = %v, want %v", m, got, want)
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
	if got := NextMode(nil, ModeAuto); got != ModeReadOnly {
		t.Errorf("default cycle after auto = %v, want read-only", got)
	}
	if got := NextMode(nil, ModeReadOnly); got != ModePlan {
		t.Errorf("default cycle after read-only = %v, want plan", got)
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
		// Read-only is plan's policy under its own name, and the reason says
		// which mode answered: the refusal the model reads and the code the
		// record keeps are both taken off this string.
		{"read-only denies edits", ModePolicy{Mode: ModeReadOnly, AllowEdits: true}, edit, Deny, "read-only mode"},
		{"read-only denies commands", ModePolicy{Mode: ModeReadOnly, AllowCommands: true, CommandAllowlist: []string{"go test"}}, cmd, Deny, "read-only mode"},
		{"read-only denies flagged commands", ModePolicy{Mode: ModeReadOnly}, flagged, Deny, "read-only mode"},
		{"read-only denies other tools", ModePolicy{Mode: ModeReadOnly}, other, Deny, "read-only mode"},
		{"read-only allows inspection commands", ModePolicy{Mode: ModeReadOnly}, Action{Kind: ActionCommand, Command: "git status"}, Allow, "read-only mode inspection"},
		{"read-only denies flagged inspection commands", ModePolicy{Mode: ModeReadOnly}, Action{Kind: ActionCommand, Command: "git diff", SafetyFlagged: true}, Deny, "read-only mode"},
	}
	for _, c := range cases {
		got, reason := c.policy.Decide(c.action)
		if got != c.want || reason != c.reason {
			t.Errorf("%s: Decide = %v %q, want %v %q", c.name, got, reason, c.want, c.reason)
		}
	}
}

func TestReadOnlyAllowed(t *testing.T) {
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
		if got := ReadOnlyAllowed(c.command, nil); got != c.want {
			t.Errorf("ReadOnlyAllowed(%q) = %v, want %v", c.command, got, c.want)
		}
	}
}

// The two read-only modes want different next rounds out of the model, so
// they refuse in different words: plan mode's refusal sends it to a plan, and
// read-only mode has no plan to send it to.
func TestModeRefusedResult(t *testing.T) {
	planned := ModeRefusedResult(ModePlan.String()+" mode", "make", nil)
	readOnly := ModeRefusedResult(ModeReadOnly.String()+" mode", "make", nil)
	if planned != PlanModeResult("make", nil) || readOnly != ReadOnlyModeResult("make", nil) {
		t.Fatalf("the refusals are crossed: plan = %q, read-only = %q", planned, readOnly)
	}
	if strings.Contains(readOnly, "plan") {
		t.Errorf("read-only's refusal asks for a plan: %q", readOnly)
	}
	// An unrecognised reason still carries a refusal the model can read: a
	// call refused with an empty result is a call it retries.
	if ModeRefusedResult("", "", nil) != PlanModeResult("", nil) {
		t.Error("an unknown reason should fall back to a refusal, not to nothing")
	}
}

// The refusal is read at the moment the model picks its next command, so it
// names the command it refused and the list that would have run, from the
// list the policy reads, cut where the paragraph is cut. A call that was not
// a command is refused without a command to quote.
func TestReadOnlyRefusalNamesTheList(t *testing.T) {
	got := ReadOnlyModeResult("golangci-lint --version --verbose", []string{"tokei"})
	want := `error: this session is in read-only mode; "golangci-lint --version --verbose" is not an inspection command. ` +
		"Read-only runs: " + prompt.InspectionList(ReadOnlyRuns([]string{"tokei"})) +
		"; every other command and every write is refused and no approval can run one. " +
		"Answer with what you can read, or ask the user to switch modes (Shift+Tab or /permissions)."
	if got != want {
		t.Fatalf("the refusal is\n%s\nwant\n%s", got, want)
	}
	for _, c := range ReadOnlyCommands() {
		if !strings.Contains(got, c) {
			t.Errorf("the refusal leaves out %q", c)
		}
	}
	if !strings.Contains(got, ", tokei;") {
		t.Errorf("the refusal leaves out the person's own entry: %s", got)
	}
	if plan := PlanModeResult("make", nil); !strings.Contains(plan, `"make" is not an inspection command. Plan mode runs: ls, pwd, cat`) ||
		!strings.Contains(plan, "Present your plan as a message") {
		t.Errorf("plan mode's refusal does not name the list: %s", plan)
	}
	if edit := ReadOnlyModeResult("", nil); !strings.Contains(edit, "read-only mode; the call was not executed. Read-only runs: ls,") {
		t.Errorf("an edit's refusal: %s", edit)
	}
	// A heredoc quoted back whole would be the model's own bytes paid again.
	long := ReadOnlyModeResult("cat <<EOF\n"+strings.Repeat("x", 500)+"\nEOF", nil)
	if strings.Count(long, "x") > refusedQuoteMax {
		t.Errorf("the refused command is quoted whole: %d bytes", len(long))
	}
	// The cap is the paragraph's: a long list is cut with its count.
	extra := make([]string, 200)
	for i := range extra {
		extra[i] = fmt.Sprintf("tool-%d --version", i)
	}
	if cut := ReadOnlyModeResult("make", extra); !strings.Contains(cut, fmt.Sprintf(", and %d more;", len(ReadOnlyRuns(extra))-prompt.InspectionListCap)) {
		t.Errorf("a long list is not cut at the cap: %s", cut)
	}
}

// The paragraph is what the model knows of the mode, so it prints the list
// the policy reads, the person's entries included, and says how a command is
// read; a command it prints is one the policy runs.
func TestReadOnlyInstructionsNameTheInspectionList(t *testing.T) {
	toolset := []string{"read_file", "search", "glob", "git", "sqlite", "execute_command", "edit_file"}
	extra := []string{"tokei", "ls"}
	for _, mode := range []Mode{ModeReadOnly, ModePlan} {
		got := ModeInstructions(mode, extra, toolset)
		for _, c := range ReadOnlyCommands() {
			if !strings.Contains(got, c) {
				t.Errorf("%s: the paragraph leaves out %q", mode, c)
			}
			if !ReadOnlyAllowed(c, nil) {
				t.Errorf("%s: the paragraph prints %q, which the policy refuses", mode, c)
			}
		}
		for _, want := range []string{
			"These inspection commands run, matched on their leading words: ls, pwd, cat, head, tail, wc,",
			"go list, go doc, go mod graph", "gofmt -l, go vet", ", tokei.",
			"Run each on its own: a pipe, chain or redirect outside quotes is refused",
			"Any other command (make, go test, a linter, a build) and every write is refused",
			"read with search, glob, git, sqlite and read_file instead.",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: the paragraph does not say %q:\n%s", mode, want, got)
			}
		}
		_, list, _ := strings.Cut(got, "leading words: ")
		list, _, _ = strings.Cut(list, ".\n")
		if n := strings.Count(", "+list+", ", ", ls, "); n != 1 {
			t.Errorf("%s: an entry the built-in list holds is printed twice:\n%s", mode, got)
		}
	}
	// A reader is named only where the toolset holds it, and a toolset with
	// no command is told nothing about commands.
	bare := ModeInstructions(ModeReadOnly, nil, []string{"read_file", "search", "edit_file"})
	if strings.Contains(bare, "inspection commands") || strings.Contains(bare, "git") || strings.Contains(bare, "sqlite") {
		t.Errorf("a toolset without commands or git is told of them:\n%s", bare)
	}
	if !strings.Contains(bare, "read with search and read_file instead") {
		t.Errorf("the readers it holds are not named:\n%s", bare)
	}
}

// The paragraph rides every request in the mode, so what it costs is bounded
// the way the toolbox is: the built-in list makes it no more than
// readOnlyParagraphBytes, and a person's list past the cap grows it by the
// count of the rest and nothing more, however long the list.
func TestReadOnlyInstructionsAreBounded(t *testing.T) {
	// The paragraph as it was written before it printed the list was 430
	// bytes; the built-in list printed is 587 more. The figure leaves the
	// built-in list room for a dozen entries before the bound is revisited.
	const readOnlyParagraphBytes = 1300
	toolset := []string{"read_file", "search", "glob", "git", "sqlite", "execute_command"}
	if n := len(ReadOnlyCommands()); n > prompt.InspectionListCap {
		t.Fatalf("the built-in list holds %d entries, more than the %d the paragraph prints", n, prompt.InspectionListCap)
	}
	base := len(ModeInstructions(ModeReadOnly, nil, toolset))
	t.Logf("the read-only paragraph is %d bytes over the built-in list", base)
	if base > readOnlyParagraphBytes {
		t.Errorf("the read-only paragraph is %d bytes, more than %d", base, readOnlyParagraphBytes)
	}
	entries := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("tool-%d --version", i)
		}
		return out
	}
	room := prompt.InspectionListCap - len(ReadOnlyCommands())
	atCap := len(ModeInstructions(ModeReadOnly, entries(room), toolset))
	for _, n := range []int{room + 1, 500, 10000} {
		got := len(ModeInstructions(ModeReadOnly, entries(n), toolset))
		tail := len(fmt.Sprintf(", and %d more", n-room))
		if got-atCap > tail {
			t.Errorf("%d entries grew the paragraph by %d bytes past the cap, more than the %d of its count", n, got-atCap, tail)
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
		{ModeAuto, ModeReadOnly, ModeReadOnly},
		{ModeManual, ModeReadOnly, ModeReadOnly},
		// Read-only and plan allow the same calls, so neither clamps the
		// other: a clamp that moved a child off plan mode would take the
		// planning away and let through nothing less than before.
		{ModePlan, ModeReadOnly, ModePlan},
		{ModeReadOnly, ModePlan, ModeReadOnly},
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

// What Go work reads runs in a read-only session, and each entry stays a
// read: the flags that would write, or run a program named on the line, are
// refused however they are quoted.
func TestReadOnlyAllowed_GoWork(t *testing.T) {
	cases := []struct {
		command string
		want    bool
	}{
		{"gofmt -l .", true},
		{"gofmt -l -w .", false},
		{"gofmt -l --w=true .", false},
		{"gofmt -w .", false},
		{"go vet ./...", true},
		{"go vet -vettool=/tmp/x ./...", false},
		{"go vet '-vettool=/tmp/x' ./...", false},
		{"go vet -toolexec /tmp/x ./...", false},
		{"go vet -mod=mod ./...", false},
		{"go vet -fix ./...", false},
		{"go vet -gcflags=-cpuprofile=/tmp/x ./...", false},
		{"go vet -debug-trace=/tmp/x ./...", false},
		{"gofmt -l -cpuprofile /tmp/x .", false},
		{"go build -n ./...", true},
		{"go build ./...", false},
		{"go build -n -n=false ./...", false},
		{"go build -n -o bin/x ./cmd/x", false},
		{"go build -n -toolexec=/tmp/x ./...", false},
		{"golangci-lint --version", true},
		{"golangci-lint --version run --fix", false},
		{"golangci-lint run", false},
		// make -n still runs a + recipe line and remakes an included
		// makefile, so it is not a read.
		{"make -n", false},
		{"make", false},
		{"go env GOPATH", true},
		{"go env -w GOFLAGS=-vettool=/tmp/x", false},
		{"find . '-delete'", false},
	}
	if !posixShell {
		t.Skip("commands run through a shell that does not quote the POSIX way")
	}
	for _, c := range cases {
		if got := ReadOnlyAllowed(c.command, nil); got != c.want {
			t.Errorf("ReadOnlyAllowed(%q) = %v, want %v", c.command, got, c.want)
		}
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
	// No grant opens a masked path, so the result must not point at one.
	if result := ScopeRefusedResult("masked"); !strings.HasPrefix(result, "error:") || !strings.Contains(result, "masked") || strings.Contains(result, "/add-dir") {
		t.Errorf("the tool result should name the boundary and offer no way to widen it, got %q", result)
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

// A tool with a closed verb set stands for a command line, and the deny list
// answers that line whatever tier the tool sits at. A person who wrote `git
// commit` on the list meant the act; a tool that let the act through under
// another name would be the way around the list.
func TestDecideRefusesADeniedActAtTheWriteTier(t *testing.T) {
	p := ModePolicy{CommandDenylist: []string{"git commit"}, AllowEdits: true}
	for _, mode := range []Mode{ModeManual, ModeAcceptEdits, ModeAuto, ModePlan} {
		p.Mode = mode
		decision, reason := p.Decide(Action{Kind: ActionEdit, Command: "git commit"})
		if decision != Deny {
			t.Errorf("%v mode: a denied act at the write tier = %v; want Deny", mode, decision)
		}
		if reason != DenyReasonDenylist {
			t.Errorf("%v mode gave reason %q; want the deny list named", mode, reason)
		}
		// The other verbs of the same tool are untouched: the list refused
		// one act, not the tool.
		if decision, _ := p.Decide(Action{Kind: ActionEdit, Command: "git add"}); decision == Deny && mode != ModePlan {
			t.Errorf("%v mode: `git add` was refused by a `git commit` entry", mode)
		}
	}
	// An act the list says nothing about proceeds where an edit proceeds.
	open := ModePolicy{Mode: ModeAuto}
	if decision, reason := open.Decide(Action{Kind: ActionEdit, Command: "git commit"}); decision != Allow || reason != "auto mode" {
		t.Errorf("auto mode = %v (%q); want Allow by the mode, as an edit is", decision, reason)
	}
	// And plan mode refuses it, because plan mode refuses every write.
	plan := ModePolicy{Mode: ModePlan}
	if decision, _ := plan.Decide(Action{Kind: ActionEdit, Command: "git commit"}); decision != Deny {
		t.Errorf("plan mode = %v; want Deny", decision)
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

// A host the person has answered for is answered for the rest of the
// session, in every mode a fetch is possible in, and before auto mode would
// have paid the classifier to think about it. An ungranted host is the
// decision it always was.
func TestDecideAllowsAGrantedHostAndAsksAboutEveryOther(t *testing.T) {
	p := ModePolicy{AllowHosts: []string{"docs.python.org", "PKG.GO.DEV"}}
	for _, mode := range []Mode{ModeManual, ModeAcceptEdits, ModeAuto} {
		p.Mode = mode
		// Case and the trailing dot of an absolute name are two spellings of
		// one host, not two hosts.
		for _, host := range []string{"docs.python.org", "DOCS.python.org", "pkg.go.dev."} {
			decision, reason := p.Decide(Action{Kind: ActionFetch, Host: host})
			if decision != Allow {
				t.Errorf("%v mode, %q = %v; want Allow", mode, host, decision)
			}
			if reason != "session grant" {
				t.Errorf("%v mode, %q gave reason %q; want the grant named", mode, host, reason)
			}
		}
		// The grant is the host and never a suffix: the person read one site
		// on the card, not every subdomain a company will ever publish.
		for _, host := range []string{"python.org", "docs.python.org.evil.test", "evil-docs.python.org"} {
			if decision, _ := p.Decide(Action{Kind: ActionFetch, Host: host}); decision != Ask {
				t.Errorf("%v mode, %q = %v; want Ask", mode, host, decision)
			}
		}
	}
	// Plan mode is read-only, and a fetch is a request leaving the machine
	// however quiet the grant is.
	p.Mode = ModePlan
	if decision, reason := p.Decide(Action{Kind: ActionFetch, Host: "docs.python.org"}); decision != Deny || reason != "plan mode" {
		t.Errorf("plan mode = %v (%q); want Deny by the mode", decision, reason)
	}
}

// The host deny list is read before the grant, before the mode and before
// the classifier, and nothing a session can grant reaches past it.
func TestDecideRefusesADeniedHostInEveryMode(t *testing.T) {
	p := ModePolicy{
		DenyHosts:  []string{"paste.example.test"},
		AllowHosts: []string{"paste.example.test", "docs.python.org"},
	}
	for _, mode := range []Mode{ModeManual, ModeAcceptEdits, ModeAuto, ModePlan} {
		p.Mode = mode
		decision, reason := p.Decide(Action{Kind: ActionFetch, Host: "paste.example.test"})
		if decision != Deny {
			t.Errorf("%v mode = %v; want Deny", mode, decision)
		}
		if reason != DenyReasonHost {
			t.Errorf("%v mode gave reason %q; want the host list named", mode, reason)
		}
	}
	// The list answers for hosts and for nothing else: a command is a
	// different question, and the command list is what answers it.
	p.Mode = ModeManual
	if decision, _ := p.Decide(Action{Kind: ActionCommand, Command: "curl paste.example.test"}); decision != Ask {
		t.Errorf("the host deny list answered for a command: %v", decision)
	}
}

// A conversation's fetch is a read: it is allowed without a card, under a
// reason of its own, whatever mode sits underneath — and the host deny list
// is still read first. Nothing else a conversation asks about moves: a spawn
// is the question it was.
func TestDecideAllowsAConversationsFetchPastTheDenyList(t *testing.T) {
	p := ModePolicy{Conversation: true, DenyHosts: []string{"paste.example.test"}}
	for _, mode := range []Mode{ModeManual, ModeReadOnly, ModePlan} {
		p.Mode = mode
		decision, reason := p.Decide(Action{Kind: ActionFetch, Host: "docs.python.org"})
		if decision != Allow || reason != ConversationReadReason {
			t.Errorf("%v mode = %v (%q); want Allow as a conversation read", mode, decision, reason)
		}
		if decision, reason := p.Decide(Action{Kind: ActionFetch, Host: "paste.example.test"}); decision != Deny || reason != DenyReasonHost {
			t.Errorf("%v mode, denied host = %v (%q); want the host list to refuse it", mode, decision, reason)
		}
	}
	p.Mode = ModeManual
	if decision, _ := p.Decide(Action{Kind: ActionOther}); decision != Ask {
		t.Errorf("a conversation's spawn = %v; want Ask", decision)
	}
	// A coding session's fetch is untouched: the same host is a card.
	p.Conversation = false
	if decision, _ := p.Decide(Action{Kind: ActionFetch, Host: "docs.python.org"}); decision != Ask {
		t.Errorf("a coding session's fetch = %v; want Ask", decision)
	}
}

// What the model is told about a refused host says the refusal covers the
// host rather than the URL, and names no key: the list is the person's, and
// a refusal that came with editing instructions would hand over the way
// around it.
func TestDeniedHostResultStopsTheRetryWithoutNamingTheList(t *testing.T) {
	for _, word := range []string{"web.deny_hosts", "config", "~/.config"} {
		if strings.Contains(DeniedHostResult, word) {
			t.Errorf("the refusal names %q, which is the way around the list", word)
		}
	}
	if !strings.Contains(DeniedHostResult, "another path on the same host") {
		t.Error("the refusal does not say that another URL on the host will not work either")
	}
}

// A refusal is written down, and an allow is not. The policy's denials are
// the verdict with no lasting surface anywhere else: the model is handed an
// error and goes on, and the person finds out that nothing happened.
func TestDecide_ARefusalIsWrittenDownWithItsRule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.log")
	logs.To(path)
	t.Cleanup(func() { logs.To("") })

	p := ModePolicy{Mode: ModeAuto, CommandDenylist: []string{"rm -rf"}}
	if d, _ := p.Decide(Action{Kind: ActionCommand, Command: "rm -rf /tmp/x"}); d != Deny {
		t.Fatalf("the deny list must refuse the command")
	}
	if d, _ := p.Decide(Action{Kind: ActionEdit, Path: "/repo/main.go"}); d != Allow {
		t.Fatalf("auto mode applies an edit")
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the log: %v", err)
	}
	line := string(written)
	for _, want := range []string{"call refused", "what=command", "command=rm", DenyReasonDenylist} {
		if !strings.Contains(line, want) {
			t.Errorf("the log does not say %q:\n%s", want, line)
		}
	}
	// The first word of the command and nothing else: the log is shared
	// between sessions and outlives all of them, and the edit that was
	// allowed left no line at all.
	if strings.Contains(line, "/tmp/x") || strings.Contains(line, "main.go") {
		t.Errorf("the log carries what the calls were pointed at:\n%s", line)
	}
	if n := strings.Count(line, "call refused"); n != 1 {
		t.Errorf("one refusal wrote %d lines, want 1:\n%s", n, line)
	}
}

// The two modes that bound the session are the two that say so to the model;
// the rest are about what runs without asking and add nothing.
func TestModeInstructions(t *testing.T) {
	toolset := []string{"read_file", "execute_command"}
	for _, tc := range []struct {
		mode Mode
		want string
	}{
		{ModeReadOnly, prompt.ReadOnlyModeInstructions(ReadOnlyCommands(), toolset)},
		{ModePlan, prompt.PlanModeInstructions(ReadOnlyCommands(), toolset)},
		{ModeManual, ""},
		{ModeAcceptEdits, ""},
		{ModeAuto, ""},
	} {
		if got := ModeInstructions(tc.mode, nil, toolset); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.mode, got, tc.want)
		}
	}
}

// A destroying command pointed at something this session may not destroy is
// refused in every mode, whatever the grants, before anything that can allow
// — and the deny list, which is the person's own answer, still names itself
// first where both apply.
func TestDecide_AnIrreplaceableTargetIsRefusedInEveryMode(t *testing.T) {
	a := Action{Kind: ActionCommand, Command: "rm -rf ~", SafetyFlagged: true,
		Irreplaceable: "~ — your home directory"}
	for _, mode := range []Mode{ModeManual, ModeAcceptEdits, ModeAuto, ModeReadOnly, ModePlan} {
		p := ModePolicy{Mode: mode, AllowCommands: true, CommandAllowlist: []string{"rm"},
			TurnGrants: Grants{AllCommands: true}}
		decision, reason := p.Decide(a)
		if decision != Deny || !IsIrreplaceable(reason) || !strings.Contains(reason, "~ — your home directory") {
			t.Errorf("%s: Decide = %v %q, want the irreplaceable-target refusal naming the target", mode, decision, reason)
		}
	}
	both := ModePolicy{Mode: ModeAuto, CommandDenylist: []string{"rm"}}
	if _, reason := both.Decide(a); reason != DenyReasonDenylist {
		t.Errorf("the deny list answered second: %q", reason)
	}
	if _, ok := RuleRefusal(nil, Action{Kind: ActionCommand, Command: "rm -rf build"}); ok {
		t.Error("a command with nothing irreplaceable in it was refused by rule")
	}
}

// The refusal the model reads names the target, says a respelling is refused
// as well, and names no setting: there is none that lifts it, and a sentence
// that hinted at one would be the way around it.
func TestIrreplaceableResultNamesTheTargetAndNoWayAround(t *testing.T) {
	reason, _ := RuleRefusal(nil, Action{Command: "rm -rf /", Irreplaceable: "/ — the filesystem root"})
	got := RuleRefusedResult(reason)
	for _, want := range []string{"error:", "/ — the filesystem root", "outside what this session may destroy", "another spelling"} {
		if !strings.Contains(got, want) {
			t.Errorf("the result does not say %q: %q", want, got)
		}
	}
	for _, leak := range []string{"config", "behavior.", "deny list", "!"} {
		if strings.Contains(got, leak) {
			t.Errorf("the result names %q, which the model can act on: %q", leak, got)
		}
	}
}
