//go:build !windows

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// sandboxTestImage is a digest-pinned reference the policy accepts; no
// engine ever pulls it, since the engine here is a script.
var sandboxTestImage = "example.com/shhh-sandbox@sha256:" + strings.Repeat("a", 64)

// sandboxTestConfig is the config table a sandbox session in these tests
// reads, as appended to a config file.
var sandboxTestConfig = "\n[sandbox]\ncontainer_engine = \"docker\"\ncontainer_image = \"" + sandboxTestImage + "\"\nprofile = \"workspace-netless\"\n"

// fakeSandboxEngine puts a `docker` on PATH that writes down every call and
// answers the ones a sandbox session makes: the probe, the create, the
// helper's --version with helper, an exec by running the argv after `--`
// here, and the remove. It hands back the log.
//
// With engineHelperEnv naming a program, an exec runs that program as the
// helper instead, with the helper's own arguments — the second exec's
// `--stop` among them — and with engineHoldEnv set too the helper's stdin
// is one that never hangs up: an engine that holds the stream open after
// its client has let go.
func fakeSandboxEngine(t *testing.T, helper string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\n" +
		"case \"$1\" in\n" +
		"info) echo '[name=seccomp]' ;;\n" +
		"run) echo 0123456789ab ;;\n" +
		"rm|inspect) ;;\n" +
		"image) echo 'Error: No such image' >&2; exit 1 ;;\n" +
		"exec)\n" +
		"  for last; do :; done\n" +
		"  if [ \"$last\" = --version ]; then " + helper + "; exit; fi\n" +
		"  if [ -n \"$" + engineHelperEnv + "\" ]; then\n" +
		"    while [ $# -gt 0 ] && [ \"$1\" != " + sandbox.HelperPath + " ]; do shift; done\n" +
		"    shift\n" +
		"    if [ -n \"$" + engineHoldEnv + "\" ]; then exec \"$" + engineHelperEnv + "\" \"$@\" </dev/zero; fi\n" +
		"    exec \"$" + engineHelperEnv + "\" \"$@\"\n" +
		"  fi\n" +
		"  while [ $# -gt 0 ] && [ \"$1\" != -- ]; do shift; done\n" +
		"  shift\n" +
		"  exec \"$@\" ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// helperAnswers is the helper a released image carries.
var helperAnswers = "echo 'shhh-sandbox-exec " + sandbox.HelperVersion + "'"

func sandboxTestSetup(t *testing.T) (config.Config, *scope.Scope, string) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	withProjectTrust(t, project.Trust{Root: t.TempDir()})
	ws := t.TempDir()
	sc, errs := scope.New(ws)
	if len(errs) > 0 {
		t.Fatalf("scope: %v", errs)
	}
	cfg := config.Config{Sandbox: config.SandboxConfig{
		ContainerEngine: "docker",
		ContainerImage:  sandboxTestImage,
		Profile:         string(sandbox.ProfileWorkspaceNetless),
	}}
	return cfg, sc, ws
}

