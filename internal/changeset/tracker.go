package changeset

import (
	"context"
	"strings"
	"sync"

	"github.com/rfizzle/shhh/internal/hostgit"
)

// Tracker answers whether git knew about a file when it was edited. It is the
// only part of the changeset that talks to git, and it is optional: outside a
// repository every answer is TrackUnknown and the store still records
// everything else.
//
// A nil *Tracker answers TrackUnknown, so a session without one calls it
// unconditionally.
type Tracker struct {
	dir  string
	repo bool

	mu sync.Mutex
}

// NewTracker returns a tracker for the repository containing dir, or one that
// only ever answers TrackUnknown when dir is not inside a work tree.
func NewTracker(dir string) *Tracker {
	if dir == "" {
		dir = "."
	}
	t := &Tracker{dir: dir}
	out, err := t.git("rev-parse", "--is-inside-work-tree")
	t.repo = err == nil && strings.TrimSpace(out) == "true"
	return t
}

// Track reports whether path is tracked right now. It is called just before
// the edit is applied, so the answer is the file's state at the time of the
// edit — a file the turn creates is untracked even after the user adds it.
func (t *Tracker) Track(path string) Tracking {
	if t == nil || !t.repo || path == "" {
		return TrackUnknown
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := t.git("ls-files", "--error-unmatch", "--", path); err != nil {
		return TrackUntracked
	}
	return TrackTracked
}

// MarkIgnored returns the turn with each untracked record the checkout's
// ignore rules cover marked TrackIgnored, from one check-ignore call for the
// whole turn. The index is consulted, so a tracked file that also matches a
// pattern is not ignored (the rule the agent's tree notice states too); only
// an untracked record can be ignored, so a turn with none costs no process.
// Outside a checkout, or when the call fails, the turn comes back as it was.
func (t *Tracker) MarkIgnored(turn Turn) Turn {
	if t == nil || !t.repo {
		return turn
	}
	var paths []string
	for _, r := range turn.Records {
		if r.Track == TrackUntracked {
			paths = append(paths, r.Path)
		}
	}
	if len(paths) == 0 {
		return turn
	}
	t.mu.Lock()
	cmd := hostgit.Command(context.Background(), t.dir, "check-ignore", "-z", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")
	out, err := cmd.Output()
	t.mu.Unlock()
	// Exit status 1 is check-ignore saying none of them.
	if err != nil {
		return turn
	}
	ignored := map[string]bool{}
	for _, p := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if p != "" {
			ignored[p] = true
		}
	}
	records := append([]Record(nil), turn.Records...)
	for i, r := range records {
		if r.Track == TrackUntracked && ignored[r.Path] {
			records[i].Track = TrackIgnored
		}
	}
	turn.Records = records
	return turn
}

// Repo reports whether the tracker found a git work tree; the surfaces that
// speak about reversibility need to know the difference between "untracked"
// and "there is no repository to be tracked by".
func (t *Tracker) Repo() bool { return t != nil && t.repo }

func (t *Tracker) git(args ...string) (string, error) {
	out, err := hostgit.Command(context.Background(), t.dir, args...).Output()
	return string(out), err
}
