package cli

// Session observability: `shhh observe` renders local, content-free
// dashboards over recorded agent sessions, with JSON export and purge. The
// observeRecorder half persists what a running session reports.

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/spf13/cobra"
)

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

	var sessionTranscript bool
	sessionCmd := &cobra.Command{
		Use:   "session <id>",
		Short: "Show one recorded session as a timeline",
		Long: "Print one session's provenance and its events in order, grouped by turn: tool calls with what they were pointed at, their duration and outcome, decisions, and the signals the loop raised. " +
			"What each call was pointed at is read from the conversation this machine saved beside the record, never from the record itself, which holds no such thing.",
		Args: cobra.ExactArgs(1),
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
			return renderObserveSession(cmd, db, id, sessionTranscript)
		},
	}
	sessionCmd.Flags().BoolVar(&sessionTranscript, "transcript", false,
		"print what each call came back with under its row, bounded the way the session's own feed bounds it")

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

	classifyCmd := &cobra.Command{
		Use:   "classify",
		Short: "Say what each command recorded before the record carried it was for",
		Long: "Read every command recorded without a purpose word back out of the conversation this machine saved beside it, " +
			"and write the word onto the record. Only the word is written — never the command. " +
			"A command whose conversation is gone, or that was recorded before messages carried a position, stays unrecorded.",
		Args: cobra.NoArgs,
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
			written, unread, err := classifyRecordedCommands(db, since)
			if err != nil {
				return err
			}
			if written == 0 && unread == 0 {
				return report.Fprintln(cmd.OutOrStdout(), report.Empty(
					"no unrecorded commands in the last "+window, "shhh observe"))
			}
			r := report.Done("classified", countOf(written, "command", "commands"))
			if unread > 0 {
				r.Detail = countOf(unread, "command", "commands") + " had no conversation to read"
			}
			return report.Fprintln(cmd.OutOrStdout(), r)
		},
	}

	cmd.AddCommand(exportCmd, sessionCmd, classifyCmd, purgeCmd, newObserveCompareCmd(&window),
		newObserveStartupCmd(&window), newObserveQuietCmd(&window), newObservePatternsCmd(&window))
	return cmd
}

// classifyRecordedCommands writes a purpose word onto every command the
// window recorded without one, reading each command's line out of the
// conversation its session saved (docs/capabilities/sessions-and-memory.md#a-command-is-recorded-by-what-it-was-for).
// It returns how many took a word and how many could not be read.
//
// The pairing is observeCalls', the one the session page draws targets by:
// a command is the nth execute_command of its turn and round on both sides.
// A command at turn 0 is left alone even where a call sits at turn 0 too:
// both sides wrote zero before they kept a position, so the pairing would be
// by order across the whole session, and a compaction that dropped the
// early calls would hand every later command its predecessor's line.
func classifyRecordedCommands(db *storage.DB, since time.Time) (written, unread int, err error) {
	sessions, err := db.AgentUnclassifiedCommandSessions(since)
	if err != nil {
		return 0, 0, fmt.Errorf("query unrecorded commands: %w", err)
	}
	for _, id := range sessions {
		events, err := db.AgentCommandEvents(id)
		if err != nil {
			return written, unread, fmt.Errorf("query commands of session %d: %w", id, err)
		}
		calls, err := db.AgentSessionCalls(id)
		if err != nil {
			return written, unread, fmt.Errorf("query calls of session %d: %w", id, err)
		}
		found := newObserveCalls(calls)
		purposes := map[int64]string{}
		for _, e := range events {
			call := found.next(storage.AgentExportEvent{Kind: storage.AgentEventTool,
				Turn: e.Turn, Round: e.Round, Tool: tools.ExecCommandName})
			if e.Purpose != "" {
				continue
			}
			if e.Turn == 0 || call.Tool == "" {
				unread++
				continue
			}
			purposes[e.ID] = observe.ToolPurpose(call.Tool, call.Args)
		}
		n, err := db.SetAgentCommandPurposes(purposes)
		if err != nil {
			return written, unread, fmt.Errorf("record purposes of session %d: %w", id, err)
		}
		written += n
	}
	return written, unread, nil
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
