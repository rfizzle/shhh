package subagent

// Integration. A writer's patch whose three-way merge over the checkout
// leaves a conflict region is neither landed with markers nor settled by
// picking a side: which of two intentions over the same lines wins is a
// judgement about the work, and a merge has no standing to make it. So the
// patch is kept and a writer of its own — an integration writer — is started
// to reconcile the two, in a copy of its own, and its patch comes back
// through the same card, checks and merge as any writer's. It is the one
// result: when it lands, the kept patch it reconciled is spent. Where it
// cannot reconcile them, nothing lands and both patches stay kept for the
// person, which is the last resort and never the first.
// See docs/capabilities/subagents.md#a-conflict-is-a-task-for-a-writer.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// integratorRole is the stem an integration writer's generated name takes.
// It names the job rather than the profile the child runs as, which is the
// conflicting writer's own.
const integratorRole Role = "integrator"

// integrationContext is how many lines either side of a conflict region the
// evidence carries: enough to place the region in the function it is in.
const integrationContext = 3

// integration is what an integration writer is reconciling.
type integration struct {
	// source is the writer whose patch conflicted, and kept is that patch as
	// it was kept on its row: the base it was written against and the
	// writer's own change over it.
	source string
	kept   *keptPatch
	// batch is the source's spawn round, which the integration joins.
	batch int
	// evidence is what its first turn opens on (integrationEvidence).
	evidence string
	// generated is the files the source's patch was kept without, which the
	// integration's landing regenerates.
	generated []string

	mu sync.Mutex
	// conflicts is the files the merge could not settle as the copy was
	// seeded, and seeded each of them as the copy then held it — the
	// workspace's side. A file still reading so at the end is one the
	// integration writer did not reconcile.
	conflicts []string
	seeded    map[string]seededFile
}

// seededFile is one file as a copy held it: its text, and whether it was
// there at all.
type seededFile struct {
	text   string
	exists bool
}

// sourceName is the writer an integration reconciles, or "" for no
// integration at all.
func (in *integration) sourceName() string {
	if in == nil {
		return ""
	}
	return in.source
}

// withGenerated is a patch's generated paths with the source's added: what
// the source's patch was kept without is regenerated over the integration's
// landing, or it would be the one part of that patch no landing carries.
func (in *integration) withGenerated(paths []string) []string {
	if in == nil {
		return paths
	}
	for _, p := range in.generated {
		if !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
	}
	return paths
}

// IntegrationStarted is the answer to a kept patch reviewed from its row
// that conflicts with the checkout: no card, because a patch that cannot
// land is not a decision, and an integration writer started instead.
type IntegrationStarted struct {
	Agent string
	Files []string
}

func (e *IntegrationStarted) Error() string {
	return "the kept patch conflicts with the workspace in " + patchPaths(e.Files) + "; " +
		e.Agent + " was started to reconcile the two, and its patch comes back for review"
}

