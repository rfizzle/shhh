package todo

// The sprint: one file that names a set of items, in the order they should
// go, under a goal. It is the item's own shape applied to a set — the same
// header grammar, the same two rules about what a write may touch — so a
// person can open it, reorder the list and change the goal without the tool
// in the way. See docs/capabilities/todo.md#a-sprint-is-a-file-that-names-its-items.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SprintFile is the sprint's name inside the backlog directory, and
// SprintsSubdir where a closed one goes under the archive. There is one
// sprint at a time, which is why the active one is a fixed name rather
// than a directory to search.
const (
	SprintFile    = "sprint.md"
	SprintsSubdir = "sprints"
)

// The sprint header's keys. Anything else is carried in Extra and written
// back where it was, exactly as an item's unknown field is.
const (
	keyName = "name"
)

var sprintKnownKeys = map[string]bool{
	keyName: true, keyStatus: true, keyCreated: true, keySession: true,
}

// sprintItemsHeading opens the list of slugs. Everything above it is the
// goal; the bullets under it are the set, in the order they are written.
const sprintItemsHeading = "## Items"

// SprintStatus is whether the sprint is still being worked.
type SprintStatus string

const (
	SprintOpen   SprintStatus = "open"
	SprintClosed SprintStatus = "closed"
)

// Sprint is the sprint file as read: the header, the goal paragraph and
// the slugs in file order. The order is stated rather than computed, which
// is the one thing a sprint adds to the backlog's own ordering; what it
// may not do is admit an item that is not ready, and that stays computed.
type Sprint struct {
	Path   string
	Name   string
	Status SprintStatus
	// Created and Session are when and by whom the set was chosen.
	Created string
	Session string
	Extra   []Unknown

	// Goal is the paragraph above the item list, as written.
	Goal string
	// Slugs are the items, in the file's order.
	Slugs []string

	// Warnings are what was odd about the file without stopping it from
	// loading — a slug that cannot name an item, a slug written twice.
	Warnings []string
}

// Open reports a sprint that still scopes the backlog. A closed sprint on
// disk is a record, and the whole ready list applies again.
func (sp *Sprint) Open() bool { return sp != nil && sp.Status == SprintOpen }

// GoalPlaceholder is the goal a freshly planned sprint carries until
// somebody writes one. It is a sentence rather than an empty paragraph so
// the file reads as unfinished instead of as a sprint with no purpose, and
// nothing sends it to a model: an unwritten goal is not carried into an
// item's research prompt.
const GoalPlaceholder = "No goal written yet. `/todo sprint goal <text>` says what this set is for."

// Purpose is what a run carries into its research stage: the open sprint's
// goal, or nothing at all. The placeholder counts as nothing — telling a
// model the goal has not been written is worse than telling it there is no
// sprint, because it invites the model to invent one.
func (sp *Sprint) Purpose() string {
	if !sp.Open() {
		return ""
	}
	goal := strings.TrimSpace(sp.Goal)
	if goal == GoalPlaceholder {
		return ""
	}
	return goal
}

// SprintPath is where the active sprint lives under a root.
func SprintPath(root string) string { return filepath.Join(Dir(root), SprintFile) }

// LoadSprint reads the sprint file. A root with no sprint file returns a
// nil sprint and no error: having no sprint is the ordinary case, not a
// failure.
func LoadSprint(root string) (*Sprint, error) {
	path := SprintPath(root)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseSprint(path, string(data))
}

