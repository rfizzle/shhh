package subagent

import (
	"context"
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/hostgit"
	wtree "github.com/rfizzle/shhh/internal/subagent/worktree"
)

// PatchApplied is what a child's applied patch changed, file by file. The
// parent's changeset store is the only reader: a child's edits happen in an
// isolated worktree, so this — the moment the patch lands on the real
// checkout — is when the session actually changed.
type PatchApplied struct {
	Agent string
	Files []PatchedFile
}

// PatchedFile is one file of an applied patch; the worktree package
// reads it either side of `git apply`.
type PatchedFile = wtree.PatchedFile

// reviewPatch computes the writer's worktree patch and routes it through the
// approval flow before anything touches the real checkout.
func (s *Supervisor) reviewPatch(c *child) (landed bool) {
	patch, err := wtree.WorktreePatch(c.worktree)
	if err != nil {
		c.mu.Lock()
		c.patchNote = "the worktree patch could not be computed: " + firstLine(err.Error()) + "; no files were changed"
		c.mu.Unlock()
		return false
	}
	// An integration writer's patch is the one result only where it holds a
	// reconciliation of every conflicting file. One that left a file as the
	// workspace had it, or wrote a conflict marker, lands nothing, and both
	// patches stay kept for the person.
	// See docs/capabilities/subagents.md#a-conflict-is-a-task-for-a-writer.
	if c.integrates != nil {
		if files := c.integrates.unreconciled(c.worktree, patch); len(files) > 0 {
			note := s.unreconciledNote(c, files, patch)
			c.mu.Lock()
			c.patchNote = note
			c.mu.Unlock()
			return false
		}
	}
	if strings.TrimSpace(patch) == "" {
		c.mu.Lock()
		c.patchNote = "no file changes were made"
		c.mu.Unlock()
		return false
	}

	settle := func(note string) {
		c.mu.Lock()
		c.patchNote = note
		c.mu.Unlock()
	}

	// What the card shows and what lands are one patch, whichever it is: the
	// writer's own where it applies to the checkout as it stands, and its
	// merge over the checkout where the checkout has moved under it since the
	// copy's base — another writer landed, or the person edited the file, or
	// the writer ended on an answer and never reached the round boundary a
	// landing is carried in at. A card that showed the writer's own diff and
	// then applied a merge would be approving one change and landing
	// another. The plain patch is always tried first; the merge is shhh's own
	// (worktree.MergeWorktree) and never `git apply --3way`.
	// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
	//
	// A file the project declares generated is taken out of that altogether:
	// it is never merged and never applied as the writer wrote it. The rest
	// of the patch — plain or merged — is applied to a copy of the checkout,
	// the generators for those files run there, and the copy's difference is
	// what the card shows and what lands. A clean-looking merge of a golden
	// file is still a file no generator would write.
	held := "; the patch would have touched " + wtree.PatchPaths(wtree.PatchFiles(patch))
	generated := c.integrates.withGenerated(wtree.GeneratedPaths(s.opts.Generators, wtree.PatchFiles(patch)))
	source := wtree.WithoutFiles(patch, generated)
	offer, merged := source, []string(nil)
	for {
		if offer != "" && wtree.CheckPatch(c.repoTop, offer) != nil {
			m, err := wtree.MergeWorktree(c.worktree, c.repoTop, generated)
			if err == nil && m.Patch != "" && len(m.Conflicts) == 0 {
				err = wtree.CheckPatch(c.repoTop, m.Patch)
			}
			switch {
			case err != nil:
				settle("the patch no longer applies to the workspace and could not be merged over it: " +
					firstLine(err.Error()) + "; no files were changed" + c.keepWriterPatch(patch, s.opts.Generators) + held)
				return false
			case len(m.Conflicts) > 0:
				// A conflict region is not settled here: which side wins is
				// the one judgement this merge has no standing to make. It
				// is a writer's, in a copy of its own, under review.
				// See docs/capabilities/subagents.md#a-conflict-is-a-task-for-a-writer.
				kept := c.keepWriterPatch(patch, s.opts.Generators)
				settle("the patch no longer applies to the workspace: " + wtree.PatchPaths(m.Conflicts) +
					" moved since it started and the changes conflict there; no files were changed" +
					kept + s.integrateKept(c, m.Conflicts) + held)
				return false
			case m.Patch == "" && len(generated) == 0:
				settle("the workspace already holds every change the patch makes; no files were changed")
				return false
			}
			offer, merged = m.Patch, m.Moved
		}

		// What lands: the offer, and over it whatever the generators write.
		// A generator that fails does not stop the card — the person is
		// shown the change without the generated files and why, and the
		// checkout has not been touched either way.
		land, ran, regenErr := offer, []string(nil), error(nil)
		if len(generated) > 0 {
			c.set(StateRunning, "regenerating "+wtree.PatchPaths(generated))
			s.emitUpdate(c)
			var out string
			out, ran, regenErr = wtree.RegenerateOver(c.ctx, s.opts.Generators, s.opts.Root, s.parentUntracked(), offer, generated)
			if regenErr == nil {
				land = out
			} else {
				ran = nil
			}
		}
		if land == "" {
			if regenErr != nil {
				settle("the patch's generated files could not be regenerated: " + firstLine(regenErr.Error()) +
					"; no files were changed" + c.keepWriterPatch(patch, s.opts.Generators) + held)
				return false
			}
			settle("the workspace already holds every change the patch makes; no files were changed")
			return false
		}

		ask, touched := s.patchAsk(c, c.repoTop, land)
		ask.Merged = merged
		ask.Regenerated = ran
		if regenErr != nil {
			ask.Warnings = append(ask.Warnings, wtree.PatchPaths(generated)+" left as your checkout has them: "+firstLine(regenErr.Error()))
		}
		// What the patch names is the whole of what the parent has to know to
		// integrate it, and it is already in hand here: a note that gave only
		// a count sent the parent to `git status` for the names, one round and
		// one approval after the patch had already landed.
		held = "; the patch would have touched " + wtree.PatchPaths(touched)
		approved, ok := s.await(c, ask)
		// The wait stood in front of the person, whichever way it ended, and
		// the turn is still open: it is booked on this goroutine, which owns
		// the clock, and c.mu is not held across the await.
		c.tookAnswer()
		switch {
		case !ok:
			settle("cancelled before the patch was reviewed; no files were changed" + c.keepPatch(land, merged) + held)
			return false
		case !approved:
			settle("the user declined the patch; no files were changed" + c.keepPatch(land, merged) + held)
			return false
		}
		applied, applyErr := s.landPatch(c, c.repoTop, land, touched)
		if applyErr == nil {
			if len(merged) > 0 {
				applied += ", merged over " + wtree.PatchPaths(merged) + ", which moved since it started"
			}
			if len(ran) > 0 {
				applied += ", with " + wtree.PatchPaths(generated) + " regenerated by " + strings.Join(ran, ", ") + " rather than merged"
			}
			settle(applied)
			if c.integrates != nil {
				s.integrationLanded(c)
			}
			return true
		}
		// The landing is all-or-nothing, so a refusal changed nothing. Where
		// the checkout moved while the card was up — a second writer landed,
		// the person saved a file — the approval was of a patch that no
		// longer describes what would land, so it goes back through the
		// merge and to the card again rather than being forced. Where the
		// patch still applies, the tree did not move and the failure is
		// something else, which another card would only repeat.
		if wtree.CheckPatch(c.repoTop, land) == nil {
			settle("the patch failed to apply cleanly: " + firstLine(applyErr.Error()) + c.keepPatch(land, merged) + held)
			return false
		}
	}
}

