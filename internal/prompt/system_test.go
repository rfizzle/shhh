package prompt

import (
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/proposal"
	"github.com/rfizzle/shhh/internal/shell"
)

func TestBuild_ContainsShell(t *testing.T) {
	info := shell.Info{Shell: "fish", OS: "darwin", Cwd: "/home/user"}
	got := Build(info)

	if !strings.Contains(got, "Shell: fish") {
		t.Errorf("expected prompt to contain shell name, got:\n%s", got)
	}
}

func TestBuild_ContainsOS(t *testing.T) {
	info := shell.Info{Shell: "zsh", OS: "darwin", Cwd: "/tmp"}
	got := Build(info)

	if !strings.Contains(got, "OS: macOS") {
		t.Errorf("expected 'OS: macOS' for darwin, got:\n%s", got)
	}
}

func TestBuild_LinuxOS(t *testing.T) {
	info := shell.Info{Shell: "bash", OS: "linux", Cwd: "/tmp"}
	got := Build(info)

	if !strings.Contains(got, "OS: Linux") {
		t.Errorf("expected 'OS: Linux' for linux, got:\n%s", got)
	}
}

func TestBuild_ContainsCwd(t *testing.T) {
	info := shell.Info{Shell: "zsh", OS: "darwin", Cwd: "/Users/me/projects"}
	got := Build(info)

	if !strings.Contains(got, "Cwd: /Users/me/projects") {
		t.Errorf("expected prompt to contain cwd, got:\n%s", got)
	}
}

func TestBuild_InstructsCommandOnly(t *testing.T) {
	info := shell.Info{Shell: "zsh", OS: "darwin", Cwd: "/tmp"}
	got := Build(info)

	if !strings.Contains(got, "ONLY the command") {
		t.Errorf("expected prompt to instruct command-only output, got:\n%s", got)
	}
}

func TestBuild_NoMarkdown(t *testing.T) {
	info := shell.Info{Shell: "zsh", OS: "darwin", Cwd: "/tmp"}
	got := Build(info)

	if !strings.Contains(got, "no markdown") {
		t.Errorf("expected prompt to forbid markdown, got:\n%s", got)
	}
}

func TestBuild_Terse(t *testing.T) {
	info := shell.Info{Shell: "zsh", OS: "darwin", Cwd: "/tmp"}
	got := Build(info)

	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) > 20 {
		t.Errorf("prompt should be terse, got %d lines:\n%s", len(lines), got)
	}
}

func TestBuild_UnknownOS(t *testing.T) {
	info := shell.Info{Shell: "sh", OS: "freebsd", Cwd: "/tmp"}
	got := Build(info)

	if !strings.Contains(got, "OS: freebsd") {
		t.Errorf("expected unknown OS to pass through, got:\n%s", got)
	}
}

func TestBuild_WithExtra(t *testing.T) {
	info := shell.Info{Shell: "zsh", OS: "darwin", Cwd: "/tmp"}
	got := Build(info, "Always use ripgrep instead of grep")

	if !strings.Contains(got, "Always use ripgrep instead of grep") {
		t.Error("expected extra prompt to be appended")
	}
	if !strings.Contains(got, "Shell: zsh") {
		t.Error("expected base prompt to still be present")
	}
}

func TestBuild_WithEmptyExtra(t *testing.T) {
	info := shell.Info{Shell: "zsh", OS: "darwin", Cwd: "/tmp"}
	withEmpty := Build(info, "")
	without := Build(info)

	if withEmpty != without {
		t.Error("empty extra should produce same result as no extra")
	}
}

func TestBuildConversation(t *testing.T) {
	info := shell.Info{Shell: "bash", OS: "linux", Cwd: "/home/user"}
	got := BuildConversation(info, "Prefer explaining with examples")

	if !strings.Contains(got, "Prefer explaining with examples") {
		t.Error("expected extra prompt to be appended")
	}
	for _, want := range []string{"Everything you can reach is a read", "cannot run commands or edit files", "Cwd: /home/user"} {
		if !strings.Contains(got, want) {
			t.Errorf("conversation prompt missing %q", want)
		}
	}
	// No tool the session lacks is named, and no shell guidance rides
	// along: there is nothing that could use it. The notebook is in the
	// list because it is registered on a condition like every other
	// optional tool, and Toolbox is what states those.
	for _, absent := range []string{"execute_command", "write_file", "Shell: bash", "sudo", "write_note", "Notebook"} {
		if strings.Contains(got, absent) {
			t.Errorf("conversation prompt names %q", absent)
		}
	}
}

