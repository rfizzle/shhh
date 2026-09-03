// Package process implements the long-running process supervisor:
// the agent manages named processes (dev servers, watchers, test runners)
// through one `process` tool — start (approval-gated like any command, and
// contained by the same mechanism), status, read, input, and stop. Output is
// captured into bounded per-stream ring buffers for paged reads, with the
// full log (bounded) stored in the evidence store when the process ends.
// Every process runs in its own process group so stop, session end, cancel,
// and quit terminate the full tree — no orphans.
package process

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/shell"
)

const (
	// MaxProcesses bounds how many processes (running or exited) the
	// supervisor tracks at once; a replaced exited entry frees its slot.
	MaxProcesses = 8

	// RingBytes bounds each stream's in-memory ring buffer; reads page over
	// this window, and bytes evicted from it survive only in the spool.
	RingBytes = 256 << 10

	// MaxSpoolBytes bounds the full-log spool kept for the evidence store
	// (matching evidence.MaxStoredBytes); output past it is dropped and the
	// stored log marked truncated.
	MaxSpoolBytes = 4 << 20

	// DefaultReadBytes and MaxReadBytes clamp one read action's page.
	DefaultReadBytes = 4096
	MaxReadBytes     = 16 << 10

	// startProbe is how long start waits to catch a command that dies
	// immediately, so the model sees the failure in the start result.
	startProbe = 700 * time.Millisecond

	// stopGrace is how long a stopped process group gets to exit after
	// SIGTERM before it is SIGKILLed.
	stopGrace = 2 * time.Second
)

var (
	nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	envRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// StoreFunc persists one full process log into the evidence store and
// returns its opaque id; nil disables retention (ring buffers only).
type StoreFunc func(tool string, content []byte) (string, error)

// Supervisor owns one session's named long-running processes. Close must run
// when the session ends so no process tree outlives it.
type Supervisor struct {
	root  string
	store StoreFunc

	// ringBytes/spoolBytes are the per-stream capture bounds, and probe the
	// immediate-exit wait after a start; fields so tests can shrink them.
	ringBytes  int
	spoolBytes int
	probe      time.Duration

	mu     sync.Mutex
	procs  map[string]*proc
	closed bool
	// env is the session's NAME=value pairs every process gets after the
	// caller's own, so the model's `env` argument cannot shadow a secret.
	env []string
	// scrub rewrites captured output as it arrives; nil captures it raw.
	// Each stream takes a copy at start, so changing it affects processes
	// started afterwards.
	scrub func(string) string
	// contain is the mechanism a start runs under; the zero value is a
	// session where nothing contains commands either.
	contain Containment
}

// Containment is the session's containment mechanism as the supervisor needs
// it: the name a refusal states, and the wrap that turns a command's argv
// into the argv that runs it contained.
type Containment struct {
	// Mechanism names what is doing the containing — the word every surface
	// reporting this session says, and the word a refusal has to name so the
	// reader knows which thing said no.
	Mechanism string
	// Wrap returns argv wrapped to run in dir under the mechanism. The
	// directory travels with the argv rather than being left to the spawn's
	// own: a mechanism that chdirs would otherwise put the process in
	// shhh's working directory, and the cwd the start asked for — already
	// contained to the workspace — would be silently dropped.
	Wrap func(dir string, argv []string) ([]string, error)
}

// inForce reports whether this containment can actually wrap a start. A
// mechanism named with no wrap behind it would be a session that reports
// containment and does not have it, so the pair decides together.
func (c Containment) inForce() bool { return c.Mechanism != "" && c.Wrap != nil }

// SetEnv sets the session pairs every started process carries beyond
// PATH, HOME and the model's own extras. The process tool builds a
// deliberately bare environment, so the secrets a foreground command sees
// through the inherited one have to be handed to it here.
// See docs/capabilities/secrets.md#a-secret-is-an-environment-variable.
func (s *Supervisor) SetEnv(env []string) {
	s.mu.Lock()
	s.env = append([]string(nil), env...)
	s.mu.Unlock()
}

// SetScrub installs the rewrite every byte of captured output goes through
// on its way into the buffers. A process is handed the session's secrets as
// environment variables, so its output is where one is most likely to be
// printed, and the spool is a copy that reaches the evidence store and
// outlives the process by a week. Scrubbing on the way in rather than on
// the way out is what keeps the two agreeing: a read pages the ring by
// absolute stream offset, and a scrub applied at read time would return
// bytes that are not the ones the offsets count.
//
// It is a function and not a vault so this package needs to know nothing
// about what a secret is; nil captures raw, which is a session with no
// secrets.
// See docs/capabilities/secrets.md#the-value-is-scrubbed-at-every-door.
func (s *Supervisor) SetScrub(scrub func(string) string) {
	s.mu.Lock()
	s.scrub = scrub
	s.mu.Unlock()
}

// SetContainment installs the mechanism every process started afterwards
// runs under. A start is a second command path — it spawns exactly what
// execute_command would have spawned, and then outlives the call that made
// it — so it takes the session's wrap for the same reasons the ordinary path
// does, and a start the wrap refuses is refused rather than run bare.
//
// It is a wrap and not a policy so this package needs to know nothing about
// mechanisms, profiles or masks; the zero value is a session where nothing
// contains a command either.
// See docs/capabilities/containment.md#a-started-process-is-contained-too.
func (s *Supervisor) SetContainment(c Containment) {
	s.mu.Lock()
	s.contain = c
	s.mu.Unlock()
}

// Contained names the mechanism started processes run under, empty when none
// is in force. The surfaces that report containment ask the supervisor
// rather than the runner: the two paths are wired from one policy but are
// not the same code, and a card reading the other one would be describing a
// process that is not the one about to run.
// containment copies the wrap out from under the lock, so a start can
// resolve a policy against the filesystem without holding the one mutex
// every status, read and stop in the session goes through.
func (s *Supervisor) containment() Containment {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.contain
}

func (s *Supervisor) Contained() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.contain.inForce() {
		return ""
	}
	return s.contain.Mechanism
}

