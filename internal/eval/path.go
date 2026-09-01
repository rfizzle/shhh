package eval

import "os/exec"

// defaultLookPath is the real PATH probe behind lookPath (load.go).
func defaultLookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
