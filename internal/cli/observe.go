package cli

// Session observability: `shhh observe` renders local, content-free
// dashboards over recorded agent sessions, with JSON export and purge. The
// observeRecorder half persists what a running session reports.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

	cmd.AddCommand(exportCmd, sessionCmd, purgeCmd)
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
