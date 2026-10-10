package run

// A lane's copy of its item.
//
// The item file lives in the checkout, and a lane's stages work in a copy of
// it that has no way to write outside: a stage told to tick the real file
// would be refused after the yes, and the archive would read as if nothing
// had been satisfied. So the lane ticks a copy of the item inside its own
// copy, and the runner applies those ticks to the real file at the finish.
// See docs/capabilities/todo.md#a-sprint-can-work-several-items-at-once.

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/rfizzle/shhh/internal/todo"
)

// ItemCopyPath is where a lane's copy of an item is kept inside the lane's
// copy of the checkout: under the run's scratch directory, which a run never
// stages.
func ItemCopyPath(tree, slug string) string {
	return filepath.Join(Dir(tree), slug+".item.md")
}

// CopyItem writes the item as the checkout holds it to its place in the
// lane's copy and answers with the path.
func CopyItem(tree string, it todo.Item) (string, error) {
	data, err := os.ReadFile(it.Path)
	if err != nil {
		return "", err
	}
	to := ItemCopyPath(tree, it.Slug)
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return "", err
	}
	return to, os.WriteFile(to, data, 0o644)
}

// ApplyTicks ticks, in the real item file, each checkbox the copy ticked
// that the real file still holds open, and answers with how many it ticked.
// A line is matched by its text after the box, in order, so two criteria
// that read alike are ticked in the order they stand; a line the real file
// no longer holds, or already holds ticked, is left alone, and every other
// line of the real file is written back as it was.
func ApplyTicks(real, copied string) (int, error) {
	data, err := os.ReadFile(copied)
	if err != nil {
		return 0, err
	}
	ticked := map[string]int{}
	for _, l := range strings.Split(string(data), "\n") {
		if _, text, done, ok := checkbox(l); ok && done {
			ticked[text]++
		}
	}
	if len(ticked) == 0 {
		return 0, nil
	}
	raw, err := os.ReadFile(real)
	if err != nil {
		return 0, err
	}
	lines := strings.Split(string(raw), "\n")
	n := 0
	for i, l := range lines {
		at, text, done, ok := checkbox(l)
		if !ok || done || ticked[text] == 0 {
			continue
		}
		ticked[text]--
		lines[i] = l[:at] + "[x]" + l[at+3:]
		n++
	}
	if n == 0 {
		return 0, nil
	}
	return n, os.WriteFile(real, []byte(strings.Join(lines, "\n")), 0o644)
}

// checkbox reads a list line that is a checkbox: where its box starts, the
// text after it, and whether the box is ticked.
func checkbox(line string) (at int, text string, done, ok bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if len(trimmed) < 5 || (trimmed[0] != '-' && trimmed[0] != '*') || trimmed[1] != ' ' || trimmed[2] != '[' || trimmed[4] != ']' {
		return 0, "", false, false
	}
	switch trimmed[3] {
	case ' ':
	case 'x', 'X':
		done = true
	default:
		return 0, "", false, false
	}
	return len(line) - len(trimmed) + 2, strings.TrimSpace(trimmed[5:]), done, true
}
