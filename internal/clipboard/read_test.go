package clipboard

import (
	"os/exec"
	"testing"
)

// PowerShell writes CRLF, and every reader downstream expects the newline it
// would have got from any other platform's tool.
func TestReadWindowsFoldsCRLF(t *testing.T) {
	old := output
	output = func(name string, args ...string) ([]byte, error) {
		if name != "powershell" {
			t.Errorf("windows reads through powershell, got %q", name)
		}
		return []byte("one\r\ntwo\r\n"), nil
	}
	t.Cleanup(func() { output = old })

	if got := readWindows().Text; got != "one\ntwo\n" {
		t.Errorf("got %q", got)
	}
}

// A reader that cannot reach the clipboard reports nothing rather than an
// error: a paste that found nothing is not a failed session.
func TestReadWindowsIsEmptyWhenPowerShellFails(t *testing.T) {
	old := output
	output = func(string, ...string) ([]byte, error) { return nil, exec.ErrNotFound }
	t.Cleanup(func() { output = old })

	if got := readWindows(); !got.Empty() {
		t.Errorf("got %+v", got)
	}
}
