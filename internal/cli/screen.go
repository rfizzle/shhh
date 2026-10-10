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
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
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
	// height is the terminal's rows, which the ground is laid down to: a
	// screen whose render stops short of the foot still stands on the ground
	// to the window's last row. Zero until the terminal says.
	height int
	answer func(done bool, result R) tea.Cmd
	// begin is what the program starts by doing, for a screen a command has
	// to feed — the doctor's first probe and its spinner. nil starts nothing.
	begin func() tea.Cmd
	// other handles the messages a command drives its own screen with. nil is
	// a screen that answers keys and nothing else, which is four of the five.
	other func(msg tea.Msg) tea.Cmd
	// armed is when the first press of the cancel chord came, waiting for
	// the second; zero is no press waiting.
	armed time.Time
}

// pressAgain is how long the cancel chord's first press waits for its
// second: the chat's own window, so the quit is the same two presses on
// every surface.
const pressAgain = 2 * time.Second

// newScreenModel hosts a screen at the width it is drawn at for the one frame
// before the terminal has said how wide it is.
func newScreenModel[R any](screen takeover[R], width int, answer func(bool, R) tea.Cmd) screenModel[R] {
	return screenModel[R]{screen: screen, width: width, answer: answer}
}

// Init asks the terminal what its own background is, as the chat does: under
// the auto theme the answer decides which table the screen draws with and
// whether its ground is painted, and a screen that never asked would stand on
// the dark table's ground on a light terminal the chat leaves alone
// (docs/interface/principles.md#a-colour-is-three-values-and-a-ground).
func (m screenModel[R]) Init() tea.Cmd {
	if m.begin == nil {
		return tea.RequestBackgroundColor
	}
	return tea.Batch(tea.RequestBackgroundColor, m.begin())
}

func (m screenModel[R]) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.screen.SetSize(msg.Width, msg.Height)
		return m, nil
	case tea.BackgroundColorMsg:
		// Every frame is rendered afresh from the palette, so there is no
		// cache to drop when the answer moves it.
		components.SetGround(msg.IsDark())
		return m, nil
	case tea.KeyPressMsg:
		// The cancel chord is the one destructive key and it means the same
		// on every surface: there is no run here to stop, so it is the quit,
		// and the quit is two presses of it in a row. It is answered here,
		// around the screen, because no screen's own way back is it — that
		// is esc (docs/interface/principles.md#esc-is-always-the-safe-answer).
		if keys.Match(msg, keys.Draft.Cancel) {
			now := time.Now()
			if !m.armed.IsZero() && now.Sub(m.armed) < pressAgain {
				return m, tea.Quit
			}
			m.armed = now
			return m, nil
		}
		m.armed = time.Time{}
		return m, m.answer(m.screen.Update(msg))
	}
	if m.other == nil {
		return m, nil
	}
	return m, m.other(msg)
}

// View is the frame: the screen, on the alt screen it takes over and on the
// theme's ground, through the one door every full screen takes
// (components.FullScreen), so leaving the chat for one of these does not
// change the colour of the window.
func (m screenModel[R]) View() tea.View {
	return components.FullScreen(components.GroundFrame(m.screen.View(m.width), m.width, m.height))
}
