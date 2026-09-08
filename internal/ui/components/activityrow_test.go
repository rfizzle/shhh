package components

import (
	"strings"
	"testing"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// fieldsOf splits a rendered row into its grid fields
// (docs/interface/principles.md#one-grid): the 13-column lead — pointer 2,
// rail 1, glyph 2, verb 8 — then the target, and the right edge holding
// outcome and the 6-column duration.
func fieldsOf(t *testing.T, view string) (lead, rail, verb, rest string) {
	t.Helper()
	line := []rune(strings.Split(stripANSI(view), "\n")[0])
	// Trailing blanks are trimmed off the rendered line, so a row with
	// nothing past its lead can be shorter than the grid.
	for len(line) < leadWidth {
		line = append(line, ' ')
	}
	lead = string(line[:leadWidth])
	rail = string(line[ptrWidth : ptrWidth+railWidth])
	verb = string(line[ptrWidth+railWidth+glyphWidth : leadWidth])
	rest = string(line[leadWidth:])
	return
}

func TestActivityRow_GridAlignment(t *testing.T) {
	rows := []ActivityRow{
		{Kind: ActivityTool, Verb: "read", Target: "internal/agent/loop.go", Counts: "218 lines", Duration: "0.6s"},
		{Kind: ActivityTool, Verb: "search", Target: "ErrRoundLimit ./internal", Counts: "6 matches"},
		{Kind: ActivityEdit, Verb: "edit", Target: "internal/agent/loop.go", Counts: "+12 −4 · 2 hunks", Duration: "1.1s"},
		{Kind: ActivityCommand, Verb: "run", Target: "go build ./cmd/shhh", Outcome: OutcomeExit(0), Duration: "4.8s"},
	}
	for _, width := range []int{60, 80, 120} {
		var targets, rights []int
		for _, r := range rows {
			line := stripANSI(r.View(width))
			if w := len([]rune(line)); w > width {
				t.Fatalf("width %d: row overflows to %d cells: %q", width, w, line)
			}
			lead, _, verb, _ := fieldsOf(t, r.View(width))
			if len([]rune(lead)) != leadWidth {
				t.Fatalf("width %d: lead should be %d columns, got %q", width, leadWidth, lead)
			}
			if strings.TrimRight(verb, " ") != r.Verb {
				t.Fatalf("width %d: verb field should hold %q, got %q", width, r.Verb, verb)
			}
			targets = append(targets, runeIndex(line, r.Target))
			// The right edge: outcome then the 6-column duration field.
			rights = append(rights, len([]rune(strings.TrimRight(line, " "))))
		}
		for i, col := range targets {
			if col != leadWidth {
				t.Fatalf("width %d: row %d target starts at %d, want %d", width, i, col, leadWidth)
			}
		}
		// Rows carrying a duration end at the right edge; the duration field is
		// reserved on the others, so their outcomes stop 6 columns short.
		if got := rights[0]; got != width {
			t.Fatalf("width %d: a row with a duration should reach the right edge, got %d", width, got)
		}
		if got := rights[1]; got != width-durWidth {
			t.Fatalf("width %d: a row without a duration should stop %d short, got %d", width, durWidth, width-got)
		}
	}
}

func TestActivityRow_TargetClipsOutcomeDoesNot(t *testing.T) {
	r := ActivityRow{Kind: ActivityTool, Verb: "read",
		Target: "internal/ui/chat/" + strings.Repeat("very-long-", 8) + "model.go",
		Counts: "412 lines", Duration: "0.7s"}
	line := stripANSI(r.View(60))
	if w := len([]rune(line)); w != 60 {
		t.Fatalf("clipped row should fill exactly 60 cells, got %d: %q", w, line)
	}
	if !strings.Contains(line, "…") {
		t.Fatalf("an overlong target clips with an ellipsis:\n%s", line)
	}
	for _, want := range []string{"412 lines", "0.7s"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the outcome never clips, want %q:\n%s", want, line)
		}
	}
}

