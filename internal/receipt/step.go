package receipt

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rfizzle/shhh/internal/tools"
)

// State is how one call stands. A receipt reads what the result says; that a
// call is still running, or was refused, is the session's to say, which is
// why it is an input to a step rather than something the receipt reads.
type State int

const (
	// StateDone is a call that came back. Whether it broke is then the
	// receipt's to say: a tool that answered with an error, a command that
	// did not succeed.
	StateDone State = iota
	// StateRunning is a call still in flight.
	StateRunning
	// StateFailed is a call that broke: the session says so where the result
	// cannot — a command the ceiling or a signal ended — and the receipt
	// says so everywhere else.
	StateFailed
	// StateRefused is a call that never ran, or was stopped: a person or a
	// rule said no, the queue skipped it, the reader cancelled it. Nothing
	// it would have done is counted.
	StateRefused
)

// Act is one call of a step: its receipt, how it stands where the session
// knows more than the result, and what it took.
type Act struct {
	Receipt  Receipt
	State    State
	Duration time.Duration
}

// state is the call's standing with the receipt's own reading folded in: a
// call the session calls done broke if its result says it did.
func (a Act) state() State {
	if a.State == StateDone && a.Receipt.broke() {
		return StateFailed
	}
	return a.State
}

// broke reports whether the result says the call broke: a tool that answered
// with an error, a command that did not succeed. A command handed off to the
// supervisor is working elsewhere, which is not a break.
func (r Receipt) broke() bool {
	return r.failed || (r.command && r.Ended.Failed())
}

// Mark is one call as the strip shows it, and the step as its header's glyph
// shows it: the kind of act, and how it stands.
type Mark struct {
	Kind  Kind
	State State
}

// Tally is the calls of one kind in a step, as the step's receipt counts
// them.
type Tally struct {
	Kind  Kind
	Calls int
	// Files is how many different files the calls named, for reads and
	// writes, and zero where any call of the kind named none: a git log or a
	// memory is not a file, and counting it as one would put a number on
	// the header that is of what was called rather than of what was read.
	Files int
	// Subject is what the kind's one call was about, while it has one: a
	// kind with a single call names it rather than counting it, since `ran
	// 1 command` says less than the command does in the same columns.
	// Counting starts at two.
	Subject string
}

// Dir is one directory of a step's reads and the files read in it, in the
// order they were first read. Name ends in a slash, as the header spells it.
type Dir struct {
	Name  string
	Files []string
}

// Evidence is the one line a step's footer shows: the tool's own text, cut
// to one line, never a sentence composed about it. Line is empty for a step
// with nothing to show.
type Evidence struct {
	Line string
	// Path is the file a write's line is from, and empty on every other
	// line, whose call is already named by the header.
	Path string
	// Call is the place in the strip of the call the line was cut from.
	Call int
}

// Step is what a card states about one step, computed once so the header,
// the footer, the open card and the outline all draw the same facts.
type Step struct {
	// Tallies is the calls by kind, in the order each kind was first used,
	// leaving out calls that never ran.
	Tallies []Tally
	// Reads is the step's reads by directory, the directory with the most
	// files first, and empty where the reads did not all name a file.
	Reads []Dir
	// Added and Removed are the lines the step's edits added and removed.
	Added, Removed int
	// Duration is what the calls took, summed: a step's calls carry no wall
	// clock of their own, and the sum is the honest number for what the step
	// cost.
	Duration time.Duration
	// Lead is the step's glyph, picked by precedence (lead).
	Lead Mark
	// Running reports a call of the step still in flight.
	Running bool
	// Rail reports that a call that ran carries the mutation rail, so the
	// card carries it too.
	Rail bool
	// Evidence is the footer's line, picked by the precedence that picked
	// Lead, so the line is always about what the glyph says.
	Evidence Evidence
	// Strip is every call in the order it was made, refused ones included.
	Strip []Mark
	// Groups is the calls as an open card lists them: one group per kind,
	// in the order the tallies count kinds, then a group for each kind
	// whose every call was refused.
	Groups []Group
}

// Group is the calls of one kind of a step, under the line an open card
// heads them with. The strip keeps the order the calls were made in; the
// groups are the same calls sorted by what they did, so a reader can find
// the commands without walking past the reads.
type Group struct {
	Kind Kind
	// Calls are the group's places in the strip, in the order they were
	// made, refused ones included.
	Calls []int
	// Label is the group's line in the receipt's verbs: the kind's clause
	// without the reads' directory clause, which the open card draws as a
	// row per directory under it.
	Label string
	// Duration is what the group's calls that ran took, summed.
	Duration time.Duration
	// Added and Removed are the lines the group's edits changed.
	Added, Removed int
}

