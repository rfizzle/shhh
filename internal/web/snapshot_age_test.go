//go:build hostlists

package web

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// snapshotAgeBound is how old a shipped snapshot may be when the runner
// checks it: a third of the 180-day window a snapshot stops answering at
// (hostSources' window for tranco and disposable), and well past the week the
// download refreshes a list on, so a binary installed from a release a few
// months old still has a floor that answers.
const snapshotAgeBound = 60 * day

// The shipped snapshots are the floor under the per-list download — first
// run, offline, and the decision path that never waits — and nothing else
// makes anyone regenerate them. This is a date check and not a fact about a
// change, so it runs in `make ci` and the release workflow through
// `make host-lists-check`, and never in the hermetic gate: a checkout must not
// go red on a quiet month. When it fails, `make host-lists` rewrites both.
func TestShippedHostSnapshotsAreFresh(t *testing.T) {
	now := time.Now()
	shipped := 0
	for _, src := range hostSources {
		if src.snapshot == nil {
			continue
		}
		shipped++
		if snapshotAgeBound*3 > src.window {
			t.Errorf("%s: the bound %s is more than a third of the list's %s window", src.name, snapshotAgeBound, src.window)
		}
		path := filepath.Join("hosts", src.name+".hosts")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("internal/web/%s: %v", path, err)
			continue
		}
		_, at, ok := readHeader(bytes.NewReader(data), int64(len(data)), src.name)
		if !ok {
			t.Errorf("internal/web/%s: the header does not read as #shhh-hosts 1 %s <RFC 3339 time>; an unreadable floor is no floor — run make host-lists", path, src.name)
			continue
		}
		if age := now.Sub(at); age > snapshotAgeBound {
			t.Errorf("internal/web/%s: taken %s, %d days ago, past the %d-day bound — run make host-lists",
				path, at.UTC().Format(time.RFC3339), int(age/day), int(snapshotAgeBound/day))
		}
	}
	if shipped == 0 {
		t.Fatal("no host list ships a snapshot; the check has nothing to read")
	}
}