// A sandbox session's containment is values backed by its container: the
// commands go through the helper, attached, the chip names the container and
// the profile, the network is the profile's switch and no host list, and
// nothing wraps a hook. A process start goes in the container, and the
// container is destroyed by the cleanup it came with.
func TestSandboxContainmentValues(t *testing.T) {
	log := fakeSandboxEngine(t, helperAnswers)
	cfg, sc, ws := sandboxTestSetup(t)
	sup, err := process.New(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sup.Close)

	c, cleanup, err := sandboxContainment(context.Background(), cfg, ws, sc, sup, nil)
	if err != nil {
		t.Fatalf("sandboxContainment: %v", err)
	}
	if c.Mechanism != "container sandbox" || c.Profile != "workspace-netless" || c.Network || len(c.Hosts) != 0 {
		t.Errorf("mechanism %q, profile %q, network %v, hosts %v", c.Mechanism, c.Profile, c.Network, c.Hosts)
	}
	if c.Wrap != nil {
		t.Error("a sandbox session wraps a hook, which cannot follow the commands into the container")
	}
	if c.Manage == nil || !strings.Contains(c.Manage([]string{"scope"}), "/sandbox scope") {
		t.Error("the /sandbox subcommands are not wired")
	}
	if !strings.Contains(c.Detail, "docker") || !strings.Contains(c.Detail, "example.com/shhh-sandbox") {
		t.Errorf("detail = %q, want the engine and the image", c.Detail)
	}
	if !strings.Contains(c.Status, "container sandbox") || !strings.Contains(c.Writers, "refused") {
		t.Errorf("status %q, writers %q", c.Status, c.Writers)
	}

	if got := c.Run(context.Background(), "echo from-the-container"); got.ExitCode != 0 || !strings.Contains(got.Output, "from-the-container") {
		t.Errorf("Run = %+v", got)
	}
	var lines []string
	if got := c.TailRun(context.Background(), "echo one; echo two", func(l string) { lines = append(lines, l) }); got.ExitCode != 0 || strings.Join(lines, ",") != "one,two" {
		t.Errorf("TailRun = %+v, lines %q", got, lines)
	}
	calls := engineCalls(t, log)
	want := "exec -i --workdir /workspace shhh-sbx-"
	helper := helperCall(" --stdin=null --hangup=stop -- /bin/sh -c echo from-the-container")
	if !strings.Contains(calls, want) || !helper.MatchString(calls) {
		t.Errorf("the command did not go through the helper:\n%s", calls)
	}
	if !strings.Contains(calls, sandbox.ExecArg+" --version") {
		t.Errorf("the helper was not probed:\n%s", calls)
	}

	// What the model is told: a disposable container, its profile and its
	// network, and a ceiling that moves a command to the background.
	cfg.Behavior.CommandTimeoutSeconds = 600
	said := commandEnvironmentBlock(sandboxCommandEnvironment(c, cfg, true))
	for _, want := range []string{"contained by a disposable container under the workspace-netless profile", "no network at all", "moved to the background"} {
		if !strings.Contains(said, want) {
			t.Errorf("the prompt does not say %q:\n%s", want, said)
		}
	}

	cleanup()
	if calls := engineCalls(t, log); !strings.Contains(calls, "rm --force shhh-sbx-") {
		t.Errorf("the cleanup did not remove the container:\n%s", calls)
	}
}

// A sandbox that cannot be made is a session that does not open — never one
// that opens under the host's mechanism, or under nothing. No engine is
// refused before anything is created; an engine whose image lacks the helper
// is refused with the image named and the fix, and the container it made for
// the probe is removed again.
func TestSandboxSessionRefusesToDowngrade(t *testing.T) {
	cfg, sc, ws := sandboxTestSetup(t)
	t.Setenv("PATH", t.TempDir())
	_, _, err := sandboxContainment(context.Background(), cfg, ws, sc, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "container") {
		t.Fatalf("no engine: %v, want a refusal", err)
	}

	_, built, out, err := assembleSessionWith(t, sandboxTestConfig, "code", "--sandbox")
	if built || err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("a sandbox session with no engine opened anyway (built %v, err %v)\n%s", built, err, out)
	}
}

func TestSandboxSessionRefusesAnImageWithoutTheHelper(t *testing.T) {
	log := fakeSandboxEngine(t, "echo 'OCI runtime exec failed: no such file or directory' >&2; exit 126")
	cfg, sc, ws := sandboxTestSetup(t)
	_, _, err := sandboxContainment(context.Background(), cfg, ws, sc, nil, nil)
	if err == nil {
		t.Fatal("an image without the helper was accepted")
	}
	for _, want := range []string{sandboxTestImage, "command helper", "leave sandbox.container_image unset"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%v", want, err)
		}
	}
	if calls := engineCalls(t, log); !strings.Contains(calls, "rm --force shhh-sbx-") {
		t.Errorf("the container made for the probe was left behind:\n%s", calls)
	}
}

// --sandbox opens the session rather than a headless run, its commands in
// the container and its prompt saying so.
func TestSandboxNoLongerImpliesPrint(t *testing.T) {
	fakeSandboxEngine(t, helperAnswers)
	w, built, out, err := assembleSessionWith(t, sandboxTestConfig, "code", "--sandbox")
	if err != nil || !built {
		t.Fatalf("shhh code --sandbox did not open a session (built %v): %v\n%s", built, err, out)
	}
	if w.Containment.Mechanism != "container sandbox" {
		t.Errorf("the session's containment is %q, not the container", w.Containment.Mechanism)
	}
}

