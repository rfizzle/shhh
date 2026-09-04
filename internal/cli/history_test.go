package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// The host half of the history browser: what an entry means, how long
// ago it was, and what its action and its exit code add up to. The screen's
// own rules are tested where the screen is.

func ptrInt64(v int64) *int64 { return &v }

func ptrDuration(d time.Duration) *time.Duration { return &d }

// The exit code is the strongest thing an entry can say, so it outranks the
// action: a command that was run and failed says so however it was reached,
// and it says so in a glyph as well as in words (invariant 1).
func TestHistoryOutcome_ExitCodeOutranksTheAction(t *testing.T) {
	ran := storage.HistoryEntry{Action: "run", ExitCode: ptrInt64(0), Success: true}
	if state, outcome := historyOutcome(ran); state != components.ActivityDone || outcome != "exit 0" {
		t.Fatalf("a clean run reads %v %q", state, outcome)
	}
	broke := storage.HistoryEntry{Action: "copy", ExitCode: ptrInt64(128), Success: true}
	state, outcome := historyOutcome(broke)
	if state != components.ActivityFailed || outcome != "exit 128" {
		t.Fatalf("a command that exited non-zero reads %v %q", state, outcome)
	}
}

// A command that was never run says what was done with it instead. None of
// these may read as "exit 0" — inventing the one fact the reader came for is
// the worst thing this screen could do.
func TestHistoryOutcome_NeverRunSaysWhatWasDoneInstead(t *testing.T) {
	for _, tc := range []struct {
		action  string
		state   components.ActivityState
		outcome string
	}{
		{"copy", components.ActivityDone, "copied"},
		{"save", components.ActivityDone, "saved"},
		{"cancel", components.ActivityDenied, "dismissed"},
		{"", components.ActivityQueued, "not run"},
	} {
		state, outcome := historyOutcome(storage.HistoryEntry{Action: tc.action, Success: true})
		if state != tc.state || outcome != tc.outcome {
			t.Fatalf("action %q reads %v %q, want %v %q", tc.action, state, outcome, tc.state, tc.outcome)
		}
	}
}

// An entry recorded before the exit code column existed says the code was not
// recorded rather than claiming a clean exit.
func TestHistoryOutcome_RunWithNoCodeSaysSo(t *testing.T) {
	state, outcome := historyOutcome(storage.HistoryEntry{Action: "run", Success: true})
	if state == components.ActivityFailed {
		t.Fatalf("a run with no recorded code is not a failure, got %v", state)
	}
	if !strings.Contains(outcome, "not recorded") {
		t.Fatalf("want the missing code stated, got %q", outcome)
	}
}

// A request that broke before it answered says that, and never a blank.
func TestHistoryOutcome_NoAnswerIsAFailure(t *testing.T) {
	state, outcome := historyOutcome(storage.HistoryEntry{Action: "", Success: false})
	if state != components.ActivityFailed || outcome != "no answer" {
		t.Fatalf("a broken request reads %v %q", state, outcome)
	}
}

// The duration is blank under half a second, the same rule every activity row
// follows.
func TestHistoryDuration_BlankUnderHalfASecond(t *testing.T) {
	for _, tc := range []struct {
		in   *time.Duration
		want string
	}{
		{nil, ""},
		{ptrDuration(120 * time.Millisecond), ""},
		{ptrDuration(1400 * time.Millisecond), "1.4s"},
		{ptrDuration(42 * time.Second), "42s"},
	} {
		if got := historyDuration(tc.in); got != tc.want {
			t.Fatalf("duration %v reads %q, want %q", tc.in, got, tc.want)
		}
	}
}

// How long ago is stated in the row's own words up to a week, and as a date
// after it — "23 days ago" is not how anyone looks for a command.
func TestHistoryAgo(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{now.Add(-30 * time.Second), "just now"},
		{now.Add(-4 * time.Minute), "4m ago"},
		{now.Add(-3 * time.Hour), "3h ago"},
		{now.Add(-30 * time.Hour), "yesterday"},
		{now.Add(-4 * 24 * time.Hour), "sun"},
		{now.Add(-30 * 24 * time.Hour), "Jul 28"},
	} {
		if got := historyAgo(tc.at, now); got != tc.want {
			t.Fatalf("%v ago reads %q, want %q", now.Sub(tc.at), got, tc.want)
		}
	}
	if got := historyAgo(time.Time{}, now); got != "unknown" {
		t.Fatalf("an entry with no timestamp reads %q", got)
	}
}

