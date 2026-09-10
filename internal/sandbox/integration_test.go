//go:build integration

package sandbox

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/storage"
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

// The same claim about a masked file rather than a masked directory, which is
// a different mount and a different failure: bubblewrap binds /dev/null over
// the path, so the read succeeds and returns nothing at all. `cat ~/.netrc`
// exiting 0 is the mask working, so the exit status the directory case leans
// on says nothing here — what is asserted instead is that the shell ran (the
// sentinel arrives) and the password did not.
//
// .netrc is the one worth putting to the kernel: curl and git both read it
// without being asked, so a contained command that only fetches a URL is a
// contained command that has already opened it.
func refuseTheMaskedFileRead(t *testing.T, avail Availability) {
	t.Helper()
	const password = "NETRC-PASSWORD-BYTES"
	home := testHome(t)
	path := filepath.Join(home, ".netrc")
	if err := os.WriteFile(path, []byte("machine example.com login u password "+password), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, ws := workspacePolicy(t)
	policy.Cwd = ws
	command := "echo SHELL-RAN; cat " + path

	if out, err := capture(t, shellPath(), "-c", command); err != nil || !strings.Contains(out, password) {
		t.Fatalf("the uncontained control must read the password, or this proves nothing: %v: %s", err, out)
	}

	argv, err := Wrap(avail, policy, command)
	if err != nil {
		t.Fatalf("Wrap under %s: %v", avail.Mechanism, err)
	}
	out, _ := capture(t, argv[0], argv[1:]...)
	if !strings.Contains(out, "SHELL-RAN") {
		t.Fatalf("the contained shell never ran under %s, so the empty read proves nothing:\n%s", avail.Mechanism, out)
	}
	if strings.Contains(out, password) {
		t.Fatalf("a contained command read ~/.netrc under %s:\n%s", avail.Mechanism, out)
	}
}

// The credential stores that are not in the fixed mask, put to the kernel
// both ways round: masked while nothing has granted the directory, and
// readable once the working scope holds it. The second half is the one worth
// executing — a mask assembled from the write grants is a mask that can be
// spelled correctly and still hide a directory the person asked to work in,
// and every command in that session fails on a path they granted.
func refuseTheUngrantedCredentialStore(t *testing.T, avail Availability) {
	t.Helper()
	const token = "KUBECONFIG-TOKEN-BYTES"
	home := testHome(t)
	kube := mkdir(t, filepath.Join(home, ".kube"))
	path := filepath.Join(kube, "config")
	if err := os.WriteFile(path, []byte("token: "+token), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, ws := workspacePolicy(t)
	policy.Cwd = ws
	command := "echo SHELL-RAN; cat " + path

	if out, err := capture(t, shellPath(), "-c", command); err != nil || !strings.Contains(out, token) {
		t.Fatalf("the uncontained control must read the kubeconfig, or this proves nothing: %v: %s", err, out)
	}

	argv, err := Wrap(avail, policy, command)
	if err != nil {
		t.Fatalf("Wrap under %s: %v", avail.Mechanism, err)
	}
	out, _ := capture(t, argv[0], argv[1:]...)
	if !strings.Contains(out, "SHELL-RAN") {
		t.Fatalf("the contained shell never ran under %s, so the empty read proves nothing:\n%s", avail.Mechanism, out)
	}
	if strings.Contains(out, token) {
		t.Fatalf("a contained command read an ungranted ~/.kube under %s:\n%s", avail.Mechanism, out)
	}

	policy.WriteExtra = []string{kube}
	argv, err = Wrap(avail, policy, command)
	if err != nil {
		t.Fatalf("Wrap with the store granted under %s: %v", avail.Mechanism, err)
	}
	if out, err := capture(t, argv[0], argv[1:]...); err != nil || !strings.Contains(out, token) {
		t.Fatalf("a granted ~/.kube must be readable under %s: %v:\n%s", avail.Mechanism, err, out)
	}
}

func TestBubblewrapRefusesAContainedReadOfAMaskedFile(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("bubblewrap is the Linux mechanism and this host is %s", runtime.GOOS)
	}
	avail := detectBwrap()
	if !avail.OK {
		t.Skipf("no bubblewrap containment here: %s", avail.Detail)
	}
	refuseTheMaskedFileRead(t, avail)
}

func TestSeatbeltRefusesAContainedReadOfAMaskedFile(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skipf("Seatbelt is the macOS mechanism and this host is %s", runtime.GOOS)
	}
	avail := detectSeatbelt()
	if !avail.OK {
		t.Skipf("no Seatbelt containment here: %s", avail.Detail)
	}
	refuseTheMaskedFileRead(t, avail)
}