// patchAsk is the card a writer's patch is put to the person on, whichever
// door it arrives by: a writer finishing, or a patch kept from one that did
// not land being reviewed from its row. The two are one path so that what
// the card warns about and where it measures are the same either way.
func (s *Supervisor) patchAsk(c *child, repoTop, patch string) (*Ask, []string) {
	name := c.name
	hunks, files := wtree.PatchHunks(patch)
	adds, dels := diff.Stats(hunks)
	title := fmt.Sprintf("apply patch (+%d −%d, %s)", adds, dels, plural(files, "file"))
	touched := wtree.PatchFiles(patch)
	ask := NewAsk(name, AskPatch, title)
	ask.Hunks = hunks
	// A patch is the one child request that writes the reader's own files, so
	// it is measured in the reader's own checkout: the worktree the child
	// edited in is not where any of this lands.
	ask.Root, ask.Files = repoTop, touched
	// Two writers can hold the same file in separate worktrees; the collision
	// only becomes visible when the second patch lands on top of the first.
	// Say so on the card, before it is applied — except where the patch is an
	// integration writer's over the files it was started to reconcile, whose
	// whole job is to overwrite both sides; there the card says whose change
	// it settles instead, so the warning keeps its weight everywhere else.
	// See docs/capabilities/subagents.md#a-conflict-is-a-task-for-a-writer.
	clashes, reconciled := s.patchClashes(c, touched)
	if len(clashes) > 0 {
		ask.Warnings = append(ask.Warnings, "overwrites changes already applied by "+strings.Join(clashes, ", "))
	}
	if len(reconciled) > 0 {
		ask.Reconciles = strings.Join(reconciled, ", ") + " with " + c.integrates.sourceName() + "'s"
	}
	return ask, touched
}