// The tokens line is omitted rather than rendered as zero for an entry
// recorded before the columns existed.
func TestHistoryTokens_OmittedWhenNotRecorded(t *testing.T) {
	if got := historyTokens(storage.HistoryEntry{}); got != "" {
		t.Fatalf("an entry with no token counts reads %q", got)
	}
	got := historyTokens(storage.HistoryEntry{TokensIn: ptrInt64(412), TokensOut: ptrInt64(38)})
	if got != "↑ 412 · ↓ 38 tokens" {
		t.Fatalf("token line reads %q", got)
	}
}

// The model is stated as provider/model, and degrades to whichever half was
// recorded rather than to a stray slash.
func TestHistoryModelName(t *testing.T) {
	for _, tc := range []struct{ provider, model, want string }{
		{"openai", "gpt-5.2", "openai/gpt-5.2"},
		{"", "gpt-5.2", "gpt-5.2"},
		{"openai", "", "openai"},
		{"", "", ""},
	} {
		got := historyModelName(storage.HistoryEntry{Provider: tc.provider, Model: tc.model})
		if got != tc.want {
			t.Fatalf("%q/%q reads %q, want %q", tc.provider, tc.model, got, tc.want)
		}
	}
}

func historyEntries() []storage.HistoryEntry {
	now := time.Now()
	return []storage.HistoryEntry{
		{ID: 1, CreatedAt: now.Add(-4 * time.Minute), Provider: "openai", Model: "gpt-5.2",
			Prompt: "delete every log file older than a week", Command: "find . -mtime +7 -delete",
			Action: "run", ExitCode: ptrInt64(0), Duration: ptrDuration(1400 * time.Millisecond),
			TokensIn: ptrInt64(412), TokensOut: ptrInt64(38), Success: true},
		{ID: 2, CreatedAt: now.Add(-26 * time.Hour), Provider: "openai", Model: "gpt-5.2",
			Prompt: "show the ten biggest files", Command: "du -ah . | sort -rh | head -10",
			Action: "copy", Success: true},
	}
}

// The header counts what the store holds and how much of it was run — the two
// numbers a reader opens this screen with a question about.
func TestHistoryModel_SubjectCountsTheStore(t *testing.T) {
	m := newHistoryModel(nil, historyEntries(), "", time.Now())
	if m.screen.Subject != "2 entries · 1 run" {
		t.Fatalf("header subject reads %q", m.screen.Subject)
	}
	if len(m.screen.Rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(m.screen.Rows))
	}
	if m.screen.Rows[0].Outcome != "exit 0" || m.screen.Rows[1].Outcome != "copied" {
		t.Fatalf("rows read %q and %q", m.screen.Rows[0].Outcome, m.screen.Rows[1].Outcome)
	}
}

// A count of one is "1 entry", which "entry" plus an s is not.
func TestHistoryModel_SubjectCountsOneEntry(t *testing.T) {
	m := newHistoryModel(nil, historyEntries()[:1], "", time.Now())
	if !strings.HasPrefix(m.screen.Subject, "1 entry ") {
		t.Fatalf("header subject reads %q", m.screen.Subject)
	}
}

// --search lands in the screen's own filter row rather than being applied in
// SQL, so the query row's `1 of 2 match` counts the store and not the query.
func TestHistoryModel_SearchSeedsTheFilterRow(t *testing.T) {
	m := newHistoryModel(nil, historyEntries(), "biggest", time.Now())
	out := m.screen.View(defaultHistoryWidth)
	if !strings.Contains(out, "1 of 2 match") {
		t.Fatalf("the seeded query did not keep both counts:\n%s", out)
	}
	if !strings.Contains(out, "1 entry hidden by the filter") {
		t.Fatalf("the screen did not say what the seeded query hid:\n%s", out)
	}
}

// Deleting the last entry leaves the pointer on a row that still exists.
func TestHistoryModel_DropKeepsThePointerInRange(t *testing.T) {
	m := newHistoryModel(nil, historyEntries(), "", time.Now())
	m.screen.Focus = 1
	m.drop(2)
	if len(m.entries) != 1 {
		t.Fatalf("want 1 entry left, got %d", len(m.entries))
	}
	if m.screen.Focus != 0 {
		t.Fatalf("the pointer is past the end of the list: %d", m.screen.Focus)
	}
}

// A pipe gets the listing as a report: the prompt on the row, the command it
// produced under it, and what became of it as the outcome.
func TestHistoryReport_CarriesTheCommandUnderThePrompt(t *testing.T) {
	got := historyReport(historyEntries(), "", time.Now()).Render(80)
	for _, want := range []string{"shhh history — 2 commands", "✓ run", "delete every log file older than a week", "find . -mtime +7 -delete", "[exit 0]", "✓ copy", "[copied]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the report does not carry %q:\n%s", want, got)
		}
	}
}
