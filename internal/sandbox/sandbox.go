// Package sandbox wraps agent-executed shell commands in OS-level process
// containment: bubblewrap on Linux, Seatbelt on macOS. A fixed deny
// mask hides credential and shhh state directories from contained commands,
// writes are limited to the workspace and scratch/cache paths, and any
// configuration the mechanism cannot express honestly is refused ("wrap
// unsupported") instead of silently weakened.
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/storage"
)

// Profile selects how much the contained command may reach. There are exactly
// two profiles: both limit writes to the workspace/scratch/cache set, and
// they differ only in network access.
type Profile string

const (
	// ProfileWorkspace preserves network access (the default).
	ProfileWorkspace Profile = "workspace"
	// ProfileWorkspaceNetless additionally removes network access.
	ProfileWorkspaceNetless Profile = "workspace-netless"
)

// ParseProfile maps a config value to its Profile; empty means the default
// workspace profile.
func ParseProfile(s string) (Profile, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(ProfileWorkspace):
		return ProfileWorkspace, nil
	case string(ProfileWorkspaceNetless):
		return ProfileWorkspaceNetless, nil
	}
	return ProfileWorkspace, fmt.Errorf("unknown sandbox profile %q (valid: workspace, workspace-netless)", s)
}

// Policy is the containment configuration for one session: the workspace
// root, the profile, and the config-provided extensions. The built-in deny
// mask is not part of the policy on purpose — it cannot be disabled.
type Policy struct {
	Workspace  string
	Profile    Profile
	DenyExtra  []string
	WriteExtra []string
	// Cwd is the directory the contained command starts in; empty means the
	// current process directory (sub-agents run in their own worktree,
	// sub-agents).
	Cwd string
	// ReadOnlyWorkspace withholds the workspace write grant, for callers that
	// must run commands read-only (the quality gate). Scratch and
	// toolchain-cache paths stay writable so builds and test runners keep
	// working.
	ReadOnlyWorkspace bool
	// Env is the NAME=value set the contained command's environment is
	// drawn from before the allowlist narrows it — the session's own, which
	// carries the values the vault added and this package has no way to
	// learn. Empty draws from this process's environment.
	Env []string
	// SecretNames are the variables the person declared as this session's
	// secrets. They join the allowlist because declaring one is asking for
	// it to reach the command; nothing about the shape of a name is
	// consulted, only what the vault was told.
	SecretNames []string
}

// Availability reports whether a containment mechanism can wrap commands on
// this host. Detail is honest either way: the mechanism's note when OK, or
// exactly why containment is unavailable when not.
type Availability struct {
	Mechanism string // "bwrap", "sandbox-exec", or "" when the platform has none
	OK        bool
	Detail    string
}

// Detect probes for a containment mechanism: bubblewrap with unprivileged
// user namespaces on Linux, Seatbelt (sandbox-exec) on macOS. Anything else
// reports honestly unavailable.
func Detect() Availability {
	switch runtime.GOOS {
	case "linux":
		return detectBwrap()
	case "darwin":
		return detectSeatbelt()
	}
	return Availability{Detail: fmt.Sprintf("no containment mechanism for %s", runtime.GOOS)}
}

// Wrap builds the full argv that runs command contained under the detected
// mechanism. The command text rides as one argv element after `sh -c` — it is
// never parsed or re-quoted. A policy the mechanism cannot express is refused
// with a "wrap unsupported" error rather than weakened.
func Wrap(avail Availability, p Policy, command string) ([]string, error) {
	if !avail.OK {
		return nil, fmt.Errorf("wrap unsupported: %s", avail.Detail)
	}
	s, err := resolvePolicy(p)
	if err != nil {
		return nil, err
	}
	switch avail.Mechanism {
	case "bwrap":
		return bwrapArgv(s, command), nil
	case "sandbox-exec":
		return seatbeltArgv(s, command), nil
	}
	return nil, fmt.Errorf("wrap unsupported: unknown mechanism %q", avail.Mechanism)
}

// WrapArgv is Wrap for callers that already hold a resolved argv (the quality
// gate's trusted checks): the argv runs directly under containment
// with no shell in between, so its elements are never parsed or re-quoted.
func WrapArgv(avail Availability, p Policy, argv []string) ([]string, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("wrap unsupported: empty argv")
	}
	if !avail.OK {
		return nil, fmt.Errorf("wrap unsupported: %s", avail.Detail)
	}
	s, err := resolvePolicy(p)
	if err != nil {
		return nil, err
	}
	switch avail.Mechanism {
	case "bwrap":
		return append(bwrapPrefix(s), argv...), nil
	case "sandbox-exec":
		return append(seatbeltPrefix(s), argv...), nil
	}
	return nil, fmt.Errorf("wrap unsupported: unknown mechanism %q", avail.Mechanism)
}

