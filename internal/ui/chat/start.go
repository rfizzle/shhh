package chat

// First contact (docs/interface/surfaces.md#the-start-screen). An
// empty session is the one screen every user sees and the one that used to
// say the least: a single italic sentence in the middle of a blank viewport.
// It now states what shhh already knows about the checkout — path, toolchain,
// branch, dirty count, package count — names the files it read into the
// system prompt and the quality gate that will run without asking, and offers
// three concrete pieces of work.
//
// Two rules keep it from becoming a wizard. Everything on it was computed
// once, at session start, by the survey the CLI hands in; View does
// arithmetic on strings and nothing else. And typing dismisses the list — the
// suggestions and their keys go, the facts stay — because the input textarea
// owns every ordinary key and an offer nothing accepts is worse than no
// offer.

import (
	"fmt"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// StartGate is the quality-gate configuration in effect, which the screen
// names because it governs what runs without an approval.
type StartGate struct {
	// Path is where the config was looked for, stated whether or not it was
	// found — a gate that is not configured is only actionable if you know
	// which file to write.
	Path string
	// Suite is the default suite's name and Checks the checks it runs.
	// Suites counts every configured suite.
	Suite  string
	Checks []string
	Suites int
	// Err is a config that exists but does not load. It is reported rather
	// than swallowed: a broken gate is not an absent one.
	Err string
}

// Configured reports a gate that loaded.
func (g StartGate) Configured() bool { return g.Suite != "" }

// StartRecent is the most recent saved session, behind the resume
// suggestion. Priced marks a cost that came from an observability record;
// without one the clause is dropped rather than printed as $0.00.
type StartRecent struct {
	Present bool
	Name    string
	// Title is the session's generated title, shown ahead of the count so
	// a timestamp of a name still says what the conversation was about.
	Title   string
	Turns   int
	Updated time.Time
	Cost    float64
	Priced  bool
}

// StartInfo is everything the start screen renders, gathered once by the CLI
// at session start.
type StartInfo struct {
	Project project.Info
	Gate    StartGate
	Recent  StartRecent
	// Now fixes the clock the "4m ago" clause is measured against; the zero
	// value means time.Now, which is what the product uses and the tests do
	// not.
	Now time.Time
}

// WithStartScreen supplies the first-contact facts. Without it the empty
// session keeps the plain welcome line, which is what every non-chat host
// (the attached child view, tests that build a bare model) gets.
func (m Model) WithStartScreen(info StartInfo) Model {
	m.start = &info
	return m
}

// startScreenShowing reports whether the empty session renders the screen
// rather than the welcome line. The latch is the interesting half: once a
// session has said something to the model or loaded a conversation, /clear
// empties the transcript but does not make the session new again, so the
// screen does not come back.
func (m Model) startScreenShowing() bool {
	return m.start != nil && !m.startSpent && len(m.transcript) == 0
}

// startChoosing reports whether the suggestion list is live — showing, and
// with the keys still free. A draft in the input dismisses the list: the
// reader has said what they want, and ↑ belongs to the input history again.
func (m Model) startChoosing() bool {
	if !m.startScreenShowing() || m.state != stateInput || m.attachedTo != "" {
		return false
	}
	if m.completionActive() || m.browsingHistory() {
		return false
	}
	return strings.TrimSpace(m.input.Value()) == ""
}

// spendStartScreen retires the screen for the rest of the session.
func (m *Model) spendStartScreen() { m.startSpent = true }

// startKey handles ↑↓ and enter while the suggestion list is live. It reports
// false when the screen is not claiming the key, leaving the ordinary
// handlers (input history, submit) exactly as they were.
func (m Model) startKey(key string) (Model, bool) {
	if !m.startChoosing() {
		return m, false
	}
	screen, actions := m.startScreen()
	if len(actions) == 0 {
		return m, false
	}
	switch key {
	case "up":
		m.startFocus = screen.FocusAfter(-1)
	case "down":
		m.startFocus = screen.FocusAfter(1)
	default:
		return m, false
	}
	m.syncViewport()
	return m, true
}

// startAction is what enter types on behalf of the focused suggestion. It
// returns "" when the screen is not claiming enter. Choosing a suggestion and
// typing it are the same act by construction — the action is a line of input,
// dispatched through the same submit path — so a suggestion can never reach
// somewhere typing could not.
func (m Model) startAction() string {
	if !m.startChoosing() {
		return ""
	}
	_, actions := m.startScreen()
	if m.startFocus < 0 || m.startFocus >= len(actions) {
		return ""
	}
	return actions[m.startFocus]
}

// renderStartScreen draws the screen for the transcript pane.
func (m Model) renderStartScreen(width int) string {
	screen, _ := m.startScreen()
	if !m.startChoosing() {
		// Typing dismisses the list, not the facts — and not the
		// navigation line, whose keys are still live.
		screen.Suggestions, screen.Lead, screen.Hint = nil, "", ""
	}
	return screen.View(width)
}

// startScreen builds the component and the action line behind each
// suggestion, in one pass so the two can never drift apart.
func (m Model) startScreen() (components.StartScreen, []string) {
	info := m.start
	if info == nil {
		return components.StartScreen{}, nil
	}
	suggestions, actions := startSuggestions(*info, m.scaffoldOffered())
	return components.StartScreen{
		Facts:       startFacts(info.Project),
		Notes:       startNotes(*info),
		Lead:        "Some things worth doing first:",
		Suggestions: suggestions,
		Focus:       min(max(m.startFocus, 0), max(len(suggestions)-1, 0)),
		Hint:        "[↑↓] choose · [enter] start · or just type what you want",
		// The navigation line survives the typing dismissal above, because
		// these keys survive it: every one of them works with a half-written
		// draft in the box. This is the one screen every user
		// sees, so it is where the two panes are introduced — and the one
		// place the mouse chord can be learned before it is wanted, which for
		// that setting is the whole difficulty.
		//
		// Scrolling and the handover are named apart, because they are two
		// things now: pgup reads without giving up the keyboard, the reading
		// chord is what hands it over when the rows are what you want. The
		// spellings are the register's, so this line cannot survive a rebind
		// with the old chord on it.
		Nav: "[pgup] or [shift+↑↓] scroll · " + keys.Bracket(keys.Draft.Reading) + " select rows · " +
			keys.Bracket(keys.Draft.Palette) + " palette · " + keys.Bracket(keys.Draft.Mouse) + " mouse",
	}, actions
}

// startFacts is the header line: where we are, in what, on which branch, how
// dirty, how big. Each clause is dropped when it would be a guess rather than
// a fact.
func startFacts(p project.Info) []components.StartFact {
	dir := p.Display
	if dir == "" {
		dir = "this directory"
	}
	facts := []components.StartFact{{Text: dir, Lead: true}}

	if p.Language != "" {
		lang := p.Language
		if p.Toolchain != "" {
			lang += " " + p.Toolchain
		}
		facts = append(facts, components.StartFact{Text: lang})
	}
	switch {
	case !p.Repo:
		facts = append(facts, components.StartFact{Text: "not a git repository", Tone: components.ToneOpen})
	case p.Detached:
		facts = append(facts, components.StartFact{Text: "git detached HEAD", Tone: components.ToneOpen})
	default:
		facts = append(facts, components.StartFact{Text: "git " + p.Branch})
	}
	if p.Repo {
		if p.Dirty == 0 {
			facts = append(facts, components.StartFact{Text: "clean tree", Tone: components.ToneSafe})
		} else {
			changed := fmt.Sprintf("%d files changed", p.Dirty)
			if p.Dirty == 1 {
				changed = "1 file changed"
			}
			facts = append(facts, components.StartFact{Text: changed, Tone: components.ToneOpen})
		}
	}
	if p.Packages > 0 {
		count := fmt.Sprintf("%d", p.Packages)
		if p.Partial {
			// The walk hit its bound, so the number is a floor. Saying so is
			// cheaper than a walk that never finishes.
			count += "+"
		}
		unit := p.Unit
		if unit == "" {
			unit = "package"
		}
		if p.Packages != 1 || p.Partial {
			unit += "s"
		}
		facts = append(facts, components.StartFact{Text: count + " " + unit})
	}
	return facts
}

// startNotes are the labelled lines: which directory this session's project
// state belongs to where that is not the one in the header, what the model
// was told about this project, and what will run without asking.
func startNotes(info StartInfo) []components.StartNote {
	var notes []components.StartNote
	// The backlog, the refused offers and the run's checkpoints are keyed
	// on the root, not on the working directory, and outside a repository
	// the root is wherever a shhh directory was found up the tree. Two
	// terminals in one project that key on two roots see two different
	// backlogs, so the root is named wherever it is not the path the header
	// already prints — and where it is, the header has named it.
	if root := info.Project.RootDisplay; root != "" && root != info.Project.Display {
		notes = append(notes, components.StartNote{Label: "root", Value: root,
			Detail: "the backlog and this project's state are kept here"})
	}
	// The names are spelled out because a checkout that has told the model
	// nothing looks exactly like one that has, and the reader's next move is
	// to write one of these three files.
	context := components.StartNote{Label: "context", Value: "nothing read",
		Detail: "no " + project.InstructionNames()}
	if files := info.Project.ContextFiles; len(files) > 0 {
		// Outermost first, the order the prompt states them in: the last one
		// named is the one nearest this directory and the one with the last
		// word.
		context.Value = strings.Join(files, " · ")
		context.Detail = "in the system prompt"
	}

	gate := components.StartNote{Label: "gate", Value: "not configured", Detail: info.Gate.Path}
	switch {
	case info.Gate.Err != "":
		gate.Value, gate.Detail = "unreadable", info.Gate.Err
	case info.Gate.Configured():
		gate.Value = info.Gate.Suite
		detail := strings.Join(info.Gate.Checks, ", ")
		if info.Gate.Suites > 1 {
			detail += fmt.Sprintf(" (%d suites)", info.Gate.Suites)
		}
		gate.Detail = detail + " · runs without asking"
	}
	return append(notes, context, gate)
}

// startSuggestions is the three offers, paired with the input line each one
// stands for. The order is what the working tree suggests: something to pick
// up first, then a read-only offer, then the one that costs an approval.
//
// scaffold is what the last of those is when the checkout has no `.shhh` of
// its own: a project that has told the model nothing about itself is worth
// more than a test run, and it is how `shhh init --project` is found without
// reading the manual (docs/interface/surfaces.md#the-start-screen).
func startSuggestions(info StartInfo, scaffold bool) ([]components.StartSuggestion, []string) {
	var out []components.StartSuggestion
	var actions []string
	add := func(glyph, title, detail, action string) {
		out = append(out, components.StartSuggestion{Glyph: glyph, Title: title, Detail: detail})
		actions = append(actions, action)
	}

	if r := info.Recent; r.Present {
		add("▸", "pick up "+r.Name, recentDetail(r, info.Now), "/load "+r.Name)
	}

	if info.Project.Repo && info.Project.Dirty > 0 {
		add("⚙", "explain what changed in the working tree", "reads only, no writes",
			"explain what changed in the working tree")
	} else {
		add("⚙", "walk me through what this project does", "reads only, no writes",
			"walk me through what this project does, starting from its entry point")
	}

	// Three offers, always. Without a session to pick up there is room for a
	// second read-only offer; with one there is not, and the resume is the
	// better use of the row.
	if len(out) < 2 {
		add("⚙", "summarise the last ten commits", "reads only, no writes",
			"summarise the last ten commits and what they were working towards")
	}

	if scaffold {
		// The write glyph, because this row is the one that writes: the
		// read-only offers above it keep ⚙.
		add("✎", "scaffold "+project.StateDir+"/", "one approval", scaffoldCommandName)
		return out, actions
	}
	verify := verifyPrompt(info)
	add("⚙", verify, "one approval, then it reports back", verify)
	return out, actions
}

// verifyPrompt is the offer that costs an approval: the configured gate where
// there is one, otherwise the detected toolchain's own test command. It is
// never invented — an unrecognised project is asked for its tests in words
// rather than handed a command that may not exist.
func verifyPrompt(info StartInfo) string {
	if info.Gate.Configured() {
		return "run the " + info.Gate.Suite + " quality gate and triage what fails"
	}
	switch info.Project.Language {
	case "go":
		return "run go test ./... and triage the failures"
	case "rust":
		return "run cargo test and triage the failures"
	case "node":
		return "run npm test and triage the failures"
	case "python":
		return "run the test suite with pytest and triage the failures"
	}
	return "find this project's tests, run them, and triage the failures"
}

// recentDetail prices the resume offer: how much conversation is in it, what
// it cost, and how long ago it was. A session with no observability record
// keeps the first and last clauses and drops the price.
func recentDetail(r StartRecent, now time.Time) string {
	parts := []string{plural(r.Turns, "turn")}
	if r.Title != "" {
		parts = []string{r.Title, parts[0]}
	}
	if r.Priced {
		parts = append(parts, fmt.Sprintf("$%.2f", r.Cost))
	}
	if !r.Updated.IsZero() {
		parts = append(parts, agoLabel(r.Updated, now))
	}
	return strings.Join(parts, " · ")
}

// agoLabel renders how long ago t was, at the coarsest unit that is still
// true. now is the caller's clock, so a test does not have to race one.
func agoLabel(t, now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
