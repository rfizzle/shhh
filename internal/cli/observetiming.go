package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/spf13/cobra"
)

// The readings of where a session's time went: startup by phase and by
// server, the longest quiet stretches, and a turn's split on its own page
// (docs/capabilities/sessions-and-memory.md#startup-and-waits-are-timed).
// They are arithmetic over the record's timing rows, with no model, and what
// they say is said to the person alone.

// observeQuietLimit is how many stretches `observe quiet` lists.
const observeQuietLimit = 10

// newObserveStartupCmd is `observe startup`. It takes its window from
// `observe`, the way the comparison does.
func newObserveStartupCmd(window *string) *cobra.Command {
	return &cobra.Command{
		Use:   "startup",
		Short: "Rank where startup spent its time",
		Long: "For each startup phase and each MCP server, print the median and the worst duration over the window's sessions with how the connects came out. " +
			"A server that timed out, or took over half its bound, is said with the keys that set the bound.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			since, err := parseObserveWindow(*window)
			if err != nil {
				return err
			}
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()
			rows, err := db.AgentStartupTimings(since)
			if err != nil {
				return fmt.Errorf("query startup timings: %w", err)
			}
			return report.Fprint(cmd.OutOrStdout(), observeStartupReport(rows, *window, ConfigFrom(cmd.Context())))
		},
	}
}

// newObserveQuietCmd is `observe quiet`.
func newObserveQuietCmd(window *string) *cobra.Command {
	return &cobra.Command{
		Use:   "quiet",
		Short: "List the longest stretches with nothing on screen",
		Long:  "List the window's ten longest quiet stretches: the session and turn, how long, what the turn waited on, and whether the stream delivered anything in it.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			since, err := parseObserveWindow(*window)
			if err != nil {
				return err
			}
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()
			stretches, err := db.AgentQuietStretches(since, observeQuietLimit)
			if err != nil {
				return fmt.Errorf("query quiet stretches: %w", err)
			}
			return report.Fprint(cmd.OutOrStdout(), observeQuietReport(stretches, *window))
		},
	}
}

