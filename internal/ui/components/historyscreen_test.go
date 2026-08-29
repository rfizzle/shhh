package components

// The history browser (
// docs/interface/surfaces.md#the-supporting-screens). The assertions here are
// about the two rules the screen exists to keep: the search is on the left
// and the entry it selects is previewed on the right with no cursor of its
// own, and nothing is re-run until [enter].

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

func historyRows() []HistoryRow {
	return []HistoryRow{
		{ID: "1", Prompt: "delete every log file older than a week",
			Command: "find . -name '*.log' -mtime +7 -delete", When: "4m ago",
			Model: "openai/gpt-5.2", Action: "run", Outcome: "exit 0",
			State: ActivityDone, Duration: "1.4s", Counts: "↑ 412 · ↓ 38 tokens"},
		{ID: "2", Prompt: "show the ten biggest files here",
			Command: "du -ah . | sort -rh | head -10", When: "yesterday",
			Model: "openai/gpt-5.2", Action: "copy", Outcome: "copied",
			State: ActivityDone, Duration: "0.9s"},
		{ID: "3", Prompt: "rebase onto main and force push",
			Command: "git rebase main && git push --force-with-lease", When: "tue",
			Model: "anthropic/claude-sonnet-4.6", Action: "run", Outcome: "exit 128",
			State: ActivityFailed, Duration: "2.1s"},
		{ID: "4", Prompt: "count the log lines by level",
			Command: "awk '{print $3}' app.log | sort | uniq -c", When: "mon",
			Model: "openai/gpt-5.2", Action: "cancel", Outcome: "dismissed",
			State: ActivityDenied},
	}
}

func historyScreen() *HistoryScreen {
	return &HistoryScreen{Rows: historyRows(), Subject: "4 entries · 2 run", MaxLines: 18}
}

func typeIntoHistory(h *HistoryScreen, text string) {
	for _, r := range text {
		h.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func plainView(h *HistoryScreen, width int) string { return ansi.Strip(h.View(width)) }

// previewText is the right pane flattened onto one line, for assertions about
// a command that wrapped. Stacked, the whole screen is the pane.
func previewText(h *HistoryScreen, width int) string {
	var parts []string
	for _, line := range strings.Split(plainView(h, width), "\n") {
		if at := strings.Index(line, "\u2502"); at >= 0 {
			line = line[at+len("\u2502"):]
		}
		parts = append(parts, line)
	}
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

// previewCommand is previewText with the outcome and the duration lifted back
// out of the grid row, so a command that wrapped over the row and its
// continuation reads as the one command it is.
func previewCommand(h *HistoryScreen, width int, row HistoryRow) string {
	text := previewText(h, width)
	for _, field := range []string{row.Outcome, row.Duration} {
		if field != "" {
			text = strings.Replace(text, " "+field, "", 1)
		}
	}
	return strings.Join(strings.Fields(text), " ")
}

// The preview follows the pointer and shows the entry's own command — the
// right pane is what makes this a browser rather than a list of prompts.
func TestHistoryScreen_PreviewFollowsThePointer(t *testing.T) {
	h := historyScreen()
	if out := previewCommand(h, 130, h.Rows[0]); !strings.Contains(out, "find . -name '*.log' -mtime +7 -delete") {
		t.Fatalf("the first entry's command is not previewed:\n%s", out)
	}
	h.Update(key("down"))
	out := previewCommand(h, 130, h.Rows[1])
	if !strings.Contains(out, "du -ah . | sort -rh | head -10") {
		t.Fatalf("the preview did not follow the pointer:\n%s", out)
	}
	if strings.Contains(out, "find . -name") {
		t.Fatalf("the previous entry is still previewed:\n%s", out)
	}
}

// The preview has no cursor of its own: exactly one ❯ is on the
// screen, and it is in the list.
func TestHistoryScreen_PreviewHasNoCursor(t *testing.T) {
	out := plainView(historyScreen(), 130)
	if n := strings.Count(out, "❯"); n != 1 {
		t.Fatalf("want one pointer on the screen, got %d:\n%s", n, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "❯") {
			continue
		}
		if at := strings.Index(line, "❯"); at > strings.Index(line, "│") && strings.Contains(line, "│") {
			t.Fatalf("the pointer is in the preview pane, not the list:\n%s", line)
		}
	}
}

// Every row states its outcome and leads with a glyph that says the same
// thing, so a monochrome terminal loses no information (invariant 1).
func TestHistoryScreen_RowStatesOutcomeInGlyphAndWord(t *testing.T) {
	out := plainView(historyScreen(), 130)
	for _, want := range []string{"$ delete every log", "exit 0", "✗ rebase onto main", "exit 128", "⊘ count the log lines", "dismissed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("row is missing %q:\n%s", want, out)
		}
	}
}

// The duration is the right-aligned field the column grid reserves for it.
func TestHistoryScreen_DurationIsRightAligned(t *testing.T) {
	out := plainView(historyScreen(), 130)
	var row string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "delete every log file") && strings.Contains(line, "1.4s") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("no row carried the duration:\n%s", out)
	}
	left := strings.SplitN(row, "│", 2)[0]
	if !strings.HasSuffix(strings.TrimRight(left, " "), "1.4s") {
		t.Fatalf("the duration is not the last field on the row: %q", left)
	}
}

