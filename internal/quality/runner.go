package quality

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Verdict is a gate run's outcome. Blocked and cancelled are distinct from
// fail so an infrastructure problem or an interrupted run can never be
// mistaken for the checks having passed.
type Verdict string

const (
	VerdictPass      Verdict = "pass"
	VerdictFail      Verdict = "fail"
	VerdictBlocked   Verdict = "blocked"
	VerdictCancelled Verdict = "cancelled"
)

// Run ceilings.
const (
	// DefaultCheckTimeout bounds one check when the suite sets no timeout.
	DefaultCheckTimeout = 10 * time.Minute
	// MaxParallelChecks caps the configurable concurrency ceiling.
	MaxParallelChecks = 4
	// MaxCaptureBytes caps how much of one check's output is kept (head and
	// tail) and stored as evidence.
	MaxCaptureBytes = 1 << 20
	// MaxInlineBytes bounds the output excerpt a failing check contributes to
	// the formatted result; the full bounded capture lives in the evidence
	// store when one is wired.
	MaxInlineBytes = 2 << 10
)

// WrapFunc builds the containment argv for one check; allowWrite
// grants the workspace write access (suites opt in via allow_write, the
// default is read-only). A wrap error blocks the run — a check never falls
// back to running bare when containment was expected.
type WrapFunc func(argv []string, allowWrite bool) ([]string, error)

// EvidenceFunc stores one check's full bounded output and returns its
// opaque evidence id.
type EvidenceFunc func(tool string, content []byte) (string, error)

// CheckResult is one check's outcome within a run.
type CheckResult struct {
	Name       string
	Command    string
	ExitCode   int
	TimedOut   bool
	Err        string // spawn or containment failure: the check did not run to completion
	Output     string // bounded excerpt (tail) of combined stdout/stderr
	EvidenceID string
	Duration   time.Duration
}

// OK reports whether the check ran and passed.
func (c CheckResult) OK() bool { return c.Err == "" && !c.TimedOut && c.ExitCode == 0 }

// Result is one gate run's full outcome, fingerprinted against the tree it
// ran over.
type Result struct {
	Suite       string
	Verdict     Verdict
	Reason      string // blocked/cancelled: why the run has no trustworthy verdict
	Checks      []CheckResult
	Fingerprint Fingerprint
	// Trusted says the suite name resolved in the project's own config,
	// which is what makes the name safe to record. The gate tool takes its
	// suite from the model, and a name that matched nothing is text the
	// model wrote — it must never reach a store that is content-free by
	// construction.
	// See docs/capabilities/sessions-and-memory.md#whether-it-worked.
	Trusted bool
	// ChangedDuringRun marks a tree that changed while the checks ran; the
	// result is stale from birth.
	ChangedDuringRun bool
	Contained        string
	Duration         time.Duration
}

// Runner owns a session's gate runs: it loads the trusted config fresh per
// run, executes at most one run at a time, and keeps the latest result for
// the "result" action and /gate result.
type Runner struct {
	Workspace string
	// Wrap contains each check; nil runs checks bare, reported
	// honestly in the result.
	Wrap WrapFunc
	// Mechanism names the containment mechanism Wrap uses, for the result's
	// containment line.
	Mechanism string
	// Evidence stores full check output; nil keeps only the inline excerpt.
	Evidence EvidenceFunc
	// Observe reports one completed run's verdict to the session record,
	// with the suite when the name resolved in the trusted config and an
	// empty string when it did not; nil records nothing. It is set before
	// the first run and never reassigned, which is what makes it safe to
	// read from the goroutine a background run finishes on.
	//
	// It hangs off finish, the one place a run of either kind lands, so a
	// background run started by hand is recorded on the same footing as one
	// the model asked for — and no path can produce a verdict the record
	// does not see.
	//
	// The gate is the only reading of whether the work was right that
	// nobody has to remember to give.
	// See docs/capabilities/sessions-and-memory.md#whether-it-worked.
	Observe func(suite string, v Verdict)

	mu      sync.Mutex
	running string // suite of the in-flight run, "" when idle
	last    *Result
}

// Run executes a suite to completion and returns its result. Only the
// returned error means "no result": a second run while one is in flight.
func (r *Runner) Run(ctx context.Context, suite string) (*Result, error) {
	suite = orDefault(suite)
	if err := r.begin(suite); err != nil {
		return nil, err
	}
	res := r.execute(ctx, suite)
	r.finish(res)
	return res, nil
}

