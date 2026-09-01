package eval

// Running a case.
//
// The session under measurement is a real one: the shhh binary that is doing
// the measuring, invoked as `code --print --json` in a copy of the case's
// workspace. Driving the agent in-process would be less code and would
// measure less of the thing — the system prompt is assembled from the
// project, the machine and the config, and a harness that skipped that
// assembly could not tell you whether a change to it helped.
//
// What it does not do is grade the transcript. The verdict is the case's own
// check command, run in the workspace the session left behind; the transcript
// is read only for what it costs to say something was done.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Options is how a run is parameterised. The zero value runs each case once,
// with no ceiling, on whatever the config resolves to.
type Options struct {
	// Binary is the shhh to measure. Empty means the running one, which is
	// what makes `shhh eval` measure the build it is part of.
	Binary string
	// Repeat is how many attempts each case gets. Zero and one both mean one.
	Repeat int
	// Timeout bounds one attempt. Zero removes it.
	Timeout time.Duration
	// Args are the flags forwarded to the session — the provider, model and
	// reasoning level being measured. They are forwarded rather than resolved
	// here so that what runs is exactly what the reader would get by typing
	// the same flags.
	Args []string
	// Price prices one attempt's usage. Nil leaves every attempt unpriced,
	// which is honest rather than zero.
	Price func(model string, in, out int) (float64, bool)
	// Model names what is being measured, for the report's title.
	Model string
	// Progress, when set, is called as each attempt finishes, so a run that
	// takes minutes says something while it does.
	Progress func(c Case, attempt int, a Attempt)
}

func (o Options) repeats() int {
	if o.Repeat < 1 {
		return 1
	}
	return o.Repeat
}

// transcript is the part of `--json` this needs. It is deliberately a subset:
// the emitter owns that format, and a decoder that insisted on every field
// would break on a field being added to it.
type transcript struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Messages []struct {
		Role      string `json:"role"`
		ToolCalls []struct {
			Name string `json:"name"`
		} `json:"tool_calls"`
	} `json:"messages"`
}

// rounds and calls are the shape of the work: an assistant message carrying
// tool calls is one round, whatever it asked for in it.
func (t transcript) rounds() (rounds, calls int) {
	for _, m := range t.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			rounds++
			calls += len(m.ToolCalls)
		}
	}
	return rounds, calls
}

// Run measures every case, in order, and returns the summary.
//
// Cases run one at a time on purpose. They are measured on rounds and wall
// clock as much as on passing, and several sessions competing for the same
// cores, the same disk and the same rate limit would make those numbers about
// the machine instead of about the change.
func Run(ctx context.Context, cases []Case, opts Options) (Summary, error) {
	bin := opts.Binary
	if bin == "" {
		self, err := os.Executable()
		if err != nil {
			return Summary{}, fmt.Errorf("cannot find the binary to measure: %w", err)
		}
		bin = self
	}

	sum := Summary{Model: opts.Model}
	for _, c := range cases {
		res := Result{Case: c}
		if c.Skip == "" {
			for i := 0; i < opts.repeats(); i++ {
				if err := ctx.Err(); err != nil {
					return sum, err
				}
				a := attempt(ctx, bin, c, opts)
				res.Attempts = append(res.Attempts, a)
				if opts.Progress != nil {
					opts.Progress(c, i+1, a)
				}
			}
		}
		sum.Results = append(sum.Results, res)
	}
	return sum, nil
}

// attempt runs one case once. Every failure is a result rather than an error:
// one case that cannot run must not end a suite that is measuring twelve.
func attempt(ctx context.Context, bin string, c Case, opts Options) Attempt {
	start := time.Now()
	a := Attempt{}

	dir, cleanup, err := materialize(c)
	if err != nil {
		a.Err = err
		a.Elapsed = time.Since(start)
		return a
	}
	defer cleanup()

	runCtx := ctx
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	args := append([]string{"code", "--print", "--json", "--yes"}, opts.Args...)
	args = append(args, c.Prompt)
	cmd := exec.CommandContext(runCtx, bin, args...)
	cmd.Dir = dir
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	runErr := cmd.Run()

	// The transcript is read whatever the exit status: a session that failed
	// late still did work, and the numbers from it are the ones that say how
	// far it got.
	var t transcript
	if decodeErr := json.NewDecoder(strings.NewReader(out.String())).Decode(&t); decodeErr == nil {
		a.Rounds, a.Calls = t.rounds()
		a.TokensIn, a.TokensOut = t.Usage.PromptTokens, t.Usage.CompletionTokens
		if opts.Price != nil {
			a.Cost, a.Priced = opts.Price(opts.Model, a.TokensIn, a.TokensOut)
		}
	}

	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		a.Err = fmt.Errorf("the session did not finish inside %s", opts.Timeout)
	case runErr != nil:
		a.Err = fmt.Errorf("the session failed: %s", firstLine(t.Error, errOut.String(), runErr.Error()))
	}
	if a.Err != nil {
		a.Elapsed = time.Since(start)
		return a
	}

	a.Passed, a.CheckOutput = check(ctx, dir, c.Check)
	a.Elapsed = time.Since(start)
	return a
}

// check runs the case's verdict command in the workspace the session left.
func check(ctx context.Context, dir string, argv []string) (passed bool, output string) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, ""
	}
	return false, strings.TrimSpace(string(out))
}

// materialize copies a case's fixture somewhere the session may edit it, and
// makes it a repository.
//
// The copy is the point: a case that ran in its own directory would grade its
// second attempt against the leftovers of its first, and would drift a little
// further every time it ran.
//
// The git init is not decoration. A session in a checkout can undo a turn, its
// prompt states the branch and the dirty count, and its containment grants
// differ — so a fixture that was not a repository would be measuring a
// session nobody actually runs.
func materialize(c Case) (dir string, cleanup func(), err error) {
	dir, err = os.MkdirTemp("", "shhh-eval-")
	if err != nil {
		return "", func() {}, fmt.Errorf("cannot make a workspace: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	if err := copyTree(c.Workspace, dir); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := initRepo(dir); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return dir, cleanup, nil
}

func initRepo(dir string) error {
	for _, argv := range [][]string{
		{"init", "--quiet"},
		{"config", "user.email", "eval@shhh.invalid"},
		{"config", "user.name", "shhh eval"},
		{"add", "-A"},
		{"commit", "--quiet", "-m", "fixture"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, argv...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w: %s", argv[0], err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// copyTree copies src into dst, preserving the executable bit and nothing
// else. Fixtures are source files.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		// A symlink in a fixture would point at the case directory, which is
		// exactly the thing an attempt must not be able to edit.
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s: a fixture may not contain a symlink", rel)
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// firstLine is the first non-empty line of the first non-empty candidate,
// which is what an error row has room for.
func firstLine(candidates ...string) string {
	for _, c := range candidates {
		for _, line := range strings.Split(c, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				return line
			}
		}
	}
	return "no output"
}
