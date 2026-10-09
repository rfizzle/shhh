package chat

// The tray (docs/interface/surfaces.md#the-input-frame): every attachment a
// sent message carried has a row under it, and every row that holds
// something opens it, whenever it is pressed or clicked.

import (
	"fmt"
	"image/color"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// trayPNG is a real 32×16 picture under a name, the way a door hands one
// over: sniffed and named, and not yet given a handle.
func trayPNG(t *testing.T, name string) provider.Attachment {
	t.Helper()
	a := stageImage(t, frameModel(t, 100, 40), name).attachments[0]
	a.Handle = ""
	return a
}

// trayPDF is a two-page PDF as the path door reads one.
func trayPDF(t *testing.T) provider.Attachment {
	t.Helper()
	a, err := attachment.FromBytes("spec.pdf", []byte("%PDF-1.4\n1 0 obj <</Type /Pages /Count 2>>\n"+
		"2 0 obj <</Type /Page>>\n3 0 obj <</Type /Page>>\n%%EOF\n"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != provider.AttachmentDocument {
		t.Fatalf("the fixture sniffed as %s, not a document", a.Kind)
	}
	return a
}

// attach stages one file through the door a path takes: at the cursor, the
// way a dragged path lands, or by `/paste <path>`, which leaves no fold.
func attach(t *testing.T, m Model, a provider.Attachment, path string, atCursor bool) Model {
	t.Helper()
	updated, _ := m.handleAttachedFile(attachedFileMsg{attachment: a, path: path, atCursor: atCursor})
	return updated.(Model)
}

// sendDraft sends the draft as it stands, with whatever is staged.
func sendDraft(t *testing.T, m Model) Model {
	t.Helper()
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return updated.(Model)
}

// trayIndices is where the transcript's tray rows are.
func trayIndices(m Model) []int {
	var out []int
	for i, e := range m.transcript {
		if e.kind == entryTray {
			out = append(out, i)
		}
	}
	return out
}

// trayAt is the tray row that stands for a handle.
func trayAt(t *testing.T, m Model, handle string) int {
	t.Helper()
	for _, i := range trayIndices(m) {
		if m.transcript[i].fold.label == handle {
			return i
		}
	}
	t.Fatalf("no tray row for %s", handle)
	return -1
}

// twoPictures is a session that sent two pictures in one message, both
// folded into the words, and a paste and a PDF beside them.
func twoPictures(t *testing.T) Model {
	t.Helper()
	m := frameModel(t, 120, 40)
	m.input.SetValue("before ")
	m = attach(t, m, trayPNG(t, "first.png"), "", true)
	m.input.InsertString(" and after ")
	m = attach(t, m, trayPNG(t, "second.png"), "", true)
	m.input.InsertString(" the click; the log ")
	updated, _ := m.stagePaste(strings.TrimSuffix(strings.Repeat("round 26 reached, loop still running\n", 214), "\n"))
	m = updated.(Model)
	m = attach(t, m, trayPDF(t), "", false)
	return sendDraft(t, m)
}

// Every attachment has its own row, folded in the words or not, and the
// sentence keeps its folds as they were. The `attached:` line is gone.
func TestUserEntry_EveryAttachmentHasARow(t *testing.T) {
	m := twoPictures(t)
	if got := len(trayIndices(m)); got != 4 {
		t.Fatalf("the message has %d tray rows, want one for each of its four attachments", got)
	}
	user := m.transcript[trayIndices(m)[0]-1]
	if user.kind != entryUser || !user.trayed {
		t.Fatalf("the tray does not stand under its message: %+v", user)
	}
	view := stripANSI(strings.Join(m.renderHistoryLines(), "\n"))
	for _, want := range []string{
		"⟨Image#1 · 32×16⟩", "⟨Image#2 · 32×16⟩", "⟨Paste#1 · 214 lines⟩",
		"⟨▣ Image#1⟩  first.png · 32×16",
		"⟨▣ Image#2⟩  second.png · 32×16",
		"⟨¶ Paste#1⟩  paste-1.txt · 214 lines · ",
		"⟨▤ File#1⟩   spec.pdf · 2 pages",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the transcript does not carry %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "attached:") {
		t.Errorf("the attached line is still drawn:\n%s", view)
	}

	// A message saved before handles existed has a row for its picture too.
	rows := m.sentEntries("look", []provider.Attachment{{Kind: provider.AttachmentImage, Name: "old.png", Data: pngHeader}})
	if len(rows) != 2 || !strings.Contains(stripANSI(m.renderEntry(rows[1], 120)), "⟨▣⟩") {
		t.Fatalf("a handle-less picture has no row: %d entries", len(rows))
	}
}

// A picture staged with `/paste <path>` put no fold in the sentence, and its
// row is a row like the rest: it says where the picture came from and opens
// the picture.
func TestUserEntry_APathStagedPictureIsOpenable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := attach(t, frameModel(t, 120, 40), trayPNG(t, "shot.png"), filepath.Join(home, "Desktop", "shot.png"), false)
	m.input.SetValue("this is the chip strip at 32 columns")
	m = sendDraft(t, m)
	idx := trayAt(t, m, "Image#1")
	row := stripANSI(m.renderEntry(m.transcript[idx], 120))
	if want := "⟨▣ Image#1⟩  shot.png · 32×16 · from ~/Desktop"; !strings.Contains(row, want) {
		t.Fatalf("the row does not say %q:\n%s", want, row)
	}
	if !trayOpens(m.transcript[idx]) {
		t.Fatal("a picture staged by path cannot be opened")
	}
	opened, _ := m.openTrayPicture(idx)
	if card := opened.(Model); card.state != statePreview || card.preview == nil || card.preview.Name != "shot.png" {
		t.Fatalf("the row did not open its picture (state %d)", card.state)
	}
}

// Reading mode's cursor stops on each row that opens something — the two
// pictures and the paste — and not on the message or the PDF; enter opens
// the attachment the row names, so the second picture is as reachable as the
// first.
func TestReading_TheCursorStopsOnEachAttachmentRow(t *testing.T) {
	m := twoPictures(t)
	first, second := trayAt(t, m, "Image#1"), trayAt(t, m, "Image#2")
	paste, pdf := trayAt(t, m, "Paste#1"), trayAt(t, m, "File#1")
	stops := m.expandableIndices()
	for _, want := range []int{first, second, paste} {
		if !slices.Contains(stops, want) {
			t.Errorf("row %d is not a stop: %v", want, stops)
		}
	}
	for _, not := range []int{first - 1, pdf} {
		if slices.Contains(stops, not) {
			t.Errorf("row %d is a stop and should not be: %v", not, stops)
		}
	}

	next, _ := m.enterFocusMode()
	rm := next.(Model)
	rm.focusIdx = first
	rm.moveFocus(1)
	if rm.focusIdx != second {
		t.Fatalf("j from the first picture went to row %d, want the second picture's %d", rm.focusIdx, second)
	}
	opened, _ := rm.openCursorRow(stateFocus)
	if card := opened.(Model); card.state != statePreview || card.preview.Name != "second.png" {
		t.Fatalf("enter on the second picture's row opened %+v", card.preview)
	}
	if bar := stripANSI(strings.Join(rm.focusHintLines(), "\n")); !strings.Contains(bar, "[enter] open it") {
		t.Fatalf("the bar does not say what enter does on a picture's row:\n%s", bar)
	}
}

// A picture's row has no open state: every press opens the card, the card's
// way out comes back to the row, and the row never draws ▾.
func TestReading_EveryPressOnAPictureRowOpensItsCard(t *testing.T) {
	m := twoPictures(t)
	idx := trayAt(t, m, "Image#1")
	next, _ := m.enterFocusMode()
	rm := next.(Model)
	rm.focusIdx = idx
	for press := 1; press <= 3; press++ {
		opened, _ := rm.openCursorRow(stateFocus)
		card := opened.(Model)
		if card.state != statePreview || card.preview == nil {
			t.Fatalf("press %d did not open the card (state %d)", press, card.state)
		}
		rm = pressOn(t, card, tea.KeyPressMsg{Code: tea.KeyEscape})
		if rm.state != stateFocus || rm.focusIdx != idx || rm.transcript[idx].expanded {
			t.Fatalf("press %d came back to state %d, row %d, open %v",
				press, rm.state, rm.focusIdx, rm.transcript[idx].expanded)
		}
		if view := stripANSI(strings.Join(rm.renderHistoryLines(), "\n")); strings.Contains(view, "▾") {
			t.Fatalf("press %d left an open mark on the transcript:\n%s", press, view)
		}
	}
}

// fgParam is the SGR parameter run that sets c as a truecolour foreground.
func fgParam(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)
}

// No row prints a key; the one key the tray prints is the opened paste's
// count, and it is in the hint grey, never the key colour, because the draft
// holds enter while the reader looks at it.
func TestUserEntry_TheRowOfferIsAHintOrAbsent(t *testing.T) {
	themeRestore(t)
	components.SetMono(false)
	was := components.Profile()
	components.SetProfile(colorprofile.TrueColor)
	t.Cleanup(func() { components.SetProfile(was) })

	m := twoPictures(t)
	key := fgParam(components.Palette.Key.Color())
	for _, h := range []string{"Image#1", "Paste#1", "File#1"} {
		row := m.renderEntry(m.transcript[trayAt(t, m, h)], 120)
		if strings.Contains(stripANSI(row), "[") || strings.Contains(row, key) {
			t.Errorf("%s's closed row prints a key:\n%s", h, row)
		}
	}
	paste := m.transcript[trayAt(t, m, "Paste#1")]
	paste.expanded = true
	open := m.renderEntry(paste, 120)
	plain := stripANSI(open)
	if !strings.Contains(plain, "… 206 more · [enter] again for the rest") {
		t.Fatalf("the opened paste does not count what it held back:\n%s", plain)
	}
	if n := strings.Count(plain, "round 26 reached"); n != maxToolResultLines {
		t.Fatalf("the opened paste shows %d lines, want %d", n, maxToolResultLines)
	}
	if strings.Contains(open, key) {
		t.Fatalf("the opened paste draws a key in the key colour:\n%q", open)
	}
	countLine := ""
	for _, l := range strings.Split(open, "\n") {
		if strings.Contains(stripANSI(l), "again for the rest") {
			countLine = l
		}
	}
	if !strings.Contains(countLine, fgParam(components.Palette.Dim.Color())) {
		t.Fatalf("the count's key is not in the hint grey: %q", countLine)
	}
}

// `/paste show <handle>` reaches what the session has sent, and the menu
// offers it after what is staged. A handle that names nothing says what is
// staged and how sent ones are reached.
func TestPasteShow_ASentHandleOpensItsCard(t *testing.T) {
	m := attach(t, frameModel(t, 120, 40), trayPNG(t, "first.png"), "", false)
	m.input.SetValue("look at this")
	m = sendDraft(t, m)

	updated, _ := m.runPaste([]string{"/paste", "show", "image#1"})
	card := updated.(Model)
	if card.state != statePreview || card.preview == nil || card.preview.Sent != "sent with turn 1" {
		t.Fatalf("the sent handle did not open its card: state %d, %+v", card.state, card.preview)
	}
	if hint := stripANSI(card.renderPreviewHint()); !strings.Contains(hint, "back to the draft") || strings.Contains(hint, "remove") {
		t.Fatalf("the card's hint is %q", hint)
	}

	updated, _ = m.runPaste([]string{"/paste", "show", "Image#3"})
	if got, want := lastSystemText(updated.(Model)), "nothing staged · sent attachments are reached by handle: Image#1"; got != want {
		t.Fatalf("the notice is %q, want %q", got, want)
	}

	m = attach(t, m, trayPNG(t, "second.png"), "", false)
	updated, _ = m.runPaste([]string{"/paste", "show", "Image#3"})
	if got := lastSystemText(updated.(Model)); !strings.HasPrefix(got, "Image#3 is not attached — Image#2 (second.png, ") ||
		!strings.HasSuffix(got, " · sent attachments are reached by handle: Image#1") {
		t.Fatalf("with something staged the notice is %q", got)
	}

	var offered []string
	for _, o := range attachmentShowArgs(&m) {
		offered = append(offered, o.value)
	}
	if got := strings.Join(offered, " "); got != "Image#2 Image#1" {
		t.Fatalf("the menu offers %q, want the staged handle before the sent one", got)
	}
}

// Bare `/paste show` keeps meaning the staging area, whatever was sent.
func TestPasteShow_BareStillMeansTheStagingArea(t *testing.T) {
	m := attach(t, frameModel(t, 120, 40), trayPNG(t, "first.png"), "", false)
	m.input.SetValue("look at this")
	m = sendDraft(t, m)

	updated, _ := m.runPaste([]string{"/paste", "show"})
	if got, want := lastSystemText(updated.(Model)), "nothing staged · sent attachments are reached by handle: Image#1"; got != want {
		t.Fatalf("bare with nothing staged says %q, want %q", got, want)
	}

	m = attach(t, m, trayPNG(t, "second.png"), "", false)
	updated, _ = m.runPaste([]string{"/paste", "show"})
	card := updated.(Model)
	if card.state != statePreview || card.preview.Name != "second.png" || card.preview.Sent != "" {
		t.Fatalf("bare opened %+v, want the staged picture", card.preview)
	}
}

// The card says the picture was sent and with which turn, offers no drop,
// and goes back to wherever it was opened from.
func TestPreview_ASentPictureSaysWhichTurnCarriedIt(t *testing.T) {
	pic := trayPNG(t, "clipboard.png")
	pic.Handle = "Image#1"
	m := frameModel(t, 110, 40)
	m.transcript = append(m.sentEntries("the first question", nil),
		m.sentEntries("and this one carries the picture", []provider.Attachment{pic})...)
	idx := trayAt(t, m, "Image#1")

	opened, _ := m.openTrayPicture(idx)
	card := opened.(Model)
	if card.preview == nil || card.preview.Sent != "sent with turn 2" {
		t.Fatalf("the card says %+v", card.preview)
	}
	if top := stripANSI(strings.Split(card.preview.View(108), "\n")[0]); !strings.Contains(top, "clipboard.png · sent with turn 2") {
		t.Fatalf("the card's title is %q", top)
	}
	if hint := stripANSI(card.renderPreviewHint()); hint != "[esc] back to the draft" {
		t.Fatalf("opened from the draft, the hint is %q", hint)
	}

	next, _ := m.enterFocusMode()
	rm := next.(Model)
	rm.focusIdx = idx
	opened, _ = rm.openCursorRow(stateFocus)
	card = opened.(Model)
	if hint := stripANSI(card.renderPreviewHint()); hint != "[esc] back to reading" {
		t.Fatalf("opened from reading, the hint is %q", hint)
	}
	if back := pressOn(t, card, tea.KeyPressMsg{Code: tea.KeyEscape}); back.state != stateFocus || back.focusIdx != idx {
		t.Fatalf("esc came back to state %d on row %d", back.state, back.focusIdx)
	}

	// A recall stages the same picture again under the same handle and name;
	// the sent row's card still offers no drop, and its key leaves the
	// recalled chip where it is.
	rm.attachments = []provider.Attachment{pic}
	opened, _ = rm.openCursorRow(stateFocus)
	card = opened.(Model)
	if hint := stripANSI(card.renderPreviewHint()); hint != "[esc] back to reading" {
		t.Fatalf("with its recalled copy staged, the sent card's hint is %q", hint)
	}
	if kept := pressOn(t, card, tea.KeyPressMsg{Code: 'x', Text: "x"}); len(kept.attachments) != 1 {
		t.Fatal("the sent card's drop took the recalled chip out of the draft")
	}
}

// A session reopened from its store draws the same rows, and the picture
// opens from the bytes stored with the turn.
func TestReopen_ASentPictureStillOpens(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	pic := trayPNG(t, "clipboard.png")
	pic.Handle = "Image#1"
	saved := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "this one", Attachments: []provider.Attachment{pic}},
		{Role: provider.RoleAssistant, Content: "Looked at it."},
	}
	if err := db.SaveChat("tray", saved); err != nil {
		t.Fatal(err)
	}
	loaded, err := db.LoadChat("tray")
	if err != nil {
		t.Fatal(err)
	}
	m := resumedModel(t, loaded)
	idx := trayAt(t, m, "Image#1")
	if row := stripANSI(m.renderEntry(m.transcript[idx], 100)); !strings.Contains(row, "⟨▣ Image#1⟩  clipboard.png · 32×16") {
		t.Fatalf("the reopened row is %q", row)
	}
	opened, _ := m.openTrayPicture(idx)
	card := opened.(Model)
	if card.state != statePreview || card.preview == nil || card.preview.Image == nil || card.preview.Sent != "sent with turn 1" {
		t.Fatalf("the reopened picture did not open: state %d, %+v", card.state, card.preview)
	}
}

