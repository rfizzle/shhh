package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Container sandboxes: a disposable engine-managed workspace for long
// or unsupervised agent runs. The container gets exactly one host mount (the
// workspace, writable), no host environment or credentials, all capabilities
// dropped, and resource ceilings; everything else it touches is its own
// disposable filesystem layer.

// ContainerSpec describes one sandbox container. Zero-valued ceilings take
// the package defaults; Image must be digest-pinned and pass the allowlist.
type ContainerSpec struct {
	Image string
	// Prepared is the ID of the image PrepareImage built from Image and the
	// checkout's declaration. Where it is set the container runs it in
	// Image's place, and Image is still what the policy is asked about.
	Prepared  string
	Workspace string
	Network   bool // false adds --network none (the netless profile)
	Memory    string
	CPUs      string
	PidsLimit int
	TTL       time.Duration

	// git is the workspace repository's program paths as the container
	// holds them, read on the host by CreateContainer once the workspace is
	// resolved.
	git spec
}

// Default resource ceilings and lifetime for sandbox containers.
const (
	DefaultContainerMemory = "2g"
	DefaultContainerCPUs   = "2"
	DefaultContainerPids   = 256
	DefaultContainerTTL    = 24 * time.Hour

	containerLifecycleTimeout = 60 * time.Second

	// workspaceMount is where the single writable host mount lands inside the
	// container; exec always runs there.
	workspaceMount = "/workspace"
)

// containerPath is the explicit PATH inside the container — host environment
// is never forwarded.
const containerPath = "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

