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
	"text/tabwriter"
	"time"

	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/ui/chat"
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
			db, err := storage.Open()
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
			db, err := storage.Open()
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
			fmt.Fprintf(cmd.OutOrStdout(), "Exported %d session(s) to %s\n", len(sessions), exportOut)
			return nil
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
			db, err := storage.Open()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()
			return renderObserveSession(cmd, db, id)
		},
	}

	purgeCmd := &cobra.Command{
		Use:   "purge",
		Short: "Delete all recorded agent-session metrics",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := storage.Open()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()
			n, err := db.PurgeAgentObservability()
			if err != nil {
				return fmt.Errorf("purge: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Purged %d recorded session(s) and their events.\n", n)
			return nil
		},
	}

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

func renderObserveDashboard(cmd *cobra.Command, db *storage.DB, window string, since time.Time) error {
	out := cmd.OutOrStdout()

	sessions, err := db.AgentSessions(since, 20)
	if err != nil {
		return fmt.Errorf("query sessions: %w", err)
	}
	if len(sessions) == 0 {
		fmt.Fprintf(out, "No agent sessions recorded in the last %s. Run `shhh chat` or `shhh code` first.\n", window)
		return nil
	}

	fmt.Fprintf(out, "Agent sessions — last %s\n\n", window)

	byDay, err := db.AgentUsageByDay(since)
	if err != nil {
		return fmt.Errorf("query usage by day: %w", err)
	}
	fmt.Fprintln(out, "Usage by day:")
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "  DAY\tSESSIONS\tTOKENS IN\tTOKENS OUT\tEST. COST")
	for _, u := range byDay {
		fmt.Fprintf(w, "  %s\t%d\t%d\t%d\t%s\n", u.Day, u.Sessions, u.TokensIn, u.TokensOut, fmtObserveCost(u.Cost))
	}
	_ = w.Flush()

	byModel, err := db.AgentUsageByModel(since)
	if err != nil {
		return fmt.Errorf("query usage by model: %w", err)
	}
	fmt.Fprintln(out, "\nUsage by model:")
	w = tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "  PROVIDER\tMODEL\tSESSIONS\tTOKENS IN\tTOKENS OUT\tEST. COST")
	for _, u := range byModel {
		fmt.Fprintf(w, "  %s\t%s\t%d\t%d\t%d\t%s\n", u.Provider, u.Model, u.Sessions, u.TokensIn, u.TokensOut, fmtObserveCost(u.Cost))
	}
	_ = w.Flush()

	toolMix, err := db.AgentToolMix(since)
	if err != nil {
		return fmt.Errorf("query tool mix: %w", err)
	}
	if len(toolMix) > 0 {
		fmt.Fprintln(out, "\nTool mix:")
		w = tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "  TOOL\tCALLS\tAVG TIME\tERRORS")
		for _, u := range toolMix {
			fmt.Fprintf(w, "  %s\t%d\t%s\t%.0f%%\n", u.Tool, u.Count, fmtMs(u.AvgDurationMs), u.ErrorRate*100)
		}
		_ = w.Flush()
	}

	decisions, err := db.AgentDecisions(since)
	if err != nil {
		return fmt.Errorf("query decisions: %w", err)
	}
	if len(decisions) > 0 {
		fmt.Fprintln(out, "\nApproval decisions:")
		w = tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "  DECISION\tREASON\tCOUNT")
		for _, d := range decisions {
			fmt.Fprintf(w, "  %s\t%s\t%d\n", d.Decision, d.Reason, d.Count)
		}
		_ = w.Flush()
	}

	turns, err := db.AgentTurns(since)
	if err != nil {
		return fmt.Errorf("query turns: %w", err)
	}
	if len(turns) > 0 {
		fmt.Fprintln(out, "\nTurns:")
		w = tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "  ENDED\tCOUNT\tAVG ROUNDS\tMAX ROUNDS\tAVG TIME")
		for _, t := range turns {
			fmt.Fprintf(w, "  %s\t%d\t%.1f\t%d\t%s\n", t.Outcome, t.Count, t.AvgRounds, t.MaxRounds, fmtMs(t.AvgDurationMs))
		}
		_ = w.Flush()
	}

	toolErrors, err := db.AgentToolErrors(since)
	if err != nil {
		return fmt.Errorf("query tool errors: %w", err)
	}
	if len(toolErrors) > 0 {
		fmt.Fprintln(out, "\nTool errors:")
		w = tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "  TOOL\tCLASS\tCOUNT")
		for _, e := range toolErrors {
			fmt.Fprintf(w, "  %s\t%s\t%d\n", e.Tool, orDash(e.Class), e.Count)
		}
		_ = w.Flush()
	}

	signals, err := db.AgentSignals(since)
	if err != nil {
		return fmt.Errorf("query signals: %w", err)
	}
	if len(signals) > 0 {
		fmt.Fprintln(out, "\nSignals:")
		w = tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "  SIGNAL\tREASON\tCOUNT")
		for _, s := range signals {
			fmt.Fprintf(w, "  %s\t%s\t%d\n", s.Signal, orDash(s.Reason), s.Count)
		}
		_ = w.Flush()
	}

	fmt.Fprintln(out, "\nRecent sessions:")
	w = tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "  ID\tSTARTED\tKIND\tMODEL\tDURATION\tTURNS\tTOKENS IN\tTOKENS OUT\tEST. COST")
	for _, s := range sessions {
		duration := "active"
		if s.EndedAt != nil {
			duration = s.EndedAt.Sub(s.StartedAt).Round(time.Second).String()
		}
		fmt.Fprintf(w, "  %d\t%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
			s.ID, s.StartedAt.Local().Format("Jan 2 15:04"), s.Kind, s.Model, duration,
			s.Turns, s.TokensIn, s.TokensOut, fmtObserveCost(s.Cost))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(out, "\n`shhh observe session <id>` shows one session turn by turn.")
	return nil
}

