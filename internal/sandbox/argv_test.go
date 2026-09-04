package sandbox

import (
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeSocketPath is a real socket file for the tests that need the mask to
// have somewhere to land: the mask only covers a path something is listening
// on, so a made-up one would assert nothing.
func writeSocketPath(t *testing.T) string {
	t.Helper()
	// A unix socket path is capped at about a hundred bytes, and a scratch
	// directory named after the test runs past that on macOS, where the
	// three tests that need one would then skip and the Seatbelt half of
	// the mask would never be put to a kernel. A short scratch directory
	// under the same temp root keeps them running.
	dir, err := os.MkdirTemp("", "shhh-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "agent.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Skipf("no unix socket here: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	// Resolved, because that is the spelling the mask uses and a scratch
	// directory is behind a symlink on some hosts.
	resolved, err := resolvePath(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestResolveReadOnlyWorkspaceOmitsWorkspaceGrant(t *testing.T) {
	testHome(t)
	policy, ws := workspacePolicy(t)
	resolvedWS, _ := filepath.EvalSymlinks(ws)

	s, err := resolvePolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.write, resolvedWS) {
		t.Fatalf("default policy must grant workspace write, write=%v", s.write)
	}

	policy.ReadOnlyWorkspace = true
	s, err = resolvePolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(s.write, resolvedWS) {
		t.Fatalf("read-only policy must withhold the workspace grant, write=%v", s.write)
	}
	if s.workspace != resolvedWS {
		t.Fatalf("workspace should still resolve, got %q", s.workspace)
	}
}

func TestWrapArgvBwrapRunsArgvDirectly(t *testing.T) {
	testHome(t)
	policy, _ := workspacePolicy(t)
	avail := Availability{Mechanism: "bwrap", OK: true}

	argv, err := WrapArgv(avail, policy, []string{"/usr/bin/go", "test", "./..."})
	if err != nil {
		t.Fatal(err)
	}
	sep := slices.Index(argv, "--")
	if sep < 0 {
		t.Fatalf("bwrap argv needs the -- separator: %v", argv)
	}
	if got := argv[sep+1:]; !slices.Equal(got, []string{"/usr/bin/go", "test", "./..."}) {
		t.Fatalf("the check argv must ride verbatim after --, got %v", got)
	}
	if slices.Contains(argv, "-c") {
		t.Fatalf("no shell may sit between containment and the check: %v", argv)
	}
}

func TestWrapArgvSeatbeltRunsArgvDirectly(t *testing.T) {
	testHome(t)
	policy, _ := workspacePolicy(t)
	avail := Availability{Mechanism: "sandbox-exec", OK: true}

	argv, err := WrapArgv(avail, policy, []string{"/usr/bin/go", "vet"})
	if err != nil {
		t.Fatal(err)
	}
	if argv[0] != seatbeltPath || argv[1] != "-p" {
		t.Fatalf("seatbelt prefix missing: %v", argv)
	}
	if got := argv[len(argv)-2:]; !slices.Equal(got, []string{"/usr/bin/go", "vet"}) {
		t.Fatalf("the check argv must ride verbatim at the end, got %v", got)
	}
	if strings.Contains(strings.Join(argv, " "), " -c ") {
		t.Fatalf("no shell may sit between containment and the check: %v", argv)
	}
}

func TestWrapArgvRefusals(t *testing.T) {
	testHome(t)
	policy, _ := workspacePolicy(t)

	if _, err := WrapArgv(Availability{Detail: "nope"}, policy, []string{"true"}); err == nil || !strings.Contains(err.Error(), "wrap unsupported") {
		t.Fatalf("unavailable mechanism must refuse, got %v", err)
	}
	if _, err := WrapArgv(Availability{Mechanism: "bwrap", OK: true}, policy, nil); err == nil {
		t.Fatal("empty argv must refuse")
	}
	if _, err := WrapArgv(Availability{Mechanism: "warden", OK: true}, policy, []string{"true"}); err == nil {
		t.Fatal("unknown mechanism must refuse")
	}
}

// A command is over by the time its result is read and a started process is
// not, so the report counts the ones still running — under the mechanism
// where there is one, and unconfined where there is not.
func TestReportCountsRunningProcesses(t *testing.T) {
	testHome(t)
	policy, _ := workspacePolicy(t)
	contained := Availability{Mechanism: "bwrap", OK: true, Detail: "ok"}

	if r := Report(contained, policy, 0); !strings.Contains(r, "processes: none running") {
		t.Fatalf("a session with no processes should say so:\n%s", r)
	}
	if r := Report(contained, policy, 1); !strings.Contains(r, "processes: 1 process running under it") {
		t.Fatalf("one contained process should be counted singular:\n%s", r)
	}
	if r := Report(contained, policy, 3); !strings.Contains(r, "processes: 3 processes running under it") {
		t.Fatalf("contained processes should be counted under the mechanism:\n%s", r)
	}
	bare := Availability{Detail: "bubblewrap (bwrap) not found on PATH"}
	if r := Report(bare, policy, 2); !strings.Contains(r, "processes: 2 processes running unconfined") {
		t.Fatalf("with no mechanism the report must not soften it:\n%s", r)
	}
}

// The namespaces and the environment are the half of containment that needs
// nothing configured, so they are asserted on the argv rather than left to
// the one host that happens to run the integration test.
func TestBwrapUnsharesTheNamespacesAndRebuildsTheEnvironment(t *testing.T) {
	testHome(t)
	t.Setenv("PATH", "/usr/bin:/bin")
	sock := writeSocketPath(t)
	t.Setenv("SSH_AUTH_SOCK", sock)
	t.Setenv("AWS_SESSION_TOKEN", "borrowed")
	policy, _ := workspacePolicy(t)
	policy.Env = []string{"PATH=/usr/bin:/bin", "SSH_AUTH_SOCK=" + sock, "DEPLOY_KEY=hunter2"}
	policy.SecretNames = []string{"DEPLOY_KEY"}

	argv, err := Wrap(Availability{Mechanism: "bwrap", OK: true}, policy, "true")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--unshare-pid", "--unshare-ipc", "--unshare-uts", "--clearenv"} {
		if !slices.Contains(argv, want) {
			t.Errorf("the wrap must carry %s: %v", want, argv)
		}
	}
	line := strings.Join(argv, " ")
	if !strings.Contains(line, "--setenv PATH /usr/bin:/bin") {
		t.Errorf("a command with no PATH cannot find a program: %v", argv)
	}
	// The vault's names cross because the vault named them, and nothing else
	// about a name is consulted.
	if !strings.Contains(line, "--setenv DEPLOY_KEY hunter2") {
		t.Errorf("a declared secret must still reach the command: %v", argv)
	}
	// The agent socket is the whole reason the environment is an allowlist:
	// a contained command that inherits it is holding a signing oracle.
	for i, a := range argv {
		if a == "--setenv" && i+1 < len(argv) && argv[i+1] == "SSH_AUTH_SOCK" {
			t.Errorf("the agent socket address crossed into containment: %v", argv)
		}
	}
	// A variable nobody named does not travel, whatever it looks like.
	if strings.Contains(line, "AWS_SESSION_TOKEN") {
		t.Errorf("an unnamed variable must not cross: %v", argv)
	}
}

// The address is gone from the environment, but the path is a convention as
// much as an address, so each mechanism also refuses the socket itself.
func TestTheAgentSocketIsMaskedByBothMechanisms(t *testing.T) {
	testHome(t)
	sock := writeSocketPath(t)
	t.Setenv("SSH_AUTH_SOCK", sock)
	policy, _ := workspacePolicy(t)

	argv, err := Wrap(Availability{Mechanism: "bwrap", OK: true}, policy, "true")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(argv, " "), "--ro-bind /dev/null "+sock) {
		t.Errorf("bubblewrap should mask the agent socket: %v", argv)
	}

	argv, err = Wrap(Availability{Mechanism: "sandbox-exec", OK: true}, policy, "true")
	if err != nil {
		t.Fatal(err)
	}
	profile := argv[2]
	if !strings.Contains(profile, `(deny network-outbound`) || !strings.Contains(profile, sbplQuote(sock)) {
		t.Errorf("Seatbelt should deny the agent socket:\n%s", profile)
	}
	// Seatbelt has no environment option of its own, so the allowlist is
	// applied by starting from nothing on the way in.
	if !slices.Contains(argv, "-i") || !slices.Contains(argv, envPath) {
		t.Errorf("Seatbelt should hand the command a rebuilt environment: %v", argv)
	}
}

// A host with no agent binds nothing rather than failing the wrap.
func TestNoAgentSocketMasksNothing(t *testing.T) {
	testHome(t)
	t.Setenv("SSH_AUTH_SOCK", "")
	policy, _ := workspacePolicy(t)

	argv, err := Wrap(Availability{Mechanism: "bwrap", OK: true}, policy, "true")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(argv, " "), "--ro-bind /dev/null") {
		t.Errorf("nothing to mask, nothing bound: %v", argv)
	}
}

// A mount needs somewhere to land and bubblewrap fails the whole wrap when
// it has nowhere: an address left over from a dead agent would stop every
// command in the session rather than hide a socket that is not there. So a
// path nothing is listening on is masked by not being masked.
func TestAStaleAgentAddressMasksNothingAndStillRuns(t *testing.T) {
	testHome(t)
	dir := t.TempDir()
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(dir, "gone.sock"))
	policy, ws := workspacePolicy(t)
	policy.Cwd = ws

	argv, err := Wrap(Availability{Mechanism: "bwrap", OK: true}, policy, "true")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(argv, "--ro-bind") && strings.Contains(strings.Join(argv, " "), "gone.sock") {
		t.Fatalf("a path nothing listens on must not be bound over: %v", argv)
	}
	if avail := detectBwrap(); avail.OK {
		// The claim the argv cannot make: bubblewrap accepts this wrap.
		argv, err = Wrap(avail, policy, "echo ran")
		if err != nil {
			t.Fatal(err)
		}
		if out, err := capture(t, argv[0], argv[1:]...); err != nil || !strings.Contains(out, "ran") {
			t.Fatalf("a stale agent address must not fail the wrap: %v: %s", err, out)
		}
	}
}
