package cli

// Session observability: `shhh observe` renders local, content-free
// dashboards over recorded agent sessions, with JSON export and purge. The
// observeRecorder half persists what a running session reports.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/spf13/cobra"
)

// observeRecorder writes one agent session's content-free events to storage.
// A nil recorder is a no-op, so callers can wire it unconditionally.
type observeRecorder struct {
	db     *storage.DB
	id     int64
	prices *pricing.Table
	model  string
	// kind and provider are what the row was opened with, kept so a session
	// boundary can open the next row the same way rather than being handed
	// the three of them again by a caller that would then be free to change
	// one (restart).
	kind, provider string
	// linked is the saved conversation the row was last linked to, so an
	// autosave that lands in the same slot costs no write.
	linked string
	// outcome is the session outcome the last closing turn wrote, so the
	// end knows whether anything ever said how the session came out.
	outcome string
}

// startObserveRecorder opens a session row; any failure disables recording
// for the session rather than blocking it.
func startObserveRecorder(db *storage.DB, kind, provider, model string, prices *pricing.Table) *observeRecorder {
	if db == nil {
		return nil
	}
	// Every start reconciles first: a session that was killed outright never
	// wrote its end time, and a row left open reads to the next session as
	// somebody still working in this checkout. Best effort and quiet, the way
	// sandbox ownership records are reaped — a store that will not answer is
	// never a reason to refuse to start
	// (docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone).
	_, _ = db.CloseCrashedAgentSessions()
	id, err := db.StartAgentSession(kind, provider, model)
	if err != nil {
		return nil
	}
	return &observeRecorder{db: db, id: id, prices: prices, model: model, kind: kind, provider: provider}
}

// startChildObserveRecorder opens a sub-agent's session row linked to its
// parent session; failures disable recording for that child only.
func startChildObserveRecorder(db *storage.DB, kind, provider, model string, prices *pricing.Table, parentID int64) *observeRecorder {
	if db == nil {
		return nil
	}
	id, err := db.StartChildAgentSession(parentID, kind, provider, model)
	if err != nil {
		return nil
	}
	return &observeRecorder{db: db, id: id, prices: prices, model: model, kind: kind, provider: provider}
}

// sessionID is the recorder's session row id (0 when recording is disabled),
// used to link child sessions to their parent.
func (r *observeRecorder) sessionID() int64 {
	if r == nil {
		return 0
	}
	return r.id
}

// stamp records what the session ran under: the build, a fingerprint of the
// system prompt as sent, how many skills loaded, a fingerprint of the
// checkout, and the settings it was configured with. Fingerprints rather
// than the things themselves: the prompt carries the project context and the
// path names the machine, and neither belongs in a table that is
// content-free by construction. A hash still splits "before the edit" from
// "after it", which is all a comparison needs.
func (r *observeRecorder) stamp(sysPrompt string, skills int, root string, settings storage.AgentSettings) {
	if r == nil {
		return
	}
	_ = r.db.StampAgentSession(r.id, storage.AgentProvenance{
		Version:    version,
		PromptHash: fingerprint(sysPrompt),
		Skills:     skills,
		Project:    fingerprint(root),
		Settings:   settings,
	})
}

// runSettings are the values a surface resolved for itself before it opened
// its row — the ones the config file alone cannot answer, because a flag, a
// profile, a clamp to a parent or a mechanism check had the last word.
type runSettings struct {
	// mode is the permission mode the surface starts in, or empty where no
	// permission mode applies: a one-shot has no policy to be in a mode of,
	// and a headless run answers with --yes and --allow instead.
	mode string
	// effort is the reasoning level the surface starts at.
	effort provider.Effort
	// rounds is the per-turn tool-round cap in force, 0 for none; a
	// surface that resolves its cap through maxRoundsFor spells it with
	// roundCapFor first.
	rounds int
	// sandbox is the containment profile in force, or empty when nothing
	// contains the surface's commands — an unconfined session runs under
	// no profile, whatever the config asked for.
	sandbox string
	// model is what the summariser and the classifier fall back to when
	// their own keys are unset — the provider's small model where it names
	// one, else the session's own. It is resolved by the surface rather than
	// here so the record states the model that was actually asked.
	model string
	// summary and classifier say whether each mechanism exists on this
	// surface at all. A one-shot takes no readings and asks no classifier,
	// and recording the model it would have used is recording a setting
	// that was not in force.
	summary, classifier bool
}

// sessionSettings is the allowlist: every config value the record keeps
// whole, resolved to what was actually in force, and the hash over the rest.
//
// Nothing here is a path, a command or a secret. The scope directories, the
// sandbox's deny and write lists, the command allowlists and the API keys
// reach the store only through the hash. A config value joins the stamped
// set by being read here — or, for the sandbox profile, by the surface that
// parsed it through the profile's closed set before filling runSettings —
// and nowhere else, so a field added to the config is excluded until someone
// decides otherwise. The test beside this fills every config field with a
// marker and checks that none of them comes through except the ones named
// here.
// See docs/capabilities/sessions-and-memory.md#what-a-session-ran-under.
func sessionSettings(cfg config.Config, run runSettings) storage.AgentSettings {
	out := storage.AgentSettings{
		Mode:           run.mode,
		Reasoning:      run.effort.String(),
		MaxRounds:      run.rounds,
		SummaryEnabled: run.summary,
		SandboxProfile: run.sandbox,
		ConfigHash:     configHash(cfg),
	}
	if run.summary {
		out.SummaryModel = modelOr(cfg.Summary.Model, run.model)
		out.SummaryInterval = agent.SummaryConfig{IntervalRounds: cfg.Summary.IntervalRounds}.Interval()
	}
	if run.classifier {
		out.ClassifierModel = modelOr(cfg.Behavior.ClassifierModel, run.model)
	}
	return out
}

// roundCapFor turns maxRoundsFor's three-way answer into the cap in force as
// the record spells it: the number, or 0 for none.
func roundCapFor(rounds int) int {
	switch {
	case rounds < 0:
		return 0
	case rounds == 0:
		return agent.DefaultMaxToolRounds
	default:
		return rounds
	}
}

// configHash fingerprints the whole effective config, secrets and paths
// included, so a change to any field splits sessions on either side of it
// even when the field is one the allowlist does not keep. Going through a
// hash is what makes that safe: nothing in the config is recoverable from
// twelve hex digits of a digest over the whole document.
//
// The encoding is JSON rather than TOML for its determinism: struct fields
// go out in declaration order and map keys sorted, so the same config hashes
// the same on every run. Marshalling a config cannot fail — every field is a
// scalar, a slice, a map of strings or a pointer to one — so an error here
// would be a new field of a shape the encoder refuses, and it is reported
// as an empty hash rather than hidden, because an empty hash is the one
// value the store reads as "no settings were taken".
func configHash(cfg config.Config) string {
	data, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	return fingerprint(string(data))
}

// projectFingerprintRoot is the checkout the session runs in — its
// repository root when there is one, else the working directory — as the
// string the project fingerprint hashes.
func projectFingerprintRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return todo.Root(cwd)
}

// liveSibling reports another session already open in this checkout, and
// when it started. One reading answers for every surface that says so — the
// start screen's fact, the workspace block's line, the tree reading's last
// clause — because three readings of the same question are three chances to
// disagree about whether anybody else is here.
//
// A store that will not answer costs the fact and nothing else: two sessions
// in one checkout is a decision the person made, and nothing here refuses to
// start over it.
// See docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone.
func liveSibling(db *storage.DB) (time.Time, bool) {
	if db == nil {
		return time.Time{}, false
	}
	sib, ok, err := db.LiveSibling(fingerprint(projectFingerprintRoot()), time.Now())
	if err != nil || !ok {
		return time.Time{}, false
	}
	return sib.Since, true
}

// fingerprint is a short stable hash of a string, or empty for empty input.
func fingerprint(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}

// observer adapts the recorder to the observer contract every surface
// reports a session through.
func (r *observeRecorder) observer() observe.Observer {
	if r == nil {
		return observe.Observer{}
	}
	return observe.Observer{
		Usage:    r.usagePriced,
		ToolCall: r.toolCallAt,
		Decision: r.decisionAt,
		Turn:     r.turn,
		Signal:   r.signal,
		Gate:     r.gate,
		Session:  r.link,
	}
}

// usage records a session's running totals, pricing them against the model
// the session was opened on. It is what a sub-agent reports with: a child
// runs on one model for its whole life, so that model is the right one.
func (r *observeRecorder) usage(turns, tokensIn, tokensOut int64) {
	if r == nil {
		return
	}
	var cost float64
	if r.prices != nil {
		if in, out, found := r.prices.Cost(r.model, tokensIn, tokensOut); found {
			cost = in + out
		}
	}
	_ = r.db.UpdateAgentSession(r.id, turns, tokensIn, tokensOut, cost)
}