// ReadDirCeiling is how many directories the header names behind a step's
// read count before the rest are a `…`. The rollup exists to say where a
// step read without listing what; past three places the clause is the list
// again, and the open card groups every directory anyway. The artboard's
// large step names two and does not say where it stops, so three stands
// until the artboard does.
const ReadDirCeiling = 3

// evidenceMaxBytes bounds the evidence line in bytes. The footer clips it
// to the pane, so this is never the cut a reader sees: it is wider than any
// row a card is drawn in, and is there so that one line of a minified file
// is not a megabyte carried on every repaint.
const evidenceMaxBytes = 512

// BuildStep reads the receipt of a step from its calls, in the order they
// were made.
func BuildStep(acts []Act) Step {
	var s Step
	at := map[Kind]int{}
	files := map[Kind][]string{}
	pathless := map[Kind]bool{}
	for _, a := range acts {
		st := a.state()
		s.Strip = append(s.Strip, Mark{Kind: a.Receipt.Kind, State: st})
		s.Duration += a.Duration
		if st == StateRunning {
			s.Running = true
		}
		if st == StateRefused {
			continue
		}
		r := a.Receipt
		if r.Rail() {
			s.Rail = true
		}
		if r.Kind == KindWrite && r.Hunk != nil {
			s.Added += r.Hunk.Added
			s.Removed += r.Hunk.Removed
		}
		i, ok := at[r.Kind]
		if !ok {
			i = len(s.Tallies)
			at[r.Kind] = i
			s.Tallies = append(s.Tallies, Tally{Kind: r.Kind})
		}
		s.Tallies[i].Calls++
		if s.Tallies[i].Calls == 1 {
			s.Tallies[i].Subject = r.Subject
			if r.gitVerb != "" {
				// Git's writing half is named by its own act — `ran commit
				// …`, `ran add …` — since what follows `ran` is read as a
				// command line, and a commit message alone is not one.
				s.Tallies[i].Subject = strings.TrimSpace(r.gitVerb + " " + r.Subject)
			}
		}
		if r.Kind != KindRead && r.Kind != KindWrite {
			continue
		}
		p := r.Path()
		if p == "" {
			pathless[r.Kind] = true
		} else if !slices.Contains(files[r.Kind], p) {
			files[r.Kind] = append(files[r.Kind], p)
		}
	}
	for i, t := range s.Tallies {
		if !pathless[t.Kind] {
			s.Tallies[i].Files = len(files[t.Kind])
		}
	}
	if !pathless[KindRead] {
		s.Reads = byDir(files[KindRead])
	}
	s.Lead, s.Evidence = lead(acts, s)
	s.Groups = s.groups(acts)
	return s
}

// groups sorts the calls under their kinds, in the tallies' order. A call
// that was refused is listed under its kind with the calls that ran, and a
// kind nothing of which ran is a group of its own at the end, counted as
// what it is: refused.
func (s Step) groups(acts []Act) []Group {
	gs := make([]Group, 0, len(s.Tallies))
	at := map[Kind]int{}
	for _, t := range s.Tallies {
		at[t.Kind] = len(gs)
		gs = append(gs, Group{Kind: t.Kind, Label: s.groupLabel(t)})
	}
	for i, a := range acts {
		k := a.Receipt.Kind
		g, ok := at[k]
		if !ok {
			g = len(gs)
			at[k] = g
			gs = append(gs, Group{Kind: k})
		}
		gs[g].Calls = append(gs[g].Calls, i)
		if a.state() == StateRefused {
			continue
		}
		gs[g].Duration += a.Duration
		if h := a.Receipt.Hunk; h != nil && k == KindWrite {
			gs[g].Added += h.Added
			gs[g].Removed += h.Removed
		}
	}
	for i, g := range gs {
		if g.Label == "" {
			gs[i].Label = counted(len(g.Calls), "refused call", "refused calls")
		}
	}
	return gs
}

// groupLabel is a kind's clause as its group's line says it: the receipt's
// own, except that reads are counted without saying where, since the
// directories are the rows under the line.
func (s Step) groupLabel(t Tally) string {
	if t.Kind == KindRead && t.Files > 0 && (t.Calls > 1 || t.Subject == "") {
		return "read " + counted(t.Files, "file", "files")
	}
	return s.phrase(t)
}

