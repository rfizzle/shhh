package sandbox

// A prepared image: the base image with a checkout's declared toolchain
// installed into it, built once in a container of its own and kept on this
// machine under a key of the base and the declaration, so that a sandbox's
// container starts with the tools already there and its network can stay
// off. Setup and session are two containers rather than one whose network is
// switched off partway: a container that once had the network may have
// fetched anything, and a switch flipped mid-life is a state to get wrong.
// See docs/capabilities/containment.md#a-sandbox-starts-from-an-image-prepared-from-it.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/project"
)

const (
	// preparedRepo is the local name every prepared image is tagged under,
	// with the key as its tag. It is a name and not a registry anybody
	// pushes to: the image never leaves this machine.
	preparedRepo = "localhost/shhh-toolchain"

	// The labels a prepared image carries: the key it was prepared under,
	// which is what a reuse is checked against, and the base it was
	// prepared from, which is what the image policy was asked about.
	labelToolchainKey  = "shhh.toolchain.key"
	labelToolchainBase = "shhh.toolchain.base"

	// prepareRecipe is part of the key, so an image prepared by a shhh
	// that ran the lines differently — another installer variable, another
	// order — is prepared again rather than reused as if it were the same.
	prepareRecipe = "1"

	// prepareTimeout bounds the whole preparation: a go install compiles,
	// and a setup container that never finishes must not hold a run
	// forever. The setup container's ownership record outlives it by a
	// margin, so a crashed preparation is reaped and a live one never is.
	prepareTimeout = time.Hour
	prepareTTL     = prepareTimeout + 10*time.Minute

	// prepareTailLines is how much of a failing line's output the refusal
	// quotes: the end is where an installer says why.
	prepareTailLines = 20
)

// preparedIDRE is an image ID as the engines report one: docker prefixes
// the algorithm and podman does not. It is also what keeps an ID from ever
// being read as an engine flag.
var preparedIDRE = regexp.MustCompile(`^(sha256:)?[0-9a-f]{64}$`)

// containerInstallEnv points each installer at a directory on the PATH the
// session's container is created with, rather than at the one it would pick
// for itself (~/go/bin, ~/.local/bin), which is not on it. The variables are
// handed to each line's exec and never to the container itself, because a
// commit keeps the container's own environment in the image, and the
// session would then run under an installer's settings. pip needs none: as
// root it installs where the image's own Python reads.
var containerInstallEnv = []string{
	"GOBIN=/usr/local/bin",
	"CARGO_INSTALL_ROOT=/usr/local",
	"NPM_CONFIG_PREFIX=/usr/local",
	"NPM_CONFIG_GLOBAL=true",
	"PNPM_HOME=/usr/local/bin",
	"PIPX_HOME=/opt/pipx",
	"PIPX_BIN_DIR=/usr/local/bin",
}

// NeedsPreparing reports whether a declaration names anything to put into an
// image. A declaration of `check` names alone installs nothing, and the base
// is what its container runs.
func NeedsPreparing(tc project.Toolchain) bool {
	return len(tc.Packages) > 0 || len(tc.Install) > 0
}

// PreparedKey is what a prepared image is kept under: the base's digest and
// the declaration's, so the image is reused while both stand and prepared
// again when either moves. The base is keyed by its digest rather than its
// name, because two names for one digest are one image.
func PreparedKey(base, declaration string) string {
	_, digest, _ := strings.Cut(base, "@sha256:")
	sum := sha256.Sum256([]byte("shhh toolchain image " + prepareRecipe + "\n" + digest + "\n" + declaration + "\n"))
	return hex.EncodeToString(sum[:])
}

// preparedRef is the local tag a key's image is kept under.
func preparedRef(key string) string { return preparedRepo + ":" + key }

// LookupPrepared finds the image prepared for base and a declaration's digest
// on this machine: its ID, and whether there is one. An image under the tag
// whose key label says otherwise is not it — somebody else's image under
// shhh's name is prepared over rather than trusted.
func LookupPrepared(ctx context.Context, eng Engine, base, declaration string) (id string, found bool, err error) {
	if !eng.OK {
		return "", false, fmt.Errorf("container engine unavailable: %s", eng.Detail)
	}
	key := PreparedKey(base, declaration)
	ctx, cancel := context.WithTimeout(ctx, containerLifecycleTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, eng.Path, "image", "inspect", preparedRef(key))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if isMissingImage(stderr.Bytes()) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("inspect %s: %v: %s", preparedRef(key), err, probeLine(stderr.Bytes()))
	}
	return readInspect(stdout.Bytes(), key)
}