// usagePriced records totals that arrive already priced, which is what a
// parent session reports. Its spend is a mixture — several models, the
// classifier and the summary among them — and only the ledger that billed
// each request knows what rate each one went out at. Falling back to the
// session model would price the mixture at whichever model happened to be
// current, which is the number this exists to avoid.
func (r *observeRecorder) usagePriced(turns, tokensIn, tokensOut int64, cost float64, priced bool) {
	if r == nil {
		return
	}
	if !priced {
		r.usage(turns, tokensIn, tokensOut)
		return
	}
	_ = r.db.UpdateAgentSession(r.id, turns, tokensIn, tokensOut, cost)
}

func (r *observeRecorder) toolCallAt(at observe.Pos, tool string, duration time.Duration, outcome, class string) {
	if r == nil {
		return
	}
	ms := duration.Milliseconds()
	_ = r.db.RecordAgentEvent(r.id, storage.AgentEvent{
		Kind: storage.AgentEventTool, Tool: tool, DurationMs: &ms, Outcome: outcome, Reason: class,
		Turn: at.Turn, Round: at.Round,
	})
}

func (r *observeRecorder) decisionAt(at observe.Pos, decision, reason string) {
	if r == nil {
		return
	}
	_ = r.db.RecordAgentEvent(r.id, storage.AgentEvent{
		Kind: storage.AgentEventDecision, Outcome: decision, Reason: reason, Turn: at.Turn, Round: at.Round,
	})
}

// turn records a turn closing: the rounds it took ride in the event's
// round column, its wall time in the duration. The turn also says how the
// session has come out so far, which is written now rather than at the exit
// because the session that most needs an outcome is the one whose exit never
// runs (docs/capabilities/sessions-and-memory.md#whether-it-worked).
func (r *observeRecorder) turn(turn, rounds int64, duration time.Duration, outcome string) {
	if r == nil {
		return
	}
	// The heartbeat rides the turn close rather than being called from each
	// front-end's own boundary: this callback is the one every surface
	// already reports a finished turn through, so there is one site to keep
	// pointing at the row the recorder holds now — which a new conversation
	// inside the same process replaces.
	_ = r.db.BeatAgentSession(r.id)
	ms := duration.Milliseconds()
	_ = r.db.RecordAgentEvent(r.id, storage.AgentEvent{
		Kind: storage.AgentEventTurn, Outcome: outcome, DurationMs: &ms, Turn: turn, Round: rounds,
	})
	if o := observe.SessionOutcome(outcome); o != "" {
		if err := r.db.SetAgentSessionOutcome(r.id, o); err == nil {
			r.outcome = o
		}
	}
}

func (r *observeRecorder) signal(at observe.Pos, code, reason string) {
	if r == nil {
		return
	}
	_ = r.db.RecordAgentEvent(r.id, storage.AgentEvent{
		Kind: storage.AgentEventSignal, Outcome: code, Reason: reason, Turn: at.Turn, Round: at.Round,
	})
}

// gate records one quality-gate run. The suite rides in the event's tool
// column — it is what the verdict is a verdict of, the way a tool event's
// tool is what the outcome is an outcome of — and the verdict is the
// signal's qualifier, which is what a pass rate groups by.
//
// It carries no position, because a gate run has none: /gate run starts one
// in the background between turns, and a turn and a round would be real for
// the runs the model asked for and invented for the rest. The zero position
// is what the store already reads as "the recorder had no position".
func (r *observeRecorder) gate(suite, verdict string) {
	if r == nil {
		return
	}
	_ = r.db.RecordAgentEvent(r.id, storage.AgentEvent{
		Kind: storage.AgentEventSignal, Tool: suite, Outcome: observe.SignalGate, Reason: verdict,
	})
}

// link names the saved conversation the session is writing.
func (r *observeRecorder) link(name string) {
	if r == nil || name == "" || name == r.linked {
		return
	}
	if err := r.db.LinkAgentSession(r.id, name); err == nil {
		r.linked = name
	}
}

// end closes the session row, correcting the standing outcome only when
// there is none to stand. A session that reached its own exit having never
// closed a turn is abandoned: the process survived to say something, and
// what it says is that nothing was finished. Leaving the field empty is
// reserved for the session that never got to say anything at all, which
// reads as unknown — a different fact, and one about the record rather than
// about the work.
func (r *observeRecorder) end() {
	if r == nil {
		return
	}
	outcome := ""
	if r.outcome == "" {
		outcome = observe.SessionAbandoned
	}
	_ = r.db.EndAgentSession(r.id, outcome)
}

// endWith closes the row with an outcome the surface knows better than its
// turns do. A session whose program failed did not come out the way its last
// turn did, and the exit that reports the failure leaves no deferred close
// to run — so without this the row would keep the last turn's reading and a
// crashed session would be indistinguishable from one that finished well.
func (r *observeRecorder) endWith(outcome string) {
	if r == nil {
		return
	}
	r.outcome = outcome
	_ = r.db.EndAgentSession(r.id, outcome)
}

// restart closes this row and opens another for the conversation that
// follows it, reporting whether it could. The recorder itself is not
// replaced: every surface holding its observer goes on reporting through the
// same callbacks, which is what keeps "which row does this event belong to"
// a question with one answer rather than one per front-end.
//
// A row that cannot be opened leaves the recorder on the row it just closed.
// The alternative is a recorder that silently writes nothing for the rest of
// the process, and a session whose events land on a closed row is a smaller
// wrong than a session with no record at all.
func (r *observeRecorder) restart() bool {
	if r == nil {
		return false
	}
	r.end()
	id, err := r.db.StartAgentSession(r.kind, r.provider, r.model)
	if err != nil {
		return false
	}
	r.id, r.linked, r.outcome = id, "", ""
	return true
}

func newObserveCmd() *cobra.Command {
	var window string

	cmd := &cobra.Command{
		Use:   "observe",
		Short: "Show agent-session usage dashboards",
		Long:  "Display local, content-free metrics about agent sessions: usage and cost by day and model, tool mix, approval decisions, quality-gate verdicts, how sessions came out, and recent sessions.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			since, err := parseObserveWindow(window)
			if err != nil {
				return err
			}
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()
			return renderObserveDashboard(cmd, db, window, since)
		},
	}
	cmd.PersistentFlags().StringVar(&window, "window", "30d", "time window, in days (e.g. 7d, 30d)")

	var exportOut string
	var exportTranscript bool
	exportCmd := &cobra.Command{
		Use:   "export",
		Short: "Export recorded agent-session metrics as JSON",
		Long:  "Export every recorded session with its events. The export is content-free unless --transcript is given, which joins each session's saved conversation to it.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			since, err := parseObserveWindow(window)
			if err != nil {
				return err
			}
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			sessions, err := db.ExportAgentObservability(since, exportTranscript)
			if err != nil {
				return fmt.Errorf("export: %w", err)
			}
			if sessions == nil {
				sessions = []storage.AgentExportSession{}
			}
			payload := struct {
				Window   string                       `json:"window"`
				Sessions []storage.AgentExportSession `json:"sessions"`
			}{Window: window, Sessions: sessions}
			data, err := json.MarshalIndent(payload, "", "  ")
			if err != nil {
				return err
			}
			data = append(data, '\n')
			if exportOut == "" {
				_, err = cmd.OutOrStdout().Write(data)
				return err
			}
			if err := os.WriteFile(exportOut, data, 0o600); err != nil {
				return err
			}
			return report.Fprintln(cmd.OutOrStdout(), report.Done("wrote", exportOut+" · "+
				countOf(len(sessions), "session", "sessions")))
		},
	}
	exportCmd.Flags().StringVarP(&exportOut, "out", "o", "", "write to a file (user-only permissions) instead of stdout")
	exportCmd.Flags().BoolVar(&exportTranscript, "transcript", false, "join each session's saved conversation to its metrics (the export is no longer content-free)")

	sessionCmd := &cobra.Command{
		Use:   "session <id>",
		Short: "Show one recorded session as a timeline",
		Long:  "Print one session's provenance and its events in order, grouped by turn: tool calls with their duration and outcome, decisions, and the signals the loop raised.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil || id <= 0 {
				return fmt.Errorf("invalid session id %q", args[0])
			}
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()
			return renderObserveSession(cmd, db, id)
		},
	}

	var purgeYes bool
	purgeCmd := &cobra.Command{
		Use:   "purge",
		Short: "Delete all recorded agent-session metrics",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()
			if !purgeYes {
				fmt.Fprint(cmd.OutOrStdout(), "Delete every recorded session and its events? [y/N] ")
				var confirm string
				// No answer — a closed stdin — reads as an empty line, and an
				// empty line is No.
				_, _ = fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "Y" {
					return report.Fprintln(cmd.OutOrStdout(),
						report.Row{State: report.Skip, Subject: "cancelled", Detail: "nothing was deleted"})
				}
			}
			n, err := db.PurgeAgentObservability()
			if err != nil {
				return fmt.Errorf("purge: %w", err)
			}
			return report.Fprintln(cmd.OutOrStdout(),
				report.Done("purged", countOf(int(n), "recorded session", "recorded sessions")+" and their events"))
		},
	}

	purgeCmd.Flags().BoolVarP(&purgeYes, "yes", "y", false, "skip the confirmation")

	cmd.AddCommand(exportCmd, sessionCmd, purgeCmd, newObserveCompareCmd(&window))
	return cmd
}

