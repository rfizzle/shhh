package main

import (
	"context"
	"fmt"
	"os"

	"github.com/rfizzle/shhh/internal/cli"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// The status shhh leaves behind is the command tree's answer and not this
// function's: an unattended run says what happened in a code a script can act
// on, and everything else is a 1 (internal/cli.ExitCode).
func main() {
	// The user's keymap moves a key before there is a command to answer one.
	// Every hint and every handler reads the register, so a file applied
	// after a program had started would be a screen offering keys it no
	// longer answers. A file that would leave a surface answering one
	// keystroke twice is refused whole and said on stderr, and the keyboard
	// shhh declared runs instead: a refusal nobody is told about is a session
	// quietly running a keyboard that is neither the file's nor shhh's.
	if err := keys.Load(config.KeymapPaths()...); err != nil {
		fmt.Fprintln(os.Stderr, "shhh: keybindings refused:", err)
	}
	if err := cli.Execute(context.Background()); err != nil {
		os.Exit(cli.ExitCode(err))
	}
}
