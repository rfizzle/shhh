// Package run is the backlog runner's state machine: the stages an item
// goes through, what each one asks the model for, how its answer is read,
// and which stage comes next. It knows nothing about a terminal or a
// provider. The front-end sends the prompt a stage hands it, gives back the
// text the model produced, and is told what to do next — which is what
// keeps the gates in code rather than in the model's judgement.
// See docs/capabilities/todo.md#a-run-is-turns-with-gates-between-them.
package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/todo"
)

// Stage is where a run is.
type Stage string

const (
	// StageResearch reads the code in plan mode and answers with a plan, a
	// size and any open questions.
	StageResearch Stage = "research"
	// StageImplement carries the plan out.
	StageImplement Stage = "implement"
	// StageVerify runs the item's tests and the project's checks. No model.
	StageVerify Stage = "verify"
	// StageReview reads the change against the item and the project's
	// conventions and answers clean or with findings.
	StageReview Stage = "review"
	// StageRemediate fixes what verify or review found, then goes back to
	// verify.
	StageRemediate Stage = "remediate"
	// StageCommit writes the commit message and the report.
	StageCommit Stage = "commit"
	// StageDone is a run that archived its item.
	StageDone Stage = "done"
	// StageBlocked is a run that stopped with evidence.
	StageBlocked Stage = "blocked"
)

// Action is what the front-end does next.
type Action int

const (
	// ActionPrompt: send Prompt to the model in Mode and report the text.
	ActionPrompt Action = iota
	// ActionVerify: run the item's tests and the checks; report the outcome.
	ActionVerify
	// ActionCommit: stage the run's paths and commit with Message; then
	// archive the item with Report.
	ActionCommit
	// ActionBlocked: the run is over with State.Blocked as the evidence.
	ActionBlocked
	// ActionDone: the run is over; the item is archived.
	ActionDone
)

// Mode is the permission mode a prompt is sent in.
type Mode string

const (
	ModePlan Mode = "plan"
	ModeAuto Mode = "auto"
)

// Step is what the front-end is handed on every transition.
type Step struct {
	Action Action
	Stage  Stage
	Mode   Mode
	Prompt string
	// Shown is the one-line label the transcript shows in place of the
	// prompt, so a stage reads as a stage rather than as a wall of text.
	Shown string
}

// Rounds is how many remediation rounds a size gets before the run blocks.
// See docs/capabilities/todo.md#a-run-is-turns-with-gates-between-them.
func Rounds(size todo.Size) int {
	if size == todo.SizeS {
		return 1
	}
	return 2
}