func TestActivityRow_MutationRailPerKind(t *testing.T) {
	cases := []struct {
		name string
		row  ActivityRow
		rail bool
	}{
		{"read-only", ActivityRow{Kind: ActivityTool, Verb: "read"}, false},
		{"sub-agent", ActivityRow{Kind: ActivitySubagent, Verb: "agent"}, false},
		{"command", ActivityRow{Kind: ActivityCommand, Verb: "run"}, true},
		{"edit", ActivityRow{Kind: ActivityEdit, Verb: "edit"}, true},
		{"server call", ActivityRow{Kind: ActivityRemote, Verb: "mcp"}, true},
		{"denied read", ActivityRow{Kind: ActivityTool, Verb: "read", State: ActivityDenied}, true},
		{"failed read", ActivityRow{Kind: ActivityTool, Verb: "read", State: ActivityFailed}, true},
		{"queued read", ActivityRow{Kind: ActivityTool, Verb: "read", State: ActivityQueued}, false},
	}
	for _, tc := range cases {
		if got := tc.row.mutated(); got != tc.rail {
			t.Fatalf("%s: rail predicate = %v, want %v", tc.name, got, tc.rail)
		}
		_, rail, _, _ := fieldsOf(t, tc.row.View(60))
		if want := " "; !tc.rail && rail != want {
			t.Fatalf("%s: read-only rows leave the gutter blank, got %q", tc.name, rail)
		}
		if tc.rail && rail != "▎" {
			t.Fatalf("%s: row should carry the mutation rail, got %q", tc.name, rail)
		}
	}
}

func TestActivityRow_StateGlyphs(t *testing.T) {
	cases := []struct {
		state ActivityState
		kind  ActivityKind
		glyph string
	}{
		{ActivityDone, ActivityTool, "⚙"},
		{ActivityDone, ActivityCommand, "$"},
		{ActivityDone, ActivityEdit, "✎"},
		{ActivityDone, ActivitySubagent, "◇"},
		{ActivityQueued, ActivityTool, "·"},
		{ActivityRunning, ActivityCommand, "▸"},
		{ActivityChecking, ActivityCommand, "✦"},
		{ActivityFailed, ActivityCommand, "✗"},
		{ActivityDenied, ActivityEdit, "⊘"},
	}
	for _, tc := range cases {
		r := ActivityRow{Kind: tc.kind, State: tc.state, Verb: "run"}
		lead, _, _, _ := fieldsOf(t, r.View(60))
		if !strings.Contains(lead, tc.glyph) {
			t.Fatalf("state %d kind %d should render %q, got lead %q", tc.state, tc.kind, tc.glyph, lead)
		}
	}
}

func TestActivityRow_DeniedNamesTheDecider(t *testing.T) {
	you := ActivityRow{Kind: ActivityEdit, Verb: "edit", Target: "go.mod", State: ActivityDenied,
		Outcome: OutcomeBy(OutcomeDenied, "you"), Duration: NoDuration}
	line := stripANSI(you.View(70))
	for _, want := range []string{"⊘", "denied · you", "—"} {
		if !strings.Contains(line, want) {
			t.Fatalf("your refusal should read %q:\n%s", want, line)
		}
	}

	// A rule's no is a different word, so the two denials are told apart by
	// what they say and not only by what they are painted in
	// (docs/interface/principles.md#two-denials-are-not-one-denial). The
	// rule that said it is the account beside the word.
	rule := ActivityRow{Kind: ActivityCommand, Verb: "run", Target: "rm -rf ./dist", State: ActivityDenied,
		ByRule: true, Outcome: OutcomeBlocked, Allowed: "plan mode",
		Keys: "/mode why", Duration: NoDuration}
	line = stripANSI(rule.View(80))
	for _, want := range []string{"⊘", "blocked · plan mode", "/mode why"} {
		if !strings.Contains(line, want) {
			t.Fatalf("a rule's refusal should read %q:\n%s", want, line)
		}
	}
	if strings.Contains(line, OutcomeDenied) {
		t.Fatalf("a rule's no is not your no:\n%s", line)
	}
	if strings.Contains(line, "✗") {
		t.Fatalf("a refusal is never a failure:\n%s", line)
	}
}