// ParseSprint reads a sprint from its file content. It is lenient in the
// same places an item is: a slug that could never name a file is a warning
// and is left out of the set, while a header with no name or a status off
// the scale is an error, because a sprint that cannot be named cannot be
// archived under that name either.
func ParseSprint(path, content string) (*Sprint, error) {
	block, body, err := splitHeader(content)
	if err != nil {
		return nil, err
	}
	h, err := parseHeader(block)
	if err != nil {
		return nil, err
	}
	sp := &Sprint{Path: path}
	for _, l := range h.lines {
		if !l.field {
			continue
		}
		switch l.key {
		case keyName:
			sp.Name = l.value
		case keyStatus:
			sp.Status = SprintStatus(l.value)
		case keyCreated:
			sp.Created = l.value
		case keySession:
			sp.Session = l.value
		default:
			sp.Extra = append(sp.Extra, Unknown{Key: l.key, Value: l.value})
		}
	}
	if sp.Name == "" {
		return nil, fmt.Errorf("no name in the header")
	}
	if err := ValidSlug(sp.Name); err != nil {
		return nil, fmt.Errorf("name: %w", err)
	}
	if sp.Status == "" {
		sp.Status = SprintOpen
	}
	switch sp.Status {
	case SprintOpen, SprintClosed:
	default:
		return nil, fmt.Errorf("unknown status %q (open, closed)", sp.Status)
	}
	sp.Goal, sp.Slugs, sp.Warnings = parseSprintBody(body)
	return sp, nil
}

// parseSprintBody splits the goal from the list. The list starts at the
// items heading and runs to the next heading, so a person may write their
// own sections under it without shhh reading their bullets as slugs.
func parseSprintBody(body string) (goal string, slugs []string, warnings []string) {
	lines := strings.Split(body, "\n")
	items := -1
	for i, l := range lines {
		if strings.EqualFold(strings.TrimSpace(l), sprintItemsHeading) {
			items = i
			break
		}
	}
	if items < 0 {
		return strings.TrimSpace(body), nil, []string{"no " + sprintItemsHeading + " heading, so the sprint names no items"}
	}
	goal = strings.TrimSpace(strings.Join(lines[:items], "\n"))
	seen := map[string]bool{}
	for _, l := range lines[items+1:] {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "#") {
			break
		}
		if !strings.HasPrefix(t, "- ") {
			continue
		}
		slug := strings.TrimSpace(strings.TrimPrefix(t, "- "))
		if err := ValidSlug(slug); err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		if seen[slug] {
			warnings = append(warnings, fmt.Sprintf("%s is listed twice; the first place it appears is the one that counts", slug))
			continue
		}
		seen[slug] = true
		slugs = append(slugs, slug)
	}
	// An open sprint that names nothing is what "ready" means, so it means
	// nothing is ready. That is a file to fix rather than a backlog to
	// stare at, and nothing else on screen would say which it was.
	if len(slugs) == 0 {
		warnings = append(warnings, "the item list is empty, so nothing is ready while this sprint is open")
	}
	return goal, slugs, warnings
}

// RenderSprint writes a new sprint file. Like an item's Render, it is only
// for a file that does not exist yet; an existing one is line-edited.
func RenderSprint(sp Sprint) string {
	var h header
	h.set(keyName, sp.Name)
	status := sp.Status
	if status == "" {
		status = SprintOpen
	}
	h.set(keyStatus, string(status))
	if sp.Created != "" {
		h.set(keyCreated, sp.Created)
	}
	if sp.Session != "" {
		h.set(keySession, sp.Session)
	}
	for _, f := range sp.Extra {
		if !sprintKnownKeys[f.Key] {
			h.set(f.Key, f.Value)
		}
	}
	var b strings.Builder
	b.WriteString(h.render())
	b.WriteString("\n")
	if goal := strings.TrimSpace(sp.Goal); goal != "" {
		b.WriteString(goal + "\n\n")
	}
	b.WriteString(sprintItemsHeading + "\n")
	for _, slug := range sp.Slugs {
		b.WriteString("- " + slug + "\n")
	}
	return b.String()
}

// SprintArchivePath is where a sprint of that name is filed when it closes.
func SprintArchivePath(root, name string) string {
	return filepath.Join(Dir(root), DoneSubdir, SprintsSubdir, name+".md")
}

// SprintNameTaken reports a name a closed sprint already occupies. A caller
// choosing a name has to ask, because the archive is where the name has to
// be free: a sprint that cannot be filed under its own name is one that
// cannot close, and a sprint that cannot close goes on scoping the ready
// list to slugs that are all finished.
func SprintNameTaken(root, name string) bool {
	_, err := os.Stat(SprintArchivePath(root, name))
	return err == nil
}