// The container goes with the session: it is removed once the session has
// ended, and a session opened again — a resume — makes a container of its
// own rather than finding the last one.
func TestSandboxSessionDestroysItsContainer(t *testing.T) {
	log := fakeSandboxEngine(t, helperAnswers)
	for range 2 {
		if _, built, out, err := assembleSessionWith(t, sandboxTestConfig, "code", "--sandbox"); err != nil || !built {
			t.Fatalf("the session did not open (built %v): %v\n%s", built, err, out)
		}
	}
	calls := engineCalls(t, log)
	if runs, rms := strings.Count(calls, "run --detach"), strings.Count(calls, "rm --force shhh-sbx-"); runs != 2 || rms != 2 {
		t.Errorf("%d containers made and %d removed, want 2 and 2:\n%s", runs, rms, calls)
	}
}

// A scripted --sandbox run is built on the session's builder and keeps what
// it refuses: a hook is not run and says so. A command at its ceiling goes to
// the supervisor as anywhere, and the model is told so.
func TestPrintSandboxKeepsItsRefusals(t *testing.T) {
	fakeSandboxEngine(t, helperAnswers)
	cfg, sc, ws := sandboxTestSetup(t)
	sup, err := process.New(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sup.Close)
	box, cleanup, err := startPrintSandbox(context.Background(), cfg, sc, sup, nil)
	if err != nil {
		t.Fatalf("startPrintSandbox: %v", err)
	}
	defer cleanup()
	if box.noHooks == "" || box.wrap != nil {
		t.Error("a --sandbox run builds hooks")
	}
	if !box.said.Backgrounds || box.said.Mechanism != "a disposable container" || box.profile != "workspace-netless" {
		t.Errorf("said %+v, profile %q", box.said, box.profile)
	}
	if got := box.run(context.Background(), "echo ran"); !strings.Contains(got.Output, "ran") {
		t.Errorf("run = %+v", got)
	}
}

// The declared tools a sandbox session reports missing are the ones its
// container lacks, asked of the container through the helper — not the ones
// this machine's PATH lacks — and nothing is offered to install them here:
// the image is where a sandbox's tools come from.
func TestSandboxToolchainReadsTheContainer(t *testing.T) {
	log := fakeSandboxEngine(t, helperAnswers)
	cfg, sc, ws := sandboxTestSetup(t)
	writeDeclaration(t, "check = [\"sh\", \"shhh-no-such-tool\"]\n")
	c, cleanup, err := sandboxContainment(context.Background(), cfg, ws, sc, nil, nil)
	if err != nil {
		t.Fatalf("sandboxContainment: %v", err)
	}
	defer cleanup()
	if got := strings.Join(c.Toolchain.Missing, ","); got != "shhh-no-such-tool" {
		t.Errorf("missing = %q, want the one tool the container lacks", got)
	}
	if c.Toolchain.Install != nil || c.Toolchain.Refusal == "" || c.Toolchain.Runnable() {
		t.Error("a sandbox session offered to install a declared tool on this machine")
	}
	if calls := engineCalls(t, log); !strings.Contains(calls, "command -v") {
		t.Errorf("the container was not asked:\n%s", calls)
	}
}

// In a sandbox session no child runs a command, writer or not: a child's
// commands do not follow the session's into its container, and run on the
// host they would be outside what the person asked for. Outside one, a child
// that holds a command on a host with a mechanism is given a runner.
func TestSandboxSessionRefusesEveryChildsCommands(t *testing.T) {
	cfg, sc, ws := sandboxTestSetup(t)
	avail := sandbox.Availability{OK: true, Mechanism: "bwrap"}
	for _, writer := range []bool{false, true} {
		run, refusal := childCommands(cfg, ws, sc, writer, avail, true)
		if run != nil || refusal != sandboxChildRefusal {
			t.Errorf("writer %v: a sandbox session's child was given a runner (refusal %q)", writer, refusal)
		}
	}
	if run, refusal := childCommands(cfg, ws, sc, false, avail, false); run == nil || refusal != "" {
		t.Errorf("outside a sandbox session a reader with a mechanism got no runner (refusal %q)", refusal)
	}
}