func TestActivityRow_BlankDurationKeepsTheColumn(t *testing.T) {
	with := ActivityRow{Kind: ActivityTool, Verb: "read", Target: "a.go", Counts: "3 lines", Duration: "0.6s"}
	without := ActivityRow{Kind: ActivityTool, Verb: "read", Target: "a.go", Counts: "3 lines"}
	w, wo := stripANSI(with.View(60)), stripANSI(without.View(60))
	if strings.Index(w, "3 lines") != strings.Index(wo, "3 lines") {
		t.Fatalf("the duration field is reserved even when blank:\n%q\n%q", w, wo)
	}
	if strings.HasSuffix(wo, " ") {
		t.Fatalf("a blank duration leaves no trailing spaces: %q", wo)
	}
}

func TestActivityRow_ExpandedAndFailed(t *testing.T) {
	r := ActivityRow{Kind: ActivityCommand, Verb: "run", Target: "go vet ./...",
		State: ActivityFailed, Outcome: OutcomeExit(1), Detail: []string{"vet: unreachable code"}}
	view := r.View(80)
	if !strings.Contains(view, "✗") || !strings.Contains(view, "vet: unreachable code") {
		t.Fatalf("failed rows auto-expand with the error glyph:\n%s", view)
	}

	r = ActivityRow{Kind: ActivityEdit, Verb: "edit", Target: "loop.go", Counts: "+12 −4",
		Expanded: true, Detail: []string{"hunk 1", "hunk 2"}, MaxDetail: 1}
	view = r.View(80)
	if !strings.Contains(view, "hunk 1") || strings.Contains(view, "hunk 2") {
		t.Fatalf("expanded detail should respect MaxDetail:\n%s", view)
	}
}

func TestActivityRow_CollapsedHidesDetail(t *testing.T) {
	r := ActivityRow{Kind: ActivityTool, Verb: "search", Target: "advanceExecQueue",
		Counts: "3 matches", Duration: "0.6s", Detail: []string{"model.go:152"}}
	view := r.View(80)
	if strings.Contains(view, "model.go:152") {
		t.Fatalf("collapsed row must not show detail:\n%s", view)
	}
	for _, want := range []string{"⚙", "search", "advanceExecQueue", "3 matches", "0.6s"} {
		if !strings.Contains(view, want) {
			t.Fatalf("row should contain %q:\n%s", want, view)
		}
	}
}

func TestActivityRow_RunningTail(t *testing.T) {
	r := ActivityRow{Kind: ActivityCommand, Verb: "run", Target: "go test ./...",
		State: ActivityRunning, Outcome: OutcomeRunning, Tail: "ok  internal/agent  0.31s"}
	view := r.View(80)
	if !strings.Contains(view, "▸") || !strings.Contains(view, "ok  internal/agent") {
		t.Fatalf("running rows show the live tail:\n%s", view)
	}
}

// runeIndex is strings.Index in character cells rather than bytes.
func runeIndex(haystack, needle string) int {
	i := strings.Index(haystack, needle)
	if i < 0 {
		return -1
	}
	return len([]rune(haystack[:i]))
}

