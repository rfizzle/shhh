package clipboard

// Copying through the terminal instead of through a program on this machine.
//
// Every tool in copy.go copies to the clipboard of the machine shhh is
// running on. Over ssh that is the wrong machine: xclip on the server puts
// the text on the server's X display, where nobody is sitting, and on a
// server with no display it finds no tool at all and the copy is simply
// lost. OSC 52 runs the other way — the text is handed to the terminal
// emulator in front of the reader, down the same connection the screen is
// being drawn on — so wherever the terminal takes it, it is the copy that is
// right rather than the copy that is available.
//
// This file composes the sequence and says what may go in it. Writing it is
// somebody else's: shhh's programs speak to the terminal through one writer
// (docs/architecture.md#only-one-place-speaks-to-the-terminal), so what
// leaves here is a string for that writer rather than a write of its own.

import (
	"encoding/base64"

	"github.com/charmbracelet/x/ansi"
)

// Terminal is what Result.Tool says when the copy went to the terminal
// itself. The other values there name a program that was run; this one names
// the thing on the other end of the connection, because there is no program
// to name.
const Terminal = "the terminal"

// osc52Max is the most base64 one clipboard write may carry.
//
// The sequence carries no length and draws no reply, so a terminal that will
// not take this much does not say so — it truncates, and the reader pastes
// half a diff without ever being told. The protocol has no cap to read, so
// the number is the one taken from xterm's limit on how long a single control
// sequence may be: the only published bound, and the one the editors' OSC 52
// support is written against. Past it the copy goes to the tools, which have
// no bound at all.
const osc52Max = 74994

// OSC52 is the sequence that hands text to the terminal's own clipboard, and
// false where OSC 52 cannot carry it: nothing to copy, or more than one
// write holds. A false answer is not a failure — it is this path declining,
// and the caller falls back to the tools in copy.go.
func OSC52(text string) (string, bool) {
	if text == "" || base64.StdEncoding.EncodedLen(len(text)) > osc52Max {
		return "", false
	}
	return ansi.SetClipboard(ansi.SystemClipboard, text), true
}
