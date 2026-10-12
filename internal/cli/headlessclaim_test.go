package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rfizzle/shhh/internal/tools"
)

// An unattended run's command is bracketed as a session's is: what became
// dirty while it ran joins what the run wrote, and a file somebody had
// already changed before it ran does not.
func TestBracketRun_AddsWhatTheCommandMadeDirty(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "# project\n")
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
		{"add", "README.md"}, {"commit", "-q", "-m", "seed"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write("README.md", "# project\n\nmine\n")

	var wrote []string
	run := bracketRun(root, func(context.Context, string) tools.ExecResult {
		write("gen.pb.go", "package gen\n")
		write("README.md", "# project\n\nmine, and the command's\n")
		return tools.ExecResult{}
	}, func(paths ...string) { wrote = append(wrote, paths...) })
	run(context.Background(), "make gen")

	if len(wrote) != 1 || filepath.Base(wrote[0]) != "gen.pb.go" {
		t.Fatalf("the run wrote %v, want the generated file alone", wrote)
	}
}