// Line is the one line an open card's row shows beside a call's subject:
// the line a call that broke said it with, or an edit's first changed line.
// Any other call's row is its subject and its outcome, and Line is empty.
// Like the footer's evidence it is the tool's own text cut to one line.
func (a Act) Line() string {
	r := a.Receipt
	switch {
	case a.state() == StateFailed:
		return cut(errorLine(r))
	case r.Kind == KindWrite && r.Hunk != nil && a.state() == StateDone:
		return cut(r.Hunk.Marked())
	}
	return ""
}

// byDir groups paths by their directory, the directory holding the most
// first and, between two holding as many, the one read first.
func byDir(paths []string) []Dir {
	var dirs []Dir
	at := map[string]int{}
	for _, p := range paths {
		name := path.Dir(p)
		if !strings.HasSuffix(name, "/") {
			name += "/"
		}
		i, ok := at[name]
		if !ok {
			i = len(dirs)
			at[name] = i
			dirs = append(dirs, Dir{Name: name})
		}
		dirs[i].Files = append(dirs[i].Files, p)
	}
	slices.SortStableFunc(dirs, func(a, b Dir) int { return cmp.Compare(len(b.Files), len(a.Files)) })
	return dirs
}

// rank is a kind's place in the precedence a step's glyph and evidence are
// picked by: a write over a command over a read. A write is what the reader
// came for; a command, or a call to a server nobody vouched for, may have
// written and nobody can say, so it is next; reading of every sort is the
// bottom, as it is on the rail
// (docs/interface/principles.md#weight-tracks-risk). The kinds the
// precedence does not name — a sub-agent, a report — sit between a command
// and a read: they did more than read, and less that the workspace shows.
func rank(k Kind) int {
	switch k {
	case KindWrite:
		return 3
	case KindRun, KindRemote:
		return 2
	case KindRead, KindSearch, KindLookup:
		return 0
	}
	return 1
}

// lead picks the step's glyph and its evidence line together, by one
// precedence: a failure over a write over a command over a search over a
// read. They are
// picked in one place because they are one claim — a footer showing a
// write under a header that says the step failed is a card arguing with
// itself.
//
// A failure is the last call that broke, since a failure the step went on
// past and answered is not what it stands on. A write is the first edit
// with a change to show, as the open card lists them. A command is the last
// that answered, its output being what the step stands on. A search is the
// last query asked, since the query is the search's own text. A read shows
// its path only where the step read one file: past one, the header's
// rollup has already said where, and there is nothing one line can add.
func lead(acts []Act, s Step) (Mark, Evidence) {
	failed, top := -1, -1
	for i, a := range acts {
		switch a.state() {
		case StateRefused:
			continue
		case StateFailed:
			failed = i
		}
		if top < 0 || rank(a.Receipt.Kind) > rank(acts[top].Receipt.Kind) {
			top = i
		}
	}
	switch {
	case failed >= 0:
		return Mark{Kind: acts[failed].Receipt.Kind, State: StateFailed},
			footer(failed, errorLine(acts[failed].Receipt), "")
	case top < 0 && len(acts) > 0:
		// Every call was refused: the step did nothing, and says so with
		// the first thing it was refused.
		return Mark{Kind: acts[0].Receipt.Kind, State: StateRefused}, Evidence{}
	case top < 0:
		return Mark{}, Evidence{}
	}
	m := Mark{Kind: acts[top].Receipt.Kind}
	switch rank(m.Kind) {
	case 3:
		for i, a := range acts {
			if h := a.Receipt.Hunk; h != nil && a.state() == StateDone && a.Receipt.Kind == KindWrite {
				return m, footer(i, h.Marked(), h.Path)
			}
		}
	case 2:
		for i := len(acts) - 1; i >= 0; i-- {
			a := acts[i]
			if a.state() != StateDone || rank(a.Receipt.Kind) != 2 {
				continue
			}
			if line := firstOutputLine(a.Receipt); line != "" {
				return m, footer(i, line, "")
			}
		}
	case 0:
		// A search's query stands above a read's path: what was looked for
		// says more about where the step went than one of the files it
		// opened, and its count of what was found is the footer's figure.
		for i := len(acts) - 1; i >= 0; i-- {
			a := acts[i]
			if a.state() == StateDone && a.Receipt.Kind == KindSearch && a.Receipt.Subject != "" {
				return m, footer(i, a.Receipt.Subject, "")
			}
		}
		if len(s.Reads) == 1 && len(s.Reads[0].Files) == 1 {
			p := s.Reads[0].Files[0]
			for i, a := range acts {
				if a.state() == StateDone && a.Receipt.Kind == KindRead && a.Receipt.Path() == p {
					return m, footer(i, p, "")
				}
			}
		}
	}
	return m, Evidence{}
}