func TestBubblewrapReadsACredentialStoreOnlyWhenItIsGranted(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("bubblewrap is the Linux mechanism and this host is %s", runtime.GOOS)
	}
	avail := detectBwrap()
	if !avail.OK {
		t.Skipf("no bubblewrap containment here: %s", avail.Detail)
	}
	refuseTheUngrantedCredentialStore(t, avail)
}

func TestSeatbeltReadsACredentialStoreOnlyWhenItIsGranted(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skipf("Seatbelt is the macOS mechanism and this host is %s", runtime.GOOS)
	}
	avail := detectSeatbelt()
	if !avail.OK {
		t.Skipf("no Seatbelt containment here: %s", avail.Detail)
	}
	refuseTheUngrantedCredentialStore(t, avail)
}

func TestBubblewrapGivesAContainedCommandItsOwnTmpdir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("bubblewrap is the Linux mechanism and this host is %s", runtime.GOOS)
	}
	avail := detectBwrap()
	if !avail.OK {
		t.Skipf("no bubblewrap containment here: %s", avail.Detail)
	}
	refuseTheHostTmpdir(t, avail)
}

func TestSeatbeltGivesAContainedCommandItsOwnTmpdir(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skipf("Seatbelt is the macOS mechanism and this host is %s", runtime.GOOS)
	}
	avail := detectSeatbelt()
	if !avail.OK {
		t.Skipf("no Seatbelt containment here: %s", avail.Detail)
	}
	refuseTheHostTmpdir(t, avail)
}

