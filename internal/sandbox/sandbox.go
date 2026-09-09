// Package sandbox wraps agent-executed shell commands in OS-level process
// containment: bubblewrap on Linux, Seatbelt on macOS. A fixed deny
// mask hides credential and shhh state directories from contained commands,
// writes are limited to the workspace and scratch/cache paths, and any
// configuration the mechanism cannot express honestly is refused ("wrap
// unsupported") instead of silently weakened.
package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"

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

// WithEnv returns the policy widened by pairs the caller is handing this one
// command directly — the `env` argument of a process start. The mechanism
// rebuilds the environment from the policy and clears whatever the spawn set
// beside it, so a pair that is not in the policy never reaches the command:
// a server told PORT=3001 comes up on 3000 and the model debugs a server
// that is running fine.
//
// It widens by name and nothing else. Each pair's name joins the allowlist
// the way a declared secret's does — there is no shape a name can have that
// gets it in — and a name the person already declared as a session secret is
// dropped rather than applied, so the session's value still wins. That is
// the precedence the uncontained spawn has, where the session's pairs are
// appended after the caller's, and the two paths agreeing is the whole
// reason this is one function rather than a rule each of them remembers.
func (p Policy) WithEnv(pairs []string) Policy {
	if len(pairs) == 0 {
		return p
	}
	declared := make(map[string]bool, len(p.SecretNames))
	for _, name := range p.SecretNames {
		declared[name] = true
	}
	// An empty Env means "this process's own", and appending to it would
	// quietly turn that into "these pairs and nothing else" — a
	// contained command with no PATH, which fails as "not found" rather than
	// as anything about the environment. Materialise it first.
	base := p.Env
	if base == nil {
		base = os.Environ()
	}
	// Fresh slices: a policy is handed out by value but its slices are
	// shared, and appending in place would let one start's extras survive
	// into the next command's environment.
	env := make([]string, 0, len(base)+len(pairs))
	env = append(env, base...)
	names := make([]string, 0, len(p.SecretNames)+len(pairs))
	names = append(names, p.SecretNames...)
	for _, pair := range pairs {
		name, _, ok := strings.Cut(pair, "=")
		if !ok || declared[name] {
			continue
		}
		// After p.Env, whose last pairs are the session's own: containedEnv
		// keeps the later of two pairs with one name, so an extra outranks
		// the inherited variable it shares a name with.
		env = append(env, pair)
		names = append(names, name)
	}
	p.Env, p.SecretNames = env, names
	return p
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
	s, err := resolvePolicy(p, avail.Mechanism)
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
	s, err := resolvePolicy(p, avail.Mechanism)
	if err != nil {
		return nil, err
	}
	switch avail.Mechanism {
	case "bwrap":
		return append(bwrapPrefix(s), argv...), nil
	case "sandbox-exec":
		return append(seatbeltPrefix(s), s.argvWithAppleGit(argv)...), nil
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
	// appleGit is the developer-toolchain git resolved before Seatbelt
	// starts. /usr/bin/git is an xcrun shim whose private cache sits outside
	// the contained process's temporary directory.
	appleGit string
	// tmpHidden are the host's shared temporary directories, made private to
	// the session; empty when no mechanism is named, because nothing is
	// hiding anything then either.
	tmpHidden []string
	// tmpdir is the scratch directory TMPDIR points at, spelled the way the
	// contained command will see it.
	tmpdir string
	// tmpVisible are the paths inside a hidden temporary directory that have
	// to stay readable anyway: a workspace or a working directory that is not
	// also a write grant. A grant needs no entry here — it is rebound over the
	// privatised tmpdir on its own.
	tmpVisible []string
	network    bool
}

// DenyPaths is the deny mask that cannot be disabled, for the callers that
// have to know what it covers before they offer to widen anything: the
// working scope refuses to hold a directory behind this mask, because
// a grant it cannot honour is a promise the sandbox would break.
func DenyPaths() []string { return fixedDenyPaths() }

// fixedDenyPaths is the deny mask that cannot be disabled: stores whose
// contents no session has honest business reading or writing.
//
// What is here rather than in CredentialPaths is decided by whether a
// session could ever have honest business writing to it. Nothing legitimate
// writes to the user's keyring, their GPG home, their password store, or the
// file curl and git read plaintext passwords out of, so those need no way
// back and get none: the working scope refuses to hold them at all.
func fixedDenyPaths() []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out,
			filepath.Join(home, ".ssh"),
			filepath.Join(home, ".aws"),
			filepath.Join(home, ".config", "gh"),
			// .netrc is the one plaintext store on this list and the one most
			// likely to be read by accident: curl and git both consult it
			// without being asked, so a command that merely fetches a URL is
			// a command that has already opened it.
			filepath.Join(home, ".netrc"),
			filepath.Join(home, ".gnupg"),
			filepath.Join(home, ".password-store"),
			filepath.Join(home, ".secrets"),
		)
	}
	return out
}

