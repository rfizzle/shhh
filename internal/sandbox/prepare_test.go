package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/project"
)

const (
	testBase   = "ghcr.io/acme/sandbox" + testDigest
	testDecl   = "1111111111111111111111111111111111111111111111111111111111111111"
	testImgID  = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	testPodman = "3333333333333333333333333333333333333333333333333333333333333333"
)

// The key is what reuse stands on, so it has to move with each half and
// with nothing else: the base's digest and the declaration's.
func TestPreparedKeyMovesWithTheBaseDigestAndTheDeclaration(t *testing.T) {
	key := PreparedKey(testBase, testDecl)
	if len(key) != 64 || key != PreparedKey(testBase, testDecl) {
		t.Fatalf("the key is not a stable sha256: %q", key)
	}
	otherBase := "ghcr.io/acme/sandbox@sha256:" + strings.Repeat("b", 64)
	if PreparedKey(otherBase, testDecl) == key {
		t.Fatal("a new base digest kept the key, so the old image would be reused over it")
	}
	if PreparedKey(testBase, strings.Repeat("9", 64)) == key {
		t.Fatal("a changed declaration kept the key, so the old image would be reused over it")
	}
	// Two names for one digest are one image.
	if PreparedKey("mirror.example/sandbox"+testDigest, testDecl) != key {
		t.Fatal("the key read the base's name rather than its digest")
	}
}

// The setup container is the session's hardening without its host-facing
// half: nothing of the workspace is mounted, the only variables are the two
// the session sets anyway, and the network is the one thing left on.
func TestPrepareArgvMountsNothingAndCarriesNoHostEnvironment(t *testing.T) {
	t.Setenv("SHHH_IT_CANARY", "host-secret")
	eng := Engine{Name: "docker", Path: "/usr/bin/docker", OK: true}
	s, err := ContainerSpec{Image: testBase, Workspace: t.TempDir()}.withDefaults()
	if err != nil {
		t.Fatal(err)
	}
	argv := prepareArgv(eng, "shhh-prep-x", s)
	joined := strings.Join(argv, " ")
	for _, want := range []string{
		"--cap-drop ALL", "--security-opt no-new-privileges",
		"--memory " + DefaultContainerMemory, "--cpus " + DefaultContainerCPUs, "--pids-limit 256",
		"--label shhh.sandbox=1", "--entrypoint ",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("setup argv missing %q: %q", want, joined)
		}
	}
	for _, never := range []string{"--volume", "--mount", "--network", s.Workspace, "host-secret"} {
		if strings.Contains(joined, never) {
			t.Errorf("setup argv carries %q: %q", never, joined)
		}
	}
	if envs := flagValues(argv, "--env"); !slices.Equal(envs, []string{"HOME=/root", containerPath}) {
		t.Errorf("setup env must be exactly HOME and PATH, got %v", envs)
	}
	if got := argv[len(argv)-3]; got != testBase {
		t.Errorf("setup runs %q, want the base %q", got, testBase)
	}
}

// The packages go first, since a line may need one; each install line runs
// through the shell with the installers pointed at the session's PATH, and
// every variable is a value named here — a bare `--env NAME` would carry
// the host's value in.
func TestPrepareStepsRunThePackagesThenEachLine(t *testing.T) {
	eng := Engine{Path: "/usr/bin/docker"}
	tc := project.Toolchain{
		Packages: []string{"jq", "golangci-lint=2.5.0-r0"},
		Install:  []string{"CGO_ENABLED=0 go install example.com/cmd/tool@v1.2.3", "apk add ripgrep=15.1.0-r0"},
	}
	steps := prepareSteps(eng, "shhh-prep-x", tc)
	if len(steps) != 3 {
		t.Fatalf("got %d steps, want the packages and two lines: %+v", len(steps), steps)
	}
	if want := []string{"/usr/bin/docker", "exec", "shhh-prep-x", "apk", "add", "--no-cache", "jq", "golangci-lint=2.5.0-r0"}; !slices.Equal(steps[0].argv, want) {
		t.Errorf("packages step = %v, want %v", steps[0].argv, want)
	}
	for i, step := range steps[1:] {
		tail := step.argv[len(step.argv)-4:]
		if want := []string{"shhh-prep-x", "/bin/sh", "-c", tc.Install[i]}; !slices.Equal(tail, want) {
			t.Errorf("line %d runs %v, want %v", i, tail, want)
		}
		envs := flagValues(step.argv, "--env")
		if !slices.Equal(envs, containerInstallEnv) {
			t.Errorf("line %d env = %v, want the installers' own %v", i, envs, containerInstallEnv)
		}
		if !strings.Contains(step.line, tc.Install[i]) || !strings.HasPrefix(step.line, "install[") {
			t.Errorf("line %d is named %q, which does not quote it", i, step.line)
		}
	}
	for _, pair := range containerInstallEnv {
		if !strings.Contains(pair, "=") {
			t.Errorf("installer variable %q names no value, so the host's would cross", pair)
		}
	}
	if !slices.Contains(containerInstallEnv, "GOBIN=/usr/local/bin") || !strings.Contains(containerPath, "/usr/local/bin") {
		t.Error("go install does not land on the PATH the session's container is handed")
	}
	if got := prepareSteps(eng, "n", project.Toolchain{Check: []string{"jq"}}); len(got) != 0 {
		t.Errorf("a declaration of checks alone prepared %v", got)
	}
}

