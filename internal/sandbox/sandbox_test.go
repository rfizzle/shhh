package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/storage"
)

// testHome isolates every home-derived path (deny mask, caches, config,
// state) under a temp dir so the resolved spec is hermetic.
func testHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("GOMODCACHE", "")
	t.Setenv("GOPATH", "")
	t.Setenv("SHELL", "/bin/sh")
	return home
}

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// resolvedPath is how a spec spells a path: every grant and every mask has
// its symlinks resolved, and a temp directory sits behind one on macOS.
func resolvedPath(t *testing.T, path string) string {
	t.Helper()
	p, err := resolvePath(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func workspacePolicy(t *testing.T) (Policy, string) {
	t.Helper()
	ws := t.TempDir()
	return Policy{Workspace: ws, Profile: ProfileWorkspace}, ws
}

func TestDetectSeatbelt_RequiresASuccessfulProbe(t *testing.T) {
	if _, err := os.Stat(seatbeltPath); err != nil {
		t.Skipf("sandbox-exec is unavailable: %v", err)
	}
	original := seatbeltProbe
	seatbeltProbe = func() ([]byte, error) {
		return []byte("sandbox_apply: Operation not permitted"), errors.New("exit status 71")
	}
	t.Cleanup(func() { seatbeltProbe = original })

	avail := detectSeatbelt()
	if avail.OK || !strings.Contains(avail.Detail, "sandbox_apply") {
		t.Fatalf("a Seatbelt application failure must make containment unavailable: %+v", avail)
	}
}

func TestParseProfile(t *testing.T) {
	for in, want := range map[string]Profile{
		"":                    ProfileWorkspace,
		"workspace":           ProfileWorkspace,
		" Workspace-Netless ": ProfileWorkspaceNetless,
	} {
		got, err := ParseProfile(in)
		if err != nil || got != want {
			t.Errorf("ParseProfile(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseProfile("yolo"); err == nil {
		t.Error("ParseProfile should reject unknown profiles")
	}
}

func TestResolveMasksExistingDenyPaths(t *testing.T) {
	home := testHome(t)
	ssh := mkdir(t, filepath.Join(home, ".ssh"))
	policy, _ := workspacePolicy(t)

	s, err := resolvePolicy(policy, "bwrap")
	if err != nil {
		t.Fatal(err)
	}
	resolvedSSH, _ := filepath.EvalSymlinks(ssh)
	if !slices.Contains(s.denyDirs, resolvedSSH) {
		t.Fatalf("existing ~/.ssh should be masked, denyDirs=%v", s.denyDirs)
	}
	// Nonexistent fixed deny paths (e.g. ~/.aws) are simply absent.
	for _, d := range s.denyDirs {
		if strings.Contains(d, ".aws") {
			t.Fatalf("nonexistent deny path should be skipped, got %s", d)
		}
	}
}

func TestResolveMasksShhhPathsUntilTheScopeGrantsThem(t *testing.T) {
	testHome(t)
	configDir := mkdir(t, filepath.Dir(config.Paths()[0]))
	state, err := storage.Dir()
	if err != nil {
		t.Fatal(err)
	}
	state = mkdir(t, state)
	policy, _ := workspacePolicy(t)

	s, err := resolvePolicy(policy, "bwrap")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{resolvedPath(t, configDir), resolvedPath(t, state)} {
		if !slices.Contains(s.denyDirs, want) {
			t.Errorf("an ungranted shhh directory must be masked, denyDirs=%v; want %s", s.denyDirs, want)
		}
	}

	policy.WriteExtra = []string{configDir, state}
	s, err = resolvePolicy(policy, "bwrap")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{resolvedPath(t, configDir), resolvedPath(t, state)} {
		if slices.Contains(s.denyDirs, path) {
			t.Errorf("a granted shhh directory must not stay masked, denyDirs=%v; path=%s", s.denyDirs, path)
		}
		if !slices.Contains(s.write, path) {
			t.Errorf("a granted shhh directory must be writable, write=%v; path=%s", s.write, path)
		}
	}
}

// The stores nothing legitimate writes to are in the fixed mask, and two of
// them are files rather than directories — a different mount on both
// mechanisms, so the spec has to sort them before the argv can.
func TestResolveMasksTheStoresNothingWritesTo(t *testing.T) {
	home := testHome(t)
	dirs := []string{".gnupg", ".password-store"}
	for _, name := range dirs {
		mkdir(t, filepath.Join(home, name))
	}
	files := []string{".netrc", ".secrets"}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(home, name), []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	policy, _ := workspacePolicy(t)

	s, err := resolvePolicy(policy, "bwrap")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range dirs {
		if !slices.Contains(s.denyDirs, resolvedPath(t, filepath.Join(home, name))) {
			t.Errorf("~/%s should be masked, denyDirs=%v", name, s.denyDirs)
		}
	}
	for _, name := range files {
		if !slices.Contains(s.denyFiles, resolvedPath(t, filepath.Join(home, name))) {
			t.Errorf("~/%s should be masked, denyFiles=%v", name, s.denyFiles)
		}
	}
}

// The other half of the credential story, and the one that is not a fixed
// mask at all: a kubeconfig or a registry login is masked while nothing has
// granted it and readable once the working scope holds it, which is the same
// act that makes it writable. There is no third state and no setting.
func TestResolveMasksACredentialStoreUntilAGrantCoversIt(t *testing.T) {
	home := testHome(t)
	kube := resolvedPath(t, mkdir(t, filepath.Join(home, ".kube")))
	docker := resolvedPath(t, mkdir(t, filepath.Join(home, ".docker")))
	policy, _ := workspacePolicy(t)

	s, err := resolvePolicy(policy, "bwrap")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.denyDirs, kube) || !slices.Contains(s.denyDirs, docker) {
		t.Fatalf("an ungranted credential store should be masked, denyDirs=%v", s.denyDirs)
	}

	policy.WriteExtra = []string{kube}
	if s, err = resolvePolicy(policy, "bwrap"); err != nil {
		t.Fatalf("a granted credential store must not refuse the wrap: %v", err)
	}
	if slices.Contains(s.denyDirs, kube) {
		t.Errorf("a granted credential store must not be masked, denyDirs=%v", s.denyDirs)
	}
	if !slices.Contains(s.write, kube) {
		t.Errorf("a granted credential store is a write grant like any other, write=%v", s.write)
	}
	if !slices.Contains(s.denyDirs, docker) {
		t.Errorf("granting one store must not unmask the next, denyDirs=%v", s.denyDirs)
	}
}

// A grant of something inside a store unmasks the store. Masking the parent
// of a writable path is a configuration resolvePolicy refuses outright, so
// the alternative to unmasking is not a narrower mask — it is a session where
// every command fails to wrap.
func TestResolveUnmasksACredentialStoreGrantedBelowItsRoot(t *testing.T) {
	home := testHome(t)
	kube := resolvedPath(t, mkdir(t, filepath.Join(home, ".kube")))
	inside := mkdir(t, filepath.Join(kube, "cache"))
	policy, _ := workspacePolicy(t)
	policy.WriteExtra = []string{inside}

	s, err := resolvePolicy(policy, "bwrap")
	if err != nil {
		t.Fatalf("resolvePolicy = %v", err)
	}
	if slices.Contains(s.denyDirs, kube) {
		t.Errorf("a store with a granted directory inside it must not be masked, denyDirs=%v", s.denyDirs)
	}
}

func TestResolveFollowsSymlinksBeforeMasking(t *testing.T) {
	home := testHome(t)
	real := mkdir(t, filepath.Join(home, "real-secrets"))
	if err := os.Symlink(real, filepath.Join(home, ".aws")); err != nil {
		t.Fatal(err)
	}
	policy, _ := workspacePolicy(t)

	s, err := resolvePolicy(policy, "bwrap")
	if err != nil {
		t.Fatal(err)
	}
	resolvedReal, _ := filepath.EvalSymlinks(real)
	if !slices.Contains(s.denyDirs, resolvedReal) {
		t.Fatalf("symlinked deny path should resolve to its target %s, denyDirs=%v", resolvedReal, s.denyDirs)
	}
}

func TestResolveRefusesWriteGrantInsideMask(t *testing.T) {
	home := testHome(t)
	secrets := mkdir(t, filepath.Join(home, "secrets"))
	inside := mkdir(t, filepath.Join(secrets, "scratch"))
	policy, _ := workspacePolicy(t)
	policy.DenyExtra = []string{secrets}
	policy.WriteExtra = []string{inside}

	_, err := resolvePolicy(policy, "bwrap")
	if err == nil || !strings.Contains(err.Error(), "wrap unsupported") {
		t.Fatalf("write grant inside a mask must be refused, got %v", err)
	}
}

func TestResolveRefusesWorkspaceInsideMask(t *testing.T) {
	home := testHome(t)
	area := mkdir(t, filepath.Join(home, "area"))
	ws := mkdir(t, filepath.Join(area, "project"))
	policy := Policy{Workspace: ws, DenyExtra: []string{area}}

	_, err := resolvePolicy(policy, "bwrap")
	if err == nil || !strings.Contains(err.Error(), "wrap unsupported") {
		t.Fatalf("workspace inside a mask must be refused, got %v", err)
	}
}

func TestResolveRefusesUnknownProfile(t *testing.T) {
	testHome(t)
	policy, _ := workspacePolicy(t)
	policy.Profile = "vm"
	if _, err := resolvePolicy(policy, "bwrap"); err == nil || !strings.Contains(err.Error(), "wrap unsupported") {
		t.Fatalf("unknown profile must be refused, got %v", err)
	}
}

// hiddenTmp reports whether path is inside one of the temporary directories
// the spec hides. A host whose TMPDIR is itself under /tmp is covered by /tmp
// and not named twice, so this is the question every caller has.
func hiddenTmp(s spec, path string) bool {
	return slices.ContainsFunc(s.tmpHidden, func(h string) bool { return within(path, h) })
}

// The host's temporary directory is the session's own now: not a write grant,
// hidden by the mechanism, and TMPDIR pointing somewhere the rest of the
// machine cannot read. Asserted on the spec because both mechanisms read it
// from there, and on each argv below because they hide it differently.
func TestResolvePrivatisesTheHostTmpdir(t *testing.T) {
	testHome(t)
	policy, _ := workspacePolicy(t)
	hostTmp := resolvedPath(t, os.TempDir())

	s, err := resolvePolicy(policy, "bwrap")
	if err != nil {
		t.Fatal(err)
	}
	if !hiddenTmp(s, hostTmp) {
		t.Fatalf("the host tmpdir should be hidden, tmpHidden=%v", s.tmpHidden)
	}
	if slices.Contains(s.write, hostTmp) {
		t.Fatalf("the host tmpdir must not be a write grant, write=%v", s.write)
	}
	if s.tmpdir == "" || !slices.Contains(s.env, "TMPDIR="+s.tmpdir) {
		t.Fatalf("TMPDIR must point at the private tmpdir %q, env=%v", s.tmpdir, s.env)
	}
	// One TMPDIR, not the host's beside the session's.
	tmpdirs := 0
	for _, pair := range s.env {
		if strings.HasPrefix(pair, "TMPDIR=") {
			tmpdirs++
		}
	}
	if tmpdirs != 1 {
		t.Fatalf("the environment carries %d TMPDIRs: %v", tmpdirs, s.env)
	}
}

// Bubblewrap mounts the tmpfs before the write binds, which is what makes a
// grant of something inside /tmp mean exactly what it says: the granted path
// is bound back over the empty filesystem and nothing else comes with it.
func TestBwrapMountsTheTmpfsBeforeTheGrantsItRebinds(t *testing.T) {
	testHome(t)
	policy, ws := workspacePolicy(t)
	hostTmp := resolvedPath(t, os.TempDir())
	shared := resolvedPath(t, mkdir(t, filepath.Join(t.TempDir(), "lsp-sockets")))
	policy.WriteExtra = []string{shared}

	s, err := resolvePolicy(policy, "bwrap")
	if err != nil {
		t.Fatal(err)
	}
	if !hiddenTmp(s, hostTmp) {
		t.Fatalf("the host tmpdir should be hidden, tmpHidden=%v", s.tmpHidden)
	}
	argv := bwrapArgv(s, "true")
	tmpfs := -1
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "--tmpfs" && slices.Contains(s.tmpHidden, argv[i+1]) {
			tmpfs = i
		}
	}
	if tmpfs < 0 {
		t.Fatalf("the host tmpdir should be a tmpfs: %v", argv)
	}
	for _, granted := range []string{shared, resolvedPath(t, ws)} {
		bind := -1
		for i := 0; i+2 < len(argv); i++ {
			if argv[i] == "--bind" && argv[i+1] == granted {
				bind = i
			}
		}
		if bind < 0 {
			t.Fatalf("a grant inside the host tmpdir must be bound back: %v", argv)
		}
		if bind < tmpfs {
			t.Fatalf("the tmpfs must be mounted before the grant it rebinds: %v", argv)
		}
	}
}

// Seatbelt cannot rearrange the filesystem, so it says the same thing in
// rules: the host's tmpdir denied before the write allowances, the grants
// readable again after it, and the session's own scratch allowed last of all
// because it lives under the state directory the fixed mask has just denied.
func TestSeatbeltDeniesTheHostTmpdirAndAllowsTheSessionsOwn(t *testing.T) {
	home := testHome(t)
	mkdir(t, filepath.Join(home, ".ssh"))
	policy, _ := workspacePolicy(t)
	shared := resolvedPath(t, mkdir(t, filepath.Join(t.TempDir(), "lsp-sockets")))
	policy.WriteExtra = []string{shared}

	s, err := resolvePolicy(policy, "sandbox-exec")
	if err != nil {
		t.Fatal(err)
	}
	state, err := storage.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if s.tmpdir == "" || !within(s.tmpdir, resolvedPath(t, state)) {
		t.Fatalf("the session tmpdir must live under the masked state dir, got %q", s.tmpdir)
	}
	if _, err := os.Stat(s.tmpdir); err != nil {
		t.Fatalf("the session tmpdir must exist for the command to write in it: %v", err)
	}
	profile := seatbeltProfile(s)
	denyTmp := strings.Index(profile, "(deny file-read* file-write*\n  (subpath "+sbplQuote(s.tmpHidden[0]))
	if denyTmp < 0 {
		t.Fatalf("the host tmpdir should be denied:\n%s", profile)
	}
	// The write allowances follow the deny, so a grant is what brings a path
	// back — and it has to bring reads back too, because the deny took those.
	allowWrite := strings.Index(profile, "(allow file-write*\n  (subpath "+sbplQuote(s.workspace))
	allowRead := strings.Index(profile, "(allow file-read*\n")
	if allowWrite < denyTmp || allowRead < denyTmp || !strings.Contains(profile[max(allowRead, 0):], sbplQuote(shared)) {
		t.Fatalf("a grant inside the host tmpdir must outrank the deny:\n%s", profile)
	}
	// Last of all: the state directory is masked, and SBPL reads the later
	// rule as the answer. The mask is found by its own first entry rather
	// than by being the last deny in the profile — a host with an ssh agent
	// puts the socket's deny after it, and that one is a literal about a
	// socket, not the mask this ordering is about.
	allowTmp := strings.Index(profile, "(allow file-read* file-write*\n  (subpath "+sbplQuote(s.tmpdir))
	mask := strings.Index(profile, "(deny file-read* file-write*\n  (subpath "+sbplQuote(s.denyDirs[0]))
	if allowTmp < 0 || mask < 0 || allowTmp < mask {
		t.Fatalf("the session tmpdir must be allowed after the deny mask:\n%s", profile)
	}
	if !strings.Contains(strings.Join(seatbeltPrefix(s), " "), "TMPDIR="+s.tmpdir) {
		t.Fatalf("the command must be told where its tmpdir is: %v", seatbeltPrefix(s))
	}
}

// An allowance inside a mask is only an allowance if the path to it can be
// walked. What the kernel does with this rule is put to it in
// integration_test.go; what is asserted here is the shape it has to have to
// be safe — the ancestors of the allowance, as literals, for the one
// operation an lstat asks for and no other.
func TestSeatbeltProfileLetsAMaskedAncestorAnswerAnLstat(t *testing.T) {
	home := testHome(t)
	ssh := resolvedPath(t, mkdir(t, filepath.Join(home, ".ssh")))
	policy, _ := workspacePolicy(t)

	s, err := resolvePolicy(policy, "sandbox-exec")
	if err != nil {
		t.Fatal(err)
	}
	dirs := traversable(s)
	state := filepath.Dir(filepath.Dir(s.tmpdir))
	for _, want := range []string{state, filepath.Join(state, "tmp")} {
		if !slices.Contains(dirs, want) {
			t.Errorf("the session tmpdir cannot be reached without %s: %v", want, dirs)
		}
	}
	// Only the way in. The allowance already speaks for itself, and a masked
	// directory nothing is allowed inside has no reason to answer anything.
	if slices.Contains(dirs, s.tmpdir) {
		t.Errorf("the allowance itself needs no traversal rule: %v", dirs)
	}
	if slices.Contains(dirs, ssh) {
		t.Errorf("a mask with nothing allowed inside it stays silent: %v", dirs)
	}

	profile := seatbeltProfile(s)
	allow := strings.Index(profile, "(allow file-read-metadata")
	mask := strings.Index(profile, "(deny file-read* file-write*\n  (subpath "+sbplQuote(s.denyDirs[0]))
	if allow < 0 || mask < 0 || allow < mask {
		t.Fatalf("the traversal rule must follow the mask it reaches through:\n%s", profile)
	}
	block := profile[allow:]
	if end := strings.Index(block, "\n("); end >= 0 {
		block = block[:end]
	}
	for _, dir := range dirs {
		if !strings.Contains(block, "(literal "+sbplQuote(dir)+")") {
			t.Errorf("%s must be named as a literal, or the rule opens a subtree:\n%s", dir, block)
		}
	}
	if strings.Contains(block, "subpath") {
		t.Errorf("a subpath here would make every masked file's metadata readable:\n%s", block)
	}
}

// A nested contained command inherits the outer session's scratch directory.
// That directory is already private, and the outer profile grants it without
// granting the state directory siblings a nested process would otherwise make.
func TestSessionTmpDir_ReusesAnInheritedPrivateDirectory(t *testing.T) {
	testHome(t)
	state, err := storage.Dir()
	if err != nil {
		t.Fatal(err)
	}
	inherited := mkdir(t, filepath.Join(state, "tmp", "outer-session"))
	t.Setenv("TMPDIR", inherited)

	got, err := sessionTmpDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != resolvedPath(t, inherited) {
		t.Fatalf("sessionTmpDir() = %q, want inherited private directory %q", got, inherited)
	}
}

// A session's scratch directory outlives the session that made it — the wrap
// is built per command and nothing runs when a session ends — so the next one
// sweeps what is left of a process that has gone. A name that is not a pid is
// not shhh's to delete.
func TestSweepRemovesOnlyTheScratchOfProcessesThatAreGone(t *testing.T) {
	base := t.TempDir()
	alive := mkdir(t, filepath.Join(base, strconv.Itoa(os.Getpid())))
	stranger := mkdir(t, filepath.Join(base, "not-a-pid"))
	// Reserved by POSIX and never a running process, so it is the one pid
	// that can be asserted dead.
	gone := mkdir(t, filepath.Join(base, "999999999"))

	sweepSessionTmpDirs(base)

	for _, keep := range []string{alive, stranger} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("the sweep removed %s: %v", keep, err)
		}
	}
	if _, err := os.Stat(gone); err == nil {
		t.Errorf("the scratch of a process that has gone should be swept: %s", gone)
	}
}