// CreateSprint writes the sprint file. One sprint at a time: an existing
// file is refused rather than overwritten, because the set it names may be
// half worked and nothing else records it.
func CreateSprint(root string, sp Sprint) (string, error) {
	if err := ValidSlug(sp.Name); err != nil {
		return "", fmt.Errorf("name: %w", err)
	}
	dir, err := ensureDir(root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, SprintFile)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists; close it before planning another", path)
	}
	if SprintNameTaken(root, sp.Name) {
		return "", fmt.Errorf("%s already holds a sprint named %q; a sprint has to be free to close under its own name", SprintArchivePath(root, sp.Name), sp.Name)
	}
	if err := os.WriteFile(path, []byte(RenderSprint(sp)), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// SprintAdd appends a slug to the list. It is one line added and nothing
// else touched, so a hand-written goal and any section under the list come
// back exactly as they were.
func SprintAdd(path, slug string) error {
	if err := ValidSlug(slug); err != nil {
		return err
	}
	return editSprintBody(path, func(lines []string) ([]string, error) {
		items, last := sprintListRange(lines)
		if items < 0 {
			return nil, fmt.Errorf("%s has no %s heading to add to", path, sprintItemsHeading)
		}
		for _, i := range sprintSlugLines(lines) {
			if sprintSlugAt(lines[i]) == slug {
				return nil, fmt.Errorf("%s is already in the sprint", slug)
			}
		}
		out := append([]string{}, lines[:last+1]...)
		out = append(out, "- "+slug)
		return append(out, lines[last+1:]...), nil
	})
}

// SprintDrop removes a slug's line.
func SprintDrop(path, slug string) error {
	return editSprintBody(path, func(lines []string) ([]string, error) {
		for _, i := range sprintSlugLines(lines) {
			if sprintSlugAt(lines[i]) == slug {
				return append(append([]string{}, lines[:i]...), lines[i+1:]...), nil
			}
		}
		return nil, fmt.Errorf("%s is not in the sprint", slug)
	})
}

// SprintSetGoal replaces the goal paragraph and leaves the header and the
// item list where they are.
func SprintSetGoal(path, goal string) error {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return fmt.Errorf("a goal needs words")
	}
	return editSprintBody(path, func(lines []string) ([]string, error) {
		items, _ := sprintListRange(lines)
		if items < 0 {
			return nil, fmt.Errorf("%s has no %s heading, so there is nothing above it to replace", path, sprintItemsHeading)
		}
		out := []string{""}
		out = append(out, strings.Split(goal, "\n")...)
		out = append(out, "")
		return append(out, lines[items:]...), nil
	})
}

// SprintSetStatus flips the sprint's status line and nothing else.
func SprintSetStatus(path string, status SprintStatus) error {
	return editHeader(path, func(h *header) bool { return h.set(keyStatus, string(status)) })
}

// sprintListRange is the index of the items heading and of the last line
// the list owns — the heading itself when the list is empty, so a new
// bullet still lands under it.
func sprintListRange(lines []string) (heading, last int) {
	heading = -1
	for i, l := range lines {
		if strings.EqualFold(strings.TrimSpace(l), sprintItemsHeading) {
			heading, last = i, i
			break
		}
	}
	if heading < 0 {
		return -1, -1
	}
	for i := heading + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "#") {
			break
		}
		if strings.HasPrefix(t, "- ") {
			last = i
		}
	}
	return heading, last
}

// sprintSlugLines are the indexes of the lines the list owns.
func sprintSlugLines(lines []string) []int {
	heading, _ := sprintListRange(lines)
	if heading < 0 {
		return nil
	}
	var out []int
	for i := heading + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "#") {
			break
		}
		if strings.HasPrefix(t, "- ") {
			out = append(out, i)
		}
	}
	return out
}

func sprintSlugAt(line string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
}

