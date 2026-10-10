package project

// The checkout read once. A session opening used to ask git about the tree
// once per thing that wanted to know: the start screen's survey for its
// branch and its dirty count, the reading that tells a turn the tree moved
// for its root and its baseline, and the changeset's tracker for whether
// there was a repository at all. Each asked at a slightly different moment,
// and the status calls among them were most of what the person waited for
// before the first request could go. One reading now answers all of them,
// and a reader that needs a fresher view takes a new one rather than running
// git itself.
// See docs/capabilities/sessions-and-memory.md#startup-and-waits-are-timed.

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/hostgit"
)

// Checkout is the tree at one moment: the root of the work tree it was read
// in, the commit, the branch, and every path git status names with its
// two-character state.
type Checkout struct {
	// Top is the root of the work tree, with symlinks resolved, and empty
	// where the directory was not inside one.
	Top string
	// Head is the commit HEAD points at, empty before the first commit.
	// Branch is the branch checked out, empty on a detached HEAD, which
	// Detached marks.
	Head     string
	Branch   string
	Detached bool
	// Status is every path git status names, keyed relative to Top. It is
	// nil where the status could not be read, which Err says why.
	Status map[string]string
	// Err is why a work tree's status could not be read: a broken index, a
	// repository removed from under the reading. It is nil outside a work
	// tree, which an empty Top already says.
	Err error
	// Took is how long the reading took, for the startup phase it is filed
	// under.
	Took time.Duration
}

// Repo reports whether the reading found a work tree.
func (c Checkout) Repo() bool { return c.Top != "" }

// Read reports whether the reading holds the tree's status, and so can stand
// as a baseline to compare a later reading with.
func (c Checkout) Read() bool { return c.Top != "" && c.Err == nil }

// ReadCheckout reads the work tree dir is in: its root, then its status at
// the root. Paths in a status come back relative to the directory git ran
// in, which is why the second call is run at the root.
//
// Where the status will not read, the commit and the branch are asked for
// on their own, so a checkout with a broken index is still named by its
// branch rather than read as no repository at all. That costs the calls
// only on the way that fails.
func ReadCheckout(dir string) Checkout {
	started := time.Now()
	out, err := gitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return Checkout{Took: time.Since(started)}
	}
	top := strings.TrimSpace(out)
	if resolved, err := filepath.EvalSymlinks(top); err == nil {
		top = resolved
	}
	c, err := ReadStatus(top)
	c.Top, c.Err = top, err
	if err != nil {
		c.Head = Head(top)
		if b, err := gitOutput(top, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
			if b = strings.TrimSpace(b); b == "HEAD" {
				c.Detached = true
			} else {
				c.Branch = b
			}
		}
	}
	c.Took = time.Since(started)
	return c
}

// ReadStatus reads the tree at top in one git call: porcelain v2 with the
// branch header, NUL-terminated so a path is never quoted. top is the root
// of the work tree, which the reading that compares two of these already
// holds and so does not ask for again.
//
// The untracked mode is asked for rather than left to the checkout. A person
// who set `status.showUntrackedFiles=all` for their own reading of a tree has
// said nothing about this one, and the setting is not a small difference: git
// names every file under a new directory where the default names the
// directory once, which is how one notice came to count 5,825 cache files one
// by one. Every reader of the tree reads it the same way in every checkout.
func ReadStatus(top string) (Checkout, error) {
	out, err := gitOutput(top, "status", "--porcelain=v2", "--branch", "--untracked-files=normal", "-z")
	if err != nil {
		return Checkout{}, err
	}
	return ParseStatus(out), nil
}

// ParseStatus reads `git status --porcelain=v2 --branch -z`. Entry kinds:
// `1` ordinary, `2` renamed or copied (the original path follows as its own
// field), `u` unmerged, `?` untracked, `!` ignored; `#` lines are headers.
func ParseStatus(out string) Checkout {
	c := Checkout{Status: map[string]string{}}
	fields := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if f == "" {
			continue
		}
		switch {
		case strings.HasPrefix(f, "# branch.oid "):
			if oid := strings.TrimPrefix(f, "# branch.oid "); oid != "(initial)" {
				c.Head = oid
			}
		case strings.HasPrefix(f, "# branch.head "):
			if name := strings.TrimPrefix(f, "# branch.head "); name == "(detached)" {
				c.Detached = true
			} else {
				c.Branch = name
			}
		case strings.HasPrefix(f, "# "):
		case strings.HasPrefix(f, "1 "):
			if parts := strings.SplitN(f, " ", 9); len(parts) == 9 {
				c.Status[parts[8]] = parts[1]
			}
		case strings.HasPrefix(f, "2 "):
			if parts := strings.SplitN(f, " ", 10); len(parts) == 10 {
				c.Status[parts[9]] = parts[1]
			}
			i++ // the original path rides in the next field
		case strings.HasPrefix(f, "u "):
			if parts := strings.SplitN(f, " ", 11); len(parts) == 11 {
				c.Status[parts[10]] = parts[1]
			}
		case strings.HasPrefix(f, "? "):
			c.Status[f[2:]] = "??"
			// `!` (ignored) entries are not shown unless asked for, and are
			// not workspace changes when they are.
		}
	}
	return c
}

// gitOutput runs git and returns its standard output. A failure carries
// git's own first line of complaint where it gave one, since "exit status
// 128" is not a reason anybody can act on, and the reading that tells a turn
// the tree would not read says this reason.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := hostgit.Command(context.Background(), dir, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		if line, _, _ := strings.Cut(strings.TrimSpace(errOut.String()), "\n"); line != "" {
			return "", errors.New(line)
		}
		return "", err
	}
	return out.String(), nil
}