func TestBwrapArgvOrderAndShape(t *testing.T) {
	s := spec{
		workspace: "/work",
		cwd:       "/work",
		shell:     "/bin/sh",
		write:     []string{"/work", "/tmp"},
		denyDirs:  []string{"/home/u/.ssh"},
		denyFiles: []string{"/home/u/.netrc"},
		network:   true,
	}
	command := "echo 'hi; there' && ls"
	argv := bwrapArgv(s, command)

	// The command is the single final element — never parsed or re-quoted.
	if argv[len(argv)-1] != command || argv[len(argv)-2] != "-c" || argv[len(argv)-3] != "/bin/sh" {
		t.Fatalf("command must ride as one argv element after sh -c, got %v", argv[len(argv)-3:])
	}
	if argv[0] != "bwrap" {
		t.Fatalf("argv[0] = %q", argv[0])
	}
	if slices.Contains(argv, "--unshare-net") {
		t.Fatal("workspace profile must preserve network")
	}
	// Deny masks come after every write bind so they outrank grants.
	lastBind := slices.Index(argv, "/tmp")
	tmpfs := slices.Index(argv, "--tmpfs")
	if tmpfs < lastBind {
		t.Fatalf("deny masks must be mounted after write binds: argv=%v", argv)
	}
	if argv[tmpfs+1] != "/home/u/.ssh" {
		t.Fatalf("masked dir should mount as tmpfs, got %v", argv[tmpfs:tmpfs+2])
	}
	nullBind := -1
	for i := 0; i+2 < len(argv); i++ {
		if argv[i] == "--ro-bind" && argv[i+1] == "/dev/null" {
			nullBind = i
		}
	}
	if nullBind < 0 || argv[nullBind+2] != "/home/u/.netrc" {
		t.Fatalf("masked file should bind /dev/null over it, argv=%v", argv)
	}
	if !slices.Contains(argv, "--die-with-parent") {
		t.Fatal("expected --die-with-parent")
	}
	chdir := slices.Index(argv, "--chdir")
	if chdir < 0 || argv[chdir+1] != "/work" {
		t.Fatal("cwd must pass through via --chdir")
	}
}

