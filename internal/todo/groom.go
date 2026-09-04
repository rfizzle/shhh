package todo

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Grooming: an item read against the checkout as it stands now.
//
// An item written weeks ago names files, functions, flags and dependencies
// that have since moved, and states what the code does today. A run finds
// that out three stages in, on a plan built on the wrong file. A reading
// finds it out first, and answers with a verdict per claim rather than with
// prose — because what comes of a reading is a diff, and a diff needs a fact
// per line. See docs/capabilities/todo.md#an-item-is-checked-before-it-is-worked.

// Verdict is one reading of one claim an item makes. The set is closed:
// "this may need updating" is not a verdict, it is a sentence that can be
// said about everything, and a reading that can say it will.
type Verdict string

const (
	// VerdictHolds is a claim the tree still bears out. It proposes no edit.
	VerdictHolds Verdict = "holds"
	// VerdictMoved is a reference to something that is still there under
	// another name or in another file.
	VerdictMoved Verdict = "moved"
	// VerdictChanged is a statement about what the code does that is no
	// longer what it does.
	VerdictChanged Verdict = "changed"
	// VerdictGone is a reference to something the tree no longer holds.
	VerdictGone Verdict = "gone"
	// VerdictDone is an acceptance criterion the tree already satisfies.
	VerdictDone Verdict = "already done"
	// VerdictUnknown is a claim the reading could not settle either way. It
	// proposes no edit: a guess written into the item is worse than the
	// stale line it replaced, because nothing marks it as a guess.
	VerdictUnknown Verdict = "unknown"
)

// Verdicts is the closed set in the order the prompt offers it and a card
// lists it. It is the one place the words are written, so the prompt that
// asks for them and the parser that reads them cannot come to disagree.
func Verdicts() []Verdict {
	return []Verdict{VerdictHolds, VerdictMoved, VerdictChanged, VerdictGone, VerdictDone, VerdictUnknown}
}

// verdictOf reads a verdict word, and reports whether it is one of the set.
func verdictOf(s string) (Verdict, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, v := range Verdicts() {
		if string(v) == s {
			return v, true
		}
	}
	return "", false
}

// Finding is one claim read: what the item says, what the tree says about
// it, and the one line the reading would write in its place.
type Finding struct {
	Verdict Verdict `json:"verdict"`
	// Claim is the item's own line, as the reading quoted it back.
	Claim string `json:"claim"`
	// Now is the line as the reading would have it read. Empty against a
	// verdict that proposes an edit means the line goes.
	Now string `json:"now,omitempty"`
	// Evidence is the one line of free text a verdict is allowed: the
	// path, the symbol or the commit the verdict was read off. Everything
	// else in the answer is a word from a closed set.
	Evidence string `json:"evidence,omitempty"`
	// Line is where the claim was found in the file, counting from one, and
	// zero for a claim that matched no line. A claim that matched nothing
	// can still be read — the verdict and its evidence stand — but it
	// cannot be a line edit, because there is no line to edit.
	Line int `json:"line,omitempty"`
	// Criterion reports the claim is one of the item's acceptance criteria,
	// which is what decides whether the tree has finished the item rather
	// than merely outrun its prose.
	Criterion bool `json:"criterion,omitempty"`
}

// Edits reports that the finding proposes a change to its line. A claim that
// holds and one the reading could not settle propose none, and neither does
// a line the reading would write back exactly as it found it.
func (f Finding) Edits() bool {
	switch f.Verdict {
	case VerdictHolds, VerdictUnknown:
		return false
	}
	return f.Line > 0 && strings.TrimRight(f.Now, " \t") != strings.TrimRight(f.Claim, " \t")
}

// Reading is one item read against one commit: every claim's verdict, and
// the head the tree was at. It is written down after the person accepts it,
// so a run started later can be handed the reading instead of taking it
// again.
type Reading struct {
	Slug string `json:"slug"`
	// Head is the commit the reading was taken against, empty outside a
	// repository.
	Head string `json:"head"`
	// Read is when it was taken, which is what dates the header's stamp.
	Read time.Time `json:"read"`
	// Accepted is when the person accepted it, stamped by the write rather
	// than by the reading. It is a second time and not the first one
	// re-used because accepting a reading edits the item, which moves the
	// file's own timestamp past the moment the reading was taken: a
	// freshness rule measured from Read would call every accepted reading
	// stale the instant it landed.
	Accepted time.Time `json:"accepted,omitempty"`
	Findings []Finding `json:"findings"`
}

