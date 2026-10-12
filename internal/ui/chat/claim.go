package chat

// A claim answered. The model may ask to stage a path the session's record
// does not hold — a file its command made that the bracket missed — and the
// question is put on the git write's card, one row a path. Saying yes there
// is the act that makes the path the session's: it is recorded here, with
// the model's reason as its evidence, and the stager admits it because the
// record now holds it. Nothing about the call's arguments does that by
// itself; a path reaches the index only through the record.
// See docs/capabilities/approvals-and-safety.md#the-writing-half-of-git-is-a-tool-too.

import (
	"os"
	"strings"

	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/structural"
)

// recordClaims files an answered call's claims in the turn, each as a record
// with no before side read as the file stands now. It runs where an approved
// call is carried out, which every yes reaches — the card, the list's head,
// a grant taken at the card, the classifier — and which nothing reaches for
// a claim without one of those (agent.Action.Claims).
func (m *Model) recordClaims(req *approvalRequest) {
	if req == nil || len(req.claims) == 0 || m.changes == nil {
		return
	}
	held := heldBySession(m.changes, m.wiring.Workspace)
	var evicted []int64
	for _, c := range req.claims {
		if held != nil && held(c.Abs) {
			// Already the session's, with both sides: the stager admits it
			// as it stands, and a record with no before side would only take
			// its undo away.
			continue
		}
		r := changeset.Record{
			Path: c.Abs, Agent: changeset.MainAgent, Origin: changeset.ByClaim,
			Evidence: c.Why, Generated: c.Generated, Track: m.wiring.Tracker.Track(c.Abs),
		}
		if data, err := os.ReadFile(c.Abs); err == nil {
			r.After, r.AfterExists = string(data), true
			if fi, err := os.Stat(c.Abs); err == nil {
				r.AfterMode = fi.Mode().Perm()
			}
		}
		evicted = append(evicted, m.changes.Add(m.turnCount, r)...)
	}
	m.noteEvictedTurns(evicted)
}

// claimPaths names a call's claimed paths as the model typed them.
func claimPaths(claims []structural.Claim) string {
	names := make([]string, 0, len(claims))
	for _, c := range claims {
		names = append(names, c.Path)
	}
	return strings.Join(names, ", ")
}
