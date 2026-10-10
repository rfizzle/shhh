//go:build !windows

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
// nothing wraps a hook. A process start is refused, and the container is
// destroyed by the cleanup it came with.
func TestSandboxContainmentValues(t *testing.T) {
	log := fakeSandboxEngine(t, helperAnswers)
	cfg, sc, ws := sandboxTestSetup(t)
	sup, err := process.New(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sup.Close)

	c, cleanup, err := sandboxContainment(context.Background(), cfg, ws, sc, sup)
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
	helper := sandbox.HelperPath + " " + sandbox.ExecArg + " --stdin=null --hangup=stop -- /bin/sh -c echo from-the-container"
	if !strings.Contains(calls, want) || !strings.Contains(calls, helper) {
		t.Errorf("the command did not go through the helper:\n%s", calls)
	}
	if !strings.Contains(calls, sandbox.ExecArg+" --version") {
		t.Errorf("the helper was not probed:\n%s", calls)
	}

	if _, err := sup.Execute(json.RawMessage(`{"action":"start","name":"dev","command":"sleep 1"}`)); err == nil {
		t.Error("a process started in a sandbox session, where nothing can stop it in the container")
	}

	// What the model is told: a disposable container, its profile and its
	// network, and a ceiling that stops a command rather than moving it.
	cfg.Behavior.CommandTimeoutSeconds = 600
	said := commandEnvironmentBlock(sandboxCommandEnvironment(c, cfg))
	for _, want := range []string{"contained by a disposable container under the workspace-netless profile", "no network at all", "is stopped"} {
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
	_, _, err := sandboxContainment(context.Background(), cfg, ws, sc, nil)
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
	_, _, err := sandboxContainment(context.Background(), cfg, ws, sc, nil)
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
// it refused: a hook is not run and says so, and a command at its ceiling is
// stopped rather than moved to the background.
func TestPrintSandboxKeepsItsRefusals(t *testing.T) {
	fakeSandboxEngine(t, helperAnswers)
	cfg, sc, ws := sandboxTestSetup(t)
	sup, err := process.New(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sup.Close)
	box, cleanup, err := startPrintSandbox(context.Background(), cfg, sc, sup)
	if err != nil {
		t.Fatalf("startPrintSandbox: %v", err)
	}
	defer cleanup()
	if box.noHooks == "" || box.wrap != nil {
		t.Error("a --sandbox run builds hooks")
	}
	if box.said.Backgrounds || box.said.Mechanism != "a disposable container" || box.profile != "workspace-netless" {
		t.Errorf("said %+v, profile %q", box.said, box.profile)
	}
	if _, err := sup.Execute(json.RawMessage(`{"action":"start","name":"dev","command":"sleep 1"}`)); err == nil {
		t.Error("a --sandbox run started a process")
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
	c, cleanup, err := sandboxContainment(context.Background(), cfg, ws, sc, nil)
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
