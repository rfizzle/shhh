package cli

// The "once" behind the keymap-change notice (internal/ui/chat/keysnotice.go).
// A release that moves keys says so on the first launch after the upgrade and
// then never again; what makes it once is a generation number in the data
// directory. The generation is bumped by the release that rebinds, not per
// key — one notice per rebinding release, however many keys it moved.
//
// The notice is owed only to a hand that learned the old keys. A person
// opening shhh for the first time learned none, so their first screen does not
// open on an announcement about keys they never had.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/rfizzle/shhh/internal/storage"
)

// keymapGeneration numbers the most recent release that moved keys. Bump it
// when a release rebinds again, and the notice shows once more.
const keymapGeneration = 2

const keymapMarkerFile = "keymap_notice"

// keymapLaunch is what the data directory says about this launch: whether it
// is the machine's first run of shhh, and whether the keymap notice is owed.
// The two are one reading because they are answered from the same marker, and
// a caller that asked them separately would find the marker already written
// by the first question when it asked the second.
type keymapLaunch struct {
	firstRun  bool
	noticeDue bool
}

// keymapNoticeDue reports whether this launch should carry the notice, and
// records that it did.
func keymapNoticeDue() bool {
	return readKeymapLaunch().noticeDue
}

// readKeymapLaunch reads the marker in the data directory and brings it up to
// date. A machine whose data directory cannot be resolved or written shows
// nothing: a notice that cannot be marked seen would show on every launch,
// which teaches the reader to stop reading notices.
func readKeymapLaunch() keymapLaunch {
	dir, err := storage.Dir()
	if err != nil {
		return keymapLaunch{}
	}
	return keymapLaunchIn(dir, storeBeforeLaunch())
}

// keymapLaunchIn is the reading against one directory. A marker at or past
// this binary's generation owes nothing. A missing marker is a first run only
// where the directory held no store before this launch: nothing was learned,
// and the marker is written quietly. A missing marker beside a store is an
// install from before the marker existed, whose hand did learn the old keys,
// and a marker behind the generation is an upgrade across a rebind; both are
// owed the notice once.
func keymapLaunchIn(dir string, storeExisted bool) keymapLaunch {
	path := filepath.Join(dir, keymapMarkerFile)
	marked := false
	if data, err := os.ReadFile(path); err == nil {
		marked = true
		if seen, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && seen >= keymapGeneration {
			return keymapLaunch{}
		}
	}
	first := !marked && !storeExisted
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return keymapLaunch{firstRun: first}
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(keymapGeneration)+"\n"), 0o644); err != nil {
		return keymapLaunch{firstRun: first}
	}
	return keymapLaunch{firstRun: first, noticeDue: !first}
}

// storeHeld is whether the data directory held a store when this process
// first went to open it. It has to be read before the open, because the open
// creates the file and would make every first launch look like an old one.
var storeHeld struct {
	once    sync.Once
	existed bool
}

// noteStoreBeforeOpen takes that reading, once per process: openStore calls
// it ahead of storage.Open, and storeBeforeLaunch for a launch that has not
// opened the store yet.
func noteStoreBeforeOpen() {
	storeHeld.once.Do(func() {
		dir, err := storage.Dir()
		if err != nil {
			return
		}
		_, err = os.Stat(filepath.Join(dir, "shhh.db"))
		storeHeld.existed = err == nil
	})
}

func storeBeforeLaunch() bool {
	noteStoreBeforeOpen()
	return storeHeld.existed
}
