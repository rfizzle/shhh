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

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/ui/chat"
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
	// linked is the saved conversation the row was last linked to, so an
	// autosave that lands in the same slot costs no write.
	linked string
}

// startObserveRecorder opens a session row; any failure disables recording
// for the session rather than blocking it.
func startObserveRecorder(db *storage.DB, kind, provider, model string, prices *pricing.Table) *observeRecorder {
	if db == nil {
		return nil
	}
	id, err := db.StartAgentSession(kind, provider, model)
	if err != nil {
		return nil
	}
	return &observeRecorder{db: db, id: id, prices: prices, model: model}
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
	return &observeRecorder{db: db, id: id, prices: prices, model: model}
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
// system prompt as sent, how many skills loaded, and a fingerprint of the
// checkout. Fingerprints rather than the things themselves: the prompt
// carries the project context and the path names the machine, and neither
// belongs in a table that is content-free by construction. A hash still
// splits "before the edit" from "after it", which is all a comparison needs.
func (r *observeRecorder) stamp(sysPrompt string, skills int, root string) {
	if r == nil {
		return
	}
	_ = r.db.StampAgentSession(r.id, storage.AgentProvenance{
		Version:    version,
		PromptHash: fingerprint(sysPrompt),
		Skills:     skills,
		Project:    fingerprint(root),
	})
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

// fingerprint is a short stable hash of a string, or empty for empty input.
func fingerprint(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}

// toolCallOutcome records a tool event without a duration or position, for
// sub-agent runners that don't time individual calls.
func (r *observeRecorder) toolCallOutcome(tool, outcome string) {
	if r == nil {
		return
	}
	_ = r.db.RecordAgentEvent(r.id, storage.AgentEvent{Kind: storage.AgentEventTool, Tool: tool, Outcome: outcome})
}

// observer adapts the recorder to the chat TUI's observability hooks.
func (r *observeRecorder) observer() chat.Observer {
	if r == nil {
		return chat.Observer{}
	}
	return chat.Observer{
		Usage:    r.usagePriced,
		ToolCall: r.toolCallAt,
		Decision: r.decisionAt,
		Turn:     r.turn,
		Signal:   r.signal,
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

func (r *observeRecorder) toolCall(tool string, duration time.Duration, outcome string) {
	r.toolCallAt(chat.Pos{}, tool, duration, outcome, "")
}

func (r *observeRecorder) toolCallAt(at chat.Pos, tool string, duration time.Duration, outcome, class string) {
	if r == nil {
		return
	}
	ms := duration.Milliseconds()
	_ = r.db.RecordAgentEvent(r.id, storage.AgentEvent{
		Kind: storage.AgentEventTool, Tool: tool, DurationMs: &ms, Outcome: outcome, Reason: class,
		Turn: at.Turn, Round: at.Round,
	})
}

func (r *observeRecorder) decision(decision, reason string) {
	r.decisionAt(chat.Pos{}, decision, reason)
}

func (r *observeRecorder) decisionAt(at chat.Pos, decision, reason string) {
	if r == nil {
		return
	}
	_ = r.db.RecordAgentEvent(r.id, storage.AgentEvent{
		Kind: storage.AgentEventDecision, Outcome: decision, Reason: reason, Turn: at.Turn, Round: at.Round,
	})
}

// turn records a turn closing: the rounds it took ride in the event's
// round column, its wall time in the duration.
func (r *observeRecorder) turn(turn, rounds int64, duration time.Duration, outcome string) {
	if r == nil {
		return
	}
	ms := duration.Milliseconds()
	_ = r.db.RecordAgentEvent(r.id, storage.AgentEvent{
		Kind: storage.AgentEventTurn, Outcome: outcome, DurationMs: &ms, Turn: turn, Round: rounds,
	})
}

func (r *observeRecorder) signal(at chat.Pos, code, reason string) {
	if r == nil {
		return
	}
	_ = r.db.RecordAgentEvent(r.id, storage.AgentEvent{
		Kind: storage.AgentEventSignal, Outcome: code, Reason: reason, Turn: at.Turn, Round: at.Round,
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

func (r *observeRecorder) end() {
	if r == nil {
		return
	}
	_ = r.db.EndAgentSession(r.id)
}

func newObserveCmd() *cobra.Command {
	var window string

	cmd := &cobra.Command{
		Use:   "observe",
		Short: "Show agent-session usage dashboards",
		Long:  "Display local, content-free metrics about agent sessions: usage and cost by day and model, tool mix, approval decisions, and recent sessions.",
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
	} {
		if err := q.read(); err != nil {
			return observeData{}, fmt.Errorf("query %s: %w", q.name, err)
		}
	}
	return data, nil
}

// observeReport is the whole dashboard as one report: the sections the store
// can answer for, in the order a reader asks them — what it cost, what it
// ran, what it was allowed to do, and which sessions those were
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

func observeSignalRows(signals []storage.AgentSignalCount) []report.Row {
	rows := make([]report.Row, 0, len(signals))
	for _, s := range signals {
		rows = append(rows, report.Row{State: report.Queue, Name: s.Signal,
			Subject: countOf(s.Count, "time", "times"), Detail: s.Reason})
	}
	return rows
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
	pairs := []report.Pair{
		{Key: "started", Value: s.StartedAt.Local().Format("Jan 2 15:04")},
		{Key: "model", Value: joinDetail(s.Provider, s.Model)},
		{Key: "turns", Value: strconv.FormatInt(s.Turns, 10)},
	}
	for _, p := range []report.Pair{
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

	r := report.Report{
		Title:    "shhh observe session " + strconv.FormatInt(id, 10),
		Subject:  joinDetail(s.Kind, observeElapsed(s)),
		Sections: []report.Section{{Pairs: pairs}},
	}

	events, err := db.AgentSessionEvents(id)
	if err != nil {
		return fmt.Errorf("query events: %w", err)
	}
	if len(events) == 0 {
		return report.Fprint(cmd.OutOrStdout(),
			emptyInto(r, "no events recorded for this session", "shhh observe"))
	}
	turn := int64(-1)
	for _, e := range events {
		if e.Turn != turn {
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
	return report.Fprint(cmd.OutOrStdout(), r)
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
		row.State, row.Subject, row.Detail = report.Queue, e.Outcome, e.Reason
		row.Outcome = "signal"
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
