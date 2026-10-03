package cli

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// doctorModel hosts the surface. It runs one probe at a time and redraws
// between them, so a run in progress is something the reader can watch rather
// than a blank terminal that resolves all at once.
type doctorModel struct {
	cfg    config.Config
	probes []doctorProbe
	// ctx is the command's context, handed to every probe, because it
	// carries what the checkout's own settings file contributed — which the
	// model row needs to name the file that set a key. Nil is a background
	// context: a screen nobody built from a command.
	ctx     context.Context
	started time.Time
	at      int

	// findings are the answers behind the rows the screen is drawing. The
	// screen is handed only what it renders, and an action is not renderable
	// — so the function `[a]` invokes stays here, indexed the same way.
	findings []doctorFinding
	// nouns are what the header and the text report count, singular and
	// plural, when the screen is not doctor's own.
	nouns [2]string

	screen components.DoctorScreen
}

// doctorDoneMsg carries one probe's answer back to the model.
type doctorDoneMsg struct {
	at      int
	finding doctorFinding
	took    time.Duration
}

// doctorTickMsg drives the one spinner on the screen, at the shared tick
// interval.
type doctorTickMsg time.Time

// doctorAppliedMsg carries back what an action did, so a migration that has
// to move a large store does not freeze the surface while it runs.
type doctorAppliedMsg struct {
	lines []string
	err   error
}

func newDoctorModel(cfg config.Config, probes []doctorProbe) *doctorModel {
	m := &doctorModel{cfg: cfg, probes: probes}
	m.findings = make([]doctorFinding, len(m.probes))
	m.screen.Checks = make([]components.DoctorCheck, len(m.probes))
	for i, probe := range m.probes {
		subject := probe.queued
		if subject == "" {
			subject = doctorQueuedSubject(probe.name)
		}
		m.screen.Checks[i] = components.DoctorCheck{
			Name: probe.name, Subject: subject,
			Outcome: components.OutcomeQueued, Duration: components.NoDuration,
			State: components.DoctorQueued,
		}
	}
	m.screen.Running = len(m.probes) > 0
	m.screen.Spin = true
	return m
}

// doctorQueuedSubject is what a check says about itself before it has run.
// A queued row that said nothing would be a row the reader cannot read.
func doctorQueuedSubject(name string) string {
	switch name {
	case "binary":
		return "which shhh this is"
	case "keys":
		return "whether the terminal delivers every chord the keyboard offers"
	case "keymap":
		return "the keybindings file and whether it was applied"
	case "config":
		return "the config file and what it sets"
	case "migrate":
		return "whether this machine is still shaped an older way"
	case "model":
		return "the provider and where its key comes from"
	case "flows":
		return "the model each bounded call runs on"
	case "search":
		return "the backend a session searches the web with"
	case "hosts":
		return "the lists a fetch's host is read against"
	case "store":
		return "the local store"
	case "logs":
		return "where a refused request is written down"
	case "reports":
		return "the report pages sessions built"
	case "otel":
		return "where the session record is sent"
	case "sandbox":
		return "what contains an approved command"
	case "engine":
		return "container sandboxes"
	case "image":
		return "the image a container sandbox starts from"
	case "git":
		return "the workspace, and whether an edit can be undone"
	case "project":
		return "the instruction files a session here reads"
	case "trust":
		return "what this checkout may make a session load"
	case "needs":
		return "the tools this checkout's work declares"
	case "hooks":
		return "your own commands at the session's seams"
	case "prompts":
		return "the wordings a file replaced"
	case "tools":
		return "the tools and language servers on PATH"
	case "memory":
		return "what this project remembers"
	case "update":
		return "check for a newer shhh"
	}
	return name
}

// begin starts the first probe and the one tick source.
func (m *doctorModel) begin() tea.Cmd {
	return tea.Batch(m.runNext(), doctorTick())
}

// runNext starts the check at the cursor, or nothing when the run is done.
func (m *doctorModel) runNext() tea.Cmd {
	if m.at >= len(m.probes) {
		return nil
	}
	at, probe, cfg, ctx := m.at, m.probes[m.at], m.cfg, m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		started := time.Now()
		finding := probe.run(ctx, cfg)
		return doctorDoneMsg{at: at, finding: finding, took: time.Since(started)}
	}
}

// doctorTick is the one tick source: the spinner in the header and on
// the running row are the same frame.
func doctorTick() tea.Cmd {
	return tea.Tick(components.SpinnerInterval, func(t time.Time) tea.Msg {
		return doctorTickMsg(t)
	})
}

// answer carries out what a key asked for. A key that asked for something
// does not also close the screen: the whole point of `[c]` and `[r]` is that
// the report is still there afterwards.
func (m *doctorModel) answer(done bool, result components.DoctorResult) tea.Cmd {
	m.screen.Notice = ""
	if result.Command != nil {
		return m.apply(*result.Command)
	}
	if done {
		return tea.Quit
	}
	return nil
}

