// Package sandbox wraps agent-executed shell commands in OS-level process
// containment (S-062): bubblewrap on Linux, Seatbelt on macOS. A fixed deny
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

// spec is a Policy resolved against the real filesystem: symlinks followed,
// missing paths dropped, conflicts refused.
type spec struct {
	workspace string
	cwd       string
	shell     string
	write     []string // writable grants, in mount order
	denyDirs  []string // existing directories to mask (read as empty)
	denyFiles []string // existing regular files to mask (read as empty)
	network   bool
}

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

// resolvePolicy turns a Policy into a mountable spec. Every path has its
// symlinks resolved before grant/mask decisions so a link cannot smuggle a
// masked path into a grant; a configuration that cannot be masked faithfully
// is refused.
func resolvePolicy(p Policy) (spec, error) {
	s := spec{shell: shellPath()}

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
	if cwd, err := os.Getwd(); err == nil {
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
	addWrite(ws)
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

func shellPath() string {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	return filepath.Clean(sh)
}
