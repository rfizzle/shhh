package sandbox

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

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