// Apple's /usr/bin/git is an xcrun shim. The resolver cache lives in the
// host's per-user temporary directory, which containment cannot expose
// without reopening the shared scratch channel. The direct developer-toolchain
// binary is resolved before Seatbelt starts and must still run after it does.
func TestSeatbeltRunsAppleGitWithoutTheXcrunShim(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skipf("Seatbelt is the macOS mechanism and this host is %s", runtime.GOOS)
	}
	avail := detectSeatbelt()
	if !avail.OK {
		t.Skipf("no Seatbelt containment here: %s", avail.Detail)
	}
	testHome(t)
	policy, ws := workspacePolicy(t)
	policy.Cwd = ws

	argv, err := WrapArgv(avail, policy, []string{"/usr/bin/git", "--version"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := capture(t, argv[0], argv[1:]...)
	if err != nil || !strings.Contains(out, "git version") {
		t.Fatalf("the resolved Apple Git must run under Seatbelt: %v:\n%s", err, out)
	}
	if strings.Contains(strings.ToLower(out), "xcrun") {
		t.Fatalf("the contained command must not run the xcrun shim:\n%s", out)
	}
}

// The hole in an otherwise closed write boundary, put to the kernel: the
// host's temporary directory was a writable bind on both mechanisms, so a
// contained command could read what an uncontained one had left there and
// leave what an uncontained one would read.
//
// Both directions are asserted, because closing one of them is not closing
// the channel. The read is the file this test wrote to the host's tmpdir
// before the command, with the uncontained control that shows it was there to
// be read. The write is a file the contained command puts in its own TMPDIR,
// looked for afterwards where an uncontained process would look — which is
// the claim the two mechanisms agree on: bubblewrap's tmpfs keeps it out of
// the filesystem entirely and Seatbelt's session directory keeps it out of
// the shared one.
func refuseTheHostTmpdir(t *testing.T, avail Availability) {
	t.Helper()
	const left = "HOST-TMP-BYTES"
	const leaf = "shhh-contained-scratch"
	testHome(t)
	// Directly under the host's tmpdir rather than in a scratch tree of the
	// test's own: what is being asserted is the shared directory, and
	// t.TempDir() is a workspace the grant makes reachable on purpose.
	host, err := os.CreateTemp("", "shhh-host-tmp-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.WriteString(left); err != nil {
		t.Fatal(err)
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(host.Name()) })
	landed := filepath.Join(os.TempDir(), leaf)
	t.Cleanup(func() { _ = os.Remove(landed) })

	policy, ws := workspacePolicy(t)
	policy.Cwd = ws
	command := "echo SHELL-RAN; cat " + host.Name() + "; echo scratch > \"$TMPDIR/" + leaf + "\" && echo WROTE"

	if out, err := capture(t, shellPath(), "-c", "cat "+host.Name()); err != nil || !strings.Contains(out, left) {
		t.Fatalf("the uncontained control must read the host tmpdir, or this proves nothing: %v: %s", err, out)
	}

	argv, err := Wrap(avail, policy, command)
	if err != nil {
		t.Fatalf("Wrap under %s: %v", avail.Mechanism, err)
	}
	out, _ := capture(t, argv[0], argv[1:]...)
	if !strings.Contains(out, "SHELL-RAN") {
		t.Fatalf("the contained shell never ran under %s, so the empty read proves nothing:\n%s", avail.Mechanism, out)
	}
	if strings.Contains(out, left) {
		t.Errorf("a contained command read the host tmpdir under %s:\n%s", avail.Mechanism, out)
	}
	// A private tmpdir nothing may write to is a build that fails, so the
	// other half of the claim is that the scratch space is real.
	if !strings.Contains(out, "WROTE") {
		t.Errorf("a contained command must be able to write to its own TMPDIR under %s:\n%s", avail.Mechanism, out)
	}
	if _, err := os.Stat(landed); err == nil {
		t.Errorf("a contained command's scratch reached the host tmpdir under %s: %s", avail.Mechanism, landed)
	}

	// A workspace that lives in the host's tmpdir is still where the work is,
	// and the quality gate withholds its write grant: hiding the temporary
	// directory must not take the checkout with it, or a read-only check runs
	// in a directory that is no longer there.
	if err := os.WriteFile(filepath.Join(ws, "tracked.txt"), []byte("IN-THE-WORKSPACE"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy.ReadOnlyWorkspace = true
	argv, err = Wrap(avail, policy, "cat "+filepath.Join(ws, "tracked.txt"))
	if err != nil {
		t.Fatalf("Wrap read-only under %s: %v", avail.Mechanism, err)
	}
	if out, err := capture(t, argv[0], argv[1:]...); err != nil || !strings.Contains(out, "IN-THE-WORKSPACE") {
		t.Errorf("a read-only workspace inside the host tmpdir must still be readable under %s: %v:\n%s", avail.Mechanism, err, out)
	}
}

// storeProbeEnv tells a test binary it is the contained half of the SQLite
// claim below rather than the driver of it. It rides in as a declared session
// secret, which is the only way a name the allowlist has never heard of
// reaches a contained command.
const storeProbeEnv = "SHHH_IT_STORE_PROBE"

// storeProbeOK is what the contained half prints when it got a store open. A
// sentinel rather than the exit status, because a wrap that never started the
// binary also exits non-zero and would otherwise read as the same failure.
const storeProbeOK = "STORE-OPENED"

// The claim that a contained command can still use the scratch space it was
// given, put to the kernel: a SQLite store opened inside the session's own
// temporary directory, by a Go program, with no TMPDIR of the test's own.
//
// It is here rather than in the profile tests because the profile that fails
// this is spelled correctly. Every rule reads right, a plain `os.WriteFile`
// into the same directory succeeds, and SQLite still cannot open a database
// beside it — the deny took `lstat` of the ancestors with it and SQLite walks
// them (traversable, seatbelt.go). Nothing but a kernel says so, and the cost
// of nobody asking one was a session that spent eight rounds reading
// `unable to open database file (14)` as a broken test.
//
// The contained command is this test binary again. `go test` builds it under
// the host's temporary directory, which is the directory containment hides,
// so it is copied into the workspace first and run from there.
func TestSeatbeltOpensASQLiteStoreInItsOwnTmpdir(t *testing.T) {
	if os.Getenv(storeProbeEnv) != "" {
		openAStoreInTheContainedTmpdir(t)
		return
	}
	if runtime.GOOS != "darwin" {
		t.Skipf("Seatbelt is the macOS mechanism and this host is %s", runtime.GOOS)
	}
	avail := detectSeatbelt()
	if !avail.OK {
		t.Skipf("no Seatbelt containment here: %s", avail.Detail)
	}

	testHome(t)
	policy, ws := workspacePolicy(t)
	policy.Cwd = ws
	policy.Env = append(os.Environ(), storeProbeEnv+"=1")
	policy.SecretNames = []string{storeProbeEnv}

	s, err := resolvePolicy(policy, avail.Mechanism)
	if err != nil {
		t.Fatalf("resolvePolicy: %v", err)
	}
	if s.tmpdir == "" {
		t.Fatal("the session tmpdir is what this test is about; the spec has none")
	}
	argv, err := WrapArgv(avail, policy, []string{
		copyTestBinary(t, ws), "-test.run", "^" + t.Name() + "$", "-test.v",
	})
	if err != nil {
		t.Fatalf("WrapArgv under %s: %v", avail.Mechanism, err)
	}
	out, err := capture(t, argv[0], argv[1:]...)
	if err != nil || !strings.Contains(out, storeProbeOK) {
		t.Fatalf("a contained command must be able to open a store in its own tmpdir under %s: %v:\n%s", avail.Mechanism, err, out)
	}
	// The other half: it was the containment's temporary directory and not
	// one the test pointed somewhere friendlier, which is the workaround this
	// failure was lived with under.
	if !strings.Contains(out, storeProbeOK+" "+s.tmpdir) {
		t.Errorf("the store should have been opened inside %s, got:\n%s", s.tmpdir, out)
	}
	// And the mask the traversal rule reaches through still holds. Letting a
	// masked directory answer `lstat` is a hole if it also lets the command
	// see what is in it, and the ungranted state directory is where the
	// session's own database lives — so the kernel is asked, not the profile text.
	refuseTheMaskedStateDirectory(t, avail, filepath.Dir(filepath.Dir(s.tmpdir)))
}

// refuseTheMaskedStateDirectory holds the mask over shhh's own state now that
// the profile names two of its directories in an allowance. `lstat` says a
// directory is there; nothing here may say what is in it.
func refuseTheMaskedStateDirectory(t *testing.T, avail Availability, state string) {
	t.Helper()
	const secret = "STATE-DIR-BYTES"
	path := filepath.Join(state, "shhh.db")
	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	policy, ws := workspacePolicy(t)
	policy.Cwd = ws
	// The sentinels rather than the paths: a denial names the path it refused
	// in its own error message, so looking for the file's name in the output
	// finds the refusal as readily as the listing.
	command := "echo SHELL-RAN; ls " + state + " >/dev/null 2>&1 && echo LISTED; cat " + path

	if out, err := capture(t, shellPath(), "-c", command); err != nil || !strings.Contains(out, secret) || !strings.Contains(out, "LISTED") {
		t.Fatalf("the uncontained control must read the state directory, or this proves nothing: %v: %s", err, out)
	}

	argv, err := Wrap(avail, policy, command)
	if err != nil {
		t.Fatalf("Wrap under %s: %v", avail.Mechanism, err)
	}
	out, _ := capture(t, argv[0], argv[1:]...)
	if !strings.Contains(out, "SHELL-RAN") {
		t.Fatalf("the contained shell never ran under %s, so the empty read proves nothing:\n%s", avail.Mechanism, out)
	}
	if strings.Contains(out, secret) {
		t.Errorf("a contained command read shhh's own state under %s:\n%s", avail.Mechanism, out)
	}
	if strings.Contains(out, "LISTED") {
		t.Errorf("a contained command listed shhh's own state under %s:\n%s", avail.Mechanism, out)
	}
}

// openAStoreInTheContainedTmpdir is the half that runs inside containment. It
// writes a plain file first, because a directory that cannot take one at all
// makes the store's failure say nothing about SQLite.
func openAStoreInTheContainedTmpdir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plain.txt"), []byte("PLAIN"), 0o600); err != nil {
		t.Fatalf("a plain write into the contained tmpdir failed, so the store proves nothing: %v", err)
	}
	db, err := storage.OpenPath(filepath.Join(dir, "probe.db"))
	if err != nil {
		t.Fatalf("open a store at %s: %v", dir, err)
	}
	defer func() { _ = db.Close() }()
	fmt.Println(storeProbeOK, dir)
}

