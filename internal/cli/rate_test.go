package cli

// The host half of the rating screen: which keystroke writes what, what the
// tally at the end says, and the shape the walk takes when nobody is holding
// the keyboard. The screen's own rules are tested where the screen is.

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// fakeRatings is a store that remembers what it was told, so a key sequence
// can be checked against the writes it produced.
type fakeRatings struct {
	calls []ratingCall
	err   error
}

type ratingCall struct {
	id int64
	up bool
}

func (f *fakeRatings) RateRequest(id int64, up bool) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, ratingCall{id, up})
	return nil
}

func rateEntries() []storage.UnratedRequest {
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

// press drives the host the way the program would, through the model's own
// Update.
func press(m rateModel, pressed ...string) rateModel {
	for _, p := range pressed {
		next, _ := m.Update(rateKey(p))
		m = next.(rateModel)
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

// The whole of the host in one sequence: `y` and `n` are writes, `s` is not,
// and esc stops with the tally saying what was answered, what was passed over
// and what is left.
func TestRateModel_KeysWriteAndTheTallySaysWhatIsLeft(t *testing.T) {
	db := &fakeRatings{}
	m := press(newRateModel(db, rateEntries(), goldenNow), "y", "n", "s", "esc")

	want := []ratingCall{{11, true}, {12, false}}
	if len(db.calls) != len(want) {
		t.Fatalf("the sequence wrote %+v, want two ratings", db.calls)
	}
	for i, c := range want {
		if db.calls[i] != c {
			t.Errorf("write %d was %+v, want %+v", i, db.calls[i], c)
		}
	}
	if got := m.tally(); got != "rated 2 · skipped 1 · 1 left" {
		t.Errorf("the tally says %q", got)
	}
}

// Answering the last entry closes the program: the screen has nothing left to
// ask and the host has nothing left to write.
func TestRateModel_TheLastAnswerEndsTheRun(t *testing.T) {
	db := &fakeRatings{}
	m := newRateModel(db, rateEntries()[:2], goldenNow)
	m = press(m, "y")
	next, cmd := m.Update(rateKey("y"))
	if cmd == nil {
		t.Fatal("answering the last entry did not end the run")
	}
	if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Errorf("the last answer produced %T, not a quit", cmd())
	}
	if got := next.(rateModel).tally(); got != "rated 2" {
		t.Errorf("a run that answered everything tallies %q", got)
	}
}

// Stopping ends the program too, and the tally it leaves counts what is left
// rather than pretending the rest were answered.
func TestRateModel_EscEndsTheRunWithWhatIsLeft(t *testing.T) {
	m := newRateModel(&fakeRatings{}, rateEntries(), goldenNow)
	next, cmd := press(m, "y").Update(rateKey("esc"))
	if cmd == nil {
		t.Fatal("esc did not end the run")
	}
	if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Errorf("esc produced %T, not a quit", cmd())
	}
	if got := next.(rateModel).tally(); got != "rated 1 · 3 left" {
		t.Errorf("stopping tallied %q", got)
	}
}

// A write that fails leaves the entry unrated and says so, rather than ending
// a run the reader is most of the way through.
func TestRateModel_AFailedWriteIsANoticeNotAnEnding(t *testing.T) {
	db := &fakeRatings{err: errors.New("database is locked")}
	m := press(newRateModel(db, rateEntries(), goldenNow), "y")
	if m.rated != 0 {
		t.Errorf("a failed write counted as a rating")
	}
	if !strings.Contains(m.screen.Notice, "database is locked") {
		t.Errorf("the failure is not on the notice line: %q", m.screen.Notice)
	}
	if got := m.tally(); got != "rated 0 · 4 left" {
		t.Errorf("the tally says %q", got)
	}
}

// The notice belongs to the keystroke that left it, not to the run: the next
// key clears it, so a write that failed once does not sit under every card
// after it.
func TestRateModel_TheNoticeLastsOneKeystroke(t *testing.T) {
	db := &fakeRatings{err: errors.New("database is locked")}
	m := press(newRateModel(db, rateEntries(), goldenNow), "y")
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
func TestRateRow_ReadsTheEntryTheWayTheBrowserDoes(t *testing.T) {
	entries := rateEntries()
	if row := rateRow(entries[1], goldenNow); row.Outcome != "exit 128" ||
		row.State != components.ActivityFailed {
		t.Errorf("a command that exited non-zero reads %v %q", row.State, row.Outcome)
	}
	// A command that was copied and never run says that instead — never
	// "exit 0", which would invent the one fact the reader came for.
	if row := rateRow(entries[2], goldenNow); row.Outcome != "copied" {
		t.Errorf("a command that was never run reads %q", row.Outcome)
	}
	if row := rateRow(entries[0], goldenNow); row.ID != "11" || row.When != "4m ago" {
		t.Errorf("the host did not carry the entry's handle and its age: %+v", row)
	}
}

// Without a terminal the walk is line-oriented, and it is the same walk: the
// entries go through the report primitive, `q` stops, and the tally closes it.
func TestRateByLine_IsTheSameWalkThroughTheReport(t *testing.T) {
	db := &fakeRatings{}
	var out strings.Builder
	if err := rateByLine(db, strings.NewReader("y\nn\ns\nq\n"), &out, rateEntries(), goldenNow); err != nil {
		t.Fatalf("the line walk failed: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"shhh rate",
		"[y] worked · [n] did not · [s] skip · [q] stop",
		"1 of 4",
		"prompt:",
		"outcome:",
		"rated 2 · skipped 1 · 1 left",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the line walk does not say %q:\n%s", want, got)
		}
	}
	if len(db.calls) != 2 {
		t.Errorf("the line walk wrote %+v, want two ratings", db.calls)
	}
}

// A write that fails ends the walk with a non-zero exit — and still says what
// was rated and what is left, because those answers were written and a reader
// who is told neither has to go and find out.
func TestRateByLine_AFailedWriteStillTallies(t *testing.T) {
	db := &fakeRatings{err: errors.New("database is locked")}
	var out strings.Builder
	err := rateByLine(db, strings.NewReader("s\ny\n"), &out, rateEntries(), goldenNow)
	if err == nil {
		t.Fatal("a failed write did not end the walk")
	}
	if got := out.String(); !strings.Contains(got, "rated 0 · skipped 1 · 3 left") {
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
	if err := rateByLine(db, strings.NewReader("y\n"), &out, rateEntries(), goldenNow); err != nil {
		t.Fatalf("the line walk failed: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "rated 1 · 3 left") {
		t.Errorf("a closed stdin did not leave the rest of the list alone:\n%s", got)
	}
}
