package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestContainerLifecycleIntegration exercises the real engine end to end:
// create, exec (workspace write-through, clean environment), reconcile, and
// destroy. It needs a container engine and a local digest-pinned image with a
// POSIX sh, so it only runs when SHHH_SANDBOX_IT_IMAGE is set, e.g.:
//
//	SHHH_SANDBOX_IT_IMAGE=docker.io/library/alpine@sha256:… go test ./internal/sandbox -run Integration -v
func TestContainerLifecycleIntegration(t *testing.T) {
	image := os.Getenv("SHHH_SANDBOX_IT_IMAGE")
	if image == "" {
		t.Skip("set SHHH_SANDBOX_IT_IMAGE to a local digest-pinned image to run")
	}
	eng := DetectEngine(os.Getenv("SHHH_SANDBOX_IT_ENGINE"))
	if !eng.OK {
		t.Skipf("no container engine: %s", eng.Detail)
	}

	ctx := context.Background()
	store := NewStoreAt(filepath.Join(t.TempDir(), "sandboxes.json"))
	ws := t.TempDir()

	t.Setenv("SHHH_IT_CANARY", "host-secret")
	c, err := CreateContainer(ctx, eng, ContainerSpec{Image: image, Workspace: ws, Network: true, TTL: time.Hour}, nil, store)
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	destroyed := false
	defer func() {
		if !destroyed {
			_ = DestroyContainer(ctx, eng.Path, store, c.Record)
		}
	}()

	if recs, err := store.List(); err != nil || len(recs) != 1 {
		t.Fatalf("ownership record missing after create: %v, %v", recs, err)
	}

	// Exec runs in the workspace mount; a written file must land on the host.
	out, err := runEngine(ctx, c.ExecArgv("echo from-sandbox > proof.txt && cat proof.txt && pwd"))
	if err != nil {
		t.Fatalf("exec: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "from-sandbox") || !strings.Contains(string(out), workspaceMount) {
		t.Errorf("exec output = %q, want file content and cwd %s", out, workspaceMount)
	}
	hostProof, err := os.ReadFile(filepath.Join(ws, "proof.txt"))
	if err != nil || strings.TrimSpace(string(hostProof)) != "from-sandbox" {
		t.Errorf("workspace write should reach the host, got %q (%v)", hostProof, err)
	}

	// No host environment or credentials inside.
	out, err = runEngine(ctx, c.ExecArgv("env"))
	if err != nil {
		t.Fatalf("env: %v: %s", err, out)
	}
	if strings.Contains(string(out), "host-secret") {
		t.Error("host environment leaked into the sandbox")
	}

	// Reconcile keeps a live, unexpired container.
	res := Reconcile(ctx, store, time.Now().UTC())
	if len(res.Kept) != 1 || len(res.Dropped)+len(res.Reaped) != 0 {
		t.Errorf("live container should be kept: %+v", res)
	}

	if err := DestroyContainer(ctx, eng.Path, store, c.Record); err != nil {
		t.Fatalf("DestroyContainer: %v", err)
	}
	destroyed = true
	if recs, err := store.List(); err != nil || len(recs) != 0 {
		t.Errorf("record should be gone after destroy: %v, %v", recs, err)
	}
	if _, gone, err := ContainerState(ctx, eng.Path, c.Record.Name); err != nil || !gone {
		t.Errorf("container should be gone after destroy (gone=%v, err=%v)", gone, err)
	}
}

// The claim this package exists to make, put to the kernel rather than to the
// argv builder. Everything else here is a spec that names the right paths and
// a wrap that assembles them in the right order; none of it says the mechanism
// agrees, and a mask that is spelled correctly and does not hold reads exactly
// the same in every one of those tests.
//
// There is one test per mechanism rather than one over Detect because a
// mechanism that is not this platform's is a skip worth reading by name: a
// Linux runner that quietly exercised no Seatbelt looks exactly like one that
// did, and the macOS half of the claim is the half nobody runs by accident.
func TestBubblewrapRefusesAContainedReadOfTheDenyMask(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("bubblewrap is the Linux mechanism and this host is %s", runtime.GOOS)
	}
	avail := detectBwrap()
	if !avail.OK {
		t.Skipf("no bubblewrap containment here: %s", avail.Detail)
	}
	refuseTheMaskedRead(t, avail)
}

func TestSeatbeltRefusesAContainedReadOfTheDenyMask(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skipf("Seatbelt is the macOS mechanism and this host is %s", runtime.GOOS)
	}
	avail := detectSeatbelt()
	if !avail.OK {
		t.Skipf("no Seatbelt containment here: %s", avail.Detail)
	}
	refuseTheMaskedRead(t, avail)
}

// refuseTheMaskedRead runs the read the mask exists to stop — the user's
// private key, from a home of this test's own — and holds both halves of the
// claim: the bytes never arrive, and the read failed rather than coming back
// empty for some other reason. An empty capture satisfies the first half
// whatever went wrong, which is what the second half and the uncontained
// control are for.
//
// The two mechanisms disagree about how a masked path reads — bubblewrap
// mounts an empty tmpfs over it, Seatbelt errors the read — so what is
// asserted is the pair they do agree on rather than either one's wording.
func refuseTheMaskedRead(t *testing.T, avail Availability) {
	t.Helper()
	const key = "PRIVATE-KEY-BYTES"
	home := testHome(t)
	mkdir(t, filepath.Join(home, ".ssh"))
	path := filepath.Join(home, ".ssh", "id_rsa")
	if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, ws := workspacePolicy(t)
	policy.Cwd = ws
	command := "cat " + path

	if out, err := capture(t, shellPath(), "-c", command); err != nil || !strings.Contains(out, key) {
		t.Fatalf("the uncontained control must read the key, or this proves nothing: %v: %s", err, out)
	}

	argv, err := Wrap(avail, policy, command)
	if err != nil {
		t.Fatalf("Wrap under %s: %v", avail.Mechanism, err)
	}
	out, err := capture(t, argv[0], argv[1:]...)
	if strings.Contains(out, key) {
		t.Fatalf("a contained command read the deny mask under %s:\n%s", avail.Mechanism, out)
	}
	if err == nil {
		t.Fatalf("the contained read exited cleanly under %s, so nothing was masked:\n%s", avail.Mechanism, out)
	}
}

// capture runs one argv and hands back everything it printed, wrapped or
// bare. The deadline is the mechanism's rather than the command's: `cat`
// returns at once, and a wrap that hangs on a kernel that will not have it
// would otherwise take the package's whole timeout.
func capture(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}