// parseObserveWindow parses a day-granularity window like "7d" or "30d" into
// its cutoff time.
func parseObserveWindow(s string) (time.Time, error) {
	trimmed := strings.TrimSuffix(strings.TrimSpace(strings.ToLower(s)), "d")
	days, err := strconv.Atoi(trimmed)
	if err != nil || days <= 0 || !strings.HasSuffix(strings.ToLower(s), "d") {
		return time.Time{}, fmt.Errorf("invalid window %q (use a number of days, e.g. 7d, 30d)", s)
	}
	return time.Now().AddDate(0, 0, -days), nil
}

// renderObserveDashboard reads the store and prints the dashboard.
func renderObserveDashboard(cmd *cobra.Command, db *storage.DB, window string, since time.Time) error {
	data, err := readObserveData(db, window, since)
	if err != nil {
		return err
	}
	return report.Fprint(cmd.OutOrStdout(), observeReport(data))
}

// observeData is everything one dashboard reads out of the store, so building
// the report is a pure function of it and testable without a database.
type observeData struct {
	Window     string
	Sessions   []storage.AgentSessionSummary
	ByDay      []storage.AgentDayUsage
	ByModel    []storage.AgentModelUsage
	ToolMix    []storage.AgentToolUsage
	ToolErrors []storage.AgentToolErrorCount
	Decisions  []storage.AgentDecisionCount
	Turns      []storage.AgentTurnOutcome
	Signals    []storage.AgentSignalCount
	Gates      []storage.AgentGateVerdict
	Outcomes   []storage.AgentSessionOutcome
}

// readObserveData runs every aggregate the dashboard draws. Each query is
// named in its error so a failure says which reading is missing rather than
// that the dashboard broke.
func readObserveData(db *storage.DB, window string, since time.Time) (observeData, error) {
	data := observeData{Window: window}
	for _, q := range []struct {
		name string
		read func() error
	}{
		{"sessions", func() (err error) { data.Sessions, err = db.AgentSessions(since, 20); return }},
		{"usage by day", func() (err error) { data.ByDay, err = db.AgentUsageByDay(since); return }},
		{"usage by model", func() (err error) { data.ByModel, err = db.AgentUsageByModel(since); return }},
		{"tool mix", func() (err error) { data.ToolMix, err = db.AgentToolMix(since); return }},
		{"tool errors", func() (err error) { data.ToolErrors, err = db.AgentToolErrors(since); return }},
		{"decisions", func() (err error) { data.Decisions, err = db.AgentDecisions(since); return }},
		{"turns", func() (err error) { data.Turns, err = db.AgentTurns(since); return }},
		{"signals", func() (err error) { data.Signals, err = db.AgentSignals(since); return }},
		{"gate verdicts", func() (err error) { data.Gates, err = db.AgentGateVerdicts(since); return }},
		{"outcomes", func() (err error) { data.Outcomes, err = db.AgentSessionOutcomes(since); return }},
	} {
		if err := q.read(); err != nil {
			return observeData{}, fmt.Errorf("query %s: %w", q.name, err)
		}
	}
	return data, nil
}

// observeReport is the whole dashboard as one report: the sections the store
// can answer for, in the order a reader asks them — what it cost, what it
// ran, what it was allowed to do, whether it worked, and which sessions
// those were
// (docs/interface/surfaces.md#outside-the-tui). It was seven prose headings
// over seven tabwriter tables, which is seven shapes for one screen.
func observeReport(data observeData) report.Report {
	r := report.Report{Title: "shhh observe"}
	if len(data.Sessions) == 0 {
		r.Subject = "last " + data.Window
		return emptyInto(r, "no agent sessions in the last "+data.Window, "shhh chat")
	}

	var spend float64
	for _, s := range data.Sessions {
		spend += s.Cost
	}
	r.Subject = joinDetail(fmt.Sprintf("%s · last %s",
		countOf(len(data.Sessions), "session", "sessions"), data.Window), observeCost(spend))

	for _, section := range []report.Section{
		{Header: "BY DAY", Rows: observeDayRows(data.ByDay)},
		{Header: "BY MODEL", Rows: observeModelRows(data.ByModel)},
		{Header: "TOOLS", Rows: observeToolRows(data.ToolMix, data.ToolErrors)},
		{Header: "DECISIONS", Rows: observeDecisionRows(data.Decisions)},
		{Header: "TURNS", Rows: observeTurnRows(data.Turns)},
		{Header: "SIGNALS", Rows: observeSignalRows(data.Signals)},
		{Header: "GATE", Rows: observeGateRows(data.Gates)},
		{Header: "OUTCOMES", Rows: observeOutcomeRows(data.Outcomes)},
		{Header: "SESSIONS", Rows: observeSessionRows(data.Sessions)},
	} {
		if len(section.Rows) > 0 {
			r.Sections = append(r.Sections, section)
		}
	}
	r.Notes = append(r.Notes, report.Note{State: report.Run,
		Text: "`shhh observe session <id>` shows one session turn by turn"})
	return r
}

func observeDayRows(days []storage.AgentDayUsage) []report.Row {
	rows := make([]report.Row, 0, len(days))
	for _, u := range days {
		rows = append(rows, report.Row{State: report.Pass, Name: u.Day,
			Subject: countOf(u.Sessions, "session", "sessions"),
			Detail:  joinDetail(observeTokens(u.TokensIn, u.TokensOut), observeCost(u.Cost))})
	}
	return rows
}

func observeModelRows(models []storage.AgentModelUsage) []report.Row {
	rows := make([]report.Row, 0, len(models))
	for _, u := range models {
		rows = append(rows, report.Row{State: report.Pass, Name: u.Model,
			Subject: countOf(u.Sessions, "session", "sessions"),
			Detail:  joinDetail(observeTokens(u.TokensIn, u.TokensOut), observeCost(u.Cost)),
			Outcome: u.Provider})
	}
	return rows
}

// observeToolRows is the tool mix with each tool's failures folded in as the
// consequence line: an error rate is only actionable beside the class it is
// made of, and the classes were a table of their own before this.
func observeToolRows(mix []storage.AgentToolUsage, errs []storage.AgentToolErrorCount) []report.Row {
	classes := map[string][]string{}
	for _, e := range errs {
		class := e.Class
		if class == "" {
			class = "unclassified"
		}
		classes[e.Tool] = append(classes[e.Tool], fmt.Sprintf("%s %d", class, e.Count))
	}
	rows := make([]report.Row, 0, len(mix))
	for _, u := range mix {
		row := report.Row{State: report.Pass, Name: u.Tool,
			Subject: countOf(u.Count, "call", "calls"), Detail: latencyText(u.AvgDurationMs)}
		if u.ErrorRate > 0 {
			row.State = report.Warn
			row.Outcome = fmt.Sprintf("%.0f%% failed", u.ErrorRate*100)
			row.Consequence = strings.Join(classes[u.Tool], " · ")
		}
		rows = append(rows, row)
	}
	return rows
}

// observeDecisionRows names who decided, in the transcript's own words: a
// denial by a person is a preference and a denial by a rule is policy, and
// the rows say which (docs/interface/principles.md#weight-tracks-risk).
func observeDecisionRows(decisions []storage.AgentDecisionCount) []report.Row {
	rows := make([]report.Row, 0, len(decisions))
	for _, d := range decisions {
		// The verdict and its decider are one phrase, so they stay one field
		// rather than being split across the name column.
		rows = append(rows, report.Row{
			State:   observeDecisionState(d.Decision),
			Subject: components.OutcomeBy(observeDecisionWord(d.Decision), observeDecider(d.Reason)),
			Outcome: countOf(d.Count, "time", "times"),
		})
	}
	return rows
}

// observeDecisionWord is the verdict in the word the transcript uses for it.
func observeDecisionWord(decision string) string {
	switch decision {
	case "allow":
		return "allowed"
	case "deny":
		return "denied"
	case "ask":
		return "asked"
	}
	return decision
}

func observeDecisionState(decision string) report.State {
	switch decision {
	case "allow":
		return report.Pass
	case "deny":
		return report.Skip
	}
	return report.Warn
}

// observeDecider is who or what decided. A decision with no recorded reason
// was the person's, because the rules all record theirs.
func observeDecider(reason string) string {
	if reason == "" {
		return "you"
	}
	return "auto · " + reason
}

func observeTurnRows(turns []storage.AgentTurnOutcome) []report.Row {
	rows := make([]report.Row, 0, len(turns))
	for _, t := range turns {
		rows = append(rows, report.Row{
			State:   observeTurnState(t.Outcome),
			Name:    t.Outcome,
			Subject: countOf(t.Count, "turn", "turns"),
			Detail: joinDetail(fmt.Sprintf("%.1f rounds avg · %d max", t.AvgRounds, t.MaxRounds),
				latencyText(t.AvgDurationMs)),
		})
	}
	return rows
}

func observeTurnState(outcome string) report.State {
	switch outcome {
	case "failed":
		return report.Fail
	case "cancelled", "cap-paused":
		return report.Skip
	}
	return report.Pass
}

