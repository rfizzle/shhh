package components

import (
	"strings"
	"testing"
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

	rule := ActivityRow{Kind: ActivityCommand, Verb: "run", Target: "rm -rf ./dist", State: ActivityDenied,
		ByRule: true, Outcome: OutcomeBy(OutcomeDenied, "auto") + " · plan mode",
		Keys: "/mode why", Duration: NoDuration}
	line = stripANSI(rule.View(80))
	for _, want := range []string{"⊘", "denied · auto · plan mode", "/mode why"} {
		if !strings.Contains(line, want) {
			t.Fatalf("a rule's refusal should read %q:\n%s", want, line)
		}
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
