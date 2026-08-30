// Package process implements the long-running process supervisor:
// the agent manages named processes (dev servers, watchers, test runners)
// through one `process` tool — start (approval-gated like any command),
// status, read, input, and stop. Output is captured into bounded per-stream
// ring buffers for paged reads, with the full log (bounded) stored in the
// evidence store when the process ends. Every process runs in its own
// process group so stop, session end, cancel, and quit terminate the full
// tree — no orphans.
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
	"syscall"
	"time"
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
}

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
// of the first spoolMax bytes kept for the evidence store.
type streamBuf struct {
	ringMax  int
	spoolMax int

	mu             sync.Mutex
	ring           []byte
	ringStart      int64 // absolute offset of ring[0]
	total          int64 // bytes ever written
	spool          []byte
	spoolTruncated bool
}

func newStreamBuf(ringMax, spoolMax int) *streamBuf {
	return &streamBuf{ringMax: ringMax, spoolMax: spoolMax}
}

func (b *streamBuf) Write(p []byte) (int, error) {
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
	return len(p), nil
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

func (b *streamBuf) spoolCopy() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
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

	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	cmd := exec.Command(filepath.Clean(sh), "-c", command)
	cmd.Dir = dir
	cmd.Env = append(env, s.env...)
	cmd.SysProcAttr = sysProcAttr()

	p := &proc{
		name:     name,
		command:  command,
		started:  time.Now(),
		stdout:   newStreamBuf(s.ringBytes, s.spoolBytes),
		stderr:   newStreamBuf(s.ringBytes, s.spoolBytes),
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

// signalGroup signals a process's whole group (never just the leader).
func signalGroup(pid int, sig syscall.Signal) {
	_ = syscall.Kill(-pid, sig)
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
	signalGroup(p.pid, syscall.SIGTERM)
	select {
	case <-p.exited:
		return
	case <-time.After(stopGrace):
	}
	signalGroup(p.pid, syscall.SIGKILL)
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