func TestBwrapArgvNetless(t *testing.T) {
	argv := bwrapArgv(spec{shell: "/bin/sh", network: false}, "true")
	if !slices.Contains(argv, "--unshare-net") {
		t.Fatal("workspace-netless must add --unshare-net")
	}
}

func TestSeatbeltProfileShape(t *testing.T) {
	s := spec{
		shell:     "/bin/sh",
		write:     []string{`/work/it"s here`},
		denyDirs:  []string{"/Users/u/.ssh"},
		denyFiles: []string{"/Users/u/.netrc"},
		network:   false,
	}
	profile := seatbeltProfile(s)

	if !strings.Contains(profile, "(deny file-write*)") {
		t.Fatal("profile must default-deny writes")
	}
	if !strings.Contains(profile, `(allow file-write*`+"\n"+`  (subpath "/dev"))`) {
		t.Fatal("profile must allow file-write to /dev")
	}
	if !strings.Contains(profile, `(subpath "/work/it\"s here")`) {
		t.Fatalf("write grant paths must be SBPL-quoted, got:\n%s", profile)
	}
	// SBPL gives later rules precedence: the deny mask must follow the allow.
	allow := strings.Index(profile, "(allow file-write*")
	deny := strings.Index(profile, "(deny file-read* file-write*")
	if deny < allow {
		t.Fatalf("deny mask must come after write allowances:\n%s", profile)
	}
	if !strings.Contains(profile, `(literal "/Users/u/.netrc")`) {
		t.Fatal("masked files use literal rules")
	}
	if !strings.Contains(profile, "(deny network*)") {
		t.Fatal("netless profile must deny network")
	}

	argv := seatbeltArgv(s, "echo hi")
	if argv[0] != seatbeltPath || argv[len(argv)-1] != "echo hi" {
		t.Fatalf("seatbelt argv shape wrong: %v", argv)
	}
}