// other is the run's own traffic: a probe finishing, the spinner's tick, and
// what an applied action did.
func (m *doctorModel) other(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case doctorTickMsg:
		if !m.screen.Running {
			// The spinner stops when the run does; a frame still turning over
			// a finished run would say the screen is doing something.
			return nil
		}
		m.screen.Frame++
		m.screen.Elapsed = doctorElapsed(time.Since(m.started))
		return doctorTick()

	case doctorAppliedMsg:
		return m.applied(msg)

	case doctorDoneMsg:
		m.findings[msg.at] = msg.finding
		m.screen.Checks[msg.at] = doctorCheck(m.probes[msg.at].name, msg.finding, msg.took)
		m.at = msg.at + 1
		m.screen.Elapsed = doctorElapsed(time.Since(m.started))
		if m.at >= len(m.probes) {
			m.screen.Running = false
			return nil
		}
		m.markRunning(m.at)
		return m.runNext()
	}
	return nil
}

// markRunning turns the queued row at the cursor into the running one. It is
// the model's job rather than the screen's: which check is in flight is a
// fact about the run, not about the rendering.
func (m *doctorModel) markRunning(at int) {
	m.screen.Checks[at].State = components.DoctorRunning
	m.screen.Checks[at].Outcome = components.OutcomeRunning
	m.screen.Checks[at].Duration = ""
}

// apply carries out one command. `[c]` copies the report the surface is
// showing; `[r]` puts every row back to queued and starts again, so a fix
// applied in another terminal can be checked without leaving this one.
func (m *doctorModel) apply(command components.DoctorCommand) tea.Cmd {
	switch command.Act {
	case components.DoctorCopy:
		if res := clipboard.Copy(m.report()); !res.OK {
			m.screen.Notice = "clipboard: " + res.Warning
		} else {
			m.screen.Notice = "copied the report to the clipboard"
		}
		return nil
	case components.DoctorRerun:
		return m.rerun()
	case components.DoctorApply:
		// The screen has already asked, so by the time this arrives the
		// answer was yes. It is run off the update loop because a migration
		// moves files, and a surface that stopped repainting while it did
		// would read as a hang.
		apply := m.findings[command.At].Apply
		if apply == nil {
			return nil
		}
		m.screen.Notice = "applying…"
		return func() tea.Msg {
			lines, err := apply()
			return doctorAppliedMsg{lines: lines, err: err}
		}
	}
	return nil
}

// applied reports what an action did and re-runs every check, because the
// answer to "did that work" is the report itself and not a line at the foot
// of a stale one. The notice survives the re-run: it is the record of what
// changed, and the rows that are about to redraw will not say it again.
func (m *doctorModel) applied(msg doctorAppliedMsg) tea.Cmd {
	cmd := m.rerun()
	switch {
	case msg.err != nil && len(msg.lines) == 0:
		m.screen.Notice = "nothing changed: " + msg.err.Error()
	case msg.err != nil:
		m.screen.Notice = countOf(len(msg.lines), "change made", "changes made") +
			", then it stopped: " + msg.err.Error()
	default:
		m.screen.Notice = countOf(len(msg.lines), "change made", "changes made")
	}
	return cmd
}

// rerun puts every row back to queued and starts the checks again — what
// `[r]` does, and what an applied action does after it, so the report the
// reader is left looking at is a reading of the machine as it is now. What
// the terminal and the calling command settled stays: the row budget, the
// screen's title and the nouns its report counts in are not findings.
func (m *doctorModel) rerun() tea.Cmd {
	rows, title, nouns, ctx := m.screen.MaxLines, m.screen.Title, m.nouns, m.ctx
	*m = *newDoctorModel(m.cfg, m.probes)
	m.screen.MaxLines, m.screen.Title, m.nouns, m.ctx = rows, title, nouns, ctx
	m.started = time.Now()
	m.markRunning(0)
	return m.begin()
}

// report is the run as text under the screen's own title.
func (m *doctorModel) report() string {
	title, one, many := "shhh doctor", "check", "checks"
	if m.screen.Title != "" {
		title, one, many = m.screen.Title, m.nouns[0], m.nouns[1]
	}
	return doctorReportOf(title, one, many, m.screen.Checks).String()
}

// doctorElapsed is the header's running clock: tenths while a run is short
// enough for them to mean something, whole seconds after that.
func doctorElapsed(d time.Duration) string {
	if d < 10*time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

func runDoctorScreen(ctx context.Context, cfg config.Config, probes []doctorProbe) error {
	return runDoctorScreenIn(ctx, cfg, probes, "", [2]string{})
}

// runDoctorScreenTitled is the screen under another command's name, with
// the nouns its header and report count in. Empty means doctor's own.
func runDoctorScreenTitled(cfg config.Config, probes []doctorProbe, title string, nouns [2]string) error {
	return runDoctorScreenIn(context.Background(), cfg, probes, title, nouns)
}

// runDoctorScreenIn is the screen with the context its probes are handed.
func runDoctorScreenIn(ctx context.Context, cfg config.Config, probes []doctorProbe, title string, nouns [2]string) error {
	m := newDoctorModel(cfg, probes)
	m.ctx, m.screen.Title, m.nouns = ctx, title, nouns
	m.started = time.Now()
	m.markRunning(0)
	host := newScreenModel(&m.screen, defaultDoctorWidth, m.answer)
	host.begin, host.other = m.begin, m.other
	_, err := newProgram(host).Run()
	return err
}