// trayProgram is a session under the real program that has sent two pictures
// in one message, with the reply drawn under them.
func trayProgram(t *testing.T) (*program, string) {
	t.Helper()
	p := &programProvider{turns: []programTurn{{text: "Looked at both."}}}
	tm := runProgram(t, New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, streamOf(p), Wiring{}))
	tm.Send(attachedFileMsg{attachment: trayPNG(t, "first.png")})
	tm.Send(attachedFileMsg{attachment: trayPNG(t, "second.png")})
	waitForText(t, tm, "Image#2")
	tm.Type("before and after the click")
	tm.Send(programEnter)
	frame := waitForFrame(t, tm, "both tray rows and the reply", func(f string) bool {
		return strings.Contains(f, "⟨▣ Image#2⟩") && strings.Contains(f, "Looked at both.")
	})
	return tm, frame
}

// clickTrayRow clicks the name on the row that names a handle.
func clickTrayRow(t *testing.T, tm *program, frame, handle string) {
	t.Helper()
	for y, line := range strings.Split(frame, "\n") {
		at := strings.Index(line, "⟨▣ "+handle+"⟩")
		if at < 0 || strings.Contains(line, "before") {
			continue
		}
		x := len([]rune(line[:at])) + components.TrayHandleSlot
		tm.Send(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y})
		tm.Send(tea.MouseReleaseMsg{Button: tea.MouseNone, X: x, Y: y})
		return
	}
	t.Fatalf("no row for %s on the frame:\n%s", handle, frame)
}

// A click on a row opens the picture that row names: the second picture of
// the message, not the first.
func TestProgram_AClickOpensThePictureItsRowNames(t *testing.T) {
	tm, frame := trayProgram(t)
	clickTrayRow(t, tm, frame, "Image#2")
	waitForText(t, tm, "second.png · sent with turn 1")
	if f := finalFrame(t, tm); strings.Contains(f, "first.png · sent with") {
		t.Fatalf("the click opened the first picture:\n%s", f)
	}
}

// A second click on the same row opens the card again: the row has no open
// state for the second click to close.
func TestProgram_ASecondClickOnASentPictureOpensItAgain(t *testing.T) {
	tm, frame := trayProgram(t)
	clickTrayRow(t, tm, frame, "Image#2")
	waitForText(t, tm, "second.png · sent with turn 1")
	programPress(t, tm, "esc")
	waitForGone(t, tm, "sent with turn 1")
	clickTrayRow(t, tm, frame, "Image#2")
	waitForText(t, tm, "second.png · sent with turn 1")
	frameHas(t, finalFrame(t, tm), "[esc] back to the draft")
}
