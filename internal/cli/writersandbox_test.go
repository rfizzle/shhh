package cli

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// standInContainment answers childContainment with avail for one test. A
// mechanism nothing knows how to wrap for stands in for "a host with one":
// the runner takes the contained path and the wrap refuses to build, so a
// command that reached the host bare and one that was contained are told
// apart without a mechanism on the machine.
func standInContainment(t *testing.T, avail sandbox.Availability) {
	t.Helper()
	restore := childContainment
	t.Cleanup(func() { childContainment = restore })
	childContainment = func() sandbox.Availability { return avail }
}

var (
	standInMechanism = sandbox.Availability{OK: true, Mechanism: "stand-in", Detail: "stand-in"}
	noMechanism      = sandbox.Availability{Detail: "nothing here contains a command"}
)

func requireSandboxOff() config.Config {
	return config.Config{Agents: config.AgentsConfig{RequireSandbox: new(bool)}}
}

// A writer's commands are contained by default: on a host with a mechanism
// they take the contained path whatever the session requires, on a host
// without one they are refused with the doctor's fix, agents.require_sandbox
// off hands the writer back to the session's own rule, and sandbox.require on
// the session still wins. A child that is not a writer keeps the session's
// rule — a researcher and a reviewer hold no command at all.
// See docs/capabilities/containment.md#containment-can-be-required.
func TestAWritersCommandsAreContainedByDefault(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	required := config.Config{Sandbox: config.SandboxConfig{Require: true}}
	requiredOptedOut := requireSandboxOff()
	requiredOptedOut.Sandbox.Require = true
	fix := doctorSandbox(noMechanism, sandbox.Policy{}, runtime.GOOS).Fix[0]

	for _, tc := range []struct {
		name   string
		cfg    config.Config
		writer bool
		avail  sandbox.Availability
		// want is ran, contained or the refusal's lead.
		want string
	}{
		{"a writer on a host with a mechanism", config.Config{}, true, standInMechanism, "contained"},
		{"a writer with the default off on a host with a mechanism", requireSandboxOff(), true, standInMechanism, "contained"},
		{"a writer on a host with none", config.Config{}, true, noMechanism, "a writer's commands require containment"},
		{"a writer with the default off", requireSandboxOff(), true, noMechanism, "ran"},
		{"a writer in a session that requires containment", required, true, noMechanism, "this session requires containment"},
		{"a writer with the default off in a session that requires it", requiredOptedOut, true, noMechanism, "this session requires containment"},
		{"a child that is not a writer", config.Config{}, false, noMechanism, "ran"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			standInContainment(t, tc.avail)
			dir := t.TempDir()
			sc, errs := scope.New(dir)
			if len(errs) > 0 {
				t.Fatalf("scope: %v", errs)
			}
			got := childCommandRunnerUnbounded(tc.cfg, dir, sc, tc.writer)(context.Background(), "echo ran")
			switch tc.want {
			case "ran":
				if got.Outcome != tools.ExecSucceeded || !strings.Contains(got.Output, "ran") {
					t.Fatalf("the command did not run: %+v", got)
				}
			case "contained":
				// The stand-in cannot be wrapped for, so the contained path
				// ends in a wrap that did not build — never in the command
				// running bare, and never in the refusal.
				if got.Outcome != tools.ExecDidNotStart || got.Prereq != tools.PrereqContainment ||
					!strings.Contains(got.Output, "unknown mechanism") {
					t.Fatalf("the command did not take the contained path: %+v", got)
				}
			default:
				if got.Outcome != tools.ExecDidNotStart || strings.Contains(got.Output, "\nran") {
					t.Fatalf("the command was not refused: %+v", got)
				}
				if !strings.Contains(got.Output, tc.want) || !strings.Contains(got.Output, fix) {
					t.Fatalf("the refusal should lead %q and carry the doctor's fix %q, got %q", tc.want, fix, got.Output)
				}
			}
		})
	}
}