// observeSignalRows is the loop's own safeguards, with the gate left out: it
// has a section of its own below, and one fact counted twice on one screen
// reads as two.
func observeSignalRows(signals []storage.AgentSignalCount) []report.Row {
	rows := make([]report.Row, 0, len(signals))
	for _, s := range signals {
		if s.Signal == "gate" {
			continue
		}
		rows = append(rows, report.Row{State: report.Queue, Name: s.Signal,
			Subject: countOf(s.Count, "time", "times"), Detail: s.Reason})
	}
	return rows
}

// observeGateRows is one row per suite: how many runs, how many passed, and
// what the rest came out as. The pass rate leads because it is the only
// figure on this screen that judges the work rather than describing it — the
// checks are the project's own, and the gate ran them against a fingerprint
// of the tree.
//
// A run that was blocked or cancelled is named beside the failures and left
// out of the rate on both sides. It is not a failing check — it is no
// reading of the code at all — and counting it in the denominator is the
// same mistake as counting it in the numerator: a checkout with no gate
// config yet would report every suite at 0% passed, which is an
// infrastructure problem wearing a verdict's clothes. A suite with no
// verdict either way says so instead of printing a rate over nothing.
func observeGateRows(gates []storage.AgentGateVerdict) []report.Row {
	var rows []report.Row
	// The query returns a suite's verdicts together, so one pass folds each
	// run of them into a row.
	for i := 0; i < len(gates); {
		var runs, judged, passed int
		var rest []string
		j := i
		for ; j < len(gates) && gates[j].Suite == gates[i].Suite; j++ {
			runs += gates[j].Count
			switch gates[j].Verdict {
			case "pass":
				judged, passed = judged+gates[j].Count, passed+gates[j].Count
			case "fail":
				judged += gates[j].Count
				rest = append(rest, fmt.Sprintf("fail %d", gates[j].Count))
			default:
				rest = append(rest, fmt.Sprintf("%s %d", gates[j].Verdict, gates[j].Count))
			}
		}
		row := report.Row{State: report.Pass, Name: gates[i].Suite,
			Subject: countOf(runs, "run", "runs"), Outcome: "no verdict"}
		if judged > 0 {
			row.Outcome = fmt.Sprintf("%.0f%% passed", float64(passed)/float64(judged)*100)
		}
		if len(rest) > 0 {
			row.State = report.Warn
			row.Detail = strings.Join(rest, " · ")
		}
		rows = append(rows, row)
		i = j
	}
	return rows
}

// observeGateState is one gate verdict's weight. Blocked and cancelled are
// neither a pass nor a failure: the run produced no reading of the code, and
// drawing it as a failure is exactly the confusion the gate keeps them apart
// to avoid.
func observeGateState(verdict string) report.State {
	switch verdict {
	case "pass":
		return report.Pass
	case "fail":
		return report.Fail
	}
	return report.Skip
}

// observeOutcomeRows is the outcome mix: how the sessions in the window came
// out. Every other number on this screen is a description, and this is the
// column they are worth correlating against.
func observeOutcomeRows(outcomes []storage.AgentSessionOutcome) []report.Row {
	rows := make([]report.Row, 0, len(outcomes))
	for _, o := range outcomes {
		rows = append(rows, report.Row{State: observeOutcomeState(o.Outcome), Name: o.Outcome,
			Subject: countOf(o.Count, "session", "sessions")})
	}
	return rows
}

// observeRating is what a person said about the session, in the words the
// walk asked the question in — it did what was wanted, or it did not.
func observeRating(rating *bool) string {
	switch {
	case rating == nil:
		return ""
	case *rating:
		return "worked"
	}
	return "did not work"
}

// observeOutcomeState reads the outcome words this build's rows are written
// with, and the words earlier builds wrote theirs with. They are literals
// for the same reason the decision and turn words are: a case on a string
// constant compiles whatever its value, so pointing these at the constants
// would buy nothing and would assert that the renderer only ever reads rows
// this build wrote.
//
// Which is exactly why only `completed` draws as a pass. Reading rows this
// build did not write means a word this build has never seen is a live
// possibility, and the one thing it must not do is arrive wearing a tick.
func observeOutcomeState(outcome string) report.State {
	switch outcome {
	case "completed":
		return report.Pass
	case "error":
		return report.Fail
	case "interrupted", "abandoned":
		return report.Skip
	}
	return report.Queue
}

// observeSessionRows is the recent sessions: what kind of session it was, on
// what model, for how long, and what it cost. One that has not ended says
// `active` where the others say how long they took.
func observeSessionRows(sessions []storage.AgentSessionSummary) []report.Row {
	rows := make([]report.Row, 0, len(sessions))
	for _, s := range sessions {
		// When it ran leads the detail: a month of sessions with no date on
		// any of them is a list with no order the reader can see.
		row := report.Row{
			State:   report.Pass,
			Name:    strconv.FormatInt(s.ID, 10),
			Subject: joinDetail(s.Kind, s.Model),
			Detail: joinDetail(s.StartedAt.Local().Format("Jan 2 15:04"),
				joinDetail(countOf(int(s.Turns), "turn", "turns"),
					joinDetail(observeTokens(s.TokensIn, s.TokensOut), observeCost(s.Cost)))),
			Outcome: observeElapsed(s),
		}
		if s.EndedAt == nil {
			row.State = report.Run
		}
		rows = append(rows, row)
	}
	return rows
}

// observeElapsed is how long a session took, or `active` for one that is
// still going.
func observeElapsed(s storage.AgentSessionSummary) string {
	if s.EndedAt == nil {
		return "active"
	}
	return components.FormatElapsed(s.EndedAt.Sub(s.StartedAt).Round(time.Second))
}

// observeTokens is the vitals rail's usage segment; nothing at all where
// nothing was counted.
func observeTokens(in, out int64) string {
	if in == 0 && out == 0 {
		return ""
	}
	return "↑" + tokenCount(in) + " ↓" + tokenCount(out)
}

