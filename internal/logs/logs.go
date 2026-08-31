// Package logs is shhh's diagnostic log: the file a session appends what went
// wrong to, and the reader that tails it.
//
// It is not a transcript. A conversation is in the store and `shhh chats`
// shows it. This file holds the things that have no surface of their own — a
// provider failure that has already scrolled past, and whatever a library
// writes to the standard logger while the alternate screen is up, which would
// otherwise be painted over the session.
// See docs/capabilities/configuration.md#a-failure-is-written-down.
//
// Nothing here knows where shhh keeps its state: the path is handed in, so
// the layout is decided in one place beside the store's own
// (internal/cli/logs.go). That is also what keeps this package a leaf — the
// provider that writes to it is in the store's own import graph.
package logs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MaxBytes is how large the log grows before the file is set aside and a
// fresh one started. One generation is kept: a diagnostic that fills a disk
// is a fault of its own, and nobody debugging this morning's failure reads
// back past the last few megabytes.
const MaxBytes = 4 << 20

// FollowInterval is how often a follow looks for more. It is a poll rather
// than a filesystem watch because the file is written by other processes,
// often on a filesystem that has no watches to give.
const FollowInterval = 200 * time.Millisecond

// dest is where records go. It starts discarding and becomes a file when To
// names one, so logger below can be built once, handed out immediately, and
// still write to the right place afterwards.
var dest = &sink{}

// logger is the process's log. It is a fixed value so that a caller holding
// it races with nothing; the switching happens under dest.
var logger = slog.New(slog.NewTextHandler(dest, &slog.HandlerOptions{Level: slog.LevelInfo}))

// Logger is what a seam writes through. Until To has named a file it
// discards, which is the right answer for a test and for a command that
// never touches the state directory.
func Logger() *slog.Logger { return logger }

// To sends this process's log to path, and makes it the destination of the
// standard loggers as well. That second half is what stops a dependency's
// stray log line from landing on top of a running session: slog.SetDefault
// re-points the log package too, so a library that writes to either one
// writes here instead of over the screen.
//
// An empty path discards, which is what a process that cannot name a state
// directory gets, and what a test resets to.
func To(path string) {
	dest.use(path)
	slog.SetDefault(logger)
}

// sink is the log file. It is opened for one record and closed again rather
// than held for the length of a session, and the three things that buys are
// each the reason on their own.
//
// The generation check happens per record, so MaxBytes is a bound rather
// than a reading taken once at startup: a session that spends an hour being
// refused by an overloaded provider cannot grow the file past it.
//
// Two sessions share one file. A session holding a handle would keep writing
// into the generation another one had already set aside — a file no reader
// looks at, which the next rotation then deletes out from under it — so the
// failure you are tailing for would be the one that went missing.
//
// And a directory that could not be written to a minute ago can be written
// to now. Holding the first failure against every later one would mean a
// blip on a network mount costs a session its whole log.
//
// A record costs an open, a stat, a write and a close. It is written because
// a request to a provider failed, so it is by some distance the cheapest
// thing that happened on that path.
type sink struct {
	mu   sync.Mutex
	path string
}

func (s *sink) use(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.path = path
}

// Write appends one record, and drops it rather than reporting a failure to
// write it. There is nowhere to report one to: the screen belongs to the
// session, and stderr is the screen. Nor is the fault silent — the store
// lives in this same directory, and its row in `shhh doctor` is what fails,
// loudly, when the directory cannot be used at all.
func (s *sink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return len(p), nil
	}
	if f, err := open(s.path); err == nil {
		_, _ = f.Write(p)
		f.Close()
	}
	return len(p), nil
}

// open readies the log for one record: the directory, the generation check,
// and a handle in append mode. O_APPEND is what lets two sessions share one
// file without either keeping an offset — every write lands at the end of
// whatever is at that path now.
func open(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Stat(path); err == nil && info.Size() >= MaxBytes {
		// A failed rename is not worth dropping the record over: the file
		// goes on growing, which is worse than a bounded one and better than
		// no log at all.
		_ = os.Rename(path, path+".1")
	}
	// The log carries the provider's own words about a refused request, which
	// can be whatever the provider chose to echo back. It is 0600 and it sits
	// inside the containment deny mask with the rest of shhh's state, so an
	// approved command cannot read it either.
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
}

// Tail returns the last n lines of the file at path, and the offset it read
// to — which is where a follow carries on from, so that a line written
// between the two is neither printed twice nor missed.
//
// At most MaxBytes is read, from the end: the file is bounded there, so this
// is the whole of it in every case that matters and stays bounded in the one
// where an earlier build left a larger one behind.
func Tail(path string, n int) ([]string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, 0, err
	}
	if n <= 0 {
		return nil, size, nil
	}

	from := int64(0)
	if size > MaxBytes {
		from = size - MaxBytes
	}
	buf := make([]byte, size-from)
	read, err := f.ReadAt(buf, from)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, err
	}
	// Something truncated the file between the seek and the read, and the
	// rest of the buffer is zeros that were never in it. The count is what
	// arrived, and it is also where a follow has to resume from — the offset
	// the seek reported is past the end of what is there now.
	buf = buf[:read]
	end := from + int64(read)
	if from > 0 {
		// The window opened in the middle of a line, and half a line is
		// worse than one line fewer.
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		} else {
			buf = nil
		}
	}

	body := strings.TrimSuffix(string(buf), "\n")
	if body == "" {
		return nil, end, nil
	}
	lines := strings.Split(body, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, end, nil
}

// Follow copies everything appended to path after from into w, until ctx is
// done. A file that does not exist yet is waited for rather than refused:
// following before the first failure is the whole point of following.
func Follow(ctx context.Context, path string, from int64, w io.Writer) error {
	tick := time.NewTicker(FollowInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		at, err := copyFrom(path, from, w)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		from = at
	}
}

// copyFrom writes the bytes of path after off to w and reports the offset it
// reached. A file shorter than off was set aside and replaced under us, so
// the read restarts at its beginning rather than skipping what replaced it.
func copyFrom(path string, off int64, w io.Writer) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return off, err
	}
	defer f.Close()

	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return off, err
	}
	if size < off {
		off = 0
	}
	if size == off {
		return off, nil
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return off, err
	}
	n, err := io.Copy(w, f)
	return off + n, err
}