// spec is a Policy resolved against the real filesystem: symlinks followed,
// missing paths dropped, conflicts refused.
type spec struct {
	workspace string
	cwd       string
	shell     string
	write     []string // writable grants, in mount order
	denyDirs  []string // existing directories to mask (read as empty)
	denyFiles []string // existing regular files to mask (read as empty)
	env       []string // the whole environment the command gets, allowlisted
	// agentSocket is the ssh agent's socket, masked rather than left
	// reachable; empty when this host has no agent.
	agentSocket string
	network     bool
}

// DenyPaths is the deny mask that cannot be disabled, for the callers that
// have to know what it covers before they offer to widen anything: the
// working scope refuses to hold a directory behind this mask, because
// a grant it cannot honour is a promise the sandbox would break.
func DenyPaths() []string { return fixedDenyPaths() }

// fixedDenyPaths is the deny mask that cannot be disabled: credential
// directories plus shhh's own config and state dirs, so an allowed command
// still cannot read the user's keys or shhh's database.
func fixedDenyPaths() []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out,
			filepath.Join(home, ".ssh"),
			filepath.Join(home, ".aws"),
			filepath.Join(home, ".config", "gh"),
		)
	}
	for _, p := range config.Paths() {
		out = append(out, filepath.Dir(p))
	}
	if dir, err := storage.Dir(); err == nil {
		out = append(out, dir)
	}
	return out
}

// defaultWritePaths are the writable grants beyond the workspace: scratch
// space and toolchain caches, so builds and package managers keep working.
func defaultWritePaths() []string {
	out := []string{os.TempDir()}
	if cache, err := os.UserCacheDir(); err == nil {
		out = append(out, cache)
	}
	if mod := os.Getenv("GOMODCACHE"); mod != "" {
		out = append(out, mod)
	} else if gp := os.Getenv("GOPATH"); gp != "" {
		out = append(out, filepath.Join(gp, "pkg", "mod"))
	} else if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, "go", "pkg", "mod"))
	}
	return out
}

// envAllowlist is every variable name a contained command keeps. The
// environment is an allowlist rather than a mask because it is the one part
// of a command's world that names things nobody enumerated: a mask has to
// know what to drop, and what leaked before this existed was a variable
// shhh had never heard of. SSH_AUTH_SOCK is the reason it is worth the
// inconvenience — a contained command that inherits it is holding a signing
// oracle, and no file mask can take that back.
//
// What is on it is what a build needs to be a build: where to find programs,
// whose home this is, what language to speak, and the caches that are
// already writable grants. Anything else a command needs, the person adds
// as a session secret, which is the same act of naming it.
// See docs/capabilities/containment.md#a-contained-command-carries-almost-no-environment.
var envAllowlist = []string{
	"PATH", "HOME", "LANG", "TERM",
	"TMPDIR", "XDG_CACHE_HOME",
	"GOPATH", "GOCACHE", "GOMODCACHE",
}

// containedEnv applies the allowlist to env, keeping the pairs in the order
// they arrived so a later NAME=value still wins over an earlier one — which
// is what the session's own pairs rely on, appended as they are after the
// inherited environment.
func containedEnv(env, secrets []string) []string {
	if env == nil {
		env = os.Environ()
	}
	allow := make(map[string]bool, len(envAllowlist)+len(secrets))
	for _, name := range envAllowlist {
		allow[name] = true
	}
	for _, name := range secrets {
		allow[name] = true
	}
	at := make(map[string]int, len(allow))
	var out []string
	for _, pair := range env {
		name, _, ok := strings.Cut(pair, "=")
		if !ok || !allow[name] {
			continue
		}
		if i, dup := at[name]; dup {
			out[i] = pair
			continue
		}
		at[name] = len(out)
		out = append(out, pair)
	}
	return out
}

