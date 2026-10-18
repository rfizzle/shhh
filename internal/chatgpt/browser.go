package chatgpt

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// openBrowser asks the desktop to open the authorize page. It is the one
// program this package starts, and it is started for the person rather than
// for the model, which is why it does not take the containment wrap an
// assistant's command takes: it is the same kind of start as the desktop
// opener the report viewer uses. It opens the authorize page and nothing
// else, so a caller handing it any other address is refused rather than
// turned into a way to launch a URL. A test replaces it.
var openBrowser = func(u string) error {
	if !strings.HasPrefix(u, AuthorizeURL+"?") {
		return errors.New("refusing to open anything but the ChatGPT sign-in page")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// The opener hands the URL to the desktop and exits, and on a machine
	// with no desktop it exits non-zero — xdg-open on a server starts fine
	// and then finds no browser. Its exit is waited for briefly so that
	// case prints the address instead of waiting on a page nobody can see;
	// one still running after that is a browser it started in the
	// foreground, and is reaped in the background.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		return err
	case <-time.After(openerGrace):
		return nil
	}
}

// openerGrace is how long the opener is given to fail. open and xdg-open
// return within a few hundred milliseconds either way.
const openerGrace = 3 * time.Second
