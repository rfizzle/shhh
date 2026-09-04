package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/logs"
)

// TestMain gives the whole package a home of its own. Every test that builds
// the root command runs its PersistentPreRunE, which resolves this process's
// log, its config and its provider profiles against whatever home the
// machine has — and this process is one test binary shared by every test in
// the package. Without the state directory, a test that made a provider
// classify an error would append to the log of whoever is running the suite.
// Without the config directory, the suite reads the settings of whoever is
// running it: a default provider, a model, an MCP server, a rounds cap
// someone set for their own sessions all become inputs to tests that never
// asked for them, and a `[provider]` block that happens to be malformed
// fails eighteen tests that have nothing to do with configuration.
//
// Both paths are set because config.Paths reads both: XDG_CONFIG_HOME when
// it is set, and ~/.config/shhh either way. It also matters that the config
// directory is one shhh's own sandbox masks (internal/sandbox) — a suite run
// inside it cannot read the developer's config even to be tainted by it, so
// the test that reads it fails only on the machines that sandbox their
// tests, which is the worst way to find out.
//
// It also pins the zone. A report prints a timestamp in local time, so a
// fixture recorded in one zone reads as a different clock time in another —
// the goldens would pass where they were written and fail on CI, which runs
// in UTC. Pinning makes the checked-in text mean the same thing everywhere.
func TestMain(m *testing.M) {
	time.Local = time.UTC
	dir, err := os.MkdirTemp("", "shhh-cli-test")
	if err != nil {
		panic(err)
	}
	// Before the home moves, because the Go build cache lives under the real
	// one: a link that cannot find it compiles the module from scratch every
	// time this package runs (buildShhhBinary, print_integration_test.go).
	buildShhhBinary(dir)
	for _, key := range []string{"HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME"} {
		if err := os.Setenv(key, dir); err != nil {
			panic(err)
		}
	}
	code := m.Run()
	// Best effort: the run is over and the temp directory is the operating
	// system's to sweep if this cannot.
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func writeLog(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	must(t, err)
	for _, line := range lines {
		_, err := f.WriteString(line + "\n")
		must(t, err)
	}
	must(t, f.Close())
}

// The flags are the acceptance: `-n` and `-f` are what a reader who knows
// tail already has in their fingers, and a bare `shhh logs` prints a
// thousand lines.
func TestLogsOffersTheFlagsTailDoes(t *testing.T) {
	flags := newLogsCmd().Flags()
	for _, c := range []struct{ name, short, value string }{
		{"tail", "n", "1000"},
		{"follow", "f", "false"},
	} {
		f := flags.Lookup(c.name)
		if f == nil {
			t.Fatalf("`shhh logs` has no --%s", c.name)
		}
		if f.Shorthand != c.short {
			t.Errorf("--%s is -%s, want -%s", c.name, f.Shorthand, c.short)
		}
		if f.DefValue != c.value {
			t.Errorf("--%s defaults to %s, want %s", c.name, f.DefValue, c.value)
		}
	}
}

func TestLogsPrintsAThousandLinesWhenNobodySaysOtherwise(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.log")
	lines := make([]string, 0, 1200)
	for i := 1; i <= 1200; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	writeLog(t, path, lines...)

	var out strings.Builder
	must(t, runLogs(context.Background(), &out, path, logTailDefault, false))

	got := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(got) != 1000 {
		t.Fatalf("a bare `shhh logs` printed %d lines, want 1000", len(got))
	}
	if got[0] != "line 201" || got[999] != "line 1200" {
		t.Errorf("the thousand lines run %s … %s", got[0], got[999])
	}
}

func TestLogsPrintsTheLastLinesAndNothingElse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.log")
	writeLog(t, path, "one", "two", "three", "four", "five")

	var out strings.Builder
	must(t, runLogs(context.Background(), &out, path, 3, false))

	if got := out.String(); got != "three\nfour\nfive\n" {
		t.Errorf("`shhh logs --tail 3` printed %q", got)
	}
}

// The log is bytes a person greps: nothing is added around the lines, so a
// pipe gets what was written and no title, rule or tally.
func TestLogsAddsNothingToTheLinesItPrints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.log")
	writeLog(t, path, `time=2026-08-31T09:00:00.000Z level=ERROR msg="provider request refused" provider=anthropic`)

	var out strings.Builder
	must(t, runLogs(context.Background(), &out, path, logTailDefault, false))

	if strings.Contains(out.String(), "shhh logs") || strings.Contains(out.String(), "·") {
		t.Errorf("the tail was dressed as a listing: %q", out.String())
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Errorf("one line in gave %q", out.String())
	}
}