func TestActivityGroup_FoldsOnTheSameGrid(t *testing.T) {
	row := ActivityRow{Kind: ActivityTool, Verb: "read", Target: "internal/agent/loop.go",
		Counts: "218 lines", Duration: "0.6s"}
	group := ActivityGroup{Label: "6 reads · 2 searches", Duration: "3.9s"}

	for _, width := range []int{60, 80, 120} {
		line := stripANSI(group.View(width))
		if w := len([]rune(line)); w > width {
			t.Fatalf("width %d: group row overflows to %d cells: %q", width, w, line)
		}
		// The fold state takes the glyph column and ⚙ the verb column, so the
		// group's label starts where a row's target does.
		_, rail, _, rest := fieldsOf(t, group.View(width))
		if strings.TrimSpace(rail) != "" {
			t.Fatalf("a fold changed nothing, so it carries no mutation rail: %q", line)
		}
		if !strings.HasPrefix(rest, "6 reads") {
			t.Fatalf("width %d: the label should start in the target column: %q", width, rest)
		}
		// It states what it swallowed and what that cost, and offers the key
		// that brings the rows back (invariant 4).
		for _, want := range []string{"▸", "⚙", GroupExpandKey, "3.9s"} {
			if !strings.Contains(line, want) {
				t.Fatalf("width %d: group row should contain %q: %q", width, want, line)
			}
		}
		// Both lines end on the same right edge, so a fold does not break the
		// duration column the feed is scanned down.
		if got, want := len([]rune(line)), len([]rune(stripANSI(row.View(width)))); got != want {
			t.Fatalf("width %d: group row ends at %d, rows at %d", width, got, want)
		}
	}
}

// The account of who allowed a call is the one field in the outcome group
// the row will give up. It stands where the row has spare columns, and it
// goes rather than take the target below what still names the act — which is
// why nothing else in the group is ever dropped: what the act did and what
// it counted have nowhere else to be said.
func TestActivityRow_TheAccountGivesWayToTheTarget(t *testing.T) {
	r := ActivityRow{Kind: ActivityEdit, Verb: "edit", Target: "internal/ui/chat/approval.go",
		Allowed: OutcomeBy(OutcomeAutoAllowed, "auto mode"), Counts: "+12 −4 · 2 hunks", Duration: "1.1s"}

	wide := stripANSI(r.View(120))
	if !strings.Contains(wide, "auto-allowed · auto mode") || !strings.Contains(wide, "internal/ui/chat/approval.go") {
		t.Fatalf("a row with room states both:\n%s", wide)
	}

	narrow := stripANSI(r.View(60))
	if strings.Contains(narrow, "auto-allowed") {
		t.Fatalf("a row without room drops the account:\n%s", narrow)
	}
	for _, want := range []string{"+12 −4 · 2 hunks", "internal/ui"} {
		if !strings.Contains(narrow, want) {
			t.Fatalf("and keeps %q:\n%s", want, narrow)
		}
	}
	if got := len([]rune(narrow)); got > 60 {
		t.Fatalf("the row still fits its width, got %d cells: %q", got, narrow)
	}
}

// The outcome column is what the reader drops down to tell finished from
// running from failed, so the word takes the state's own token — and the word
// is always there as well, which is what makes the colour reinforcement
// rather than the message (invariant 1).
func TestActivityRow_OutcomeTakesTheStatesToken(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	cases := []struct {
		name string
		row  ActivityRow
		want lipgloss.Style
	}{
		{"a command that finished", ActivityRow{Kind: ActivityCommand, Verb: "run",
			Outcome: OutcomeExit(0)}, sty.Add},
		{"an edit you approved", ActivityRow{Kind: ActivityEdit, Verb: "edit",
			Outcome: OutcomeBy(OutcomeApproved, "you")}, sty.Add},
		{"a command in flight", ActivityRow{Kind: ActivityCommand, Verb: "run",
			State: ActivityRunning, Outcome: OutcomeRunning}, sty.SpinText},
		{"a call the classifier is judging", ActivityRow{Kind: ActivityCommand, Verb: "run",
			State: ActivityChecking, Outcome: OutcomeChecking}, sty.SpinText},
		{"a command that broke", ActivityRow{Kind: ActivityCommand, Verb: "run",
			State: ActivityFailed, Outcome: OutcomeExit(1)}, sty.Del},
		{"a rule's no", ActivityRow{Kind: ActivityCommand, Verb: "run", State: ActivityDenied,
			ByRule: true, Outcome: OutcomeBlocked, Allowed: "plan mode"}, sty.Del},
		{"your own no", ActivityRow{Kind: ActivityCommand, Verb: "run", State: ActivityDenied,
			Outcome: OutcomeBy(OutcomeDenied, "you")}, sty.Dim},
		{"a call that has not started", ActivityRow{Kind: ActivityTool, Verb: "read",
			State: ActivityQueued, Outcome: OutcomeQueued}, sty.Dim},
	}
	for _, tc := range cases {
		if want, got := tc.want.Render(tc.row.Outcome), tc.row.outcomeField(); !strings.Contains(got, want) {
			t.Fatalf("%s: outcome field %q should paint the word as %q", tc.name, got, want)
		}
	}

	// A read's count is the other half of the field and keeps its own tone:
	// what a call found is content, and content is dimmer.
	counted := ActivityRow{Kind: ActivityTool, Verb: "read", Counts: "218 lines"}
	if want, got := sty.Dimmer.Render("218 lines"), counted.outcomeField(); !strings.Contains(got, want) {
		t.Fatalf("a count stays dimmer, got %q", got)
	}
}