// The doctor's image row says whether the image carries the helper, and an
// image without it is the one reading that warns: a sandbox session will
// refuse it.
func TestDoctorImageSaysWhetherTheImageCarriesTheHelper(t *testing.T) {
	r := preparedReading{}
	if f := doctorImage(r); strings.Contains(f.Detail, "helper") {
		t.Errorf("nothing was read and the row says %q", f.Detail)
	}
	r.helper = sandbox.HelperCarried
	if f := doctorImage(r); !strings.Contains(f.Detail, "carries the command helper") || f.State == components.DoctorWarned {
		t.Errorf("carried: %+v", f)
	}
	r.helper = sandbox.HelperLacking
	if f := doctorImage(r); f.Outcome != "no helper" || f.State != components.DoctorWarned || len(f.Fix) == 0 {
		t.Errorf("lacking: %+v", f)
	}
	r.helper = sandbox.HelperUnpulled
	if f := doctorImage(r); !strings.Contains(f.Detail, "not pulled") {
		t.Errorf("unpulled: %+v", f)
	}
}

// `shhh chat --sandbox` is refused in a sentence: a conversation runs no
// command, so a container would contain nothing.
func TestChatRefusesSandbox(t *testing.T) {
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"chat", "--sandbox"})
	if err := execute(context.Background(), cmd); !errors.Is(err, errConversationSandbox) {
		t.Fatalf("shhh chat --sandbox = %v\n%s", err, out.String())
	}
	if f := newChatCmd().Flags().Lookup("sandbox"); f == nil || !f.Hidden {
		t.Error("the refused flag is offered in the help")
	}
}

// sandboxProcessSession is a sandbox session's supervisor with the container
// behind it, for the process tests below.
func sandboxProcessSession(t *testing.T) (*process.Supervisor, string) {
	t.Helper()
	log := fakeSandboxEngine(t, helperAnswers)
	cfg, sc, ws := sandboxTestSetup(t)
	sup, err := process.New(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sup.Close)
	_, cleanup, err := sandboxContainment(context.Background(), cfg, ws, sc, sup, nil)
	if err != nil {
		t.Fatalf("sandboxContainment: %v", err)
	}
	t.Cleanup(cleanup)
	return sup, log
}

