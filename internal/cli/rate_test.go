package cli

// The host half of the rating screen: which keystroke writes what and to
// which table, what the tally at the end says, and the shape the walk takes
// when nobody is holding the keyboard. The screen's own rules are tested
// where the screen is.

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// fakeRatings is a store that remembers what it was told, so a key sequence
// can be checked against the writes it produced — and against the table each
// one landed on, which is the whole of what the second kind adds.
type fakeRatings struct {
	calls []ratingCall
	err   error
}

type ratingCall struct {
	handle string
	up     bool
}

func (f *fakeRatings) RateRequest(id int64, up bool) error {
	return f.record(rateHandle{id: id}, up)
}

func (f *fakeRatings) RateAgentSession(id int64, up bool) error {
	return f.record(rateHandle{session: true, id: id}, up)
}

func (f *fakeRatings) record(h rateHandle, up bool) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, ratingCall{h.String(), up})
	return nil
}

func rateCommands() []storage.UnratedRequest {
	at := goldenNow.Add(-4 * time.Minute)
	return []storage.UnratedRequest{
		{ID: 11, CreatedAt: at, Prompt: "delete every log file older than a week",
			Command: "find . -name '*.log' -mtime +7 -delete", Action: "run", ExitCode: ptrInt64(0)},
		{ID: 12, CreatedAt: at.Add(-time.Hour), Prompt: "rebase onto main and force push",
			Command: "git rebase main && git push --force-with-lease", Action: "run", ExitCode: ptrInt64(128)},
		{ID: 13, CreatedAt: at.Add(-2 * time.Hour), Prompt: "show the ten biggest files here",
			Command: "du -ah . | sort -rh | head -10", Action: "copy"},
		{ID: 14, CreatedAt: at.Add(-3 * time.Hour), Prompt: "count the log lines by level",
			Command: "awk '{print $3}' app.log | sort | uniq -c", Action: "save"},
	}
}

func rateSessions() []storage.UnratedSession {
	return []storage.UnratedSession{
		{ID: 7, StartedAt: goldenNow.Add(-30 * time.Minute), Kind: "chat", Model: "claude-opus-5",
			Turns: 14, Outcome: "completed", Chat: "2026-09-01T18-02",
			Title: "the gate pass rate", Opening: "make the dashboard show the gate rate"},
		{ID: 8, StartedAt: goldenNow.Add(-90 * time.Minute), Kind: "code", Model: "claude-opus-5",
			Turns: 3, Outcome: "abandoned", Chat: "2026-09-01T16-45",
			Opening: "rename the observer's callbacks"},
	}
}

func rateWalk() []rateItem { return rateItems(rateCommands(), rateSessions(), 0) }

// renderReport draws a listing wide enough that nothing under test is clipped
// — the wrapping is the report primitive's own business and is tested there.
func renderReport(t *testing.T, r report.Report) string {
	t.Helper()
	return r.Render(110)
}

// press drives the host the way the program would: the screen answers the
// key, and the host is handed what it answered with.
func press(m *rateModel, pressed ...string) *rateModel {
	for _, p := range pressed {
		m.answer(m.screen.Update(rateKey(p)))
	}
	return m
}