// Running counts the processes this session started that are still alive. A
// command is over by the time its result is read; a started process is what
// is still running when a report is asked for, so it is what a report of
// what containment holds has to count.
func (s *Supervisor) Running() int {
	s.mu.Lock()
	procs := make([]*proc, 0, len(s.procs))
	for _, p := range s.procs {
		procs = append(procs, p)
	}
	s.mu.Unlock()
	n := 0
	for _, p := range procs {
		if !p.isDone() {
			n++
		}
	}
	return n
}

// New builds a supervisor whose processes are contained to root (cwd-wise).
// It returns an error when root cannot be resolved: no root means no
// containment check, so no supervisor.
func New(root string, store StoreFunc) (*Supervisor, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve workspace root: %w", err)
	}
	return &Supervisor{
		root:       resolved,
		store:      store,
		ringBytes:  RingBytes,
		spoolBytes: MaxSpoolBytes,
		probe:      startProbe,
		procs:      map[string]*proc{},
	}, nil
}

// proc is one supervised process.
type proc struct {
	name    string
	command string
	pid     int
	started time.Time

	stdin  io.WriteCloser
	stdout *streamBuf
	stderr *streamBuf

	// exited is closed by the reaper once the process is gone and its exit
	// state below is recorded.
	exited chan struct{}

	mu       sync.Mutex
	done     bool
	exitCode int
	ended    time.Time
	// evidence maps stream name to the stored full-log id, once stored.
	evidence map[string]string
}

func (p *proc) isDone() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.done
}

// streamBuf captures one output stream: a ring of the most recent ringMax
// bytes for paged reads (offsets are absolute stream offsets), plus a spool
// of the first spoolMax bytes kept for the evidence store. Both hold what
// scrub returned, so the two never disagree about what the process printed.
type streamBuf struct {
	ringMax  int
	spoolMax int
	scrub    func(string) string

	mu             sync.Mutex
	ring           []byte
	ringStart      int64 // absolute offset of ring[0]
	total          int64 // bytes ever written
	spool          []byte
	spoolTruncated bool
}

func newStreamBuf(ringMax, spoolMax int, scrub func(string) string) *streamBuf {
	return &streamBuf{ringMax: ringMax, spoolMax: spoolMax, scrub: scrub}
}