// renderObserveSession prints one session: its provenance, then its events
// in order with a heading per turn, so a reader can see where the rounds
// went and where the loop's safeguards spoke.
func renderObserveSession(cmd *cobra.Command, db *storage.DB, id int64) error {
	out := cmd.OutOrStdout()
	s, ok, err := db.AgentSession(id)
	if err != nil {
		return fmt.Errorf("query session: %w", err)
	}
	if !ok {
		return fmt.Errorf("no recorded session %d", id)
	}
	duration := "active"
	if s.EndedAt != nil {
		duration = s.EndedAt.Sub(s.StartedAt).Round(time.Second).String()
	}
	fmt.Fprintf(out, "Session %d — %s %s/%s, started %s, %s\n", s.ID, s.Kind, s.Provider, s.Model,
		s.StartedAt.Local().Format("Jan 2 15:04"), duration)
	fmt.Fprintf(out, "  turns %d · tokens ↑%d ↓%d · est. cost %s\n", s.Turns, s.TokensIn, s.TokensOut, fmtObserveCost(s.Cost))
	fmt.Fprintf(out, "  version %s · prompt %s · skills %d · project %s\n",
		orDash(s.Version), orDash(s.PromptHash), s.Skills, orDash(s.Project))
	if s.ChatSession != "" {
		fmt.Fprintf(out, "  conversation %q (shhh chat --resume, or observe export --transcript)\n", s.ChatSession)
	}
	if s.ParentID != nil {
		fmt.Fprintf(out, "  child of session %d\n", *s.ParentID)
	}

	events, err := db.AgentSessionEvents(id)
	if err != nil {
		return fmt.Errorf("query events: %w", err)
	}
	if len(events) == 0 {
		fmt.Fprintln(out, "\nNo events recorded.")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	turn := int64(-1)
	for _, e := range events {
		if e.Turn != turn {
			_ = w.Flush()
			turn = e.Turn
			if turn == 0 {
				fmt.Fprintln(out, "\nBefore the first turn:")
			} else {
				fmt.Fprintf(out, "\nTurn %d:\n", turn)
			}
		}
		at := e.CreatedAt
		if t, err := time.Parse(observeTimeLayout, e.CreatedAt); err == nil {
			at = t.Local().Format("15:04:05")
		}
		switch e.Kind {
		case storage.AgentEventTool:
			fmt.Fprintf(w, "  %s\tr%d\ttool\t%s\t%s\t%s\t%s\n", at, e.Round, e.Tool, e.Outcome, orDash(e.Reason), fmtEventMs(e.DurationMs))
		case storage.AgentEventDecision:
			fmt.Fprintf(w, "  %s\tr%d\tdecision\t%s\t%s\t\t\n", at, e.Round, e.Outcome, orDash(e.Reason))
		case storage.AgentEventSignal:
			fmt.Fprintf(w, "  %s\tr%d\tsignal\t%s\t%s\t\t\n", at, e.Round, e.Outcome, orDash(e.Reason))
		case storage.AgentEventTurn:
			fmt.Fprintf(w, "  %s\t\tturn\t%s\t%d rounds\t\t%s\n", at, e.Outcome, e.Round, fmtEventMs(e.DurationMs))
		default:
			fmt.Fprintf(w, "  %s\tr%d\t%s\t%s\t%s\t\t\n", at, e.Round, e.Kind, e.Outcome, orDash(e.Reason))
		}
	}
	return w.Flush()
}

// observeTimeLayout is how storage stamps event times.
const observeTimeLayout = "2006-01-02T15:04:05.000Z"

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func fmtEventMs(ms *int64) string {
	if ms == nil {
		return ""
	}
	return (time.Duration(*ms) * time.Millisecond).String()
}

func fmtObserveCost(v float64) string {
	if v == 0 {
		return "-"
	}
	if v < 0.01 {
		return fmt.Sprintf("$%.4f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}