func TestSeatbeltProfileNetworkPreserved(t *testing.T) {
	profile := seatbeltProfile(spec{shell: "/bin/sh", network: true})
	if strings.Contains(profile, "deny network") {
		t.Fatal("workspace profile must preserve network")
	}
}

func TestWrapUnavailableRefuses(t *testing.T) {
	testHome(t)
	policy, _ := workspacePolicy(t)
	_, err := Wrap(Availability{Mechanism: "bwrap", OK: false, Detail: "probe failed"}, policy, "true")
	if err == nil || !strings.Contains(err.Error(), "wrap unsupported") {
		t.Fatalf("unavailable mechanism must refuse to wrap, got %v", err)
	}
}

func TestWrapBuildsContainedArgv(t *testing.T) {
	testHome(t)
	policy, ws := workspacePolicy(t)
	argv, err := Wrap(Availability{Mechanism: "bwrap", OK: true}, policy, "go test ./...")
	if err != nil {
		t.Fatal(err)
	}
	if argv[0] != "bwrap" || argv[len(argv)-1] != "go test ./..." {
		t.Fatalf("unexpected argv: %v", argv)
	}
	resolvedWS, _ := filepath.EvalSymlinks(ws)
	if !slices.Contains(argv, resolvedWS) {
		t.Fatalf("workspace should be granted writable, argv=%v", argv)
	}
}