// copyTestBinary puts this test binary somewhere the contained command can
// read it and hands back the new path.
func copyTestBinary(t *testing.T, dir string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("this test has to re-run itself and cannot find itself: %v", err)
	}
	body, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("read %s: %v", self, err)
	}
	path := filepath.Join(dir, "contained.test")
	if err := os.WriteFile(path, body, 0o700); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
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

// The other claim put to the kernel rather than to the argv builder: a
// contained command's environment is the allowlist and nothing else. A leaked
// SSH_AUTH_SOCK is a signing oracle — the mask can take the private key off
// the filesystem and the agent will still sign with it — so what is asserted
// is that the address is not there, next to the control that shows it would
// have been.
//
// One test per mechanism for the same reason the deny-mask pair is: a skip
// worth reading by name beats a platform that quietly exercised nothing.
func TestBubblewrapGivesAContainedCommandOnlyTheAllowedEnvironment(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("bubblewrap is the Linux mechanism and this host is %s", runtime.GOOS)
	}
	avail := detectBwrap()
	if !avail.OK {
		t.Skipf("no bubblewrap containment here: %s", avail.Detail)
	}
	refuseTheInheritedEnvironment(t, avail)
}

func TestSeatbeltGivesAContainedCommandOnlyTheAllowedEnvironment(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skipf("Seatbelt is the macOS mechanism and this host is %s", runtime.GOOS)
	}
	avail := detectSeatbelt()
	if !avail.OK {
		t.Skipf("no Seatbelt containment here: %s", avail.Detail)
	}
	refuseTheInheritedEnvironment(t, avail)
}

