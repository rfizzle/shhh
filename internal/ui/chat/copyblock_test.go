package chat

// One code block copied by itself (docs/interface/surfaces.md#reading-mode):
// /copy code by number or from the numbered card, and reading mode's key on
// any reply. The clipboard is the session's copyFn, held by every test here,
// never the machine's.

import (
	"slices"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/golden"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// threeBlocks is a reply with two languages and one bare fence. The Go block
// is indented with tabs, which the renderer expands and the copy must not.
const threeBlocks = "Here is the handler:\n" +
	"```go\nfunc copyBlock(src string, i int) {\n\tif i < 1 {\n\t\treturn\n\t}\n}\n```\n" +
	"The scene builds once and captures at both widths:\n" +
	"```sh\nmake build\nmake tui-shot SCENE=copy-block COLS=110 ROWS=40\nmake tui-shot SCENE=copy-block COLS=60 ROWS=40\n```\n" +
	"Its steps open the list and take the second row:\n" +
	"```\nkeys \"/copy code\" Enter\nsnap 01-list \"copy which block?\"\n```"

const (
	goBody   = "func copyBlock(src string, i int) {\n\tif i < 1 {\n\t\treturn\n\t}\n}"
	shBody   = "make build\nmake tui-shot SCENE=copy-block COLS=110 ROWS=40\nmake tui-shot SCENE=copy-block COLS=60 ROWS=40"
	bareBody = "keys \"/copy code\" Enter\nsnap 01-list \"copy which block?\""
)

// oneBlock is the earlier reply: a single block.
const oneBlock = "Build it first:\n```sh\nmake build\n```"

// replyModel is a session whose last response is reply, with a clipboard
// that records what reached it.
func replyModel(t *testing.T, reply string, caught *[]string) Model {
	t.Helper()
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "how?"},
		{Role: provider.RoleAssistant, Content: reply},
	}
	updated, _ := New(msgs, mockStream, Wiring{}).Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m := updated.(Model)
	m.copyFn = func(text string) clipboard.Result {
		*caught = append(*caught, text)
		return clipboard.Result{OK: true}
	}
	return m
}

// lastNotice is the newest system row's text.
func lastNotice(t *testing.T, m Model) string {
	t.Helper()
	es := *m.entries()
	for i := len(es) - 1; i >= 0; i-- {
		if es[i].kind == entrySystem {
			return es[i].text
		}
	}
	t.Fatal("no notice in the transcript")
	return ""
}

func TestCopyCode_OneBlockIsCopiedAsBefore(t *testing.T) {
	var caught []string
	m := sendText(t, replyModel(t, oneBlock, &caught), "/copy code")
	if m.picker.card != nil {
		t.Fatal("one block should be copied, not offered")
	}
	if !slices.Equal(caught, []string{"make build"}) {
		t.Fatalf("copied %q", caught)
	}
	if got := lastNotice(t, m); got != "copied last code to clipboard" {
		t.Errorf("notice %q", got)
	}
}

func TestCopyCode_SeveralBlocksOpenTheList(t *testing.T) {
	var caught []string
	m := sendText(t, replyModel(t, threeBlocks, &caught), "/copy code")
	if m.state != statePick || m.picker.card == nil {
		t.Fatalf("several blocks should open the card, got state %d", m.state)
	}
	if len(caught) != 0 {
		t.Fatalf("opening the card copies nothing, got %q", caught)
	}
	if m.picker.card.Title != "copy which block?" || len(m.picker.card.Options) != 3 {
		t.Fatalf("card %q with %d rows", m.picker.card.Title, len(m.picker.card.Options))
	}
	// The rows are the ones /run's card draws: first line, language — a
	// bare fence's is `code` — and line count, the block flattened under
	// the lit row.
	for i, want := range []string{
		"func copyBlock(src string, i int) { · go · 5 lines",
		"make build · sh · 3 lines",
		`keys "/copy code" Enter · code · 2 lines`,
	} {
		if got := pickRowText(m.picker.card.Options[i]); got != want {
			t.Errorf("row %d is %q, want %q", i+1, got, want)
		}
	}
	card := ansi.Strip(strings.Join(m.pickerLines(), "\n"))
	for _, want := range []string{"3 blocks", "[enter] select", "[esc] keep nothing", "func copyBlock(src string, i int) { ↵ if i < 1 {"} {
		if !strings.Contains(card, want) {
			t.Errorf("the card lacks %q:\n%s", want, card)
		}
	}

	m = focusPick(t, m, 1)
	if !slices.Equal(caught, []string{shBody}) {
		t.Fatalf("taking row 2 copied %q", caught)
	}
	if m.state != stateInput {
		t.Errorf("from the draft the card goes back to the draft, got state %d", m.state)
	}
	if got := lastNotice(t, m); got != "copied block 2 · sh · 3 lines" {
		t.Errorf("notice %q", got)
	}

	// Esc keeps nothing.
	caught = nil
	m = sendText(t, m, "/copy code")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.picker.card != nil || len(caught) != 0 {
		t.Fatalf("esc should close the card and copy nothing, got %q", caught)
	}
}

