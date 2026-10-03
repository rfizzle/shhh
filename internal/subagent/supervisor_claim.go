package subagent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
)

// claimConflict reports whether a writer's declared paths overlap those of a
// live writer. Two claims that both declare paths and share any file are a
// conflict; an undeclared claim conflicts with nothing (it is flagged at
// patch time instead), so existing callers keep working. shares is a writer
// spawned with overlap: allowed, which a writer that allowed it too does not
// stand in the way of.
// See docs/capabilities/subagents.md#a-conflict-is-a-task-for-a-writer.
func (s *Supervisor) claimConflict(paths []string, shares bool) (holder, claim string, conflict bool) {
	return s.claimAhead(paths, 0, shares)
}

// claimAhead is claimConflict asked on behalf of a writer queued behind a
// claim: only the live writers spawned before seq count, so two writers
// queued behind one claim wait in spawn order rather than each on the other,
// and the one named is the nearest ahead — the writer it follows, which is
// the one a lane saying whom it waits behind should name. A seq of zero
// counts every live writer and names the earliest.
func (s *Supervisor) claimAhead(paths []string, seq int, shares bool) (holder, claim string, conflict bool) {
	return s.claimHeld(paths, seq > 0, func(k *child, _ Status) bool {
		return (seq == 0 || k.seq < seq) && (!shares || !k.overlap)
	})
}

// claimAround is the claim a retried writer meets. Spawn order does not
// bound it: a retry comes back after writers spawned since may have taken
// its files, so every live writer but itself counts — except one spawned
// after it that is itself queued behind a claim, which will queue behind
// this one in turn (claimAhead counts the retried writer as ahead of it), so
// the two never wait on each other.
func (s *Supervisor) claimAround(c *child) (holder, claim string, conflict bool) {
	return s.claimHeld(c.paths, false, func(k *child, st Status) bool {
		return k != c && (k.seq < c.seq || st.WaitsOn == "") && (!c.overlap || !k.overlap)
	})
}

// claimInWay is the claim a writer queued with wait_for_claim waits behind:
// the writers spawned ahead of it on its first attempt, every live one on a
// retry.
func (s *Supervisor) claimInWay(c *child) (holder, claim string, conflict bool) {
	c.mu.Lock()
	retried := c.attempt > 1
	c.mu.Unlock()
	if retried {
		return s.claimAround(c)
	}
	return s.claimAhead(c.paths, c.seq, c.overlap)
}

// claimShared is the live writer whose claim a writer spawned with overlap:
// allowed would share, and the path it would share: the first that overlaps
// and allowed overlap too. It is what the spawn's card and its answer name,
// so the person approving the second writer knows whose file it works beside.
func (s *Supervisor) claimShared(paths []string) (holder, claim string, ok bool) {
	return s.claimHeld(paths, false, func(k *child, _ Status) bool { return k.overlap })
}

// SharedClaim is claimShared for a spawn_agent call a card is being drawn
// for: empty unless the call allows overlap and a live writer that allowed it
// too claims one of its paths.
func (s *Supervisor) SharedClaim(raw json.RawMessage) (holder, claim string) {
	args, err := parseSpawnArgs(s.Profiles(), raw)
	if err != nil || !args.overlap {
		return "", ""
	}
	holder, claim, _ = s.claimShared(args.paths)
	return holder, claim
}

// claimHeld is the one walk of the live writers' claims: the first that
// counts and overlaps paths, in spawn order, or nearest first.
func (s *Supervisor) claimHeld(paths []string, nearest bool, counts func(*child, Status) bool) (holder, claim string, conflict bool) {
	if len(paths) == 0 {
		return "", "", false
	}
	s.mu.Lock()
	kids := slices.Clone(s.children)
	s.mu.Unlock()
	if nearest {
		slices.Reverse(kids)
	}
	for _, c := range kids {
		st := c.status()
		if !c.profile.Writes || len(st.Paths) == 0 || !counts(c, st) {
			continue
		}
		// A killed child holds its claim until its goroutine notices the
		// cancel; a claim that is going is not one to wait for.
		if c.ctx.Err() != nil {
			continue
		}
		switch st.State {
		case StateDone, StateFailed:
			continue
		}
		if theirs, ok := ClaimOverlap(paths, st.Paths); ok {
			return st.Name, theirs, true
		}
	}
	return "", "", false
}

