package todo

import (
	"strings"
	"testing"
	"time"
)

// planRoot is a backlog with the three shapes a set is made of and one item
// that has fallen behind the tree: a dependency chain, two items in the same
// package, and a stale one whose reading found what it says is no longer
// true.
func planRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := Dir(root)
	write(t, dir, "cache-ttl.md", "---\ntitle: Give the cache a lifetime\npriority: high\nsize: S\n---\n")
	write(t, dir, "cache-evict.md", "---\ntitle: Evict what the lifetime expired\nkind: story\npriority: high\nsize: M\n---\n")
	write(t, dir, "cache-metrics.md", "---\ntitle: Count the hits and the misses\nkind: bug\npriority: medium\nsize: S\n---\n")
	write(t, dir, "prose-renderer.md", "---\ntitle: One renderer for every piece of prose\npriority: low\nsize: L\n---\n")
	return root
}

// A reading answers with the set in its own order, a line each, a goal, the
// kind of release it reads as, and every candidate it left out with one of
// the words.
func TestParsePlan_ReadsTheSetItsReasonsAndWhatItLeftOut(t *testing.T) {
	s := Load(BuiltinCode(), planRoot(t))
	answer := "goal: Make an entry's lifetime mean something.\n" +
		"release: minor\n" +
		"item: cache-ttl\n" +
		"why: nothing else in the set can be built until an entry has a lifetime\n" +
		"item: cache-evict\n" +
		"why: the same package, and it is what a lifetime is for\n" +
		"out: cache-metrics unrelated\n" +
		"out: prose-renderer stale\n"
	p := ParsePlan(answer, s.Ready(), nil)
	if got := strings.Join(p.Slugs(), ","); got != "cache-ttl,cache-evict" {
		t.Fatalf("set = %q", got)
	}
	if p.Items[0].Why != "nothing else in the set can be built until an entry has a lifetime" {
		t.Fatalf("first reason = %q", p.Items[0].Why)
	}
	if p.Release != ReleaseMinor {
		t.Fatalf("release = %q", p.Release)
	}
	if len(p.Left) != 2 || p.Left[1].Slug != "prose-renderer" || p.Left[1].Why != OmitStale {
		t.Fatalf("left out = %+v", p.Left)
	}
	// The goal the file carries is the sentence and the release line, so a
	// person reading the sprint sees the question they answer when they tag.
	goal := p.GoalText()
	if !strings.Contains(goal, "Make an entry's lifetime mean something.") ||
		!strings.Contains(goal, "Reads as a minor release.") {
		t.Fatalf("goal = %q", goal)
	}
}

// A set of bugs reads as a patch, which is a different answer to the same
// question and the reason the word is asked for at all.
func TestParsePlan_TakesTheReleaseWordItWasGiven(t *testing.T) {
	s := Load(BuiltinCode(), planRoot(t))
	p := ParsePlan("release: patch\nitem: cache-metrics\nwhy: it is the only bug open\n", s.Ready(), nil)
	if p.Release != ReleasePatch || p.GoalText() != "Reads as a patch release." {
		t.Fatalf("plan = %+v, goal = %q", p, p.GoalText())
	}
	// A word off the scale is no word at all: the goal states nothing
	// rather than a release kind nobody said.
	off := ParsePlan("release: major\nitem: cache-metrics\nwhy: because\n", s.Ready(), nil)
	if off.Release != "" {
		t.Fatalf("release = %q, want nothing for a word off the scale", off.Release)
	}
}

// A slug the candidate list does not hold is dropped rather than proposed:
// the set is written to a file and worked, and an item invented by a misread
// line would be a sprint naming work that is not ready or not there at all.
func TestParsePlan_DropsWhatIsNotACandidate(t *testing.T) {
	s := Load(BuiltinCode(), planRoot(t))
	p := ParsePlan("item: cache-ttl\nwhy: it is ready\n"+
		"item: cache-nothing\nwhy: invented\n"+
		"item: cache-ttl\nwhy: twice\n"+
		"out: nowhere waits\n"+
		"out: cache-metrics nonsense\n", s.Ready(), nil)
	if got := strings.Join(p.Slugs(), ","); got != "cache-ttl" {
		t.Fatalf("set = %q", got)
	}
	if len(p.Left) != 0 {
		t.Fatalf("left out = %+v, want neither an unknown slug nor an unknown word", p.Left)
	}
}

// A reading asked for `item: <slug>` answers with the title beside it often
// enough to matter, and an item dropped for what followed its slug is one
// the person never sees.
func TestParsePlan_TakesTheSlugOutOfALineThatSaysMore(t *testing.T) {
	s := Load(BuiltinCode(), planRoot(t))
	p := ParsePlan("item: `cache-ttl` — Give the cache a lifetime\nwhy: it comes first\n"+
		"out: cache-metrics — unrelated\n", s.Ready(), nil)
	if got := strings.Join(p.Slugs(), ","); got != "cache-ttl" {
		t.Fatalf("set = %q", got)
	}
	if len(p.Left) != 1 || p.Left[0].Slug != "cache-metrics" || p.Left[0].Why != OmitUnrelated {
		t.Fatalf("left out = %+v", p.Left)
	}
}