func rateKey(s string) tea.KeyPressMsg {
	if s == "esc" {
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

// The two kinds are one walk, newest first, and neither is offered twice. A
// walk that did every command and then every session would spend the whole
// limit on commands whenever there were enough of them.
func TestRateItems_AreOneWalkNewestFirst(t *testing.T) {
	got := rateWalk()
	want := []string{"c11", "s7", "c12", "s8", "c13", "c14"}
	if len(got) != len(want) {
		t.Fatalf("the walk holds %d entries, want %d", len(got), len(want))
	}
	seen := map[string]bool{}
	for i, item := range got {
		handle := item.handle().String()
		if handle != want[i] {
			t.Errorf("entry %d is %s, want %s", i, handle, want[i])
		}
		if seen[handle] {
			t.Errorf("%s was offered twice", handle)
		}
		seen[handle] = true
	}
	// The limit is a number of questions rather than a number of each kind,
	// so it cuts the merged walk and not the two queries behind it.
	if got := rateItems(rateCommands(), rateSessions(), 2); len(got) != 2 ||
		got[1].handle().String() != "s7" {
		t.Errorf("the limit did not cut the merged walk: %+v", got)
	}
}

// Whichever card was up, the answer reaches the table that entry came from.
// Nothing else in the walk knows there are two, which is why the handle
// carries the kind rather than an index into the list.
func TestRateModel_AnAnswerReachesTheTableItCameFrom(t *testing.T) {
	db := &fakeRatings{}
	m := press(newRateModel(db, rateWalk(), goldenNow), "y", "n", "s", "y", "esc")

	want := []ratingCall{{"c11", true}, {"s7", false}, {"s8", true}}
	if len(db.calls) != len(want) {
		t.Fatalf("the sequence wrote %+v, want %+v", db.calls, want)
	}
	for i, c := range want {
		if db.calls[i] != c {
			t.Errorf("write %d was %+v, want %+v", i, db.calls[i], c)
		}
	}
	if got := m.tally(); got != "rated 3 · skipped 1 · 2 left" {
		t.Errorf("the tally says %q", got)
	}
}

// A handle survives the trip through the screen, which draws it nowhere and
// hands it straight back. Anything else the screen could hand back is not
// something to guess at, so it writes nothing.
func TestRateHandle_RoundTripsAndRefusesNonsense(t *testing.T) {
	for _, h := range []rateHandle{{id: 11}, {session: true, id: 7}} {
		got, ok := parseRateHandle(h.String())
		if !ok || got != h {
			t.Errorf("%+v round-tripped to %+v (ok=%v)", h, got, ok)
		}
	}
	for _, bad := range []string{"", "c", "11", "x11", "cabc", "c0", "c-1"} {
		if _, ok := parseRateHandle(bad); ok {
			t.Errorf("%q parsed as a handle", bad)
		}
	}
	db := &fakeRatings{}
	m := newRateModel(db, rateWalk(), goldenNow)
	m.apply(components.RateAnswer{Act: components.RateWorked, ID: "nonsense"})
	if len(db.calls) != 0 || m.rated != 0 {
		t.Errorf("an unreadable handle wrote %+v", db.calls)
	}
}

// Answering the last entry closes the program: the screen has nothing left to
// ask and the host has nothing left to write.
func TestRateModel_TheLastAnswerEndsTheRun(t *testing.T) {
	db := &fakeRatings{}
	m := newRateModel(db, rateWalk()[:2], goldenNow)
	m = press(m, "y")
	cmd := m.answer(m.screen.Update(rateKey("y")))
	if cmd == nil {
		t.Fatal("answering the last entry did not end the run")
	}
	if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Errorf("the last answer produced %T, not a quit", cmd())
	}
	if got := m.tally(); got != "rated 2" {
		t.Errorf("a run that answered everything tallies %q", got)
	}
}

// Stopping ends the program too, and the tally it leaves counts what is left
// rather than pretending the rest were answered.
func TestRateModel_EscEndsTheRunWithWhatIsLeft(t *testing.T) {
	m := newRateModel(&fakeRatings{}, rateWalk(), goldenNow)
	press(m, "y")
	cmd := m.answer(m.screen.Update(rateKey("esc")))
	if cmd == nil {
		t.Fatal("esc did not end the run")
	}
	if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Errorf("esc produced %T, not a quit", cmd())
	}
	if got := m.tally(); got != "rated 1 · 5 left" {
		t.Errorf("stopping tallied %q", got)
	}
}

// A write that fails leaves the entry unrated and says so, rather than ending
// a run the reader is most of the way through.
func TestRateModel_AFailedWriteIsANoticeNotAnEnding(t *testing.T) {
	db := &fakeRatings{err: errors.New("database is locked")}
	m := press(newRateModel(db, rateWalk(), goldenNow), "y")
	if m.rated != 0 {
		t.Errorf("a failed write counted as a rating")
	}
	if !strings.Contains(m.screen.Notice, "database is locked") {
		t.Errorf("the failure is not on the notice line: %q", m.screen.Notice)
	}
	if got := m.tally(); got != "rated 0 · 6 left" {
		t.Errorf("the tally says %q", got)
	}
}

