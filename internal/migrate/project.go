package migrate

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rfizzle/shhh/internal/project"
)

// legacyProjectFile detects a checkout whose `.shhh` is still the single
// context file rather than the directory the backlog, skills and plans live
// in, and plans moving it inside as the context file. It looks from dir
// upward, the way the context reader does, so the file it finds is the one a
// session there would have read.
func legacyProjectFile(dir string) (Pending, bool) {
	if dir == "" {
		return Pending{}, false
	}
	old, ok := findLegacyProjectFile(dir)
	if !ok {
		return Pending{}, false
	}
	target := filepath.Join(old, "project.md")
	p := Pending{
		Name:        "the project context file",
		Summary:     fmt.Sprintf("%s is a file; this version keeps a directory there", shortHome(old)),
		Consequence: "the project context in it is not read, and nothing that needs the directory — the backlog, project skills, saved plans — can be created",
		Steps:       []string{fmt.Sprintf("move %s to %s", shortHome(old), shortHome(target))},
		Apply: func() ([]string, error) {
			return moveProjectFileInside(old)
		},
	}
	return p, true
}

// findLegacyProjectFile walks up from dir for a `.shhh` that is a file. A
// directory of that name on the way is passed over rather than ending the
// walk: the old reader read the nearest file, and a session opened here
// would have read an ancestor's file straight through a nearer directory.
func findLegacyProjectFile(dir string) (string, bool) {
	for {
		p := filepath.Join(dir, project.StateDir)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// moveProjectFileInside turns the file at path into path/project.md. The
// file is set aside first so the directory can take its name, and the
// record says both moves in the order they happened.
func moveProjectFileInside(path string) ([]string, error) {
	aside := path + ".migrating"
	if _, err := os.Lstat(aside); err == nil {
		return nil, fmt.Errorf("%s is in the way", shortHome(aside))
	}
	if err := os.Rename(path, aside); err != nil {
		return nil, err
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		// Put the file back: a half-made move is worse than none.
		_ = os.Rename(aside, path)
		return nil, err
	}
	target := filepath.Join(path, "project.md")
	if err := os.Rename(aside, target); err != nil {
		_ = os.Remove(path)
		_ = os.Rename(aside, path)
		return nil, err
	}
	return []string{fmt.Sprintf("moved %s to %s", shortHome(path), shortHome(target))}, nil
}
