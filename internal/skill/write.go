package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The writer. Skills had a reader and nothing that wrote one, because nothing
// proposed one: a skill was a file a person wrote by hand. What repeats across
// a checkout's sessions can now be offered as one — the same commands in the
// same order, session after session — so a draft is rendered here and written
// only once the person has read the file on a card and said yes
// (docs/capabilities/skills.md#a-skill-can-be-proposed-from-what-you-kept-doing).

// Draft is a skill not yet written: its name, the one line that says when to
// use it, and its steps in order.
type Draft struct {
	Name        string
	Description string
	Steps       []string
}

// DraftPath is where a draft named name is written under a checkout's root:
// shhh's own project skills directory, the first one the reader searches.
func DraftPath(root, name string) string {
	return filepath.Join(root, dirNames[0], name, "SKILL.md")
}

// Check reports what would make the draft a file the reader refuses or warns
// about, or nil: a name the specification does not allow, no description, or
// no steps. A description or a step on more than one line is refused too —
// the frontmatter reader takes a scalar to its line's end, and a second line
// would become a key nobody wrote.
func (d Draft) Check() error {
	if d.Name == "" {
		return errors.New("a skill needs a name")
	}
	if msg := checkName(d.Name); msg != "" {
		return errors.New(msg)
	}
	desc := strings.TrimSpace(d.Description)
	switch {
	case desc == "":
		return errors.New("a skill needs a description")
	case strings.ContainsAny(desc, "\r\n"):
		return errors.New("a skill's description is one line")
	case strings.Contains(desc, `\`):
		// The reader undoes an escaped quote and nothing else, so a
		// backslash written here would load as a different description
		// from the one the card showed.
		return errors.New("a skill's description cannot hold a backslash")
	case len(desc) > maxDescriptionLen:
		return fmt.Errorf("description is %d characters; the limit is %d", len(desc), maxDescriptionLen)
	}
	if len(d.Steps) == 0 {
		return errors.New("a skill needs at least one step")
	}
	for _, s := range d.Steps {
		if strings.TrimSpace(s) == "" || strings.ContainsAny(s, "\r\n") {
			return errors.New("each step is one line")
		}
	}
	return nil
}

// Render is the SKILL.md the draft would be: the frontmatter the reader
// requires, then the steps as a numbered list. The description is quoted, so
// a colon in it stays part of the value for any reader of the format; the one
// escape is a quote's, which is the one the reader undoes.
func (d Draft) Render() string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", d.Name)
	fmt.Fprintf(&b, "description: \"%s\"\n", strings.ReplaceAll(strings.TrimSpace(d.Description), `"`, `\"`))
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", d.Name)
	b.WriteString("Run these in order, and stop at the first that fails:\n\n")
	for i, s := range d.Steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, strings.TrimSpace(s))
	}
	return b.String()
}

// Write puts the draft under root and returns the file's path. A skill of
// that name already in the directory is refused rather than replaced: the
// card showed a new file, and a write over one the person wrote would be a
// change they never saw.
func Write(root string, d Draft) (string, error) {
	if err := d.Check(); err != nil {
		return "", err
	}
	path := DraftPath(root, d.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("a skill named %s is already there", d.Name)
		}
		return "", err
	}
	_, werr := f.WriteString(d.Render())
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path) // the write is the failure to report
		return "", werr
	}
	return path, nil
}
