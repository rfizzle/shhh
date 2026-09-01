package eval

// Reading a suite off disk.
//
// A case is a directory, which is the only shape that lets the fixture be
// ordinary files: a task is only realistic if its workspace is a checkout
// somebody could open, and a workspace embedded in a config file is neither
// editable nor runnable on its own.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	// CaseFile is the case definition inside a case directory, and
	// WorkspaceDir the fixture beside it that each attempt gets a copy of.
	CaseFile     = "case.toml"
	WorkspaceDir = "workspace"
)

// caseFile is the on-disk form. It is a separate type from Case because what
// is written down and what is loaded are different things: the file may leave
// the name out, and the loaded case never has it missing.
//
// Every key is top level and none of them is a table, which is not a
// stylistic choice: a TOML table swallows every key written after it, so a
// `requires` added below a `[check]` section silently becomes part of it. A
// flat file has no order to get wrong.
type caseFile struct {
	Name   string `toml:"name"`
	Prompt string `toml:"prompt"`
	// Check is the argv whose exit status is the case's verdict.
	Check []string `toml:"check"`
	// Requires names commands the case cannot run without. A case that needs
	// a toolchain this machine lacks is skipped and says so, because failing
	// it would blame the agent for the machine.
	Requires []string `toml:"requires"`
}

// Load reads every case directory under root, in name order so a report reads
// the same way twice.
//
// A directory without a case file is not a case and is not an error: a suite
// keeps shared fixtures and notes beside its cases, and refusing to load
// because of a README would be a rule nobody could satisfy.
func Load(root string) ([]Case, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("cannot read the suite: %w", err)
	}
	var cases []Case
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if _, statErr := os.Stat(filepath.Join(dir, CaseFile)); statErr != nil {
			continue
		}
		c, loadErr := LoadCase(dir)
		if loadErr != nil {
			return nil, loadErr
		}
		cases = append(cases, c)
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].Name < cases[j].Name })
	if len(cases) == 0 {
		return nil, fmt.Errorf("no cases in %s: a case is a directory holding %s", root, CaseFile)
	}
	return cases, nil
}

// LoadCase reads one case directory. Every validation failure names the file,
// because the reader is editing it.
func LoadCase(dir string) (Case, error) {
	path := filepath.Join(dir, CaseFile)
	var f caseFile
	if _, err := toml.DecodeFile(path, &f); err != nil {
		return Case{}, fmt.Errorf("%s: %w", path, err)
	}
	if strings.TrimSpace(f.Prompt) == "" {
		return Case{}, fmt.Errorf("%s: prompt is required — it is the task", path)
	}
	if len(f.Check) == 0 {
		return Case{}, fmt.Errorf("%s: check is required — without it nothing decides whether the task was done", path)
	}
	ws := filepath.Join(dir, WorkspaceDir)
	if info, err := os.Stat(ws); err != nil || !info.IsDir() {
		return Case{}, fmt.Errorf("%s: no %s/ directory — a case needs a workspace to work in", dir, WorkspaceDir)
	}

	c := Case{
		Name:      strings.TrimSpace(f.Name),
		Dir:       dir,
		Workspace: ws,
		Prompt:    strings.TrimSpace(f.Prompt),
		Check:     f.Check,
	}
	if c.Name == "" {
		c.Name = filepath.Base(dir)
	}
	c.Skip = missingRequirement(f.Requires)
	return c, nil
}

// lookPath is the PATH probe, a variable so a test can decide what this
// machine has without depending on what it really has.
var lookPath = defaultLookPath

// missingRequirement is why the case cannot run here, or "" when it can.
func missingRequirement(requires []string) string {
	var missing []string
	for _, r := range requires {
		if r = strings.TrimSpace(r); r != "" && !lookPath(r) {
			missing = append(missing, r)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "not on PATH: " + strings.Join(missing, ", ")
}
