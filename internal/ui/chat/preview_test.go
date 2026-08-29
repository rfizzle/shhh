package chat

// The staged attachment preview (
// docs/interface/surfaces.md#a-staged-attachment).

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// stageImage stages a real PNG — the fake header stagePNG writes is enough to
// be classified as an image and not enough to be drawn, which is the one
// thing this surface needs from it.
func stageImage(t *testing.T, m Model, name string) Model {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 32, 16))
	for y := range 16 {
		for x := range 32 {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x * 8), uint8(y * 16), 0x80, 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	a, err := attachment.FromBytes(name, buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	next, _ := m.stage([]provider.Attachment{a})
	return next.(Model)
}

func stageText(t *testing.T, m Model, name string) Model {
	t.Helper()
	a, err := attachment.FromBytes(name, []byte("# notes\n\nsomething\n"))
	if err != nil {
		t.Fatal(err)
	}
	next, _ := m.stage([]provider.Attachment{a})
	return next.(Model)
}

// The point of the surface: the chip says a file is staged, and this says
// which file it is. It is opened by naming the chip, because a chip has no
// key of its own.
func TestPreview_ShowDrawsTheStagedImage(t *testing.T) {
	// A test binary's stdout is not a terminal, so without this the picture
	// would draw at the rung a terminal with no colour gets — which is
	// its own test, below.
	was := components.Profile()
	components.SetProfile(colorprofile.ANSI256)
	t.Cleanup(func() { components.SetProfile(was) })

	m := stageImage(t, frameModel(t, 130, 40), "shot.png")
	updated, _ := m.runPaste([]string{"/paste", "show", "shot.png"})
	m = updated.(Model)
	if m.state != statePreview || m.preview == nil {
		t.Fatalf("state = %v, preview = %v", m.state, m.preview)
	}
	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "shot.png") || !strings.Contains(view, "32×16") {
		t.Fatalf("the card should name the file and its pixels:\n%s", view)
	}
	if !strings.ContainsRune(view, '▄') {
		t.Fatalf("no picture was drawn:\n%s", view)
	}
	// The way out is named, the way every other takeover names it.
	if !strings.Contains(view, "esc back") {
		t.Fatalf("the surface never says how to leave:\n%s", view)
	}
}

// Esc never destroys (invariant 3). The file the reader just looked at is
// still staged when they get back to the draft — this surface reads, and
// `/paste drop` is what removes.
func TestPreview_LeavingKeepsWhatIsStaged(t *testing.T) {
	m := stageImage(t, frameModel(t, 130, 40), "shot.png")
	updated, _ := m.runPaste([]string{"/paste", "show", "shot.png"})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateInput || m.preview != nil {
		t.Fatalf("esc should hand the pane back: state = %v, preview = %v", m.state, m.preview)
	}
	if len(m.attachments) != 1 {
		t.Fatalf("esc dropped the attachment: %d staged", len(m.attachments))
	}
	if !strings.Contains(stripANSI(m.View().Content), "▣ shot.png") {
		t.Fatal("the staged rail should be back with the chip on it")
	}
}

// `q` is the other spelling of the same thing, as it is on every full-screen
// viewer in shhh.
func TestPreview_QLeavesToo(t *testing.T) {
	m := stageImage(t, frameModel(t, 130, 40), "shot.png")
	updated, _ := m.runPaste([]string{"/paste", "show", "shot.png"})
	updated, _ = updated.(Model).Update(key('q'))
	if m := updated.(Model); m.state != stateInput || m.preview != nil {
		t.Fatalf("q should hand the pane back: state = %v", m.state)
	}
}

// Bare `/paste show` is worth having for one staged screenshot and worth
// refusing for two, because guessing which was meant is the mistake the
// surface exists to stop somebody making.
func TestPreview_BareShowTakesTheOnlyImage(t *testing.T) {
	m := stageImage(t, frameModel(t, 130, 40), "shot.png")
	updated, _ := m.runPaste([]string{"/paste", "show"})
	if m := updated.(Model); m.state != statePreview {
		t.Fatalf("one staged image should open bare: state = %v", m.state)
	}
	// A second image, and the name becomes required.
	two := stageImage(t, m, "other.png")
	updated, _ = two.runPaste([]string{"/paste", "show"})
	next := updated.(Model)
	if next.state == statePreview {
		t.Fatal("two staged images must not be guessed between")
	}
	notice := stripANSI(next.View().Content)
	if !strings.Contains(notice, "shot.png") || !strings.Contains(notice, "other.png") {
		t.Fatalf("the refusal should name what is staged:\n%s", notice)
	}
}

// The things it says no to, and the fact that each of them says why.
func TestPreview_RefusalsNameWhatIsThere(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, m Model) Model
		arg   string
		want  string
	}{
		{"nothing staged at all", func(_ *testing.T, m Model) Model { return m },
			"shot.png", "nothing is attached"},
		{"a name that is not staged", stageImageNamed("shot.png"),
			"other.png", "other.png is not attached"},
		{"a PDF, which shhh does not render", stagePDFNamed("spec.pdf"),
			"spec.pdf", "not an image or text"},
	} {
		m := c.setup(t, frameModel(t, 130, 40))
		updated, _ := m.runPaste([]string{"/paste", "show", c.arg})
		next := updated.(Model)
		if next.state == statePreview {
			t.Errorf("%s: opened the surface anyway", c.name)
			continue
		}
		if got := stripANSI(next.View().Content); !strings.Contains(got, c.want) {
			t.Errorf("%s: never says %q:\n%s", c.name, c.want, got)
		}
	}
}