func TestReportUnavailable(t *testing.T) {
	testHome(t)
	policy, _ := workspacePolicy(t)
	r := Report(Availability{Detail: "no containment mechanism for plan9"}, policy, 0)
	if !strings.Contains(r, "unavailable") || !strings.Contains(r, "plan9") {
		t.Fatalf("report should state unavailability honestly:\n%s", r)
	}
	if !strings.Contains(r, "unconfined") {
		t.Fatalf("report should say commands run unconfined:\n%s", r)
	}
}

func TestReportShowsPolicy(t *testing.T) {
	home := testHome(t)
	mkdir(t, filepath.Join(home, ".ssh"))
	policy, ws := workspacePolicy(t)
	policy.Profile = ProfileWorkspaceNetless

	r := Report(Availability{Mechanism: "bwrap", OK: true, Detail: "ok"}, policy, 0)
	resolvedWS, _ := filepath.EvalSymlinks(ws)
	if !strings.Contains(r, resolvedWS) {
		t.Fatalf("report should list the workspace grant:\n%s", r)
	}
	if !strings.Contains(r, ".ssh") {
		t.Fatalf("report should list the deny mask:\n%s", r)
	}
	if !strings.Contains(r, "network disabled") {
		t.Fatalf("report should show the netless profile:\n%s", r)
	}
	// The answer to "what can it reach" used to include the host's shared
	// temporary directory without saying so; now it says the opposite.
	if !strings.Contains(r, "tmpdir:") || !strings.Contains(r, "private to this session") {
		t.Fatalf("report should say the tmpdir is the session's own:\n%s", r)
	}
	if strings.Contains(r, "writable:  "+resolvedPath(t, os.TempDir())+"\n") {
		t.Fatalf("the host tmpdir must not be reported writable:\n%s", r)
	}
}