func TestBuildAgent_AgentInstructions(t *testing.T) {
	info := shell.Info{Shell: "bash", OS: "linux", Cwd: "/home/user/project"}
	got := BuildAgent(info)

	for _, want := range []string{
		"coding agent",
		"use them proactively",
		"Read a file before editing",
		"verify your changes",
		"Keep going",
		"write_file and edit_file rather than pasting code blocks",
		"quality_gate tool is available, run it before declaring a task complete",
		"brief public progress note",
		"Do not reveal private reasoning",
		"Implementation, bug-fix, and diagnosis requests",
		"Explanation, review, planning, brainstorming, and analysis-only requests are exceptions",
		"A failed tool call is evidence",
		"inspect or reproduce the failure early",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected agent prompt to contain %q, got:\n%s", want, got)
		}
	}
}

// The approval sentence has to be true in all four permission modes: --yes,
// accept-edits and auto each run some or all of the gated tools without
// asking anybody, and a model told its edits are waiting on the user stops to
// report work it has already done.
func TestBuildAgent_ApprovalSentenceIsModeNeutral(t *testing.T) {
	got := BuildAgent(shell.Info{Shell: "bash", OS: "linux", Cwd: "/work"})
	if !strings.Contains(got, "the session's permission mode") {
		t.Errorf("the mode should be named as what decides, got:\n%s", got)
	}
	for _, absent := range []string{"require their approval", "must approve"} {
		if strings.Contains(got, absent) {
			t.Errorf("the prompt claims every gated call asks (%q), which three modes make false", absent)
		}
	}
}

func TestBuildAgent_ContainsEnvironment(t *testing.T) {
	info := shell.Info{Shell: "zsh", OS: "darwin", Cwd: "/Users/me/proj"}
	got := BuildAgent(info)

	if !strings.Contains(got, "Shell: zsh") {
		t.Errorf("expected agent prompt to contain shell name, got:\n%s", got)
	}
	if !strings.Contains(got, "OS: macOS") {
		t.Errorf("expected agent prompt to contain OS, got:\n%s", got)
	}
	if !strings.Contains(got, "Cwd: /Users/me/proj") {
		t.Errorf("expected agent prompt to contain cwd, got:\n%s", got)
	}
}

func TestBuildAgent_WithExtra(t *testing.T) {
	info := shell.Info{Shell: "bash", OS: "linux", Cwd: "/home/user"}
	got := BuildAgent(info, "This repo uses make test.")

	if !strings.Contains(got, "This repo uses make test.") {
		t.Error("expected extra prompt to be appended to agent prompt")
	}

	if withEmpty := BuildAgent(info, ""); withEmpty != BuildAgent(info) {
		t.Error("empty extra should produce same result as no extra")
	}
}

func TestFriendlyOS(t *testing.T) {
	tests := []struct {
		goos string
		want string
	}{
		{"darwin", "macOS"},
		{"linux", "Linux"},
		{"windows", "Windows"},
		{"freebsd", "freebsd"},
	}
	for _, tt := range tests {
		if got := friendlyOS(tt.goos); got != tt.want {
			t.Errorf("friendlyOS(%q) = %q, want %q", tt.goos, got, tt.want)
		}
	}
}

func TestBuildResearcher_Instructions(t *testing.T) {
	info := shell.Info{Shell: "bash", OS: "linux", Cwd: "/home/user/project"}
	got := BuildResearcher(info, WebTools{Fetch: true, Search: true}, "EXTRA CONTEXT")

	for _, want := range []string{
		"research sub-agent",
		"cannot edit files or run commands",
		"last message IS the deliverable",
		"Cwd: /home/user/project",
		"EXTRA CONTEXT",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected researcher prompt to contain %q, got:\n%s", want, got)
		}
	}
}

