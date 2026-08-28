package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
// the surface says what is pending (§12g). It names the file rather than
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
	if !strings.Contains(stripANSI(m.View()), "▣ shot.png") {
		t.Fatalf("the staged rail should reach the surface:\n%s", stripANSI(m.View()))
	}
}

// The rail is a row like any other, so the viewport gives one up for it —
// otherwise the frame grows past the bottom of the terminal (§12e).
func TestAttachments_StagedRailCostsTheViewportALine(t *testing.T) {
	m := frameModel(t, 130, 40)
	base := m.viewport.Height
	m = stagePNG(t, m, "shot.png")
	m.syncViewport()
	if m.viewport.Height != base-1 {
		t.Fatalf("the staged rail must shrink the viewport (%d -> %d)", base, m.viewport.Height)
	}
}

// Attached, the keyboard is pointed at a child and ctrl+v is a textarea key
// again, so the orchestrator's staging area is not what is on screen (§12d).
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

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
	view := stripANSI(next.View())
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

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
