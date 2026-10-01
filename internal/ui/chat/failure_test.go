package chat

// The session's half of the failure taxonomy: the mapping from a
// class to a row, and whether the keys that row offers actually reach
// anything.

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// failureModel is a session wide enough to render, with both recovery hooks
// wired so every key a class can offer is actually offered.
func failureModel(t *testing.T) Model {
	t.Helper()
	m := frameModel(t, 110, 40)
	m.providerName = "openai"
	m.replaceKeyFn = func(string) error { return nil }
	m.switchProviderFn = func(string) error { return nil }
	return m
}

// keyPress builds the key message for a single-rune key, the way bubbletea
// delivers one.
func keyPress(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

func TestStreamError_RendersAsAClassifiedRow(t *testing.T) {
	m := failureModel(t)
	failure := &provider.Failure{
		Class: provider.ClassAuth, Status: 401, Provider: "openai",
		Message: "Incorrect API key provided", KeyTail: "4f9c",
	}
	updated, _ := m.Update(streamErrMsg{err: failure})
	next := updated.(Model)

	var found *entry
	for i := range next.transcript {
		if next.transcript[i].kind == entryFailure {
			found = &next.transcript[i]
		}
	}
	if found == nil {
		t.Fatal("a stream failure should land as a failure row, not as an error line")
	}
	view := stripANSI(next.failureRow(*found).View(110))
	for _, want := range []string{"model", "401 unauthorized", "key ···4f9c rejected", "Incorrect API key provided"} {
		if !strings.Contains(view, want) {
			t.Errorf("the row should say %q, got:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Error:") {
		t.Errorf("no raw error line should survive, got:\n%s", view)
	}
}

func TestStreamError_UnclassifiedStillRendersAsARow(t *testing.T) {
	m := failureModel(t)
	updated, _ := m.Update(streamErrMsg{err: errors.New("something went sideways")})
	next := updated.(Model)
	var e entry
	for _, candidate := range next.transcript {
		if candidate.kind == entryFailure {
			e = candidate
		}
	}
	if e.kind != entryFailure || e.fail == nil {
		t.Fatalf("an unclassified error should still be a row, got %d entries", len(next.transcript))
	}
	if e.fail.Class != provider.ClassUnclassified {
		t.Errorf("class = %q, want unclassified", e.fail.Class)
	}
	view := stripANSI(next.failureRow(e).View(110))
	if !strings.Contains(view, "unclassified") || !strings.Contains(view, "something went sideways") {
		t.Errorf("the message belongs in the detail body, got:\n%s", view)
	}
}

func TestFailureRow_StateAndKeysByClass(t *testing.T) {
	m := failureModel(t)
	cases := []struct {
		class provider.Class
		state components.RecoveryState
		keys  []string
	}{
		{provider.ClassAuth, components.RecoveryBroken, []string{"[e]", "[p]"}},
		{provider.ClassRateLimit, components.RecoveryStalled, []string{"[r]", "[p]"}},
		{provider.ClassQuota, components.RecoveryBroken, []string{"[p]"}},
		{provider.ClassOverloaded, components.RecoveryStalled, []string{"[r]", "[p]"}},
		{provider.ClassContextLength, components.RecoveryBroken, []string{"[c]", "[r]"}},
		// No [r]: the next request would carry the same id.
		{provider.ClassModelNotFound, components.RecoveryBroken, []string{"[p]"}},
		{provider.ClassNetwork, components.RecoveryStalled, []string{"[r]", "[p]"}},
		{provider.ClassMalformed, components.RecoveryBroken, []string{"[r]", "[p]"}},
		{provider.ClassCancelled, components.RecoveryStopped, nil},
		{provider.ClassUnclassified, components.RecoveryBroken, []string{"[r]", "[p]"}},
	}
	for _, tc := range cases {
		t.Run(string(tc.class), func(t *testing.T) {
			row := m.failureRow(entry{kind: entryFailure, fail: &provider.Failure{Class: tc.class}})
			if row.State != tc.state {
				t.Errorf("state = %v, want %v", row.State, tc.state)
			}
			var got []string
			for _, k := range row.Keys {
				got = append(got, k.Key)
			}
			if strings.Join(got, " ") != strings.Join(tc.keys, " ") {
				t.Errorf("keys = %v, want %v", got, tc.keys)
			}
			if row.Outcome == "" {
				t.Error("every class owes the row an outcome")
			}
		})
	}
}

func TestFailureKeys_OmitWhatTheSessionCannotDo(t *testing.T) {
	m := frameModel(t, 110, 40)
	m.providerName = "openai"
	// No hooks wired: a session that cannot replace its key or switch its
	// provider must not offer to.
	row := m.failureRow(entry{kind: entryFailure, fail: &provider.Failure{Class: provider.ClassAuth}})
	if len(row.Keys) != 0 {
		t.Errorf("a session with no hooks should offer no keys, got %v", row.Keys)
	}
	if row.Outcome != "no key was sent" {
		t.Errorf("outcome = %q, want the no-key case", row.Outcome)
	}
}

func TestFailureKey_ClaimedOnlyForTheFocusedRow(t *testing.T) {
	m := failureModel(t)
	m.transcript = []entry{{kind: entryFailure, fail: &provider.Failure{Class: provider.ClassAuth}}}
	m.focusIdx = 0

	if _, _, claimed := m.failureKey("e"); !claimed {
		t.Fatal("[e] should reach the focused auth row")
	}
	// A key the class does not offer is never claimed.
	if _, _, claimed := m.failureKey("c"); claimed {
		t.Error("[c] is not offered by an auth failure")
	}
	// And nothing is claimed with the cursor elsewhere: the input keeps every
	// one of these letters for typing.
	m.focusIdx = -1
	if _, _, claimed := m.failureKey("e"); claimed {
		t.Error("a row the cursor is not on should claim nothing")
	}
}

func TestEnterFocusMode_OpensOnTheFailureThatEndedTheTurn(t *testing.T) {
	m := failureModel(t)
	m.transcript = []entry{
		{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"a.go"}`, toolResult: "x"},
		{kind: entryFailure, fail: &provider.Failure{Class: provider.ClassRateLimit}},
		{kind: entryTurnClose, close: &components.TurnClose{State: components.TurnFailed}},
	}
	m.invalidateRenderCache()
	focused, _ := m.enterFocusMode()
	next := focused.(Model)
	if next.state != stateFocus {
		t.Fatalf("state = %v, want focus mode", next.state)
	}
	if next.transcript[next.focusIdx].kind != entryFailure {
		t.Errorf("the cursor should open on the failure, not on the rows about it")
	}
	if _, _, claimed := next.failureKey("r"); !claimed {
		t.Error("[r] should reach the row focus mode opened on")
	}
}

func TestFailureKey_RetryReopensTheTurn(t *testing.T) {
	m := failureModel(t)
	m.transcript = []entry{{kind: entryFailure, fail: &provider.Failure{Class: provider.ClassOverloaded}}}
	m.focusIdx = 0
	updated, cmd, claimed := m.failureKey("r")
	if !claimed {
		t.Fatal("[r] should be claimed by an overloaded row")
	}
	next := updated.(Model)
	if next.turnState() != stateStreaming {
		t.Errorf("state = %v, want streaming", next.turnState())
	}
	if !next.turnOpen {
		t.Error("a retry is a turn, and should be accounted as one")
	}
	if cmd == nil {
		t.Error("[r] should ask the model again")
	}
}

func TestFailureKey_ProviderPickOpensTheSelector(t *testing.T) {
	m := failureModel(t)
	m.transcript = []entry{{kind: entryFailure, fail: &provider.Failure{Class: provider.ClassQuota}}}
	m.focusIdx = 0
	updated, _, claimed := m.failureKey("p")
	if !claimed {
		t.Fatal("[p] should be claimed by a quota row")
	}
	if next := updated.(Model); next.state != statePick {
		t.Errorf("state = %v, want the picker", next.state)
	}
}

func TestKeyEntry_AppliesTheKeyAndNeverShowsIt(t *testing.T) {
	var applied string
	m := failureModel(t)
	m.replaceKeyFn = func(key string) error {
		applied = key
		return nil
	}
	opened, _ := m.openKeyEntry(&provider.Failure{Class: provider.ClassAuth, KeyTail: "4f9c"})
	next := opened.(Model)
	if next.state != stateKeyEntry || next.keyAsk == nil {
		t.Fatalf("[e] should open the key prompt, state = %v", next.state)
	}
	for _, r := range "sk-secret-1234" {
		updated, _ := next.routeOverlay(overlayFor(stateKeyEntry), keyPress(r))
		next = updated.(Model)
	}
	view := strings.Join(next.keyEntryLines(), "\n")
	if strings.Contains(stripANSI(view), "secret") {
		t.Errorf("the prompt must never echo the key, got:\n%s", stripANSI(view))
	}
	done, _ := next.routeOverlay(overlayFor(stateKeyEntry), tea.KeyPressMsg{Code: tea.KeyEnter})
	final := done.(Model)
	if applied != "sk-secret-1234" {
		t.Errorf("the key reached the session as %q", applied)
	}
	if final.state == stateKeyEntry {
		t.Error("enter should hand the screen back")
	}
	last := stripANSI(final.transcript[len(final.transcript)-1].text)
	if !strings.Contains(last, "···1234") {
		t.Errorf("the notice should name the key by its tail, got %q", last)
	}
	if strings.Contains(last, "sk-secret") {
		t.Errorf("the notice must not repeat the key, got %q", last)
	}
}

func TestKeyEntry_EscKeepsTheOldKey(t *testing.T) {
	applied := false
	m := failureModel(t)
	m.replaceKeyFn = func(string) error {
		applied = true
		return nil
	}
	opened, _ := m.openKeyEntry(&provider.Failure{Class: provider.ClassAuth})
	next := opened.(Model)
	updated, _ := next.routeOverlay(overlayFor(stateKeyEntry), keyPress('x'))
	updated, _ = updated.(Model).routeOverlay(overlayFor(stateKeyEntry), tea.KeyPressMsg{Code: tea.KeyEscape})
	final := updated.(Model)
	if applied {
		t.Error("esc declines; it must not replace the key")
	}
	if final.state == stateKeyEntry {
		t.Error("esc should hand the screen back")
	}
}

func TestFailureRow_DurationIsWhatTheTurnCost(t *testing.T) {
	m := failureModel(t)
	m.turnStarted = time.Now().Add(-3 * time.Second)
	m.appendFailure(&provider.Failure{Class: provider.ClassNetwork})
	e := m.transcript[len(m.transcript)-1]
	if e.duration < 2*time.Second {
		t.Errorf("duration = %v, want the turn's elapsed time", e.duration)
	}
	if got := stripANSI(m.failureRow(e).View(110)); !strings.Contains(got, "s") {
		t.Errorf("the row should carry a duration, got:\n%s", got)
	}
}

func TestFocusMode_PutsTheCursorOnTheFailureRow(t *testing.T) {
	m := failureModel(t)
	m.transcript = []entry{
		{kind: entryUser, text: "rename the sentinel"},
		{kind: entryFailure, fail: &provider.Failure{
			Class: provider.ClassAuth, Status: 401, KeyTail: "4f9c",
		}},
	}
	m.invalidateRenderCache()
	focused, _ := m.enterFocusMode()
	next := focused.(Model)
	content, _, _ := next.renderFocusHistory()
	for _, line := range strings.Split(stripANSI(content), "\n") {
		if strings.Contains(line, "401 unauthorized") {
			if !strings.HasPrefix(line, "❯ ") {
				t.Errorf("the failure row should carry the focus cursor, got %q", line)
			}
			return
		}
	}
	t.Fatal("the failure row never rendered")
}

// failedTurn is a session whose turn read a file and then broke on an
// unclassified 400.
func failedTurn(t *testing.T) Model {
	t.Helper()
	m := failureModel(t)
	m.modelName = "gpt-4o"
	m = sendText(t, m, "rename the sentinel")
	m.appendEntry(entry{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"loop.go"}`, toolResult: "package agent"})
	updated, _ := m.Update(streamErrMsg{err: &provider.Failure{
		Class: provider.ClassUnclassified, Status: 400, Provider: "openai",
		Message: "Unknown parameter: 'reasoning.effort'",
	}})
	return updated.(Model)
}

// A failure is a card: the model and what the turn had done on the header,
// the class right; what it means as the body; the provider's own words and
// the ways out as the footer — sharing a line at 120 columns and over, and
// the keys on a line of their own, right-aligned, under it below that. The
// keys are grey beside the handover until the card holds the keyboard.
func TestFailure_IsACardWithItsKeys(t *testing.T) {
	for _, tc := range []struct {
		width int
		share bool
	}{{110, false}, {80, false}, {60, false}} {
		m := failedTurn(t)
		e := m.transcript[indexOfKind(t, m, entryFailure)]
		lines := strings.Split(stripANSI(m.renderEntry(e, tc.width)), "\n")
		header := cardLine(lines, "✗ model gpt-4o")
		if header == "" || !strings.Contains(header, "400 unclassified") {
			t.Fatalf("w%d: the header names the model and the class:\n%s", tc.width, strings.Join(lines, "\n"))
		}
		if tc.width >= 110 && !strings.Contains(header, "after read loop.go") {
			t.Errorf("w%d: the header says what the turn had done: %q", tc.width, header)
		}
		if body := cardLine(lines, "The provider refused the request"); !strings.Contains(body, "Nothing in the turn was lost.") && tc.width > 100 {
			t.Errorf("w%d: the body says what the failure means and what it cost: %q", tc.width, body)
		}
		words := cardLine(lines, "Unknown parameter")
		if got := strings.Contains(words, "[r]"); got != tc.share {
			t.Errorf("w%d: the keys share the provider's line = %v, want %v:\n%s", tc.width, got, tc.share, strings.Join(lines, "\n"))
		}
		if keys := strings.Join(lines, "\n"); !strings.Contains(keys, "[r] retry the last failure") || !strings.Contains(keys, "to use them") {
			t.Errorf("w%d: unselected, the keys wait beside the handover that reaches them:\n%s", tc.width, strings.Join(lines, "\n"))
		}
	}

	m := failedTurn(t)
	view := stripANSI(m.renderEntryKeys(m.transcript[indexOfKind(t, m, entryFailure)], 130, rowUnderCursor))
	if !strings.Contains(view, "❯ ✗ model gpt-4o") || strings.Contains(view, "to use them") {
		t.Fatalf("under the cursor the card is lit and its keys are live:\n%s", view)
	}
	if words := cardLine(strings.Split(view, "\n"), "Unknown parameter"); !strings.Contains(words, "[r] try again") {
		t.Fatalf("at 120 columns and over the keys share the provider's line:\n%s", view)
	}
}

// A retry leaves a line where the reader's words would stand, at the
// prompt's column, saying what it asks again: the same prompt, of the same
// model, or of the model a provider switch since put the session on.
func TestRetry_RowSaysWhatItRepeats(t *testing.T) {
	for _, tc := range []struct {
		name, model, want string
	}{
		{"the same model", "gpt-4o", "↻ try again · same prompt, same model"},
		{"after a switch", "gpt-4.1", "↻ try again · same prompt, now on gpt-4.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := failedTurn(t)
			m.modelName = tc.model
			next, _ := m.retryTurn()
			m = next.(Model)
			at := indexOfKind(t, m, entryRetry)
			if at < indexOfKind(t, m, entryFailure) {
				t.Fatal("the retry line stands after the failure it retries")
			}
			if got := stripANSI(m.renderEntry(m.transcript[at], 80)); !strings.HasPrefix(got, tc.want) {
				t.Fatalf("the retry line reads %q, want %q", got, tc.want)
			}
		})
	}
}