// spawnIntegration starts an integration writer for a writer's kept patch
// whose merge left a conflict region in these files, and answers with its
// name. It is a spawn like any other (Supervisor.spawn): the same slots,
// caps, depth and admission, a claim on the conflicting files that waits
// behind every writer ahead of it holding them — the conflicting writer
// first of all, which releases its claim as it finishes — and the copy of
// the tree it takes when it starts. Its parent is the conflicting writer's,
// so its report reaches whoever was collecting that writer's.
func (s *Supervisor) spawnIntegration(src *child, k *keptPatch, conflicts []string) (string, error) {
	src.mu.Lock()
	if k.integrated {
		src.mu.Unlock()
		return "", errors.New("an integration writer was already started for this patch")
	}
	k.integrated = true
	parent, task, batch, role := src.parent, src.task, src.batch, src.role
	src.mu.Unlock()
	undo := func(err error) (string, error) {
		src.mu.Lock()
		k.integrated = false
		src.mu.Unlock()
		return "", err
	}
	evidence, err := integrationEvidence(k.repoTop, k.base, k.patch, conflicts, src.name)
	if err != nil {
		return undo(err)
	}
	var claim []string
	for _, p := range conflicts {
		if len(claim) < maxClaimedPaths && !strings.Contains(p, "..") {
			claim = append(claim, p)
		}
	}
	s.mu.Lock()
	name := ""
	for name == "" || s.byName[name] != nil {
		s.counters[integratorRole]++
		name = fmt.Sprintf("%s-%d", integratorRole, s.counters[integratorRole])
	}
	s.mu.Unlock()
	raw, err := json.Marshal(struct {
		Role         string   `json:"role"`
		Name         string   `json:"name"`
		Task         string   `json:"task"`
		Paths        []string `json:"paths,omitempty"`
		WaitForClaim bool     `json:"wait_for_claim"`
		// Inherit is stated rather than left to the profile's default: the
		// conflict is the whole of what it needs, and this spawn runs on a
		// child's goroutine, where the parent's conversation is not read.
		Inherit int `json:"inherit"`
	}{string(role), name, integrationTask(src.name, task, conflicts), claim, true, 0})
	if err != nil {
		return undo(err)
	}
	integ := &integration{source: src.name, kept: k, batch: batch, evidence: evidence, generated: k.generated}
	if _, err := s.spawn(parent, raw, integ); err != nil {
		return undo(err)
	}
	return name, nil
}

// integrateKept is the fragment of a conflicting writer's note that says what
// became of the conflict: an integration writer started for the patch it has
// just kept, or why none was. An integration writer's own conflict — the
// checkout moved again while it worked — starts nothing further: it stops
// with both patches kept, which is where a second failed attempt belongs.
func (s *Supervisor) integrateKept(c *child, conflicts []string) string {
	c.mu.Lock()
	k := c.kept
	c.mu.Unlock()
	if k == nil {
		return ""
	}
	if c.integrates != nil {
		return "; " + c.integrates.source + "'s patch is still kept too"
	}
	name, err := s.spawnIntegration(c, k, conflicts)
	if err != nil {
		return "; no integration writer could be started: " + firstLine(err.Error())
	}
	return "; " + name + " was started to reconcile the two, and its patch comes back for review"
}

// integrationTask is an integration writer's task: which writer's patch, in
// which files, and what that writer was asked, so the intention on its side
// is in the writer's own words rather than read back out of a diff.
func integrationTask(source, task string, conflicts []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Reconcile %s's patch with the workspace in %s.\n\n%s was asked:\n\n", source, patchPaths(conflicts), source)
	for _, line := range strings.Split(strings.TrimSpace(task), "\n") {
		b.WriteString("> " + line + "\n")
	}
	return b.String()
}

// integrationEvidence is what an integration writer's first turn opens on,
// in the shape a review's declared evidence takes: for each conflicting
// file, the regions the merge could not settle, each holding the workspace's
// text, the text both started from and the writer's. It is bounded at the
// review's allowance and says so where it is cut.
func integrationEvidence(repoTop, base, patch string, conflicts []string, source string) (string, error) {
	if base == "" {
		return "", errors.New("the kept patch does not record what it was written against")
	}
	theirs, err := keptTree(repoTop, base, patch)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# The conflict\n\n%s's patch was written against a tree the workspace has since moved from, and in %s the two changed the same lines. Your copy is the workspace as it stands, with every other file of %s's patch already merged into it; the files below are as the workspace has them. Each region is marked as `git merge-file --diff3` marks it: the workspace's text, then the text both started from, then %s's.\n\n",
		source, patchPaths(conflicts), source, source)
	for _, p := range conflicts {
		fmt.Fprintf(&b, "## %s\n\n", p)
		ours, err := checkoutSide(filepath.Join(repoTop, filepath.FromSlash(p)))
		if err != nil {
			return "", err
		}
		sides := []mergeSide{ours, blobSide(repoTop, base, p), blobSide(repoTop, theirs, p)}
		regions, ok := conflictRegions(sides, []string{"the workspace", "base", source})
		if !ok {
			fmt.Fprintf(&b, "Not a line merge: the workspace %s it, the base %s it and %s %s it. Decide which the file should be.\n\n",
				sideWord(sides[0]), sideWord(sides[1]), source, sideWord(sides[2]))
			continue
		}
		b.WriteString("```\n" + regions + "```\n\n")
	}
	b.WriteString("Reconcile these regions, verify, and report which intention each one kept.\n\n")
	out := b.String()
	if len(out) > reviewEvidenceBytes {
		cut := strings.LastIndex(out[:reviewEvidenceBytes], "\n")
		if cut <= 0 {
			cut = reviewEvidenceBytes
		}
		out = out[:cut] + fmt.Sprintf("\n[conflict evidence truncated: %d of %d bytes shown; read the files for the rest]\n\n", cut, len(out))
	}
	return out, nil
}

