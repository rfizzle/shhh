// Package ci holds the tests that keep docker/ci/Dockerfile saying what
// .github/workflows/test.yml installs. The image is a replica of the CI job;
// a pin that moves in one and not the other makes the replica pass what CI
// fails, which is the failure it exists to prevent.
package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readRepo(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func match(t *testing.T, src, pattern, what string) string {
	t.Helper()
	m := regexp.MustCompile(pattern).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("no %s found (pattern %s)", what, pattern)
	}
	return m[1]
}

// TestCI_TheGoVersionIsNamedOnce reads the version `make prepush` pins, the
// Dockerfile's ARG GO_VERSION and go.mod's go line, and fails unless the three
// agree. CI resolves the same version through go-version-file: go.mod, which
// the test also requires the workflow to still use.
func TestCI_TheGoVersionIsNamedOnce(t *testing.T) {
	mod := match(t, readRepo(t, "go.mod"), `(?m)^go (\S+)$`, "go line in go.mod")
	docker := match(t, readRepo(t, "docker/ci/Dockerfile"), `(?m)^ARG GO_VERSION=(\S+)$`, "ARG GO_VERSION")
	if docker != mod {
		t.Errorf("Dockerfile ARG GO_VERSION is %s, go.mod says %s", docker, mod)
	}
	if mk, err := exec.LookPath("make"); err == nil {
		cmd := exec.Command(mk, "-s", "--no-print-directory", "-C", filepath.Join("..", ".."), "prepush-go")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("make prepush-go: %v", err)
		}
		if got := strings.TrimSpace(string(out)); got != "go"+mod {
			t.Errorf("make prepush pins %s, go.mod says go%s", got, mod)
		}
	}
	if !strings.Contains(readRepo(t, ".github/workflows/test.yml"), "go-version-file: go.mod") {
		t.Error("the workflow no longer reads the Go version from go.mod")
	}
}

// TestCIImage_PinsWhatTheWorkflowInstalls compares the tools the workflow
// installs with the ones the Dockerfile installs.
func TestCIImage_PinsWhatTheWorkflowInstalls(t *testing.T) {
	wf := readRepo(t, ".github/workflows/test.yml")
	df := readRepo(t, "docker/ci/Dockerfile")

	wfLint := match(t, wf, `golangci-lint/v2/cmd/golangci-lint@(\S+)`, "golangci-lint install in test.yml")
	dfLint := match(t, df, `(?m)^ARG GOLANGCI_LINT_VERSION=(\S+)$`, "ARG GOLANGCI_LINT_VERSION")
	if wfLint != dfLint {
		t.Errorf("golangci-lint: test.yml installs %s, Dockerfile %s", wfLint, dfLint)
	}
	if !strings.Contains(df, "golangci-lint@${GOLANGCI_LINT_VERSION}") {
		t.Error("the Dockerfile does not install golangci-lint at its ARG")
	}

	wfImports := match(t, wf, `golang.org/x/tools/cmd/goimports@(\S+)`, "goimports install in test.yml")
	dfImports := match(t, df, `golang.org/x/tools/cmd/goimports@(\S+)`, "goimports install in the Dockerfile")
	if wfImports != dfImports {
		t.Errorf("goimports: test.yml installs @%s, Dockerfile @%s", wfImports, dfImports)
	}

	for _, pkg := range []string{"tmux", "python3", "bubblewrap"} {
		if !strings.Contains(df, pkg) {
			t.Errorf("the Dockerfile does not install %s, which test.yml's ci job needs", pkg)
		}
	}
}

// TestCI_TheImageJobsBuildWhatTheDockerfileCopies reads the test workflow's
// sandbox-image job, the release workflow's image job and the sandbox
// Dockerfile, and fails unless both jobs build the binary for every
// architecture under the path the Dockerfile copies from, with the same build
// command, from the Go version go.mod names.
func TestCI_TheImageJobsBuildWhatTheDockerfileCopies(t *testing.T) {
	df := readRepo(t, "docker/sandbox/Dockerfile")
	copied := match(t, df, `(?m)^COPY (bin/linux-\$\{TARGETARCH\}/shhh) `, "COPY of the shhh binary in the sandbox Dockerfile")

	build := regexp.MustCompile(`for arch in ([a-z0-9 ]+); do\s*(CGO_ENABLED=0 GOOS=linux GOARCH=\$arch go build[^\n]*\\\s*-o (\S+) \./cmd/shhh)\s*done`)
	jobs := []struct{ file, job string }{
		{".github/workflows/test.yml", "sandbox-image"},
		{".github/workflows/release.yml", "image"},
	}
	var commands []string
	for _, j := range jobs {
		wf := readRepo(t, j.file)
		start := regexp.MustCompile(`(?m)^  ` + j.job + `:\n`).FindStringIndex(wf)
		if start == nil {
			t.Fatalf("%s has no %s job", j.file, j.job)
		}
		body := wf[start[1]:]
		if end := regexp.MustCompile(`(?m)^  \S`).FindStringIndex(body); end != nil {
			body = body[:end[0]]
		}
		if !strings.Contains(body, "go-version-file: go.mod") {
			t.Errorf("%s: the %s job does not set up Go from go.mod", j.file, j.job)
		}
		if !strings.Contains(body, "context: docker/sandbox") {
			t.Errorf("%s: the %s job does not build the image from docker/sandbox, where the binary lands", j.file, j.job)
		}
		m := build.FindStringSubmatch(body)
		if m == nil {
			t.Errorf("%s: the %s job does not build the shhh binary per architecture", j.file, j.job)
			continue
		}
		commands = append(commands, strings.Join(strings.Fields(m[2]), " "))
		built := strings.NewReplacer("$arch", "${TARGETARCH}", "docker/sandbox/", "").Replace(m[3])
		if built != copied {
			t.Errorf("%s: the %s job builds %s, the Dockerfile copies %s", j.file, j.job, m[3], copied)
		}
		if got := strings.Join(strings.Fields(m[1]), " "); got != "amd64 arm64" {
			t.Errorf("%s: the %s job builds %q, the image ships amd64 arm64", j.file, j.job, got)
		}
	}
	if len(commands) == 2 && commands[0] != commands[1] {
		t.Errorf("the two jobs build with different commands:\n%s\n%s", commands[0], commands[1])
	}
}