// `+N` and `−M` mean the same two things wherever a row states them, so they
// carry the diff's own tokens on the row as they do in the diff itself.
func TestActivityRow_LineCountsCarryTheDiffsTokens(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	r := ActivityRow{Kind: ActivityEdit, Verb: "edit", Target: "internal/agent/loop.go",
		Counts: "+12 −4 · 2 hunks", Duration: "1.1s"}
	view := r.View(110)
	for _, want := range []string{
		sty.Add.Render("+12"),
		sty.Del.Render("−4"),
		sty.Dimmer.Render(" · ") + sty.Dimmer.Render("2 hunks"),
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("an edit's counts should carry %q:\n%q", want, view)
		}
	}
	// Only a line count takes them: a search's own numbers are not additions.
	found := ActivityRow{Kind: ActivityTool, Verb: "search", Counts: "6 matches · 4 files"}
	if want, got := sty.Dimmer.Render("6 matches"), found.outcomeField(); !strings.Contains(got, want) {
		t.Fatalf("a count that is not a line count stays dimmer, got %q", got)
	}
}

// The row's subject is the verb and the target together, in one tone: body
// text at rest, bright while the call is happening, dim before it started and
// after a refusal that means it never will.
func TestActivityRow_SubjectTone(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	cases := []struct {
		name string
		row  ActivityRow
		want lipgloss.Style
	}{
		{"at rest", ActivityRow{Kind: ActivityTool, Verb: "read", Target: "loop.go"}, sty.Body},
		{"in flight", ActivityRow{Kind: ActivityCommand, Verb: "run", Target: "go test ./...",
			State: ActivityRunning, Outcome: OutcomeRunning}, sty.Bright},
		{"queued", ActivityRow{Kind: ActivityTool, Verb: "read", Target: "loop.go",
			State: ActivityQueued, Outcome: OutcomeQueued}, sty.Dim},
		{"refused by you", ActivityRow{Kind: ActivityEdit, Verb: "edit", Target: "go.mod",
			State: ActivityDenied, Outcome: OutcomeBy(OutcomeDenied, "you")}, sty.Dim},
		// A rule's no keeps body text: the reader is being told about an act
		// somebody else stopped, not about a preference of their own.
		{"blocked by a rule", ActivityRow{Kind: ActivityCommand, Verb: "run", Target: "rm -rf ./dist",
			State: ActivityDenied, ByRule: true, Outcome: OutcomeBlocked}, sty.Body},
		{"broken", ActivityRow{Kind: ActivityCommand, Verb: "run", Target: "go vet ./...",
			State: ActivityFailed, Outcome: OutcomeExit(1)}, sty.Body},
	}
	for _, tc := range cases {
		view := tc.row.View(110)
		for _, want := range []string{tc.want.Render(tc.row.Verb), tc.want.Render(tc.row.Target)} {
			if !strings.Contains(view, want) {
				t.Fatalf("%s: subject should carry %q:\n%q", tc.name, want, view)
			}
		}
	}
}

