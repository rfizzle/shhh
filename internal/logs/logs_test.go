package logs

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// write appends lines to path, the way a session does.
func write(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	must(t, err)
	for _, line := range lines {
		_, err := f.WriteString(line + "\n")
		must(t, err)
	}
	must(t, f.Close())
}

func TestTailReturnsTheLastLinesAndWhereItStopped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.log")
	write(t, path, "one", "two", "three", "four")

	lines, at, err := Tail(path, 3)
	must(t, err)
	if got := strings.Join(lines, ","); got != "two,three,four" {
		t.Errorf("the last three lines are %q", got)
	}
	info, err := os.Stat(path)
	must(t, err)
	if at != info.Size() {
		t.Errorf("the tail stopped at %d, the file is %d long", at, info.Size())
	}

	// Asking for more than there is gives everything, not an error.
	lines, _, err = Tail(path, 100)
	must(t, err)
	if len(lines) != 4 {
		t.Errorf("a tail longer than the file gave %d lines, want 4", len(lines))
	}
}

func TestTailOfNothingIsNoLinesRatherThanOneEmptyOne(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shhh.log")
	must(t, os.WriteFile(path, nil, 0o600))

	lines, at, err := Tail(path, 10)
	must(t, err)
	if len(lines) != 0 || at != 0 {
		t.Errorf("an empty file tailed to %d lines at offset %d, want none at 0", len(lines), at)
	}

	// Nothing to print at all is the caller's decision to explain, so it
	// gets the filesystem's own answer rather than an empty result that
	// cannot be told from an empty file.
	if _, _, err := Tail(filepath.Join(dir, "absent.log"), 10); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing log tailed to %v, want a not-exist error", err)
	}
}

// A tail of zero is where a follow that wants only new lines starts, and it
// still has to report the offset or the follow would replay the file.
func TestTailOfZeroPrintsNothingAndStillFindsTheEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.log")
	write(t, path, "one", "two")

	lines, at, err := Tail(path, 0)
	must(t, err)
	if len(lines) != 0 {
		t.Errorf("a tail of zero printed %d lines", len(lines))
	}
	if at != 8 {
		t.Errorf("a tail of zero stopped at %d, want the end of the file at 8", at)
	}
}

// A window that opens mid-line drops that half-line: the file is bounded at
// MaxBytes, so this only happens to one an earlier build left larger.
func TestTailNeverReturnsHalfALine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.log")
	long := strings.Repeat("x", 1024)
	f, err := os.Create(path)
	must(t, err)
	for written := 0; written < MaxBytes+4096; written += len(long) + 1 {
		_, err := f.WriteString(long + "\n")
		must(t, err)
	}
	must(t, f.Close())

	lines, _, err := Tail(path, 100000)
	must(t, err)
	for i, line := range lines {
		if len(line) != len(long) {
			t.Fatalf("line %d of the window is %d characters, want a whole one of %d", i, len(line), len(long))
		}
	}
}

func TestFollowSeesALineAppendedAfterIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.log")
	write(t, path, "before")

	_, at, err := Tail(path, 10)
	must(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- Follow(ctx, path, at, out) }()

	write(t, path, "after")
	waitFor(t, func() bool { return strings.Contains(out.String(), "after\n") })

	// What the tail already printed is not printed again.
	if strings.Contains(out.String(), "before") {
		t.Errorf("the follow replayed what the tail had shown: %q", out.String())
	}
	cancel()
	must(t, <-done)
}

// The pane you start a follow in is usually open before the failure that
// creates the file, so a log that is not there yet is waited for.
func TestFollowWaitsForALogThatDoesNotExistYet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.log")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- Follow(ctx, path, 0, out) }()

	write(t, path, "the first failure")
	waitFor(t, func() bool { return strings.Contains(out.String(), "the first failure\n") })
	cancel()
	must(t, <-done)
}

// A file set aside at MaxBytes leaves a follow holding an offset past the
// end of its replacement, and the replacement is what it should print.
func TestFollowRestartsWhenTheFileIsReplacedUnderIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.log")
	write(t, path, "a long first generation of the log")
	_, at, err := Tail(path, 10)
	must(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- Follow(ctx, path, at, out) }()

	must(t, os.Rename(path, path+".1"))
	write(t, path, "new")
	waitFor(t, func() bool { return strings.Contains(out.String(), "new\n") })
	cancel()
	must(t, <-done)
}