// A budget the answer overran is still a budget: what does not fit goes to
// the left-out list rather than into the set.
func TestParsePlan_MovesWhatTheBudgetCannotHoldOutOfTheSet(t *testing.T) {
	s := Load(BuiltinCode(), planRoot(t))
	budget, err := ParseSprintBudget(BuiltinCode(), "S=1,M=1")
	if err != nil {
		t.Fatal(err)
	}
	p := ParsePlan("item: cache-ttl\nwhy: first\n"+
		"item: cache-metrics\nwhy: second small one\n"+
		"item: cache-evict\nwhy: the medium\n", s.Ready(), budget)
	if got := strings.Join(p.Slugs(), ","); got != "cache-ttl,cache-evict" {
		t.Fatalf("set = %q", got)
	}
	if len(p.Left) != 1 || p.Left[0].Slug != "cache-metrics" || p.Left[0].Why != OmitTooBig {
		t.Fatalf("left out = %+v", p.Left)
	}
}

// A budget naming only sizes the ready list does not hold admits nothing,
// which the item headers already say: no turn is worth spending on it.
func TestSprintBudgetFits(t *testing.T) {
	s := Load(BuiltinCode(), planRoot(t))
	small, _ := ParseSprintBudget(BuiltinCode(), "S=1")
	if !small.Fits(s.Ready()) {
		t.Error("a backlog with small items does not fit S=1")
	}
	none, _ := ParseSprintBudget(BuiltinCode(), "L=0")
	if none.Fits(s.Ready()) {
		t.Error("a budget with no allowance admits something")
	}
	if (SprintBudget(nil)).Fits(nil) {
		t.Error("an empty ready list fits an unbounded budget")
	}
}

// The prompt hands the planner what the backlog already knows — the header,
// what an item unblocks, where its file is — and the reading of it that
// stands, so the planner takes the reading the person accepted rather than
// paying for it again.
func TestPlanPrompt_CarriesTheFactsAndTheReadings(t *testing.T) {
	root := planRoot(t)
	if err := SaveReading(root, Reading{
		Slug: "cache-ttl", Head: "abc1234def", Read: time.Now(),
		Findings: []Finding{{Verdict: VerdictHolds, Claim: "a"}, {Verdict: VerdictChanged, Claim: "b"}},
	}); err != nil {
		t.Fatal(err)
	}
	s := Load(BuiltinCode(), root)
	budget, _ := ParseSprintBudget(BuiltinCode(), "S=1,M=1")
	prompt := s.PlanPrompt(s.Ready(), budget.String())
	for _, want := range []string{
		"cache-ttl — Give the cache a lifetime",
		"priority high · size S",
		"1 holds · 1 changed",
		"not read against the tree",
		"BUDGET: S=1 M=1",
		"waits, too big, unrelated, stale",
		"patch, minor",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt never says %q", want)
		}
	}
	// No budget states no constraint: telling a model its budget is
	// everything would be stating one that is not there.
	if strings.Contains(s.PlanPrompt(s.Ready(), ""), "BUDGET:") {
		t.Error("an unbounded plan states a budget")
	}
}

// Closing after the last item writes the set's notes into the archived file:
// each item's title, what was built, and the commit that carries it, in the
// shape that pastes into a tag message.
func TestSprintNotes_NameWhatLandedAndItsCommit(t *testing.T) {
	root := planRoot(t)
	write(t, Dir(root), SprintFile, "---\nname: caching\n---\nMake an entry's lifetime mean something.\n\n## Items\n- cache-ttl\n- cache-evict\n")
	report := "## Report\nSummary: an entry carries its own deadline.\n" +
		"Committed: internal/cache/store.go\n" + CommitLine("abc1234def", "feat(cache): give an entry a deadline\n\nbody")
	if _, err := Archive(root, "cache-ttl", report); err != nil {
		t.Fatal(err)
	}
	s := Load(BuiltinCode(), root)
	notes := SprintNotes(s.Sprint, s.SprintEntries())
	for _, want := range []string{
		"Make an entry's lifetime mean something.",
		"- Give the cache a lifetime (cache-ttl) — an entry carries its own deadline. (abc1234 feat(cache): give an entry a deadline)",
		"Deferred:",
		"- Evict what the lifetime expired (cache-evict) — ready, back in the backlog",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("the notes never say %q:\n%s", want, notes)
		}
	}
	// The deferred item goes back to the backlog untouched: closing a
	// sprint early is a decision about the set, not about its items.
	if _, err := CloseSprint(BuiltinCode(), root); err != nil {
		t.Fatal(err)
	}
	after := Load(BuiltinCode(), root)
	it, ok := after.Find("cache-evict")
	if !ok || it.Archived || it.Status != StatusOpen {
		t.Fatalf("the deferred item = %+v", it)
	}
}

// A set that finished nothing and left nothing says so: a sprint whose every
// slug was deleted from the backlog leaves no landed items and nothing to
// defer, and notes that came back empty would be a page block with no words
// in it.
func TestSprintNotes_SayNothingHappenedRatherThanNothing(t *testing.T) {
	sp := &Sprint{Name: "caching", Goal: GoalPlaceholder}
	entries := []SprintEntry{{Slug: "cache-gone", State: SprintItemDropped}}
	if got := SprintNotes(sp, entries); got != "The set finished nothing and left nothing." {
		t.Fatalf("notes = %q", got)
	}
}

// A run asked for without a commit names none, and an item nobody ran names
// neither a summary nor a commit.
func TestCommitLine_IsOnlyThereWhenACommitWasMade(t *testing.T) {
	if got := CommitLine("abc1234def", ""); got != "" {
		t.Errorf("a run with no message named a commit: %q", got)
	}
	if got := CommitLine("", "fix(todo): stop it"); got != "Commit: fix(todo): stop it\n" {
		t.Errorf("outside a repository the subject still stands: %q", got)
	}
	bare := Item{Body: "## Report\nDid it by hand.\n"}
	if ItemCommit(bare) != "" || ItemSummary(bare) != "Did it by hand." {
		t.Errorf("hand-written report = %q / %q", ItemCommit(bare), ItemSummary(bare))
	}
}