// [/] opens the shared filter row rather than a second query line: the ▸
// prompt, both counts on the row, and the count of what it hid under the
// list with the key that clears it.
func TestHistoryScreen_FilterRowIsTheSharedOne(t *testing.T) {
	h := historyScreen()
	h.Update(key("/"))
	typeIntoHistory(h, "log")
	out := plainView(h, 130)
	for _, want := range []string{"▸ log█", "2 of 4 match", "2 entries hidden by the filter", "[ctrl+u] clear it"} {
		if !strings.Contains(out, want) {
			t.Fatalf("filtered view is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "rebase onto main") {
		t.Fatalf("a row the filter excluded is still showing:\n%s", out)
	}
}

// The filter matches what came back as well as what was asked — a reader
// looking for a command they half remember has both to work with.
func TestHistoryScreen_FilterMatchesTheCommandToo(t *testing.T) {
	h := historyScreen()
	h.Update(key("/"))
	typeIntoHistory(h, "uniq")
	out := plainView(h, 130)
	if !strings.Contains(out, "count the log lines") {
		t.Fatalf("a match on the command did not survive the filter:\n%s", out)
	}
	if !strings.Contains(out, "1 of 4 match") {
		t.Fatalf("the query row miscounted:\n%s", out)
	}
}

// The matched run is bolded in the row rather than tinted.
func TestHistoryScreen_MatchedRunIsBold(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	h := historyScreen()
	h.Update(key("/"))
	typeIntoHistory(h, "log")
	h.Update(key("down")) // off the focused row, which paints whole
	if !strings.Contains(h.View(130), sty.Match.Render("log")) {
		t.Fatal("the matched run is not emphasized in the row")
	}
}

// A filter that matched nothing is a row, not an empty pane, and it keeps the
// key that clears it.
func TestHistoryScreen_NoMatchSaysSo(t *testing.T) {
	h := historyScreen()
	h.Update(key("/"))
	typeIntoHistory(h, "zzz")
	out := plainView(h, 130)
	if !strings.Contains(out, `no match for "zzz"`) {
		t.Fatalf("a filter that matched nothing said nothing:\n%s", out)
	}
	if !strings.Contains(out, "[ctrl+u]") {
		t.Fatalf("the key that clears the filter is gone:\n%s", out)
	}
	if !strings.Contains(out, "no entry selected") {
		t.Fatalf("the preview should say it has nothing to show:\n%s", out)
	}
}

// While the query line is open the row keys are letters: x types an x
// rather than opening the delete confirm.
func TestHistoryScreen_LettersAreTextWhileFiltering(t *testing.T) {
	h := historyScreen()
	h.Update(key("/"))
	typeIntoHistory(h, "x")
	out := plainView(h, 130)
	if strings.Contains(out, "Delete the entry") {
		t.Fatalf("x opened the confirm from inside the query line:\n%s", out)
	}
	if !strings.Contains(out, "▸ x█") {
		t.Fatalf("x was not typed into the query line:\n%s", out)
	}
	if strings.Contains(out, "[x] delete it") {
		t.Fatalf("a key that cannot act is still offered:\n%s", out)
	}
}

// ctrl+u clears the filter, and clearing one that is already empty closes it
// — which is how the row keys are got back without leaving the screen.
func TestHistoryScreen_CtrlUClearsThenCloses(t *testing.T) {
	h := historyScreen()
	h.Update(key("/"))
	typeIntoHistory(h, "log")
	h.Update(key("ctrl+u"))
	if out := plainView(h, 130); !strings.Contains(out, "▸ █") {
		t.Fatalf("ctrl+u did not clear the query:\n%s", out)
	}
	h.Update(key("ctrl+u"))
	out := plainView(h, 130)
	if strings.Contains(out, "▸ ") {
		t.Fatalf("a second ctrl+u did not close the query line:\n%s", out)
	}
	if !strings.Contains(out, "[x] delete it") {
		t.Fatalf("the row keys did not come back:\n%s", out)
	}
}

// A query given on the command line lands in the same filter row a keystroke
// would have opened.
func TestHistoryScreen_SetQuerySeedsTheFilter(t *testing.T) {
	h := historyScreen()
	h.SetQuery("rebase")
	out := plainView(h, 130)
	if !strings.Contains(out, "▸ rebase█") || !strings.Contains(out, "1 of 4 match") {
		t.Fatalf("the seeded query is not in the filter row:\n%s", out)
	}
	if !strings.Contains(out, "git rebase main") {
		t.Fatalf("the pointer did not land on the entry that survived:\n%s", out)
	}
}

// [enter] is the only key that leaves the screen with something to run, and
// it carries the command out with it.
func TestHistoryScreen_EnterRunsTheEntryUnderThePointer(t *testing.T) {
	h := historyScreen()
	h.Update(key("down"))
	done, result := h.Update(key("enter"))
	if !done {
		t.Fatal("enter did not close the screen")
	}
	got, ok := result.(HistoryResult)
	if !ok || !got.Run {
		t.Fatalf("want a run result, got %#v", result)
	}
	if got.ID != "2" || got.Command != "du -ah . | sort -rh | head -10" {
		t.Fatalf("enter ran the wrong entry: %#v", got)
	}
}

// esc and q both leave running nothing, and the foot says so before they are
// pressed.
func TestHistoryScreen_LeavingRunsNothing(t *testing.T) {
	if !strings.Contains(plainView(historyScreen(), 130), "nothing is re-run until [enter]") {
		t.Fatal("the hint line does not say that nothing is re-run on its own")
	}
	for _, k := range []string{"esc", "q", "ctrl+c"} {
		h := historyScreen()
		done, result := h.Update(key(k))
		got, ok := result.(HistoryResult)
		if !done || !ok || !got.Canceled || got.Run {
			t.Fatalf("%s should leave running nothing, got done=%v %#v", k, done, result)
		}
	}
}

// [c] and [s] resolve to the host and leave the screen up, so a reader can
// copy one entry and then delete another.
func TestHistoryScreen_CopyAndSaveResolveWithoutClosing(t *testing.T) {
	for _, tc := range []struct {
		key string
		act HistoryAct
	}{{"c", HistoryCopy}, {"s", HistorySave}} {
		h := historyScreen()
		h.Update(key("down"))
		done, result := h.Update(key(tc.key))
		if done {
			t.Fatalf("[%s] closed the screen", tc.key)
		}
		got, ok := result.(HistoryCommand)
		if !ok || got.Act != tc.act || got.ID != "2" {
			t.Fatalf("[%s] resolved %#v", tc.key, result)
		}
	}
}

// [x] asks before it destroys, names what it would take, and resolves
// nothing until the answer is yes.
func TestHistoryScreen_DeleteAsksFirst(t *testing.T) {
	h := historyScreen()
	done, result := h.Update(key("x"))
	if done || result != nil {
		t.Fatalf("x resolved a delete without asking: done=%v %#v", done, result)
	}
	out := plainView(h, 130)
	if !strings.Contains(out, `Delete the entry for "delete every log file older than a week"?`) {
		t.Fatalf("the confirm does not name what it would take:\n%s", out)
	}
	if !strings.Contains(out, "[y/N]") {
		t.Fatalf("the confirm does not default to no:\n%s", out)
	}

	// n changes nothing.
	if _, result := h.Update(key("n")); result != nil {
		t.Fatalf("declining resolved %#v", result)
	}
	if strings.Contains(plainView(h, 130), "Delete the entry") {
		t.Fatal("the confirm is still up after declining")
	}

	h.Update(key("x"))
	done, result = h.Update(key("y"))
	got, ok := result.(HistoryCommand)
	if done || !ok || got.Act != HistoryDelete || got.ID != "1" {
		t.Fatalf("confirming resolved done=%v %#v", done, result)
	}
}

// The confirm holds the keyboard while it is up: y answers it rather than
// being a letter, and the row keys underneath it are inert (invariant 5).
func TestHistoryScreen_ConfirmHoldsTheKeyboard(t *testing.T) {
	h := historyScreen()
	h.Update(key("x"))
	out := plainView(h, 130)
	if strings.Contains(out, "[c] copy it") {
		t.Fatalf("the row keys are still offered under the confirm:\n%s", out)
	}
	if _, result := h.Update(key("c")); result != nil {
		t.Fatalf("c acted while the confirm held the keyboard: %#v", result)
	}
}

// The command is the thing [enter] would run, so none of it is clipped away:
// a command too long for the pane keeps going underneath (invariant 4).
func TestHistoryScreen_LongCommandIsNotClippedAway(t *testing.T) {
	h := historyScreen()
	h.Rows[0].Command = "rg --hidden --glob '!.git' --line-number 'ErrRoundLimit' internal/agent internal/ui | sort -u"
	out := previewCommand(h, 110, h.Rows[0])
	if !strings.Contains(out, "'ErrRoundLimit' internal/agent internal/ui | sort -u") {
		t.Fatalf("the tail of the command was clipped off:\n%s", out)
	}
	if strings.Contains(out, "…") {
		t.Fatalf("the command was clipped rather than continued:\n%s", out)
	}
}

// A narrow terminal stacks the panes rather than clipping both, and keeps the
// preview (the browser's own reason for the second pane).
func TestHistoryScreen_NarrowStacksThePanes(t *testing.T) {
	out := plainView(historyScreen(), 60)
	if strings.Contains(out, "│") {
		t.Fatalf("60 columns should not be split into two panes:\n%s", out)
	}
	if !strings.Contains(out, "find . -name") {
		t.Fatalf("the preview was dropped instead of stacked:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if len([]rune(line)) > 60 {
			t.Fatalf("a row ran past the terminal: %q", line)
		}
	}
}

// The header names the command and its subject, and offers the two keys every
// one of these screens offers.
func TestHistoryScreen_Header(t *testing.T) {
	head := strings.SplitN(plainView(historyScreen(), 130), "\n", 2)[0]
	for _, want := range []string{"shhh history", "4 entries · 2 run", "[?] keys · [q] quit"} {
		if !strings.Contains(head, want) {
			t.Fatalf("header is missing %q: %q", want, head)
		}
	}
}

// [?] lists every key the screen has, including the ones the compact row gave
// up to make room for the field beside it.
func TestHistoryScreen_KeyListIsComplete(t *testing.T) {
	h := historyScreen()
	h.Update(key("?"))
	out := plainView(h, 130)
	for _, want := range []string{"[↑↓/jk] move", "[enter] run the command", "[c] copy", "[s] save", "[x] delete", "[/] filter", "[ctrl+u]", "[esc]", "[q]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("[?] does not list %q:\n%s", want, out)
		}
	}
}

// The screen is a takeover surface: no card frame around it.
func TestHistoryScreen_DrawsNoFrame(t *testing.T) {
	out := plainView(historyScreen(), 130)
	for _, glyph := range []string{"╭", "╮", "╰", "╯"} {
		if strings.Contains(out, glyph) {
			t.Fatalf("the screen drew a frame (%s):\n%s", glyph, out)
		}
	}
}

// An empty store renders rather than panicking — the host does not open the
// screen on one, but a delete can empty it while it is up.
func TestHistoryScreen_EmptyRenders(t *testing.T) {
	h := &HistoryScreen{Subject: "0 entries · 0 run", MaxLines: 18}
	out := plainView(h, 130)
	if !strings.Contains(out, "no entry selected") {
		t.Fatalf("an empty screen says nothing:\n%s", out)
	}
	if strings.Contains(out, "[enter] re-run it") {
		t.Fatalf("a key with nothing to act on is still offered:\n%s", out)
	}
	if done, result := h.Update(key("enter")); done || result != nil {
		t.Fatalf("enter acted on an empty list: done=%v %#v", done, result)
	}
}

// A filter that missed by a character names what it nearly found; one that
// missed by the whole word says nothing, because a match on two letters is a
// coincidence rather than a near miss.
func TestHistoryScreen_ClosestIsANearMissOrNothing(t *testing.T) {
	h := historyScreen()
	h.SetQuery("rebased")
	if out := plainView(h, 130); !strings.Contains(out, "closest is rebase onto main and force push") {
		t.Fatalf("a near miss did not name what it nearly found:\n%s", out)
	}
	h = historyScreen()
	h.SetQuery("kubectl")
	if out := plainView(h, 130); strings.Contains(out, "closest is") {
		t.Fatalf("a query that missed entirely named something anyway:\n%s", out)
	}
}

// A long prompt gives way to the outcome rather than pushing it off the row:
// the preview beside it carries the prompt in full, so nothing is lost.
func TestHistoryScreen_LongPromptKeepsTheOutcomeOnTheRow(t *testing.T) {
	h := historyScreen()
	h.Rows[0].Prompt = "find every reference to ErrRoundLimit anywhere under the internal directory"
	out := plainView(h, 130)
	var row string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "find every reference") && strings.Contains(line, "❯") {
			row = strings.SplitN(line, "│", 2)[0]
			break
		}
	}
	if row == "" {
		t.Fatalf("the long row is missing:\n%s", out)
	}
	if !strings.Contains(row, "exit 0") || !strings.Contains(row, "1.4s") {
		t.Fatalf("a long prompt pushed the outcome off the row: %q", row)
	}
	if !strings.Contains(row, "…") {
		t.Fatalf("the prompt was not folded to make room: %q", row)
	}
	if !strings.Contains(previewText(h, 130), "under the internal directory") {
		t.Fatalf("the preview does not carry the prompt in full:\n%s", previewText(h, 130))
	}
}