func (b *streamBuf) Write(p []byte) (int, error) {
	// What the caller is told it wrote is always what it handed over: this
	// is os/exec's copier, which reads a short count as a failed write and
	// tears the pipe down, and a session with secrets would then be one
	// that captures no process output at all.
	wrote := len(p)
	if b.scrub != nil {
		// One chunk at a time, so a value straddling two writes is left to
		// the fragment rule rather than the whole-value one — the same
		// trade the live command tail on screen already makes.
		p = []byte(b.scrub(string(p)))
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total += int64(len(p))
	if keep := b.spoolMax - len(b.spool); keep > 0 {
		if len(p) > keep {
			b.spool = append(b.spool, p[:keep]...)
		} else {
			b.spool = append(b.spool, p...)
		}
	}
	b.spoolTruncated = b.total > int64(len(b.spool))
	b.ring = append(b.ring, p...)
	if over := len(b.ring) - b.ringMax; over > 0 {
		b.ring = append(b.ring[:0], b.ring[over:]...)
		b.ringStart += int64(over)
	}
	return wrote, nil
}

// readAt returns the ring bytes at absolute offset (clamped into the ring
// window), the clamped start offset, the total stream size, and whether bytes
// before the window were evicted from memory.
func (b *streamBuf) readAt(offset int64, limit int) (data []byte, start, total int64, evicted bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	evicted = offset < b.ringStart
	if evicted {
		offset = b.ringStart
	}
	if offset > b.total {
		offset = b.total
	}
	i := offset - b.ringStart
	end := i + int64(limit)
	if end > int64(len(b.ring)) {
		end = int64(len(b.ring))
	}
	out := make([]byte, end-i)
	copy(out, b.ring[i:end])
	return out, offset, b.total, evicted
}

// tailOffset is the absolute offset that puts a limit-sized page at the
// stream's end.
func (b *streamBuf) tailOffset(limit int) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	off := b.total - int64(limit)
	if off < b.ringStart {
		off = b.ringStart
	}
	if off < 0 {
		off = 0
	}
	return off
}

func (b *streamBuf) size() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total
}

// spoolCopy is the full log on its way to the evidence store, and the last
// point before it becomes a file that outlives the session by a week. The
// scrub runs over it once more here because the writes it arrived in were
// whatever the pipe delivered: a value split across two of them is only
// caught by the fragment rule, and this is the one copy where the seven
// bytes that rule can leave behind are worth a second pass. The ring gets
// no second pass — its offsets are the stream's, and rewriting it after the
// fact would move bytes a read has already been told the position of.
func (b *streamBuf) spoolCopy() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.scrub != nil {
		return []byte(b.scrub(string(b.spool))), b.spoolTruncated
	}
	out := make([]byte, len(b.spool))
	copy(out, b.spool)
	return out, b.spoolTruncated
}

// resolveCwd resolves a start cwd against the workspace root and confirms it
// stays inside it, symlinks resolved. Empty means the root itself.
func (s *Supervisor) resolveCwd(p string) (string, error) {
	if p == "" || p == "." {
		return s.root, nil
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(s.root, p)
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("cannot access cwd: %w", err)
	}
	if resolved != s.root && !strings.HasPrefix(resolved, s.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("cwd %q is outside the workspace", p)
	}
	return resolved, nil
}

// buildEnv is the restricted child environment: PATH and HOME from the
// session, plus explicitly passed vars — which can never shadow those two.
func buildEnv(extra map[string]string) ([]string, error) {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !envRe.MatchString(k) {
			return nil, fmt.Errorf("invalid environment variable name %q", k)
		}
		if k == "PATH" || k == "HOME" {
			return nil, fmt.Errorf("environment variable %s cannot be overridden", k)
		}
		env = append(env, k+"="+extra[k])
	}
	return env, nil
}

