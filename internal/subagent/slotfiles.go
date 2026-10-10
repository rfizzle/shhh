package subagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// SlotDirEnv names the directory whose lock files are the check slots of every
// process that honours it: a parallel sprint sets it on each stage it starts,
// so the stages' builds and the sprint's own gates take turns across
// processes the way a session's children take turns inside one. Unset, a
// process throttles itself alone.
// See docs/capabilities/subagents.md#what-they-share.
const SlotDirEnv = "SHHH_CHECK_SLOT_DIR"

// SlotLaneEnv names the lane a process stands in, which a wait on the
// directory's slots is written under so the sprint can say which lane waits.
const SlotLaneEnv = "SHHH_CHECK_SLOT_LANE"

const (
	// slotPoll is how often a process waiting for a slot tries the locks
	// again: the kernel hands a lock to no one in particular, so the wait is
	// a short poll rather than a park.
	slotPoll = 100 * time.Millisecond
	// waitStale is how old a wait marker may grow before it is read as left
	// by a process that died; a live wait rewrites its marker every poll.
	waitStale  = 3 * time.Second
	waitPrefix = "wait-"
)

// FileSlots is size check slots held as lock files under a directory. A lock
// belongs to the open file, so the kernel gives it back when the process that
// held it ends however it ends, and no slot is left behind by a crash.
type FileSlots struct {
	dir  string
	size int
	lane string
}

// OpenFileSlots is the directory's slots, size of them (<= 0 is
// DefaultCheckSlots), for the lane named; the directory is made when a slot
// is first taken.
func OpenFileSlots(dir string, size int, lane string) *FileSlots {
	if size <= 0 {
		size = DefaultCheckSlots
	}
	return &FileSlots{dir: dir, size: size, lane: lane}
}

// ForLane is these slots as the lane named takes them.
func (f *FileSlots) ForLane(lane string) *FileSlots {
	c := *f
	c.lane = lane
	return &c
}

// Dir is the directory the slots are locked in.
func (f *FileSlots) Dir() string { return f.dir }

var waitSeq atomic.Int64

// Take takes a slot, polling while every one is held. waiting, when set, is
// told how many checks are running as the wait begins and only when there is
// one. It answers the release, and false where ctx ended first. A directory
// that cannot be used throttles nothing rather than stopping the check.
// While it waits, the lane's wait is written beside the locks for the sprint
// to read (SlotWaits).
func (f *FileSlots) Take(ctx context.Context, waiting func(running int)) (release func(), ok bool) {
	if f == nil {
		return func() {}, true
	}
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		return func() {}, true
	}
	var marker string
	defer func() {
		if marker != "" {
			_ = os.Remove(marker)
		}
	}()
	for {
		if rel, got := f.tryTake(); got {
			return rel, true
		}
		if marker == "" {
			marker = filepath.Join(f.dir, waitPrefix+strconv.Itoa(os.Getpid())+"-"+strconv.FormatInt(waitSeq.Add(1), 10))
			if waiting != nil {
				waiting(f.size)
			}
		}
		_ = os.WriteFile(marker, []byte(f.lane+"\n"+strconv.Itoa(f.size)+"\n"), 0o644)
		select {
		case <-ctx.Done():
			return nil, false
		case <-time.After(slotPoll):
		}
	}
}

// tryTake locks the first free slot file and answers the release; false is
// every slot held. A slot file that cannot be opened or locked is a slot that
// throttles nothing.
func (f *FileSlots) tryTake() (func(), bool) {
	for i := range f.size {
		file, err := os.OpenFile(filepath.Join(f.dir, fmt.Sprintf("slot-%d", i)), os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return func() {}, true
		}
		locked, err := lockFile(file)
		if err != nil {
			_ = file.Close()
			return func() {}, true
		}
		if locked {
			return func() { unlockFile(file); _ = file.Close() }, true
		}
		_ = file.Close()
	}
	return nil, false
}

// SlotWaits reads which lanes are waiting for a slot under dir, and how many
// checks were running as each began to wait. A marker nobody has rewritten
// lately is a wait that ended with its process.
func SlotWaits(dir string) map[string]int {
	waits := map[string]int{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return waits
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), waitPrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) > waitStale {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		lane, running, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")
		n, _ := strconv.Atoi(running)
		if lane != "" {
			waits[lane] = max(waits[lane], n)
		}
	}
	return waits
}
