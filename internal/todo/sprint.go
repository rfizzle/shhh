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
	Extra   []Field

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
			sp.Extra = append(sp.Extra, Field{Key: l.key, Value: l.value})
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

// SprintBudget is how many items of each size a proposed set may hold.
// Size is the budget's unit because size is what the runner gates on: a
// sprint of three L items is a different week from one of nine S ones.
type SprintBudget map[Size]int

// ParseSprintBudget reads `S=2,M=1,L=0`. A size the spec does not name gets
// no allowance, so a stated budget is the whole of what it admits — an
// ungraded item has no size to spend and is left out until it is graded.
func ParseSprintBudget(spec string) (SprintBudget, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	b := SprintBudget{}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("%q is not size=count", part)
		}
		size := Size(strings.ToUpper(strings.TrimSpace(key)))
		switch size {
		case SizeS, SizeM, SizeL:
		default:
			return nil, fmt.Errorf("%q is not a size (S, M, L)", key)
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &n); err != nil || n < 0 {
			return nil, fmt.Errorf("%q is not a count", value)
		}
		b[size] = n
	}
	return b, nil
}

// ProposeSprint is the ready list in backlog order under a budget: a
// filter, not a recommendation. Nothing is read and nothing is judged —
// the order is the backlog's own, which a reader can recompute from the
// headers, and the budget only says when to stop.
func (s *Store) ProposeSprint(budget SprintBudget) []Item {
	if s == nil {
		return nil
	}
	ready := s.readyAll()
	if budget == nil {
		return ready
	}
	left := SprintBudget{}
	for size, n := range budget {
		left[size] = n
	}
	var out []Item
	for _, it := range ready {
		if left[it.Size] <= 0 {
			continue
		}
		left[it.Size]--
		out = append(out, it)
	}
	return out
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
func CloseSprint(root string) (string, error) {
	s := Load(root)
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
	if err := Append(sp.Path, sprintClosingBlock(s.SprintEntries())); err != nil {
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
func CloseSprintIfDone(root string) (string, error) {
	s := Load(root)
	if !s.Sprint.Open() || !s.SprintFinished() {
		return "", nil
	}
	return CloseSprint(root)
}

// sprintClosingBlock is what is appended to the sprint on its way to the
// archive: each item's report under its slug, then what was left undone.
func sprintClosingBlock(entries []SprintEntry) string {
	var b strings.Builder
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
	var left []string
	for _, e := range entries {
		if !e.Done() {
			left = append(left, e.Slug+" ("+string(e.State)+")")
		}
	}
	if len(left) > 0 {
		b.WriteString("\n## Left undone\n")
		for _, l := range left {
			b.WriteString("- " + l + "\n")
		}
	}
	return b.String()
}

// ItemReport is the `## Report` section of an item's body, or "" when it
// has none — an item archived by hand rather than by a run.
func ItemReport(it Item) string {
	lines := strings.Split(it.Body, "\n")
	start := -1
	for i, l := range lines {
		if strings.EqualFold(strings.TrimSpace(l), "## Report") {
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
