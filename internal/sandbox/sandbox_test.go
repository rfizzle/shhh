package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
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

func workspacePolicy(t *testing.T) (Policy, string) {
	t.Helper()
	ws := t.TempDir()
	return Policy{Workspace: ws, Profile: ProfileWorkspace}, ws
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

	s, err := resolvePolicy(policy)
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

func TestResolveFollowsSymlinksBeforeMasking(t *testing.T) {
	home := testHome(t)
	real := mkdir(t, filepath.Join(home, "real-secrets"))
	if err := os.Symlink(real, filepath.Join(home, ".aws")); err != nil {
		t.Fatal(err)
	}
	policy, _ := workspacePolicy(t)

	s, err := resolvePolicy(policy)
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

	_, err := resolvePolicy(policy)
	if err == nil || !strings.Contains(err.Error(), "wrap unsupported") {
		t.Fatalf("write grant inside a mask must be refused, got %v", err)
	}
}

func TestResolveRefusesWorkspaceInsideMask(t *testing.T) {
	home := testHome(t)
	area := mkdir(t, filepath.Join(home, "area"))
	ws := mkdir(t, filepath.Join(area, "project"))
	policy := Policy{Workspace: ws, DenyExtra: []string{area}}

	_, err := resolvePolicy(policy)
	if err == nil || !strings.Contains(err.Error(), "wrap unsupported") {
		t.Fatalf("workspace inside a mask must be refused, got %v", err)
	}
}

func TestResolveRefusesUnmaskableDenyType(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no fifos on windows")
	}
	home := testHome(t)
	fifo := filepath.Join(home, "weird")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("cannot create fifo: %v", err)
	}
	policy, _ := workspacePolicy(t)
	policy.DenyExtra = []string{fifo}

	_, err := resolvePolicy(policy)
	if err == nil || !strings.Contains(err.Error(), "wrap unsupported") {
		t.Fatalf("unmaskable deny path must be refused, got %v", err)
	}
}

func TestResolveRefusesUnknownProfile(t *testing.T) {
	testHome(t)
	policy, _ := workspacePolicy(t)
	policy.Profile = "vm"
	if _, err := resolvePolicy(policy); err == nil || !strings.Contains(err.Error(), "wrap unsupported") {
		t.Fatalf("unknown profile must be refused, got %v", err)
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
	r := Report(Availability{Detail: "no containment mechanism for plan9"}, policy)
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

	r := Report(Availability{Mechanism: "bwrap", OK: true, Detail: "ok"}, policy)
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
}

func TestReportRefusedPolicy(t *testing.T) {
	home := testHome(t)
	secrets := mkdir(t, filepath.Join(home, "secrets"))
	policy, _ := workspacePolicy(t)
	policy.DenyExtra = []string{secrets}
	policy.WriteExtra = []string{mkdir(t, filepath.Join(secrets, "inside"))}

	r := Report(Availability{Mechanism: "bwrap", OK: true, Detail: "ok"}, policy)
	if !strings.Contains(r, "wrap unsupported") || !strings.Contains(r, "never run bare") {
		t.Fatalf("report should surface the refused policy:\n%s", r)
	}
}
