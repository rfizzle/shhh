// Package structural exposes best-in-class external code tools — fd,
// ast-grep, sd, tokei, jaq, and git — as first-class agent tools, so the
// model searches structurally, reads history, and previews transforms instead
// of improvising shell pipelines. Each tool is registered only when its binary
// is found on PATH, and git additionally only inside a repository.
//
// The safety invariants are ported from pi tool-runtime, several of them
// empirically load-bearing there:
//
//   - Injection-safe argv construction throughout: model-supplied values ride
//     attached as --flag=value or after a literal "--" delimiter, so a leading
//     "-" can never become an option (sd otherwise silently consumes a
//     flag-shaped pattern as a flag value and blocks on stdin).
//   - Search paths are resolved against the workspace root, symlinks and all,
//     and containment-checked before any spawn.
//   - No tool in this package writes a file, ever: sd always runs with
//     --preview (it writes in place by default), ast-grep never sees
//     -U/--update-all, jaq's file-reading and in-place flags
//     (-L, -f/--from-file, --slurpfile, --rawfile, -i/--in-place) are not in
//     its vocabulary, and git reaches five reading verbs with no field a
//     sixth could arrive in. Rewrites and replacements return preview diffs
//     the model applies via edit_file through the approval queue.
//   - Every spawn has a timeout and output bounds; a missing binary, timeout,
//     or cancellation degrades to a clean tool error, never a hang.
package structural

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
)

// Model-facing tool names.
const (
	FdToolName      = "fd"
	AstGrepToolName = "ast_grep"
	SdToolName      = "sd"
	TokeiToolName   = "tokei"
	JaqToolName     = "jaq"
	GitToolName     = "git"
)

const (
	// SpawnTimeout bounds one external tool run.
	SpawnTimeout = 30 * time.Second

	// MaxOutputBytes caps captured stdout; the process is killed once the cap
	// is hit, and the result carries a truncation notice. Results this large
	// are further reduced by the evidence pipeline when it is active.
	MaxOutputBytes = 64 << 10

	// MaxStderrBytes caps captured stderr embedded in error results.
	MaxStderrBytes = 4 << 10

	// MaxFindResults caps how many entries the fd tool asks for.
	MaxFindResults = 500
)

// lookPath resolves a binary on PATH; a variable so tests can control which
// binaries exist.
var lookPath = func(name string) (string, bool) {
	path, err := exec.LookPath(name)
	return path, err == nil
}

// binaryNames maps each tool to the binary it wraps.
var binaryNames = map[string]string{
	FdToolName:      "fd",
	AstGrepToolName: "ast-grep",
	SdToolName:      "sd",
	TokeiToolName:   "tokei",
	JaqToolName:     "jaq",
	GitToolName:     "git",
}

// toolOrder fixes the registration order of the wrapped tools. git is not in
// it: the five here are optional installs, and git is registered by a
// different question — whether this is a repository — which is why
// Definitions and ToolBinaries treat it separately.
var toolOrder = []string{FdToolName, AstGrepToolName, SdToolName, TokeiToolName, JaqToolName}

// ToolBinaries are the binaries the wrapped tools need, in registration
// order. `shhh doctor` reads them to say which of the five this machine has
// ; nothing else needs the list, because every other caller asks a
// built toolset what it found rather than what it looked for. git is absent
// deliberately: doctor already reports the repository, and listing git among
// the optional installs would report a missing one as an optional tool the
// user could go and install.
func ToolBinaries() []string {
	out := make([]string, 0, len(toolOrder))
	for _, name := range toolOrder {
		out = append(out, binaryNames[name])
	}
	return out
}

// Toolset is the per-session set of wrapped external tools: the workspace
// root every path argument is contained to, and the binaries that were
// actually found on PATH.
type Toolset struct {
	root    string
	bins    map[string]string
	timeout time.Duration
}

// Detect probes PATH for the wrapped binaries and returns the session
// toolset, rooted at the current directory. It returns nil when the workspace
// root cannot be established (no root means no containment check, so no
// tools).
func Detect() *Toolset {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	return NewToolset(cwd)
}

// NewToolset builds a toolset rooted at root, probing PATH for each binary.
// It returns nil when root cannot be resolved.
func NewToolset(root string) *Toolset {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil
	}
	t := &Toolset{root: resolved, bins: map[string]string{}, timeout: SpawnTimeout}
	for _, tool := range toolOrder {
		if path, ok := lookPath(binaryNames[tool]); ok {
			t.bins[tool] = path
		}
	}
	// The history tool needs a history. Outside a repository every verb it
	// has would answer "not a git repository", so it is not registered at
	// all rather than registered and useless.
	if path, ok := lookPath(binaryNames[GitToolName]); ok && insideRepo(resolved) {
		t.bins[GitToolName] = path
	}
	return t
}

// insideRepo reports whether root is inside a git working tree; a variable so
// tests can decide without building one.
var insideRepo = func(root string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), repoProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// repoProbeTimeout bounds the one probe NewToolset makes. It is short because
// the probe runs at session start, where a git hung on a slow network mount
// would otherwise hold the whole startup.
const repoProbeTimeout = 5 * time.Second

