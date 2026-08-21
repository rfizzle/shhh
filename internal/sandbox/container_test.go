package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const testDigest = "@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestParseIsolationAndRank(t *testing.T) {
	for in, want := range map[string]Isolation{
		"process":    IsolationProcess,
		" Container": IsolationContainer,
		"VM":         IsolationVM,
	} {
		got, err := ParseIsolation(in)
		if err != nil || got != want {
			t.Errorf("ParseIsolation(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "jail"} {
		if _, err := ParseIsolation(bad); err == nil {
			t.Errorf("ParseIsolation(%q) should fail", bad)
		}
	}
	if IsolationProcess.Rank() >= IsolationContainer.Rank() || IsolationContainer.Rank() >= IsolationVM.Rank() {
		t.Error("isolation levels must rank process < container < vm")
	}
}

func TestVerifyIsolation(t *testing.T) {
	procOK := Availability{OK: true, Mechanism: "bwrap", Detail: "ok"}
	procNo := Availability{Detail: "no mechanism"}
	engOK := Engine{OK: true, Name: "podman", Detail: "ok"}
	engNo := Engine{Detail: "no engine"}

	if err := VerifyIsolation(IsolationProcess, procOK, engNo); err != nil {
		t.Errorf("process with mechanism should verify: %v", err)
	}
	if err := VerifyIsolation(IsolationProcess, procNo, engOK); err == nil {
		t.Error("process without mechanism must fail, not downgrade")
	}
	if err := VerifyIsolation(IsolationContainer, procNo, engOK); err != nil {
		t.Errorf("container with engine should verify: %v", err)
	}
	if err := VerifyIsolation(IsolationContainer, procOK, engNo); err == nil {
		t.Error("container without engine must fail, not downgrade to process")
	}
	if err := VerifyIsolation(IsolationVM, procOK, engOK); err == nil {
		t.Error("vm can never be verified and must always fail")
	}
}

func TestValidateImage(t *testing.T) {
	pinned := "docker.io/library/alpine" + testDigest
	if err := ValidateImage(pinned, nil); err != nil {
		t.Errorf("pinned image with no allowlist should pass: %v", err)
	}
	if err := ValidateImage(pinned, []string{pinned}); err != nil {
		t.Errorf("allowlisted image should pass: %v", err)
	}
	for name, image := range map[string]string{
		"empty":        "",
		"tag only":     "alpine:3.20",
		"short digest": "alpine@sha256:abcd",
		"leading dash": "-alpine" + testDigest,
	} {
		if err := ValidateImage(image, nil); err == nil {
			t.Errorf("%s image %q should be refused", name, image)
		}
	}
	if err := ValidateImage(pinned, []string{"other" + testDigest}); err == nil {
		t.Error("image outside the allowlist should be refused")
	}
}

func TestSpecDefaultsAndCeilingValidation(t *testing.T) {
	s, err := ContainerSpec{}.withDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if s.Memory != DefaultContainerMemory || s.CPUs != DefaultContainerCPUs ||
		s.PidsLimit != DefaultContainerPids || s.TTL != DefaultContainerTTL {
		t.Errorf("defaults not applied: %+v", s)
	}
	if _, err := (ContainerSpec{Memory: "2g; rm -rf /"}).withDefaults(); err == nil {
		t.Error("malformed memory limit should be refused")
	}
	if _, err := (ContainerSpec{CPUs: "--privileged"}).withDefaults(); err == nil {
		t.Error("malformed cpu limit should be refused")
	}
}

func TestCreateArgvHardening(t *testing.T) {
	eng := Engine{Name: "podman", Path: "/usr/bin/podman", OK: true}
	ws := t.TempDir()
	spec, err := ContainerSpec{Image: "alpine" + testDigest, Workspace: ws, Network: true}.withDefaults()
	if err != nil {
		t.Fatal(err)
	}
	argv := createArgv(eng, "shhh-sbx-test", spec)

	joined := strings.Join(argv, " ")
	for _, want := range []string{
		"--cap-drop ALL",
		"--security-opt no-new-privileges",
		"--memory " + DefaultContainerMemory,
		"--cpus " + DefaultContainerCPUs,
		"--pids-limit 256",
		"--volume " + ws + ":" + workspaceMount,
		"--workdir " + workspaceMount,
		"--entrypoint ",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("createArgv missing %q in %q", want, joined)
		}
	}

	// Exactly one host mount, and only the two explicit env vars — no host
	// environment or credentials ride in.
	if n := strings.Count(joined, "--volume"); n != 1 {
		t.Errorf("want exactly one writable mount, got %d", n)
	}
	var envs []string
	for i, a := range argv {
		if a == "--env" {
			envs = append(envs, argv[i+1])
		}
	}
	if len(envs) != 2 || envs[0] != "HOME=/root" || envs[1] != containerPath {
		t.Errorf("container env must be exactly HOME and PATH, got %v", envs)
	}

	if strings.Contains(joined, "--network none") {
		t.Error("workspace profile should preserve network")
	}
	netless := createArgv(eng, "n", ContainerSpec{Image: spec.Image, Workspace: ws, Memory: "1g", CPUs: "1", PidsLimit: 1, TTL: time.Hour})
	if !strings.Contains(strings.Join(netless, " "), "--network none") {
		t.Error("netless spec should disable network")
	}
}