func TestReportRefusedPolicy(t *testing.T) {
	home := testHome(t)
	secrets := mkdir(t, filepath.Join(home, "secrets"))
	policy, _ := workspacePolicy(t)
	policy.DenyExtra = []string{secrets}
	policy.WriteExtra = []string{mkdir(t, filepath.Join(secrets, "inside"))}

	r := Report(Availability{Mechanism: "bwrap", OK: true, Detail: "ok"}, policy, 0)
	if !strings.Contains(r, "wrap unsupported") || !strings.Contains(r, "never run bare") {
		t.Fatalf("report should surface the refused policy:\n%s", r)
	}
}

// A directory the session put in its working scope reaches the
// mechanism as a write grant — that is the whole point of having added it,
// and it is what stops containment refusing a write the user approved.
func TestScopeDirectoriesBecomeWriteGrants(t *testing.T) {
	policy, _ := workspacePolicy(t)
	added := mkdir(t, filepath.Join(t.TempDir(), "config"))
	policy.WriteExtra = []string{added}
	// The grant is the resolved spelling, and a scratch directory sits behind
	// a symlink on macOS.
	added, err := resolvePath(added)
	if err != nil {
		t.Fatal(err)
	}

	s, err := resolvePolicy(policy, "bwrap")
	if err != nil {
		t.Fatalf("resolvePolicy = %v", err)
	}
	if !slices.Contains(s.write, added) {
		t.Fatalf("write grants = %v, want the added directory %s", s.write, added)
	}

	argv := bwrapArgv(s, "true")
	bind := slices.Index(argv, added)
	if bind < 1 || argv[bind-1] != "--bind" || argv[bind+1] != added {
		t.Fatalf("bwrap should bind the added directory writable, got %v", argv)
	}
	if profile := seatbeltProfile(s); !strings.Contains(profile, `(subpath "`+added+`")`) {
		t.Fatalf("seatbelt should allow writes under the added directory, got:\n%s", profile)
	}
	// The workspace grant is the resolved spelling too, which is what the
	// spec already holds; the raw scratch path is behind the same symlink.
	if !slices.Contains(s.write, s.workspace) {
		t.Fatal("the workspace grant must survive alongside the added directory")
	}
}