// observeCost is a spend in dollars, and nothing where there is none to
// report: `$0.0000` says a session was almost free when what it means is that
// nobody knows what its model costs
// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
func observeCost(v float64) string {
	switch {
	case v <= 0:
		return ""
	case v < 0.01:
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", v)
}

// renderObserveSession prints one session: its provenance, then its events
// in order under a section per turn, so a reader can see where the rounds
// went and where the loop's safeguards spoke.
func renderObserveSession(cmd *cobra.Command, db *storage.DB, id int64) error {
	s, ok, err := db.AgentSession(id)
	if err != nil {
		return fmt.Errorf("query session: %w", err)
	}
	if !ok {
		return fmt.Errorf("no recorded session %d", id)
	}
	events, err := db.AgentSessionEvents(id)
	if err != nil {
		return fmt.Errorf("query events: %w", err)
	}
	return report.Fprint(cmd.OutOrStdout(), observeSessionReport(s, events))
}

// observeSessionReport builds that page. It is separate from the query so
// the whole render can be held against a fixture: every event kind this
// draws is a code some surface reports, and a surface that starts reporting
// through a different path must still land on the same page.
func observeSessionReport(s storage.AgentSessionSummary, events []storage.AgentExportEvent) report.Report {
	pairs := []report.Pair{
		{Key: "started", Value: s.StartedAt.Local().Format("Jan 2 15:04")},
		{Key: "model", Value: joinDetail(s.Provider, s.Model)},
		{Key: "turns", Value: strconv.FormatInt(s.Turns, 10)},
	}
	for _, p := range []report.Pair{
		{Key: "outcome", Value: s.Outcome},
		// The rating sits next to the outcome because that is what it is for:
		// the outcome is inferred from how the session ended, and the two
		// side by side is how anyone finds out whether the inference is any
		// good (docs/capabilities/sessions-and-memory.md#a-rating-is-how-you-check-the-inference).
		// A session nobody has answered for prints no row at all, which is a
		// different fact from one somebody disliked.
		{Key: "rated", Value: observeRating(s.Rating)},
		{Key: "tokens", Value: observeTokens(s.TokensIn, s.TokensOut)},
		{Key: "cost", Value: observeCost(s.Cost)},
		{Key: "version", Value: s.Version},
		{Key: "prompt", Value: s.PromptHash},
		{Key: "project", Value: s.Project},
		{Key: "conversation", Value: s.ChatSession},
	} {
		if p.Value != "" {
			pairs = append(pairs, p)
		}
	}
	if s.Skills > 0 {
		pairs = append(pairs, report.Pair{Key: "skills", Value: strconv.Itoa(s.Skills)})
	}
	if s.ParentID != nil {
		pairs = append(pairs, report.Pair{Key: "child of", Value: strconv.FormatInt(*s.ParentID, 10)})
	}
	pairs = append(pairs, observeSettingsPairs(s.Settings)...)

	r := report.Report{
		Title:    "shhh observe session " + strconv.FormatInt(s.ID, 10),
		Subject:  joinDetail(s.Kind, observeElapsed(s)),
		Sections: []report.Section{{Pairs: pairs}},
	}

	if len(events) == 0 {
		return emptyInto(r, "no events recorded for this session", "shhh observe")
	}
	turn := int64(-1)
	for _, e := range events {
		// An event with no position joins the section it landed in, which
		// is the turn it arrived during. Only the ones recorded before the
		// first turn opened get a heading of their own: a gate verdict
		// takes no position at all, and a section of its own for each would
		// split the turn it arrived in two and claim the second half
		// started over. The cost is that a background run landing after the
		// last turn closed is drawn under that turn, which is where it
		// arrived rather than where it belongs.
		unplaced := e.Turn == 0 && turn > 0
		if e.Turn != turn && !unplaced {
			turn = e.Turn
			header := fmt.Sprintf("TURN %d", turn)
			if turn == 0 {
				header = "BEFORE THE FIRST TURN"
			}
			r.Sections = append(r.Sections, report.Section{Header: header})
		}
		last := &r.Sections[len(r.Sections)-1]
		last.Rows = append(last.Rows, observeEventRow(e))
	}
	return r
}

// observeSettingsPairs is what the session ran under, beside the provenance
// it already prints: one line per setting that was in force, and nothing at
// all for a session recorded before settings were — the page must not fill
// the gap with today's defaults, because a reader comparing two sessions
// would take the fill for a fact.
func observeSettingsPairs(c *storage.AgentSettings) []report.Pair {
	if c == nil {
		return nil
	}
	rounds := "uncapped"
	if c.MaxRounds > 0 {
		rounds = strconv.Itoa(c.MaxRounds)
	}
	summary := "off"
	if c.SummaryEnabled {
		summary = joinDetail(c.SummaryModel, fmt.Sprintf("every %d rounds", c.SummaryInterval))
	}
	var pairs []report.Pair
	for _, p := range []report.Pair{
		{Key: "mode", Value: c.Mode},
		{Key: "reasoning", Value: c.Reasoning},
		{Key: "rounds", Value: rounds},
		{Key: "summary", Value: summary},
		{Key: "classifier", Value: c.ClassifierModel},
		{Key: "sandbox", Value: c.SandboxProfile},
		{Key: "config", Value: c.ConfigHash},
	} {
		if p.Value != "" {
			pairs = append(pairs, p)
		}
	}
	return pairs
}

// observeEventRow is one recorded event on the grid: what kind of thing
// happened, to what, and how it came out.
func observeEventRow(e storage.AgentExportEvent) report.Row {
	at := e.CreatedAt
	if t, err := time.Parse(observeTimeLayout, e.CreatedAt); err == nil {
		at = t.Local().Format("15:04:05")
	}
	row := report.Row{State: report.Pass, Name: at, Outcome: e.Outcome}
	switch e.Kind {
	case storage.AgentEventTool:
		row.Subject, row.Detail = e.Tool, joinDetail(e.Reason, fmtEventMs(e.DurationMs))
		if e.Outcome == "error" {
			row.State = report.Fail
		}
	case storage.AgentEventDecision:
		row.State = observeDecisionState(e.Outcome)
		row.Subject, row.Outcome = "decision", components.OutcomeBy(
			observeDecisionWord(e.Outcome), observeDecider(e.Reason))
	case storage.AgentEventSignal:
		// The gate is the one signal with a subject as well as a qualifier,
		// and the one whose qualifier is a judgement rather than a
		// description — so its row carries the suite and takes the verdict's
		// own weight instead of the neutral one every other signal draws at.
		row.State, row.Subject, row.Detail = report.Queue, e.Outcome, joinDetail(e.Tool, e.Reason)
		row.Outcome = "signal"
		if e.Outcome == "gate" {
			row.State = observeGateState(e.Reason)
		}
	case storage.AgentEventTurn:
		row.State = observeTurnState(e.Outcome)
		row.Subject = "turn"
		row.Detail = joinDetail(fmt.Sprintf("%d rounds", e.Round), fmtEventMs(e.DurationMs))
	default:
		row.Subject, row.Detail = e.Kind, e.Reason
	}
	if e.Round > 0 && e.Kind != storage.AgentEventTurn {
		row.Detail = joinDetail(fmt.Sprintf("r%d", e.Round), row.Detail)
	}
	return row
}

// observeTimeLayout is how storage stamps event times.
const observeTimeLayout = "2006-01-02T15:04:05.000Z"

func fmtEventMs(ms *int64) string {
	if ms == nil {
		return ""
	}
	return (time.Duration(*ms) * time.Millisecond).String()
}

// compareMinSessions is the fewest sessions a cohort may hold and still have
// its rates compared against another's.
//
// It is ten, and the argument is about what the number is used for rather
// than about a confidence interval this command deliberately does not
// compute. At ten, one sitting is a tenth of the cohort: the afternoon a
// dependency broke and every turn ended in a tool error can move a rate by
// its own share and no further, which is smaller than the difference anybody
// would act on. At six — the size that motivates the rule — one sitting is a
// sixth, and two of them going the same way produce a forty-percent
// difference out of nothing at all. A row that prints such a difference
// invites acting on it, and there is no way to tell from a percentage that
// it was made of two sessions.
//
// A cohort under it prints its count and no rate. That is the whole
// treatment: not a caveat beside a number, because a caveat beside a number
// loses to the number
// (docs/capabilities/sessions-and-memory.md#a-comparison-is-two-cohorts-as-rates).
const compareMinSessions = 10

// observeFigure is how one comparable figure is written down. It travels
// with the figure rather than being decided at the row, so the same number
// reads the same way in the report and in the export.
type observeFigure int

const (
	// observeRate is a count over a denominator — steers per turn, calls per
	// turn. Two decimals, because the ones worth reading are under one.
	observeRate observeFigure = iota
	// observeRounds is rounds per turn, at the precision the dashboard
	// already prints them at.
	observeRounds
	// observeShare is a proportion of a population, written as a percentage.
	observeShare
	// observeMoney is a spend.
	observeMoney
	// observeTokenRate is tokens over a denominator.
	observeTokenRate
)

// text writes one value of this figure.
func (f observeFigure) text(v float64) string {
	switch f {
	case observeRounds:
		return fmt.Sprintf("%.1f", v)
	case observeShare:
		return fmt.Sprintf("%.0f%%", v*100)
	case observeMoney:
		return observeSpend(v)
	case observeTokenRate:
		return tokenCount(int64(math.Round(v)))
	}
	return fmt.Sprintf("%.2f", v)
}

// observeSpend is a spend inside a comparison. It differs from observeCost
// in one place and for one reason: a cohort that spent nothing here has to
// say so, because the row beside it is a number and an empty half of an
// arrow reads as a rendering fault rather than as an absence.
func observeSpend(v float64) string {
	switch {
	case v <= 0:
		return "none"
	case v < 0.01:
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", v)
}

// observeChange is one figure taken over both cohorts: what it was, what it
// became, and by how much. The report and the JSON are both rendered from
// this list and from nothing else, so the export cannot come to carry a
// different set of numbers from the screen.
type observeChange struct {
	// Section is the block the row is drawn under, and also the order: the
	// list is already in the order it is read in.
	Section string `json:"section"`
	Name    string `json:"name"`
	// Qualifier is what distinguishes two rows of the same name — which
	// decider allowed a call, which reason a signal fired for.
	Qualifier string `json:"qualifier,omitempty"`
	// Unit is what the two values are counted in, in the words the row
	// prints them in.
	Unit   string  `json:"unit"`
	Before float64 `json:"before"`
	After  float64 `json:"after"`
	// Delta is the difference in the figure's own units, and Change the
	// difference as a proportion of what it was. Change is absent where the
	// earlier cohort's figure is zero: there is no ratio to take, and
	// printing one over a denominator of nothing is the arithmetic this
	// command exists to refuse.
	Delta  float64  `json:"delta"`
	Change *float64 `json:"change,omitempty"`
	// Beside is a second figure the first one only means anything next to.
	// Rounds per turn is the case that demands it: a change that made turns
	// shorter by making them fail is an improvement on the unqualified
	// number, so the share of turns that came out that way is drawn on the
	// same row rather than in a section a reader might not reach.
	Beside *observeChange `json:"beside,omitempty"`
	form   observeFigure
	// digits is how many decimal places a share is written to, settled per
	// column by observeShareColumns rather than per row.
	digits int
	// key is how the same row is found on both sides of the comparison. It
	// is not the name: two decision rows are both `denied` and differ only
	// in who decided.
	key string
}

// figures is the pair the row leads with: `3.2 → 2.7 rounds`.
func (c observeChange) figures() string {
	pair := c.figure(c.Before) + " → " + c.figure(c.After)
	if c.Unit == "" {
		return pair
	}
	return pair + " " + c.Unit
}

// figure writes one of the pair, at the precision its column was settled at.
func (c observeChange) figure(v float64) string {
	if c.form == observeShare {
		return fmt.Sprintf("%.*f%%", c.digits, v*100)
	}
	return c.form.text(v)
}

// observeShareColumns settles how precisely each column of shares is
// written: a tool error rate of 2.5% and one of 1.6% are the same whole
// number and a very different week, while a gate pass rate of 67% gains
// nothing from a decimal and pays a column of width for it.
//
// It is decided per column and not per row. A block whose rows disagree
// about their decimal reads as a rendering fault, and the reason the decimal
// is needed at all — that the difference would otherwise round away — is a
// property of what the column measures rather than of one row in it. A share
// beside another figure is its own column, since it is read down the detail
// field rather than against the values above it.
func observeShareColumns(changes []observeChange) {
	each := func(fn func(*observeChange)) {
		for i := range changes {
			if changes[i].form == observeShare {
				fn(&changes[i])
			}
			if b := changes[i].Beside; b != nil && b.form == observeShare {
				fn(b)
			}
		}
	}
	largest := map[string]float64{}
	each(func(c *observeChange) {
		key := c.Section + " " + c.Unit
		largest[key] = math.Max(largest[key], math.Max(math.Abs(c.Before), math.Abs(c.After)))
	})
	each(func(c *observeChange) {
		if largest[c.Section+" "+c.Unit] < 0.1 {
			c.digits = 1
		}
	})
}

// direction is the size and sign of the change.
//
// A share moves in points and everything else moves in percent. The
// distinction is not pedantry: a gate pass rate going from 75% to 92% is
// seventeen points and twenty-three percent, and a reader who takes one for
// the other is wrong by the whole difference between them.
//
// A change too small to print is called unchanged rather than rendered as a
// signed zero, which reads as a rounding fault and tells the reader nothing
// either way.
func (c observeChange) direction() string {
	if c.form == observeShare {
		points := c.Delta * 100
		if smallest := 0.5 / math.Pow10(c.digits); math.Abs(points) >= smallest {
			return fmt.Sprintf("%+.*f pts", c.digits, points)
		}
		return "unchanged"
	}
	if c.Change == nil {
		// Nothing to divide by: the figure is new in the later cohort, which
		// is a fact worth the row and not a percentage.
		return "none before"
	}
	if pct := *c.Change * 100; math.Abs(pct) >= 0.5 {
		return fmt.Sprintf("%+.0f%%", pct)
	}
	return "unchanged"
}

// observeChangeOf builds one comparable figure.
func observeChangeOf(section, name, unit string, form observeFigure, before, after float64) observeChange {
	c := observeChange{Section: section, Name: name, Unit: unit, form: form,
		Before: before, After: after, Delta: after - before}
	if before != 0 {
		rel := (after - before) / before
		c.Change = &rel
	}
	return c
}

// observeCohortData is one side of the comparison: the sessions that ran
// under one value of the split key, and every aggregate over them.
type observeCohortData struct {
	storage.AgentCohort
	Reading storage.AgentCohortReading
}

// turns is the denominator every per-turn rate is over. It counts turn
// events rather than the sessions table's own column because the numerators
// are events too, and a rate whose halves are counted by different
// mechanisms is a rate that drifts when one of them stops being written.
func (c *observeCohortData) turns() float64 {
	var n int
	for _, t := range c.Reading.Turns {
		n += t.Count
	}
	return float64(n)
}

// calls is the denominator a tool error rate is over.
func (c *observeCohortData) calls() float64 {
	var n int
	for _, t := range c.Reading.Tools {
		n += t.Count
	}
	return float64(n)
}

// completed is how many of the cohort's sessions finished their work. The
// word is a literal for the same reason the outcome renderer's are: this
// reads rows written by every build that ever wrote one.
func (c *observeCohortData) completed() float64 {
	for _, o := range c.Reading.Outcomes {
		if o.Outcome == "completed" {
			return float64(o.Count)
		}
	}
	return 0
}

// MarshalJSON writes a cohort as the denominators the report's header
// prints, and not as the aggregates behind them. The figures are in the
// changes; a second copy of the raw counts beside them would be a second
// answer to the same question, and the two would be read against each other.
func (c observeCohortData) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Value     string  `json:"value"`
		Sessions  int     `json:"sessions"`
		Completed int     `json:"completed_sessions"`
		Turns     int     `json:"turns"`
		ToolCalls int     `json:"tool_calls"`
		TokensIn  int64   `json:"tokens_in"`
		TokensOut int64   `json:"tokens_out"`
		Cost      float64 `json:"est_cost"`
		First     string  `json:"first_session"`
		Last      string  `json:"last_session"`
	}{
		Value: c.Value, Sessions: c.Sessions, Completed: int(c.completed()),
		Turns: int(c.turns()), ToolCalls: int(c.calls()),
		TokensIn: c.TokensIn, TokensOut: c.TokensOut, Cost: c.Cost,
		First: c.First.UTC().Format(observeTimeLayout),
		Last:  c.Last.UTC().Format(observeTimeLayout),
	})
}