func TestCopyCode_ANumberCopiesThatBlock(t *testing.T) {
	cases := []struct {
		arg, want, notice string
	}{
		{"1", goBody, "copied block 1 · go · 5 lines"},
		{"2", shBody, "copied block 2 · sh · 3 lines"},
		{"3", bareBody, "copied block 3 · code · 2 lines"},
	}
	for _, tc := range cases {
		t.Run(tc.arg, func(t *testing.T) {
			var caught []string
			m := sendText(t, replyModel(t, threeBlocks, &caught), "/copy code "+tc.arg)
			if m.picker.card != nil {
				t.Fatal("a number takes the block without a card")
			}
			if !slices.Equal(caught, []string{tc.want}) {
				t.Fatalf("copied %q, want %q", caught, tc.want)
			}
			if got := lastNotice(t, m); got != tc.notice {
				t.Errorf("notice %q, want %q", got, tc.notice)
			}
		})
	}
}

func TestCopyCode_AllJoinsEveryBlock(t *testing.T) {
	var caught []string
	m := sendText(t, replyModel(t, threeBlocks, &caught), "/copy code all")
	if want := goBody + "\n" + shBody + "\n" + bareBody; !slices.Equal(caught, []string{want}) {
		t.Fatalf("copied %q", caught)
	}
	if got := lastNotice(t, m); got != "copied last code to clipboard" {
		t.Errorf("notice %q", got)
	}

	// The completion menu offers it after `code`.
	m = typeChars(t, readyModel(t), "/copy code ")
	var offered []string
	for _, it := range m.complete.items {
		offered = append(offered, it.name)
	}
	if !slices.Contains(offered, "all") {
		t.Errorf("the menu after /copy code offers %v, want all among them", offered)
	}
}

func TestCopyCode_OutOfRangeSaysTheRange(t *testing.T) {
	for _, arg := range []string{"4", "0", "-1", "two"} {
		t.Run(arg, func(t *testing.T) {
			var caught []string
			m := sendText(t, replyModel(t, threeBlocks, &caught), "/copy code "+arg)
			if len(caught) != 0 {
				t.Fatalf("copied %q", caught)
			}
			if got := lastNotice(t, m); got != "usage: /copy code [1-3]" {
				t.Errorf("notice %q", got)
			}
		})
	}
}

func TestCopyCode_TheBodyIsTheSourceNotTheRender(t *testing.T) {
	// Forty lines is past anything the transcript draws unfolded, and the
	// tabs are what the renderer expands to spaces.
	var long []string
	for i := range 40 {
		long = append(long, "\tline "+strings.Repeat("x", i%3))
	}
	body := strings.Join(long, "\n")
	reply := "Two blocks:\n```go\n" + goBody + "\n```\nand\n~~~text\n" + body + "\n~~~"

	var caught []string
	m := sendText(t, replyModel(t, reply, &caught), "/copy code 2")
	if !slices.Equal(caught, []string{body}) {
		t.Fatalf("copied %q, want the block's source", caught)
	}
	m = sendText(t, m, "/copy code 1")
	got := caught[len(caught)-1]
	if got != goBody {
		t.Fatalf("copied %q, want %q", got, goBody)
	}
	if strings.Contains(got, "```") || strings.HasPrefix(got, "go") || strings.HasPrefix(got, " ") {
		t.Errorf("the copy carries fence, tag or indent: %q", got)
	}

	// It fails the way every copy does.
	m.copyFn = func(string) clipboard.Result { return clipboard.Result{Warning: "no clipboard tool found"} }
	m = sendText(t, m, "/copy code 1")
	if got := lastNotice(t, m); got != "no clipboard tool found" {
		t.Errorf("a failed copy said %q", got)
	}
}