// The spawn card, /status and the doctor read one answer about a writer's
// commands.
func TestWriterContainmentIsOneAnswer(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	contained := writerContainment(config.Config{}, standInMechanism)
	if contained.state != writerContained || !strings.HasPrefix(contained.detail, "required · ") ||
		!strings.Contains(contained.detail, "network preserved") {
		t.Errorf("a writer on a host with a mechanism reads %+v", contained)
	}
	if f := contained.field(); f.Label != "commands" || f.Value != "contained · stand-in" || f.Open {
		t.Errorf("the card's row is %+v", f)
	}
	if off := writerContainment(requireSandboxOff(), standInMechanism); off.state != writerContained || off.required {
		t.Errorf("with the default off a contained writer reads %+v", off)
	}

	refused := writerContainment(config.Config{}, noMechanism)
	if refused.state != writerRefused || !strings.Contains(refused.detail, noMechanism.Detail) {
		t.Errorf("a writer on a host with none reads %+v", refused)
	}
	if line := refused.line(); line != "a writer's commands: refused — no containment mechanism is in force: "+noMechanism.Detail {
		t.Errorf("the status line is %q", line)
	}

	open := writerContainment(requireSandboxOff(), noMechanism)
	if open.state != writerUncontained || !open.field().Open {
		t.Errorf("an opted-out writer on a host with none reads %+v, want an open row", open)
	}
	if session := writerContainment(config.Config{Sandbox: config.SandboxConfig{Require: true, Profile: "workspace"}, Agents: config.AgentsConfig{RequireSandbox: new(bool)}}, noMechanism); session.state != writerRefused {
		t.Errorf("sandbox.require on the session should still refuse a writer, got %+v", session)
	}
}

// The doctor's sandbox row names the writer default in each state.
func TestDoctorSandboxRowNamesTheWriterDefault(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	policy := sandbox.Policy{Profile: sandbox.ProfileWorkspace}
	ok := sandbox.Availability{OK: true, Mechanism: "bwrap", Detail: "bubblewrap"}

	f := withWriterDefault(doctorSandbox(ok, policy, "linux"), writerContainment(config.Config{}, ok))
	if !strings.HasSuffix(f.Detail, "writers must be contained") {
		t.Errorf("contained, required: %q", f.Detail)
	}
	f = withWriterDefault(doctorSandbox(ok, policy, "linux"), writerContainment(requireSandboxOff(), ok))
	if !strings.Contains(f.Detail, "agents.require_sandbox off") {
		t.Errorf("contained, not required: %q", f.Detail)
	}
	f = withWriterDefault(doctorSandbox(noMechanism, policy, "linux"), writerContainment(config.Config{}, noMechanism))
	if f.State != components.DoctorFailed || !strings.HasSuffix(f.Consequence, "a writer's commands are refused until one is") {
		t.Errorf("none, required: %+v", f)
	}
	f = withWriterDefault(doctorSandbox(noMechanism, policy, "linux"), writerContainment(requireSandboxOff(), noMechanism))
	if !strings.Contains(f.Consequence, "run as you too (agents.require_sandbox off)") {
		t.Errorf("none, not required: %+v", f)
	}
}

// The model is told: a writer whose every command will be refused reads one
// paragraph saying so, on the request and never in its conversation; a
// contained writer, an opted-out one and a child that is not a writer read
// nothing new.
func TestAWriterWhoseCommandsAreRefusedIsToldSo(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cfg    config.Config
		writer bool
		avail  sandbox.Availability
		want   bool
	}{
		{"refused", config.Config{}, true, noMechanism, true},
		{"contained", config.Config{}, true, standInMechanism, false},
		{"opted out", requireSandboxOff(), true, noMechanism, false},
		{"not a writer", config.Config{}, false, noMechanism, false},
	} {
		if got := childCommandsRefused(tc.cfg, tc.writer, tc.avail); got != tc.want {
			t.Errorf("%s: told = %v, want %v", tc.name, got, tc.want)
		}
	}
	conversation := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "task"},
	}
	got := withRefusedCommands(conversation)
	if got[0].Content != "sys\n\n"+prompt.UncontainedWriterInstructions {
		t.Errorf("the request's system prompt is %q", got[0].Content)
	}
	if conversation[0].Content != "sys" {
		t.Fatalf("the paragraph was written into the conversation: %q", conversation[0].Content)
	}
}