// The notice belongs to the keystroke that left it, not to the run: the next
// key clears it, so a write that failed once does not sit under every card
// after it.
func TestRateModel_TheNoticeLastsOneKeystroke(t *testing.T) {
	db := &fakeRatings{err: errors.New("database is locked")}
	m := press(newRateModel(db, rateWalk(), goldenNow), "y")
	if m.screen.Notice == "" {
		t.Fatal("the failed write left no notice")
	}
	if m = press(m, "s"); m.screen.Notice != "" {
		t.Errorf("the notice outlived its keystroke: %q", m.screen.Notice)
	}
}

// The tally states what it has and nothing it does not: a run with nothing
// skipped does not say `skipped 0`, and one that reached the end does not
// claim anything is left.
func TestRateTally_StatesOnlyWhatHappened(t *testing.T) {
	for _, tc := range []struct {
		rated, skipped, left int
		want                 string
	}{
		{2, 1, 4, "rated 2 · skipped 1 · 4 left"},
		{3, 0, 0, "rated 3"},
		{0, 0, 7, "rated 0 · 7 left"},
	} {
		if got := rateTally(tc.rated, tc.skipped, tc.left); got != tc.want {
			t.Errorf("rateTally(%d, %d, %d) = %q, want %q",
				tc.rated, tc.skipped, tc.left, got, tc.want)
		}
	}
}

// The card is a reading of the store, and the host is where that reading
// happens: the exit code becomes the row's outcome and its glyph.
func TestRateItem_ReadsACommandTheWayTheBrowserDoes(t *testing.T) {
	items := rateItems(rateCommands(), nil, 0)
	if row := items[1].card(goldenNow); row.Outcome != "exit 128" ||
		row.State != components.ActivityFailed {
		t.Errorf("a command that exited non-zero reads %v %q", row.State, row.Outcome)
	}
	// A command that was copied and never run says that instead — never
	// "exit 0", which would invent the one fact the reader came for.
	if row := items[2].card(goldenNow); row.Outcome != "copied" {
		t.Errorf("a command that was never run reads %q", row.Outcome)
	}
	row := items[0].card(goldenNow)
	if row.ID != "c11" || row.When != "4m ago" {
		t.Errorf("the host did not carry the entry's handle and its age: %+v", row)
	}
	if row.Kind != components.ActivityCommand || row.Verb != "run" || row.Target != items[0].command.Command {
		t.Errorf("a command is not drawn as one: %+v", row)
	}
}

// A session is the same card with the session's own words in it: the
// conversation reminds the reader what it was, the row underneath says which
// conversation and how big, and the outcome is the one the record inferred —
// which is the thing the answer is checking.
func TestRateItem_ReadsASessionOntoTheSameCard(t *testing.T) {
	items := rateItems(nil, rateSessions(), 0)

	titled := items[0].card(goldenNow)
	if titled.Prompt != "the gate pass rate" {
		t.Errorf("a titled session is reminded by %q, want its title", titled.Prompt)
	}
	if titled.ID != "s7" || titled.When != "30m ago" {
		t.Errorf("the host did not carry the session's handle and its age: %+v", titled)
	}
	if titled.Kind != components.ActivitySubagent || titled.Verb != "agent" {
		t.Errorf("a session is not drawn as an agent run: %+v", titled)
	}
	if want := "2026-09-01T18-02 · chat · claude-opus-5 · 14 turns"; titled.Target != want {
		t.Errorf("the session row reads %q, want %q", titled.Target, want)
	}
	if titled.Outcome != "completed" || titled.State != components.ActivityDone {
		t.Errorf("a completed session reads %v %q", titled.State, titled.Outcome)
	}

	// Without a title the opening line is the reminder, and an abandoned
	// session does not arrive wearing a tick.
	untitled := items[1].card(goldenNow)
	if untitled.Prompt != "rename the observer's callbacks" {
		t.Errorf("an untitled session is reminded by %q, want its opening", untitled.Prompt)
	}
	if untitled.Outcome != "abandoned" || untitled.State != components.ActivityDenied {
		t.Errorf("an abandoned session reads %v %q", untitled.State, untitled.Outcome)
	}
}

// A session's reminder is bounded and a command's prompt is not, because an
// opening message is whatever was pasted into a session: unbounded, it would
// push the row the question is about off the bottom of the card.
func TestSessionReminder_IsBounded(t *testing.T) {
	long := strings.Repeat("describe the whole of the provider layer ", 10)
	got := sessionReminder(storage.UnratedSession{Opening: long})
	if r := []rune(got); len(r) != maxReminder || r[len(r)-1] != '…' {
		t.Errorf("a long opening reads %d runes: %q", len(r), got)
	}
	// A title is already written to a bound, so it passes through whole.
	title := "the gate pass rate"
	if got := sessionReminder(storage.UnratedSession{Title: title, Opening: long}); got != title {
		t.Errorf("a title was not left alone: %q", got)
	}
}

