package cli

// Session observability (S-065): `shhh observe` renders local, content-free
// dashboards over recorded agent sessions, with JSON export and purge. The
// observeRecorder half persists what a running session reports.

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/storage"
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

// observer adapts the recorder to the chat TUI's observability hooks.
func (r *observeRecorder) observer() chat.Observer {
	if r == nil {
		return chat.Observer{}
	}
	return chat.Observer{Usage: r.usage, ToolCall: r.toolCall, Decision: r.decision}
}

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

func (r *observeRecorder) toolCall(tool string, duration time.Duration, outcome string) {
	if r == nil {
		return
	}
	ms := duration.Milliseconds()
	_ = r.db.RecordAgentEvent(r.id, storage.AgentEventTool, tool, &ms, outcome, "")
}

func (r *observeRecorder) decision(decision, reason string) {
	if r == nil {
		return
	}
	_ = r.db.RecordAgentEvent(r.id, storage.AgentEventDecision, "", nil, decision, reason)
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
	exportCmd := &cobra.Command{
		Use:   "export",
		Short: "Export recorded agent-session metrics as JSON",
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

			sessions, err := db.ExportAgentObservability(since)
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

	cmd.AddCommand(exportCmd, purgeCmd)
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

	fmt.Fprintln(out, "\nRecent sessions:")
	w = tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "  STARTED\tKIND\tMODEL\tDURATION\tTURNS\tTOKENS IN\tTOKENS OUT\tEST. COST")
	for _, s := range sessions {
		duration := "active"
		if s.EndedAt != nil {
			duration = s.EndedAt.Sub(s.StartedAt).Round(time.Second).String()
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
			s.StartedAt.Local().Format("Jan 2 15:04"), s.Kind, s.Model, duration,
			s.Turns, s.TokensIn, s.TokensOut, fmtObserveCost(s.Cost))
	}
	return w.Flush()
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