// editSprintBody rewrites the body of the sprint file through edit and puts
// the header back as it was — its line endings and byte-order mark
// included, the way an item's header edit does. An edit that returns an
// error leaves the file alone.
func editSprintBody(path string, edit func([]string) ([]string, error)) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	raw := string(data)
	bom := strings.HasPrefix(raw, "\uFEFF")
	crlf := strings.Contains(raw, "\r\n")
	block, body, err := splitHeader(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	h, err := parseHeader(block)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	lines, err := edit(strings.Split(body, "\n"))
	if err != nil {
		return err
	}
	out := h.render() + strings.Join(lines, "\n")
	if crlf {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	if bom {
		out = "\uFEFF" + out
	}
	return os.WriteFile(path, []byte(out), 0o644)
}

// SprintState is where one of the sprint's slugs stands. It is computed
// from the backlog every time, never recorded in the sprint file: a set
// that carried its own copy of each item's status would be a second place
// for the truth to live and the wrong one to trust.
type SprintState string

const (
	// SprintItemDone is archived — by a run, or by hand.
	SprintItemDone SprintState = "done"
	// SprintItemRunning is in progress.
	SprintItemRunning SprintState = "in progress"
	// SprintItemBlocked needs a person.
	SprintItemBlocked SprintState = "blocked"
	// SprintItemReady is open with every dependency done.
	SprintItemReady SprintState = "ready"
	// SprintItemWaiting is open with a dependency outstanding. It is
	// skipped by the run rather than dropped, and the reason is on the row.
	SprintItemWaiting SprintState = "waiting"
	// SprintItemDropped is a slug the backlog no longer holds.
	SprintItemDropped SprintState = "dropped"
)

// SprintEntry is one slug of the sprint as the view draws it.
type SprintEntry struct {
	Slug string
	// Item is the backlog item, zero when the slug names nothing.
	Item Item
	// State is where it stands, and Waiting the dependencies behind a
	// waiting one.
	State   SprintState
	Waiting []string
}

// Done reports the entry no longer owes the sprint anything: it was
// finished, or the item was dropped from the backlog and cannot be.
func (e SprintEntry) Done() bool {
	return e.State == SprintItemDone || e.State == SprintItemDropped
}

// SprintEntries is the sprint's slugs in the file's order, each placed
// against the backlog as it stands. A nil store or no sprint file is no
// entries.
func (s *Store) SprintEntries() []SprintEntry {
	if s == nil || s.Sprint == nil {
		return nil
	}
	done := s.doneSet()
	out := make([]SprintEntry, 0, len(s.Sprint.Slugs))
	for _, slug := range s.Sprint.Slugs {
		e := SprintEntry{Slug: slug, State: SprintItemDropped}
		if it, ok := s.Find(slug); ok {
			e.Item = it
			switch {
			case it.Status == StatusDone:
				e.State = SprintItemDone
			case it.Status == StatusInProgress:
				e.State = SprintItemRunning
			case it.Status == StatusBlocked:
				e.State = SprintItemBlocked
			case it.Ready(done):
				e.State = SprintItemReady
			default:
				e.State, e.Waiting = SprintItemWaiting, it.Waiting(done)
			}
		}
		out = append(out, e)
	}
	return out
}

// SprintProgress is the sprint's n of m: how many of its slugs are
// finished, out of the ones still in the backlog. A slug dropped from the
// backlog leaves the set rather than counting as finished — a set that
// reported work as done because its item was deleted would be the one
// number here nobody could trust.
func (s *Store) SprintProgress() (done, total int) {
	for _, e := range s.SprintEntries() {
		switch e.State {
		case SprintItemDropped:
		case SprintItemDone:
			done, total = done+1, total+1
		default:
			total++
		}
	}
	return done, total
}

// SprintFinished reports every slug accounted for — finished, or dropped
// from the backlog and so no longer the set's to finish. That is when the
// file stops being a plan and becomes a record.
func (s *Store) SprintFinished() bool {
	entries := s.SprintEntries()
	if len(entries) == 0 {
		return false
	}
	for _, e := range entries {
		if !e.Done() {
			return false
		}
	}
	return true
}

// SprintBudget is how many items of each grade a proposed set may hold, in
// the profile's own order. The grade is the budget's unit because the grade
// is what a run spends on: a sprint of three of the largest is a different
// week from one of nine of the smallest.
//
// It is a list rather than a map because a budget has to state itself back
// — on the plan card, and in the prompt the reading is asked with — in the
// profile's own order, and a map would have to be handed the profile again
// at every one of those points to know what that order was.
type SprintBudget []GradeCount

// GradeCount is one grade's allowance.
type GradeCount struct {
	Grade string
	Count int
}

// String is the budget as every surface states it, and empty for no budget
// at all. A proposal states the budget it was bounded by, so a person
// reading a set can see the shape of the question it answered.
func (b SprintBudget) String() string {
	var parts []string
	for _, g := range b {
		parts = append(parts, fmt.Sprintf("%s=%d", g.Grade, g.Count))
	}
	return strings.Join(parts, " ")
}

// clone is the budget as a set of allowances something is about to spend
// against, so the budget itself is not decremented.
func (b SprintBudget) clone() SprintBudget {
	return append(SprintBudget(nil), b...)
}

// spend takes one item of the grade out of the budget and reports whether
// there was one to take. A grade the budget does not name has no allowance,
// which is what leaves an ungraded item out of a stated budget.
func (b SprintBudget) spend(grade string) bool {
	for i := range b {
		if b[i].Grade == grade && b[i].Count > 0 {
			b[i].Count--
			return true
		}
	}
	return false
}

// Fits reports the budget could admit at least one of the items. A budget
// naming only grades the ready list does not hold admits nothing, and a
// reading spent discovering that is a turn spent on an answer the item
// headers already gave.
func (b SprintBudget) Fits(items []Item) bool {
	if len(b) == 0 {
		return len(items) > 0
	}
	for _, it := range items {
		for _, g := range b {
			if g.Grade == it.Grade() && g.Count > 0 {
				return true
			}
		}
	}
	return false
}

// BudgetFlag is what the flag that asks for a budget is called and the
// shape a spec for it takes — `size` and `S=n,M=n,L=n` for a backlog of
// code — and false for a profile that does not grade its work, which has no
// budget to state. The flag and the words it asks for come from here rather
// than from each surface, so that the option a person is offered and the
// spec the parser accepts cannot say different things.
func BudgetFlag(p Profile) (name, shape string, ok bool) {
	f, ok := p.GradeField()
	if !ok {
		return "", "", false
	}
	parts := make([]string, 0, len(f.Values))
	for _, v := range f.Values {
		parts = append(parts, v.Name+"=n")
	}
	return f.Name, strings.Join(parts, ","), true
}

// ParseSprintBudget reads `S=2,M=1,L=0` in the profile's grades. A grade the
// spec does not name gets no allowance, so a stated budget is the whole of
// what it admits — an ungraded item has no grade to spend and is left out
// until it is graded. A profile that does not grade its work takes no
// budget at all, because there is nothing to count.
func ParseSprintBudget(p Profile, spec string) (SprintBudget, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	f, ok := p.GradeField()
	if !ok {
		return nil, fmt.Errorf("%s items are not graded, so there is nothing to budget", p.Noun)
	}
	counts := map[string]int{}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("%q is not %s=count", part, f.Name)
		}
		grade, ok := f.Canonical(strings.TrimSpace(key))
		if !ok {
			return nil, fmt.Errorf("%q is not a %s (%s)", key, f.Name, f.List())
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &n); err != nil || n < 0 {
			return nil, fmt.Errorf("%q is not a count", value)
		}
		counts[grade] = n
	}
	// The list comes out in the profile's order rather than the spec's, so
	// two people who typed the same budget in a different order read the
	// same line back.
	var b SprintBudget
	for _, v := range f.Values {
		if n, ok := counts[v.Name]; ok {
			b = append(b, GradeCount{Grade: v.Name, Count: n})
		}
	}
	return b, nil
}