// readInspect reads an image's ID and key label out of `image inspect`'s
// JSON, whose Id and Config.Labels keys both engines print, so that nothing
// rests on either engine's template field names.
func readInspect(out []byte, key string) (string, bool, error) {
	var images []struct {
		ID     string `json:"Id"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if err := json.Unmarshal(out, &images); err != nil {
		return "", false, fmt.Errorf("reading image inspect: %w", err)
	}
	if len(images) != 1 || images[0].Config.Labels[labelToolchainKey] != key {
		return "", false, nil
	}
	if !preparedIDRE.MatchString(images[0].ID) {
		return "", false, fmt.Errorf("image %s has an ID this cannot read: %q", preparedRef(key), images[0].ID)
	}
	return images[0].ID, true, nil
}

// isMissingImage recognises the engines' image-not-found errors, so an image
// never prepared is a preparation to do rather than an engine failure.
func isMissingImage(out []byte) bool {
	s := strings.ToLower(string(out))
	return strings.Contains(s, "no such image") || strings.Contains(s, "image not known")
}

// PrepareImage builds the image a sandbox for s runs when its checkout
// declares tc, and returns its ID. The base must pass the image policy: the
// allowlist names bases, and a prepared image is allowed because it is built
// here, from an allowed base and a declaration the checkout was trusted for.
//
// The setup container is a throwaway of its own: no workspace mount, no host
// environment and no session secrets, every capability dropped — so the only
// thing of the checkout's that reaches it is the declaration's lines — and
// the network on, since with nothing in it to take there is nothing for the
// network to carry away. It runs the packages, then each install line in
// order, and stops at the first that fails, naming it and quoting the end of
// its output: an image without the tool a line was for would only fail a
// check inside the session, far from the line.
func PrepareImage(ctx context.Context, eng Engine, s ContainerSpec, tc project.Toolchain, allowlist []string, store *Store) (string, error) {
	if !eng.OK {
		return "", fmt.Errorf("container engine unavailable: %s", eng.Detail)
	}
	if err := ValidateImage(s.Image, allowlist); err != nil {
		return "", err
	}
	s, err := s.withDefaults()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, prepareTimeout)
	defer cancel()

	id, err := newSandboxID()
	if err != nil {
		return "", err
	}
	name := "shhh-prep-" + id
	if out, err := runEngine(ctx, prepareArgv(eng, name, s)); err != nil {
		return "", fmt.Errorf("%s create failed for the setup container: %v: %s", eng.Name, err, probeLine(out))
	}
	now := time.Now().UTC()
	rec := Record{ID: id, Name: name, Engine: eng.Name, Image: s.Image, CreatedAt: now, ExpiresAt: now.Add(prepareTTL)}
	if err := store.Add(rec); err != nil {
		_, _ = runEngine(ctx, destroyArgv(eng.Path, name))
		return "", fmt.Errorf("cannot record sandbox ownership: %w", err)
	}
	defer func() {
		// The run's context may be spent by now; the setup container gets
		// its own bounded end, and the TTL reaper is the backstop.
		dctx, dcancel := context.WithTimeout(context.Background(), containerLifecycleTimeout)
		defer dcancel()
		_ = DestroyContainer(dctx, eng.Path, store, rec)
	}()

	for _, step := range prepareSteps(eng, name, tc) {
		out, err := exec.CommandContext(ctx, step.argv[0], step.argv[1:]...).CombinedOutput()
		if err != nil {
			return "", prepareFailure(step.line, err, out)
		}
	}

	key := PreparedKey(s.Image, tc.Digest)
	commit := commitArgv(eng, name, key, s.Image)
	if out, err := exec.CommandContext(ctx, commit[0], commit[1:]...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("%s commit of the prepared image failed: %v: %s", eng.Name, err, probeLine(out))
	}
	image, found, err := LookupPrepared(ctx, eng, s.Image, tc.Digest)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("the prepared image %s is not there after its commit", preparedRef(key))
	}
	return image, nil
}

// prepareArgv creates the setup container: createArgv's hardening and
// ceilings with neither of its host-facing halves — no mount, since nothing
// of the workspace goes in, and no `--network none`, since this is the one
// container that fetches. Its environment is only HOME and PATH, which the
// commit keeps and the session's container sets to the same values anyway.
func prepareArgv(eng Engine, name string, s ContainerSpec) []string {
	return []string{eng.Path, "run", "--detach", "--name", name,
		"--label", "shhh.sandbox=1",
		"--entrypoint", "",
		"--env", "HOME=/root",
		"--env", containerPath,
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--memory", s.Memory,
		"--cpus", s.CPUs,
		"--pids-limit", strconv.Itoa(s.PidsLimit),
		s.Image, "sleep", "2147483647",
	}
}

// prepareStep is one exec in the setup container and the line a failure of
// it is named by.
type prepareStep struct {
	line string
	argv []string
}

// prepareSteps is the declaration as execs: its packages in one apk add, then
// each install line through the shell, which is what gives a line's leading
// NAME=value words their meaning. A line was checked for shell syntax when
// the file was read, so the shell reads one command with words.
func prepareSteps(eng Engine, name string, tc project.Toolchain) []prepareStep {
	var steps []prepareStep
	if len(tc.Packages) > 0 {
		argv := append([]string{"apk", "add", "--no-cache"}, tc.Packages...)
		steps = append(steps, prepareStep{
			line: "packages: " + strings.Join(argv, " "),
			argv: append([]string{eng.Path, "exec", name}, argv...),
		})
	}
	for i, line := range tc.Install {
		argv := []string{eng.Path, "exec"}
		for _, pair := range containerInstallEnv {
			argv = append(argv, "--env", pair)
		}
		steps = append(steps, prepareStep{
			line: fmt.Sprintf("install[%d] %s", i, line),
			argv: append(argv, name, "/bin/sh", "-c", line),
		})
	}
	return steps
}

// commitArgv keeps the setup container as the prepared image, tagged under
// its key and labelled with the key and the base.
func commitArgv(eng Engine, name, key, base string) []string {
	return []string{eng.Path, "commit",
		"--change", "LABEL " + labelToolchainKey + "=" + key,
		"--change", "LABEL " + labelToolchainBase + "=" + base,
		name, preparedRef(key),
	}
}

// prepareFailure names the line that failed and quotes the end of what it
// printed, which is where an installer says why.
func prepareFailure(line string, err error, out []byte) error {
	code := err.Error()
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		code = "exit " + strconv.Itoa(exit.ExitCode())
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "preparing the image from %s stopped at %s (%s)", project.ToolchainFile, line, code)
	if len(lines) > prepareTailLines {
		fmt.Fprintf(&b, "\n  … %d earlier lines", len(lines)-prepareTailLines)
		lines = lines[len(lines)-prepareTailLines:]
	}
	for _, l := range lines {
		if l != "" {
			b.WriteString("\n  ")
			b.WriteString(l)
		}
	}
	return errors.New(b.String())
}