// A search's target is its pattern and then where it was put, and only the
// pattern is the subject: the place goes dim behind it.
func TestActivityRow_ScopeStaysBehindTheSubject(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	r := ActivityRow{Kind: ActivityTool, Verb: "search", Target: "ErrRoundLimit ./internal",
		Scope: "./internal", Counts: "6 matches"}
	view := r.View(110)
	if want := sty.Body.Render("ErrRoundLimit") + sty.Dim.Render(" ./internal"); !strings.Contains(view, want) {
		t.Fatalf("the pattern leads in body text and the place is dim behind it:\n%q", view)
	}
	// Clipped past the scope, what is left is all subject.
	narrow := ActivityRow{Kind: ActivityTool, Verb: "search", Target: "ErrRoundLimit ./internal/ui/chat",
		Scope: "./internal/ui/chat", Counts: "6 matches"}.View(40)
	if strings.Contains(stripANSI(narrow), "./internal/ui/chat") {
		t.Fatalf("a 40-column row has no room for the scope: %q", stripANSI(narrow))
	}
	if want := sty.Body.Render("ErrRoundL…"); !strings.Contains(narrow, want) {
		t.Fatalf("and what is left of the field is all subject: %q", narrow)
	}
}

// Duration is one column down the transcript, so it is one grey: the step
// outline's headers have always drawn it dim, and the rows under them now
// agree.
func TestActivityRow_DurationIsDim(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	r := ActivityRow{Kind: ActivityTool, Verb: "read", Target: "loop.go", Counts: "218 lines", Duration: "0.6s"}
	if want := sty.Dim.Render("0.6s"); !strings.Contains(r.View(110), want) {
		t.Fatalf("the duration field is dim, want %q:\n%q", want, r.View(110))
	}
}

// Nothing on a row is left in the terminal's own foreground: every cell that
// is not padding sits inside a colour the palette issued
// (docs/interface/principles.md#a-colour-is-three-values-and-a-ground).
func TestActivityRow_EveryCellCarriesAToken(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	rows := map[string]ActivityRow{
		"read":    {Kind: ActivityTool, Verb: "read", Target: "internal/agent/loop.go", Counts: "218 lines", Duration: "0.6s"},
		"search":  {Kind: ActivityTool, Verb: "search", Target: "ErrRoundLimit ./internal", Scope: "./internal", Counts: "6 matches"},
		"command": {Kind: ActivityCommand, Verb: "run", Target: "go test ./internal/agent/...", Outcome: OutcomeExit(0), Duration: "12.4s"},
		"edit": {Kind: ActivityEdit, Verb: "edit", Target: "internal/agent/loop.go",
			Outcome: OutcomeBy(OutcomeApproved, "you"), Counts: "+12 −4 · 2 hunks", Duration: "1.1s"},
		"agent":    {Kind: ActivitySubagent, Verb: "agent", Target: "writer-1", Outcome: OutcomeOK, Duration: "48.0s"},
		"think":    {Kind: ActivityThink, Verb: "think", Counts: "42 lines", Keys: GroupExpandKey},
		"queued":   {Kind: ActivityTool, Verb: "read", Target: "internal/agent/round.go", State: ActivityQueued, Outcome: OutcomeQueued, Duration: NoDuration},
		"running":  {Kind: ActivityCommand, Verb: "run", Target: "go build ./cmd/shhh", State: ActivityRunning, Outcome: OutcomeRunning, Tail: "internal/ui/chat/model.go:1660:1: too many arguments"},
		"checking": {Kind: ActivityCommand, Verb: "run", Target: "gofmt -w loop.go", State: ActivityChecking, Outcome: OutcomeChecking, Duration: "0.4s"},
		"failed": {Kind: ActivityCommand, Verb: "run", Target: "go test ./internal/agent/...", State: ActivityFailed,
			Outcome: OutcomeExit(1), Duration: "21.4s", Detail: []string{"--- FAIL: TestRoundLimit (0.03s)"}},
		"denied": {Kind: ActivityEdit, Verb: "edit", Target: "go.mod", State: ActivityDenied,
			Outcome: OutcomeBy(OutcomeDenied, "you"), Duration: NoDuration},
		"blocked": {Kind: ActivityCommand, Verb: "run", Target: "rm -rf ./dist", State: ActivityDenied, ByRule: true,
			Outcome: OutcomeBlocked, Allowed: "classifier 2.1s", Keys: "/permissions why", Duration: NoDuration},
		"allowed": {Kind: ActivityTool, Verb: "read", Target: "internal/ui/chat/model.go",
			Allowed: OutcomeBy(OutcomeAutoAllowed, "read-only"), Counts: "412 lines", Duration: "0.2s"},
		"selected": {Kind: ActivityTool, Verb: "read", Target: "internal/agent/loop.go", Selected: true,
			Counts: "218 lines", Duration: "0.6s"},
	}
	for _, width := range goldenWidths {
		for name, r := range rows {
			if bare := unpaintedRuns(r.View(width)); len(bare) > 0 {
				t.Fatalf("width %d: the %s row leaves %q outside every token:\n%q",
					width, name, bare, r.View(width))
			}
		}
	}
}