func TestSeatbeltUsesTheDirectAppleGitResolvedBeforeContainment(t *testing.T) {
	testHome(t)
	direct := filepath.Join(t.TempDir(), "Developer", "usr", "bin", "git")
	old := findAppleGit
	findAppleGit = func() (string, error) { return direct, nil }
	t.Cleanup(func() { findAppleGit = old })
	policy, _ := workspacePolicy(t)
	policy.Env = []string{"PATH=/usr/local/bin:/usr/bin"}

	s, err := resolvePolicy(policy, "sandbox-exec")
	if err != nil {
		t.Fatal(err)
	}
	if s.appleGit != direct {
		t.Fatalf("direct Apple git = %q, want %q", s.appleGit, direct)
	}
	if !slices.Contains(s.env, "PATH="+filepath.Dir(direct)+string(filepath.ListSeparator)+"/usr/local/bin:/usr/bin") {
		t.Fatalf("the direct Git directory must lead PATH, env=%v", s.env)
	}
	argv, err := WrapArgv(Availability{Mechanism: "sandbox-exec", OK: true}, policy, []string{"/usr/bin/git", "status"})
	if err != nil {
		t.Fatal(err)
	}
	if got := argv[len(argv)-2:]; !slices.Equal(got, []string{direct, "status"}) {
		t.Fatalf("the Apple shim must be replaced after containment, got %v", got)
	}
}