func TestExecArgvCommandRidesUnparsed(t *testing.T) {
	c := Container{
		Record: Record{Name: "shhh-sbx-abc"},
		Engine: Engine{Path: "/usr/bin/podman"},
	}
	command := `echo "a; b" | wc -l`
	argv := c.ExecArgv(command)
	want := []string{"/usr/bin/podman", "exec", "--workdir", workspaceMount, "shhh-sbx-abc", "/bin/sh", "-c", command}
	if !slices.Equal(argv, want) {
		t.Errorf("ExecArgv = %v, want %v", argv, want)
	}
}

func TestRecordStore(t *testing.T) {
	store := NewStoreAt(filepath.Join(t.TempDir(), "sandboxes.json"))

	recs, err := store.List()
	if err != nil || len(recs) != 0 {
		t.Fatalf("empty store should list nothing: %v, %v", recs, err)
	}

	now := time.Now().UTC()
	a := Record{ID: "aaa", Name: "shhh-sbx-aaa", Engine: "podman", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	b := Record{ID: "bbb", Name: "shhh-sbx-bbb", Engine: "docker", CreatedAt: now, ExpiresAt: now.Add(-time.Hour)}
	if err := store.Add(a); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(b); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("record file permissions = %o, want 600", perm)
	}

	if got, err := store.Get("bbb"); err != nil || got.Engine != "docker" {
		t.Errorf("Get(bbb) = %+v, %v", got, err)
	}
	if _, err := store.Get("zzz"); err == nil {
		t.Error("Get of unknown id should fail")
	}
	if b.Expired(now) != true || a.Expired(now) != false {
		t.Error("Expired should follow ExpiresAt")
	}

	if err := store.Remove("aaa"); err != nil {
		t.Fatal(err)
	}
	recs, err = store.List()
	if err != nil || len(recs) != 1 || recs[0].ID != "bbb" {
		t.Errorf("after Remove: %v, %v", recs, err)
	}
	if err := store.Remove("absent"); err != nil {
		t.Errorf("removing an absent id should be a no-op: %v", err)
	}
}

// stubEngine installs a fake engine binary and puts it alone on PATH. The
// script answers `info`, `inspect`, and `rm` the way the reconcile and
// detection paths expect.
func stubEngine(t *testing.T, name, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return dir
}

