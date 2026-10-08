package cli

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/spf13/cobra"
)

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
	// Commands is execute_command split by what each command was for.
	Commands []storage.AgentCommandPurpose
	// FirstWrites is one row per session that called a tool, carrying how
	// much of that calling came before the session changed anything.
	FirstWrites []storage.AgentFirstWrite
	Decisions   []storage.AgentDecisionCount
	// Overturns is the classifier's judged denials paired with the person's
	// answer to each.
	Overturns storage.AgentOverturns
	Turns     []storage.AgentTurnOutcome
	Signals   []storage.AgentSignalCount
	// Interventions pairs every interruption with the reading that followed
	// it, which is the one reading here that says whether the machinery
	// worked rather than how often it fired.
	Interventions []storage.AgentInterventionOutcome
	Gates         []storage.AgentGateVerdict
	// Flakes is this checkout's flake ledger, the checks that last flaked
	// inside the window: the one gate reading kept per checkout rather than
	// per session.
	Flakes   []storage.Flake
	Outcomes []storage.AgentSessionOutcome
	// Quiet is the window's longest quiet stretch, at most one.
	Quiet []storage.AgentQuietStretch
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
		{"command purposes", func() (err error) { data.Commands, err = db.AgentCommandPurposes(since); return }},
		{"first writes", func() (err error) { data.FirstWrites, err = db.AgentFirstWrites(since); return }},
		{"decisions", func() (err error) { data.Decisions, err = db.AgentDecisions(since); return }},
		{"overturns", func() (err error) { data.Overturns, err = db.AgentOverturns(since); return }},
		{"turns", func() (err error) { data.Turns, err = db.AgentTurns(since); return }},
		{"signals", func() (err error) { data.Signals, err = db.AgentSignals(since); return }},
		{"intervention outcomes", func() (err error) {
			data.Interventions, err = db.AgentInterventionOutcomes(since)
			return
		}},
		{"gate verdicts", func() (err error) { data.Gates, err = db.AgentGateVerdicts(since); return }},
		{"flakes", func() (err error) { data.Flakes, err = flakesSince(db, projectFingerprintRoot(), since); return }},
		{"outcomes", func() (err error) { data.Outcomes, err = db.AgentSessionOutcomes(since); return }},
		{"quiet stretches", func() (err error) { data.Quiet, err = db.AgentQuietStretches(since, 1); return }},
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
		{Header: "COMMANDS", Rows: observeCommandRows(data.Commands)},
		{Header: "FIRST WRITE", Rows: observeFirstWriteRows(data.FirstWrites)},
		{Header: "DECISIONS", Rows: observeDecisionRows(data.Decisions, data.Overturns)},
		{Header: "TURNS", Rows: observeTurnRows(data.Turns)},
		{Header: "SIGNALS", Rows: observeSignalRows(data.Signals)},
		{Header: "INTERVENED", Rows: observeInterventionRows(data.Interventions)},
		{Header: "GATE", Rows: append(observeGateRows(data.Gates), observeFlakeRows(data.Flakes)...)},
		{Header: "OUTCOMES", Rows: observeOutcomeRows(data.Outcomes)},
		{Header: "SESSIONS", Rows: observeSessionRows(data.Sessions)},
	} {
		if len(section.Rows) > 0 {
			r.Sections = append(r.Sections, section)
		}
	}
	r.Notes = append(r.Notes, observeLongestQuiet(data.Quiet)...)
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

// observeCommandRows splits the commands the model ran by what each was for,
// with each word's share of them — the reading that says how many shell
// calls a built-in tool could have answered, taken without a single
// command's text (docs/capabilities/sessions-and-memory.md#a-command-is-recorded-by-what-it-was-for).
//
// Commands recorded before the record said are a row of their own and are
// never folded into other: other is a reading of a line, and those are lines
// nothing read. The row names the verb that reads them.
func observeCommandRows(purposes []storage.AgentCommandPurpose) []report.Row {
	var total int
	for _, p := range purposes {
		total += p.Count
	}
	if total == 0 {
		return nil
	}
	share := func(n int) string { return fmt.Sprintf("%.0f%%", float64(n)*100/float64(total)) }
	var rows []report.Row
	var unrecorded int
	for _, p := range purposes {
		if p.Purpose == "" {
			unrecorded = p.Count
			continue
		}
		rows = append(rows, report.Row{State: report.Pass, Name: p.Purpose,
			Subject: countOf(p.Count, "command", "commands"), Outcome: share(p.Count)})
	}
	if unrecorded > 0 {
		rows = append(rows, report.Row{State: report.Skip, Name: "unrecorded",
			Subject: countOf(unrecorded, "command", "commands"),
			Detail:  "`shhh observe classify`", Outcome: share(unrecorded)})
	}
	return rows
}

