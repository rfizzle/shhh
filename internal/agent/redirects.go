package agent

import (
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/rfizzle/shhh/internal/tools"
)

// Redirects is what a surface remembers about the commands it has answered
// with the editor: the lines, so that the same line sent again runs.
//
// The scope is the session. A turn would answer the same line again next
// turn, and the model that was told once "send it again and it runs" would be
// told again for a need the editor still cannot meet. The classifier is a
// reading and holds no state, so the memory is the surface's, handed in with
// Answers; the nil value answers nothing, which is a surface (a child, a
// scripted run) that runs every line as it always did.
// See docs/capabilities/coding-agent.md#the-built-in-tools-come-before-the-shell.
type Redirects struct {
	scope    tools.ShellScope
	mu       sync.Mutex
	answered map[string]bool
}

// NewRedirects is a session's memory for a workspace. Its scratch directories
// are the temporary ones the process names.
func NewRedirects(workspace string) *Redirects {
	scratch := []string{"/tmp", os.TempDir()}
	if t := os.Getenv("TMPDIR"); t != "" {
		scratch = append(scratch, t)
	}
	return &Redirects{scope: tools.ShellScope{Workspace: workspace, Scratch: scratch}, answered: map[string]bool{}}
}

// read is the classifier's question: the error to answer command with, or
// whether it runs after having been answered. Asking does not spend the
// answer, because a call is classified more than once on its way to a card.
func (r *Redirects) read(command string) (after bool, err error) {
	if r == nil || !tools.ShellEdit(command, r.scope) {
		return false, nil
	}
	line := strings.TrimSpace(command)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.answered[line] {
		return true, nil
	}
	return false, tools.ShellEditError{Command: line}
}

// Spent records that err, if it is the redirect, was given to the model as a
// call's result. The surface that answers calls says so, once, where it
// answers.
func (r *Redirects) Spent(err error) {
	var e tools.ShellEditError
	if r == nil || !errors.As(err, &e) {
		return
	}
	r.mu.Lock()
	r.answered[e.Command] = true
	r.mu.Unlock()
}
