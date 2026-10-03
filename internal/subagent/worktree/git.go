package worktree

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/rfizzle/shhh/internal/hostgit"
)

// RunGit executes one git command in dir, returning combined output.
func RunGit(dir string, args ...string) (string, error) {
	return runGitContext(context.Background(), dir, args...)
}

// runGitContext lets a stopping writer interrupt worktree creation rather than
// waiting for git's repository lock. A writer has not started its turn until
// the copy exists, so a stop that cannot reach this command leaves its slot and
// the parent waiting behind work it no longer wants.
func runGitContext(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := hostgit.Command(ctx, dir, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// GitOutput runs one git command and returns its standard output alone, the
// streams kept apart wherever the output is content rather than a report.
func GitOutput(dir string, args ...string) (string, error) {
	return hostgit.Output(context.Background(), dir, args...)
}

// gitWithEnv runs one git command with extra environment and, where it is not
// empty, the given text on its standard input, answering with its standard
// output. It exists for the scratch index a reseed builds its base in.
func gitWithEnv(dir string, env []string, stdin string, args ...string) (string, error) {
	cmd := hostgit.Command(context.Background(), dir, args...)
	if len(env) > 0 {
		cmd.Env = hostgit.Env(append(os.Environ(), env...))
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(errBuf.String()))
	}
	return out.String(), nil
}