// since is the moment an edit to the item has to beat for the reading to
// still be the newer account of it.
func (r Reading) since() time.Time {
	if r.Accepted.IsZero() {
		return r.Read
	}
	return r.Accepted
}

// Changes are the findings that propose a line, in the order they were read.
func (r Reading) Changes() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Edits() {
			out = append(out, f)
		}
	}
	return out
}

// Unplaced is how many findings would have proposed a line and could not,
// because the claim they quoted matched nothing in the file. They are worth
// counting rather than dropping: a verdict with nowhere to land is a reading
// the person cannot act on, and a card that simply left it out would read as
// a reading that found nothing there.
func (r Reading) Unplaced() int {
	n := 0
	for _, f := range r.Findings {
		switch f.Verdict {
		case VerdictHolds, VerdictUnknown:
		default:
			if f.Line == 0 {
				n++
			}
		}
	}
	return n
}

// Count is how many findings carry a verdict.
func (r Reading) Count(v Verdict) int {
	n := 0
	for _, f := range r.Findings {
		if f.Verdict == v {
			n++
		}
	}
	return n
}

// Finished reports that the tree already satisfies every acceptance
// criterion the item states. Such an item is proposed for archiving and
// never archived: an item finished by work nobody filed under it is exactly
// the case where the person has to say so.
func (r Reading) Finished() bool {
	criteria := 0
	for _, f := range r.Findings {
		if !f.Criterion {
			continue
		}
		if f.Verdict != VerdictDone {
			return false
		}
		criteria++
	}
	return criteria > 0
}