// observeDecisionRows names who decided, in the transcript's own words: a
// denial by a person is a preference and a denial by a rule is policy, and
// the rows say which (docs/interface/principles.md#weight-tracks-risk).
// observeFirstWriteRows is how much looking a session does before it changes
// anything: the middle session's count of reads, searches, globs and
// language-server calls made before its first write.
//
// The median rather than the mean, because this is a handful of sessions and
// one afternoon spent reading a repository nobody here has opened before
// moves a mean by its own share of the sample. And the sessions that never
// wrote are counted beside it rather than in it: they have no such figure at
// all, and a zero apiece would pull the middle of the sample onto sessions
// that were never looking for a place to start.
// See docs/capabilities/coding-agent.md#where-a-map-would-sit.
func observeFirstWriteRows(firstWrites []storage.AgentFirstWrite) []report.Row {
	var (
		searches []int
		never    int
	)
	for _, f := range firstWrites {
		if !f.Wrote {
			never++
			continue
		}
		searches = append(searches, f.Searches)
	}
	if len(searches) == 0 {
		return nil
	}
	// The population is named because it is not every session in the
	// window: a session that called no tool at all is not in this reading,
	// so a bare "9 of 10" would be read against the session count above it
	// and come out short by however many sessions did nothing.
	detail := fmt.Sprintf("%d of %s that called a tool", len(searches),
		countOf(len(searches)+never, "session", "sessions"))
	return []report.Row{{State: report.Pass, Name: "search calls",
		Subject: "median " + observeCount(observeMedian(searches)), Detail: detail}}
}

// observeMedian is the middle of a sample, or the mean of the middle two.
// It is written for ints because that is what the record holds, and returns
// a float because the middle of an even sample is not one of them.
func observeMedian(v []int) float64 {
	if len(v) == 0 {
		return 0
	}
	sorted := slices.Sorted(slices.Values(v))
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return float64(sorted[mid])
	}
	return float64(sorted[mid-1]+sorted[mid]) / 2
}

