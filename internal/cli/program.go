package cli

// Where a TUI starts and where styled bytes leave (S-155).
//
// Lip Gloss v2 has no renderer inside a Style: Render always emits the colour
// it was given, and what a terminal can show is settled twice — once when
// shhh resolves a palette token against a profile (components.Profile, the
// palette), and once by whatever writes the bytes out. The two have to be the
// same profile, or the palette and the screen are answering to different
// terminals. So every program is told the profile shhh already used, and
// every direct print goes through a writer that knows it.

import (
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// newProgram starts a Bubble Tea program with the colour profile shhh's
// palette was resolved against, rather than one Bubble Tea detects for
// itself. Two detectors agreeing is not the same as one decision.
func newProgram(model tea.Model, opts ...tea.ProgramOption) *tea.Program {
	return tea.NewProgram(model, append([]tea.ProgramOption{
		tea.WithColorProfile(components.Profile()),
	}, opts...)...)
}

// fprintStyled writes styled text to a plain destination — the exit banner,
// a provider failure card — through a writer that downsamples it to what the
// destination can show. Under v1 a non-terminal stdout dropped the whole
// terminal to Ascii and Style.Render emitted nothing at all, attributes
// included; v2 emits what it was given and leaves the decision here. A
// redirected stream therefore gets the text and nothing else, which is what
// it always got.
func fprintStyled(w *os.File, s string) {
	_, _ = fmt.Fprintln(styledWriter(w), s)
}

// styledWriter is the profile-aware writer for one stream. It is detected per
// stream rather than taken from components.Profile, because that one answers
// for stdout — the screen the TUIs draw on — and these two print to stderr.
func styledWriter(w *os.File) io.Writer {
	return colorprofile.NewWriter(w, os.Environ())
}