// observeCompareData is one comparison, as both the report and the export
// read it.
type observeCompareData struct {
	Window string `json:"window"`
	Split  string `json:"split"`
	// Sessions is how many of the window's sessions carry a value for the
	// split key at all.
	Sessions int `json:"sessions"`
	// Earlier and Later are the two cohorts compared, ordered by when each
	// was first seen. They are nil when the window holds fewer than two
	// values to compare.
	Earlier *observeCohortData `json:"earlier,omitempty"`
	Later   *observeCohortData `json:"later,omitempty"`
	// Others are the values left out, largest first.
	Others []string `json:"others,omitempty"`
	// MinSessions is the threshold, carried so a reader of the export knows
	// what Comparable was decided against.
	MinSessions int `json:"min_sessions"`
	// Comparable is false when either cohort is under that threshold, and
	// Changes is then empty: a comparison that cannot be read is not
	// rendered with a caveat, it is not rendered.
	Comparable bool            `json:"comparable"`
	Changes    []observeChange `json:"changes,omitempty"`
}

// readObserveCompare reads both cohorts out of the store. The two compared
// are the two with the most sessions, which is also what the small-n rule
// would leave standing: taking the two most recent instead would hand the
// comparison to a value that appeared twice yesterday, and taking every
// value would print a screen of cohorts too small to read.
func readObserveCompare(db *storage.DB, window, split string, since time.Time) (observeCompareData, error) {
	cohorts, err := db.AgentCohorts(since, split)
	if err != nil {
		return observeCompareData{}, fmt.Errorf("query cohorts: %w", err)
	}
	data := observeCompareData{Window: window, Split: split, MinSessions: compareMinSessions}
	for _, c := range cohorts {
		data.Sessions += c.Sessions
	}
	if len(cohorts) < 2 {
		return data, nil
	}
	// The store returns them largest first, so the two at the head are the
	// pair; which of them is the earlier is what their first sessions say.
	pair := []storage.AgentCohort{cohorts[0], cohorts[1]}
	if pair[1].First.Before(pair[0].First) {
		pair[0], pair[1] = pair[1], pair[0]
	}
	sides := make([]*observeCohortData, len(pair))
	for i, c := range pair {
		reading, err := db.ReadAgentCohort(since, split, c.Value)
		if err != nil {
			return observeCompareData{}, err
		}
		sides[i] = &observeCohortData{AgentCohort: c, Reading: reading}
	}
	for _, c := range cohorts[2:] {
		data.Others = append(data.Others, c.Value)
	}
	data.Earlier, data.Later = sides[0], sides[1]
	return observeCompared(data), nil
}

// observeCompared finishes a comparison once both cohorts have been read:
// whether it can be read at all, and the figures if it can. It is separate
// from the store so the whole screen can be held against a fixture.
func observeCompared(data observeCompareData) observeCompareData {
	if data.Earlier == nil || data.Later == nil {
		return data
	}
	data.Comparable = data.Earlier.Sessions >= compareMinSessions &&
		data.Later.Sessions >= compareMinSessions
	if data.Comparable {
		data.Changes = observeCompareChanges(data.Earlier, data.Later)
	}
	return data
}

// observeCompareChanges is every figure the comparison draws, in the order
// it is read in.
//
// The rates that answer "did the change help" lead, and the descriptions
// follow: how often a turn had to be corrected, how many rounds a turn took
// at equal outcome, how often a tool failed and how, whether the gate
// passed, and what a finished session cost. Each fact appears once — the
// steering signals are dropped from the signal block and the gate from
// nowhere else — because one fact counted twice on one screen reads as two.
//
// Every figure is a rate, and that is the point rather than a presentation
// choice: two cohorts either side of a change are never the same size, and a
// count row would read as a change that is only a change in how much work
// went through
// (docs/capabilities/sessions-and-memory.md#a-comparison-is-two-cohorts-as-rates).
func observeCompareChanges(earlier, later *observeCohortData) []observeChange {
	var out []observeChange
	out = append(out, observeSteeringChanges(earlier, later)...)
	out = append(out, observeRoundsChanges(earlier, later)...)
	out = append(out, observeToolErrorChanges(earlier, later)...)
	out = append(out, observeGateChanges(earlier, later)...)
	out = append(out, observeCostChanges(earlier, later)...)
	out = append(out, observeToolChanges(earlier, later)...)
	out = append(out, observeDecisionChanges(earlier, later)...)
	out = append(out, observeSignalChanges(earlier, later)...)
	out = append(out, observeOutcomeChanges(earlier, later)...)
	observeShareColumns(out)
	return out
}

// observeTally is one aggregate reduced to a number per row, keyed so the
// same row can be found on both sides and named so it can be drawn.
type observeTally struct {
	order []string
	rows  map[string]observeTallyRow
}

type observeTallyRow struct {
	name, qualifier string
	value           float64
}