// observeCount writes a count that is usually whole: no decimal where there
// is nothing after it, since `median 6.0` invites a reader to wonder what
// the tenths are counting.
func observeCount(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func observeDecisionRows(decisions []storage.AgentDecisionCount, overturns storage.AgentOverturns) []report.Row {
	rows := make([]report.Row, 0, len(decisions)+1)
	for _, d := range decisions {
		// The verdict and its decider are one phrase, so they stay one field
		// rather than being split across the name column.
		rows = append(rows, report.Row{
			State:   observeDecisionState(d.Decision),
			Subject: components.OutcomeBy(observeDecisionWord(d.Decision), observeDecider(d.Reason)),
			Outcome: countOf(d.Count, "time", "times"),
		})
	}
	// The overturn rate is over the judged denials a person answered, not
	// over every denial above it: the classifier's no that nobody was asked
	// about is counted in its own row and has no answer to be overturned by.
	if n := overturns.Judged; n > 0 {
		rows = append(rows, report.Row{
			State: report.Queue,
			Subject: fmt.Sprintf("overturned %d of %s", overturns.Overturned,
				countOf(n, "judged denial", "judged denials")),
			Outcome: fmt.Sprintf("%.0f%%", float64(overturns.Overturned)/float64(n)*100),
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

// observeDecider is who or what decided. The person's answer is recorded as
// `user`, and the shapes a card offers it in as `user-` codes; those read as
// `you`, with the shape beside it so a batch or a standing grant stays its own
// row. A decision with no recorded reason was the person's too, from a store
// written before every answer carried one. Everything else is a rule's or the
// classifier's.
func observeDecider(reason string) string {
	if reason == "" || reason == observe.ReasonUser {
		return "you"
	}
	if shape, ok := strings.CutPrefix(reason, observe.ReasonUser+"-"); ok {
		return "you · " + shape
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
	// Both of the ways a turn breaks. They are two rows because what to do
	// about them differs, and one state because neither is a turn that did
	// the work.
	case "failed", "rejected":
		return report.Fail
	case "cancelled", "cap-paused":
		return report.Skip
	}
	return report.Pass
}

// observeSignalRows is the loop's own safeguards, with the gate and the
// interruptions left out: each has a section of its own below, and one fact
// counted twice on one screen reads as two. Every interruption reaches the
// INTERVENED block — one that nothing read afterwards as its own "none" row —
// so nothing is lost by leaving them out here, and what is gained is that
// the count a reader sees is the one with the reading beside it.
func observeSignalRows(signals []storage.AgentSignalCount) []report.Row {
	rows := make([]report.Row, 0, len(signals))
	for _, s := range signals {
		if s.Signal == "gate" || s.Signal == "intervened" {
			continue
		}
		rows = append(rows, report.Row{State: report.Queue, Name: s.Signal,
			Subject: countOf(s.Count, "time", "times"), Detail: s.Reason})
	}
	return rows
}

// observeInterventionRows is what the loop's own interruptions came to: each
// kind of interruption, the reading the session came back with afterwards,
// and how far past the interruption that reading was taken.
//
// The share is over the interruptions of that kind and nothing else. That is
// the denominator the question needs: "a hundred steers" says only that the
// threshold is low, and "sixty of a hundred steers were followed by an
// on-target reading" is the sentence somebody about to change the threshold
// is trying to write. A reading that never came keeps its row for the same
// reason — dropped, it would shrink the denominator and make the machinery
// look better the more often it interrupted a turn that was about to end.
func observeInterventionRows(outcomes []storage.AgentInterventionOutcome) []report.Row {
	total := map[string]int{}
	for _, o := range outcomes {
		total[o.Kind] += o.Count
	}
	rows := make([]report.Row, 0, len(outcomes))
	for _, o := range outcomes {
		share := ""
		if n := total[o.Kind]; n > 0 {
			share = fmt.Sprintf("%d of %d (%.0f%%)", o.Count, n, float64(o.Count)/float64(n)*100)
		}
		detail := ""
		if o.Reading != "none" && o.AvgRounds > 0 {
			detail = fmt.Sprintf("%s later", countOf(int(math.Round(o.AvgRounds)), "round", "rounds"))
		}
		rows = append(rows, report.Row{State: report.Queue,
			Name: o.Kind + " → " + o.Reading, Subject: share, Detail: detail})
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

// flakesSince is the checkout's ledger with the checks whose last flake fell
// before since left out. A dashboard outside any checkout has none.
func flakesSince(db *storage.DB, root string, since time.Time) ([]storage.Flake, error) {
	if root == "" {
		return nil, nil
	}
	all, err := db.FlakesFor(root)
	if err != nil {
		return nil, err
	}
	var kept []storage.Flake
	for _, f := range all {
		if !f.LastAt.Before(since) {
			kept = append(kept, f)
		}
	}
	return kept, nil
}

// observeFlakeRows is the gate section's flakes line: how many checks in this
// checkout passed only on their rerun in the window, how often, and the one
// that did it most — a pass rate that counts a flake as a pass is a number
// that hides the check a reader should stop trusting
// (docs/capabilities/testing.md#a-flake-is-counted-where-it-happened).
func observeFlakeRows(flakes []storage.Flake) []report.Row {
	if len(flakes) == 0 {
		return nil
	}
	total, most := 0, flakes[0]
	for _, f := range flakes {
		total += f.Seen
		if f.Seen > most.Seen {
			most = f
		}
	}
	return []report.Row{{State: report.Warn, Name: "flakes",
		Subject:     countOf(len(flakes), "check", "checks") + " in this checkout",
		Detail:      fmt.Sprintf("most %s (%s) · %s", most.Check, most.Suite, countOf(most.Seen, "time", "times")),
		Outcome:     countOf(total, "flake", "flakes"),
		Consequence: "a flake counts as a pass above · `/gate flakes` lists them"}}
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
			Subject: joinDetail(observeKindOf(s), s.Model),
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

// observeKindOf is what kind of session a row was, with the agent's name
// beside it where the row is a child's: three writers of one fan-out are
// otherwise one word three times, told apart only by their row ids.
func observeKindOf(s storage.AgentSessionSummary) string {
	return joinDetail(s.Kind, s.Name)
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