// Unblocks counts the active items whose dependencies name this slug. It
// is the one fact about an item's place in the set that a filter can state
// without reading anything.
func (s *Store) Unblocks(slug string) int {
	if s == nil {
		return 0
	}
	n := 0
	for _, it := range s.Items {
		for _, dep := range it.DependsOn {
			if dep == slug {
				n++
				break
			}
		}
	}
	return n
}

// CloseSprint flips the sprint closed and moves the file into the archive
// under its name, with each item's report under its slug and, when the set
// was not finished, what was left. It returns where the file went.
//
// The reports are copied rather than pointed at because the sprint is the
// record of a set of work and an archived item can be edited afterwards;
// what the sprint says the set produced is what it said at the time.
func CloseSprint(p Profile, root string) (string, error) {
	s := Load(p, root)
	if s.Sprint == nil {
		return "", fmt.Errorf("there is no sprint")
	}
	sp := s.Sprint
	if err := os.MkdirAll(filepath.Dir(SprintArchivePath(root, sp.Name)), 0o755); err != nil {
		return "", err
	}
	// Nothing is touched before the name is known to be free. A sprint that
	// half-closed onto an occupied name would keep scoping the ready list
	// to slugs that are all finished, with no item left to unstick it —
	// so the refusal names the way out, which is the header's name line.
	to := SprintArchivePath(root, sp.Name)
	if _, err := os.Stat(to); err == nil {
		return "", fmt.Errorf("%s already exists; change the sprint's name line and close it again", to)
	}
	if err := SprintSetStatus(sp.Path, SprintClosed); err != nil {
		return "", err
	}
	if err := Append(sp.Path, sprintClosingBlock(sp, s.SprintEntries())); err != nil {
		return "", err
	}
	if err := os.Rename(sp.Path, to); err != nil {
		return "", err
	}
	return to, nil
}