// start spawns a named process in its own process group and probes briefly
// for an immediate exit so the model sees instant failures.
func (s *Supervisor) start(name, command, cwd string, extraEnv map[string]string) (string, error) {
	if !nameRe.MatchString(name) {
		return "", fmt.Errorf("invalid process name %q (letters, digits, . _ -, max 64 chars)", name)
	}
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("command is required")
	}
	dir, err := s.resolveCwd(cwd)
	if err != nil {
		return "", err
	}
	env, err := buildEnv(extraEnv)
	if err != nil {
		return "", err
	}

	// The wrap runs outside the lock, because resolving a policy stats the
	// filesystem and the supervisor's one mutex is what every status, read
	// and stop in the session waits on. It also runs before anything is
	// mutated, so a refused start leaves the supervisor exactly as it found
	// it — including the exited entry a restart under the same name is
	// about to replace.
	argv := shell.Execution().Argv(command)
	if contain := s.containment(); contain.inForce() {
		wrapped, err := contain.Wrap(dir, argv)
		if err != nil {
			// Refused, never started bare. Every surface in this session
			// says the mechanism is containing what the assistant runs, and
			// one process quietly outside it makes all of them wrong for as
			// long as it lives.
			// See docs/capabilities/containment.md#what-is-reported-is-what-is-in-force.
			return "", fmt.Errorf("cannot start %q under %s containment: %w (it was not started)",
				name, contain.Mechanism, err)
		}
		argv = wrapped
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", fmt.Errorf("the process supervisor is shut down")
	}
	if existing, ok := s.procs[name]; ok {
		if !existing.isDone() {
			s.mu.Unlock()
			return "", fmt.Errorf("a process named %q is already running (stop it first, or pick another name)", name)
		}
		delete(s.procs, name) // replacing an exited entry frees its slot
	}
	if len(s.procs) >= MaxProcesses {
		s.mu.Unlock()
		return "", fmt.Errorf("too many processes (max %d); stop one first", MaxProcesses)
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(env, s.env...)
	cmd.SysProcAttr = sysProcAttr()

	p := &proc{
		name:     name,
		command:  command,
		started:  time.Now(),
		stdout:   newStreamBuf(s.ringBytes, s.spoolBytes, s.scrub),
		stderr:   newStreamBuf(s.ringBytes, s.spoolBytes, s.scrub),
		exited:   make(chan struct{}),
		evidence: map[string]string{},
	}
	cmd.Stdout = p.stdout
	cmd.Stderr = p.stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		s.mu.Unlock()
		return "", fmt.Errorf("cannot open stdin: %w", err)
	}
	p.stdin = stdin

	if err := cmd.Start(); err != nil {
		s.mu.Unlock()
		_ = stdin.Close()
		return "", fmt.Errorf("cannot start process: %w", err)
	}
	p.pid = cmd.Process.Pid
	s.procs[name] = p
	s.mu.Unlock()

	go s.reap(p, cmd)

	select {
	case <-p.exited:
	case <-time.After(s.probe):
	}
	return s.statusOf(p), nil
}

// reap waits for a process, records its exit state, and stores the full log
// spools as evidence.
func (s *Supervisor) reap(p *proc, cmd *exec.Cmd) {
	err := cmd.Wait()
	code := 0
	if err != nil {
		code = -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		}
	}
	_ = p.stdin.Close()

	evidence := map[string]string{}
	if s.store != nil {
		for stream, buf := range map[string]*streamBuf{"stdout": p.stdout, "stderr": p.stderr} {
			spool, _ := buf.spoolCopy()
			if len(spool) == 0 {
				continue
			}
			if id, err := s.store("process:"+p.name+":"+stream, spool); err == nil {
				evidence[stream] = id
			}
		}
	}

	p.mu.Lock()
	p.done = true
	p.exitCode = code
	p.ended = time.Now()
	p.evidence = evidence
	p.mu.Unlock()
	close(p.exited)
}

// stop terminates a process tree: SIGTERM to the group, SIGKILL after the
// grace period. Stopping an already-exited process just reports its state.
func (s *Supervisor) stop(name string) (string, error) {
	p, err := s.get(name)
	if err != nil {
		return "", err
	}
	s.terminate(p)
	return s.statusOf(p), nil
}

// terminate runs the TERM → grace → KILL sequence and waits for the reaper.
func (s *Supervisor) terminate(p *proc) {
	if p.isDone() {
		return
	}
	signalGroup(p.pid, signalTerm)
	select {
	case <-p.exited:
		return
	case <-time.After(stopGrace):
	}
	signalGroup(p.pid, signalKill)
	<-p.exited
}

// input writes text to a running process's stdin, verbatim.
func (s *Supervisor) input(name, text string) (string, error) {
	p, err := s.get(name)
	if err != nil {
		return "", err
	}
	if p.isDone() {
		return "", fmt.Errorf("process %q has exited; its stdin is closed", name)
	}
	if text == "" {
		return "", fmt.Errorf("text is required (include a trailing newline to submit a line)")
	}
	if _, err := io.WriteString(p.stdin, text); err != nil {
		return "", fmt.Errorf("cannot write to stdin: %w", err)
	}
	return fmt.Sprintf("wrote %d bytes to %s stdin", len(text), name), nil
}