func (t *observeTally) add(key, name, qualifier string, v float64) {
	if t.rows == nil {
		t.rows = map[string]observeTallyRow{}
	}
	if _, seen := t.rows[key]; !seen {
		t.order = append(t.order, key)
	}
	row := t.rows[key]
	t.rows[key] = observeTallyRow{name: name, qualifier: qualifier, value: row.value + v}
}

func (t *observeTally) value(key string) float64 { return t.rows[key].value }

// observeTallyRows compares two tallies row by row, each over its own
// cohort's denominator. That division is the whole point of the screen: the
// two cohorts are never the same size, and every count row would otherwise
// read as a change that is only a change in how much work went through.
//
// The later cohort's order leads, because it is the side being asked about,
// and a row present on only one side still gets drawn — a signal that fired
// for the first time after a change is exactly what somebody is looking for.
func observeTallyRows(section, unit string, form observeFigure,
	earlier, later observeTally, earlierDen, laterDen float64) []observeChange {
	if earlierDen <= 0 || laterDen <= 0 {
		// A rate over no denominator is not a small number, it is no number.
		return nil
	}
	var out []observeChange
	for _, key := range append(append([]string{}, later.order...), earlier.order...) {
		if _, drawn := observeChangeIndex(out, key); drawn {
			continue
		}
		before, after := earlier.value(key)/earlierDen, later.value(key)/laterDen
		if before == 0 && after == 0 {
			continue
		}
		row := later.rows[key]
		if row.name == "" {
			row = earlier.rows[key]
		}
		c := observeChangeOf(section, row.name, unit, form, before, after)
		c.Qualifier = row.qualifier
		c.key = key
		out = append(out, c)
	}
	return out
}

// observeChangeIndex finds a key among the rows already built, so the two
// cohorts' orders can be concatenated rather than merged.
func observeChangeIndex(out []observeChange, key string) (int, bool) {
	for i, c := range out {
		if c.key == key {
			return i, true
		}
	}
	return 0, false
}

// observeSteeringChanges is how often a turn was corrected while it ran.
//
// The person's steer and the session's own intervention are separate rows
// and never one figure. A change to the interruption machinery that makes
// the session take stock more often so that the person has to break in less
// is the outcome that machinery exists for, and one number over the two
// nets exactly that improvement out to nothing.
func observeSteeringChanges(earlier, later *observeCohortData) []observeChange {
	tally := func(c *observeCohortData) observeTally {
		var t observeTally
		for _, s := range c.Reading.Signals {
			if s.Signal == "steered" || s.Signal == "intervened" {
				t.add(s.Signal+"/"+s.Reason, s.Signal, s.Reason, float64(s.Count))
			}
		}
		return t
	}
	return observeTallyRows("steering", "per turn", observeRate,
		tally(earlier), tally(later), earlier.turns(), later.turns())
}

// observeRoundsChanges is rounds per turn, grouped by how the turn came out
// first — the qualification is the metric. Each row carries the share of
// turns that ended that way beside its rounds, so a cohort that got shorter
// turns by failing more of them cannot read as an improvement.
func observeRoundsChanges(earlier, later *observeCohortData) []observeChange {
	rounds := func(c *observeCohortData) observeTally {
		var t observeTally
		for _, o := range c.Reading.Turns {
			t.add(o.Outcome, o.Outcome, "", o.AvgRounds)
		}
		return t
	}
	share := func(c *observeCohortData) observeTally {
		var t observeTally
		for _, o := range c.Reading.Turns {
			t.add(o.Outcome, o.Outcome, "", float64(o.Count))
		}
		return t
	}
	// The share is what the rows are built from, and the rounds are hung on
	// them afterwards. The other way round loses a whole block to a surface
	// that records no rounds at all — a one-shot's single turn is round
	// zero on both sides — and it would take the turn mix down with it,
	// which is the qualification the block exists for.
	earlierRounds, laterRounds := rounds(earlier), rounds(later)
	out := observeTallyRows("rounds per turn", "of turns", observeShare,
		share(earlier), share(later), earlier.turns(), later.turns())
	for i := range out {
		beside := out[i]
		c := observeChangeOf("rounds per turn", beside.Name, "rounds", observeRounds,
			earlierRounds.value(beside.key), laterRounds.value(beside.key))
		c.key, c.Beside = beside.key, &beside
		out[i] = c
	}
	return out
}

// observeToolErrorChanges is the tool error rate by class, over every call
// the cohort made. The class is what makes the number actionable: a rate
// that rose on bad arguments is a prompt problem and one that rose on scope
// is a policy problem, and the two want opposite responses.
func observeToolErrorChanges(earlier, later *observeCohortData) []observeChange {
	tally := func(c *observeCohortData) observeTally {
		var t observeTally
		for _, e := range c.Reading.ToolErrors {
			class := e.Class
			if class == "" {
				class = "unclassified"
			}
			t.add(class, class, "", float64(e.Count))
		}
		return t
	}
	return observeTallyRows("tool errors", "of calls", observeShare,
		tally(earlier), tally(later), earlier.calls(), later.calls())
}

// observeGateChanges is each suite's pass rate. A run that was blocked or
// cancelled is out of the rate on both sides, the way it is on the
// dashboard: it is no reading of the code rather than a bad one, and a
// suite with no verdict either way gets no row instead of a rate over
// nothing.
func observeGateChanges(earlier, later *observeCohortData) []observeChange {
	judged := func(c *observeCohortData) (observeTally, observeTally) {
		var passed, ran observeTally
		for _, g := range c.Reading.Gates {
			switch g.Verdict {
			case "pass":
				passed.add(g.Suite, g.Suite, "", float64(g.Count))
				ran.add(g.Suite, g.Suite, "", float64(g.Count))
			case "fail":
				ran.add(g.Suite, g.Suite, "", float64(g.Count))
			}
		}
		return passed, ran
	}
	earlierPassed, earlierRan := judged(earlier)
	laterPassed, laterRan := judged(later)
	var out []observeChange
	for _, key := range append(append([]string{}, laterRan.order...), earlierRan.order...) {
		if _, drawn := observeChangeIndex(out, key); drawn {
			continue
		}
		before, after := earlierRan.value(key), laterRan.value(key)
		if before == 0 || after == 0 {
			// One side never got a verdict for this suite, so there is no
			// pair to compare.
			continue
		}
		c := observeChangeOf("gate", key, "passed", observeShare,
			earlierPassed.value(key)/before, laterPassed.value(key)/after)
		c.key = key
		out = append(out, c)
	}
	return out
}

// observeCostChanges is what the work cost, per session and per session that
// finished. The second is the one that answers the question: a cohort that
// abandoned half its sessions spent the same money for half the work, and
// only the completed denominator says so.
func observeCostChanges(earlier, later *observeCohortData) []observeChange {
	var out []observeChange
	// A cohort whose model the pricing table does not carry spent nothing
	// the record can see, and rows of "none → none" describe the pricing
	// table rather than the change being measured.
	priced := earlier.Cost > 0 || later.Cost > 0
	// The denominator is the unit rather than the name: two rows both about
	// cost line up under one word, and what differs between them is what
	// each was divided by, which is the thing being read.
	if priced && earlier.Sessions > 0 && later.Sessions > 0 {
		out = append(out, observeChangeOf("cost", "cost", "per session", observeMoney,
			earlier.Cost/float64(earlier.Sessions), later.Cost/float64(later.Sessions)))
	}
	if priced && earlier.completed() > 0 && later.completed() > 0 {
		out = append(out, observeChangeOf("cost", "cost", "per completed session", observeMoney,
			earlier.Cost/earlier.completed(), later.Cost/later.completed()))
	}
	if earlier.turns() > 0 && later.turns() > 0 {
		out = append(out, observeChangeOf("cost", "tokens", "per turn", observeTokenRate,
			float64(earlier.TokensIn+earlier.TokensOut)/earlier.turns(),
			float64(later.TokensIn+later.TokensOut)/later.turns()))
	}
	return out
}

// observeToolChanges is how often each tool was reached for, per turn, with
// its own failure rate beside it. A tool called half as often for the same
// work is the shape a prompt change makes.
func observeToolChanges(earlier, later *observeCohortData) []observeChange {
	calls := func(c *observeCohortData) observeTally {
		var t observeTally
		for _, u := range c.Reading.Tools {
			t.add(u.Tool, u.Tool, "", float64(u.Count))
		}
		return t
	}
	// The failure rate is already a rate: the aggregate averages it over the
	// cohort's own calls to that tool, so it is carried across as it stands
	// rather than multiplied back into a count and divided by the same count
	// again.
	failed := func(c *observeCohortData) observeTally {
		var t observeTally
		for _, u := range c.Reading.Tools {
			t.add(u.Tool, u.Tool, "", u.ErrorRate)
		}
		return t
	}
	earlierCalls, laterCalls := calls(earlier), calls(later)
	earlierFailed, laterFailed := failed(earlier), failed(later)
	out := observeTallyRows("tools", "per turn", observeRate,
		earlierCalls, laterCalls, earlier.turns(), later.turns())
	for i := range out {
		key := out[i].key
		// A tool only one cohort ever reached for has no pair of failure
		// rates: the zero on the side that never called it would read as a
		// tool that never failed rather than one that was never used.
		if earlierCalls.value(key) == 0 || laterCalls.value(key) == 0 {
			continue
		}
		rate := observeChangeOf("tools", out[i].Name, "failed", observeShare,
			earlierFailed.value(key), laterFailed.value(key))
		if rate.Before == 0 && rate.After == 0 {
			continue
		}
		out[i].Beside = &rate
	}
	return out
}