// CloseSprintIfDone closes the sprint when every slug it names is
// accounted for, and reports where the file went. It answers "" when
// there is no sprint or the set is still being worked, so a caller can
// call it after every archive without asking first.
func CloseSprintIfDone(p Profile, root string) (string, error) {
	s := Load(p, root)
	if !s.Sprint.Open() || !s.SprintFinished() {
		return "", nil
	}
	return CloseSprint(p, root)
}

// sprintClosingBlock is what is appended to the sprint on its way to the
// archive: the notes, then each item's report under its slug.
func sprintClosingBlock(sp *Sprint, entries []SprintEntry) string {
	var b strings.Builder
	b.WriteString("## Notes\n" + SprintNotes(sp, entries) + "\n\n")
	b.WriteString("## Reports\n")
	for _, e := range entries {
		b.WriteString("\n### " + e.Slug + "\n")
		switch {
		case e.State == SprintItemDropped:
			b.WriteString("Dropped from the backlog before the sprint closed.\n")
		case e.State != SprintItemDone:
			b.WriteString("Not finished: " + string(e.State) + ".\n")
		default:
			if report := ItemReport(e.Item); report != "" {
				b.WriteString(report + "\n")
			} else {
				b.WriteString("Archived with no report.\n")
			}
		}
	}
	return b.String()
}

// SprintNotes is the closed set as release notes: what it was for, every
// item that landed with what was built and the commit that carries it, and
// what was left as deferred.
//
// It is one flat block of plain text because that is what it is for. The
// person's next act after a set closes is a tag, and a tag message is plain
// text they paste; shhh's part ends at the paste. Naming a version and
// making the tag stay theirs — a tool that offered to make one is a tool
// that will one day make the wrong one.
// See docs/capabilities/todo.md#a-sprint-is-what-ships-together.
func SprintNotes(sp *Sprint, entries []SprintEntry) string {
	var parts []string
	// The goal leads, even where the surface around the notes already
	// states it. The notes are pasted whole into a message that will be
	// read somewhere else entirely, and a list of items with nothing saying
	// what they were for is the half of a release note nobody can use.
	if sp != nil {
		if goal := strings.TrimSpace(sp.Goal); goal != "" && goal != GoalPlaceholder {
			parts = append(parts, goal)
		}
	}
	var b strings.Builder
	for _, e := range entries {
		if e.State != SprintItemDone {
			continue
		}
		b.WriteString("- " + sprintNoteLine(e) + "\n")
	}
	if b.Len() > 0 {
		parts = append(parts, strings.TrimRight(b.String(), "\n"))
	}
	b.Reset()
	// An item the set did not finish is named as deferred rather than left
	// off: it went back to the backlog untouched, and notes that listed
	// only what landed would read as a set that planned exactly what it
	// shipped.
	var deferred []SprintEntry
	for _, e := range entries {
		if !e.Done() {
			deferred = append(deferred, e)
		}
	}
	if len(deferred) > 0 {
		b.WriteString("Deferred:\n")
		for _, e := range deferred {
			title := e.Item.Title
			if title == "" {
				title = e.Slug
			}
			fmt.Fprintf(&b, "- %s (%s) — %s, back in the backlog\n", title, e.Slug, e.State)
		}
		parts = append(parts, strings.TrimRight(b.String(), "\n"))
	}
	if len(parts) == 0 {
		return "The set finished nothing and left nothing."
	}
	return strings.Join(parts, "\n\n")
}