// landPatch applies an approved patch to the real checkout, records which
// agent last touched each file, and tells the session what changed. It
// answers with the note the parent reads. repoTop is handed in rather than
// read off the child, because a kept patch lands from a goroutine of its own
// and a retry may be giving the child a new workspace at the same moment.
func (s *Supervisor) landPatch(c *child, repoTop, patch string, touched []string) (string, error) {
	// Both sides are read around `git apply`, in the real checkout: the
	// child's own worktree edits never touched these files, so this is the
	// only place the session can see what its workspace lost and gained.
	before := wtree.ReadSides(repoTop, touched)
	if err := wtree.ApplyPatch(repoTop, patch); err != nil {
		return "", err
	}
	s.recordApplied(c.name, touched)
	// And every other writer still working is owed it, at its own next
	// boundary: its copy was taken from a tree this patch has just moved.
	s.queueLanding(c.name, repoTop, patch)
	s.emit(Event{
		Kind:   EventPatch,
		Status: c.status(),
		Patch: &PatchApplied{
			Agent: c.name,
			Files: wtree.PatchedFiles(s.opts.Root, repoTop, touched, before, wtree.ReadSides(repoTop, touched)),
		},
	})
	hunks, files := wtree.PatchHunks(patch)
	adds, dels := diff.Stats(hunks)
	return fmt.Sprintf("patch applied to the workspace (+%d −%d, %s): %s", adds, dels, plural(files, "file"), wtree.PatchPaths(touched)), nil
}