// blobSide is one file in a commit or tree of the shared object store,
// absent where it does not hold the file.
func blobSide(repoTop, treeish, path string) mergeSide {
	entry, err := gitOutput(repoTop, "ls-tree", treeish, "--", path)
	mode, _, _ := strings.Cut(entry, " ")
	if err != nil || mode == "" {
		return mergeSide{}
	}
	text, err := gitOutput(repoTop, "cat-file", "blob", treeish+":"+path)
	if err != nil {
		return mergeSide{}
	}
	return mergeSide{exists: true, mode: mode, text: text}
}

// sideWord says what one side of a merge that is not a line merge did with
// the file.
func sideWord(side mergeSide) string {
	switch {
	case !side.exists:
		return "has no copy of"
	case !side.textual():
		return "has a non-text version of"
	}
	return "has a text version of"
}

// conflictRegions runs the line merge of ours, base and theirs with the
// three labels and answers with each conflict region and the lines around
// it, each headed with the line it starts at in the workspace's file. False
// where the sides are not all text, which has no regions to show.
func conflictRegions(sides []mergeSide, labels []string) (string, bool) {
	for _, side := range sides {
		if !side.exists || !side.textual() {
			return "", false
		}
	}
	scratch, err := os.MkdirTemp("", "shhh-integrate-*")
	if err != nil {
		return "", false
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	args := []string{"merge-file", "-p", "--diff3"}
	for _, l := range labels {
		args = append(args, "-L", l)
	}
	for i, side := range sides {
		name := filepath.Join(scratch, fmt.Sprintf("side-%d", i))
		if err := os.WriteFile(name, []byte(side.text), 0o600); err != nil {
			return "", false
		}
		args = append(args, name)
	}
	cmd := exec.Command("git", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	// The exit status is the number of conflict regions; the output is
	// what is wanted either way.
	_ = cmd.Run()
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")

	// Each line's place in the workspace's file — the next of its lines,
	// for a marker or a line of the other two sides — and the regions.
	ourNo := make([]int, len(lines))
	var regions [][2]int
	next, inOurs, start := 1, false, -1
	for i, line := range lines {
		ourNo[i] = next
		switch {
		case isMarker(line, "<<<<<<<"):
			start, inOurs = i, true
		case start >= 0 && (isMarker(line, "|||||||") || line == "======="):
			inOurs = false
		case start >= 0 && isMarker(line, ">>>>>>>"):
			regions = append(regions, [2]int{start, i})
			start = -1
		case start < 0 || inOurs:
			next++
		}
	}
	var b strings.Builder
	shownTo := -1
	for _, r := range regions {
		from := max(r[0]-integrationContext, shownTo+1)
		to := min(r[1]+integrationContext, len(lines)-1)
		if from > shownTo+1 {
			if shownTo >= 0 {
				b.WriteString("…\n")
			}
			fmt.Fprintf(&b, "@@ line %d\n", ourNo[from])
		}
		for _, l := range lines[from : to+1] {
			b.WriteString(l + "\n")
		}
		shownTo = to
	}
	return b.String(), len(regions) > 0
}

// isMarker is whether a line is one of git's conflict markers of this kind:
// the seven characters, then the end of the line or a space and a label.
func isMarker(line, marker string) bool {
	return line == marker || strings.HasPrefix(line, marker+" ")
}

// seed makes an integration writer's fresh copy what it reconciles in: the
// source's patch merged over it again — against the checkout as it stands
// now, which is what the copy was just taken from — with every file that
// merges cleanly written in, and the conflicting files left as the workspace
// has them for the writer to reconcile. It records those files as seeded, so
// the landing can tell which of them the writer did not touch.
func (in *integration) seed(wt worktreeHandle) error {
	m, err := mergeKept(wt.repoTop, in.kept.base, in.kept.patch, nil)
	if err != nil {
		return fmt.Errorf("merging %s's patch into the integration's copy: %w", in.source, err)
	}
	patch := m.Patch
	if len(m.Conflicts) > 0 {
		patch = m.Resolved
	}
	if patch != "" {
		if err := applyPatch(wt.dir, patch); err != nil {
			return fmt.Errorf("writing %s's merged files into the integration's copy: %w", in.source, err)
		}
	}
	seeded := make(map[string]seededFile, len(m.Conflicts))
	for _, p := range m.Conflicts {
		data, err := os.ReadFile(filepath.Join(wt.dir, filepath.FromSlash(p)))
		seeded[p] = seededFile{text: string(data), exists: err == nil}
	}
	in.mu.Lock()
	in.conflicts, in.seeded = m.Conflicts, seeded
	in.mu.Unlock()
	return nil
}

// unreconciled is the conflicting files an integration writer's copy does not
// hold a reconciliation of: any still exactly as the workspace had it when
// the copy was seeded, and any file the patch writes a conflict marker into.
// Either is a patch that must not land as the one result.
func (in *integration) unreconciled(worktree, patch string) []string {
	in.mu.Lock()
	conflicts, seeded := in.conflicts, in.seeded
	in.mu.Unlock()
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range conflicts {
		data, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(p)))
		if (seededFile{text: string(data), exists: err == nil}) == seeded[p] {
			add(p)
		}
	}
	file := ""
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			file = parseGitDiffPath(line)
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			if body := line[1:]; isMarker(body, "<<<<<<<") || isMarker(body, ">>>>>>>") {
				add(file)
			}
		}
	}
	return out
}

