package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/provider"
)

var pngHeader = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)

func writePNG(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, pngHeader, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func stagePNG(t *testing.T, m Model, name string) Model {
	t.Helper()
	a, err := attachment.FromFile(writePNG(t, name))
	if err != nil {
		t.Fatal(err)
	}
	next, _ := m.stage([]provider.Attachment{a})
	return next.(Model)
}

// What is staged has to be visible somewhere, and the staged rail is where
// the surface says what is pending. It names the file rather than
// counting it, and carries the kind's mark so the two do not have to be
// told apart by colour.
func TestAttachments_StagedRailNamesWhatIsStaged(t *testing.T) {
	m := frameModel(t, 130, 40)
	if m.stagedRail() != "" {
		t.Fatal("an empty staging area should say nothing")
	}
	m = stagePNG(t, m, "shot.png")
	if got := stripANSI(m.stagedRail()); !strings.Contains(got, "▣ shot.png") {
		t.Fatalf("staged rail = %q", got)
	}
	// It is the frame's own row, not one of the notice rail's fields.
	if m.noticeLine() != "" {
		t.Fatalf("notice rail = %q", stripANSI(m.noticeLine()))
	}
	if !strings.Contains(stripANSI(m.View().Content), "▣ shot.png") {
		t.Fatalf("the staged rail should reach the surface:\n%s", stripANSI(m.View().Content))
	}
}

// The rail is a row like any other, so the viewport gives one up for it —
// otherwise the frame grows past the bottom of the terminal.
func TestAttachments_StagedRailCostsTheViewportALine(t *testing.T) {
	m := frameModel(t, 130, 40)
	base := m.viewport.Height()
	m = stagePNG(t, m, "shot.png")
	m.syncViewport()
	if m.viewport.Height() != base-1 {
		t.Fatalf("the staged rail must shrink the viewport (%d -> %d)", base, m.viewport.Height())
	}
}

// Attached, the keyboard is pointed at a child and ctrl+v is a textarea key
// again, so the orchestrator's staging area is not what is on screen.
func TestAttachments_StagedRailIsOrchestratorScoped(t *testing.T) {
	m := stagePNG(t, frameModel(t, 130, 40), "shot.png")
	m.attachedTo = "writer-1"
	if got := m.stagedRail(); got != "" {
		t.Fatalf("staged rail while attached = %q", stripANSI(got))
	}
}

// `/paste drop <name>` is the per-chip half of `/paste clear`: it takes one
// back out and leaves the rest staged, and a name that is not staged is said
// out loud rather than doing nothing.
func TestAttachments_DropTakesOneBackOut(t *testing.T) {
	m := stagePNG(t, stagePNG(t, frameModel(t, 130, 40), "shot.png"), "spec.png")

	updated, _ := m.runPaste([]string{"/paste", "drop", "shot.png"})
	next := updated.(Model)
	if len(next.attachments) != 1 || next.attachments[0].Name != "spec.png" {
		t.Fatalf("staged after the drop = %v", attachment.Names(next.attachments))
	}
	if !strings.Contains(lastSystemText(next), "dropped shot.png") {
		t.Fatalf("the drop should say what it dropped: %q", lastSystemText(next))
	}

	updated, _ = next.runPaste([]string{"/paste", "drop", "nothing.png"})
	if again := updated.(Model); len(again.attachments) != 1 ||
		!strings.Contains(lastSystemText(again), "nothing.png is not attached") {
		t.Fatalf("an unknown name should be refused out loud: %q", lastSystemText(again))
	}

	// A path that merely starts with the word is still a path.
	updated, _ = next.runPaste([]string{"/paste", "dropbox.png"})
	if strings.Contains(lastSystemText(updated.(Model)), "needs a name") {
		t.Fatal("/paste dropbox.png is a path, not the drop subcommand")
	}
}

// Model is a value type and takeAttachments hands the staged slice out whole,
// so dropping a chip must build a new slice rather than shuffling the one
// another holder is already looking at.
func TestAttachments_DropDoesNotReachIntoASharedSlice(t *testing.T) {
	m := stagePNG(t, stagePNG(t, frameModel(t, 130, 40), "shot.png"), "spec.png")
	held := m.attachments

	if _, _ = m.runPaste([]string{"/paste", "drop", "shot.png"}); len(held) != 2 ||
		held[0].Name != "shot.png" || held[1].Name != "spec.png" {
		t.Fatalf("the drop rewrote a slice it does not own: %v", attachment.Names(held))
	}
}

// The payoff: the bytes reach the request on the user message, and the
// transcript names them without drawing them.
func TestAttachments_RideOnTheUserMessage(t *testing.T) {
	m := stagePNG(t, frameModel(t, 100, 40), "shot.png")
	m.input.SetValue("what is this?")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	next := updated.(Model)

	msgs := next.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != provider.RoleUser || len(last.Attachments) != 1 {
		t.Fatalf("last message = %#v", last)
	}
	if last.Attachments[0].Name != "shot.png" || len(last.Attachments[0].Data) == 0 {
		t.Fatalf("attachment = %#v", last.Attachments[0])
	}
	if len(next.attachments) != 0 {
		t.Fatal("sending should empty the staging area — an attachment rides once")
	}
	view := stripANSI(next.View().Content)
	if !strings.Contains(view, "attached: shot.png") {
		t.Fatalf("the transcript should name the attachment:\n%s", view)
	}
}

// Staged while the agent works, they go with the steering line that is
// injected next — the same round, the same request.
func TestAttachments_RideOnQueuedSteering(t *testing.T) {
	m := stagePNG(t, frameModel(t, 100, 40), "shot.png")
	m.setTurnState(stateStreaming)
	m.input.SetValue("also look at this")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	next := updated.(Model)
	if len(next.steering) != 1 {
		t.Fatalf("expected the line to queue as steering, got %v", next.steering)
	}
	if len(next.attachments) != 1 {
		t.Fatal("queueing should not spend the attachment yet")
	}

	if !next.injectSteering() {
		t.Fatal("expected steering to inject")
	}
	msgs := next.Messages()
	last := msgs[len(msgs)-1]
	if len(last.Attachments) != 1 {
		t.Fatalf("the injected steering message lost the attachment: %#v", last)
	}
	if len(next.attachments) != 0 {
		t.Fatal("injecting should empty the staging area")
	}
}

func TestPaste_ClearDropsWhatIsStaged(t *testing.T) {
	m := stagePNG(t, frameModel(t, 100, 40), "shot.png")
	updated, _ := m.runPaste([]string{"/paste", "clear"})
	next := updated.(Model)
	if len(next.attachments) != 0 {
		t.Fatal("/paste clear should drop the staging area")
	}
	if !strings.Contains(lastSystemText(next), "dropped") {
		t.Fatalf("expected a note saying what was dropped: %q", lastSystemText(next))
	}

	// With nothing staged it says so rather than silently doing nothing.
	updated, _ = next.runPaste([]string{"/paste", "clear"})
	if !strings.Contains(lastSystemText(updated.(Model)), "nothing is attached") {
		t.Fatalf("got %q", lastSystemText(updated.(Model)))
	}
}

func TestHandleClipboard_TextFallsBackToTheDraft(t *testing.T) {
	m := frameModel(t, 100, 40)
	m.input.SetValue("say ")
	updated, _ := m.handleClipboard(clipboardMsg{clip: attachment.Clipboard{Text: "hello"}})
	next := updated.(Model)
	if next.input.Value() != "say hello" {
		t.Fatalf("ctrl+v over plain text should still paste it: %q", next.input.Value())
	}
	if len(next.attachments) != 0 {
		t.Fatal("plain text is not an attachment")
	}
}

func TestHandleClipboard_SaysWhyNothingWasAttached(t *testing.T) {
	m := frameModel(t, 100, 40)
	updated, _ := m.handleClipboard(clipboardMsg{
		clip: attachment.Clipboard{Text: "fallback", Rejected: errTooBig{}},
	})
	next := updated.(Model)
	if next.input.Value() != "" {
		t.Fatal("a refused attachment must not silently paste its text fallback")
	}
	if !strings.Contains(lastSystemText(next), "too big") {
		t.Fatalf("expected the reason on screen: %q", lastSystemText(next))
	}
}

type errTooBig struct{}

func (errTooBig) Error() string { return "shot.png is too big" }

// A dragged-in image attaches; a pasted path to a source file stays text,
// because that is the only way to write one into a sentence.
func TestPastedFileAttachment_OnlyClaimsImagesAndDocuments(t *testing.T) {
	png := writePNG(t, "shot.png")
	if got, ok := pastedFileAttachment(png); !ok || got != png {
		t.Fatalf("pastedFileAttachment(%q) = %q, %v", png, got, ok)
	}

	src := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(src, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := pastedFileAttachment(src); ok {
		t.Fatal("a pasted path to a text file should stay text")
	}
	if _, ok := pastedFileAttachment("just some prose"); ok {
		t.Fatal("prose is not a file")
	}
}

func TestStage_RefusesPastTheTotalCeiling(t *testing.T) {
	m := frameModel(t, 100, 40)
	m.attachments = []provider.Attachment{{
		Kind: provider.AttachmentImage, Name: "big.png",
		Data: make([]byte, attachment.MaxTotalBytes-10),
	}}
	updated, _ := m.stage([]provider.Attachment{{
		Kind: provider.AttachmentImage, Name: "more.png", Data: make([]byte, 1024),
	}})
	next := updated.(Model)
	if len(next.attachments) != 1 {
		t.Fatal("the second attachment should have been refused, not truncated")
	}
	if !strings.Contains(lastSystemText(next), "more.png") {
		t.Fatalf("the refusal should name the file: %q", lastSystemText(next))
	}
}

// lastSystemText is the most recent system notice in the transcript.
func lastSystemText(m Model) string {
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if m.transcript[i].kind == entrySystem {
			return m.transcript[i].text
		}
	}
	return ""
}

// A bracketed paste is its own message in v2 rather than a keystroke carrying
// a Paste flag, so the two things a paste can be are routed here
// rather than inside the key handler. Both have to keep working: a dragged-in
// image attaches, and everything else is text going where the keyboard goes.
func TestPasteMsg_AttachesAFileAndTypesEverythingElse(t *testing.T) {
	png := writePNG(t, "shot.png")
	m := frameModel(t, 100, 40)

	updated, cmd := m.Update(tea.PasteMsg{Content: png})
	if cmd == nil {
		t.Fatal("a pasted image path should attach the file")
	}
	if got := updated.(Model).input.Value(); got != "" {
		t.Fatalf("the path should not also land in the draft, got %q", got)
	}

	updated, _ = m.Update(tea.PasteMsg{Content: "some pasted prose"})
	if got := updated.(Model).input.Value(); got != "some pasted prose" {
		t.Fatalf("pasted text belongs in the draft, got %q", got)
	}
}

// The Paste flag bought routing: pasted text reached whichever surface held
// the keyboard rather than the textarea directly, so a card's filter row
// filtered and reading mode handed the keyboard back. The message
// has to keep buying that, which is why it is handed on as the keystroke it
// used to be — a paste that went straight to the draft would leave reading
// mode holding a keyboard it no longer has.
func TestPasteMsg_ReachesTheSurfaceHoldingTheKeyboard(t *testing.T) {
	updated, _ := focusModel(t).Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	reading := updated.(Model)
	if reading.state != stateFocus {
		t.Fatalf("ctrl+e should hand the keyboard to reading mode, state = %v", reading.state)
	}

	updated, _ = reading.Update(tea.PasteMsg{Content: "prose"})
	next := updated.(Model)
	if next.state != stateInput {
		t.Fatalf("typing into reading mode hands the keyboard back; state = %v", next.state)
	}
	if next.input.Value() != "prose" {
		t.Fatalf("and the text it typed is the draft, got %q", next.input.Value())
	}
}

// A stack trace pasted into a three-row box buries the sentence it was meant
// to go with, so past a threshold the paste is staged as a file instead. The
// pair either side of the line is what says the threshold is a threshold and
// not a guess about what the text looks like.
func TestPasteMsg_StagesAPasteTooBigForTheDraft(t *testing.T) {
	tall := strings.Repeat("goroutine 1 [running]:\n", 11)
	updated, _ := frameModel(t, 100, 40).Update(tea.PasteMsg{Content: tall})
	m := updated.(Model)
	if len(m.attachments) != 1 {
		t.Fatalf("an 11-line paste should stage one attachment, got %d", len(m.attachments))
	}
	if got := m.attachments[0].Name; got != "paste-1.txt" {
		t.Fatalf("the staged paste is called %q, want paste-1.txt", got)
	}
	if got := m.attachments[0].Kind; got != provider.AttachmentText {
		t.Fatalf("the staged paste is %q, want text", got)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("the staged paste should not also be typed, got %q", got)
	}

	short := strings.Repeat("goroutine 1 [running]:\n", 9) + "goroutine 1 [running]:"
	updated, _ = frameModel(t, 100, 40).Update(tea.PasteMsg{Content: short})
	m = updated.(Model)
	if len(m.attachments) != 0 {
		t.Fatalf("a 10-line paste belongs in the draft, %d were staged", len(m.attachments))
	}
	if m.input.Value() != short {
		t.Fatalf("a 10-line paste should be the draft, got %q", m.input.Value())
	}
}

// The other half of the threshold: one line nobody can read the end of is as
// much a file as eleven short ones.
func TestPasteMsg_StagesOneVeryWideLine(t *testing.T) {
	updated, _ := frameModel(t, 100, 40).Update(tea.PasteMsg{Content: strings.Repeat("x", 1001)})
	m := updated.(Model)
	if len(m.attachments) != 1 {
		t.Fatalf("a 1001-column paste should stage one attachment, got %d", len(m.attachments))
	}
	if m.input.Value() != "" {
		t.Fatalf("and it should not also be typed, got %q", m.input.Value())
	}

	updated, _ = frameModel(t, 100, 40).Update(tea.PasteMsg{Content: strings.Repeat("x", 1000)})
	if got := updated.(Model).attachments; len(got) != 0 {
		t.Fatalf("a 1000-column paste belongs in the draft, %d were staged", len(got))
	}
}

// A paste past the ceiling is refused with the ceiling named, and the draft is
// left exactly as it was: typing a megabyte in after all is a megabyte the
// reader then has to get back out.
//
// The ceiling is the text one, because a paste has no file behind it and goes
// into the prompt verbatim. Both sides of it are here: the byte over, and the
// six megabytes that would clear the attachment ceiling too.
func TestPasteMsg_RefusesAPastePastTheCeiling(t *testing.T) {
	for _, size := range []int{attachment.MaxTextBytes + 1, 6 << 20} {
		m := frameModel(t, 100, 40)
		m.input.SetValue("look at this: ")
		updated, _ := m.Update(tea.PasteMsg{Content: strings.Repeat("x", size)})
		next := updated.(Model)
		if len(next.attachments) != 0 {
			t.Fatalf("%d bytes should stage nothing, got %d", size, len(next.attachments))
		}
		if got := next.input.Value(); got != "look at this: " {
			t.Fatalf("%d bytes: the draft should be untouched, got %q", size, got)
		}
		notice := lastSystemText(next)
		if !strings.Contains(notice, "256 KB") {
			t.Fatalf("%d bytes: the refusal should name the limit, got %q", size, notice)
		}
	}
}

// A paste whose lines end the way a terminal decided to send them is the same
// paste. Counted in newlines alone, a CR-delimited stack trace is one line —
// it would never be staged, and if the column test caught it the chip would
// say "1 line" about fifty.
func TestPasteMsg_CarriageReturnsCountAsLines(t *testing.T) {
	for _, ending := range []string{"\r", "\r\n"} {
		updated, _ := frameModel(t, 100, 40).Update(
			tea.PasteMsg{Content: strings.Repeat("goroutine 1 [running]:"+ending, 11)})
		m := updated.(Model)
		if len(m.attachments) != 1 {
			t.Fatalf("%q: 11 lines should stage one attachment, got %d", ending, len(m.attachments))
		}
		if got := attachment.LineCount(m.attachments[0].Data); got != 11 {
			t.Fatalf("%q: the staged paste counts %d lines, want 11", ending, got)
		}
	}
}

// Ctrl+V is the other door onto the same staging area, and a clipboard
// holding a log is the same question a bracketed paste of one asks.
func TestClipboard_StagesTextTooBigForTheDraft(t *testing.T) {
	tall := strings.Repeat("goroutine 1 [running]:\n", 11)
	updated, _ := frameModel(t, 100, 40).handleClipboard(
		clipboardMsg{clip: attachment.Clipboard{Text: tall}})
	m := updated.(Model)
	if len(m.attachments) != 1 || m.attachments[0].Name != "paste-1.txt" {
		t.Fatalf("a tall clipboard should stage paste-1.txt, got %v", m.attachments)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("and it should not also be typed, got %q", got)
	}

	// Ordinary text still does what ctrl+v always did.
	updated, _ = frameModel(t, 100, 40).handleClipboard(
		clipboardMsg{clip: attachment.Clipboard{Text: "some prose"}})
	m = updated.(Model)
	if len(m.attachments) != 0 || m.input.Value() != "some prose" {
		t.Fatalf("ordinary clipboard text belongs in the draft, got %q / %v",
			m.input.Value(), m.attachments)
	}
}

// Staging is the draft's own behaviour and nothing else's: a paste into a
// card's filter row is text going where the keyboard went, whatever its
// shape. Anything else would make a surface that borrowed the screen stage an
// attachment the reader was never offering.
func TestPasteMsg_ASurfaceWithTheKeyboardStillGetsTheText(t *testing.T) {
	m := readyModel(t).
		WithModelSwitcher(func(string) {}).
		WithPricing(nil, "m1").
		WithModelOptions([]string{"m1", "m2"})
	m.input.SetValue("/model")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.state != statePick || m.picker == nil {
		t.Fatal("bare /model should open the picker")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m = updated.(Model)
	if !m.picker.Filtering {
		t.Fatal("/ should open the picker's filter row")
	}

	updated, _ = m.Update(tea.PasteMsg{Content: strings.Repeat("m2\n", 11)})
	next := updated.(Model)
	if len(next.attachments) != 0 {
		t.Fatalf("a paste into a filter row stages nothing, got %d", len(next.attachments))
	}
	if next.picker == nil || next.picker.Query == "" {
		t.Fatal("the paste should have reached the filter row")
	}
}

// Two pastes are two files, and the second one is not called paste-1.txt —
// the name is the handle `/paste drop` and `/paste show` are reached by.
func TestPasteMsg_NumbersEachStagedPaste(t *testing.T) {
	tall := strings.Repeat("goroutine 1 [running]:\n", 11)
	updated, _ := frameModel(t, 100, 40).Update(tea.PasteMsg{Content: tall})
	updated, _ = updated.(Model).Update(tea.PasteMsg{Content: tall})
	m := updated.(Model)
	if len(m.attachments) != 2 {
		t.Fatalf("two pastes should stage two attachments, got %d", len(m.attachments))
	}
	if m.attachments[1].Name != "paste-2.txt" {
		t.Fatalf("the second staged paste is %q, want paste-2.txt", m.attachments[1].Name)
	}
}

// The chip is the whole of what the reader knows about bytes that arrived
// with no name of their own, so it carries the height as well as the size —
// and only where there is a height to carry.
func TestAttachmentChips_TextCarriesItsHeightAndNothingElseDoes(t *testing.T) {
	m := frameModel(t, 100, 40)
	m.attachments = []provider.Attachment{
		{Kind: provider.AttachmentText, Name: "paste-1.txt", Data: []byte("a\nb\nc\n")},
		{Kind: provider.AttachmentImage, Name: "shot.png", Data: pngHeader},
	}
	chips := m.attachmentChips()
	if chips[0].Lines != 3 {
		t.Fatalf("the paste chip counts %d lines, want 3", chips[0].Lines)
	}
	if chips[1].Lines != 0 {
		t.Fatalf("a picture has no lines to report, got %d", chips[1].Lines)
	}
}

// A staged paste rides out as prompt text, wrapped so the model can tell it
// from the sentence it came with.
func TestPasteMsg_TheStagedPasteRidesAsText(t *testing.T) {
	updated, _ := frameModel(t, 100, 40).Update(
		tea.PasteMsg{Content: strings.Repeat("goroutine 1 [running]:\n", 11)})
	m := updated.(Model)
	got := m.attachments[0].AsText()
	if !strings.Contains(got, "goroutine 1 [running]:") || !strings.Contains(got, `name="paste-1.txt"`) {
		t.Fatalf("the paste should ride as named text, got %q", got)
	}
}

// Bytes win over the extension everywhere else in the attachment door, and
// here they must not: a paste is text or it is nothing. Staging one that
// sniffs as something else would hand the provider a part whose kind
// contradicts the name printed on the chip.
func TestPasteMsg_RefusesAPasteThatIsNotText(t *testing.T) {
	// A PDF header followed by enough lines to cross the threshold.
	updated, _ := frameModel(t, 100, 40).Update(tea.PasteMsg{
		Content: "%PDF-1.4\n" + strings.Repeat("1 0 obj\n", 12)})
	m := updated.(Model)
	if len(m.attachments) != 0 {
		t.Fatalf("a paste that is not text should stage nothing, got %v", m.attachments)
	}
	if notice := lastSystemText(m); !strings.Contains(notice, "not text") {
		t.Fatalf("the refusal should say why, got %q", notice)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("and the draft is left alone, got %q", got)
	}
}

// Bare `/paste show` takes the only thing it could open, so an image beside a
// paste is two things and has to be named. The old rule counted images alone
// and would have opened the screenshot without asking.
func TestPreview_BareShowRefusesAnImageBesideAPaste(t *testing.T) {
	m := stageText(t, stageImage(t, frameModel(t, 130, 40), "shot.png"), "paste-1.txt")
	updated, _ := m.runPaste([]string{"/paste", "show"})
	next := updated.(Model)
	if next.state == statePreview {
		t.Fatal("with an image and a paste staged, bare /paste show should ask for a name")
	}
	if notice := stripANSI(next.View().Content); !strings.Contains(notice, "needs a name") {
		t.Fatalf("and say so: %s", notice)
	}
}