// unpaintedRuns is every run of visible text in a render that no SGR sequence
// is covering. Padding is not text: the grid is made of spaces, and a space
// carries nothing to paint.
func unpaintedRuns(view string) []string {
	var runs []string
	for _, line := range strings.Split(view, "\n") {
		painted := false
		var run strings.Builder
		rs := []rune(line)
		for i := 0; i < len(rs); {
			if rs[i] == '\x1b' {
				j := i + 1
				for j < len(rs) && !unicode.IsLetter(rs[j]) {
					j++
				}
				if j < len(rs) && rs[j] == 'm' {
					params := strings.TrimPrefix(string(rs[i+1:j]), "[")
					painted = params != "" && params != "0"
				}
				i = j + 1
				continue
			}
			switch {
			case painted || rs[i] == ' ':
				if run.Len() > 0 {
					runs = append(runs, run.String())
					run.Reset()
				}
			default:
				run.WriteRune(rs[i])
			}
			i++
		}
		if run.Len() > 0 {
			runs = append(runs, run.String())
		}
	}
	return runs
}

// A reading of the session is not an act, so its outcome column is not a
// verdict on an act: `⚠ off target` in the token that means done would be the
// bad news painted as good. The glyph in front of the word is what tells the
// readings apart, on a terminal with colour and on one without.
func TestActivityRow_AReadingsVerdictIsNotASuccess(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	for _, tone := range []SummaryTone{SummaryUnclear, SummaryOnTarget, SummarySufficient, SummaryOffTarget} {
		r := ActivityRow{Kind: ActivitySummary, Verb: "summary", Target: "round 5",
			Outcome: SummaryGlyph(tone) + " " + SummaryWord(tone), Counts: "1 line"}
		if want, got := sty.Dim.Render(r.Outcome), r.outcomeField(); !strings.Contains(got, want) {
			t.Fatalf("a reading's verdict should not take the done token, got %q", got)
		}
	}
	// The same for the row that read, wrote and ran nothing at all.
	think := ActivityRow{Kind: ActivityThink, Verb: "think", Outcome: OutcomeOK}
	if want, got := sty.Dim.Render(OutcomeOK), think.outcomeField(); !strings.Contains(got, want) {
		t.Fatalf("a thought has no outcome to call done, got %q", got)
	}
	// And an act that finished still says so in the token that means done.
	ran := ActivityRow{Kind: ActivityCommand, Verb: "run", Outcome: OutcomeExit(0)}
	if want, got := sty.Add.Render(OutcomeExit(0)), ran.outcomeField(); !strings.Contains(got, want) {
		t.Fatalf("a command that finished should take the done token, got %q", got)
	}
}