// observeMedianMs is the middle of a set of durations, the mean of the two
// middle ones where there is an even number.
func observeMedianMs(v []int64) int64 {
	s := append([]int64(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// observeSpan is a duration in whole milliseconds as a reading draws it.
func observeSpan(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", ms)
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return d.Round(time.Second).String()
}

// observeServerBound is the startup bound a server runs under now: its own
// timeout_seconds, else mcp.startup_timeout_seconds, else the default. It
// reads today's configuration, since the record holds what a connect took
// and not the bound it was under.
func observeServerBound(cfg config.Config, name string) time.Duration {
	if s := cfg.MCP.Servers[name]; s.TimeoutSeconds > 0 {
		return time.Duration(s.TimeoutSeconds) * time.Second
	}
	if cfg.MCP.StartupTimeoutSeconds > 0 {
		return time.Duration(cfg.MCP.StartupTimeoutSeconds) * time.Second
	}
	return mcp.DefaultStartupTimeout
}

// observeStartupTally is the durations and outcomes of one phase or server.
type observeStartupTally struct {
	ms       []int64
	outcomes map[string]int
}

func (t *observeStartupTally) worst() int64 {
	var w int64
	for _, v := range t.ms {
		w = max(w, v)
	}
	return w
}

func (t *observeStartupTally) reading() string {
	return fmt.Sprintf("median %s · worst %s", observeSpan(observeMedianMs(t.ms)), observeSpan(t.worst()))
}

// observeStartupReport is the window's startup, ranked: the phases by their
// median, the servers by their worst, each with how its connects came out.
// A phase row is over every session that wrote it, and a session of a surface
// that does not time a phase is not in it.
func observeStartupReport(rows []storage.AgentStartupTiming, window string, cfg config.Config) report.Report {
	r := report.Report{Title: "shhh observe startup", Subject: "last " + window}
	if len(rows) == 0 {
		return emptyInto(r, "no startup recorded in the last "+window, "shhh chat")
	}
	phases, servers := map[string]*observeStartupTally{}, map[string]*observeStartupTally{}
	for _, row := range rows {
		m, key := phases, row.Phase
		if row.Phase == observe.PhaseMCP {
			m, key = servers, row.Name
		}
		t := m[key]
		if t == nil {
			t = &observeStartupTally{outcomes: map[string]int{}}
			m[key] = t
		}
		t.ms = append(t.ms, row.DurationMs)
		if row.Outcome != "" {
			t.outcomes[row.Outcome]++
		}
	}
	ranked := func(m map[string]*observeStartupTally, by func(*observeStartupTally) int64) []string {
		names := make([]string, 0, len(m))
		for n := range m {
			names = append(names, n)
		}
		sort.Slice(names, func(i, j int) bool {
			if a, b := by(m[names[i]]), by(m[names[j]]); a != b {
				return a > b
			}
			return names[i] < names[j]
		})
		return names
	}

	var phaseRows []report.Row
	for _, n := range ranked(phases, func(t *observeStartupTally) int64 { return observeMedianMs(t.ms) }) {
		t := phases[n]
		phaseRows = append(phaseRows, report.Row{State: report.Pass, Name: n, Subject: t.reading(),
			Detail: countOf(len(t.ms), "session", "sessions")})
	}
	var serverRows []report.Row
	for _, n := range ranked(servers, (*observeStartupTally).worst) {
		t := servers[n]
		row := report.Row{State: report.Pass, Name: n, Subject: t.reading(),
			Detail: countOf(len(t.ms), "connect", "connects"), Outcome: observeOutcomeCounts(t.outcomes)}
		// A connect that ran out of time, or that used over half its bound,
		// is a cost worth naming the keys for.
		bound := observeServerBound(cfg, n)
		if t.outcomes[observe.ServerTimeout] > 0 || time.Duration(t.worst())*time.Millisecond > bound/2 {
			row.State = report.Warn
			row.Consequence = fmt.Sprintf("slow against its %s bound — set `mcp.startup_timeout_seconds`, or `timeout_seconds` under `[mcp.servers.%s]`", bound, n)
		}
		serverRows = append(serverRows, row)
	}
	for _, s := range []report.Section{
		{Header: "PHASES", Rows: phaseRows},
		{Header: "MCP SERVERS", Rows: serverRows},
	} {
		if len(s.Rows) > 0 {
			r.Sections = append(r.Sections, s)
		}
	}
	return r
}

// observeOutcomeCounts words a tally of outcomes, most frequent first.
func observeOutcomeCounts(c map[string]int) string {
	words := make([]string, 0, len(c))
	for w := range c {
		words = append(words, w)
	}
	sort.Slice(words, func(i, j int) bool {
		if c[words[i]] != c[words[j]] {
			return c[words[i]] > c[words[j]]
		}
		return words[i] < words[j]
	})
	parts := make([]string, len(words))
	for i, w := range words {
		parts[i] = strconv.Itoa(c[w]) + " " + w
	}
	return strings.Join(parts, ", ")
}

// observeWaitWords is what a turn waited on, as a person reads it.
func observeWaitWords(wait string) string {
	switch wait {
	case observe.WaitModelFirst:
		return "the model's first event"
	case observe.WaitModelStream:
		return "the model writing"
	case observe.WaitTool:
		return "a tool"
	case observe.WaitPerson:
		return "the person"
	}
	return wait
}

// observeStretchDelivered says whether the stream delivered anything.
func observeStretchDelivered(n int64) string {
	if n == 0 {
		return "the stream delivered nothing"
	}
	return "the stream delivered " + countOf(int(n), "event", "events")
}

// observeQuietRows are the stretches as rows: a silent one, where the
// connection may be gone, is drawn as a warning.
func observeQuietRows(stretches []storage.AgentQuietStretch) []report.Row {
	rows := make([]report.Row, 0, len(stretches))
	for _, q := range stretches {
		state := report.Pass
		if q.Outcome == observe.StretchSilent {
			state = report.Warn
		}
		rows = append(rows, report.Row{State: state, Name: q.Outcome, Subject: observeSpan(q.DurationMs),
			Detail: joinDetail(fmt.Sprintf("session %d · turn %d", q.SessionID, q.Turn),
				joinDetail("waited on "+observeWaitWords(q.Wait), observeStretchDelivered(q.Delivered)))})
	}
	return rows
}

// observeQuietReport is `observe quiet`.
func observeQuietReport(stretches []storage.AgentQuietStretch, window string) report.Report {
	r := report.Report{Title: "shhh observe quiet", Subject: "last " + window}
	if len(stretches) == 0 {
		return emptyInto(r, "no quiet stretch recorded in the last "+window, "shhh chat")
	}
	r.Sections = append(r.Sections, report.Section{Rows: observeQuietRows(stretches)})
	r.Notes = append(r.Notes, report.Note{State: report.Run,
		Text: "`shhh observe session <id>` shows the turn's time split by what it waited on"})
	return r
}

// observeLongestQuiet is the dashboard's one line on the quiet stretches.
func observeLongestQuiet(stretches []storage.AgentQuietStretch) []report.Note {
	if len(stretches) == 0 {
		return nil
	}
	q := stretches[0]
	return []report.Note{{State: report.Run, Text: fmt.Sprintf(
		"longest quiet stretch %s · session %d turn %d · waited on %s · `shhh observe quiet` lists the ten longest",
		observeSpan(q.DurationMs), q.SessionID, q.Turn, observeWaitWords(q.Wait))}}
}

// observeSplitText is a turn's time as the four things it waited on.
func observeSplitText(t storage.AgentTiming) string {
	var parts []string
	for _, p := range []struct {
		label string
		ms    *int64
	}{{"model first", t.ModelFirstMs}, {"model writing", t.ModelStreamMs}, {"tools", t.ToolMs}, {"person", t.PersonMs}} {
		if p.ms != nil {
			parts = append(parts, p.label+" "+observeSpan(*p.ms))
		}
	}
	return strings.Join(parts, " · ")
}

// observeWithTurnSplit adds the split to each turn's row on the page, as a
// line under the row because four figures do not fit the row's target. Each
// row takes its own split in the order they were written: a turn granted more
// rounds after a cap pause wrote a second row carrying the whole split, so a
// figure summed across turn rows takes the last row of each turn, and this
// page, which sums nothing, draws each row's own. A turn from a surface that
// does not split its time says so, rather than reading as a turn that waited
// on nothing.
func observeWithTurnSplit(r report.Report, timings []storage.AgentTiming) report.Report {
	queue := map[int64][]storage.AgentTiming{}
	for _, t := range timings {
		if t.Kind == storage.AgentEventTurn {
			queue[t.Turn] = append(queue[t.Turn], t)
		}
	}
	for i := range r.Sections {
		var turn int64
		if _, err := fmt.Sscanf(r.Sections[i].Header, "TURN %d", &turn); err != nil {
			continue
		}
		for j := range r.Sections[i].Rows {
			row := &r.Sections[i].Rows[j]
			if row.Subject != "turn" {
				continue
			}
			line := "split not recorded on this surface"
			if q := queue[turn]; len(q) > 0 {
				line, queue[turn] = "split: "+observeSplitText(q[0]), q[1:]
			}
			row.Body = append(row.Body, line)
		}
	}
	return r
}