// The record's words are read as it wrote them, and a word this build has
// never seen is neither a pass nor a failure. A session with no outcome at
// all says so rather than leaving the column blank, which would read as one
// that came out fine.
func TestSessionOutcome_NamesWhatTheRecordSaid(t *testing.T) {
	for _, tc := range []struct {
		outcome string
		state   components.ActivityState
		want    string
	}{
		{"completed", components.ActivityDone, "completed"},
		{"error", components.ActivityFailed, "error"},
		{"interrupted", components.ActivityDenied, "interrupted"},
		{"abandoned", components.ActivityDenied, "abandoned"},
		{"unknown", components.ActivityQueued, "unknown"},
		{"", components.ActivityQueued, "unknown"},
		{"a word from a later build", components.ActivityQueued, "a word from a later build"},
	} {
		state, got := sessionOutcome(tc.outcome)
		if state != tc.state || got != tc.want {
			t.Errorf("sessionOutcome(%q) = %v %q, want %v %q",
				tc.outcome, state, got, tc.state, tc.want)
		}
	}
}

// Neither flag is both kinds, one flag is that kind alone, and both flags are
// both — a reader who named every kind there is has not narrowed anything.
func TestRateScopeOf_NarrowsTheWalk(t *testing.T) {
	for _, tc := range []struct {
		commandsOnly, sessionsOnly bool
		want                       rateScope
	}{
		{false, false, rateScope{commands: true, sessions: true}},
		{true, false, rateScope{commands: true}},
		{false, true, rateScope{sessions: true}},
		{true, true, rateScope{commands: true, sessions: true}},
	} {
		if got := rateScopeOf(tc.commandsOnly, tc.sessionsOnly); got != tc.want {
			t.Errorf("rateScopeOf(%v, %v) = %+v, want %+v",
				tc.commandsOnly, tc.sessionsOnly, got, tc.want)
		}
	}
}

// A narrowed walk does not run the query it excluded, so it offers nothing of
// that kind and counts nothing of it into what it prints.
func TestUnratedItems_AsksOnlyForWhatIsInScope(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	if _, err := db.RecordRequest(storage.RequestRecord{Provider: "anthropic", Model: "m",
		Prompt: "list the log files", Command: "ls *.log", Action: "run"}); err != nil {
		t.Fatalf("record request: %v", err)
	}
	if err := db.SaveChat("a-session", []provider.Message{
		{Role: provider.RoleUser, Content: "make the dashboard show the gate rate"}}); err != nil {
		t.Fatalf("save chat: %v", err)
	}
	id, err := db.StartAgentSession("chat", "anthropic", "m")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := db.LinkAgentSession(id, "a-session"); err != nil {
		t.Fatalf("link session: %v", err)
	}

	for _, tc := range []struct {
		name     string
		scope    rateScope
		want     int
		subject  string
		sessions int
	}{
		{name: "both", scope: rateScope{commands: true, sessions: true}, want: 2, sessions: 1,
			subject: "1 unrated command · 1 unrated session"},
		{name: "commands", scope: rateScope{commands: true}, want: 1,
			subject: "1 unrated command"},
		{name: "sessions", scope: rateScope{sessions: true}, want: 1, sessions: 1,
			subject: "1 unrated session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items, err := unratedItems(db, tc.scope, 20)
			if err != nil {
				t.Fatalf("read what is unrated: %v", err)
			}
			if len(items) != tc.want {
				t.Fatalf("the walk holds %d entries, want %d", len(items), tc.want)
			}
			sessions := 0
			for _, item := range items {
				if item.session != nil {
					sessions++
				}
			}
			if sessions != tc.sessions {
				t.Errorf("the walk holds %d sessions, want %d", sessions, tc.sessions)
			}
			if got := rateSubject(items); got != tc.subject {
				t.Errorf("the walk is about %q, want %q", got, tc.subject)
			}
		})
	}
}

