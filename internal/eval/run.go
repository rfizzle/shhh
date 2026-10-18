package eval

// Running a case.
//
// The session under measurement is a real one: the shhh binary that is doing
// the measuring, invoked as `code --print --output jsonl` (`chat --print
// --output jsonl` for a chat case, `serve --stdio` for a case with a persona,
// serve.go) in a copy of the case's workspace. Driving the agent in-process would be less code and would
// measure less of the thing — the system prompt is assembled from the
// project, the machine and the config, and a harness that skipped that
// assembly could not tell you whether a change to it helped.
//
// What it does not do is grade the session's words. The verdict is the case's
// own check command, run in the workspace the session left behind, and the
// assertions the case declares about its calls (assert.go); the stream is read
// for what it cost to do the work, and for which calls it made.
//
// A table case goes the other way, and for the same reason: an auxiliary call
// assembles its own instruction and its own bounds inside this process, with
// nothing from the project or the machine in them, so running a binary to
// reach it would add a session's assembly to a measurement that is not about
// a session. The request that goes out is the one a session would send.
//
// A research case runs its loop here too, and its reason is in research.go:
// its site is on loopback, and the exception that reaches one loopback server
// belongs in the process that knows which server that is.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rfizzle/llm"
	"github.com/rfizzle/shhh/internal/hostgit"
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
	// Model names what is being measured, for the report's title, and is the
	// model a table case's call is made on.
	Model string
	// Provider is what a table or research case asks. A workspace case never
	// uses it — that one runs the binary, which resolves its own — but the
	// other two shapes run in this process and have no binary to resolve one,
	// so the harness makes the requests itself and needs somewhere to send
	// them.
	Provider llm.Provider
	// ClassifierBackend is how a classifier case's rows are asked: empty or
	// "completion" for the verdict in words, "decisions" for a probability
	// from a model that offers the Decisions API, held against the built-in
	// threshold. It is a flag rather than the reader's config for the reason
	// every other bound here is: two runs on one backend have to be
	// comparable by whoever ran them.
	ClassifierBackend string
	// ClassifierThreshold is the percentage the decisions backend's
	// probability must reach for a row to be allowed, as the setting spells
	// it. Zero takes the built-in bar. Like the backend it is a flag and not
	// the reader's config, and it lets one run be read at several bars.
	ClassifierThreshold int
	// Commands runs a read-only case's inspection commands, contained the way
	// a child's are. Nil leaves such a case unable to run, which it reports
	// rather than running a model's command bare.
	Commands commandRunner
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

// Run measures every case, in order, and returns the summary.
//
// Cases run one at a time on purpose. They are measured on rounds and wall
// clock as much as on passing, and several sessions competing for the same
// cores, the same disk and the same rate limit would make those numbers about
// the machine instead of about the change.
func Run(ctx context.Context, cases []Case, opts Options) (Summary, error) {
	bin := opts.Binary
	if bin == "" && needsBinary(cases) {
		self, err := os.Executable()
		if err != nil {
			return Summary{}, fmt.Errorf("cannot find the binary to measure: %w", err)
		}
		bin = self
	}

	sum := Summary{Model: opts.Model, ClassifierBackend: opts.ClassifierBackend, ClassifierThreshold: opts.ClassifierThreshold}
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

// needsBinary reports whether anything in the suite runs a session process. A
// suite of table and research cases only makes requests from here, and must
// not refuse to start because the harness could not name its own executable.
func needsBinary(cases []Case) bool {
	for _, c := range cases {
		if c.Kind.RunsBinary() {
			return true
		}
	}
	return false
}

// attempt runs one case once. Every failure is a result rather than an error:
// one case that cannot run must not end a suite that is measuring twelve.
func attempt(ctx context.Context, bin string, c Case, opts Options) Attempt {
	if c.Kind.IsTable() {
		return tableAttempt(ctx, c, opts)
	}
	if c.Kind == KindResearch {
		return researchAttempt(ctx, c, opts)
	}
	start := time.Now()
	a := Attempt{}

	dir, cleanup, err := materialize(c)
	if err != nil {
		a.Err = err
		a.Elapsed = time.Since(start)
		return a
	}
	defer cleanup()

	// The session keeps its slot, its record and its evidence in a data
	// directory of its own, so they go with the workspace and never reach the
	// reader's store or `shhh observe`. Beside the workspace and not in it: a
	// directory in the fixture would show as an unintended change.
	// See docs/capabilities/evals.md#a-measured-session-keeps-nothing.
	dataDir, err := os.MkdirTemp("", "shhh-eval-data-")
	if err != nil {
		a.Err = fmt.Errorf("cannot make a data directory: %w", err)
		a.Elapsed = time.Since(start)
		return a
	}
	defer func() { _ = os.RemoveAll(dataDir) }()

	runCtx := ctx
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	fx := fixtureFiles(dir)

	var t trace
	var decodeErr, runErr error
	var errOut strings.Builder
	if c.persona != nil {
		// The server lives under the suite's context and not the attempt's:
		// at the ceiling its turn is interrupted and its session ended, so
		// what it did before then is still read, rather than the process
		// being killed mid-line.
		sv := serveAttempt(runCtx, sessionCommand(ctx, bin, dir, dataDir, c.sessionArgv(opts.Args)), c.Prompt, c.persona)
		t, a.Turns, a.Unscripted = sv.trace, sv.turns, sv.unscripted
		if sv.failed != "" {
			runErr = errors.New(sv.failed)
		}
	} else {
		cmd := sessionCommand(runCtx, bin, dir, dataDir, c.sessionArgv(opts.Args))
		var out strings.Builder
		cmd.Stdout = &out
		cmd.Stderr = &errOut
		runErr = cmd.Run()
		// The stream is read whatever the exit status: a session that failed
		// late still did work, and the numbers from it are the ones that say
		// how far it got. A line that is not an event ends the reading and
		// keeps what came before it.
		t, decodeErr = decodeStream(strings.NewReader(out.String()))
	}
	a.Rounds, a.Calls = t.rounds()
	a.Behaviour = t.behaviour()
	a.TokensIn, a.TokensOut = t.usage()
	if opts.Price != nil && len(t.events) > 0 {
		a.Cost, a.Priced = opts.Price(opts.Model, a.TokensIn, a.TokensOut)
	}
	_, closeErr, _, closed := t.closeLine()

	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		a.Err = fmt.Errorf("the session did not finish inside %s", opts.Timeout)
	case runErr != nil && c.persona != nil:
		// A served conversation names its own reason: a close line's error
		// here could be an earlier turn's, which ended well.
		a.Err = fmt.Errorf("the session failed: %w", runErr)
	case runErr != nil:
		a.Err = fmt.Errorf("the session failed: %s", firstLine(closeErr, errOut.String(), runErr.Error()))
	case len(c.Assertions) > 0 && (decodeErr != nil || !closed):
		// An assertion over the part of a session that was read would hold for
		// the calls that were not: it cannot be decided, and the run is one
		// that broke rather than a route that was fine.
		a.Err = errors.New("the session's stream ended before its close line, so its assertions cannot be decided")
	}
	if a.Err != nil {
		a.Elapsed = time.Since(start)
		return a
	}

	if c.AnalysisOnly {
		a.Behaviour.UnintendedMutations = dirtyPaths(ctx, dir)
	}
	if c.Kind == KindChat {
		a.CheckPassed, a.CheckOutput = gradeChat(c.Facts, t)
	} else {
		a.CheckPassed, a.CheckOutput = check(ctx, dir, c.Check)
	}
	a.Passed = a.CheckPassed
	if len(c.Assertions) > 0 {
		a.Findings = t.assert(c.Assertions, fx)
		a.Passed = a.CheckPassed && Broken(a.Findings) == 0
	}
	a.Elapsed = time.Since(start)
	return a
}