// Report is the evidence behind a finished item, in the shape an archived
// item's report takes, so the person who accepts the proposal has the record
// already written.
func (r Reading) Report() string {
	var b strings.Builder
	b.WriteString("## Report\nSummary: the tree already satisfies every acceptance criterion; this item was read against it rather than worked.\n")
	for _, f := range r.Findings {
		if f.Criterion && f.Evidence != "" {
			fmt.Fprintf(&b, "- %s — %s\n", f.Claim, f.Evidence)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// Stamp is the value the header's groomed line carries: the date the reading
// was taken and the commit it was taken against.
//
// The head is the load-bearing half. Staleness is how many commits the tree
// has taken since the reading, which the repository can compute; a date says
// how long the person waited, which is a different question and not the one
// a run needs answered.
func (r Reading) Stamp() string {
	date := r.Read.Format("2006-01-02")
	if r.Head == "" {
		return date
	}
	return date + " @ " + shortHead(r.Head)
}

// shortHead is a commit as a header line states it.
func shortHead(head string) string {
	if len(head) > 7 {
		return head[:7]
	}
	return head
}

// Groom reads a grooming answer against the item file as it stands, and
// resolves each claim to the line it is about. The file is re-read here
// rather than taken from the item in hand because the reading took a model
// turn, and a turn is long enough for the person to have edited the file.
func Groom(it Item, answer string) (Reading, error) {
	data, err := os.ReadFile(it.Path)
	if err != nil {
		return Reading{}, err
	}
	r := Reading{Slug: it.Slug, Read: time.Now(), Findings: parseFindings(answer)}
	lines := fileLines(string(data))
	criteria := criterionLines(lines)
	for i := range r.Findings {
		f := &r.Findings[i]
		f.Line = matchLine(lines, f.Claim)
		if f.Line > 0 {
			// The claim is quoted back by a model and a quote drifts; the
			// file's own text is what an edit is measured against.
			f.Claim = lines[f.Line-1]
			f.Criterion = criteria[f.Line]
		}
	}
	return r, nil
}

// The answer's shape: one block per claim, each line a marker the parser
// reads by prefix. A block that names no verdict from the set is dropped
// rather than guessed at.
const (
	claimMarker    = "claim:"
	verdictMarker  = "verdict:"
	nowMarker      = "now:"
	evidenceMarker = "evidence:"
)

// parseFindings reads the blocks out of the answer. Prose around them costs
// nothing: a line that is none of the markers ends the block it was in and
// is otherwise ignored.
func parseFindings(answer string) []Finding {
	var out []Finding
	var cur *Finding
	flush := func() {
		if cur != nil && cur.Verdict != "" && cur.Claim != "" {
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, raw := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(raw)
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")
		switch {
		case hasMarker(line, claimMarker):
			flush()
			cur = &Finding{Claim: unfence(value(line, claimMarker))}
		case cur == nil:
		case hasMarker(line, verdictMarker):
			if v, ok := verdictOf(value(line, verdictMarker)); ok {
				cur.Verdict = v
			}
		case hasMarker(line, nowMarker):
			cur.Now = unfence(value(line, nowMarker))
		case hasMarker(line, evidenceMarker):
			cur.Evidence = value(line, evidenceMarker)
		}
	}
	flush()
	return out
}

func hasMarker(line, marker string) bool {
	return len(line) >= len(marker) && strings.EqualFold(line[:len(marker)], marker)
}

func value(line, marker string) string {
	return strings.TrimSpace(line[len(marker):])
}

// unfence takes the backticks off a line the model quoted as code. An item's
// own line has none, and a claim that kept them would match nothing.
func unfence(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '`' && s[len(s)-1] == '`' {
		return strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// fileLines splits a file the way an editor numbers it, whatever wrote it.
func fileLines(raw string) []string {
	raw = strings.TrimPrefix(raw, "\uFEFF")
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	return strings.Split(raw, "\n")
}

// matchLine is the file line a claim is about, counting from one, or zero.
// An exact match on the trimmed text comes first and a containment match
// second, because a model quoting a line back drops its indentation far more
// often than it invents a line that happens to hold another one.
func matchLine(lines []string, claim string) int {
	claim = strings.TrimSpace(claim)
	if claim == "" {
		return 0
	}
	for i, l := range lines {
		if strings.TrimSpace(l) == claim {
			return i + 1
		}
	}
	for i, l := range lines {
		if strings.Contains(l, claim) {
			return i + 1
		}
	}
	return 0
}

// criterionLines is which of the file's lines are acceptance criteria: the
// checkbox bullets under the criteria heading, and nothing else. A heading
// is matched on the word rather than on the exact wording, because the
// template's heading is a person's to reword.
func criterionLines(lines []string) map[int]bool {
	out := map[int]bool{}
	in := false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "#") {
			in = strings.Contains(strings.ToLower(t), "acceptance criteria")
			continue
		}
		if in && isCheckbox(t) {
			out[i+1] = true
		}
	}
	return out
}

// isCheckbox reports a Markdown task bullet, ticked or not.
func isCheckbox(t string) bool {
	for _, bullet := range []string{"- ", "* "} {
		if rest, ok := strings.CutPrefix(t, bullet); ok {
			return strings.HasPrefix(rest, "[ ]") || strings.HasPrefix(rest, "[x]") || strings.HasPrefix(rest, "[X]")
		}
	}
	return false
}

// Accept writes the findings the person accepted and stamps the header. It
// reports how many lines it changed and the accepted findings it did not
// write.
//
// Every accepted change is one line. A header field goes through the header
// writer so its value is quoted the way every other write quotes it; a body
// line is replaced where it stands, and prose the reading did not name is
// not read, reflowed or rewritten.
// See docs/capabilities/todo.md#an-item-is-a-file-you-can-edit.
//
// **One line takes one change.** Two verdicts can land on the same line —
// two finished dependencies on one depends_on list is the ordinary case —
// and each proposes that whole line as it should read. Applying both would
// write the second over the first and count two, which reads to the person
// as two corrections written when one of them was thrown away. So the first
// is written and the rest come back as skipped, for the surface to name:
// there is no composing two replacements of one line into the line both
// meant, and inventing one is how a groomer starts editing the backlog on
// its own account.
func Accept(path string, findings []Finding, stamp string) (changed int, skipped []Unwritten, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, nil, err
	}
	raw := string(data)
	bom := strings.HasPrefix(raw, "\uFEFF")
	crlf := strings.Contains(raw, "\r\n")
	block, body, err := splitHeader(raw)
	if err != nil {
		return 0, nil, fmt.Errorf("%s: %w", path, err)
	}
	h, err := parseHeader(block)
	if err != nil {
		return 0, nil, fmt.Errorf("%s: %w", path, err)
	}
	// The header block starts on line 2 and the body on the line after the
	// closing --- , which is the line after the block's last.
	bodyFirst := len(block) + 3
	bodyLines := strings.Split(body, "\n")
	// The reading took a model turn, and a turn is long enough for the
	// person to have opened the file. A finding names a line by number and
	// by the text that was on it; the number alone would write the
	// correction onto whatever has since moved there.
	current := fileLines(raw)
	written := map[int]bool{}
	var drop []int
	for _, f := range findings {
		if !f.Edits() || f.Line > len(current) || current[f.Line-1] != f.Claim {
			continue
		}
		if written[f.Line] {
			skipped = append(skipped, Unwritten{Finding: f, Why: WhyLineTaken})
			continue
		}
		applied := false
		switch {
		case f.Line >= 2 && f.Line <= len(block)+1:
			if applied = editHeaderLine(&h, f); !applied {
				skipped = append(skipped, Unwritten{Finding: f, Why: WhyOtherField})
			}
		case f.Line >= bodyFirst && f.Line-bodyFirst < len(bodyLines):
			i := f.Line - bodyFirst
			if f.Now == "" {
				drop = append(drop, i)
			} else {
				bodyLines[i] = f.Now
			}
			applied = true
		}
		if applied {
			written[f.Line] = true
			changed++
		}
	}
	bodyLines = without(bodyLines, drop)
	if stamp != "" && h.set(keyGroomed, stamp) {
		changed++
	}
	if changed == 0 {
		return 0, skipped, nil
	}
	out := h.render() + strings.Join(bodyLines, "\n")
	if crlf {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	if bom {
		out = "\uFEFF" + out
	}
	return changed, skipped, os.WriteFile(path, []byte(out), 0o644)
}

// Unwritten is an accepted finding the file's line would not take, with the
// reason a surface puts on the row. It exists because a correction the
// person checked and cannot find in the file afterwards is worse than one
// that was refused out loud.
type Unwritten struct {
	Finding
	Why string
}

// The reasons a line refuses an accepted correction.
const (
	// WhyLineTaken is a second correction of a line another accepted one
	// has already rewritten.
	WhyLineTaken = "another accepted change had already rewritten that line"
	// WhyOtherField is a header line whose replacement names a different
	// field, which is two edits with one of them unstated.
	WhyOtherField = "the line it would write names a different header field"
)

// editHeaderLine applies a finding that landed on a header field. The line
// the reading wrote is read as a field of its own, so a value the writer
// would have to quote is quoted: an accepted line goes through the same
// writer a status change does rather than being pasted into the block.
func editHeaderLine(h *header, f Finding) bool {
	key, _, ok := strings.Cut(f.Claim, ":")
	key = strings.TrimSpace(key)
	if !ok || key == "" {
		return false
	}
	if f.Now == "" {
		return h.remove(key)
	}
	newKey, value, ok := strings.Cut(f.Now, ":")
	if !ok || strings.TrimSpace(newKey) != key {
		// A rewrite that renames the field is not a line edit to this
		// field, it is two edits with one of them unstated. The reading
		// keeps its verdict and the file keeps its line.
		return false
	}
	return h.set(key, unquote(value))
}

// without drops the lines at the given indices, highest first so the ones
// still to go do not move under the deletion.
func without(lines []string, drop []int) []string {
	if len(drop) == 0 {
		return lines
	}
	keep := make(map[int]bool, len(drop))
	for _, i := range drop {
		keep[i] = true
	}
	out := make([]string, 0, len(lines))
	for i, l := range lines {
		if !keep[i] {
			out = append(out, l)
		}
	}
	return out
}

// GroomedHead is the commit an item was last groomed against, and empty for
// one that never was or one whose stamp names no commit.
func GroomedHead(it Item) string {
	_, head, ok := strings.Cut(it.Groomed, "@")
	if !ok {
		return ""
	}
	return strings.TrimSpace(head)
}

// DefaultStaleCommits is how far behind a reading may fall before the
// surfaces say so. Fifty is a few days of a busy checkout and a month of a
// quiet one, which is the point: it counts the tree's movement rather than
// the calendar's.
const DefaultStaleCommits = 50

// Behind is how many commits the tree has taken since head, and whether the
// question could be answered at all. Outside a repository, without a git
// binary, or against a commit this checkout does not hold, it cannot be —
// and a count nobody could take must not read as zero, because zero is the
// answer that says the reading is current.
func Behind(root, head string) (int, bool) {
	if head == "" {
		return 0, false
	}
	out, err := exec.Command("git", "-C", root, "rev-list", "--count", head+"..HEAD").Output()
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// Stale is how far behind each groomed item's reading has fallen, for the
// items that are further behind than the threshold allows. An item nobody
// has groomed is absent from the map: absence is not staleness, and a
// backlog that warned about every item nobody had read yet would be a
// backlog of warnings.
//
// A threshold of zero takes the default; a negative one turns the reading
// off, which is the answer for a project that grooms by hand.
func Stale(root string, items []Item, threshold int) map[string]int {
	switch {
	case threshold < 0:
		return nil
	case threshold == 0:
		threshold = DefaultStaleCommits
	}
	var out map[string]int
	for _, it := range items {
		head := GroomedHead(it)
		if head == "" {
			continue
		}
		n, ok := Behind(root, head)
		if !ok || n <= threshold {
			continue
		}
		if out == nil {
			out = map[string]int{}
		}
		out[it.Slug] = n
	}
	return out
}

// groomPath is where an accepted reading is kept: beside the run
// checkpoints, under the scratch directory the backlog's own ignore file
// already covers. It is scratch rather than part of the item because the
// item is the person's file, and a reading is shhh's note about it.
func groomPath(root, slug string) string {
	return filepath.Join(Dir(root), RunSubdir, slug+".groom.json")
}

// SaveReading writes an accepted reading down, so a run started later can be
// handed it instead of paying for the same reading again. It is called after
// the accepted lines are written, and stamps the moment it was called: what
// a later edit is measured against is when the reading was agreed to, not
// when it was taken.
func SaveReading(root string, r Reading) error {
	r.Accepted = time.Now()
	if err := os.MkdirAll(filepath.Join(Dir(root), RunSubdir), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(groomPath(root, r.Slug), data, 0o644)
}

// LoadReading is the last accepted reading of an item, and whether there is
// one that still stands. A reading older than the item's own file does not:
// the person edited the item after accepting it, and what they wrote is
// newer than what was read.
func LoadReading(root, slug string) (Reading, bool) {
	data, err := os.ReadFile(groomPath(root, slug))
	if err != nil {
		return Reading{}, false
	}
	var r Reading
	if err := json.Unmarshal(data, &r); err != nil || r.Slug != slug {
		return Reading{}, false
	}
	st, err := os.Stat(filepath.Join(Dir(root), slug+".md"))
	if err != nil || st.ModTime().After(r.since()) {
		return Reading{}, false
	}
	return r, true
}

// DiscardReading drops an item's accepted reading. It is called where the
// item leaves the active backlog, because a reading is about work still to
// do and the scratch file would otherwise outlive every item that ever had
// one.
func DiscardReading(root, slug string) {
	_ = os.Remove(groomPath(root, slug))
}

// GroomingBlock is the accepted reading as a stage prompt states it, and
// empty where there is none that still stands. It says what was read and
// what the person accepted, so a stage that would otherwise read the item
// against the tree again knows that reading has been done and when.
func GroomingBlock(root, slug string) string {
	r, ok := LoadReading(root, slug)
	if !ok {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "GROOMING — this item was read against the tree on %s", r.Read.Format("2006-01-02"))
	if r.Head != "" {
		fmt.Fprintf(&b, " at commit %s", shortHead(r.Head))
	}
	b.WriteString(", and the corrections below were accepted into the file you have. Do not read the item against the tree again; take it as it stands and plan the work.\n")
	for _, f := range r.Findings {
		if f.Verdict == VerdictHolds {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s", f.Verdict, f.Claim)
		if f.Evidence != "" {
			fmt.Fprintf(&b, " — %s", f.Evidence)
		}
		b.WriteString("\n")
	}
	return b.String()
}