func TestCommitArgvLabelsTheKeyAndTheBase(t *testing.T) {
	key := PreparedKey(testBase, testDecl)
	argv := commitArgv(Engine{Path: "/usr/bin/podman"}, "shhh-prep-x", key, testBase)
	want := []string{"/usr/bin/podman", "commit",
		"--change", "LABEL shhh.toolchain.key=" + key,
		"--change", "LABEL shhh.toolchain.base=" + testBase,
		"shhh-prep-x", "localhost/shhh-toolchain:" + key,
	}
	if !slices.Equal(argv, want) {
		t.Errorf("commitArgv = %v, want %v", argv, want)
	}
}

// Reuse is decided by the label, not by the tag: an image under shhh's name
// with another key is prepared over.
func TestReadInspectTrustsTheKeyLabelAndNotTheTag(t *testing.T) {
	key := PreparedKey(testBase, testDecl)
	inspect := func(id, label string) []byte {
		return []byte(`[{"Id":"` + id + `","Config":{"Labels":{"shhh.toolchain.key":"` + label + `"}}}]`)
	}
	if id, found, err := readInspect(inspect(testImgID, key), key); err != nil || !found || id != testImgID {
		t.Errorf("a docker image under its key = %q, %v, %v", id, found, err)
	}
	if id, found, err := readInspect(inspect(testPodman, key), key); err != nil || !found || id != testPodman {
		t.Errorf("a podman ID without its prefix = %q, %v, %v", id, found, err)
	}
	if _, found, err := readInspect(inspect(testImgID, "someone-else"), key); err != nil || found {
		t.Errorf("an image labelled with another key was reused: %v, %v", found, err)
	}
	if _, _, err := readInspect(inspect("--privileged", key), key); err == nil {
		t.Error("an ID that is not one was handed on")
	}
	if !isMissingImage([]byte("Error response from daemon: No such image: x")) || !isMissingImage([]byte("Error: x: image not known")) {
		t.Error("an engine's image-not-found reads as a failure")
	}
}

// stubPrepareEngine is an engine that logs every call and answers the
// preparation's verbs: an image inspect finds the image only once a commit
// has made it, and an exec whose words carry STUB_FAIL fails with output.
func stubPrepareEngine(t *testing.T, fail string) (Engine, string) {
	t.Helper()
	state := t.TempDir()
	log := filepath.Join(state, "calls")
	t.Setenv("STUB_LOG", log)
	t.Setenv("STUB_COMMITTED", filepath.Join(state, "committed"))
	t.Setenv("STUB_KEY", PreparedKey(testBase, testDecl))
	t.Setenv("STUB_ID", testImgID)
	t.Setenv("STUB_FAIL", fail)
	dir := stubEngine(t, "docker", `
echo "$*" >> "$STUB_LOG"
case "$1" in
  run) echo cafe;;
  exec)
    if [ -n "$STUB_FAIL" ]; then
      case "$*" in *"$STUB_FAIL"*) echo "go: downloading example.com/cmd"; echo "go: example.com/cmd@v9.9.9: unknown revision" >&2; exit 3;; esac
    fi;;
  commit) : > "$STUB_COMMITTED";;
  image)
    if [ -f "$STUB_COMMITTED" ]; then
      echo "[{\"Id\":\"$STUB_ID\",\"Config\":{\"Labels\":{\"shhh.toolchain.key\":\"$STUB_KEY\"}}}]"
    else
      echo "Error response from daemon: No such image: $3" >&2; exit 1
    fi;;
esac
`)
	return Engine{Name: "docker", Path: filepath.Join(dir, "docker"), OK: true}, log
}

