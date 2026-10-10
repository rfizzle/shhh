package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestIntegrationContainerMatchesCreateArgv(t *testing.T) {
	eng := Engine{Name: "docker", Path: "/usr/bin/docker", OK: true}
	for _, network := range []bool{false, true} {
		spec, err := ContainerSpec{Image: "wolfi-base" + testDigest, Workspace: t.TempDir(), Network: network}.withDefaults()
		if err != nil {
			t.Fatal(err)
		}
		flags := HardeningFlags(spec.PidsLimit, network)
		argv := createArgv(eng, "n", spec)
		found := false
		for i := 0; i+len(flags) <= len(argv); i++ {
			if slices.Equal(argv[i:i+len(flags)], flags) {
				found = true
			}
		}
		if !found {
			t.Errorf("network=%v: createArgv %q does not carry %q in order", network, argv, flags)
		}
		for _, want := range []string{"--init", "--pids-limit", "--cap-drop", "--security-opt"} {
			if !slices.Contains(flags, want) {
				t.Errorf("HardeningFlags lacks %s", want)
			}
		}
		if got := slices.Contains(flags, "--network"); got == network {
			t.Errorf("network=%v but flags carry --network: %v", network, got)
		}
	}
}

func TestWorkdir_ASymlinkedWorkspaceIsInside(t *testing.T) {
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(tmp, "real")
	if err := os.MkdirAll(filepath.Join(real, "ws", "sub"), 0o777); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct{ workspace, dir string }{
		"workspace through the link": {filepath.Join(link, "ws"), filepath.Join(real, "ws", "sub")},
		"dir through the link":       {filepath.Join(real, "ws"), filepath.Join(link, "ws", "sub")},
	} {
		got, err := Container{Record: Record{Workspace: c.workspace}}.workdir(c.dir)
		if err != nil || got != workspaceMount+"/sub" {
			t.Errorf("%s: workdir = %q, %v; want %s/sub", name, got, err, workspaceMount)
		}
	}
	if _, err := (Container{Record: Record{Workspace: filepath.Join(link, "ws")}}).workdir(real); err == nil {
		t.Error("a dir above the workspace should still be refused")
	}
}
