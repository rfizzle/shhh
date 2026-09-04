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
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/rfizzle/shhh/internal/provider"
)

const (
	// CaseFile is the case definition inside a case directory, and
	// WorkspaceDir the fixture beside it that each attempt gets a copy of.
	CaseFile     = "case.toml"
	WorkspaceDir = "workspace"
	// TableFile is the labelled table beside the case file, for the kinds
	// that have no workspace. It is its own file because it is content: a
	// suite grows rows the way a test suite grows cases, and twenty of them
	// in the case file would bury the two keys that configure the case.
	TableFile = "table.toml"
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
	Name string `toml:"name"`
	// Kind selects the shape. Empty is a workspace case, which is what every
	// case written before the other kinds existed still means.
	Kind   string `toml:"kind"`
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

	kind := Kind(strings.TrimSpace(f.Kind))
	if kind == "" {
		kind = KindWorkspace
	}
	c := Case{Name: strings.TrimSpace(f.Name), Kind: kind, Dir: dir}
	if c.Name == "" {
		c.Name = filepath.Base(dir)
	}

	switch kind {
	case KindWorkspace:
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
		c.Workspace, c.Prompt, c.Check = ws, strings.TrimSpace(f.Prompt), f.Check
	case KindClassifier, KindSummary:
		rows, err := loadTable(filepath.Join(dir, TableFile), kind)
		if err != nil {
			return Case{}, err
		}
		c.Rows = rows
	default:
		return Case{}, fmt.Errorf("%s: kind %q is not one this suite knows — %s, %s or %s",
			path, f.Kind, KindWorkspace, KindClassifier, KindSummary)
	}

	c.Requires = f.Requires
	c.Skip = missingRequirement(f.Requires)
	return c, nil
}

// tableFile is a table on disk: nothing but rows, and no top-level key beside
// them. A scalar written after the first `[[row]]` would silently become part
// of that row, so the file has no place to write one and no order to get
// wrong.
type tableFile struct {
	Rows []rowFile `toml:"row"`
}

// rowFile is one row as written. The conversation is a list of lines each
// beginning `user:` or `assistant:`, which is the whole of what the evidence
// distinguishes, and keeps a turn a single string that can be written across
// several lines.
type rowFile struct {
	Name         string   `toml:"name"`
	Why          string   `toml:"why"`
	Expect       []string `toml:"expect"`
	Tool         string   `toml:"tool"`
	Arguments    string   `toml:"arguments"`
	CWD          string   `toml:"cwd"`
	Conversation []string `toml:"conversation"`
	Instruction  string   `toml:"instruction"`
	Plan         []string `toml:"plan"`
	Activity     []string `toml:"activity"`
	Assistant    string   `toml:"assistant"`
	Changes      string   `toml:"changes"`
	Alerts       []string `toml:"alerts"`
	Round        int      `toml:"round"`
	ElapsedSecs  int      `toml:"elapsed_seconds"`
	Previous     string   `toml:"previous"`
}

// loadTable reads a case's rows and refuses one that cannot be scored.
//
// A row that expects a word outside its kind's closed set, or that accepts
// every word in it, is not a strict row that fails: it is a row that measures
// nothing, and it would sit in the suite reporting a rate as though it did.
func loadTable(path string, kind Kind) ([]Row, error) {
	var f tableFile
	if _, err := toml.DecodeFile(path, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(f.Rows) == 0 {
		return nil, fmt.Errorf("%s: no rows — a %s case is its table", path, kind)
	}
	labels := kind.Labels()
	rows := make([]Row, 0, len(f.Rows))
	for i, rf := range f.Rows {
		name := strings.TrimSpace(rf.Name)
		if name == "" {
			name = fmt.Sprintf("row %d", i+1)
		}
		if len(rf.Expect) == 0 {
			return nil, fmt.Errorf("%s: %s: expect is required — it is the label the answer is compared against", path, name)
		}
		for _, e := range rf.Expect {
			if !slices.Contains(labels, e) {
				return nil, fmt.Errorf("%s: %s: expect %q is not one a %s answers — %s",
					path, name, e, kind, strings.Join(labels, ", "))
			}
		}
		if len(rf.Expect) >= len(labels) {
			return nil, fmt.Errorf("%s: %s: a row that accepts every answer measures nothing", path, name)
		}
		rows = append(rows, Row{
			Name:         name,
			Why:          strings.TrimSpace(rf.Why),
			Expect:       rf.Expect,
			Tool:         strings.TrimSpace(rf.Tool),
			Arguments:    strings.TrimSpace(rf.Arguments),
			CWD:          strings.TrimSpace(rf.CWD),
			Conversation: conversation(rf.Conversation),
			Instruction:  strings.TrimSpace(rf.Instruction),
			Plan:         rf.Plan,
			Activity:     rf.Activity,
			Assistant:    strings.TrimSpace(rf.Assistant),
			Changes:      strings.TrimSpace(rf.Changes),
			Alerts:       rf.Alerts,
			Round:        rf.Round,
			Elapsed:      time.Duration(rf.ElapsedSecs) * time.Second,
			Previous:     strings.TrimSpace(rf.Previous),
		})
	}
	return rows, nil
}

// conversation turns the written lines into the turns the evidence is drawn
// from. A line with no role prefix is the user's: that is the common case, and
// guessing wrong there costs a label on one turn rather than a row that
// silently vanishes from the evidence.
func conversation(lines []string) []provider.Message {
	var out []provider.Message
	for _, line := range lines {
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		role := provider.RoleUser
		switch {
		case strings.HasPrefix(strings.ToLower(text), "assistant:"):
			role, text = provider.RoleAssistant, strings.TrimSpace(text[len("assistant:"):])
		case strings.HasPrefix(strings.ToLower(text), "user:"):
			text = strings.TrimSpace(text[len("user:"):])
		}
		if text != "" {
			out = append(out, provider.Message{Role: role, Content: text})
		}
	}
	return out
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
