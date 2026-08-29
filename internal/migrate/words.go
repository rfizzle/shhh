package migrate

import (
	"fmt"
	"os"
	"strings"
)

// The words a Pending is written in. A migration's whole output is prose the
// doctor prints verbatim, so these live beside it rather than in the surface:
// the screen formats nothing.

// shortHome abbreviates a path under the home directory, the same way every
// path shhh prints is abbreviated.
func shortHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(path, home) {
		return path
	}
	return "~" + path[len(home):]
}

// countOf is a count with its noun, singular where it should be.
func countOf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// joinWords joins a list the way a sentence does, so a summary naming two
// directories reads as a sentence rather than as a slice.
func joinWords(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// bytesOf is a file size in the units a reader thinks in, the same reading
// the doctor's store row makes of one.
func bytesOf(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.0f kB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}