// The empty state says what was looked at rather than what exists: a walk
// narrowed to one kind that reported the other rated as well would be
// answering a question nobody asked it.
func TestRateReport_TheEmptyStateSaysWhatWasLookedAt(t *testing.T) {
	for _, tc := range []struct {
		scope rateScope
		want  string
	}{
		{rateScope{commands: true, sessions: true}, "every recent command and saved session is rated"},
		{rateScope{commands: true}, "every recent command is rated"},
		{rateScope{sessions: true}, "every recent saved session is rated"},
	} {
		got := renderReport(t, rateReport(nil, tc.scope, goldenNow))
		if !strings.Contains(got, tc.want) {
			t.Errorf("the empty state does not say %q:\n%s", tc.want, got)
		}
	}
	// `--table` lists both kinds, because a listing that dropped the sessions
	// would disagree with the walk it is the non-interactive form of.
	got := renderReport(t, rateReport(rateWalk(), rateScope{commands: true, sessions: true}, goldenNow))
	for _, want := range []string{
		"4 unrated commands · 2 unrated sessions",
		"find . -name '*.log' -mtime +7 -delete",
		"the gate pass rate", "2026-09-01T18-02 · chat · claude-opus-5 · 14 turns",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the listing does not say %q:\n%s", want, got)
		}
	}
}

// Without a terminal the walk is line-oriented, and it is the same walk: both
// kinds through the report primitive, `q` stops, and the tally closes it.
func TestRateByLine_IsTheSameWalkThroughTheReport(t *testing.T) {
	db := &fakeRatings{}
	var out strings.Builder
	if err := rateByLine(db, strings.NewReader("y\nn\ns\nq\n"), &out, rateWalk(), goldenNow); err != nil {
		t.Fatalf("the line walk failed: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"shhh rate",
		"4 unrated commands · 2 unrated sessions",
		"[y] worked · [n] did not · [s] skip · [q] stop",
		"1 of 6",
		"prompt:",
		"about:",
		"session:",
		"outcome:",
		"rated 2 · skipped 1 · 3 left",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the line walk does not say %q:\n%s", want, got)
		}
	}
	if want := []ratingCall{{"c11", true}, {"s7", false}}; len(db.calls) != 2 ||
		db.calls[0] != want[0] || db.calls[1] != want[1] {
		t.Errorf("the line walk wrote %+v, want %+v", db.calls, want)
	}
}

// A write that fails ends the walk with a non-zero exit — and still says what
// was rated and what is left, because those answers were written and a reader
// who is told neither has to go and find out.
func TestRateByLine_AFailedWriteStillTallies(t *testing.T) {
	db := &fakeRatings{err: errors.New("database is locked")}
	var out strings.Builder
	err := rateByLine(db, strings.NewReader("s\ny\n"), &out, rateWalk(), goldenNow)
	if err == nil {
		t.Fatal("a failed write did not end the walk")
	}
	if got := out.String(); !strings.Contains(got, "rated 0 · skipped 1 · 5 left") {
		t.Errorf("the walk ended without a tally:\n%s", got)
	}
}

// The walk's key row is spelled from the register rather than written down,
// so rewording a binding cannot leave the line saying the old words.
func TestRateLineKeys_ComeFromTheRegister(t *testing.T) {
	got := rateLineKeys()
	for _, b := range []keys.Binding{keys.Screen.Worked, keys.Screen.Failed, keys.Screen.Skip} {
		if want := keys.Bracket(b) + " " + keys.Words(b); !strings.Contains(got, want) {
			t.Errorf("the key row does not offer %q: %q", want, got)
		}
	}
	// The one word the walk owns: it reads lines, and esc is not a line.
	if want := keys.Bracket(keys.Screen.Quit) + " stop"; !strings.Contains(got, want) {
		t.Errorf("the key row does not offer %q: %q", want, got)
	}
}

// A stdin that closes mid-walk is a stop, not a run of skips: the rest is
// left, because answering for a reader who is not there would be inventing
// answers.
func TestRateByLine_AClosedStdinIsAStop(t *testing.T) {
	db := &fakeRatings{}
	var out strings.Builder
	if err := rateByLine(db, strings.NewReader("y\n"), &out, rateWalk(), goldenNow); err != nil {
		t.Fatalf("the line walk failed: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "rated 1 · 5 left") {
		t.Errorf("a closed stdin did not leave the rest of the list alone:\n%s", got)
	}
}