// State is a run's checkpoint: everything the machine needs to continue
// from the start of its current stage. It is written after every
// transition so a session that dies mid-run can say where it was.
type State struct {
	Slug    string    `json:"slug"`
	Session string    `json:"session"`
	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`
	Stage   Stage     `json:"stage"`
	// Turn is the session turn the run started at; every changeset turn
	// from here on belongs to it.
	Turn int `json:"turn"`
	// PrevMode is the session's mode before the run, restored after.
	PrevMode string `json:"prev_mode"`

	SizeBefore todo.Size `json:"size_before"`
	Size       todo.Size `json:"size"`
	// Plan is the research answer as written; Steps its parsed titles.
	Plan      string   `json:"plan"`
	Steps     []string `json:"steps"`
	Questions []string `json:"questions"`

	// Round counts remediation rounds used.
	Round int `json:"round"`
	// Findings is what the last review or verify turned up, for the
	// remediation prompt and, at the end, for the evidence.
	Findings string `json:"findings"`
	// Verified reports the last verify passed; a review only runs on a
	// verified tree.
	Verified bool `json:"verified"`

	Message string `json:"message"`
	Report  string `json:"report"`
	Blocked string `json:"blocked"`
	// Files are the paths the run committed, for the report.
	Files []string `json:"files"`
	// Tests are the item's test commands as they stood when the run
	// started — before any model turn could have edited the file. The
	// verify stage runs these and only these.
	Tests []string `json:"tests"`
}

// Start begins a run on an item.
func Start(it todo.Item, session, prevMode string, turn int) *State {
	now := time.Now()
	return &State{
		Slug: it.Slug, Session: session, Started: now, Updated: now,
		Stage: StageResearch, Turn: turn, PrevMode: prevMode,
		SizeBefore: it.Size, Size: it.Size,
		Tests: TestCommands(it.Body),
	}
}

// Dir is where checkpoints live.
func Dir(root string) string { return filepath.Join(todo.Dir(root), todo.RunSubdir) }

func path(root, slug string) string { return filepath.Join(Dir(root), slug+".json") }

// Save writes the checkpoint.
func (s *State) Save(root string) error {
	s.Updated = time.Now()
	if err := os.MkdirAll(Dir(root), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path(root, s.Slug), data, 0o644)
}

// Load reads a checkpoint.
func Load(root, slug string) (*State, error) {
	data, err := os.ReadFile(path(root, slug))
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path(root, slug), err)
	}
	return &s, nil
}

// Discard removes a checkpoint; a run that ended has nothing to continue.
func Discard(root, slug string) { _ = os.Remove(path(root, slug)) }

// Over reports whether the run has reached an end state.
func (s *State) Over() bool { return s.Stage == StageDone || s.Stage == StageBlocked }

// First is the run's first step: research, in plan mode.
func (s *State) First(it todo.Item, context string) Step {
	s.Stage = StageResearch
	return Step{Action: ActionPrompt, Stage: StageResearch, Mode: ModePlan,
		Prompt: researchPrompt(it, context), Shown: s.label("research")}
}

// Observe reads the model's answer to the current stage and returns the
// next step. text is the assistant's final message for the turn; empty
// means the turn ended without one — a failure, a cancel — which blocks
// the run, because a stage with no answer cannot be judged.
func (s *State) Observe(it todo.Item, text string) Step {
	text = strings.TrimSpace(text)
	if text == "" {
		return s.block("the " + string(s.Stage) + " turn ended without an answer")
	}
	// Only a stage that changes things may report itself blocked; a review
	// or a commit turn quoting the word is not a block.
	switch s.Stage {
	case StageResearch, StageImplement, StageRemediate:
		if reason, ok := blockedLine(text); ok {
			return s.block("the model reported it is blocked: " + reason)
		}
	}
	switch s.Stage {
	case StageResearch:
		return s.afterResearch(it, text)
	case StageImplement, StageRemediate:
		return s.verify()
	case StageReview:
		return s.afterReview(it, text)
	case StageCommit:
		return s.afterCommit(text)
	}
	return s.block("no stage to observe")
}

// afterResearch reads the plan, the size and the questions. An open
// question blocks: this front-end has no pause to ask it on yet, and
// guessing an answer is the one thing a deterministic runner must not do.
func (s *State) afterResearch(it todo.Item, text string) Step {
	p := plan.Parse(text)
	s.Plan = text
	s.Steps = nil
	for _, st := range p.Steps {
		s.Steps = append(s.Steps, st.Title)
	}
	if size, ok := sizeLine(text); ok {
		s.Size = size
	}
	s.Questions = questionLines(text)
	if len(s.Questions) > 0 {
		return s.block("open questions after research:\n- " + strings.Join(s.Questions, "\n- "))
	}
	if !p.Structured() {
		return s.block("research produced no numbered plan")
	}
	s.Stage = StageImplement
	return Step{Action: ActionPrompt, Stage: StageImplement, Mode: ModeAuto,
		Prompt: implementPrompt(it, s.Plan), Shown: s.label("implement")}
}

// verify is the step after any change to the tree.
func (s *State) verify() Step {
	s.Stage = StageVerify
	s.Verified = false
	return Step{Action: ActionVerify, Stage: StageVerify, Shown: s.label("verify")}
}

// VerifyResult is the front-end reporting the verify outcome. Failure spends a
// remediation round; passing goes to review.
func (s *State) VerifyResult(it todo.Item, ok bool, output string) Step {
	if ok {
		s.Verified = true
		s.Stage = StageReview
		// Review and the commit turn read; they run in the read-only mode
		// so nothing can change between the verify that passed and the
		// commit — an edit made while reviewing would land unverified.
		return Step{Action: ActionPrompt, Stage: StageReview, Mode: ModePlan,
			Prompt: reviewPrompt(it, s.Plan), Shown: s.label("review")}
	}
	return s.remediate(it, "Verification failed:\n"+output)
}

// afterReview reads the verdict line.
func (s *State) afterReview(it todo.Item, text string) Step {
	verdict, findings := verdictLine(text)
	switch verdict {
	case "clean":
		s.Stage = StageCommit
		return Step{Action: ActionPrompt, Stage: StageCommit, Mode: ModePlan,
			Prompt: commitPrompt(it, s.Plan), Shown: s.label("commit")}
	case "findings":
		return s.remediate(it, "Review findings:\n"+findings)
	}
	return s.block("the review ended without a verdict line")
}

// remediate spends a round, or blocks when they are spent.
func (s *State) remediate(it todo.Item, findings string) Step {
	s.Findings = findings
	if s.Round >= Rounds(s.Size) {
		return s.block(fmt.Sprintf("remediation rounds spent (%d):\n%s", s.Round, findings))
	}
	s.Round++
	s.Stage = StageRemediate
	return Step{Action: ActionPrompt, Stage: StageRemediate, Mode: ModeAuto,
		Prompt: remediatePrompt(it, findings), Shown: s.label(fmt.Sprintf("remediate %d/%d", s.Round, Rounds(s.Size)))}
}

// afterCommit reads the commit message and the report.
func (s *State) afterCommit(text string) Step {
	message, report, ok := commitParts(text)
	if !ok {
		return s.block("the commit turn did not produce a message and a report in the asked shape")
	}
	s.Message, s.Report = message, report
	return Step{Action: ActionCommit, Stage: StageCommit, Shown: s.label("commit")}
}

// Committed is the front-end reporting the commit landed; the run is done.
func (s *State) Committed(files []string) Step {
	s.Files = files
	s.Stage = StageDone
	return Step{Action: ActionDone, Stage: StageDone}
}

// Block ends the run with evidence from the front-end — a commit that
// could not be made, a tree with foreign staged changes.
func (s *State) Block(reason string) Step { return s.block(reason) }

func (s *State) block(reason string) Step {
	s.Blocked = reason
	s.Stage = StageBlocked
	return Step{Action: ActionBlocked, Stage: StageBlocked}
}

func (s *State) label(stage string) string {
	return fmt.Sprintf("▸ todo run %s · %s", s.Slug, stage)
}

// Summary is the one-paragraph state for /todo status and the rail.
func (s *State) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %s", s.Slug, s.Stage)
	if s.Size != "" {
		fmt.Fprintf(&b, " · size %s", s.Size)
		if s.SizeBefore != s.Size {
			fmt.Fprintf(&b, " (was %s)", orDash(string(s.SizeBefore)))
		}
	}
	if s.Round > 0 {
		fmt.Fprintf(&b, " · remediation %d/%d", s.Round, Rounds(s.Size))
	}
	if len(s.Steps) > 0 {
		fmt.Fprintf(&b, "\nplan: %d steps", len(s.Steps))
	}
	if s.Blocked != "" {
		fmt.Fprintf(&b, "\nblocked: %s", s.Blocked)
	}
	return b.String()
}

// TestCommands reads an item's Tests section: one command per bullet.
func TestCommands(body string) []string {
	var out []string
	in := false
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "## ") {
			in = strings.EqualFold(strings.TrimSpace(t[3:]), "tests")
			continue
		}
		if !in || !strings.HasPrefix(t, "- ") {
			continue
		}
		cmd := strings.Trim(strings.TrimSpace(t[2:]), "`")
		if cmd != "" {
			out = append(out, cmd)
		}
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// The answer shapes. Each is one line the runner reads by prefix, so the
// model's prose around it costs nothing.

var (
	// [ \t]* rather than \s* after the colon: \s crosses a newline, and the
	// value has to be on the marker's own line.
	sizePattern     = regexp.MustCompile(`(?im)^[ \t]*size:[ \t]*([SML])\b`)
	blockedPattern  = regexp.MustCompile(`(?im)^[ \t]*blocked:[ \t]*(.+)$`)
	verdictPattern  = regexp.MustCompile(`(?im)^[ \t]*verdict:[ \t]*(clean|findings)\b`)
	questionPattern = regexp.MustCompile(`(?im)^[ \t]*questions:[ \t]*(.*)$`)
)

func sizeLine(text string) (todo.Size, bool) {
	m := sizePattern.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	return todo.Size(strings.ToUpper(m[1])), true
}

func blockedLine(text string) (string, bool) {
	m := blockedPattern.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

// questionLines reads the `questions:` block: what is on the line, then
// the bulleted lines under it until a blank line or a heading. "none" is
// no question.
func questionLines(text string) []string {
	loc := questionPattern.FindStringSubmatchIndex(text)
	if loc == nil {
		return nil
	}
	var out []string
	if inline := strings.TrimSpace(text[loc[2]:loc[3]]); inline != "" && !strings.EqualFold(inline, "none") {
		out = append(out, inline)
	}
	// The match ends at the marker line's end; the block starts on the
	// next line.
	for _, line := range strings.Split(strings.TrimPrefix(text[loc[1]:], "\n"), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			break
		}
		if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") {
			q := strings.TrimSpace(t[2:])
			if q != "" && !strings.EqualFold(q, "none") {
				out = append(out, q)
			}
			continue
		}
		break
	}
	return out
}

func verdictLine(text string) (verdict, findings string) {
	loc := verdictPattern.FindStringSubmatchIndex(text)
	if loc == nil {
		return "", ""
	}
	verdict = strings.ToLower(text[loc[2]:loc[3]])
	return verdict, strings.TrimSpace(text[loc[1]:])
}

// commitParts reads the commit turn: a `COMMIT:` block and a `REPORT:`
// block, in that order, each running to the next marker or the end.
func commitParts(text string) (message, report string, ok bool) {
	ci := strings.Index(text, "COMMIT:")
	ri := strings.Index(text, "REPORT:")
	if ci < 0 || ri < 0 || ri < ci {
		return "", "", false
	}
	message = strings.TrimSpace(text[ci+len("COMMIT:") : ri])
	report = strings.TrimSpace(text[ri+len("REPORT:"):])
	message = strings.Trim(message, "`\n ")
	message = strings.TrimPrefix(message, "text\n")
	if message == "" || report == "" {
		return "", "", false
	}
	return message, report, true
}