// footer is a line cut for a step's footer, or nothing where the call had no
// line to give.
func footer(call int, line, file string) Evidence {
	line = cut(line)
	if line == "" {
		return Evidence{}
	}
	return Evidence{Line: line, Path: file, Call: call}
}

// cut is one line of a tool's text, trimmed and bounded, cut on a rune.
func cut(line string) string {
	line = strings.TrimSpace(line)
	if len(line) <= evidenceMaxBytes {
		return line
	}
	n := evidenceMaxBytes
	for n > 0 && !utf8.RuneStart(line[n]) {
		n--
	}
	return line[:n]
}

// errorLine is the line a call that broke said it with. A tool's is its
// `error:` line, which leads its answer. A command's is the last line of its
// output that reads as a failure, and only where none does its last line:
// the tail is where a program says how it went, but not always what went
// wrong — `make` ends a failed `go test` on `Leaving directory`, which names
// a directory and nothing that failed, so make's directory trailers are
// never the line.
// See docs/interface/surfaces.md#the-step.
func errorLine(r Receipt) string {
	body, ok := output(r)
	if !ok {
		return firstLine(r.result)
	}
	lines := strings.Split(body, "\n")
	last := ""
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" || tools.TruncationNotice(l) || makeTrailer(l) {
			continue
		}
		if readsAsFailure(l) {
			return l
		}
		if last == "" {
			last = l
		}
	}
	return last
}

// readsAsFailure reports whether a line of a command's output is one a
// program says it failed with: a test's `--- FAIL` and `FAIL`, an
// `error:`, a panic, git's `fatal:`, an `Error` that opens a line or labels
// one, and a non-zero `exit status`. Make's own lines are left out: `make:
// *** [test] Error 1` says the recipe failed, which the card's glyph already
// says, where the line above it says what did.
func readsAsFailure(l string) bool {
	if strings.HasPrefix(l, "make:") || strings.HasPrefix(l, "make[") {
		return false
	}
	switch {
	case strings.HasPrefix(l, "--- FAIL"), strings.HasPrefix(l, "FAIL"),
		strings.HasPrefix(l, "panic:"), strings.HasPrefix(l, "fatal:"),
		strings.HasPrefix(l, "Error"), strings.Contains(l, "error:"),
		strings.Contains(l, "Error:"):
		return true
	}
	_, code, ok := strings.Cut(l, "exit status ")
	return ok && code != "" && code != "0"
}

// makeTrailer reports whether a line is make's note that it entered or left
// a directory, which it prints around every recursive run.
func makeTrailer(l string) bool {
	return strings.HasPrefix(l, "make") &&
		(strings.Contains(l, ": Entering directory") || strings.Contains(l, ": Leaving directory"))
}

// firstOutputLine is the first line a command, or a call to a server,
// answered with.
func firstOutputLine(r Receipt) string {
	body, ok := output(r)
	if !ok {
		body = r.result
	}
	return firstLine(body)
}

// output is what a command printed: the result itself for a command the
// runner ran, and the body under the status line for one a tool ran, whose
// result opens with the formatter's status and `output:` header. ok is
// false for an answer that is not a program's output.
func output(r Receipt) (string, bool) {
	if r.command {
		return r.result, true
	}
	status, body, ok := strings.Cut(r.result, "\n")
	if !ok || (!strings.HasPrefix(status, "exit code: ") && !strings.HasPrefix(status, "error: ")) {
		return "", false
	}
	body, ok = strings.CutPrefix(body, "output:\n")
	return body, ok
}

// firstLine is the first line of text that says anything, leaving out the
// bounds' own notices.
func firstLine(s string) string {
	for l := range strings.SplitSeq(s, "\n") {
		if l = strings.TrimSpace(l); l != "" && !tools.TruncationNotice(l) {
			return l
		}
	}
	return ""
}

// Counts is the step's receipt as its header reads it: the calls by kind in
// the order each was first used — `read 2 files · searched once · 1 lookup`,
// `read 14 files in internal/ui/, 3 in docs/ · ran 3 commands · wrote 2
// files`. A write's `+N −M` is not in it: it is two numbers a renderer
// paints in the diff's tokens, which is Added and Removed.
func (s Step) Counts() string {
	parts := make([]string, 0, len(s.Tallies))
	for _, t := range s.Tallies {
		parts = append(parts, s.phrase(t))
	}
	return strings.Join(parts, " · ")
}