// Start launches a suite in the background for /gate run and returns the
// status line to show; /gate result reports the verdict once it lands.
func (r *Runner) Start(suite string) string {
	suite = orDefault(suite)
	if err := r.begin(suite); err != nil {
		return "Error: " + err.Error()
	}
	go func() {
		r.finish(r.execute(context.Background(), suite))
	}()
	return fmt.Sprintf("Running quality gate suite %q in the background — check /gate result for the verdict.", suite)
}

// Status reports the in-flight run or the latest result, re-fingerprinting
// the tree so a result over a changed tree reports stale.
func (r *Runner) Status() string {
	r.mu.Lock()
	running, last := r.running, r.last
	r.mu.Unlock()
	if running != "" {
		return fmt.Sprintf("A gate run (suite %q) is in progress; ask again shortly.", running)
	}
	if last == nil {
		return "No gate runs this session yet. Suites are defined in " + ConfigRelPath + "."
	}
	return last.Format(TakeFingerprint(r.Workspace))
}

func orDefault(suite string) string {
	if strings.TrimSpace(suite) == "" {
		return DefaultSuite
	}
	return suite
}

func (r *Runner) begin(suite string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running != "" {
		return fmt.Errorf("a gate run (suite %q) is already in progress", r.running)
	}
	r.running = suite
	return nil
}

func (r *Runner) finish(res *Result) {
	r.mu.Lock()
	r.last = res
	r.running = ""
	r.mu.Unlock()
	// Outside the lock: recording is somebody else's write, and holding the
	// runner's lock across it would let a slow store block the next run.
	if r.Observe != nil {
		// A name that did not resolve in the trusted config is not handed
		// over at all. The result still carries it, because the caller
		// asked for it by that name and the message back has to say so;
		// the record gets nothing rather than the model's own text.
		suite := res.Suite
		if !res.Trusted {
			suite = ""
		}
		r.Observe(suite, res.Verdict)
	}
}

// execute runs one suite: config loaded fresh, every executable and wrap
// resolved before anything runs (so a broken setup blocks instead of half
// running), checks bounded by time/output/concurrency ceilings.
func (r *Runner) execute(ctx context.Context, suiteName string) *Result {
	start := time.Now()
	res := &Result{Suite: suiteName}
	blocked := func(reason string) *Result {
		res.Verdict = VerdictBlocked
		res.Reason = reason
		res.Fingerprint = TakeFingerprint(r.Workspace)
		res.Duration = time.Since(start)
		return res
	}

	cfg, err := LoadConfig(r.Workspace)
	if err != nil {
		if os.IsNotExist(err) {
			return blocked("no quality config: define named suites in " + ConfigRelPath)
		}
		return blocked("invalid quality config: " + err.Error())
	}
	suite, ok := cfg.Suites[suiteName]
	if !ok {
		return blocked(fmt.Sprintf("unknown suite %q (available: %s)", suiteName, strings.Join(cfg.SuiteNames(), ", ")))
	}
	res.Trusted = true
	res.Contained = r.containDescription(suite.AllowWrite)

	timeout := DefaultCheckTimeout
	if suite.TimeoutSeconds > 0 {
		timeout = time.Duration(suite.TimeoutSeconds) * time.Second
	}

	argvs := make([][]string, len(suite.Checks))
	for i, check := range suite.Checks {
		path, err := resolveExe(r.Workspace, check.Exe)
		if err != nil {
			return blocked(fmt.Sprintf("check %q: %v", check.Name, err))
		}
		argv := append([]string{path}, check.Args...)
		if r.Wrap != nil {
			if argv, err = r.Wrap(argv, suite.AllowWrite); err != nil {
				return blocked(fmt.Sprintf("check %q: containment failed: %v", check.Name, err))
			}
		}
		argvs[i] = argv
	}

	before := TakeFingerprint(r.Workspace)
	res.Checks = make([]CheckResult, len(suite.Checks))
	sem := make(chan struct{}, cfg.effectiveParallel())
	var wg sync.WaitGroup
	for i, check := range suite.Checks {
		wg.Add(1)
		go func(i int, check Check) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res.Checks[i] = r.runCheck(ctx, suiteName, check, argvs[i], timeout)
		}(i, check)
	}
	wg.Wait()

	res.Fingerprint = TakeFingerprint(r.Workspace)
	res.ChangedDuringRun = before != res.Fingerprint
	res.Duration = time.Since(start)

	switch {
	case ctx.Err() != nil:
		res.Verdict = VerdictCancelled
		res.Reason = "the run was cancelled before completing"
	case anyErrored(res.Checks):
		res.Verdict = VerdictBlocked
		res.Reason = firstError(res.Checks)
	case anyFailed(res.Checks):
		res.Verdict = VerdictFail
	default:
		res.Verdict = VerdictPass
	}
	return res
}