func refuseTheInheritedEnvironment(t *testing.T, avail Availability) {
	t.Helper()
	testHome(t)
	// A real socket, so the mask over it has somewhere to land and the run
	// proves both halves: the address is gone and the wrap still starts.
	sock := writeSocketPath(t)
	t.Setenv("SSH_AUTH_SOCK", sock)
	t.Setenv("SHHH_IT_UNNAMED", "inherited")
	policy, ws := workspacePolicy(t)
	policy.Cwd = ws
	policy.Env = append(os.Environ(), "SHHH_IT_DECLARED=named")
	policy.SecretNames = []string{"SHHH_IT_DECLARED"}
	const command = "echo sock=[$SSH_AUTH_SOCK] unnamed=[$SHHH_IT_UNNAMED] declared=[$SHHH_IT_DECLARED] path=[$PATH]"

	if out, err := capture(t, shellPath(), "-c", command); err != nil || !strings.Contains(out, "sock=["+sock+"]") {
		t.Fatalf("the uncontained control must carry the agent address, or this proves nothing: %v: %s", err, out)
	}

	argv, err := Wrap(avail, policy, command)
	if err != nil {
		t.Fatalf("Wrap under %s: %v", avail.Mechanism, err)
	}
	out, err := capture(t, argv[0], argv[1:]...)
	if err != nil {
		t.Fatalf("the contained echo should run under %s: %v: %s", avail.Mechanism, err, out)
	}
	if !strings.Contains(out, "sock=[]") {
		t.Errorf("the agent socket crossed into containment under %s:\n%s", avail.Mechanism, out)
	}
	if !strings.Contains(out, "unnamed=[]") {
		t.Errorf("a variable nobody named crossed into containment under %s:\n%s", avail.Mechanism, out)
	}
	if !strings.Contains(out, "declared=[named]") {
		t.Errorf("a declared secret must still reach the command under %s:\n%s", avail.Mechanism, out)
	}
	if strings.Contains(out, "path=[]") {
		t.Errorf("a command with no PATH cannot find a program under %s:\n%s", avail.Mechanism, out)
	}
}