func TestLoggerDiscardsUntilAFileIsNamed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shhh.log")
	t.Cleanup(func() { To("") })

	To("")
	Logger().Error("nothing should come of this")
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("a log with no file wrote %v (%v)", entries, err)
	}

	// Naming a file still creates nothing: a command that logs nothing
	// leaves no file for `shhh logs` to show as empty.
	To(path)
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("naming the log created it before anything was written (%v)", err)
	}

	Logger().Error("provider request refused", "provider", "anthropic")
	body, err := os.ReadFile(path)
	must(t, err)
	if !strings.Contains(string(body), "provider request refused") ||
		!strings.Contains(string(body), "provider=anthropic") {
		t.Errorf("the record did not reach the file: %q", body)
	}
}

func TestAFullLogIsSetAsideRatherThanGrowing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shhh.log")
	must(t, os.WriteFile(path, bytes.Repeat([]byte("x"), MaxBytes), 0o600))
	t.Cleanup(func() { To("") })

	To(path)
	Logger().Error("the first record of the new generation")

	info, err := os.Stat(path)
	must(t, err)
	if info.Size() >= MaxBytes {
		t.Errorf("the full log kept growing: %d bytes", info.Size())
	}
	if old, err := os.Stat(path + ".1"); err != nil || old.Size() != MaxBytes {
		t.Errorf("the generation before it was not set aside (%v)", err)
	}
}

// A log that cannot be opened is not worth taking a session down for, and
// the one thing it must never do is fall back to the screen. It must also
// not give up: the record is dropped, and the next one tries again, because
// a directory that could not be written to a minute ago can be now.
func TestALogThatCannotBeOpenedIsSilentAndTriesAgain(t *testing.T) {
	t.Cleanup(func() { To("") })
	dir := t.TempDir()
	blocked := filepath.Join(dir, "in-the-way")
	must(t, os.WriteFile(blocked, nil, 0o600))
	path := filepath.Join(blocked, "shhh.log")

	var screen bytes.Buffer
	stderr := os.Stderr
	r, w, err := os.Pipe()
	must(t, err)
	os.Stderr = w
	To(path)
	Logger().Error("this has nowhere to go")
	must(t, w.Close())
	os.Stderr = stderr
	_, err = screen.ReadFrom(r)
	must(t, err)

	if screen.Len() != 0 {
		t.Errorf("a log with nowhere to write fell back to the screen: %q", screen.String())
	}
	if _, err := os.Stat(path); err == nil {
		t.Errorf("something was created at %q", path)
	}

	// The obstruction is removed, and the next record goes in. A sink that
	// remembered the first failure would leave this session with no log at
	// all over a blip on a network mount.
	must(t, os.Remove(blocked))
	Logger().Error("the log is reachable again")
	body, err := os.ReadFile(path)
	must(t, err)
	if !strings.Contains(string(body), "the log is reachable again") {
		t.Errorf("the sink gave up after one failure: %q", body)
	}
	if strings.Contains(string(body), "this has nowhere to go") {
		t.Errorf("the dropped record came back: %q", body)
	}
}

// The generation check runs per record, not once at startup: a session that
// spends an hour being refused cannot grow the file past the bound.
func TestTheBoundHoldsForRecordsAfterTheFirst(t *testing.T) {
	t.Cleanup(func() { To("") })
	path := filepath.Join(t.TempDir(), "shhh.log")
	To(path)

	Logger().Error("the first record, on a small file")
	must(t, os.WriteFile(path, bytes.Repeat([]byte("x"), MaxBytes), 0o600))
	Logger().Error("the record that finds it full")

	info, err := os.Stat(path)
	must(t, err)
	if info.Size() >= MaxBytes {
		t.Errorf("a session that had already opened the log kept growing it: %d bytes", info.Size())
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("the full generation was not set aside (%v)", err)
	}
}

// Two sessions share one file, and neither may keep writing into a
// generation the other set aside.
func TestARecordFollowsTheFileTheReaderIsLookingAt(t *testing.T) {
	t.Cleanup(func() { To("") })
	path := filepath.Join(t.TempDir(), "shhh.log")
	To(path)

	Logger().Error("the first session says something")
	// Another process rotates, the way this one would have.
	must(t, os.Rename(path, path+".1"))
	Logger().Error("and says something after the rotation")

	body, err := os.ReadFile(path)
	must(t, err)
	if !strings.Contains(string(body), "after the rotation") {
		t.Errorf("the record went to the generation nobody reads: %q", body)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(FollowInterval / 4)
	}
	t.Fatal("the follow never caught up")
}

// syncBuffer is a buffer the follow's goroutine writes to and the test reads
// from, which is two goroutines and therefore a lock.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// must fails the test on an error from setting it up.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