// readingOn is a session holding an earlier reply and a later one, in
// reading mode with the cursor on the entry at idx.
func readingOn(t *testing.T, earlier, later string, idx int, caught *[]string) Model {
	t.Helper()
	m := copyModel(t, caught)
	m.appendEntry(entry{kind: entryUser, text: "where does the copy go in?"})
	m.appendEntry(entry{kind: entryAssistant, text: earlier})
	m.appendEntry(entry{kind: entryUser, text: "show me the handler"})
	m.appendEntry(entry{kind: entryAssistant, text: later})
	m.viewport.SetLines(m.renderHistoryLines())
	updated, _ := m.Update(readingChord())
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("reading mode should open, got state %d", m.state)
	}
	m.focusIdx = idx
	m.refreshFocusView()
	return m
}

func pressC(t *testing.T, m Model) Model {
	t.Helper()
	k := keys.Shown(keys.Reading.CopyBlock)
	updated, _ := m.Update(tea.KeyPressMsg{Code: rune(k[0]), Text: k})
	return updated.(Model)
}

func TestReading_CopyBlockOnARowWithOneBlock(t *testing.T) {
	var caught []string
	m := readingOn(t, oneBlock, threeBlocks, 1, &caught)
	if bar := ansi.Strip(m.readingKeyLine(m.contentWidth())); !strings.Contains(bar, "[c] copy a block") || !strings.Contains(bar, "[y] copy the row") {
		t.Fatalf("a reply with a block offers both copies, got %q", bar)
	}
	m = pressC(t, m)
	if m.picker.card != nil || m.state != stateFocus {
		t.Fatalf("one block copies at once and stays in the mode, got state %d", m.state)
	}
	if !slices.Equal(caught, []string{"make build"}) {
		t.Fatalf("copied %q", caught)
	}
	if m.readingCopied != "✓ copied block 1 · sh · 1 line" {
		t.Errorf("caption %q", m.readingCopied)
	}
	if bar := ansi.Strip(m.readingKeyLine(m.contentWidth())); !strings.Contains(bar, "✓ copied block 1 · sh · 1 line") {
		t.Errorf("the bar should carry the caption, got %q", bar)
	}
}

func TestReading_CopyBlockListsOnlyThatRowsBlocks(t *testing.T) {
	twoBlocks := "Either:\n```go\nfmt.Println(1)\n```\nor:\n```sh\necho 1\necho 2\n```"
	var caught []string
	m := readingOn(t, twoBlocks, threeBlocks, 1, &caught)
	m = pressC(t, m)
	if m.state != statePick || m.picker.card == nil {
		t.Fatalf("several blocks open the card, got state %d", m.state)
	}
	if len(m.picker.card.Options) != 2 {
		t.Fatalf("the card lists the row's own blocks, got %d rows", len(m.picker.card.Options))
	}
	m = focusPick(t, m, 1)
	if !slices.Equal(caught, []string{"echo 1\necho 2"}) {
		t.Fatalf("copied %q", caught)
	}
	if m.state != stateFocus || m.focusIdx != 1 {
		t.Fatalf("the card goes back to the mode on the same row, got state %d row %d", m.state, m.focusIdx)
	}
	if m.readingCopied != "✓ copied block 2 · sh · 2 lines" {
		t.Errorf("caption %q", m.readingCopied)
	}

	// Esc on the card goes back to the mode too, and copies nothing.
	caught = nil
	m = pressC(t, m)
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateFocus || len(caught) != 0 {
		t.Fatalf("esc should go back to the mode with nothing copied, got state %d, %q", m.state, caught)
	}
}