// agentSocketPath is where this host's ssh agent listens, or "" when it has
// none. Dropping the variable is most of the answer, but the path is a
// convention as much as an address, so the socket itself is masked too.
//
// A path nothing is listening on answers "" as well, for the same reason the
// deny mask only masks what exists: a mount needs somewhere to land, and the
// mechanisms fail the whole wrap rather than skip a mask they cannot make —
// so an address left over from a dead agent would stop every command in the
// session instead of hiding a socket that is not there.
func agentSocketPath() string {
	path := os.Getenv("SSH_AUTH_SOCK")
	if path == "" {
		return ""
	}
	// Resolved like every other path here: a link that pointed the mask
	// somewhere the mechanism spells differently would be a mask that misses,
	// and resolving is also how a path that is not there answers nothing.
	resolved, err := resolvePath(path)
	if err != nil {
		return ""
	}
	return resolved
}

// resolvePolicy turns a Policy into a mountable spec. Every path has its
// symlinks resolved before grant/mask decisions so a link cannot smuggle a
// masked path into a grant; a configuration that cannot be masked faithfully
// is refused.
func resolvePolicy(p Policy) (spec, error) {
	s := spec{shell: shellPath(), env: containedEnv(p.Env, p.SecretNames), agentSocket: agentSocketPath()}

	switch p.Profile {
	case "", ProfileWorkspace:
		s.network = true
	case ProfileWorkspaceNetless:
		s.network = false
	default:
		return spec{}, fmt.Errorf("wrap unsupported: unknown profile %q (valid: workspace, workspace-netless)", p.Profile)
	}

	if p.Workspace == "" {
		return spec{}, fmt.Errorf("wrap unsupported: no workspace path")
	}
	ws, err := resolvePath(p.Workspace)
	if err != nil {
		return spec{}, fmt.Errorf("wrap unsupported: cannot resolve workspace %s: %v", p.Workspace, err)
	}
	s.workspace = ws
	if p.Cwd != "" {
		cwd, err := resolvePath(p.Cwd)
		if err != nil {
			return spec{}, fmt.Errorf("wrap unsupported: cannot resolve cwd %s: %v", p.Cwd, err)
		}
		s.cwd = cwd
	} else if cwd, err := os.Getwd(); err == nil {
		s.cwd = cwd
	}

	seen := map[string]bool{}
	addWrite := func(path string) {
		rp, err := resolvePath(path)
		if err != nil {
			return // a missing grant is just no grant, not a weakening
		}
		if !seen[rp] {
			seen[rp] = true
			s.write = append(s.write, rp)
		}
	}
	if !p.ReadOnlyWorkspace {
		addWrite(ws)
	}
	for _, w := range defaultWritePaths() {
		addWrite(w)
	}
	for _, w := range p.WriteExtra {
		addWrite(w)
	}

	denySeen := map[string]bool{}
	for _, d := range append(fixedDenyPaths(), p.DenyExtra...) {
		rp, err := resolvePath(d)
		if err != nil {
			continue // nothing exists there, nothing to mask
		}
		if denySeen[rp] {
			continue
		}
		denySeen[rp] = true
		info, err := os.Stat(rp)
		if err != nil {
			continue
		}
		switch {
		case info.IsDir():
			s.denyDirs = append(s.denyDirs, rp)
		case info.Mode().IsRegular():
			s.denyFiles = append(s.denyFiles, rp)
		default:
			return spec{}, fmt.Errorf("wrap unsupported: cannot mask %s (not a directory or regular file)", rp)
		}
	}

	// Masks outrank write grants by mount order; a write grant *inside* a mask
	// would defeat it, so that configuration is refused outright.
	for _, w := range s.write {
		for _, d := range append(s.denyDirs, s.denyFiles...) {
			if within(w, d) {
				return spec{}, fmt.Errorf("wrap unsupported: writable path %s is inside masked path %s", w, d)
			}
		}
	}

	return s, nil
}

// resolvePath makes path absolute and resolves every symlink in it; it errors
// when the path does not exist.
func resolvePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// within reports whether path is dir or inside it; both must already be
// absolute and symlink-resolved.
func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// shellPath is the shell that goes inside a bwrap or seatbelt argv.
//
// It is shell.Execution's path, and it has to be: what goes in here is the
// same command, from the same model, that the unsandboxed path runs, and a
// command that changes shell when the user turns containment on is a
// containment bug that reads as a syntax error. The wrapper is Unix-only by
// construction, so the answer is always bash or the POSIX floor.
//
// It was $SHELL once, which is how the user's login shell got inside the
// sandbox — where a config.fish reaching for a path the profile masks fails
// before the command runs at all.
func shellPath() string { return shell.Execution().Path }