// Every child's final report names the heading its assumptions go under, and
// a reviewer's names its verdict line: the lane counts the one and states the
// other, and both are read off the shape asked for here.
func TestTheFinalReportNamesItsSections(t *testing.T) {
	info := shell.Info{Shell: "bash", OS: "linux", Cwd: "/w"}
	for name, got := range map[string]string{
		"researcher": BuildResearcher(info, WebTools{}),
		"writer":     BuildWriter(info),
		"reviewer":   BuildReviewer(info, ProfileSpec{}),
	} {
		report := got[strings.Index(got, "# Final report"):]
		if !strings.Contains(report, "`## Assumptions`") {
			t.Errorf("the %s's final report does not name the Assumptions heading:\n%s", name, report)
		}
		hasVerdict := strings.Contains(report, "`Verdict: <word>`")
		if hasVerdict != (name == "reviewer") {
			t.Errorf("the %s's final report asks for a verdict line: %v, want %v", name, hasVerdict, name == "reviewer")
		}
	}
	for _, word := range reviewVerdicts {
		if !strings.Contains(BuildReviewer(info, ProfileSpec{}), word) {
			t.Errorf("the reviewer's prompt does not name the verdict %q", word)
		}
	}
}

func TestBuildWriter_Instructions(t *testing.T) {
	info := shell.Info{Shell: "bash", OS: "linux", Cwd: "/tmp/worktree/proj"}
	got := BuildWriter(info, "EXTRA CONTEXT")

	for _, want := range []string{
		"ISOLATED COPY",
		"single patch",
		"write_file and edit_file",
		"last message IS the deliverable",
		"Cwd: /tmp/worktree/proj",
		"EXTRA CONTEXT",
		"Implementation, bug-fix, and diagnosis requests",
		"A failed tool call is evidence",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected writer prompt to contain %q, got:\n%s", want, got)
		}
	}
}

// The sections are asked for on the interactive one-shot and nowhere else. A
// pipe's stdout is one command by contract, so the prompt behind it must not
// invite anything to follow it.
func TestBuildAlternatives_AsksForTheSection(t *testing.T) {
	info := shell.Info{Shell: "zsh", OS: "darwin", Cwd: "/tmp"}
	got := BuildAlternatives(info)

	if !strings.Contains(got, proposal.Sentinel) {
		t.Errorf("the interactive prompt does not ask for the section:\n%s", got)
	}
	// The sentence under the command comes back with the command, so the
	// surface does not spend a second request on it.
	if !strings.Contains(got, proposal.ExplainSentinel) {
		t.Errorf("the interactive prompt does not ask for the explanation:\n%s", got)
	}
	// The generator's own rule is "no explanation", and one of the sections
	// is one. A prompt that leaves the two to be reconciled gets prose
	// wrapped around the command.
	if !strings.Contains(got, "The command still comes first and alone") {
		t.Errorf("the prompt does not say which of its two rules wins:\n%s", got)
	}
	// Everything Build says is still said: the section is an addition, not a
	// different prompt.
	if !strings.Contains(got, "Shell: zsh") || !strings.Contains(got, "Output ONLY the command(s)") {
		t.Errorf("the alternatives prompt lost the generator's own rules:\n%s", got)
	}
}

func TestBuild_DoesNotAskForTheSections(t *testing.T) {
	got := Build(shell.Info{Shell: "zsh", OS: "darwin", Cwd: "/tmp"})
	for _, sentinel := range []string{proposal.Sentinel, proposal.ExplainSentinel} {
		if strings.Contains(got, sentinel) {
			t.Errorf("the piped prompt invited a section its stdout cannot carry:\n%s", got)
		}
	}
	if !strings.Contains(got, "No explanation") {
		t.Errorf("the piped prompt stopped saying its output is the command alone:\n%s", got)
	}
}

func TestBuildAlternatives_KeepsTheExtraLast(t *testing.T) {
	// The project's own context is the last word in the prompt, ahead of
	// neither the syntax rules nor the section.
	got := BuildAlternatives(shell.Info{Shell: "bash", OS: "linux", Cwd: "/tmp"}, "PROJECT CONTEXT")
	if !strings.HasSuffix(got, "PROJECT CONTEXT") {
		t.Errorf("the extra is no longer last:\n%s", got)
	}
	if strings.Index(got, proposal.Sentinel) > strings.Index(got, "PROJECT CONTEXT") {
		t.Errorf("the section landed after the project context:\n%s", got)
	}
}