func TestReading_CopyBlockIsSilentOnARowWithoutOne(t *testing.T) {
	cases := []struct {
		name string
		idx  int
	}{
		{"the reader's own message", 0},
		{"a reply with no block", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var caught []string
			m := readingOn(t, "No code here, only prose.", threeBlocks, tc.idx, &caught)
			if bar := ansi.Strip(m.readingKeyLine(m.contentWidth())); strings.Contains(bar, "[c]") {
				t.Fatalf("a row with no block offers no block copy, got %q", bar)
			}
			m = pressC(t, m)
			if len(caught) != 0 || m.picker.card != nil {
				t.Fatalf("copied %q", caught)
			}
			if m.state == stateFocus || m.input.Value() != "c" {
				t.Fatalf("the letter should land in the draft, state %d draft %q", m.state, m.input.Value())
			}
		})
	}
}

// TestGolden_CopyBlock captures the block copy's three surfaces: the
// numbered card /copy code opens over a reply's three blocks, reading mode's
// bar on that reply with the block copy beside the row copy, and the caption
// the bar carries once a block is taken.
func TestGolden_CopyBlock(t *testing.T) {
	captureGolden(t, "copy-block", "one code block copied by itself", goldenWidths, func(width int) []golden.Panel {
		base := func() Model {
			m := frameModel(t, width, 40)
			m.agent.SetMessages(append(m.agent.Messages(),
				provider.Message{Role: provider.RoleUser, Content: "show me the handler"},
				provider.Message{Role: provider.RoleAssistant, Content: threeBlocks}))
			m.copyFn = func(string) clipboard.Result { return clipboard.Result{OK: true} }
			m.transcript = []entry{
				{kind: entryUser, text: "show me the handler"},
				{kind: entryAssistant, text: threeBlocks},
			}
			m.invalidateRenderCache()
			return m
		}
		card := sendText(t, base(), "/copy code")
		card = focusMove(t, card, 1)

		next, _ := base().enterFocusMode()
		reading := next.(Model)
		copied := focusPick(t, focusMove(t, pressC(t, reading), 1), 1)

		return []golden.Panel{
			{Label: "/copy code over three blocks · the numbered card, the second row lit", View: strings.Join(card.pickerLines(), "\n")},
			{Label: "reading mode on the reply · [c] beside [y]", View: readingSurface(reading)},
			{Label: "a block taken · the caption on the bar", View: copied.panelView()},
		}
	})
}

// focusMove walks a picker's pointer to target without taking the row.
func focusMove(t *testing.T, m Model, target int) Model {
	t.Helper()
	for m.picker.card != nil && m.picker.card.Focus < target {
		updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = updated.(Model)
	}
	return m
}

// The route through the runtime: a reply with three blocks arrives, /copy
// code opens the card over them, a digit takes the second, and reading mode's
// key on the earlier reply copies that reply's one block — the keys reaching
// the surfaces as a terminal sends them.
func TestProgram_ABlockIsCopiedFromTheCardAndFromReadingMode(t *testing.T) {
	var (
		mu     sync.Mutex
		caught []string
	)
	p := &programProvider{turns: []programTurn{{text: oneBlock}, {text: threeBlocks}}}
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, streamOf(p), Wiring{})
	m.copyFn = func(text string) clipboard.Result {
		mu.Lock()
		defer mu.Unlock()
		caught = append(caught, text)
		return clipboard.Result{OK: true}
	}
	tm := runProgram(t, m)

	tm.Type("where does the copy go in?")
	tm.Send(programEnter)
	waitForText(t, tm, "Build it first:")
	tm.Type("show me the handler")
	tm.Send(programEnter)
	waitForText(t, tm, "Its steps open the list")

	tm.Type("/copy code")
	tm.Send(programEnter)
	waitForText(t, tm, "copy which block?")
	tm.Send(tea.KeyPressMsg{Code: '2', Text: "2"})
	waitForText(t, tm, "copied block 2 · sh · 3 lines")

	// Into reading mode, and up to the earlier reply: the bar offers the
	// block copy once the cursor stands on it.
	tm.Send(readingChord())
	waitForText(t, tm, "READING")
	for range 3 {
		tm.Send(tea.KeyPressMsg{Code: 'k', Text: "k"})
	}
	waitForText(t, tm, "[c] copy a block")
	tm.Send(tea.KeyPressMsg{Code: 'c', Text: "c"})
	waitForText(t, tm, "✓ copied block 1 · sh · 1 line")

	finalFrame(t, tm)
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(caught, []string{shBody, "make build"}) {
		t.Fatalf("the clipboard took %q", caught)
	}
}