// Definitions returns the provider tool definitions to register: only the
// tools whose binaries were found.
func (t *Toolset) Definitions() []provider.Tool {
	var defs []provider.Tool
	for _, name := range toolOrder {
		if _, ok := t.bins[name]; !ok {
			continue
		}
		switch name {
		case FdToolName:
			defs = append(defs, fdTool)
		case AstGrepToolName:
			defs = append(defs, astGrepTool)
		case SdToolName:
			defs = append(defs, sdTool)
		case TokeiToolName:
			defs = append(defs, tokeiTool)
		case JaqToolName:
			defs = append(defs, jaqTool)
		}
	}
	if _, ok := t.bins[GitToolName]; ok {
		defs = append(defs, gitTool)
	}
	return defs
}

// Has reports whether name is a structural tool this session registered.
func (t *Toolset) Has(name string) bool {
	_, ok := t.bins[name]
	return ok
}

// Execute dispatches a structural tool call.
func (t *Toolset) Execute(name string, args json.RawMessage) (string, error) {
	if _, ok := t.bins[name]; !ok {
		if name == GitToolName {
			return "", fmt.Errorf("git is not available: this workspace is not inside a git repository, or the %q binary was not found on PATH", binaryNames[name])
		}
		if _, known := binaryNames[name]; known {
			return "", fmt.Errorf("%s is not available: the %q binary was not found on PATH", name, binaryNames[name])
		}
		return "", fmt.Errorf("unknown structural tool: %s", name)
	}
	switch name {
	case FdToolName:
		return t.executeFd(args)
	case AstGrepToolName:
		return t.executeAstGrep(args)
	case SdToolName:
		return t.executeSd(args)
	case TokeiToolName:
		return t.executeTokei(args)
	case JaqToolName:
		return t.executeJaq(args)
	case GitToolName:
		return t.executeGit(args)
	}
	return "", fmt.Errorf("unknown structural tool: %s", name)
}

// WrapExecutor returns an executor that dispatches structural tools and hands
// everything else to next.
func (t *Toolset) WrapExecutor(next func(name string, args json.RawMessage) (string, error)) func(string, json.RawMessage) (string, error) {
	return func(name string, args json.RawMessage) (string, error) {
		if t.Has(name) {
			return t.Execute(name, args)
		}
		return next(name, args)
	}
}

// resolvePath resolves a model-supplied path against the workspace root and
// confirms it stays inside it, symlinks resolved, before any spawn sees it.
// An empty path means the root itself.
func (t *Toolset) resolvePath(p string) (string, error) {
	if p == "" || p == "." {
		return t.root, nil
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(t.root, p)
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("cannot access path: %w", err)
	}
	if resolved != t.root && !strings.HasPrefix(resolved, t.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q is outside the workspace", p)
	}
	return resolved, nil
}

// resolvePaths resolves every entry, requiring at least one.
func (t *Toolset) resolvePaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("paths is required: name at least one file")
	}
	resolved := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			return nil, fmt.Errorf("paths entries must not be empty")
		}
		r, err := t.resolvePath(p)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, r)
	}
	return resolved, nil
}

// capWriter buffers up to limit bytes and cancels the process once more than
// that arrives, so a flooding tool is killed instead of filling memory.
// Writes always report success; overflow is recorded, not surfaced as a write
// error.
type capWriter struct {
	buf    bytes.Buffer
	limit  int
	total  int
	cancel context.CancelFunc
}

func (w *capWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.total += n
	if remaining := w.limit - w.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		w.buf.Write(p)
	}
	if w.total > w.limit && w.cancel != nil {
		w.cancel()
	}
	return n, nil
}

// overflowed reports whether output beyond the cap was dropped.
func (w *capWriter) overflowed() bool { return w.total > w.limit }

// run spawns the tool's binary with the prebuilt argv, bounded by the
// toolset's timeout and output caps. Timeouts, cancellation, and non-zero
// exits all come back as clean errors.
func (t *Toolset) run(name string, argv []string) (string, error) {
	bin, ok := t.bins[name]
	if !ok {
		return "", fmt.Errorf("%s is not available: the %q binary was not found on PATH", name, binaryNames[name])
	}

	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, argv...)
	cmd.Dir = t.root
	cmd.Env = spawnEnv(name)
	stdout := &capWriter{limit: MaxOutputBytes, cancel: cancel}
	stderr := &capWriter{limit: MaxStderrBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = 2 * time.Second

	err := cmd.Run()
	if stdout.overflowed() {
		// The process was killed for flooding; what we kept is the result.
		return fmt.Sprintf("%s\n… (output truncated at %d bytes; narrow the query to see more)", stdout.buf.String(), MaxOutputBytes), nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("%s timed out after %s", name, t.timeout)
	}
	if ctx.Err() != nil {
		return "", fmt.Errorf("%s was cancelled", name)
	}
	if err != nil {
		detail := strings.TrimSpace(stderr.buf.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.buf.String())
		}
		if detail == "" {
			return "", fmt.Errorf("%s failed: %v", name, err)
		}
		detail, _ = tools.TruncateOutput(detail, MaxStderrBytes)
		return "", fmt.Errorf("%s failed: %v: %s", name, err, detail)
	}
	return stdout.buf.String(), nil
}