// ClaimOverlap reports whether a declared path list meets one already held,
// and the held path it meets. It is the one rule for two claims naming one
// file, asked wherever work is taken beside other work: by the supervisor of
// a writer's claim against every live writer's, and by a parallel sprint of
// an item's paths against every running lane's — so a batch the session
// queues and a sprint the runner serialises are ordered by the same test.
// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
func ClaimOverlap(paths, held []string) (string, bool) {
	for _, theirs := range held {
		for _, ours := range paths {
			if pathsOverlap(ours, theirs) {
				return theirs, true
			}
		}
	}
	return "", false
}

// awaitClaim holds a writer spawned with wait_for_claim until no writer
// spawned ahead of it claims a path its own claim meets, saying on its lane
// whom it waits behind. It is asked before the slot, so a queued writer holds
// neither a slot nor a copy of the tree while it waits, and the copy it is
// given is the tree as it stands once the claim is released — the writer it
// waited on has landed or declined its patch by then, since a writer ends
// only after its patch has been answered, or has stopped with its patch kept
// for the person, which a later landing carries in like any other. False is a writer cancelled or
// killed while it waited.
// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
func (s *Supervisor) awaitClaim(ctx context.Context, c *child) bool {
	for {
		// The signal is taken before the claims are read, so a release that
		// lands after the read closes the channel the wait below holds.
		s.mu.Lock()
		freed := s.claimsFreed
		s.mu.Unlock()
		holder, _, clash := s.claimInWay(c)
		c.mu.Lock()
		moved := c.waitsOn != holder
		c.waitsOn = holder
		if moved {
			c.detail = queuedDetail(holder)
		}
		c.mu.Unlock()
		if moved {
			s.emitUpdate(c)
		}
		if !clash {
			return true
		}
		select {
		case <-freed:
		case <-ctx.Done():
			return false
		}
	}
}

// releaseClaims wakes every writer queued behind a claim to ask again. It is
// called wherever a child ends, which is the only time a claim is released.
func (s *Supervisor) releaseClaims() {
	s.mu.Lock()
	close(s.claimsFreed)
	s.claimsFreed = make(chan struct{})
	s.mu.Unlock()
}

// queuedDetail is a queued child's lane word: behind the writer whose claim
// it waits on, where it waits on one.
func queuedDetail(behind string) string {
	if behind == "" {
		return "queued"
	}
	return "queued behind " + behind
}

// pathsOverlap reports whether two path claims can name the same file. Each
// claim is reduced to the literal prefix before its first wildcard; claims
// overlap when either prefix contains the other, which is deliberately
// generous — a false conflict costs one sequenced agent, a missed one costs
// a mangled patch.
func pathsOverlap(a, b string) bool {
	pa, pb := literalPrefix(a), literalPrefix(b)
	return strings.HasPrefix(pa, pb) || strings.HasPrefix(pb, pa)
}

// literalPrefix trims a glob to the part before its first wildcard and
// normalizes it to a comparable form.
func literalPrefix(p string) string {
	p = strings.TrimPrefix(strings.TrimSpace(filepath.ToSlash(p)), "./")
	if i := strings.IndexAny(p, "*?["); i >= 0 {
		p = p[:i]
		// Back off to the last complete segment so "internal/u*" cannot
		// match "internal/ui" by accident.
		if j := strings.LastIndex(p, "/"); j >= 0 {
			p = p[:j+1]
		} else {
			p = ""
		}
	}
	return p
}