var (
	// imagePinnedRE accepts a normal image reference pinned to a sha256 digest.
	// The leading character class also keeps a crafted "image" from ever being
	// parsed as an engine flag.
	imagePinnedRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/:-]*@sha256:[0-9a-f]{64}$`)
	memoryRE      = regexp.MustCompile(`^[0-9]+[bkmgBKMG]?$`)
	cpusRE        = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)
)

// ValidateImage enforces the image policy: a digest-pinned reference that,
// when an allowlist is configured, appears in it verbatim. Tags are refused —
// a tag can move under the sandbox, a digest cannot.
func ValidateImage(image string, allowlist []string) error {
	if strings.TrimSpace(image) == "" {
		return errors.New("no sandbox image configured (set sandbox.container_image to a digest-pinned image)")
	}
	if !imagePinnedRE.MatchString(image) {
		return fmt.Errorf("sandbox image %q is not digest-pinned (need name@sha256:<64 hex>)", image)
	}
	if len(allowlist) == 0 {
		return nil
	}
	for _, allowed := range allowlist {
		if image == allowed {
			return nil
		}
	}
	return fmt.Errorf("sandbox image %q is not in sandbox.image_allowlist", image)
}

// withDefaults fills unset ceilings and validates the ones provided, so a
// malformed config value is refused instead of riding into an engine flag.
func (s ContainerSpec) withDefaults() (ContainerSpec, error) {
	if s.Memory == "" {
		s.Memory = DefaultContainerMemory
	}
	if s.CPUs == "" {
		s.CPUs = DefaultContainerCPUs
	}
	if s.PidsLimit <= 0 {
		s.PidsLimit = DefaultContainerPids
	}
	if s.TTL <= 0 {
		s.TTL = DefaultContainerTTL
	}
	if !memoryRE.MatchString(s.Memory) {
		return s, fmt.Errorf("invalid sandbox memory limit %q", s.Memory)
	}
	if !cpusRE.MatchString(s.CPUs) {
		return s, fmt.Errorf("invalid sandbox cpu limit %q", s.CPUs)
	}
	return s, nil
}

// Container is a created sandbox: its ownership record plus the engine that
// runs it.
type Container struct {
	Record Record
	Engine Engine
	// gitHeld is a workspace repository whose program paths the container
	// holds read-only (containerGit).
	gitHeld bool
}

// GitStoreWords is what every report says of the workspace repository's
// program paths in this container: the words the host's mechanisms use
// where the container holds them read-only, "" where it holds none.
// See docs/capabilities/containment.md#the-repositorys-own-programs-are-read-only.
func (c Container) GitStoreWords() string {
	if !c.gitHeld {
		return ""
	}
	return gitStoreWords
}

// createArgv builds the engine invocation for a sandbox container: detached
// keeper process, one writable workspace mount, explicit minimal environment
// (no host env or credentials), all capabilities dropped, no privilege
// re-escalation, and resource ceilings. The image's entrypoint is cleared so
// it can neither wrap nor swallow the keeper — a plain long sleep, so any
// image with a POSIX sh works.
func createArgv(eng Engine, name string, s ContainerSpec) []string {
	argv := []string{eng.Path, "run", "--detach", "--name", name,
		"--label", "shhh.sandbox=1",
		"--entrypoint", "",
		"--volume", s.Workspace + ":" + workspaceMount,
		"--workdir", workspaceMount,
		"--env", "HOME=/root",
		"--env", containerPath,
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--memory", s.Memory,
		"--cpus", s.CPUs,
		"--pids-limit", strconv.Itoa(s.PidsLimit),
	}
	argv = append(argv, gitVolumes(s.Workspace, s.git)...)
	if !s.Network {
		argv = append(argv, "--network", "none")
	}
	return append(argv, s.run(), "sleep", "2147483647")
}

// containerGit reads the workspace repository's program paths the way
// bubblewrap holds them, with the workspace as the one grant — the mount is
// the whole of what the container can write. Git runs these on the host
// after the run: a hook on the next commit, an fsmonitor on the next status.
// So the ones that exist are bound read-only over the workspace mount, and
// the directories between the mount and each of them are bound over
// themselves, since a mount point cannot be renamed and .git moved aside,
// written into and moved back would walk around the rest. An entry that does
// not exist, or is a link, cannot be held by a mount at all, as under
// bubblewrap; an entry outside the workspace is not in the container.
// See docs/capabilities/containment.md#the-repositorys-own-programs-are-read-only.
func containerGit(ws string) spec {
	g := spec{workspace: ws, write: []string{ws}}
	g.maskGitStore("bwrap")
	return g
}

// gitVolumes are the binds that hold what containerGit read, each at its
// place under the workspace mount: the pins first, shortest first, then the
// read-only entries, so no bind covers one inside it.
func gitVolumes(ws string, g spec) []string {
	var argv []string
	at := func(host string) (string, bool) {
		rel, err := filepath.Rel(ws, host)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", false
		}
		return workspaceMount + "/" + filepath.ToSlash(rel), true
	}
	for _, p := range g.gitPinned {
		if dst, ok := at(p); ok {
			argv = append(argv, "--volume", p+":"+dst)
		}
	}
	for _, p := range g.gitReadOnly {
		if dst, ok := at(p); ok {
			argv = append(argv, "--volume", p+":"+dst+":ro")
		}
	}
	return argv
}

// run is the image the container is created from: the prepared one where
// there is one, the base otherwise.
func (s ContainerSpec) run() string {
	if s.Prepared != "" {
		return s.Prepared
	}
	return s.Image
}

// ExecArgv builds the argv that runs command inside the sandbox. The command
// text rides as one argv element after `sh -c` — never parsed or re-quoted —
// mirroring the process-containment wrappers. env names variables to carry
// in from the engine client's environment (a bare `--env NAME` is how both
// docker and podman spell that): the container was created with no host
// environment at all, so a session secret has to be named to cross into it.
// See docs/capabilities/secrets.md#a-secret-is-an-environment-variable.
func (c Container) ExecArgv(command string, env ...string) []string {
	argv := []string{c.Engine.Path, "exec", "--workdir", workspaceMount}
	for _, name := range env {
		argv = append(argv, "--env", name)
	}
	return append(argv, c.Record.Name, "/bin/sh", "-c", command)
}

// HelperExec is how one program is started in the container through the
// command helper (ExecArg). The zero value is a command: no terminal, the
// workspace root, /dev/null for the child's stdin and a hang-up that stops
// it.
type HelperExec struct {
	// Dir is the host directory the program runs in, translated to its place
	// under the workspace mount; "" is the workspace itself.
	Dir string
	// TTY gives the exec a terminal, for a process that asked for one.
	TTY bool
	// Secrets name variables carried in from the engine client's own
	// environment — the session's declared secrets — and Env is a start's
	// own NAME=value pairs. The container was created with no host
	// environment, so nothing else crosses.
	// See docs/capabilities/secrets.md#a-secret-is-an-environment-variable.
	Secrets []string
	Env     []string
	// Inherit hands the child the stream as its stdin, for a process whose
	// input is written to it; a command's child reads /dev/null.
	Inherit bool
	// IgnoreHangup leaves the program running when the stream hangs up, for
	// a caller that closes its stdin once it has written what it had to,
	// and Timeout is then the ceiling the helper holds it to itself.
	IgnoreHangup bool
	Timeout      time.Duration
}

// HelperArgv is the engine argv that runs argv in the container under the
// helper: `exec -i`, a terminal only where asked, the working directory, the
// variables that cross, the helper and its flags, then `--` and the argv.
// The stream `-i` keeps open is what a cancel travels on, so every exec has
// it whether or not the program reads anything.
//
// A directory outside the workspace is refused rather than run somewhere
// else: the workspace is the container's one mount, and a command told it
// was in one place and run in another is worse than one that never ran.
func (c Container) HelperArgv(e HelperExec, argv []string) ([]string, error) {
	if len(argv) == 0 {
		return nil, errors.New("nothing to run in the sandbox")
	}
	workdir, err := c.workdir(e.Dir)
	if err != nil {
		return nil, err
	}
	out := []string{c.Engine.Path, "exec", "-i"}
	if e.TTY {
		out = append(out, "-t")
	}
	out = append(out, "--workdir", workdir)
	for _, name := range e.Secrets {
		out = append(out, "--env", name)
	}
	for _, pair := range e.Env {
		out = append(out, "--env", pair)
	}
	stdin, hangup := stdinNull, hangupStop
	if e.Inherit {
		stdin = stdinInherit
	}
	if e.IgnoreHangup {
		hangup = hangupIgnore
	}
	out = append(out, c.Record.Name, HelperPath, ExecArg, "--stdin="+stdin, "--hangup="+hangup)
	if e.Timeout > 0 {
		out = append(out, "--timeout="+e.Timeout.String())
	}
	out = append(out, "--")
	return append(out, argv...), nil
}

// HelperCommand is HelperArgv for a command line. The text rides as one
// argv element after `sh -c` — never parsed or re-quoted — as it does under
// every other mechanism.
func (c Container) HelperCommand(e HelperExec, command string) ([]string, error) {
	return c.HelperArgv(e, []string{"/bin/sh", "-c", command})
}

// workdir is dir's place under the workspace mount.
func (c Container) workdir(dir string) (string, error) {
	if dir == "" {
		return workspaceMount, nil
	}
	resolved, err := resolvePath(dir)
	if err != nil {
		return "", fmt.Errorf("cannot resolve %s for the sandbox: %w", dir, err)
	}
	rel, err := filepath.Rel(c.Record.Workspace, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%s is outside the sandbox's workspace %s, the one directory its container can see", dir, c.Record.Workspace)
	}
	if rel == "." {
		return workspaceMount, nil
	}
	return workspaceMount + "/" + filepath.ToSlash(rel), nil
}

// ProbeHelper asks the container's helper which protocol it speaks, and
// fails where there is no helper to ask or it speaks another. It is asked
// once, before anything runs, because a container without one runs a
// command whose cancel never reaches it.
func (c Container) ProbeHelper(ctx context.Context) error {
	out, err := runEngine(ctx, []string{c.Engine.Path, "exec", c.Record.Name, HelperPath, ExecArg, "--version"})
	if err != nil {
		return fmt.Errorf("it has no command helper at %s (%s)", HelperPath, probeLine(out))
	}
	got := strings.TrimSpace(string(out))
	if got != helperBanner+HelperVersion {
		return fmt.Errorf("its command helper at %s answers %q, and this shhh speaks %s", HelperPath, probeLine(out), helperBanner+HelperVersion)
	}
	return nil
}

// CreateContainer validates the spec, starts the container, and records
// ownership durably before returning. A record that cannot be written
// destroys the container again — shhh never leaves a sandbox it does not
// track.
func CreateContainer(ctx context.Context, eng Engine, s ContainerSpec, allowlist []string, store *Store) (Container, error) {
	if !eng.OK {
		return Container{}, fmt.Errorf("container engine unavailable: %s", eng.Detail)
	}
	if err := ValidateImage(s.Image, allowlist); err != nil {
		return Container{}, err
	}
	if s.Prepared != "" && !preparedIDRE.MatchString(s.Prepared) {
		return Container{}, fmt.Errorf("prepared image %q is not an image ID", s.Prepared)
	}
	s, err := s.withDefaults()
	if err != nil {
		return Container{}, err
	}
	if s.Workspace == "" {
		return Container{}, errors.New("no workspace path for sandbox")
	}
	ws, err := resolvePath(s.Workspace)
	if err != nil {
		return Container{}, fmt.Errorf("cannot resolve workspace %s: %w", s.Workspace, err)
	}
	s.Workspace = ws
	s.git = containerGit(ws)

	id, err := newSandboxID()
	if err != nil {
		return Container{}, err
	}
	name := "shhh-sbx-" + id

	out, err := runEngine(ctx, createArgv(eng, name, s))
	if err != nil {
		return Container{}, fmt.Errorf("%s create failed: %v: %s", eng.Name, err, probeLine(out))
	}

	now := time.Now().UTC()
	rec := Record{
		ID:        id,
		Name:      name,
		Engine:    eng.Name,
		Image:     s.run(),
		Workspace: s.Workspace,
		CreatedAt: now,
		ExpiresAt: now.Add(s.TTL),
	}
	if err := store.Add(rec); err != nil {
		_, _ = runEngine(ctx, destroyArgv(eng.Path, name))
		return Container{}, fmt.Errorf("cannot record sandbox ownership: %w", err)
	}
	return Container{Record: rec, Engine: eng, gitHeld: len(s.git.gitReadOnly) > 0}, nil
}

// DestroyContainer force-removes the container and, only once the engine no
// longer knows it, drops the ownership record.
func DestroyContainer(ctx context.Context, enginePath string, store *Store, rec Record) error {
	out, err := runEngine(ctx, destroyArgv(enginePath, rec.Name))
	if err != nil && !isMissingContainer(out) {
		return fmt.Errorf("destroy %s: %v: %s", rec.Name, err, probeLine(out))
	}
	return store.Remove(rec.ID)
}

// ContainerState asks the engine for the container's state. gone reports a
// container the engine does not know (distinct from the engine itself
// failing, which returns an error and keeps ownership records intact).
func ContainerState(ctx context.Context, enginePath, name string) (state string, gone bool, err error) {
	out, err := runEngine(ctx, []string{enginePath, "inspect", "--format", "{{.State.Status}}", name})
	if err != nil {
		if isMissingContainer(out) {
			return "", true, nil
		}
		return "", false, fmt.Errorf("inspect %s: %v: %s", name, err, probeLine(out))
	}
	return strings.TrimSpace(string(out)), false, nil
}

func destroyArgv(enginePath, name string) []string {
	return []string{enginePath, "rm", "--force", name}
}

// isMissingContainer recognizes the engines' container-not-found errors, so a
// vanished container is treated as gone rather than as an engine failure.
func isMissingContainer(out []byte) bool {
	s := strings.ToLower(string(out))
	return strings.Contains(s, "no such container") || strings.Contains(s, "no such object") ||
		strings.Contains(s, "does not exist") || strings.Contains(s, "not found")
}

// runEngine executes one engine lifecycle command with a bounded lifetime.
func runEngine(ctx context.Context, argv []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, containerLifecycleTimeout)
	defer cancel()
	return exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
}

func newSandboxID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("cannot generate sandbox id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