// The two claims the approval card makes in the person's own words — "a
// contained command cannot write outside the workspace" and "netless has no
// network" — put to the kernel rather than to the argv builder. Every other
// test of them reads the spec back: that `--bind` names the workspace and
// `--unshare-net` is in the argv. A bind that lands in the wrong order, a
// grant that resolves to `/`, and a profile whose network clause is spelled
// for the wrong SBPL version all produce an argv those assertions pass.
//
// One test per mechanism, for the reason the deny-mask pair gives: a skip
// worth reading by name beats a platform that quietly exercised nothing.
func TestBubblewrapRefusesAWriteOutsideTheWorkspace(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("bubblewrap is the Linux mechanism and this host is %s", runtime.GOOS)
	}
	avail := detectBwrap()
	if !avail.OK {
		t.Skipf("no bubblewrap containment here: %s", avail.Detail)
	}
	refuseTheWriteOutsideTheWorkspace(t, avail)
}

func TestSeatbeltRefusesAWriteOutsideTheWorkspace(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skipf("Seatbelt is the macOS mechanism and this host is %s", runtime.GOOS)
	}
	avail := detectSeatbelt()
	if !avail.OK {
		t.Skipf("no Seatbelt containment here: %s", avail.Detail)
	}
	refuseTheWriteOutsideTheWorkspace(t, avail)
}

func TestBubblewrapGivesNetlessNoNetwork(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("bubblewrap is the Linux mechanism and this host is %s", runtime.GOOS)
	}
	avail := detectBwrap()
	if !avail.OK {
		t.Skipf("no bubblewrap containment here: %s", avail.Detail)
	}
	refuseTheNetlessConnection(t, avail)
}

func TestSeatbeltGivesNetlessNoNetwork(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skipf("Seatbelt is the macOS mechanism and this host is %s", runtime.GOOS)
	}
	avail := detectSeatbelt()
	if !avail.OK {
		t.Skipf("no Seatbelt containment here: %s", avail.Detail)
	}
	refuseTheNetlessConnection(t, avail)
}