// runCheck spawns one check with its timeout and output ceilings, storing the
// bounded capture as evidence and keeping an inline tail excerpt.
func (r *Runner) runCheck(ctx context.Context, suite string, check Check, argv []string, timeout time.Duration) CheckResult {
	cr := CheckResult{
		Name:    check.Name,
		Command: strings.Join(append([]string{check.Exe}, check.Args...), " "),
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	out := &boundedWriter{limit: MaxCaptureBytes}
	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
	cmd.Dir = r.Workspace
	cmd.Stdout = out
	cmd.Stderr = out
	// A killed check can leave children holding the output pipes; give up on
	// them after a grace period instead of wedging the run.
	cmd.WaitDelay = 2 * time.Second

	start := time.Now()
	err := cmd.Run()
	cr.Duration = time.Since(start)

	switch {
	case err == nil:
	case errors.Is(cctx.Err(), context.DeadlineExceeded):
		cr.TimedOut = true
	default:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			cr.ExitCode = exitErr.ExitCode()
		} else {
			cr.Err = err.Error() // spawn failure: the check never ran
		}
	}

	captured := out.String()
	if r.Evidence != nil && captured != "" {
		if id, err := r.Evidence(ToolName+":"+suite+":"+check.Name, []byte(captured)); err == nil {
			cr.EvidenceID = id
		}
	}
	cr.Output = tailExcerpt(captured, MaxInlineBytes)
	return cr
}

func anyErrored(checks []CheckResult) bool {
	for _, c := range checks {
		if c.Err != "" {
			return true
		}
	}
	return false
}

func firstError(checks []CheckResult) string {
	for _, c := range checks {
		if c.Err != "" {
			return fmt.Sprintf("check %q did not run: %s", c.Name, c.Err)
		}
	}
	return ""
}

func anyFailed(checks []CheckResult) bool {
	for _, c := range checks {
		if !c.OK() {
			return true
		}
	}
	return false
}

func (r *Runner) containDescription(allowWrite bool) string {
	if r.Wrap == nil {
		return "unconfined (no containment mechanism available)"
	}
	mech := r.Mechanism
	if mech == "" {
		mech = "contained"
	}
	if allowWrite {
		return mech + ", workspace writable (suite allow_write)"
	}
	return mech + ", read-only workspace"
}

// resolveExe resolves a check's executable: a bare name against PATH, a path
// against the workspace root. Resolution happens before any check runs, so a
// missing binary blocks the run cleanly.
func resolveExe(workspace, exe string) (string, error) {
	if strings.ContainsRune(exe, os.PathSeparator) {
		path := exe
		if !filepath.IsAbs(path) {
			path = filepath.Join(workspace, path)
		}
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("executable %s not found", path)
		}
		return path, nil
	}
	path, err := exec.LookPath(exe)
	if err != nil {
		return "", fmt.Errorf("executable %q not found on PATH", exe)
	}
	return path, nil
}

// tailExcerpt keeps the last max bytes of s, trimmed to whole lines when
// possible.
func tailExcerpt(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := s[len(s)-max:]
	if i := strings.IndexByte(cut, '\n'); i >= 0 && i < len(cut)-1 {
		cut = cut[i+1:]
	}
	return cut
}

// boundedWriter keeps the head and a rolling tail of a stream within a byte
// budget, so a chatty check cannot balloon memory while the ends — where
// build errors and test summaries live — survive.
type boundedWriter struct {
	mu    sync.Mutex
	limit int
	head  []byte
	tail  []byte
	total int64
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	w.total += int64(n)
	headMax := w.limit / 4
	if len(w.head) < headMax {
		take := min(headMax-len(w.head), len(p))
		w.head = append(w.head, p[:take]...)
		p = p[take:]
	}
	if len(p) > 0 {
		tailMax := w.limit - headMax
		w.tail = append(w.tail, p...)
		if len(w.tail) > 2*tailMax {
			w.tail = append(w.tail[:0], w.tail[len(w.tail)-tailMax:]...)
		}
	}
	return n, nil
}

func (w *boundedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	tail := w.tail
	if tailMax := w.limit - w.limit/4; len(tail) > tailMax {
		tail = tail[len(tail)-tailMax:]
	}
	elided := w.total - int64(len(w.head)+len(tail))
	if elided <= 0 {
		return string(w.head) + string(tail)
	}
	return fmt.Sprintf("%s\n… (%d bytes elided) …\n%s", w.head, elided, tail)
}