// The outcome gives way to nothing but the pane. A row whose lead, outcome
// and duration are already wider than the terminal has nothing left to spend
// on the target, so the outcome's own tail clips — the word that says what
// happened survives, and the line stays inside the pane rather than being
// broken by the terminal wherever it ran out
// (docs/interface/principles.md#one-grid).
func TestActivityRow_NoRowIsWiderThanItsPane(t *testing.T) {
	r := ActivityRow{
		Kind: ActivityReport, Verb: "report",
		Outcome:  "→ http://127.0.0.1:52104/r/rp-8f3a11c04b2d9e61",
		Duration: "0.8s",
	}
	for _, width := range []int{20, 40, 60, 80} {
		line := stripANSI(r.View(width))
		if w := lipgloss.Width(line); w > width {
			t.Fatalf("width %d: the row runs to %d cells: %q", width, w, line)
		}
	}
	narrow := stripANSI(r.View(60))
	if !strings.Contains(narrow, "→ http") || !strings.HasSuffix(strings.TrimSpace(narrow), "0.8s") {
		t.Fatalf("the head of the field and the duration both stand: %q", narrow)
	}
	if !strings.Contains(narrow, "…") {
		t.Fatalf("a field that gave something up says so: %q", narrow)
	}
	// A pane narrower than the grid's own fixed fields is still a render and
	// not a panic.
	for _, width := range []int{1, 6, 13, 19} {
		if line := stripANSI(r.View(width)); lipgloss.Width(line) > width {
			t.Fatalf("width %d: %q", width, line)
		}
	}
}

// The session's own lines sit on the grid a field short: the verb where every
// verb is, the subject in the growing field, the outcome right-aligned, and
// nothing in the glyph column, because the glyph says which kind of act a row
// was and this is not one.
func TestActivityNotice_IsARowOnTheGrid(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	n := ActivityNotice{Verb: "resumed", Subject: "master · 3 changed"}
	_, rail, verb, rest := fieldsOf(t, n.View(80))
	if strings.TrimSpace(rail) != "" {
		t.Fatalf("a notice changed nothing, so it carries no mutation rail: %q", n.View(80))
	}
	if strings.TrimSpace(verb) != "resumed" {
		t.Fatalf("the verb belongs in the verb column, got %q", verb)
	}
	if !strings.HasPrefix(rest, "master · 3 changed") {
		t.Fatalf("the subject should start in the target column, got %q", rest)
	}
	line := []rune(stripANSI(n.View(80)))
	if glyph := strings.TrimSpace(string(line[ptrWidth+railWidth : ptrWidth+railWidth+glyphWidth])); glyph != "" {
		t.Fatalf("the glyph column stays empty, got %q", glyph)
	}
	// Dim throughout: the session's bookkeeping is the quietest thing on the
	// grid (docs/interface/principles.md#weight-tracks-risk).
	full := ActivityNotice{Verb: "session", Subject: "2026-09-04 11:20:07", Outcome: "saved · shhh code --continue"}
	for _, want := range []string{sty.Dim.Render("session"), sty.Dim.Render("2026-09-04 11:20:07"),
		sty.Dim.Render("saved · shhh code --continue")} {
		if !strings.Contains(full.View(80), want) {
			t.Fatalf("every field of a notice is dim, want %q in:\n%q", want, full.View(80))
		}
	}
	for _, width := range []int{40, 60, 80, 110} {
		if w := lipgloss.Width(stripANSI(full.View(width))); w > width {
			t.Fatalf("width %d: the notice runs to %d cells", width, w)
		}
	}
	// Opened, the body it folds indents under it rather than re-gridding.
	opened := full
	opened.Detail, opened.Expanded = []string{"the reading the conversation was given"}, true
	lines := strings.Split(stripANSI(opened.View(80)), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[1], strings.Repeat(" ", GridDetailIndent)) {
		t.Fatalf("the body indents under the row, got %q", lines)
	}
}

// The place a search was put is dim only where the target is carrying it. A
// pattern that happens to end the way a scope does is still the subject
// whole: the row is told what its place is and does not go looking for one.
func TestActivityRow_APatternIsNotItsOwnScope(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	r := ActivityRow{Kind: ActivityTool, Verb: "search", Target: "func loop() .", Counts: "1 match"}
	if want := sty.Body.Render("func loop() ."); !strings.Contains(r.View(80), want) {
		t.Fatalf("a pattern with no place behind it is all subject:\n%q", r.View(80))
	}
}