// patchClashes names the other agents whose applied patches already touched
// any of these files, most recent writer per file. A clash in a file an
// integration writer (Spec.Integrates) was handed to reconcile is answered in
// reconciled instead, as that writer's change to the file: overwriting it is
// what the integration was started for.
func (s *Supervisor) patchClashes(c *child, files []string) (clashes, reconciled []string) {
	handed := map[string]bool{}
	if in := c.integrates; in != nil {
		in.mu.Lock()
		for _, f := range in.conflicts {
			handed[f] = true
		}
		in.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	for _, f := range files {
		other := s.appliedFiles[f]
		if other == "" || other == c.name {
			continue
		}
		if handed[f] {
			reconciled = append(reconciled, other+"'s change to "+f)
			continue
		}
		if seen[other] {
			continue
		}
		seen[other] = true
		clashes = append(clashes, other+" ("+f+")")
	}
	return clashes, reconciled
}

// recordApplied remembers which agent's patch last touched each file.
func (s *Supervisor) recordApplied(name string, files []string) {
	s.mu.Lock()
	for _, f := range files {
		s.appliedFiles[f] = name
	}
	s.mu.Unlock()
}

// keptPatchTool is the name a kept patch is filed under in the evidence
// store, where every other entry is filed under the tool that produced it.
const keptPatchTool = "subagent_patch"

// keptPatch is a writer's change that never reached the checkout. The patch
// is held whole because it is what an apply writes: the copy in the evidence
// store has been through the secrets scrub and may be cut at the store's
// bound, and applying either would write something the child never did.
type keptPatch struct {
	// id is the evidence store's handle for it, or "" where the session has
	// no store or the store would not take it.
	id    string
	patch string
	// repoTop is the checkout the patch was made against and lands in.
	repoTop string
	// review is the card it is out on, while one is; a second press of the
	// key hands back the same card rather than a second one over the same
	// work.
	review *Ask
	// merged is the files a merge went over where the patch is one, so the
	// card it is reviewed on from the row says so too.
	merged []string
	// base is the commit the writer's copy stood on, where the patch is the
	// writer's own against it rather than a merge over the checkout. The
	// commit outlives the copy in the object store every copy shares, which
	// is what lets the patch be merged again from its row (worktree.MergeKept). Empty
	// for a patch that lands only as it is.
	base string
	// generated is the files the project declares generated that the patch
	// was kept without; they are regenerated when it lands from the row.
	generated []string
	// integrated marks a patch an integration writer has been started for.
	// The row's [p] then puts the patch itself to the person rather than
	// starting another: the person is the last resort, asked with both
	// patches in hand.
	integrated bool
}

// keepPatch is the one fate of a writer's change that did not land, however
// the child ended: written to the evidence store — through the session's
// scrub, like every other copy that outlives a turn — and held on the child
// for review from its row. It answers with the note fragment naming it.
// See docs/capabilities/subagents.md#a-failed-child-leaves-a-handoff.
func (c *child) keepPatch(patch string, merged []string) string {
	return c.keep(&keptPatch{patch: patch, merged: merged})
}

// keep is keepPatch for a kept patch that says more about itself than the
// patch: the base it was written against and the generated files left out.
func (c *child) keep(k *keptPatch) string {
	c.mu.Lock()
	k.repoTop = c.repoTop
	// An integration writer's own patch never starts another integration
	// from its row, however it came to be kept: a second attempt at the same
	// conflict is the person's.
	k.integrated = k.integrated || c.integrates != nil
	c.mu.Unlock()
	if c.env.Archive != nil {
		if id, ok := c.env.Archive(keptPatchTool, k.patch); ok {
			k.id = id
		}
	}
	c.mu.Lock()
	c.kept = k
	c.mu.Unlock()
	if k.id == "" {
		return " (the patch is kept for the user to review)"
	}
	return " (the patch is kept as " + k.id + " for the user to review)"
}

// keepStoppedPatch keeps what a writer that did not finish had written in its
// copy of the checkout: a budget, a kill and a cancel all end it here, before
// its worktree is removed. A writer that already kept its patch at the card
// has nothing more to keep.
func (c *child) keepStoppedPatch(gen Regenerator) {
	if !c.profile.Writes {
		return
	}
	c.mu.Lock()
	worktree, kept := c.worktree, c.kept != nil
	c.mu.Unlock()
	if worktree == "" || kept {
		return
	}
	patch, err := wtree.WorktreePatch(worktree)
	if err != nil || strings.TrimSpace(patch) == "" {
		return
	}
	c.keepWriterPatch(patch, gen)
}

// keepWriterPatch keeps a writer's own patch, as it wrote it, less the files
// the project declares generated. The writer's bytes for a generated file are
// never what lands — that is the hand-merge the landing exists to refuse — so
// they are dropped and named on the kept patch, and regenerated over it in a
// copy of the checkout when it lands from the row (awaitKept). A patch that
// was nothing but generated files keeps nothing, and those files stay as the
// checkout has them until their generator is run.
// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
func (c *child) keepWriterPatch(patch string, gen Regenerator) string {
	generated := wtree.GeneratedPaths(gen, wtree.PatchFiles(patch))
	source := wtree.WithoutFiles(patch, generated)
	c.mu.Lock()
	worktree := c.worktree
	c.mu.Unlock()
	// The copy's base, read while the copy is still there: the patch is the
	// writer's own against it, so the two are what a later merge needs.
	base := ""
	if worktree != "" {
		if out, err := hostgit.Output(context.Background(), worktree, "rev-parse", "HEAD"); err == nil {
			base = strings.TrimSpace(out)
		}
	}
	if len(generated) == 0 {
		return c.keep(&keptPatch{patch: patch, base: base})
	}
	left := "; " + wtree.PatchPaths(generated) + " are generated and are regenerated when it lands"
	if source == "" {
		return "; " + wtree.PatchPaths(generated) + " are generated and left to their generator"
	}
	return c.keep(&keptPatch{patch: source, base: base, generated: generated}) + left
}

// PatchToKeep reports whether ending this agent now would keep a patch: a
// writer whose copy of the checkout holds changes, or one already holding a
// kept patch. It is what the kill confirm asks, so the confirm can say what
// survives the kill. It reads the copy's status and stages nothing, because
// the child is still working in it.
func (s *Supervisor) PatchToKeep(name string) bool {
	c, err := s.lookup(name)
	if err != nil || !c.profile.Writes {
		return false
	}
	c.mu.Lock()
	worktree, kept := c.worktree, c.kept != nil
	c.mu.Unlock()
	if kept {
		return true
	}
	if worktree == "" {
		return false
	}
	out, err := hostgit.Output(context.Background(), worktree, "status", "--porcelain")
	return err == nil && strings.TrimSpace(out) != ""
}

// ReviewKept puts a stopped writer's kept patch to the person on the same
// card a finishing writer's patch is put on (patchAsk), and applies it on a
// yes the same way (landPatch) — so the overlap warning and the record of
// which agent applied which file are the ones every patch gets. It answers
// with the card for the surface to show; the answer arrives through
// Ask.Respond. A no leaves the patch kept.
func (s *Supervisor) ReviewKept(name string) (*Ask, error) {
	c, err := s.lookup(name)
	if err != nil {
		return nil, err
	}
	if s.isClosed() {
		return nil, ErrClosed
	}
	c.mu.Lock()
	k := c.kept
	var pending *Ask
	if k != nil {
		pending = k.review
	}
	c.mu.Unlock()
	if k == nil {
		return nil, fmt.Errorf("agent %s has no kept patch", name)
	}
	if pending != nil {
		return pending, nil
	}
	// A kept patch that no longer applies is merged again from its row, the
	// way a finishing writer's is, where it records the base it was written
	// against: a merge that comes out clean is what the card shows and what
	// lands, and one that leaves a conflict region is an integration
	// writer's to reconcile rather than a card for a patch that cannot land.
	// Once one has been tried the card is put to the person as the patch
	// stands: they are the last resort, asked with both patches in hand.
	// See docs/capabilities/subagents.md#a-conflict-is-a-task-for-a-writer.
	land, merged := k.patch, k.merged
	c.mu.Lock()
	integrated := k.integrated
	c.mu.Unlock()
	if k.base != "" && wtree.CheckPatch(k.repoTop, k.patch) != nil {
		m, err := wtree.MergeKept(k.repoTop, k.base, k.patch, nil)
		switch {
		case err != nil:
			// The card as the patch stands, whose landing then says why.
		case len(m.Conflicts) > 0 && !integrated:
			agentName, err := s.spawnIntegration(c, k, m.Conflicts)
			if err != nil {
				return nil, fmt.Errorf("the kept patch conflicts with the workspace in %s, and no integration writer could be started: %s",
					wtree.PatchPaths(m.Conflicts), firstLine(err.Error()))
			}
			return nil, &IntegrationStarted{Agent: agentName, Files: m.Conflicts}
		case len(m.Conflicts) == 0 && m.Patch == "":
			return nil, fmt.Errorf("the workspace already holds every change agent %s's kept patch makes", name)
		case len(m.Conflicts) == 0:
			land, merged = m.Patch, m.Moved
		}
	}
	ask, touched := s.patchAsk(c, k.repoTop, land)
	ask.Merged = merged
	// What the patch was kept without is regenerated over it as it lands,
	// in a copy of the checkout as it stands then (awaitKept).
	if len(k.generated) > 0 {
		ask.Warnings = append(ask.Warnings, wtree.PatchPaths(k.generated)+" are generated and are regenerated by their generator as this lands")
	}
	c.mu.Lock()
	if c.kept != k {
		c.mu.Unlock()
		return nil, fmt.Errorf("agent %s has no kept patch", name)
	}
	// The card was built outside the lock, so another caller may have put
	// one out in the meantime; that one stands and this one is dropped.
	if k.review != nil {
		pending = k.review
		c.mu.Unlock()
		return pending, nil
	}
	k.review = ask
	c.mu.Unlock()
	// Tracked like every other goroutine the supervisor starts, so Close
	// waits for an apply already under way rather than closing the event
	// stream under it.
	s.wg.Add(1)
	go s.awaitKept(c, k, ask, land, touched)
	return ask, nil
}

// awaitKept waits for the answer to a kept patch's card. The patch lands only
// while it is still the one the child holds: a retry in the meantime is the
// person asking for the work again, and the patch it replaced is not what
// they are now approving.
func (s *Supervisor) awaitKept(c *child, k *keptPatch, ask *Ask, land string, touched []string) {
	defer s.wg.Done()
	var approved bool
	select {
	case approved = <-ask.resp:
	case <-s.ctx.Done():
		return
	}
	c.mu.Lock()
	current := c.kept == k
	if current {
		k.review = nil
	}
	c.mu.Unlock()
	if !approved || !current {
		return
	}
	// The generated files the patch was kept without are regenerated over it
	// in a copy of the checkout as it stands, as a finishing writer's are
	// (worktree.RegenerateOver); a generator that fails lands nothing, since the card
	// promised the regenerated files with it.
	var ran []string
	if len(k.generated) > 0 {
		out, cmds, err := wtree.RegenerateOver(s.ctx, s.opts.Generators, s.opts.Root, s.parentUntracked(), land, k.generated)
		if err != nil || out == "" {
			note := "the workspace already holds every change the kept patch makes"
			if err != nil {
				note = "the kept patch's generated files could not be regenerated: " + firstLine(err.Error()) + "; no files were changed and it is still kept"
			}
			c.mu.Lock()
			c.patchNote = note
			c.mu.Unlock()
			c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: note})
			s.emitUpdate(c)
			return
		}
		land, touched, ran = out, wtree.PatchFiles(out), cmds
	}
	note, err := s.landPatch(c, k.repoTop, land, touched)
	c.mu.Lock()
	spent := false
	if err != nil {
		note = "the kept patch failed to apply cleanly: " + firstLine(err.Error()) + "; it is still kept"
	} else {
		if len(ran) > 0 {
			note += ", with " + wtree.PatchPaths(k.generated) + " regenerated by " + strings.Join(ran, ", ")
		}
		if c.kept == k {
			c.kept, spent = nil, true
		}
	}
	c.patchNote = note
	c.mu.Unlock()
	if spent {
		s.settleHandoff(c, k)
	}
	c.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: note})
	s.emitUpdate(c)
}