// ShhhPaths are shhh's own configuration and state directories. They begin
// masked, but are grantable as sensitive working-scope directories when the
// checkout itself is the work under review.
func ShhhPaths() []string {
	var out []string
	for _, p := range config.Paths() {
		out = append(out, filepath.Dir(p))
	}
	if dir, err := storage.Dir(); err == nil {
		out = append(out, dir)
	}
	return out
}

// CredentialPaths are the credential stores a contained command may read
// only while the session's working scope holds them. They are another tool's
// keys — a kubeconfig, a registry login, a cloud SDK's cached token — and
// unlike the fixed mask's entries a working session sometimes has honest
// business with one: `kubectl get pods` is a command a person asks for.
//
// So they are read the way they are written. The grant is the whole
// mechanism: masked by default, unmasked for the session by the same person
// answering the same card that makes the directory writable, and never by a
// permissive mode or the classifier — the working scope classifies each of
// these sensitive for exactly that reason. There is no setting that
// subtracts one from the mask, because a mask with a subtraction in it is a
// mask that gets configured away.
// See docs/capabilities/containment.md#the-deny-mask-is-not-configurable.
func CredentialPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".kube"),
		filepath.Join(home, ".docker"),
		filepath.Join(home, ".azure"),
		filepath.Join(home, ".config", "gcloud"),
		filepath.Join(home, ".gem"),
	}
}

// ungrantedCredentialPaths are the CredentialPaths this policy's grants do
// not cover, which is what the mask has to hide. A grant counts when it is
// the store itself or something inside it: a grant of ~/.docker/buildx says
// nothing about the registry login beside it, but masking the parent of a
// writable path is a configuration resolvePolicy refuses outright, so the
// narrower grant unmasks the store rather than failing every command in the
// session.
//
// The workspace counts as a grant for the same reason it is writable: a
// project that lives under one of these directories is still the directory
// the work is in, and a masked workspace is a session where every command
// reads an empty tree.
func ungrantedCredentialPaths(write []string, workspace string) []string {
	return ungrantedPaths(CredentialPaths(), write, workspace)
}

// ungrantedShhhPaths keeps shhh's own configuration and state out of a
// contained command by default. They use the same grant shape as another
// tool's credentials because developing shhh is the honest reason to reach
// them, but scope still marks that decision sensitive.
func ungrantedShhhPaths(write []string, workspace string) []string {
	return ungrantedPaths(ShhhPaths(), write, workspace)
}

func ungrantedPaths(paths, write []string, workspace string) []string {
	var out []string
	for _, c := range paths {
		rp, err := resolvePath(c)
		if err != nil {
			continue // nothing exists there, nothing to mask
		}
		granted := workspace != "" && within(workspace, rp)
		for _, w := range write {
			granted = granted || within(w, rp)
		}
		if !granted {
			out = append(out, rp)
		}
	}
	return out
}

