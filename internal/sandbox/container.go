package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
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
	Image     string
	Workspace string
	Network   bool // false adds --network none (the netless profile)
	Memory    string
	CPUs      string
	PidsLimit int
	TTL       time.Duration
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
	if !s.Network {
		argv = append(argv, "--network", "none")
	}
	return append(argv, s.Image, "sleep", "2147483647")
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
		Image:     s.Image,
		Workspace: s.Workspace,
		CreatedAt: now,
		ExpiresAt: now.Add(s.TTL),
	}
	if err := store.Add(rec); err != nil {
		_, _ = runEngine(ctx, destroyArgv(eng.Path, name))
		return Container{}, fmt.Errorf("cannot record sandbox ownership: %w", err)
	}
	return Container{Record: rec, Engine: eng}, nil
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