// sessionCommand is the one way an attempt starts the binary under
// measurement, printed or served: in the copied workspace, with the reader's
// environment and a data directory of the attempt's own. XDG_CONFIG_HOME is
// left alone: the configuration is part of what is measured, and the key
// resolves from flags, env or config, never the store.
func sessionCommand(ctx context.Context, bin, dir, dataDir string, argv []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, argv...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "XDG_DATA_HOME="+dataDir)
	return cmd
}

// tableAttempt puts every row of a table case to the real call once — the
// model's, or, for a scripted case, the harness's own.
//
// Rows go one at a time, like cases: the point of the pass is the rate, not
// the wall clock, and twenty concurrent requests to the same endpoint measure
// the rate limit rather than the model. The attempt's ceiling bounds the whole
// pass; each row is bounded again, and much sooner, by the timeout the call
// itself runs under in a session.
func tableAttempt(ctx context.Context, c Case, opts Options) Attempt {
	start := time.Now()
	a := Attempt{Score: &Score{Kind: c.Kind}}

	// A scripted case asks nothing (mechanism.go), which is what lets it run
	// where there is no account at all; every other table case is a request
	// and cannot start without somewhere to send it.
	if opts.Provider == nil && !c.Kind.Scripted() {
		a.Err = fmt.Errorf("no provider to ask: a %s case is a request, not a session", c.Kind)
		a.Elapsed = time.Since(start)
		return a
	}

	runCtx := ctx
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	for _, row := range c.Rows {
		if runCtx.Err() != nil {
			break
		}
		ans := askRow(runCtx, opts.Provider, opts.Model, c.Kind, row, opts.Commands, opts.ClassifierBackend, opts.ClassifierThreshold)
		a.Score.Answers = append(a.Score.Answers, ans)
		a.TokensIn += ans.Usage.PromptTokens
		a.TokensOut += ans.Usage.CompletionTokens
	}
	// A scripted case is left unpriced rather than priced at nothing: zero
	// dollars beside a row is a measurement, and what this ran on was not
	// the price table being cheap but no request at all.
	if opts.Price != nil && !c.Kind.Scripted() {
		a.Cost, a.Priced = opts.Price(opts.Model, a.TokensIn, a.TokensOut)
	}

	// A pass that was cut short measured a prefix of the table, and a rate
	// over a prefix is not the rate. It is the same reading as a session that
	// never finished: a machine to fix, not a score to report.
	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		a.Err = fmt.Errorf("the table did not finish inside %s", opts.Timeout)
	case ctx.Err() != nil:
		a.Err = ctx.Err()
	default:
		a.Passed = a.Score.Wrong() == 0 && a.Score.Unanswered() == 0 && a.Score.Refused() == 0
	}
	a.Elapsed = time.Since(start)
	return a
}

// fixtureFiles is the files the fixture holds before the session starts, as
// slash-separated paths inside the workspace, which is what tells an edit of a
// file from the creation of one. The root is kept under both its names, since
// a session may be told the resolved one.
func fixtureFiles(dir string) fixture {
	fx := fixture{roots: []string{dir}, files: map[string]bool{}}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != dir {
		fx.roots = append(fx.roots, resolved)
	}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if rel, relErr := filepath.Rel(dir, p); relErr == nil {
			fx.files[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	return fx
}

// dirtyPaths reports the paths the session left changed in its fresh fixture.
func dirtyPaths(ctx context.Context, dir string) int {
	out, err := hostgit.Command(ctx, dir, "status", "--porcelain=v1", "-z").Output()
	if err != nil {
		return 0
	}
	return len(strings.FieldsFunc(string(out), func(r rune) bool { return r == 0 }))
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