func TestBuildReviewerIsBoundedToDeclaredEvidence(t *testing.T) {
	reviewer := BuildReviewer(shell.Info{OS: "linux", Cwd: "/w"}, ProfileSpec{})
	for _, want := range []string{"arrives ahead of your task", "Do not re-read repository instructions", "inspection pass is bounded by a round cap", "direct tests", "report rather than broadening", "say what you did not reach"} {
		if !strings.Contains(reviewer, want) {
			t.Fatalf("reviewer prompt lacks %q:\n%s", want, reviewer)
		}
	}
}

// The built-in reviewer role's prompt is fixed. Giving a reviewing profile
// its name and its real toolset is a change to what a named spec produces,
// and the zero spec has to come out byte for byte what it did before — so it
// is pinned here rather than described, because a drift of one word is the
// kind of thing a Contains assertion never notices.
func TestTheBuiltInReviewersPromptIsFixed(t *testing.T) {
	fixed := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	old := now
	now = func() time.Time { return fixed }
	t.Cleanup(func() { now = old })

	want := `You are a review sub-agent working one delegated task for an orchestrating agent. You cannot see the orchestrator's conversation, and it only receives your final message — nothing else survives.

# Environment
OS: Linux
Cwd: /w
Date: Tuesday, 1 September 2026

# Tools
You have read-only access to the workspace (read_file, list_directory, search, glob). You cannot edit files or run commands. Your declared evidence — the scoped paths and their diff — arrives ahead of your task when the caller declared any, and the task itself carries it otherwise: read that first, then the files it touches, then the tests that cover them. Do not re-read repository instructions or survey unrelated files before examining that declared evidence.

# Reviewing
You are reviewing a change, not making one. Report, in this order:
1. Bugs — concrete inputs that produce a wrong result, with the file:line.
2. Acceptance criteria the task names that are not actually met.
3. Behaviour changes the task did not ask for.
4. Missing tests, naming the case that is not covered.
5. Style only where it hides a bug or contradicts the surrounding file.

Rank by severity. Say "no findings" for an empty section rather than inventing one. Never propose a rewrite of something that works. Your inspection pass is bounded by a round cap, not by your own judgement of when to stop: once you have examined the declared evidence and its direct tests, report rather than broadening the survey. If the pass ends before you have, you are told to report on what you examined and you say what you did not reach.

# Final report
Your last message IS the deliverable. If you assumed anything you would otherwise have asked about, list each assumption as a bullet under a heading of its own, ` + "`## Assumptions`" + `; leave the heading out when you assumed nothing. End it with a last line of its own, ` + "`Verdict: <word>`" + `: the verdict word the task names, or where it names none, one of approve, approve with changes, request changes.`

	if got := BuildReviewer(shell.Info{OS: "linux", Cwd: "/w"}, ProfileSpec{}); got != want {
		t.Errorf("the built-in reviewer's prompt moved:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// A reviewing profile is still a profile: it says which agent it is and what
// it is for, and its tool paragraph names what it actually holds. A reviewer
// that can run the project's checks and is told it cannot run anything is a
// reviewer that judges the build from the diff.
func TestAReviewingProfileKeepsItsNameAndItsTools(t *testing.T) {
	info := shell.Info{OS: "linux", Cwd: "/w"}
	reads := []string{"read_file", "list_directory", "search", "glob"}

	withGate := BuildReviewer(info, ProfileSpec{
		Name: "critic", Description: "audits diffs.",
		Tools: append(append([]string{}, reads...), "quality_gate"),
	}, "Audit ruthlessly.")
	for _, want := range []string{
		`You are the "critic" review sub-agent`, "Your purpose: audits diffs.",
		"read-only access to the workspace (read_file, list_directory, search, glob)",
		"quality_gate runs the project's own configured checks",
		"the gate is the only command you can run",
		"# Reviewing", "`Verdict: <word>`",
	} {
		if !strings.Contains(withGate, want) {
			t.Errorf("a reviewing profile holding the gate lacks %q:\n%s", want, withGate)
		}
	}
	if strings.Contains(withGate, "Your purpose: audits diffs..") {
		t.Errorf("the description's own full stop was doubled:\n%s", withGate)
	}
	if strings.Contains(withGate, "You cannot edit files or run commands.") {
		t.Errorf("a reviewer holding the gate is told it can run nothing:\n%s", withGate)
	}
	if !strings.HasSuffix(withGate, "\n\nAudit ruthlessly.") {
		t.Errorf("the profile's own instructions are no longer last:\n%s", withGate)
	}

	// The same profile without it: no gate sentence, and the boundary is the
	// built-in's again.
	noGate := BuildReviewer(info, ProfileSpec{Name: "critic", Tools: reads})
	if strings.Contains(noGate, "quality_gate") {
		t.Errorf("a reviewer without the gate was told it has one:\n%s", noGate)
	}
	if !strings.Contains(noGate, "You cannot edit files or run commands.") {
		t.Errorf("a reviewer without the gate lost the boundary sentence:\n%s", noGate)
	}
	if strings.Contains(noGate, "Your purpose:") {
		t.Errorf("a profile with no description invented one:\n%s", noGate)
	}

	// A narrowed allowlist and the web tier are the other two things the
	// fixed paragraph used to be wrong about.
	narrow := BuildReviewer(info, ProfileSpec{Name: "critic", Tools: []string{"read_file", "search", "web_fetch"}})
	if !strings.Contains(narrow, "workspace (read_file, search).") {
		t.Errorf("the tool list names tools the profile does not hold:\n%s", narrow)
	}
	if !strings.Contains(narrow, "Web tools (web_fetch)") || !strings.Contains(narrow, noSearch) {
		t.Errorf("a reviewer with fetch and no search is not told either:\n%s", narrow)
	}
}

func TestBuildProfileFollowsPermissions(t *testing.T) {
	info := shell.Info{Shell: "bash", OS: "linux", Cwd: "/w"}
	reader := BuildProfile(info, ProfileSpec{Name: "reviewer", Description: "judges diffs", Tools: []string{"read_file", "search"}}, "Be terse.")
	for _, want := range []string{`"reviewer" sub-agent`, "judges diffs", "read_file, search", "cannot edit files or run commands", "the findings, the evidence", "Be terse."} {
		if !strings.Contains(reader, want) {
			t.Fatalf("read-only profile prompt lacks %q:\n%s", want, reader)
		}
	}
	for _, absent := range []string{"Shell:", "ISOLATED COPY", "glob", "execute_command", "# Shell commands"} {
		if strings.Contains(reader, absent) {
			t.Fatalf("read-only profile prompt must not mention %q", absent)
		}
	}

	fixer := BuildProfile(info, ProfileSpec{Name: "fixer", Write: true, Execute: true, Isolated: true,
		Tools: []string{"read_file", "list_directory", "search", "glob", "write_file", "edit_file", "execute_command"}})
	for _, want := range []string{"ISOLATED COPY", "Shell: bash", "execute_command, write_file, edit_file", "# Shell commands", "what you changed (files and why)", "Read a file before editing it", "Implementation, bug-fix, and diagnosis requests", "A failed tool call is evidence"} {
		if !strings.Contains(fixer, want) {
			t.Fatalf("writing profile prompt lacks %q:\n%s", want, fixer)
		}
	}
}

// Coding prompts default to the work the user asked for, while a request that
// names a read-only deliverable must not acquire a mutation merely because a
// profile can make one.
func TestExecutionDefaultReachesOnlyWritingPrompts(t *testing.T) {
	info := shell.Info{Shell: "bash", OS: "linux", Cwd: "/w"}
	for name, got := range map[string]string{
		"agent":   BuildAgent(info),
		"writer":  BuildWriter(info),
		"profile": BuildProfile(info, ProfileSpec{Name: "fixer", Write: true, Tools: []string{"read_file", "write_file", "edit_file"}}),
	} {
		for _, want := range []string{
			"complete work in the current turn",
			"research report, suggested patch, or plan",
			"Explanation, review, planning, brainstorming, and analysis-only requests are exceptions",
			"Never retry an unchanged call",
			"narrowest relevant check before the required quality gate",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s prompt lacks %q:\n%s", name, want, got)
			}
		}
	}
	reader := BuildProfile(info, ProfileSpec{Name: "reviewer", Tools: []string{"read_file"}})
	if strings.Contains(reader, "# Execution default") {
		t.Errorf("a read-only profile was given an execution default:\n%s", reader)
	}
}

// Every prompt with an environment section carries the date. A model left to
// its training cutoff misdates what it writes and assumes the newest thing it
// knows of is still the newest.
func TestEveryPromptStatesTheDate(t *testing.T) {
	fixed := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	old := now
	now = func() time.Time { return fixed }
	t.Cleanup(func() { now = old })

	// Derived from the fixed clock rather than written out, so this test
	// cannot be wrong about the calendar; TestTheDateReadsUnambiguously is
	// where the format itself is pinned.
	want := fixed.Format("Monday, 2 January 2006")
	info := shell.Info{Shell: "bash", OS: "linux", Cwd: "/work"}
	spec := ProfileSpec{Name: "auditor", Tools: []string{"read_file"}}

	for name, got := range map[string]string{
		"one-shot":     Build(info),
		"agent":        BuildAgent(info),
		"researcher":   BuildResearcher(info, WebTools{Fetch: true, Search: true}),
		"reviewer":     BuildReviewer(info, ProfileSpec{}),
		"writer":       BuildWriter(info),
		"profile":      BuildProfile(info, spec),
		"conversation": BuildConversation(info),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%s prompt does not state the date (%s)", name, want)
		}
	}
}

// A date and not a timestamp: the prompt is built once, so anything finer
// would go stale inside the session it was written for. And the month is a
// word, because 1/9 and 9/1 are the same date to two different readers.
func TestTheDateReadsUnambiguously(t *testing.T) {
	old := now
	now = func() time.Time { return time.Date(2026, time.September, 1, 23, 45, 0, 0, time.UTC) }
	t.Cleanup(func() { now = old })

	got := BuildAgent(shell.Info{Shell: "bash", OS: "linux", Cwd: "/work"})
	if !strings.Contains(got, "Date: Tuesday, 1 September 2026") {
		t.Errorf("the date should name its month and its weekday:\n%s", got)
	}
	if strings.Contains(got, "23:45") {
		t.Errorf("the environment block should carry no clock:\n%s", got)
	}
}

// A Windows session must never be told to reach for sudo. IsRoot is false
// there the way it is false for an ordinary Unix user, and the two mean
// opposite things: one wants sudo in front of the command, the other has none
// to put there.
func TestWindowsIsNeverToldAboutSudo(t *testing.T) {
	for _, root := range []bool{false, true} {
		got := sudoRules("windows", root)
		if strings.Contains(got, "Prefix commands with sudo") {
			t.Errorf("root=%v: %s", root, got)
		}
		if !strings.Contains(got, "no sudo") {
			t.Errorf("root=%v: the absence has to be stated, not merely omitted: %s", root, got)
		}
	}
}

func TestUnixStillGetsItsSudoRule(t *testing.T) {
	if !strings.Contains(sudoRules("linux", false), "Prefix commands with sudo") {
		t.Error("an ordinary Linux user still needs the rule")
	}
	if strings.Contains(sudoRules("linux", true), "Prefix commands with sudo") {
		t.Error("root does not need sudo")
	}
}

// The two PowerShells differ on the one thing that costs a whole command:
// 5.1 has no && or ||, and using them there is a syntax error.
func TestPowerShellChainingIsToldApartByVersion(t *testing.T) {
	five := shellSyntaxRules("powershell")
	seven := shellSyntaxRules("pwsh")

	if !strings.Contains(five, "syntax error in Windows PowerShell 5.1") {
		t.Errorf("5.1 must be warned off && and ||:\n%s", five)
	}
	if !strings.Contains(seven, "&& and || work in this version") {
		t.Errorf("7 should not be made to write the long form:\n%s", seven)
	}
	for _, rules := range []string{five, seven} {
		if !strings.Contains(rules, "$env:NAME") {
			t.Error("both take the same environment-variable syntax")
		}
	}
}

func TestCmdGetsItsOwnSyntaxNotAPosixOne(t *testing.T) {
	got := shellSyntaxRules("cmd")
	if !strings.Contains(got, "%NAME%") {
		t.Errorf("cmd variables are %%NAME%%:\n%s", got)
	}
	if strings.Contains(got, "${VAR}") {
		t.Errorf("cmd is not a POSIX shell:\n%s", got)
	}
}

// An unknown shell still falls back to POSIX rather than to nothing.
func TestAnUnknownShellStillGetsRules(t *testing.T) {
	if got := shellSyntaxRules("nu"); !strings.Contains(got, "POSIX") {
		t.Errorf("got %q", got)
	}
}

// The trap that is worse than absence: the name is there and the flags are
// not, so the command fails rather than being not found.
func TestWindowsRulesWarnAboutTheAliasedPosixNames(t *testing.T) {
	got := osRules("windows")
	if !strings.Contains(got, "ls -la") {
		t.Errorf("the aliased-name trap should be named concretely:\n%s", got)
	}
	if !strings.Contains(got, "where") {
		t.Errorf("finding an executable is a different command there:\n%s", got)
	}
}

// The whole prompt has to hold together: a Windows session gets Windows rules
// throughout, and no POSIX ones anywhere in it.
func TestAWindowsPromptCarriesNoPosixAdvice(t *testing.T) {
	got := BuildAgent(shell.Info{Shell: "pwsh", OS: "windows", Cwd: `C:\src\app`})
	for _, wrong := range []string{"Prefix commands with sudo", "GNU coreutils", "BSD command-line tools", "${VAR}"} {
		if strings.Contains(got, wrong) {
			t.Errorf("a Windows prompt should not carry %q", wrong)
		}
	}
	for _, want := range []string{"PowerShell syntax only", "no sudo", "This is Windows"} {
		if !strings.Contains(got, want) {
			t.Errorf("a Windows prompt should carry %q", want)
		}
	}
}

// The tree notice is a vocabulary the session prompt has to teach, or the
// first one arrives unexplained.
func TestTheAgentPromptExplainsTheTreeNotice(t *testing.T) {
	got := BuildAgent(shell.Info{Shell: "bash", OS: "linux", Cwd: "/work"})
	if !strings.Contains(got, `"[tree: …]" message`) || !strings.Contains(got, "never who moved it") {
		t.Errorf("the agent prompt should explain the tree notice:\n%s", got)
	}
}

// A session is told which half of the web it has, and told it once. The
// sentence about having no search is there exactly when no search backend is
// registered — a researcher promised a search it does not have spends a
// round finding that out and then guesses at the URL.
func TestWebTools_TheFetchOnlySentenceIsThereExactlyWhenSearchIsNot(t *testing.T) {
	info := shell.Info{Shell: "bash", OS: "linux", Cwd: "/tmp"}
	both := BuildResearcher(info, WebTools{Fetch: true, Search: true})
	fetchOnly := BuildResearcher(info, WebTools{Fetch: true})
	none := BuildResearcher(info, WebTools{})

	if strings.Contains(both, noSearch) {
		t.Error("a session with a search backend was told it has none")
	}
	if !strings.Contains(both, "web_search") {
		t.Error("a session with both tools was not told about search")
	}
	if !strings.Contains(fetchOnly, noSearch) {
		t.Error("a session with fetch alone was not told search is missing")
	}
	if strings.Contains(fetchOnly, "web_search") {
		t.Error("a session with fetch alone was told about web_search anyway")
	}
	if strings.Contains(none, "web_fetch") || !strings.Contains(none, "no web tools at all") {
		t.Errorf("a session with no web tools was told about them:\n%s", none)
	}

	// A profile that researches gets the same sentence on the same terms.
	withSearch := BuildProfile(info, ProfileSpec{Name: "auditor", Tools: []string{"read_file", "web_fetch", "web_search"}})
	withoutSearch := BuildProfile(info, ProfileSpec{Name: "auditor", Tools: []string{"read_file", "web_fetch"}})
	if strings.Contains(withSearch, noSearch) {
		t.Error("a profile granted search was told it has none")
	}
	if !strings.Contains(withoutSearch, noSearch) {
		t.Error("a profile granted only fetch was not told search is missing")
	}
	// A profile with no web tools is told nothing about the web at all.
	noWeb := BuildProfile(info, ProfileSpec{Name: "auditor", Tools: []string{"read_file"}})
	if strings.Contains(noWeb, noSearch) || strings.Contains(noWeb, "Web tools") {
		t.Error("a profile with no web tools was told about the web")
	}
}
