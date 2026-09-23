// Command gotest runs a `go test -json` command line and prints what a plain
// `go test` would have, followed by one line per distinct reason tests
// skipped for and how many did. `make test` runs the suite through it so the
// gate's test check can say which tests did not run.
// See docs/capabilities/testing.md#a-skipped-test-is-counted.
//
// Usage: go run ./scripts/gotest go test -json <packages>
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/rfizzle/shhh/internal/quality"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: gotest <go test -json command line>")
		os.Exit(2)
	}
	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gotest:", err)
		os.Exit(1)
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "gotest:", err)
		os.Exit(1)
	}
	_, failed, readErr := quality.ReadTestJSON(stdout, os.Stdout)
	waitErr := cmd.Wait()

	code := 0
	var exitErr *exec.ExitError
	switch {
	case errors.As(waitErr, &exitErr):
		code = exitErr.ExitCode()
	case waitErr != nil:
		fmt.Fprintln(os.Stderr, "gotest:", waitErr)
		code = 1
	}
	if readErr != nil {
		fmt.Fprintln(os.Stderr, "gotest:", readErr)
	}
	// The command's own status decides, but a stream that reported a
	// failure never passes, whatever the status said.
	if code == 0 && (failed || readErr != nil) {
		code = 1
	}
	os.Exit(code)
}
