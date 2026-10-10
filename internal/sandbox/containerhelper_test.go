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

// Every program a sandbox session starts in its container is one exec under
// the helper, and the five callers differ only in the flags: a command and a
// tailed command stop on a hang-up and read nothing; a process may take a
// terminal and its stream, and carries its own variables; a hook writes its
// payload and lets go, so it ignores the hang-up and is held to its own
// ceiling; a gate check runs where its suite says. The command text rides
// whole after `sh -c`, and a directory outside the workspace is refused in a
// sentence rather than run somewhere else.
func TestContainerHelperArgv(t *testing.T) {
	ws, err := resolvePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(ws, "pkg", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	c := Container{Record: Record{Name: "shhh-sbx-abc", Workspace: ws}, Engine: Engine{Path: "/usr/bin/docker"}}
	command := `echo "a; b" | wc -l`
	head := []string{"/usr/bin/docker", "exec", "-i"}
	helper := []string{"shhh-sbx-abc", HelperPath, ExecArg}
	cat := func(parts ...[]string) []string {
		var out []string
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	for _, tc := range []struct {
		name string
		exec HelperExec
		argv []string
		want []string
	}{
		{"a command", HelperExec{Secrets: []string{"API_TOKEN"}}, []string{"/bin/sh", "-c", command},
			cat(head, []string{"--workdir", "/workspace", "--env", "API_TOKEN"}, helper,
				[]string{"--stdin=null", "--hangup=stop", "--", "/bin/sh", "-c", command})},
		{"a tailed command", HelperExec{}, []string{"/bin/sh", "-c", "go test ./..."},
			cat(head, []string{"--workdir", "/workspace"}, helper,
				[]string{"--stdin=null", "--hangup=stop", "--", "/bin/sh", "-c", "go test ./..."})},
		{"a process", HelperExec{Dir: sub, TTY: true, Inherit: true, Secrets: []string{"API_TOKEN"}, Env: []string{"PORT=3001"}},
			[]string{"npm", "run", "dev"},
			cat(head, []string{"-t", "--workdir", "/workspace/pkg/api", "--env", "API_TOKEN", "--env", "PORT=3001"}, helper,
				[]string{"--stdin=inherit", "--hangup=stop", "--", "npm", "run", "dev"})},
		{"a hook", HelperExec{IgnoreHangup: true, Timeout: 30 * time.Second}, []string{"/bin/sh", "-c", "./notify"},
			cat(head, []string{"--workdir", "/workspace"}, helper,
				[]string{"--stdin=null", "--hangup=ignore", "--timeout=30s", "--", "/bin/sh", "-c", "./notify"})},
		{"a gate check", HelperExec{Dir: ws}, []string{"go", "vet", "./..."},
			cat(head, []string{"--workdir", "/workspace"}, helper,
				[]string{"--stdin=null", "--hangup=stop", "--", "go", "vet", "./..."})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.HelperArgv(tc.exec, tc.argv)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("HelperArgv =\n  %q\nwant\n  %q", got, tc.want)
			}
		})
	}
	if got, err := c.HelperCommand(HelperExec{}, command); err != nil || got[len(got)-1] != command || got[len(got)-2] != "-c" {
		t.Errorf("HelperCommand = %q, %v; want the text whole after sh -c", got, err)
	}
	if _, err := c.HelperArgv(HelperExec{Dir: t.TempDir()}, []string{"true"}); err == nil || !strings.Contains(err.Error(), "outside the sandbox's workspace") {
		t.Errorf("a directory outside the workspace: %v", err)
	}
	if _, err := c.HelperArgv(HelperExec{}, nil); err == nil {
		t.Error("an empty argv was built into an exec")
	}
}

// The probe a session makes before its first turn: a helper that answers
// this shhh's protocol passes; no helper, or one that speaks another, is
// refused with what it answered.
func TestContainerProbeHelper(t *testing.T) {
	for _, tc := range []struct{ name, script, refusal string }{
		{"speaks it", `echo "shhh-sandbox-exec ` + HelperVersion + `"`, ""},
		{"absent", `echo "OCI runtime exec failed: stat ` + HelperPath + `: no such file or directory" >&2; exit 126`, "no command helper"},
		{"another protocol", `echo "shhh-sandbox-exec 0"`, "answers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := stubEngine(t, "docker", tc.script)
			c := Container{Record: Record{Name: "shhh-sbx-abc"}, Engine: Engine{Path: filepath.Join(dir, "docker")}}
			err := c.ProbeHelper(context.Background())
			if tc.refusal == "" {
				if err != nil {
					t.Fatalf("ProbeHelper = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.refusal) {
				t.Fatalf("ProbeHelper = %v, want it refused (%s)", err, tc.refusal)
			}
		})
	}
}