func TestReconcile(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "removed")
	t.Setenv("SBX_MARKER", marker)
	stubEngine(t, "podman", `
case "$1" in
  inspect)
    case "$4" in
      *gone*) echo "Error: no such container $4" >&2; exit 1;;
      *) echo running;;
    esac;;
  rm) echo "$3" >> "$SBX_MARKER";;
esac
`)

	store := NewStoreAt(filepath.Join(t.TempDir(), "sandboxes.json"))
	now := time.Now().UTC()
	live := Record{ID: "live", Name: "shhh-sbx-live", Engine: "podman", ExpiresAt: now.Add(time.Hour)}
	gone := Record{ID: "gone", Name: "shhh-sbx-gone", Engine: "podman", ExpiresAt: now.Add(time.Hour)}
	expired := Record{ID: "old", Name: "shhh-sbx-old", Engine: "podman", ExpiresAt: now.Add(-time.Hour)}
	noEngine := Record{ID: "orphan", Name: "shhh-sbx-orphan", Engine: "docker", ExpiresAt: now.Add(time.Hour)}
	for _, r := range []Record{live, gone, expired, noEngine} {
		if err := store.Add(r); err != nil {
			t.Fatal(err)
		}
	}

	res := Reconcile(context.Background(), store, now)

	ids := func(recs []Record) []string {
		var out []string
		for _, r := range recs {
			out = append(out, r.ID)
		}
		return out
	}
	if got := ids(res.Dropped); !slices.Equal(got, []string{"gone"}) {
		t.Errorf("Dropped = %v, want [gone]", got)
	}
	if got := ids(res.Reaped); !slices.Equal(got, []string{"old"}) {
		t.Errorf("Reaped = %v, want [old]", got)
	}
	if got := ids(res.Kept); !slices.Equal(got, []string{"live", "orphan"}) {
		t.Errorf("Kept = %v, want [live orphan]", got)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "docker") {
		t.Errorf("missing-engine record should keep an error note: %v", res.Errors)
	}

	removed, err := os.ReadFile(marker)
	if err != nil || strings.TrimSpace(string(removed)) != "shhh-sbx-old" {
		t.Errorf("only the expired container should be force-removed, got %q (%v)", removed, err)
	}

	left, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(left); !slices.Equal(got, []string{"live", "orphan"}) {
		t.Errorf("records after reconcile = %v, want [live orphan]", got)
	}
}

func TestDetectEnginePrefersRootless(t *testing.T) {
	dir := t.TempDir()
	write := func(name, script string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Rootful podman, rootless docker: the rootless engine wins.
	write("podman", `echo false`)
	write("docker", `echo "[name=seccomp name=rootless]"`)
	t.Setenv("PATH", dir)

	eng := DetectEngine("")
	if !eng.OK || eng.Name != "docker" || !eng.Rootless {
		t.Errorf("expected rootless docker preferred, got %+v", eng)
	}

	forced := DetectEngine("podman")
	if !forced.OK || forced.Name != "podman" || forced.Rootless {
		t.Errorf("forced podman should be detected rootful, got %+v", forced)
	}
	if !strings.Contains(forced.Detail, "rootful") {
		t.Errorf("rootless state must be reported, got %q", forced.Detail)
	}

	if bad := DetectEngine("qemu"); bad.OK || !strings.Contains(bad.Detail, "unknown container engine") {
		t.Errorf("unknown forced engine must be refused: %+v", bad)
	}
}

func TestDetectEngineHonestWhenAbsent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	eng := DetectEngine("")
	if eng.OK {
		t.Fatalf("no engines on PATH should not detect one: %+v", eng)
	}
	for _, name := range []string{"podman", "docker"} {
		if !strings.Contains(eng.Detail, name) {
			t.Errorf("failure detail should name %s: %q", name, eng.Detail)
		}
	}
}

func TestCreateContainerFailsClosed(t *testing.T) {
	store := NewStoreAt(filepath.Join(t.TempDir(), "sandboxes.json"))
	engNo := Engine{Detail: "no engine"}
	if _, err := CreateContainer(context.Background(), engNo, ContainerSpec{Image: "a" + testDigest, Workspace: t.TempDir()}, nil, store); err == nil {
		t.Error("create without an engine must fail")
	}
	engOK := Engine{Name: "podman", Path: "/usr/bin/podman", OK: true}
	if _, err := CreateContainer(context.Background(), engOK, ContainerSpec{Image: "alpine:latest", Workspace: t.TempDir()}, nil, store); err == nil {
		t.Error("create with an unpinned image must fail")
	}
	if recs, _ := store.List(); len(recs) != 0 {
		t.Error("failed creations must not leave ownership records")
	}
}