// defaultWritePaths are the writable grants beyond the workspace: the
// toolchain caches, so builds and package managers keep working.
//
// The host's temporary directory used to be the first entry here and is not
// one any more: it is the session's own now (hostTempDirs), and a grant of it
// would be the two-way channel the privatised tmpdir exists to close. Scratch
// space is still writable — TMPDIR points at somewhere the command may write —
// it is just not somewhere anything else on the machine can read.
func defaultWritePaths() []string {
	var out []string
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

// hostTempDirs are the shared temporary directories on this host: /tmp, and
// whatever TMPDIR names when that is somewhere else. They are the one part of
// the filesystem every process on the machine can both read and write, which
// makes a bind of one a channel through the containment wall in both
// directions: a contained command reads what an uncontained one left there,
// and leaves what an uncontained one will read.
//
// /tmp is first because it is the one the command sees under the mechanism
// that can give it a filesystem of its own, and it is where TMPDIR then
// points. A path that is not there is not returned: the mechanisms mask what
// exists, and there is nothing to hide at a path nothing is using.
// See docs/capabilities/containment.md#the-temporary-directory-is-the-sessions-own.
func hostTempDirs() []string {
	var out []string
	for _, p := range []string{"/tmp", os.TempDir()} {
		rp, err := resolvePath(p)
		if err != nil {
			continue
		}
		// A TMPDIR under /tmp is already hidden by /tmp, and naming it twice
		// would be a second mount of a directory that is not there any more.
		if slices.ContainsFunc(out, func(h string) bool { return within(rp, h) }) {
			continue
		}
		out = append(out, rp)
	}
	return out
}

// sessionTmpDir is the scratch directory TMPDIR points at where the mechanism
// cannot give the command a filesystem of its own — Seatbelt, which says what
// a process may reach and cannot rearrange what is there. It is one directory
// per shhh process under the state dir, which the deny mask already hides
// from every contained command: the session's own scratch is reachable
// because the profile names it after the mask and SBPL gives the later rule
// precedence, and another session's is not reachable at all.
//
// A process that is gone takes its directory with it. Nothing else ever
// deletes one — the wrap is built per command and there is no seam that runs
// when a session ends — so the sweep is here, where the next session is
// already reading the directory it is about to write in.
func sessionTmpDir() (string, error) {
	state, err := storage.Dir()
	if err != nil {
		return "", err
	}
	base := filepath.Join(state, "tmp")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	sweepSessionTmpDirs(base)
	mine := filepath.Join(base, strconv.Itoa(os.Getpid()))
	if err := os.MkdirAll(mine, 0o700); err != nil {
		return "", err
	}
	return resolvePath(mine)
}

// sweepSessionTmpDirs removes the scratch directories of sessions that have
// ended. A name that is not a pid is left alone: this directory is shhh's
// own, and a sweep that deleted what it did not recognise would be one bug
// away from deleting somebody's files.
func sweepSessionTmpDirs(base string) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	self := os.Getpid()
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self || processAlive(pid) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(base, e.Name()))
	}
}