// phrase is one kind's clause in the step's receipt, in the receipt's verbs:
// read, searched, looked up, ran, wrote. A kind with no word of its own is
// counted under its kind's name, which is the signal the list has fallen
// behind (docs/interface/principles.md#closed-vocabularies). A kind with one
// call names what it was about instead of counting it — `ran git status`,
// `read a.go` — and the count starts at two.
func (s Step) phrase(t Tally) string {
	if t.Calls == 1 && t.Subject != "" {
		return stepVerb(t.Kind) + " " + t.Subject
	}
	switch t.Kind {
	case KindRead:
		if t.Files == 0 {
			return "read " + times(t.Calls)
		}
		return "read " + s.readFiles(t.Files)
	case KindSearch:
		return "searched " + times(t.Calls)
	case KindLookup:
		return counted(t.Calls, "lookup", "lookups")
	case KindRun:
		return "ran " + counted(t.Calls, "command", "commands")
	case KindWrite:
		if t.Files == 0 {
			return "wrote " + times(t.Calls)
		}
		return "wrote " + counted(t.Files, "file", "files")
	}
	return counted(t.Calls, t.Kind.String(), t.Kind.String()+"s")
}

// Header is the step's receipt split the way a card's header draws it: the
// verb that leads it, and what follows in the order a narrow pane gives it
// up. Rollup is the rest of the receipt — the lead kind's count and every
// other kind's clause — and Bare is the same without the reads' directory
// clause, which goes before the rollup does. Subject is the lead kind's one
// call where it has one, which a pane cuts rather than drops: a card that
// said `ran` and nothing else would not say what ran.
type Header struct {
	Verb, Subject, Rollup, Bare string
}

// Header splits the receipt for a card's header. A step with no call that
// ran has no verb, and an empty header.
func (s Step) Header() Header {
	if len(s.Tallies) == 0 {
		return Header{}
	}
	lead := s.Tallies[0]
	h := Header{Verb: stepVerb(lead.Kind)}
	var rest, bare []string
	if lead.Calls == 1 && lead.Subject != "" {
		h.Subject = lead.Subject
	} else {
		head, ok := strings.CutPrefix(s.phrase(lead), h.Verb+" ")
		if !ok {
			// A clause that does not open with its verb — `2 lookups` —
			// leads with the verb and counts the calls behind it.
			head = times(lead.Calls)
		}
		rest, bare = append(rest, head), append(bare, s.bareClause(lead, head))
	}
	for _, t := range s.Tallies[1:] {
		p := s.phrase(t)
		rest, bare = append(rest, p), append(bare, s.bareClause(t, p))
	}
	h.Rollup, h.Bare = strings.Join(rest, " · "), strings.Join(bare, " · ")
	return h
}

// bareClause is a clause as the narrower rollup has it: the reads counted
// without their directories, and a kind's one call counted rather than
// named. A subject behind the lead is what makes a rollup too long to keep
// — a whole command line, a path — and the count is the clause's fact in the
// fewest columns, so the narrow form keeps the count rather than giving the
// rollup up for one subject.
// See docs/interface/departures.md#the-narrow-rollup-counts-what-the-wide-one-names.
func (s Step) bareClause(t Tally, clause string) string {
	if t.Calls == 1 && t.Subject != "" {
		bare := t
		bare.Subject = ""
		return s.phrase(bare)
	}
	if t.Kind != KindRead || t.Files == 0 {
		return clause
	}
	files := counted(t.Files, "file", "files")
	if strings.HasPrefix(clause, "read ") {
		return "read " + files
	}
	return files
}

// stepVerb is the word a kind leads its clause with, in the receipt's past
// tense. A kind with no word of its own is its kind's name.
func stepVerb(k Kind) string {
	switch k {
	case KindRead:
		return "read"
	case KindSearch:
		return "searched"
	case KindLookup:
		return "looked up"
	case KindRun:
		return "ran"
	case KindWrite:
		return "wrote"
	}
	return k.String()
}

// readFiles is the read clause's count, rolled up by directory where the
// rollup groups something: `14 files in internal/ui/, 3 in docs/`. Where no
// directory holds two of the files, the rollup is the file list over again,
// and the count stands alone.
func (s Step) readFiles(n int) string {
	if len(s.Reads) == 0 || len(s.Reads[0].Files) < 2 {
		return counted(n, "file", "files")
	}
	parts := []string{counted(len(s.Reads[0].Files), "file", "files") + " in " + s.Reads[0].Name}
	for i, d := range s.Reads[1:] {
		if i+1 == ReadDirCeiling {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, fmt.Sprintf("%d in %s", len(d.Files), d.Name))
	}
	return strings.Join(parts, ", ")
}

func counted(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}

func times(n int) string {
	switch n {
	case 1:
		return "once"
	case 2:
		return "twice"
	}
	return fmt.Sprintf("%d times", n)
}
