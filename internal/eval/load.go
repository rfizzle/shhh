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
	// Facts is what a research case's write-up must contain, each one an
	// expression rather than a phrase (research.go).
	Facts []string `toml:"facts"`
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
	case KindResearch:
		if strings.TrimSpace(f.Prompt) == "" {
			return Case{}, fmt.Errorf("%s: prompt is required — it is the question", path)
		}
		site := filepath.Join(dir, SiteDir)
		if info, err := os.Stat(site); err != nil || !info.IsDir() {
			return Case{}, fmt.Errorf("%s: no %s/ directory — a research case brings the site it is answered from", dir, SiteDir)
		}
		if len(f.Facts) == 0 {
			return Case{}, fmt.Errorf("%s: facts is required — without it nothing decides whether the question was answered", path)
		}
		facts, err := compileFacts(path, f.Facts)
		if err != nil {
			return Case{}, err
		}
		c.Site, c.Prompt, c.Facts = site, strings.TrimSpace(f.Prompt), facts
	case KindClassifier, KindSummary, KindSteer, KindCompaction, KindSpawn, KindGate:
		rows, err := loadTable(filepath.Join(dir, TableFile), kind)
		if err != nil {
			return Case{}, err
		}
		c.Rows = rows
	default:
		return Case{}, fmt.Errorf("%s: kind %q is not one this suite knows — %s",
			path, f.Kind, strings.Join(kindNames(), ", "))
	}

	c.Requires = f.Requires
	c.Skip = missingRequirement(f.Requires)
	return c, nil
}

// kindNames is every kind a case file may name, in the order the shapes were
// added, for the sentence a misspelled one is refused with.
func kindNames() []string {
	return []string{string(KindWorkspace), string(KindResearch), string(KindClassifier),
		string(KindSummary), string(KindSteer), string(KindCompaction), string(KindSpawn), string(KindGate)}
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

	// What a scripted row states (mechanism.go): the model's part, and what
	// the mechanism's output has to carry.
	State     string   `toml:"state"`
	Reason    string   `toml:"reason"`
	Needs     []string `toml:"needs"`
	Rounds    int      `toml:"rounds"`
	Finished  bool     `toml:"finished"`
	Summary   string   `toml:"summary"`
	Window    int64    `toml:"window"`
	Role      string   `toml:"role"`
	Task      string   `toml:"task"`
	Paths     []string `toml:"paths"`
	WritePath string   `toml:"write_path"`
	WriteBody string   `toml:"write_body"`
	Reply     string   `toml:"reply"`
	Decline   bool     `toml:"decline"`
	Config    string   `toml:"config"`
	Suite     string   `toml:"suite"`
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
		row := Row{
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

			State:     strings.TrimSpace(rf.State),
			Reason:    strings.TrimSpace(rf.Reason),
			Needs:     rf.Needs,
			Rounds:    rf.Rounds,
			Finished:  rf.Finished,
			Summary:   strings.TrimSpace(rf.Summary),
			Window:    rf.Window,
			Role:      strings.TrimSpace(rf.Role),
			Task:      strings.TrimSpace(rf.Task),
			Paths:     rf.Paths,
			WritePath: strings.TrimSpace(rf.WritePath),
			WriteBody: rf.WriteBody,
			Reply:     strings.TrimSpace(rf.Reply),
			Decline:   rf.Decline,
			Config:    rf.Config,
			Suite:     strings.TrimSpace(rf.Suite),
		}
		if err := checkScriptedRow(path, kind, row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// checkScriptedRow refuses a scripted row that cannot measure anything.
//
// Every one of these is a row that would otherwise sit in the suite
// reporting a rate: a steer row with no reading states nothing to act on, a
// compaction or spawn row with no `needs` asserts only that the mechanism
// ran, and a gate row with no config measures a workspace nobody set up. The
// reader is editing the file, so the sentence says which key is missing and
// what it is for.
func checkScriptedRow(path string, kind Kind, row Row) error {
	switch kind {
	case KindSteer:
		if row.State == "" {
			return fmt.Errorf("%s: %s: state is required — it is the reading the policy acts on", path, row.Name)
		}
		if !slices.Contains(KindSummary.Labels(), row.State) {
			return fmt.Errorf("%s: %s: state %q is not a reading — %s",
				path, row.Name, row.State, strings.Join(KindSummary.Labels(), ", "))
		}
		if row.Instruction == "" {
			return fmt.Errorf("%s: %s: instruction is required — it is what a steer quotes back", path, row.Name)
		}
	case KindCompaction:
		if len(row.Conversation) == 0 {
			return fmt.Errorf("%s: %s: conversation is required — it is what the step is run over", path, row.Name)
		}
		if row.Window <= 0 {
			return fmt.Errorf("%s: %s: window is required — nothing crosses a line that is not there", path, row.Name)
		}
		if len(row.Needs) == 0 {
			return fmt.Errorf("%s: %s: needs is required — it is what the next round has to still have", path, row.Name)
		}
	case KindSpawn:
		if row.Role == "" || row.Task == "" {
			return fmt.Errorf("%s: %s: role and task are required — they are the spawn call", path, row.Name)
		}
		if len(row.Needs) == 0 {
			return fmt.Errorf("%s: %s: needs is required — it is what the parent has to be told", path, row.Name)
		}
	case KindGate:
		if strings.TrimSpace(row.Config) == "" {
			return fmt.Errorf("%s: %s: config is required — it is the workspace the gate reads", path, row.Name)
		}
	}
	return nil
}

// conversation turns the written lines into the turns the evidence is drawn
// from. A line with no role prefix is the user's: that is the common case, and
// guessing wrong there costs a label on one turn rather than a row that
// silently vanishes from the evidence.
//
// `system:` and `tool:` are here for the scripted kinds, which are run over a
// conversation rather than shown one: a window recovery keeps the system
// message and elides the oldest tool results, and neither can be measured
// against a transcript that has neither in it.
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
		case strings.HasPrefix(strings.ToLower(text), "system:"):
			role, text = provider.RoleSystem, strings.TrimSpace(text[len("system:"):])
		case strings.HasPrefix(strings.ToLower(text), "tool:"):
			role, text = provider.RoleTool, strings.TrimSpace(text[len("tool:"):])
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