func calls(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

func TestPrepareImageRunsTheLinesCommitsAndRemovesTheSetupContainer(t *testing.T) {
	eng, log := stubPrepareEngine(t, "")
	store := NewStoreAt(filepath.Join(t.TempDir(), "sandboxes.json"))
	tc := project.Toolchain{Packages: []string{"jq"}, Install: []string{"go install example.com/cmd@v1.2.3"}, Digest: testDecl}

	if _, found, err := LookupPrepared(context.Background(), eng, testBase, testDecl); err != nil || found {
		t.Fatalf("an image never prepared was found: %v, %v", found, err)
	}
	id, err := PrepareImage(context.Background(), eng, ContainerSpec{Image: testBase}, tc, []string{testBase}, store)
	if err != nil {
		t.Fatalf("PrepareImage: %v", err)
	}
	if id != testImgID {
		t.Errorf("PrepareImage = %q, want the committed image's ID %q", id, testImgID)
	}
	got := calls(t, log)
	var verbs []string
	for _, c := range got[1:] {
		verbs = append(verbs, strings.Fields(c)[0])
	}
	if want := []string{"run", "exec", "exec", "commit", "image", "rm"}; !slices.Equal(verbs, want) {
		t.Errorf("the preparation ran %v, want %v:\n%s", verbs, want, strings.Join(got, "\n"))
	}
	if recs, _ := store.List(); len(recs) != 0 {
		t.Errorf("the setup container's record outlived it: %+v", recs)
	}
	if again, found, err := LookupPrepared(context.Background(), eng, testBase, testDecl); err != nil || !found || again != testImgID {
		t.Errorf("the prepared image is not found again: %q, %v, %v", again, found, err)
	}
}

// A line that fails stops the preparation there: nothing after it runs,
// nothing is committed, and the refusal names the line and quotes its end.
func TestPrepareImageStopsAtTheFailingLineAndQuotesIt(t *testing.T) {
	eng, log := stubPrepareEngine(t, "@v9.9.9")
	store := NewStoreAt(filepath.Join(t.TempDir(), "sandboxes.json"))
	tc := project.Toolchain{
		Install: []string{"go install example.com/ok@v1.0.0", "go install example.com/cmd@v9.9.9", "go install example.com/after@v1.0.0"},
		Digest:  testDecl,
	}
	_, err := PrepareImage(context.Background(), eng, ContainerSpec{Image: testBase}, tc, nil, store)
	if err == nil {
		t.Fatal("a failing line prepared an image")
	}
	for _, want := range []string{"install[1] go install example.com/cmd@v9.9.9", "exit 3", "unknown revision", "go: downloading"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	joined := strings.Join(calls(t, log), "\n")
	if strings.Contains(joined, "after@") || strings.Contains(joined, "commit") {
		t.Errorf("the preparation went on past the failing line:\n%s", joined)
	}
	if !strings.Contains(joined, "rm --force shhh-prep-") {
		t.Errorf("the setup container was left behind:\n%s", joined)
	}
	if recs, _ := store.List(); len(recs) != 0 {
		t.Errorf("a failed setup container kept its record: %+v", recs)
	}
}

// The allowlist names bases, and a base it refuses is refused before the
// engine is asked anything.
func TestPrepareImageRefusesABaseThePolicyRefuses(t *testing.T) {
	eng, log := stubPrepareEngine(t, "")
	store := NewStoreAt(filepath.Join(t.TempDir(), "sandboxes.json"))
	tc := project.Toolchain{Packages: []string{"jq"}, Digest: testDecl}
	for name, base := range map[string]string{"unlisted": testBase, "a tag": "ghcr.io/acme/sandbox:latest"} {
		if _, err := PrepareImage(context.Background(), eng, ContainerSpec{Image: base}, tc, []string{"other" + testDigest}, store); err == nil {
			t.Errorf("%s base was prepared from", name)
		}
	}
	if got := calls(t, log); len(got) != 0 {
		t.Errorf("the engine was asked %v for a refused base", got)
	}
}

// The session's container runs the prepared image, and the base is still
// what the policy is asked about.
func TestTheSessionContainerRunsThePreparedImage(t *testing.T) {
	eng := Engine{Name: "docker", Path: "/usr/bin/docker", OK: true}
	s, err := ContainerSpec{Image: testBase, Prepared: testImgID, Workspace: t.TempDir()}.withDefaults()
	if err != nil {
		t.Fatal(err)
	}
	argv := createArgv(eng, "shhh-sbx-x", s)
	if got := argv[len(argv)-3]; got != testImgID {
		t.Errorf("the session runs %q, want the prepared %q", got, testImgID)
	}
	store := NewStoreAt(filepath.Join(t.TempDir(), "sandboxes.json"))
	if _, err := CreateContainer(context.Background(), eng, ContainerSpec{Image: testBase, Prepared: testImgID, Workspace: t.TempDir()}, []string{"other" + testDigest}, store); err == nil {
		t.Error("a prepared image ran over a base the allowlist refuses")
	}
	if _, err := CreateContainer(context.Background(), eng, ContainerSpec{Image: testBase, Prepared: "--privileged", Workspace: t.TempDir()}, nil, store); err == nil {
		t.Error("a prepared image that is not an ID reached the engine")
	}
}

func flagValues(argv []string, flag string) []string {
	var out []string
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			out = append(out, argv[i+1])
		}
	}
	return out
}