// processAlive reports whether pid is still running. Signal 0 is the ask
// without the signal; a process owned by somebody else answers EPERM, which
// is an answer that it is there.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
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
//
// The mechanism is a parameter because one part of the answer is not the same
// on both: where the session's temporary directory is. Bubblewrap can give
// the command a filesystem of its own at /tmp and Seatbelt cannot, so the two
// hand TMPDIR different paths, and TMPDIR is in the environment the spec
// already carries. An empty mechanism resolves the paths and privatises
// nothing — that is `shhh doctor` on a host where nothing wraps a command,
// and a report that named a private tmpdir there would be describing a
// containment that is not happening.
func resolvePolicy(p Policy, mechanism string) (spec, error) {
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
	if mechanism == "sandbox-exec" {
		s.preferAppleGit()
	}

	if err := s.privatiseTmp(mechanism); err != nil {
		return spec{}, err
	}

	// The mask is resolved after the grants because part of it is decided by
	// them: a credential store the scope holds is one the person asked to
	// work in, and masking it anyway would refuse every command in the
	// session rather than protect anything.
	deny := append(fixedDenyPaths(), p.DenyExtra...)
	deny = append(deny, ungrantedCredentialPaths(s.write, s.workspace)...)
	deny = append(deny, ungrantedShhhPaths(s.write, s.workspace)...)

	denySeen := map[string]bool{}
	for _, d := range deny {
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

// privatiseTmp gives the contained command a temporary directory of its own
// and hides the host's, which is the one place an otherwise closed write
// boundary was open in both directions.
//
// It runs after the write grants because the grant is how a tool that
// legitimately needs the host's /tmp gets it — a socket a language server put
// there, a build cache a person pointed at. Bubblewrap mounts the tmpfs first
// and binds the grants back over it, so a grant is exactly as wide as it was
// asked for; Seatbelt has the deny before the allowances, which SBPL reads
// the same way. Either way nothing new is refused: the grant path already
// existed and this is the only thing it now has to answer for.
func (s *spec) privatiseTmp(mechanism string) error {
	switch mechanism {
	case "bwrap", "sandbox-exec":
	default:
		return nil // nothing is wrapping the command; nothing is private
	}
	s.tmpHidden = hostTempDirs()
	if len(s.tmpHidden) == 0 {
		return nil
	}
	switch mechanism {
	case "bwrap":
		// The tmpfs is mounted over the host's own /tmp, so the path the
		// command writes to is the path it already expects.
		s.tmpdir = s.tmpHidden[0]
	case "sandbox-exec":
		dir, err := sessionTmpDir()
		if err != nil {
			return fmt.Errorf("wrap unsupported: cannot make a private tmpdir: %v", err)
		}
		s.tmpdir = dir
	}
	// A workspace or a working directory inside the host's tmpdir is not a
	// place the command may write unless something granted it, but it is
	// still where the work is: hiding it would leave the command chdir'd into
	// a directory that is no longer there.
	for _, path := range []string{s.workspace, s.cwd} {
		if path == "" || !slices.ContainsFunc(s.tmpHidden, func(h string) bool { return within(path, h) }) {
			continue
		}
		if slices.ContainsFunc(s.write, func(w string) bool { return within(path, w) }) {
			continue // a grant is rebound over the tmpdir already
		}
		if !slices.Contains(s.tmpVisible, path) {
			s.tmpVisible = append(s.tmpVisible, path)
		}
	}
	s.env = withTmpdir(s.env, s.tmpdir)
	return nil
}

// withTmpdir points TMPDIR at the session's own scratch. The variable is on
// the allowlist because a build needs somewhere to write, and what it said on
// the way in was a directory the whole machine shares.
func withTmpdir(env []string, dir string) []string {
	out := make([]string, 0, len(env)+1)
	for _, pair := range env {
		if name, _, ok := strings.Cut(pair, "="); ok && name == "TMPDIR" {
			continue
		}
		out = append(out, pair)
	}
	return append(out, "TMPDIR="+dir)
}

// findAppleGit is a variable so the policy test can hold the developer-tool
// lookup to its contract without depending on an Xcode installation.
var findAppleGit = func() (string, error) {
	out, err := exec.Command("/usr/bin/xcrun", "--find", "git").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// preferAppleGit resolves Apple's git before containment and teaches both a
// shell command and a direct argv to use it. The /usr/bin shim invokes xcrun,
// whose cache is deliberately outside the contained process's private temp
// space; the resolved tool is the same developer selection without reopening
// the host temporary directory.
// See docs/capabilities/containment.md#apple-toolchain-shims-stay-compatible.
func (s *spec) preferAppleGit() {
	git, err := findAppleGit()
	if err != nil || !filepath.IsAbs(git) || filepath.Clean(git) == "/usr/bin/git" {
		return
	}
	s.appleGit = filepath.Clean(git)
	s.env = prependPath(s.env, filepath.Dir(s.appleGit))
}

func prependPath(env []string, dir string) []string {
	out := make([]string, 0, len(env)+1)
	found := false
	for _, pair := range env {
		name, value, ok := strings.Cut(pair, "=")
		if !ok || name != "PATH" {
			out = append(out, pair)
			continue
		}
		out = append(out, "PATH="+dir+string(filepath.ListSeparator)+value)
		found = true
	}
	if !found {
		out = append(out, "PATH="+dir)
	}
	return out
}

func (s spec) argvWithAppleGit(argv []string) []string {
	if s.appleGit == "" || len(argv) == 0 || filepath.Clean(argv[0]) != "/usr/bin/git" {
		return argv
	}
	out := append([]string(nil), argv...)
	out[0] = s.appleGit
	return out
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
