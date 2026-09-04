package cli

// The Bubble Tea host every take-over screen gets
// (docs/interface/surfaces.md#the-supporting-screens).
//
// Five commands put a full-screen component on the alt screen, and every one
// of them had written the same model: the terminal's size into a width and a
// row budget, a key press into the component's Update, and the render into a
// view that asks for the alt screen. What actually differs between them is
// what the answer means — a rating written down, a setting staged, a command
// run once the terminal has been given back — and that is the one thing the
// host takes as an argument.
//
// The commands' own state stays in their own types, behind a pointer the
// answer closes over. It has to: a Bubble Tea model is a value, so a run's
// tallies would otherwise be read off whichever copy the program happened to
// return, and every one of these commands has something to say after the
// screen has closed.

import (
	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// takeover is what a component takes and gives while it holds the terminal:
// a rectangle, a key, and a render.
type takeover[R any] interface {
	components.Sized
	components.Keyed[R]
	View(width int) string
}

// screenModel hosts one take-over screen. answer is called with everything
// the screen's Update returned and gives back the command that earned —
// tea.Quit ends the program, and nil leaves it up.
type screenModel[R any] struct {
	screen takeover[R]
	width  int
	answer func(done bool, result R) tea.Cmd
	// begin is what the program starts by doing, for a screen a command has
	// to feed — the doctor's first probe and its spinner. nil starts nothing.
	begin func() tea.Cmd
	// other handles the messages a command drives its own screen with. nil is
	// a screen that answers keys and nothing else, which is four of the five.
	other func(msg tea.Msg) tea.Cmd
}

// newScreenModel hosts a screen at the width it is drawn at for the one frame
// before the terminal has said how wide it is.
func newScreenModel[R any](screen takeover[R], width int, answer func(bool, R) tea.Cmd) screenModel[R] {
	return screenModel[R]{screen: screen, width: width, answer: answer}
}

func (m screenModel[R]) Init() tea.Cmd {
	if m.begin == nil {
		return nil
	}
	return m.begin()
}

func (m screenModel[R]) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.screen.SetSize(msg.Width, msg.Height)
		return m, nil
	case tea.KeyPressMsg:
		return m, m.answer(m.screen.Update(msg))
	}
	if m.other == nil {
		return m, nil
	}
	return m, m.other(msg)
}

// View is the frame: the screen, on the alt screen it takes over. In v2 that
// state is a field on the view rather than an option the host passes to
// NewProgram.
func (m screenModel[R]) View() tea.View {
	v := tea.NewView(m.screen.View(m.width))
	v.AltScreen = true
	return v
}