// untilProcess polls the process tool until its answer holds want.
func untilProcess(t *testing.T, sup *process.Supervisor, args, want string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, err := sup.Execute(json.RawMessage(args))
		if err == nil && strings.Contains(got, want) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never answered %q; last %q, %v", args, want, got, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A process a sandbox session starts is an exec through the helper with the
// stream as its stdin, and what it prints is read through the client's stdout
// as it is for any process.
func TestContainerProcessOutputIsRead(t *testing.T) {
	sup, log := sandboxProcessSession(t)
	if _, err := sup.Execute(json.RawMessage(`{"action":"start","name":"dev","command":"echo ready; sleep 30"}`)); err != nil {
		t.Fatalf("start: %v", err)
	}
	untilProcess(t, sup, `{"action":"read","name":"dev"}`, "ready")
	calls := engineCalls(t, log)
	want := "exec -i --workdir /workspace shhh-sbx-"
	helper := helperCall(" --stdin=inherit --hangup=stop -- /bin/sh -c echo ready; sleep 30")
	if !strings.Contains(calls, want) || !helper.MatchString(calls) {
		t.Errorf("the process did not start through the helper with its stdin inherited:\n%s", calls)
	}
}

// Input reaches the process through the client's stdin, and a stop signals
// the client's group — the stop the supervisor already has — which ends the
// exec and so hangs the stream up for the helper. A terminal is asked of the
// engine with -t.
func TestContainerProcessStopReachesTheClient(t *testing.T) {
	sup, log := sandboxProcessSession(t)
	if _, err := sup.Execute(json.RawMessage(`{"action":"start","name":"echoer","command":"cat"}`)); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := sup.Execute(json.RawMessage(`{"action":"input","name":"echoer","text":"hello\n"}`)); err != nil {
		t.Fatalf("input: %v", err)
	}
	untilProcess(t, sup, `{"action":"read","name":"echoer"}`, "hello")
	if _, err := sup.Execute(json.RawMessage(`{"action":"stop","name":"echoer"}`)); err != nil {
		t.Fatalf("stop: %v", err)
	}
	untilProcess(t, sup, `{"action":"status","name":"echoer"}`, "exited")

	if _, err := sup.Execute(json.RawMessage(`{"action":"start","name":"term","command":"sleep 30","pty":true}`)); err != nil {
		t.Fatalf("start with a terminal: %v", err)
	}
	if calls := engineCalls(t, log); !strings.Contains(calls, "exec -i -t --workdir /workspace") {
		t.Errorf("a terminal was not asked of the engine:\n%s", calls)
	}
}

// The stand-in engine's switches: engineHelperEnv names the program it runs
// as the helper, and engineHoldEnv holds the hang-up back from it.
const (
	engineHelperEnv = "SHHH_TEST_ENGINE_HELPER"
	engineHoldEnv   = "SHHH_TEST_ENGINE_HOLD"
)

// helperCall matches the helper's part of an exec in the engine's log: its
// argument, the fresh token every exec is handed, then rest.
func helperCall(rest string) *regexp.Regexp {
	return regexp.MustCompile(regexp.QuoteMeta(sandbox.HelperPath+" "+sandbox.ExecArg+" --id=") + "[0-9a-f]{16}" + regexp.QuoteMeta(rest))
}

// cancelInTheContainer runs, through a sandbox session whose engine runs
// this binary as the helper, a command that leaves a sleep behind it in the
// background, cancels it once the sleep is running, and hands back the
// engine's log once the cancel has returned and the sleep is gone.
func cancelInTheContainer(t *testing.T, hold bool) string {
	t.Helper()
	log := fakeSandboxEngine(t, helperAnswers)
	t.Setenv(engineHelperEnv, os.Args[0])
	if hold {
		t.Setenv(engineHoldEnv, "1")
	}
	t.Setenv("SHHH_SANDBOX_EXEC_DIR", t.TempDir())
	cfg, sc, ws := sandboxTestSetup(t)
	c, cleanup, err := sandboxContainment(context.Background(), cfg, ws, sc, nil, nil)
	if err != nil {
		t.Fatalf("sandboxContainment: %v", err)
	}
	t.Cleanup(cleanup)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pid := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.TailRun(ctx, `sleep 300 & echo "sleeping $!"; wait`, func(line string) {
			if p, ok := strings.CutPrefix(line, "sleeping "); ok {
				pid, _ = strconv.Atoi(p)
				cancel()
			}
		})
	}()
	// The ceiling a cancel is held to: the far end's grace, the second
	// exec's own timeout and the kill, with room for a loaded machine.
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the cancelled command did not return")
	}
	if pid == 0 {
		t.Fatal("the command never said its sleep was running")
	}
	for i := 0; i < 50 && syscall.Kill(pid, 0) == nil; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("the sleep %d outlived the cancel", pid)
	}
	return engineCalls(t, log)
}

// When the engine passes the hang-up on, the helper stops the group and the
// exec ends by itself: the stream was enough, and no second exec is made.
func TestAttachedStopNeedsNoSecondExecWhenTheHangupLands(t *testing.T) {
	calls := cancelInTheContainer(t, false)
	if strings.Contains(calls, "--stop=") {
		t.Errorf("a second exec was made although the hang-up landed:\n%s", calls)
	}
}

// When the engine holds the stream open, the helper never hears the
// hang-up, and the client is still there when the far end's grace is up:
// one second exec, of the helper's stop for the first exec's own token,
// reaches the group it recorded, and nothing of the command is left.
func TestAttachedStopFallsBackToTheSecondExec(t *testing.T) {
	calls := cancelInTheContainer(t, true)
	first := regexp.MustCompile(regexp.QuoteMeta(sandbox.ExecArg+" --id=") + "([0-9a-f]{16})").FindStringSubmatch(calls)
	if first == nil {
		t.Fatalf("no exec through the helper:\n%s", calls)
	}
	stop := "exec shhh-sbx-"
	second := regexp.MustCompile(regexp.QuoteMeta(stop) + `\S+ ` + regexp.QuoteMeta(sandbox.HelperPath+" "+sandbox.ExecArg+" --stop="+first[1]) + "\n")
	if !second.MatchString(calls) || strings.Count(calls, "--stop=") != 1 {
		t.Errorf("want one second exec stopping %s:\n%s", first[1], calls)
	}
}
