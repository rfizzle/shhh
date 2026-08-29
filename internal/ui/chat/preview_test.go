package chat

// The staged image preview (S-158,
// docs/interface/surfaces.md#a-staged-picture).

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
// key of its own (§12g).
func TestPreview_ShowDrawsTheStagedImage(t *testing.T) {
	// A test binary's stdout is not a terminal, so without this the picture
	// would draw at the rung a terminal with no colour gets (§10e) — which is
	// its own test, below.
	was := components.Profile()
	components.SetProfile(colorprofile.ANSI256)
	t.Cleanup(func() { components.SetProfile(was) })

	m := stageImage(t, frameModel(t, 130, 40), "shot.png")
	updated, _ := m.runPaste([]string{"/paste", "show", "shot.png"})
	m = updated.(Model)
	if m.state != statePicture || m.picture == nil {
		t.Fatalf("state = %v, picture = %v", m.state, m.picture)
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
	if m.state != stateInput || m.picture != nil {
		t.Fatalf("esc should hand the pane back: state = %v, picture = %v", m.state, m.picture)
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
	if m := updated.(Model); m.state != stateInput || m.picture != nil {
		t.Fatalf("q should hand the pane back: state = %v", m.state)
	}
}

// Bare `/paste show` is worth having for one staged screenshot and worth
// refusing for two, because guessing which was meant is the mistake the
// surface exists to stop somebody making.
func TestPreview_BareShowTakesTheOnlyImage(t *testing.T) {
	m := stageImage(t, frameModel(t, 130, 40), "shot.png")
	updated, _ := m.runPaste([]string{"/paste", "show"})
	if m := updated.(Model); m.state != statePicture {
		t.Fatalf("one staged image should open bare: state = %v", m.state)
	}
	// A second image, and the name becomes required.
	two := stageImage(t, m, "other.png")
	updated, _ = two.runPaste([]string{"/paste", "show"})
	next := updated.(Model)
	if next.state == statePicture {
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
		{"a file that is not an image", stageTextNamed("notes.md"),
			"notes.md", "not an image"},
	} {
		m := c.setup(t, frameModel(t, 130, 40))
		updated, _ := m.runPaste([]string{"/paste", "show", c.arg})
		next := updated.(Model)
		if next.state == statePicture {
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

func stageTextNamed(name string) func(*testing.T, Model) Model {
	return func(t *testing.T, m Model) Model { return stageText(t, m, name) }
}

// A terminal with no colour to give still gets the picture, as density
// rather than hue (§10e, §10f). The surface is the one place in shhh whose
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
	if m.state != statePicture {
		t.Fatalf("state = %v, want the picture surface", m.state)
	}
	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "broken.png") {
		t.Fatalf("the card should still name the file:\n%s", view)
	}
	if !strings.Contains(strings.ToLower(view), "png") {
		t.Fatalf("the card should say why there is no picture:\n%s", view)
	}
}

// The completion menu offers the staged images and only those (S-079): a
// name it would then decline is a name the reader found out about by typing.
func TestPreview_CompletionOffersStagedImagesOnly(t *testing.T) {
	m := stageText(t, stageImage(t, frameModel(t, 130, 40), "shot.png"), "notes.md")
	var names []string
	for _, o := range attachmentShowArgs(&m) {
		names = append(names, o.value)
	}
	if len(names) != 1 || names[0] != "shot.png" {
		t.Fatalf("/paste show offers %v, want just the image", names)
	}
	// The drop menu still offers everything: dropping a document is fine.
	if got := len(attachmentDropArgs(&m)); got != 2 {
		t.Fatalf("/paste drop offers %d, want both staged files", got)
	}
}

// `dropbox.png` is a path and not a subcommand; so is anything else whose
// first word only starts with one.
func TestPreview_ShowIsAWholeWord(t *testing.T) {
	m := stageImage(t, frameModel(t, 130, 40), "shot.png")
	updated, _ := m.runPaste([]string{"/paste", "showreel.png"})
	if next := updated.(Model); next.state == statePicture {
		t.Fatal("showreel.png is a path, not /paste show")
	}
}