// The environment a start passes has to be in the policy or it is not in the
// command: the mechanism clears whatever the spawn set. This is the check
// that a variable named in a `process start` survives the rebuild, that it
// outranks an inherited variable of the same name — the value the caller
// asked for is the value it meant — and that a name the person declared as a
// session secret keeps the session's value instead, which is the precedence
// the uncontained spawn has.
func TestWithEnvWidensTheAllowlistByNameOnly(t *testing.T) {
	policy, _ := workspacePolicy(t)
	policy.Env = []string{"PATH=/usr/bin:/bin", "PORT=3000", "DEPLOY_KEY=session"}
	policy.SecretNames = []string{"DEPLOY_KEY"}

	widened := policy.WithEnv([]string{"PORT=3001", "APP_MODE=debug", "DEPLOY_KEY=stolen"})
	env := containedEnv(widened.Env, widened.SecretNames)

	for _, want := range []string{"PORT=3001", "APP_MODE=debug", "DEPLOY_KEY=session"} {
		if !slices.Contains(env, want) {
			t.Errorf("contained environment = %v, want %s", env, want)
		}
	}
	if slices.Contains(env, "PORT=3000") {
		t.Errorf("the start's own value must win over the inherited one: %v", env)
	}
	if slices.Contains(env, "DEPLOY_KEY=stolen") {
		t.Errorf("a start must not shadow a declared secret: %v", env)
	}
	// A variable nobody named is still not on the list — widening is by
	// name, and there is no shape that gets a name in.
	if got := containedEnv([]string{"AWS_SESSION_TOKEN=borrowed"}, widened.SecretNames); len(got) != 0 {
		t.Errorf("an undeclared variable must not cross: %v", got)
	}
	// The original policy is untouched: it is rebuilt per command and handed
	// out by value, so one start's extras leaking into the next command's
	// environment is the failure this copy prevents.
	if len(policy.Env) != 3 || len(policy.SecretNames) != 1 {
		t.Errorf("WithEnv must not mutate the policy it was given: %v %v", policy.Env, policy.SecretNames)
	}
}

// A policy with no environment of its own draws from this process's, and
// widening it must not turn that into "these pairs and nothing else" — a
// contained command with no PATH fails as "not found", which points nowhere
// near the environment.
func TestWithEnvKeepsTheInheritedEnvironmentWhenPolicyHasNone(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	policy, _ := workspacePolicy(t)

	widened := policy.WithEnv([]string{"PORT=3001"})
	env := containedEnv(widened.Env, widened.SecretNames)
	for _, want := range []string{"PATH=/usr/bin:/bin", "PORT=3001"} {
		if !slices.Contains(env, want) {
			t.Errorf("contained environment = %v, want %s", env, want)
		}
	}
}