// The writer default reaches the stamp, so a cohort comparison can split on
// it: unset is on, and a file that turned it off is recorded as off.
func TestTheWriterDefaultIsStamped(t *testing.T) {
	if got := sessionSettings(config.Config{}, runSettings{}); !got.AgentsRequireSandbox {
		t.Error("an unset agents.require_sandbox was stamped off")
	}
	if got := sessionSettings(requireSandboxOff(), runSettings{}); got.AgentsRequireSandbox {
		t.Error("agents.require_sandbox = false was stamped on")
	}
}

// Only a child handed execute_command has a commands row or a paragraph about
// refused commands: a profile that may write and not execute runs nothing.
func TestOnlyAWriterThatRunsCommandsIsToldAboutThem(t *testing.T) {
	agents := &agentProfiles{definitions: map[string]config.AgentDefinition{
		"editor": {Name: "editor", Permissions: []string{config.PermissionWrite}},
		"runner": {Name: "runner", Permissions: []string{config.PermissionWrite, config.PermissionExecute}},
	}}
	for role, want := range map[string]bool{"writer": true, "editor": false, "runner": true, "researcher": false} {
		if got := agents.runsCommands(subagent.Role(role)); got != want {
			t.Errorf("%s runs commands = %v, want %v", role, got, want)
		}
	}
	if holdsCommand(tools.Definitions()) || !holdsCommand(tools.DefinitionsFull()) {
		t.Error("holdsCommand should find execute_command in the full toolset alone")
	}
}

// Every surface's containment refusal is filed under the containment class:
// the session's and a headless run's (both are buildContainment's Refusal,
// worded by uncontainedRefusal) and a child's, whether the session or the
// writer default required it. Before the clause was read, the host's own
// reason decided the class — "not found" on Linux and macOS, "other"
// elsewhere — so the record could not say how often the harness, not the
// model, stopped a command.
// See docs/capabilities/containment.md#containment-can-be-required.
func TestEveryContainmentRefusalIsFiledAsContainment(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	required := config.Config{Sandbox: config.SandboxConfig{Require: true}}
	for _, detail := range []string{
		"bubblewrap (bwrap) not found on PATH",
		"sandbox-exec not found at /usr/bin/sandbox-exec",
		"no containment mechanism for windows",
	} {
		avail := sandbox.Availability{Detail: detail}
		for surface, refusal := range map[string]string{
			"session":                     uncontainedRefusal(avail),
			"child of a required session": childCommandRefusal(required, false, avail),
			"writer":                      childCommandRefusal(config.Config{}, true, avail),
		} {
			if got := observe.ClassFromResult(refusal); got != observe.ClassHarnessContainment {
				t.Errorf("%s, %q: filed as %q, want %q", surface, detail, got, observe.ClassHarnessContainment)
			}
		}
	}
}

// The refusal a child's gate answers with ahead of the card is the one its
// runner answers with behind an approval, and only where the command must be
// contained and nothing can contain it: with a mechanism, with the writer
// default off, or for a child that is not a writer, the gate is handed
// nothing and the card is raised as before.
// See docs/capabilities/containment.md#containment-can-be-required.
func TestAChildsGateAndRunnerRefuseTheSameCommands(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	required := config.Config{Sandbox: config.SandboxConfig{Require: true}}
	for _, tc := range []struct {
		name    string
		cfg     config.Config
		writer  bool
		avail   sandbox.Availability
		refused bool
	}{
		{"a writer on a host with none", config.Config{}, true, noMechanism, true},
		{"a writer on a host with a mechanism", config.Config{}, true, standInMechanism, false},
		{"a writer with the default off", requireSandboxOff(), true, noMechanism, false},
		{"a child that is not a writer", config.Config{}, false, noMechanism, false},
		{"a child in a session that requires containment", required, false, noMechanism, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gate := childCommandRefusal(tc.cfg, tc.writer, tc.avail)
			if (gate != "") != tc.refused {
				t.Fatalf("the gate's refusal is %q, want refused = %v", gate, tc.refused)
			}
			if !tc.refused {
				return
			}
			dir := t.TempDir()
			sc, errs := scope.New(dir)
			if len(errs) > 0 {
				t.Fatalf("scope: %v", errs)
			}
			got := childCommandRunnerIn(tc.cfg, dir, sc, tc.writer, tc.avail)(context.Background(), "echo ran")
			if got.Output != gate {
				t.Fatalf("the runner refused with %q, the gate with %q", got.Output, gate)
			}
		})
	}
}
