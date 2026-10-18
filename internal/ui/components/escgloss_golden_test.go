package components

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	goldenANSI = regexp.MustCompile("(?:\x1b|␛)\\[[0-9;]*[A-Za-z]")
	// goldenEscGloss is a drawn `[esc]` segment and the words beside it: the
	// gloss runs to the column gap, the pane edge or the end of the line.
	goldenEscGloss = regexp.MustCompile(`\[esc\] (.+?)(?:\s{2,}|\s[│·─╯╮]|$)`)
)

// TestGoldens_EveryDrawnEscGlossIsOnTheList reads the rendered goldens of every
// surface and holds each `[esc]` segment to the closed list, wherever it was
// built. AuditEscGlosses only hears the glosses painted through the renderer;
// this is the cross-check that a string drawn another way cannot slip by.
func TestGoldens_EveryDrawnEscGlossIsOnTheList(t *testing.T) {
	seen := 0
	for _, dir := range []string{"testdata/golden", "../testdata/golden", "../chat/testdata/golden"} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			// The help text and the key list describe every key in a sentence of
			// their own; they are the register spoken aloud, not a foot.
			if base := filepath.Base(path); strings.HasPrefix(base, "help-") || strings.HasPrefix(base, "key-list") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, line := range strings.Split(goldenANSI.ReplaceAllString(string(raw), ""), "\n") {
				for _, m := range goldenEscGloss.FindAllStringSubmatch(line, -1) {
					seen++
					if !EscGlossOnList(strings.TrimSpace(m[1])) {
						t.Errorf("%s: [esc] %q is off the closed list", path, m[1])
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if seen == 0 {
		t.Fatal("no [esc] segment found in any golden: the scan has stopped reading them")
	}
}