// unreconciledNote is what an integration writer that did not reconcile
// comes to: nothing lands, its own patch is kept beside the source's, and
// the source's row is told. Neither kept patch starts another integration
// from its row; the person is who is asked now.
func (s *Supervisor) unreconciledNote(c *child, files []string, patch string) string {
	in := c.integrates
	kept := "; this one changed nothing"
	if strings.TrimSpace(patch) != "" {
		kept = c.keepWriterPatch(patch, s.opts.Generators)
	}
	if src, err := s.lookup(in.source); err == nil {
		src.appendEntry(TranscriptEntry{Kind: EntrySystem,
			Text: c.name + " did not reconcile " + patchPaths(files) + "; this patch is still kept for the user to review"})
		s.emitUpdate(src)
	}
	return fmt.Sprintf("%s did not reconcile %s with the workspace; no files were changed, and both patches are kept for the user: %s's, and this one%s",
		c.name, patchPaths(files), in.source, kept)
}

// integrationLanded spends the kept patch an integration writer reconciled,
// once its own patch has landed: that landing is the one result, and the
// kept patch left on the source's row would be the same work offered twice.
// A kept patch already replaced — the source was retried — is not this one's
// to spend.
func (s *Supervisor) integrationLanded(c *child) {
	in := c.integrates
	src, err := s.lookup(in.source)
	if err != nil {
		return
	}
	note := "its patch landed through " + c.name + ", which reconciled it with the workspace"
	src.mu.Lock()
	spent := src.kept == in.kept
	if spent {
		src.kept = nil
		src.patchNote = note
	}
	src.mu.Unlock()
	if spent {
		src.appendEntry(TranscriptEntry{Kind: EntrySystem, Text: note})
		s.emitUpdate(src)
	}
}