// observeDecisionChanges is how often the permission policy was asked and
// what it said, per turn. The decider stays on the row: a denial by a person
// is a preference and a denial by a rule is policy, and a change that moved
// work from one to the other is the change worth seeing.
func observeDecisionChanges(earlier, later *observeCohortData) []observeChange {
	tally := func(c *observeCohortData) observeTally {
		var t observeTally
		for _, d := range c.Reading.Decisions {
			t.add(d.Decision+"/"+d.Reason, observeDecisionWord(d.Decision),
				observeDecider(d.Reason), float64(d.Count))
		}
		return t
	}
	return observeTallyRows("decisions", "per turn", observeRate,
		tally(earlier), tally(later), earlier.turns(), later.turns())
}

// observeSignalChanges is the rest of the loop's safeguards, per turn. The
// steering signals are left out because they lead the screen, and the gate
// because it has a block of its own.
func observeSignalChanges(earlier, later *observeCohortData) []observeChange {
	tally := func(c *observeCohortData) observeTally {
		var t observeTally
		for _, s := range c.Reading.Signals {
			switch s.Signal {
			case "steered", "intervened", "gate":
				continue
			}
			t.add(s.Signal+"/"+s.Reason, s.Signal, s.Reason, float64(s.Count))
		}
		return t
	}
	return observeTallyRows("signals", "per turn", observeRate,
		tally(earlier), tally(later), earlier.turns(), later.turns())
}

// observeOutcomeChanges is the share of sessions that came out each way. It
// closes the screen because it is the column every rate above it is worth
// correlating against.
func observeOutcomeChanges(earlier, later *observeCohortData) []observeChange {
	tally := func(c *observeCohortData) observeTally {
		var t observeTally
		for _, o := range c.Reading.Outcomes {
			t.add(o.Outcome, o.Outcome, "", float64(o.Count))
		}
		return t
	}
	return observeTallyRows("outcomes", "of sessions", observeShare,
		tally(earlier), tally(later), float64(earlier.Sessions), float64(later.Sessions))
}

// observeCompareReport draws the comparison.
//
// No row carries a tick or a cross. Every figure on this screen is a
// difference, and which direction is the good one is the reader's to decide:
// fewer rounds is an improvement unless the turns got shorter by failing,
// and more interventions is a regression unless they are what stopped the
// person from having to break in. A glyph that guessed would be wrong often
// enough to be worth nothing and confident every time
// (docs/interface/principles.md#colour-never-carries-meaning-alone).
func observeCompareReport(data observeCompareData) report.Report {
	r := report.Report{
		Title: "shhh observe compare",
		Subject: joinDetail(data.Split, joinDetail(
			countOf(data.Sessions, "session", "sessions"), "last "+data.Window)),
	}
	// The way out names no --split: whoever is reading this typed one, and
	// the thing to change is the window.
	const wayOut = "shhh observe compare --window 90d"
	if data.Earlier == nil || data.Later == nil {
		// One value in the window is not a comparison with one side at
		// nothing; it is a window with nothing to compare. Every rate would
		// otherwise read as having appeared or vanished entirely.
		if data.Sessions == 0 {
			return emptyInto(r, "no sessions in the last "+data.Window+" carry a "+data.Split, "shhh chat")
		}
		return emptyInto(r, "only one "+data.Split+" in the last "+data.Window, wayOut)
	}

	r.Sections = append(r.Sections, report.Section{Header: "COHORTS", Rows: []report.Row{
		observeCohortRow("earlier", data.Earlier),
		observeCohortRow("later", data.Later),
	}})

	if !data.Comparable {
		short, side := data.Earlier, "earlier"
		if data.Later.Sessions < short.Sessions {
			short, side = data.Later, "later"
		}
		r.Sections = append(r.Sections, report.Section{Rows: []report.Row{{
			State: report.Skip, Subject: "too few sessions to compare",
			Outcome: joinDetail(side, countOf(short.Sessions, "session", "sessions")),
			Consequence: strconv.Itoa(data.MinSessions) +
				" is the fewest a rate is read from: under that, one unusual sitting moves a rate " +
				"further than the change being measured would",
			Fix: []string{wayOut},
		}}})
		return observeCompareNotes(r, data)
	}

	for _, c := range data.Changes {
		// The change list is already in reading order, so the blocks fall
		// out of it: a new section starts wherever the section name does.
		// One list behind both the screen and the export is what stops the
		// two from coming to hold different figures.
		if header := strings.ToUpper(c.Section); r.Sections[len(r.Sections)-1].Header != header {
			r.Sections = append(r.Sections, report.Section{Header: header})
		}
		last := &r.Sections[len(r.Sections)-1]
		last.Rows = append(last.Rows, observeChangeRow(c))
	}
	return observeCompareNotes(r, data)
}

// observeCohortRow is one side of the comparison in the header: what it ran
// under, how long it ran for, and the session count every rate below is
// divided by. The count is the outcome field because it never clips: it is
// the denominator, and a rate whose denominator went missing is a rate
// nobody can weigh.
func observeCohortRow(side string, c *observeCohortData) report.Row {
	return report.Row{State: report.Queue, Name: side, Subject: c.Value,
		Detail: joinDetail(
			c.First.Local().Format("Jan 2")+" – "+c.Last.Local().Format("Jan 2"),
			countOf(int(c.turns()), "turn", "turns")),
		Outcome: countOf(c.Sessions, "session", "sessions")}
}

func observeChangeRow(c observeChange) report.Row {
	detail := c.Qualifier
	if c.Beside != nil {
		detail = joinDetail(detail, c.Beside.figures()+" ("+c.Beside.direction()+")")
	}
	return report.Row{State: report.Queue, Name: c.Name,
		Subject: c.figures(), Detail: detail, Outcome: c.direction()}
}

// observeCompareNotes closes the report with what it left out and what it
// does not claim.
func observeCompareNotes(r report.Report, data observeCompareData) report.Report {
	if len(data.Others) > 0 {
		r.Notes = append(r.Notes, report.Note{State: report.Skip,
			Text: countOf(len(data.Others), "other value", "other values") +
				" in the window: " + strings.Join(data.Others, ", ")})
	}
	// The posture, stated where the numbers are — and only where there are
	// numbers. This is a local store of a few hundred sessions, and a
	// p-value over a sample this shape would lend the screen an authority it
	// has not got, so the rates, the denominators and the direction are the
	// whole answer and the reader draws the conclusion.
	if len(data.Changes) > 0 {
		r.Notes = append(r.Notes, report.Note{State: report.Queue,
			Text: "rates over the denominators above; no significance is claimed"})
	}
	return r
}

// observeSplitKey checks a split key against what the store will group on,
// so a mistyped one is answered with the list rather than with an empty
// screen that looks like a window with nothing in it.
func observeSplitKey(key string) error {
	keys := storage.AgentSplitKeys()
	for _, k := range keys {
		if k == key {
			return nil
		}
	}
	if key == "" {
		// The sentence opens on a word rather than on the flag: the error
		// banner upper-cases its first letter, and `--Split` is a flag
		// nobody can type.
		return fmt.Errorf("a --split is required: one of %s", strings.Join(keys, ", "))
	}
	return fmt.Errorf("cannot split sessions on %q: one of %s", key, strings.Join(keys, ", "))
}

// newObserveCompareCmd is the comparison. It hangs off `observe` and takes
// its window from there, because a comparison is the same window read two
// ways rather than a screen with a clock of its own.
func newObserveCompareCmd(window *string) *cobra.Command {
	var (
		split    string
		asJSON   bool
		compared = &cobra.Command{
			Use:   "compare",
			Short: "Compare two cohorts of sessions as rates",
			Long: "Split the window's sessions on one recorded value and draw the dashboard's aggregates for both cohorts as rates, " +
				"with the direction and size of each change. A cohort too small to read prints its count and no rate.",
			Args: cobra.NoArgs,
		}
	)
	compared.RunE = func(cmd *cobra.Command, args []string) error {
		if err := observeSplitKey(split); err != nil {
			return err
		}
		since, err := parseObserveWindow(*window)
		if err != nil {
			return err
		}
		db, err := openStore()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer db.Close()
		data, err := readObserveCompare(db, *window, split, since)
		if err != nil {
			return err
		}
		if asJSON {
			out, err := json.MarshalIndent(data, "", "  ")
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(append(out, '\n'))
			return err
		}
		return report.Fprint(cmd.OutOrStdout(), observeCompareReport(data))
	}
	compared.Flags().StringVar(&split, "split", "",
		"what to split the window's sessions on: "+strings.Join(storage.AgentSplitKeys(), ", "))
	compared.Flags().BoolVar(&asJSON, "json", false, "write the comparison as JSON instead of a report")
	return compared
}