func stageImageNamed(name string) func(*testing.T, Model) Model {
	return func(t *testing.T, m Model) Model { return stageImage(t, m, name) }
}

func stagePDFNamed(name string) func(*testing.T, Model) Model {
	return func(t *testing.T, m Model) Model {
		a, err := attachment.FromBytes(name, []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\n"))
		if err != nil {
			t.Fatal(err)
		}
		next, _ := m.stage([]provider.Attachment{a})
		return next.(Model)
	}
}

// A terminal with no colour to give still gets the picture, as density
// rather than hue. The surface is the one place in shhh whose
// content is colour, and a photograph is still the photograph without it.
func TestPreview_NoColourStillDrawsThePicture(t *testing.T) {
	was := components.Profile()
	components.SetProfile(colorprofile.Ascii)
	t.Cleanup(func() { components.SetProfile(was) })

	m := stageImage(t, frameModel(t, 130, 40), "shot.png")
	updated, _ := m.runPaste([]string{"/paste", "show", "shot.png"})
	view := stripANSI(updated.(Model).View().Content)
	if strings.ContainsRune(view, '▄') || strings.ContainsRune(view, '▀') {
		t.Fatalf("half-blocks on a terminal with no colour:\n%s", view)
	}
	if !strings.ContainsAny(view, "░▒▓█") {
		t.Fatalf("no picture was drawn at all:\n%s", view)
	}
}

// A file that is an image and will not decode still opens: "this is staged
// and shhh cannot read it" is a fact about the send that follows, and the
// card is where it is said.
func TestPreview_UndecodableImageOpensOntoTheReason(t *testing.T) {
	m := stagePNG(t, frameModel(t, 130, 40), "broken.png")
	updated, _ := m.runPaste([]string{"/paste", "show", "broken.png"})
	m = updated.(Model)
	if m.state != statePreview {
		t.Fatalf("state = %v, want the preview surface", m.state)
	}
	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "broken.png") {
		t.Fatalf("the card should still name the file:\n%s", view)
	}
	if !strings.Contains(strings.ToLower(view), "png") {
		t.Fatalf("the card should say why there is no picture:\n%s", view)
	}
}

// The completion menu offers what the surface will actually open, and
// nothing else: a name it would then decline is a name the reader found out
// about by typing.
func TestPreview_CompletionOffersWhatTheSurfaceOpens(t *testing.T) {
	m := stagePDFNamed("spec.pdf")(t, stageText(t, stageImage(t, frameModel(t, 130, 40), "shot.png"), "notes.md"))
	var names []string
	for _, o := range attachmentShowArgs(&m) {
		names = append(names, o.value)
	}
	if len(names) != 2 || names[0] != "shot.png" || names[1] != "notes.md" {
		t.Fatalf("/paste show offers %v, want the image and the text", names)
	}
	// The drop menu still offers everything: dropping a document is fine.
	if got := len(attachmentDropArgs(&m)); got != 3 {
		t.Fatalf("/paste drop offers %d, want all three staged files", got)
	}
}

// A staged paste opens as its own text: the reader's two questions about
// bytes with no file behind them — is this the right log, and is it all
// there — are both answered by looking at it.
func TestPreview_ShowDrawsAStagedPasteAsText(t *testing.T) {
	m := stageText(t, frameModel(t, 130, 40), "paste-1.txt")
	updated, _ := m.runPaste([]string{"/paste", "show", "paste-1.txt"})
	next := updated.(Model)
	if next.state != statePreview {
		t.Fatalf("state = %v, want the preview surface", next.state)
	}
	view := stripANSI(next.View().Content)
	for _, want := range []string{"paste-1.txt", "# notes", "something", "3 lines"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the card never says %q:\n%s", want, view)
		}
	}
}

// `dropbox.png` is a path and not a subcommand; so is anything else whose
// first word only starts with one.
func TestPreview_ShowIsAWholeWord(t *testing.T) {
	m := stageImage(t, frameModel(t, 130, 40), "shot.png")
	updated, _ := m.runPaste([]string{"/paste", "showreel.png"})
	if next := updated.(Model); next.state == statePreview {
		t.Fatal("showreel.png is a path, not /paste show")
	}
}

// Bare `/paste show` takes whatever is staged when only one thing is, which
// after this story is most often a paste rather than a screenshot.
func TestPreview_BareShowTakesALonePaste(t *testing.T) {
	m := stageText(t, frameModel(t, 130, 40), "paste-1.txt")
	updated, _ := m.runPaste([]string{"/paste", "show"})
	if next := updated.(Model); next.state != statePreview {
		t.Fatalf("state = %v, want the preview surface", next.state)
	}
	// Two it could have meant is still a refusal.
	two := stageImage(t, m, "shot.png")
	updated, _ = two.runPaste([]string{"/paste", "show"})
	if next := updated.(Model); next.state == statePreview {
		t.Fatal("with two staged, bare /paste show should ask for a name")
	}
}
