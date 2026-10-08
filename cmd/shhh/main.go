package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/cli"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// The status shhh leaves behind is the command tree's answer and not this
// function's: an unattended run says what happened in a code a script can act
// on, and everything else is a 1 (internal/cli.ExitCode).
func main() {
	// Inside a bubblewrap namespace confined to a host list, this binary is
	// the bridge that carries the namespace's loopback to the proxy. It is
	// answered before the keymap and the command tree because inside
	// containment their files are behind the mask, and nothing of them is
	// wanted.
	if len(os.Args) > 1 && os.Args[1] == sandbox.BridgeArg {
		os.Exit(sandbox.RunBridge(os.Args[2:]))
	}
	// The user's keymap moves a key before there is a command to answer one.
	// Every hint and every handler reads the register, so a file applied
	// after a program had started would be a screen offering keys it no
	// longer answers. A file that would leave a surface answering one
	// keystroke twice is refused whole and said on stderr, and the keyboard
	// shhh declared runs instead: a refusal nobody is told about is a session
	// quietly running a keyboard that is neither the file's nor shhh's.
	// A line naming a key shhh has since given up is read and does nothing,
	// and is named here for the same reason a refusal is.
	// How long the read took joins the session's first startup row (the
	// configuration's), since there is no command yet to hold it.
	keymapStarted := time.Now()
	err := keys.Load(config.KeymapPaths()...)
	cli.NoteKeymap(time.Since(keymapStarted))
	if err != nil {
		fmt.Fprintln(os.Stderr, "shhh: keybindings refused:", err)
	} else if dead := keys.Dead(); len(dead) > 0 {
		fmt.Fprintf(os.Stderr, "shhh: keybindings: %s names a key shhh no longer has; the line does nothing\n",
			strings.Join(dead, ", "))
	}
	if err := cli.Execute(context.Background()); err != nil {
		os.Exit(cli.ExitCode(err))
	}
}