// refuseTheWriteOutsideTheWorkspace holds both halves of the write boundary
// at once: the file inside the workspace lands on the host, and the one
// outside every grant does not. The negative half alone would pass on a wrap
// that failed for any reason at all, which is what the sentinel from the
// inside write is for.
//
// The host is asked whether the outside file exists rather than the command
// being asked whether it succeeded, because those are different questions:
// bubblewrap's read-only root fails the write, but a mechanism that let the
// write land somewhere the host cannot see would report success to the
// command and still be a mask that holds. The control run creates the file
// and this test removes it again, so the absence at the end is this run's.
func refuseTheWriteOutsideTheWorkspace(t *testing.T, avail Availability) {
	t.Helper()
	home := testHome(t)
	// TMPDIR is a write grant and every t.TempDir sits under the host's, so
	// a path meant to be outside every grant has to be built before this
	// run's scratch space is pointed somewhere of its own. Without this the
	// "outside" file is inside the temp grant and the test proves nothing.
	outside := mkdir(t, filepath.Join(home, "outside"))
	t.Setenv("TMPDIR", mkdir(t, filepath.Join(home, "scratch")))
	policy, ws := workspacePolicy(t)
	policy.Cwd = ws
	inside := filepath.Join(ws, "inside.txt")
	beyond := filepath.Join(outside, "beyond.txt")
	command := "touch " + inside + " && echo WROTE-INSIDE; touch " + beyond + " && echo WROTE-OUTSIDE"

	if out, err := capture(t, shellPath(), "-c", command); err != nil || !strings.Contains(out, "WROTE-OUTSIDE") {
		t.Fatalf("the uncontained control must write outside the workspace, or this proves nothing: %v: %s", err, out)
	}
	if err := os.Remove(beyond); err != nil {
		t.Fatal(err)
	}

	argv, err := Wrap(avail, policy, command)
	if err != nil {
		t.Fatalf("Wrap under %s: %v", avail.Mechanism, err)
	}
	out, _ := capture(t, argv[0], argv[1:]...)
	if !strings.Contains(out, "WROTE-INSIDE") {
		t.Fatalf("a contained command must still write its own workspace under %s:\n%s", avail.Mechanism, out)
	}
	if strings.Contains(out, "WROTE-OUTSIDE") {
		t.Errorf("a contained command wrote outside every grant under %s:\n%s", avail.Mechanism, out)
	}
	if _, err := os.Stat(beyond); err == nil {
		t.Errorf("a contained write reached %s on the host under %s", beyond, avail.Mechanism)
	}
	if _, err := os.Stat(inside); err != nil {
		t.Errorf("the contained workspace write should reach the host under %s: %v", avail.Mechanism, err)
	}
}

// refuseTheNetlessConnection puts the netless profile to a listener this test
// opened, which is the one destination that cannot be down, rate-limited or
// behind somebody's proxy: a run that fails to reach example.com says nothing
// about the profile.
//
// It runs the same command under both profiles rather than only the netless
// one, so what is asserted is the profile and not containment in general —
// a wrap that could reach nothing at all would pass the netless half on its
// own.
func refuseTheNetlessConnection(t *testing.T, avail Availability) {
	t.Helper()
	if filepath.Base(shellPath()) != "bash" {
		// /dev/tcp is bash's, and the wrap runs the execution shell rather
		// than one this test picks, so there is no honest way to open a
		// socket from inside without it.
		t.Skipf("the execution shell here is %s, which has no /dev/tcp", shellPath())
	}
	testHome(t)
	addr := listenLocally(t)
	policy, ws := workspacePolicy(t)
	policy.Cwd = ws
	command := "exec 3<>/dev/tcp/" + strings.Replace(addr, ":", "/", 1) + " && echo CONNECTED"

	if out, err := capture(t, shellPath(), "-c", command); err != nil || !strings.Contains(out, "CONNECTED") {
		t.Fatalf("the uncontained control must reach the listener, or this proves nothing: %v: %s", err, out)
	}

	argv, err := Wrap(avail, policy, command)
	if err != nil {
		t.Fatalf("Wrap under %s: %v", avail.Mechanism, err)
	}
	if out, err := capture(t, argv[0], argv[1:]...); err != nil || !strings.Contains(out, "CONNECTED") {
		t.Fatalf("the workspace profile keeps the network under %s: %v:\n%s", avail.Mechanism, err, out)
	}

	policy.Profile = ProfileWorkspaceNetless
	argv, err = Wrap(avail, policy, command)
	if err != nil {
		t.Fatalf("Wrap netless under %s: %v", avail.Mechanism, err)
	}
	out, err := capture(t, argv[0], argv[1:]...)
	if strings.Contains(out, "CONNECTED") {
		t.Fatalf("a netless command reached a loopback listener under %s:\n%s", avail.Mechanism, out)
	}
	if err == nil {
		t.Fatalf("the netless connect exited cleanly under %s, so nothing was blocked:\n%s", avail.Mechanism, out)
	}
}

// listenLocally opens a loopback listener that accepts and closes for the
// life of the test, and hands back its host:port. It accepts in the
// background because the contained half is expected never to arrive.
func listenLocally(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no loopback listener here: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return ln.Addr().String()
}