func TestLogsFollowSeesALineAppendedWhileItRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.log")
	writeLog(t, path, "before")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &syncWriter{}
	done := make(chan error, 1)
	go func() { done <- runLogs(ctx, out, path, logTailDefault, true) }()

	// The tail lands first, then the line that arrives after it — which is
	// the pane you leave open while you run the thing that fails.
	waitForLogs(t, func() bool { return strings.Contains(out.String(), "before\n") })
	writeLog(t, path, "after")
	waitForLogs(t, func() bool { return strings.Contains(out.String(), "after\n") })

	if got := out.String(); got != "before\nafter\n" {
		t.Errorf("a follow printed %q", got)
	}
	cancel()
	must(t, <-done)
}

// Following a log that does not exist yet is the ordinary case: you open the
// pane before the request fails.
func TestLogsFollowWaitsForALogThatIsNotThereYet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.log")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &syncWriter{}
	done := make(chan error, 1)
	go func() { done <- runLogs(ctx, out, path, logTailDefault, true) }()

	writeLog(t, path, "the first failure")
	waitForLogs(t, func() bool { return strings.Contains(out.String(), "the first failure\n") })
	cancel()
	must(t, <-done)
}

// `--tail 0` prints nothing, the way tail does, and that is not the same
// answer as a log with nothing in it.
func TestLogsAskedForNoneOfThePastSaysNothingAtAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.log")
	writeLog(t, path, "one", "two")

	var out strings.Builder
	must(t, runLogs(context.Background(), &out, path, 0, false))
	if out.String() != "" {
		t.Errorf("`shhh logs --tail 0` printed %q", out.String())
	}
}

func TestLogsSaysWhenThereIsNothingToShow(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "empty.log"), nil, 0o600))
	for _, c := range []struct {
		name string
		path string
	}{
		{"no file at all", filepath.Join(dir, "absent.log")},
		{"a file with nothing in it", filepath.Join(dir, "empty.log")},
	} {
		var out strings.Builder
		must(t, runLogs(context.Background(), &out, c.path, logTailDefault, false))
		if !strings.Contains(out.String(), "⊘ nothing has been written to the log") {
			t.Errorf("%s: %q", c.name, out.String())
		}
		if !strings.Contains(out.String(), filepath.Base(c.path)) {
			t.Errorf("%s: the empty state does not say where it looked: %q", c.name, out.String())
		}
	}
}

// The doctor row and the command have to name the same file, or the check
// sends the reader to a path they cannot tail.
func TestTheDoctorNamesTheLogTheReaderOpens(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	path, err := logPath()
	must(t, err)

	got := doctorLogs(path, 2048, nil)
	if !strings.Contains(got.Subject, filepath.Base(path)) {
		t.Errorf("the log row names %q, the command opens %q", got.Subject, path)
	}
	if got.Detail != "2 kB" {
		t.Errorf("the row says %q about a 2048-byte log", got.Detail)
	}

	// A machine where nothing has gone wrong has no file, and that is not a
	// fault: the row still names where one would be written.
	if fresh := doctorLogs(path, 0, nil); fresh.Outcome != "ok" || fresh.Detail != "nothing recorded yet" {
		t.Errorf("an unwritten log reads as %q · %q", fresh.Outcome, fresh.Detail)
	}
	if broken := doctorLogs("", 0, os.ErrPermission); broken.Outcome != "nowhere" || broken.Consequence == "" {
		t.Errorf("a log with nowhere to go reads as %q with no consequence", broken.Outcome)
	}
}

// The log is a leaf: the package the provider writes through must not reach
// back into the state directory, which is in the provider's own import graph.
func TestTheLogPathIsJoinedHereAndNotInTheLogPackage(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	path, err := logPath()
	must(t, err)
	if filepath.Base(path) != "shhh.log" {
		t.Errorf("the log is at %q", path)
	}
	if filepath.Dir(path) != filepath.Dir(doctorStorePath()) {
		t.Errorf("the log at %q is not beside the store at %q", path, doctorStorePath())
	}
}

func waitForLogs(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(logs.FollowInterval / 4)
	}
	t.Fatalf("the follow never caught up")
}

// syncWriter is written by the follow's goroutine and read by the test.
type syncWriter struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}