// sprintNoteLine is one landed item as the notes state it: its title, what
// the run said it built, and the commit that carries it.
func sprintNoteLine(e SprintEntry) string {
	line := e.Slug
	if e.Item.Title != "" {
		line = e.Item.Title + " (" + e.Slug + ")"
	}
	if built := ItemSummary(e.Item); built != "" {
		line += " — " + built
	}
	if commit := ItemCommit(e.Item); commit != "" {
		line += " (" + commit + ")"
	}
	return line
}

// ItemReport is the `## Report` section of an item's body, or "" when it
// has none — an item archived by hand rather than by a run.
func ItemReport(it Item) string { return itemSection(it.Body, "## Report") }

// The lines a report carries that are read back rather than only read: what
// the run summarised the work as, and the commit it made.
const (
	summaryPrefix = "Summary:"
	commitPrefix  = "Commit:"
)

// CommitLine is the line an archived item carries naming the commit its run
// made, and "" for a run that made none.
//
// It is written when the item is archived rather than asked of git later,
// because whether the backlog directory is committed at all is the
// project's decision: in a checkout that ignores `.shhh` there is no
// history to ask which commit carried an item, and the set's notes still
// have to name one.
func CommitLine(head, message string) string {
	subject := firstLine(message)
	if subject == "" {
		return ""
	}
	if head = shortHead(strings.TrimSpace(head)); head != "" {
		subject = head + " " + subject
	}
	return commitPrefix + " " + subject + "\n"
}

// ItemSummary is what an item's report says was built: the labelled line a
// run writes, or the report's first line for one a person wrote by hand.
// The fallback is there because the archive holds both, and notes that
// named only what a run produced would leave a hand-finished item as a
// title with nothing after it.
func ItemSummary(it Item) string {
	if summary := reportField(it, summaryPrefix); summary != "" {
		return summary
	}
	return firstLine(ItemReport(it))
}

// ItemCommit is the commit an archived item names, and "" for one that
// names none — archived by hand, or a run asked for without a commit.
func ItemCommit(it Item) string { return reportField(it, commitPrefix) }

// reportField is the value of one labelled line of an item's report. The
// report is prose a person may have edited, so a label it does not hold is
// "" rather than an error.
func reportField(it Item, prefix string) string {
	for _, l := range strings.Split(ItemReport(it), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(l), prefix); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// firstLine is the first non-empty line of a block, which for a commit
// message is its subject.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// ItemBlock is the `## Blocked` section: what stopped the item, in the
// run's own evidence. A sprint that stopped names it beside the item that
// wrote it, because "blocked" on its own is a state and not a reason.
func ItemBlock(it Item) string { return itemSection(it.Body, "## Blocked") }

// itemSection is one `## `-headed section of a body, to the next heading of
// the same level or the end. It is a reader over prose a person may have
// edited, so a heading it cannot find is "" rather than an error.
func itemSection(body, heading string) string {
	lines := strings.Split(body, "\n")
	start := -1
	for i, l := range lines {
		if strings.EqualFold(strings.TrimSpace(l), heading) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
			end = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}
