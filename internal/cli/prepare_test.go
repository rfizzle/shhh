package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/ui/components"
)

const preparedBase = "ghcr.io/acme/sandbox@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// writeDeclaration puts a toolchain declaration in a trusted checkout of the
// test's own and hands back what the loader makes of it.
func writeDeclaration(t *testing.T, decl string) project.Toolchain {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(project.ToolchainFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(decl), 0o644); err != nil {
		t.Fatal(err)
	}
	withProjectTrust(t, project.Trust{Root: root, Granted: true})
	// A reading puts shhh's toolchain directory on the captured commands'
	// PATH, under a cache of the test's own.
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Cleanup(func() { runner.SetPathAfter("") })
	tc, _, _ := project.LoadToolchain(projectTrust())
	return tc
}

// recordingEngine is an engine that writes down every call and finds the
// prepared image for key, where key is set.
func recordingEngine(t *testing.T, key string) (sandbox.Engine, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\n" +
		"if [ \"$1\" = image ] && [ -n \"" + key + "\" ]; then\n" +
		"  echo '[{\"Id\":\"sha256:" + strings.Repeat("c", 64) + "\",\"Config\":{\"Labels\":{\"shhh.toolchain.key\":\"" + key + "\"}}}]'\n" +
		"  exit 0\nfi\necho 'Error response from daemon: No such image' >&2\nexit 1\n"
	path := filepath.Join(dir, "docker")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return sandbox.Engine{Name: "docker", Path: path, OK: true}, log
}

func engineCalls(t *testing.T, log string) string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

// A declaration that does not load stops the run before the engine is asked
// anything, and one that installs nothing leaves the base to run: in neither
// case is there an image to prepare.
func TestASandboxRunStartsFromTheBaseOnlyWhereNothingIsDeclared(t *testing.T) {
	eng, log := recordingEngine(t, "")
	store := sandbox.NewStoreAt(filepath.Join(t.TempDir(), "sandboxes.json"))
	spec := sandbox.ContainerSpec{Image: preparedBase}

	writeDeclaration(t, "check = [\"jq\"]\n")
	if id, err := preparedImage(context.Background(), eng, spec, nil, store); err != nil || id != "" {
		t.Errorf("a declaration of checks alone prepared %q, %v", id, err)
	}
	writeDeclaration(t, "install = [\"go install example.com/cmd@latest\"]\n")
	if _, err := preparedImage(context.Background(), eng, spec, nil, store); err == nil || !strings.Contains(err.Error(), "@latest") {
		t.Errorf("a declaration that does not load started a run: %v", err)
	}
	withProjectTrust(t, project.Trust{Root: t.TempDir()})
	if id, err := preparedImage(context.Background(), eng, spec, nil, store); err != nil || id != "" {
		t.Errorf("an untrusted checkout prepared %q, %v", id, err)
	}
	if calls := engineCalls(t, log); calls != "" {
		t.Errorf("the engine was asked about an image nothing declared:\n%s", calls)
	}
}

// A prepared image current for the base and the declaration is the one the
// run starts from, and nothing is prepared again.
func TestASandboxRunReusesTheImagePreparedForItsDeclaration(t *testing.T) {
	tc := writeDeclaration(t, "packages = [\"jq\"]\n")
	eng, log := recordingEngine(t, sandbox.PreparedKey(preparedBase, tc.Digest))
	store := sandbox.NewStoreAt(filepath.Join(t.TempDir(), "sandboxes.json"))

	id, err := preparedImage(context.Background(), eng, sandbox.ContainerSpec{Image: preparedBase}, nil, store)
	if err != nil || id != "sha256:"+strings.Repeat("c", 64) {
		t.Fatalf("preparedImage = %q, %v; want the image already prepared", id, err)
	}
	calls := engineCalls(t, log)
	if strings.Contains(calls, "run ") || strings.Contains(calls, "commit") {
		t.Errorf("a current image was prepared again:\n%s", calls)
	}
	// A base the allowlist refuses is refused before the engine is asked
	// whether anything was prepared from it.
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	if _, err := preparedImage(context.Background(), eng, sandbox.ContainerSpec{Image: preparedBase}, []string{"other@sha256:" + strings.Repeat("d", 64)}, store); err == nil {
		t.Error("a prepared image was used over a base the allowlist refuses")
	}
	if calls := engineCalls(t, log); calls != "" {
		t.Errorf("the engine was asked about a refused base:\n%s", calls)
	}
}