// read returns one byte-clamped page of a stream. A negative offset (or an
// omitted one) means the tail.
func (s *Supervisor) read(name, stream string, offset int64, limit int) (string, error) {
	p, err := s.get(name)
	if err != nil {
		return "", err
	}
	var buf *streamBuf
	switch stream {
	case "", "stdout":
		stream = "stdout"
		buf = p.stdout
	case "stderr":
		buf = p.stderr
	default:
		return "", fmt.Errorf("unknown stream %q (valid: stdout, stderr)", stream)
	}
	if limit <= 0 {
		limit = DefaultReadBytes
	}
	if limit > MaxReadBytes {
		limit = MaxReadBytes
	}
	if offset < 0 {
		offset = buf.tailOffset(limit)
	}
	data, start, total, evicted := buf.readAt(offset, limit)

	var b strings.Builder
	end := start + int64(len(data))
	fmt.Fprintf(&b, "process %s (%s): bytes %d-%d of %d", name, stream, start, end, total)
	if p.isDone() {
		b.WriteString(" (exited)")
	} else {
		b.WriteString(" (running)")
	}
	b.WriteString("\n")
	if evicted {
		fmt.Fprintf(&b, "[older output was evicted from the %d-byte buffer; the full log (bounded) is stored as evidence once the process ends]\n", buf.ringMax)
	}
	b.Write(data)
	if end < total {
		fmt.Fprintf(&b, "\n… (more output: read again with offset=%d)", end)
	}
	return b.String(), nil
}

// status reports one process, or all of them when name is empty.
func (s *Supervisor) status(name string) (string, error) {
	if name == "" {
		return s.List(), nil
	}
	p, err := s.get(name)
	if err != nil {
		return "", err
	}
	return s.statusOf(p), nil
}

// statusOf renders one process's full status block.
func (s *Supervisor) statusOf(p *proc) string {
	p.mu.Lock()
	done, code, ended, evidence := p.done, p.exitCode, p.ended, p.evidence
	p.mu.Unlock()

	var b strings.Builder
	if done {
		fmt.Fprintf(&b, "process %s: exited (code %d) after %s\n", p.name, code, roundDuration(ended.Sub(p.started)))
	} else {
		fmt.Fprintf(&b, "process %s: running (pid %d, up %s)\n", p.name, p.pid, roundDuration(time.Since(p.started)))
	}
	fmt.Fprintf(&b, "  command: %s\n", firstLine(p.command))
	fmt.Fprintf(&b, "  stdout: %d bytes captured", p.stdout.size())
	if id, ok := evidence["stdout"]; ok {
		fmt.Fprintf(&b, " (full log: evidence %s)", id)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "  stderr: %d bytes captured", p.stderr.size())
	if id, ok := evidence["stderr"]; ok {
		fmt.Fprintf(&b, " (full log: evidence %s)", id)
	}
	return b.String()
}

// List renders the /ps view: every process the session owns.
func (s *Supervisor) List() string {
	s.mu.Lock()
	procs := make([]*proc, 0, len(s.procs))
	for _, p := range s.procs {
		procs = append(procs, p)
	}
	s.mu.Unlock()
	if len(procs) == 0 {
		return "No processes started this session."
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].started.Before(procs[j].started) })

	var b strings.Builder
	b.WriteString("Processes this session owns:\n")
	for _, p := range procs {
		p.mu.Lock()
		done, code, ended := p.done, p.exitCode, p.ended
		p.mu.Unlock()
		state := fmt.Sprintf("running (pid %d, up %s)", p.pid, roundDuration(time.Since(p.started)))
		if done {
			state = fmt.Sprintf("exited (code %d) after %s", code, roundDuration(ended.Sub(p.started)))
		}
		fmt.Fprintf(&b, "  %-16s %-32s %s\n", p.name, state, firstLine(p.command))
	}
	b.WriteString("Manage with the process tool; stop everything by ending the session.")
	return b.String()
}

// Close terminates every owned process tree. It is how session end, cancel,
// and quit guarantee no orphans; the supervisor is unusable afterwards.
func (s *Supervisor) Close() {
	s.mu.Lock()
	s.closed = true
	procs := make([]*proc, 0, len(s.procs))
	for _, p := range s.procs {
		procs = append(procs, p)
	}
	s.mu.Unlock()

	var wg sync.WaitGroup
	for _, p := range procs {
		if p.isDone() {
			continue
		}
		wg.Add(1)
		go func(p *proc) {
			defer wg.Done()
			s.terminate(p)
		}(p)
	}
	wg.Wait()
}

// get looks up a named process.
func (s *Supervisor) get(name string) (*proc, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.procs[name]
	if !ok {
		return nil, fmt.Errorf("no process named %q this session", name)
	}
	return p, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

func roundDuration(d time.Duration) time.Duration {
	if d >= time.Minute {
		return d.Round(time.Second)
	}
	return d.Round(10 * time.Millisecond)
}
