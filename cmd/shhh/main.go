package main

import (
	"context"
	"os"

	"github.com/rfizzle/shhh/internal/cli"
)

// The status shhh leaves behind is the command tree's answer and not this
// function's: an unattended run says what happened in a code a script can act
// on, and everything else is a 1 (internal/cli.ExitCode).
func main() {
	if err := cli.Execute(context.Background()); err != nil {
		os.Exit(cli.ExitCode(err))
	}
}