// The image row names the declaration, the prepared image and whether it is
// current, in the outcome a row never clips; an image not yet prepared is a
// wait it warns of rather than a fault.
func TestTheDoctorNamesThePreparedImageAndWhetherItIsCurrent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reading preparedReading
		outcome string
		state   components.DoctorState
		says    []string
	}{
		{"current", preparedReading{declared: true, sandbox: true, id: "sha256:" + strings.Repeat("c", 64)},
			"current", components.DoctorPassed, []string{project.ToolchainFile, "sha256:cccccccccccc…"}},
		{"podman's spelling", preparedReading{declared: true, sandbox: true, id: strings.Repeat("c", 64)},
			"current", components.DoctorPassed, []string{"sha256:cccccccccccc…"}},
		{"not prepared", preparedReading{declared: true, sandbox: true},
			"not prepared", components.DoctorSkipped, []string{project.ToolchainFile, "next --sandbox run prepares it"}},
		{"engine could not say", preparedReading{declared: true, sandbox: true, err: errors.New("daemon gone")},
			"unknown", components.DoctorWarned, []string{project.ToolchainFile, "daemon gone"}},
		{"no sandbox", preparedReading{declared: true},
			"not checked", components.DoctorSkipped, []string{project.ToolchainFile}},
		{"broken", preparedReading{broken: true},
			"not checked", components.DoctorSkipped, []string{project.ToolchainFile, "--sandbox refuses"}},
		{"nothing declared", preparedReading{},
			"empty", components.DoctorSkipped, []string{"nothing to prepare"}},
	} {
		f := doctorImage(tc.reading)
		if f.Outcome != tc.outcome || f.State != tc.state {
			t.Errorf("%s: the row reads %q (%v), want %q (%v)", tc.name, f.Outcome, f.State, tc.outcome, tc.state)
		}
		row := f.Subject + " · " + f.Detail
		for _, want := range tc.says {
			if !strings.Contains(row, want) {
				t.Errorf("%s: the row does not say %q: %q", tc.name, want, row)
			}
		}
	}
}

// The probe reads the declaration before it looks for an engine, and says a
// declaration with no sandbox to prepare it for is not checked rather than
// not prepared.
func TestTheImageProbeWithNoEngineChecksNothing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	writeDeclaration(t, "packages = [\"jq\"]\n")
	if f := probeImage(context.Background(), config.Config{}); f.Outcome != "not checked" || f.Subject != project.ToolchainFile {
		t.Errorf("a declaration with no engine reads %+v", f)
	}
	writeDeclaration(t, "check = [\"jq\"]\n")
	if f := probeImage(context.Background(), config.Config{}); f.Outcome != "empty" {
		t.Errorf("a declaration of checks alone reads %+v", f)
	}
}

// A --sandbox run's commands find what the prepared image holds, so the
// host's PATH is not what it is warned about.
func TestASandboxRunIsNotToldWhatTheHostsPathLacks(t *testing.T) {
	writeDeclaration(t, "check = [\"shhh-no-such-tool\"]\n")
	if note := (chatSession{}).toolchainNote(); !strings.Contains(note, "shhh-no-such-tool") {
		t.Fatalf("a host session is not told the tool is missing: %q", note)
	}
	if note := (chatSession{sandbox: true}).toolchainNote(); note != "" {
		t.Errorf("a --sandbox run is told about the host's PATH: %q", note)
	}
	if note := (chatSession{conversation: true}).toolchainNote(); note != "" {
		t.Errorf("a conversation is told about tools it cannot run: %q", note)
	}
}
