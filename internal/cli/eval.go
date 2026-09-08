package cli

// `shhh eval`: what a suite of real tasks does on this setup.
//
// It is the same report shape as every other listing — a row per case, its
// verdict in the outcome field, and the numbers that turn a pass into a
// comparison beneath it. The suite is a directory of cases, defaulting to
// `evals/` beside the checkout, so a project can keep the tasks that matter
// to it next to the code they are about.
// See docs/capabilities/evals.md.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/eval"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/spf13/cobra"
)

// DefaultSuiteDir is where a project's cases live, relative to the working
// directory.
const DefaultSuiteDir = "evals"

// defaultEvalTimeout bounds one attempt. A case that has not finished in this
// long is not going to: the round cap is off in a headless run, so the way a
// bad attempt ends is by being stopped.
const defaultEvalTimeout = 15 * time.Minute

func newEvalCmd() *cobra.Command {
	var flags resolve.Opts
	var repeat int
	var timeout time.Duration
	var only []string
	var baselinePath, comparePath string
	var refresh bool

	cmd := &cobra.Command{
		Use:   "eval [suite]",
		Short: "Run a suite of coding tasks and report what the session did",
		Long: "Run each case in a suite: copy its workspace, hand the session the task, then run the case's own check " +
			"over what it left behind. The check decides — nothing here grades the transcript — and the rounds, tokens " +
			"and cost beside each verdict are what make two runs comparable.\n\n" +
			"A case with no workspace is a labelled table instead, put to one of the calls a session makes beside the " +
			"coding loop — a permission decision, a status reading — and scored by comparing the answer with the label. " +
			"Those are made on the model named here, so name the one your sessions actually make them on.\n\n" +
			"A research case brings a site instead of a workspace, served over loopback for the length of the run, and " +
			"reports three rates over the write-up: the pages it cited that it actually read, the sentences it quoted " +
			"that are on the page it named, and the facts the case requires.\n\n" +
			"A scripted case has no model in it at all: it puts one of the harness's own mechanisms — the steer, the " +
			"window recovery, the spawn and patch loop, the quality gate — to a labelled table with the model's part " +
			"written down, so what it measures is the wiring around the model and it costs nothing to run.\n\n" +
			"Every run is read against the suite's own `baseline.json` unless `--compare` names another file, and " +
			"`--refresh-baseline` is how that file is replaced — deliberately, so a change to what the suite is " +
			"expected to produce is reviewed like any other change to it.\n\n" +
			"Every case that names a model costs real requests. A suite is a way to find out whether a model, a prompt " +
			"or a setting change actually did the work, and it is not part of `make ci` for that reason.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := DefaultSuiteDir
			if len(args) == 1 {
				dir = args[0]
			}
			cases, err := eval.Load(dir)
			if err != nil {
				return err
			}
			if cases, err = selectCases(cases, only); err != nil {
				return err
			}

			if refresh && len(only) > 0 {
				return fmt.Errorf("--refresh-baseline writes the whole suite's baseline, so it cannot be taken from a run of part of it")
			}

			cfg := ConfigFrom(cmd.Context())
			flags.ConfigProvider = cfg.Provider.Default
			flags.ConfigModel = cfg.Provider.Model
			flags.ConfigReasoning = cfg.Provider.Reasoning
			resolved := resolve.Resolve(flags)

			// A run of scripted cases alone measured no model, and the name a
			// flag or the config would have resolved to is not a reading of
			// anything. Recorded, it would put a model in the baseline that
			// nothing asked, and the next comparison would report a change of
			// model that never happened.
			if !measuresAModel(cases) {
				resolved.Model = ""
			}

			// A table or research case has no session process to run, so the
			// harness makes the requests itself and needs a provider here. A
			// suite of workspace cases does not, and must not be stopped at
			// the door by a credential it was never going to use.
			//
			// A provider that will not resolve skips the cases that need one
			// rather than ending the run. It is the same fact as a toolchain
			// a case requires being absent — this machine cannot run that
			// case — and the run still has the scripted cases to measure,
			// which are exactly the ones a machine with no account has. The
			// report says so on every row it skipped and once at the top,
			// because a mistyped key must not read as a suite that passed.
			var prov provider.Provider
			var unprovided string
			if needsProvider(cases) {
				req := providerRequest{Provider: resolved.Provider, Model: resolved.Model, APIKey: flags.FlagAPIKey}
				p, err := tryProvider(cfg, req)
				switch {
				case err == nil:
					prov = p
				case anyScripted(cases):
					unprovided = firstLineOf(err.Error())
					cases = skipUnprovided(cases, unprovided)
					// And the run is recorded as having measured no model,
					// because it did not. The name a flag or the config
					// would have resolved to is what the skipped cases
					// would have asked, and a baseline carrying it would
					// claim a reading on a model nothing here spoke to.
					resolved.Model = ""
				default:
					// Nothing here can run without a model, so this is the
					// ordinary failure every other command has: the setup
					// card, and the places it looked.
					p, req, err = resolveProvider(cmd.Context(), cfg, req)
					if err != nil {
						return err
					}
					resolved.Provider, resolved.Model = req.Provider, req.Model
					prov = p
				}
			}

			prices := loadPricing()
			opts := eval.Options{
				Repeat:   repeat,
				Timeout:  timeout,
				Args:     sessionArgs(resolved),
				Model:    resolved.Model,
				Provider: prov,
				Price: func(model string, in, out int) (float64, bool) {
					if prices == nil {
						return 0, false
					}
					inCost, outCost, ok := prices.Cost(model, int64(in), int64(out))
					return inCost + outCost, ok
				},
				Progress: evalProgress(cmd, len(cases), repeat),
			}

			sum, err := eval.Run(cmd.Context(), cases, opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if err := report.Fprint(out, evalReport(sum, unprovided)); err != nil {
				return err
			}

			// The baseline to compare against is read before the new one is
			// written, and the new one is written before either failure is
			// returned. Both halves of that matter: `--baseline b --compare
			// b` is the obvious way to keep a rolling baseline, and writing
			// first would have it compare the run with itself and report no
			// change every time — while returning early on a comparison that
			// refuses would throw away a run that has already been paid for.
			committed := filepath.Join(dir, eval.BaselineFile)
			// The suite's own baseline is the default comparison, and a suite
			// that has none yet is not an error: the reader is told how to
			// write one, and gets their report either way.
			against := comparePath
			if _, statErr := os.Stat(committed); against == "" && statErr == nil {
				against = committed
			}
			var before eval.Baseline
			var compareErr error
			if against != "" {
				before, compareErr = eval.ReadBaseline(against)
			}
			if baselinePath != "" {
				if err := eval.WriteBaseline(baselinePath, sum.Baseline()); err != nil {
					return err
				}
			}
			if refresh {
				if err := eval.WriteBaseline(committed, sum.Baseline()); err != nil {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "wrote %s — what moved in it is the diff a reviewer reads\n", committed)
			}
			if compareErr != nil {
				return compareErr
			}
			if against == "" {
				if !refresh {
					_ = report.Fprintln(cmd.ErrOrStderr(), report.Row{State: report.Skip,
						Subject: fmt.Sprintf("no %s in %s, so this run was read against nothing — --refresh-baseline writes one", eval.BaselineFile, dir)})
				}
				return nil
			}
			// A run narrowed to named cases is compared against those rows of
			// the baseline alone. Nothing else narrows it: a case the run
			// measured and the file does not hold is still refused, because
			// there is nothing to read it against.
			if len(only) > 0 {
				before = eval.Narrow(before, only)
			}
			cmp, err := eval.Compare(before, sum.Baseline())
			if err != nil {
				return err
			}
			return report.Fprint(out, compareReport(cmp, time.Now()))
		},
	}

	addModelFlags(cmd, &flags)
	cmd.Flags().IntVar(&repeat, "repeat", 1, "attempts per case; more than one is what tells a flaky case from a failing one")
	cmd.Flags().DurationVar(&timeout, "timeout", defaultEvalTimeout, "ceiling on one attempt (0 removes it)")
	cmd.Flags().StringArrayVar(&only, "case", nil, "run only this case, by name (repeatable)")
	cmd.Flags().StringVar(&baselinePath, "baseline", "", "write this run's verdicts and medians to this file")
	cmd.Flags().StringVar(&comparePath, "compare", "", "read this baseline instead of the suite's own and print the delta beneath the report")
	cmd.Flags().BoolVar(&refresh, "refresh-baseline", false, "write this run over the suite's committed baseline, so what changed in it is reviewed like code")
	return cmd
}

// needsProvider reports whether anything selected asks a model from this
// process rather than from a session it starts. A scripted case asks nothing
// of anybody (internal/eval/mechanism.go).
func needsProvider(cases []eval.Case) bool {
	for _, c := range cases {
		if !c.Kind.RunsBinary() && !c.Kind.Scripted() {
			return true
		}
	}
	return false
}

// measuresAModel reports whether anything in the run puts a question to one —
// in this process or in a session it starts.
func measuresAModel(cases []eval.Case) bool {
	for _, c := range cases {
		if !c.Kind.Scripted() {
			return true
		}
	}
	return false
}

// anyScripted reports whether the run has a case that can be measured
// without an account, which is what decides whether a provider that will not
// resolve is a skipped case or the end of the run.
func anyScripted(cases []eval.Case) bool {
	for _, c := range cases {
		if c.Kind.Scripted() {
			return true
		}
	}
	return false
}

// skipUnprovided marks every case that needs a model as one this machine
// cannot run, with the resolution's own sentence as the reason.
//
// A workspace case is one of them: it starts a session, and a session with
// nowhere to send a request fails at its first round. Skipping it says what
// happened; letting it run would report a machine with no account as a
// harness that cannot do the task.
func skipUnprovided(cases []eval.Case, why string) []eval.Case {
	out := make([]eval.Case, 0, len(cases))
	for _, c := range cases {
		if !c.Kind.Scripted() && c.Skip == "" {
			c.Skip = "no model to ask: " + why
		}
		out = append(out, c)
	}
	return out
}

// selectCases narrows the suite to the named cases, refusing a name that
// matches nothing rather than silently measuring less than was asked for.
func selectCases(cases []eval.Case, only []string) ([]eval.Case, error) {
	if len(only) == 0 {
		return cases, nil
	}
	want := make(map[string]bool, len(only))
	for _, n := range only {
		want[n] = true
	}
	var out []eval.Case
	for _, c := range cases {
		if want[c.Name] {
			out = append(out, c)
			delete(want, c.Name)
		}
	}
	for n := range want {
		return nil, fmt.Errorf("no case named %q in the suite", n)
	}
	return out, nil
}

// sessionArgs is the resolved provider and model as the flags the child
// session takes, so what is measured is what those flags produce.
func sessionArgs(r resolve.Resolved) []string {
	var args []string
	if r.Provider != "" {
		args = append(args, "--provider", r.Provider)
	}
	if r.Model != "" {
		args = append(args, "--model", r.Model)
	}
	if r.Reasoning != "" {
		args = append(args, "--reasoning", r.Reasoning)
	}
	return args
}

// evalProgress writes a line per attempt to stderr, because a suite takes
// minutes and a command that says nothing for minutes reads as hung. It is
// stderr so the report on stdout stays the command's output.
func evalProgress(cmd *cobra.Command, cases, repeat int) func(eval.Case, int, eval.Attempt) {
	if repeat < 1 {
		repeat = 1
	}
	done := 0
	total := cases * repeat
	return func(c eval.Case, attempt int, a eval.Attempt) {
		done++
		outcome := "pass"
		switch {
		case a.Err != nil:
			outcome = "error"
		case !a.Passed:
			outcome = "fail"
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "[%d/%d] %s attempt %d: %s (%s)\n",
			done, total, c.Name, attempt, outcome, a.Elapsed.Round(time.Second))
	}
}

// evalReport is the summary as the shape every listing prints in.
func evalReport(sum eval.Summary, unprovided string) report.Report {
	r := report.Report{Title: "shhh eval"}
	if sum.Model != "" {
		r.Subject = sum.Model
	}
	if unprovided != "" {
		r.Notes = append(r.Notes, report.Note{State: report.Warn,
			Text: "no model could be resolved (" + unprovided + "), so every case that asks one was skipped; " +
				"what ran is the scripted set, which measures the harness and not the model"})
	}
	section := report.Section{}
	for _, res := range sum.Results {
		section.Rows = append(section.Rows, evalRow(res))
	}
	r.Sections = []report.Section{section}

	passed, flaky, failed, skipped, errored := sum.Tally()
	var parts []string
	parts = append(parts, fmt.Sprintf("%d passed", passed))
	if flaky > 0 {
		parts = append(parts, fmt.Sprintf("%d flaky", flaky))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	if errored > 0 {
		parts = append(parts, fmt.Sprintf("%d never ran", errored))
	}
	if skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", skipped))
	}
	parts = append(parts, components.FormatElapsed(sum.Elapsed().Round(time.Second)))
	if cost, priced := sum.Cost(); priced {
		parts = append(parts, metricsSpend(cost, priced))
	}
	r.Tally = strings.Join(parts, " · ")
	return r
}

// evalRow is one case's line.
//
// The subject is the measurement, not the task. A row clips its target to fit
// the stream, and a redirected report measures 80 columns — which is exactly
// what a reader does with a run that takes seven minutes. With the prompt in
// the subject the numbers were the part clipped away, and they are the only
// part that is not already somewhere else: what was asked is a property of the
// suite and can be read there, and what it cost is a property of this run and
// exists nowhere but this row.
func evalRow(res eval.Result) report.Row {
	row := report.Row{Name: res.Case.Name}
	switch res.Verdict() {
	case eval.Passed:
		row.State, row.Outcome = report.Pass, "passed"
	case eval.Flaky:
		row.State, row.Outcome = report.Warn, "flaky"
		row.Consequence = fmt.Sprintf("passed %d of %d attempts — the task is one this setup can lose, not one it cannot do",
			res.Passes(), len(res.Attempts))
	case eval.Failed:
		row.State, row.Outcome = report.Fail, "failed"
	case eval.Skipped:
		row.State, row.Outcome = report.Skip, "skipped"
		row.Subject = res.Case.Skip
		return row
	case eval.Errored:
		row.State, row.Outcome = report.Fail, "never ran"
		row.Consequence = "the session did not finish, so nothing was checked — this says nothing about the task"
		switch {
		case res.Case.Kind.Scripted():
			// A scripted case asked no model, so a broken one is a fact
			// about this machine and never about the thing being measured.
			row.Consequence = "the table did not finish, so any rate off it is over a prefix — this says nothing about the mechanism"
		case res.Case.Kind.IsTable():
			row.Consequence = "the table did not finish, so any rate off it is over a prefix — this says nothing about the model"
		}
	}

	row.Subject = evalDetail(res)
	row.Body = evalBody(res)
	if score, ok := res.Score(); ok {
		if c := tableConsequence(score); c != "" {
			row.Consequence = c
		}
		row.Body = append(row.Body, tableBody(score)...)
	}
	if score, ok := res.Research(); ok {
		if c := researchConsequence(score); c != "" {
			row.Consequence = c
		}
		row.Body = append(row.Body, researchBody(score)...)
	}
	return row
}

// researchConsequence is what a research case's misses cost, and it leads on
// the citation because that is the one that costs something outside the
// suite: a write-up short of a fact is incomplete, and one citing a page it
// never opened is a source a reader will go and check and not find.
func researchConsequence(score eval.ResearchScore) string {
	var parts []string
	if n := score.Cited.Of - score.Cited.Got; n > 0 {
		parts = append(parts, fmt.Sprintf("%d cited, not read", n))
	}
	if n := score.Quoted.Of - score.Quoted.Got; n > 0 {
		parts = append(parts, fmt.Sprintf("%d quoted, not on the page", n))
	}
	if n := score.Facts.Of - score.Facts.Got; n > 0 {
		parts = append(parts, fmt.Sprintf("%d fact missing", n))
	}
	if len(parts) == 0 {
		return ""
	}
	line := strings.Join(parts, " · ")
	if score.Cited.Of > score.Cited.Got {
		line += " — a citation the run never fetched is the failure the ledger exists to catch"
	}
	return line
}

// maxResearchMisses is how many missed lines a research row names before
// counting the rest, for the reason a table row has the same ceiling.
const maxResearchMisses = 8

// researchBody names what came out wrong, in the order the three checks run.
func researchBody(score eval.ResearchScore) []string {
	var out []string
	seen := map[string]bool{}
	left := 0
	for _, line := range score.Misses {
		if seen[line] {
			continue
		}
		seen[line] = true
		if len(out) == maxResearchMisses {
			left++
			continue
		}
		out = append(out, line)
	}
	if left > 0 {
		out = append(out, fmt.Sprintf("… and %d more", left))
	}
	return out
}

// tableConsequence is what a table case's misses cost, counted apart rather
// than averaged.
//
// A classifier that refuses too much is an annoyance; one that allows too
// much is the security control failing open, and a single accuracy figure
// reports the two as the same number. A row that answered nothing is a third
// thing again — that is a broken call, not a cautious one, and scoring it as
// a deny would report an outage as a security posture.
func tableConsequence(score eval.Score) string {
	var parts []string
	if n := score.FalseAllow(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d false allow", n))
	}
	if n := score.FalseDeny(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d false deny", n))
	}
	if n := score.Wrong() - score.FalseAllow() - score.FalseDeny(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d wrong", n))
	}
	if n := score.Unanswered(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d with no answer", n))
	}
	if len(parts) == 0 {
		return ""
	}
	line := strings.Join(parts, " · ") + fmt.Sprintf(" out of %d rows", score.Rows())
	if score.FalseAllow() > 0 {
		line += " — a false allow is the control failing open, which a false deny is not"
	}
	return line
}

// maxTableMisses is how many missed rows a report names before counting the
// rest. A table that missed everything is a finding on its own and does not
// need forty lines to say so.
const maxTableMisses = 8

// tableBody names the rows that missed, in the order the table lists them,
// each with what the row was written to test — "this one came back allow" is
// only actionable beside the rule it was checking.
//
// A row that missed the same way on every attempt is named once, the way a
// case that failed the same way three times is: repeated attempts are more
// samples of the same rows, and a row that missed two different ways is the
// fact worth two lines.
func tableBody(score eval.Score) []string {
	var out []string
	seen := map[string]bool{}
	left := 0
	for _, a := range score.Misses() {
		got := "no answer"
		if a.Answered() {
			got = a.Label
		}
		line := fmt.Sprintf("%s — wanted %s, got %s", a.Row.Name, strings.Join(a.Row.Expect, " or "), got)
		if a.Row.Why != "" {
			line += ": " + a.Row.Why
		}
		if seen[line] {
			continue
		}
		seen[line] = true
		if len(out) == maxTableMisses {
			left++
			continue
		}
		out = append(out, line)
	}
	if left > 0 {
		out = append(out, fmt.Sprintf("… and %d more", left))
	}
	return out
}

// evalDetail is the numbers that make two runs comparable, in the spellings
// the rest of the CLI already uses for them: a run reported in one vocabulary
// here and another in `shhh metrics` is two numbers a reader has to reconcile.
func evalDetail(res eval.Result) string {
	var parts []string
	if score, ok := res.Score(); ok {
		parts = append(parts, fmt.Sprintf("%d of %d correct", score.Correct(), score.Rows()))
	}
	// Three rates and never their average. A write-up can carry every fact
	// and cite a page it never opened, and one number would report that as a
	// good run with a rounding error in it.
	if score, ok := res.Research(); ok {
		parts = append(parts,
			score.Cited.String()+" cited read",
			score.Quoted.String()+" quotes found",
			score.Facts.String()+" facts")
	}
	if rounds := res.MedianRounds(); rounds > 0 {
		parts = append(parts, eval.FormatRounds(rounds)+" rounds")
	}
	if tokens := res.Median(func(a eval.Attempt) float64 { return float64(a.TokensIn + a.TokensOut) }); tokens > 0 {
		parts = append(parts, tokenCount(int64(tokens))+" tokens")
	}
	if secs := res.Median(func(a eval.Attempt) float64 { return a.Elapsed.Seconds() }); secs > 0 {
		parts = append(parts, components.FormatElapsed(time.Duration(secs)*time.Second))
	}
	if cost, priced := res.Cost(); priced {
		parts = append(parts, metricsSpend(cost, priced))
	}
	return strings.Join(parts, " · ")
}

// evalBody is why a case did not pass, which is the only thing a failing row
// is read for. Every attempt's reason is shown rather than the first: a case
// that failed three different ways is a different problem from one that
// failed the same way three times.
func evalBody(res eval.Result) []string {
	var out []string
	seen := map[string]bool{}
	for _, a := range res.Attempts {
		var line string
		switch {
		case a.Err != nil:
			line = a.Err.Error()
		case a.Score != nil, a.Research != nil:
			// A table or research attempt ran no check and printed nothing.
			// What it did instead is its rows or its three rates, which are
			// listed beside this.
			continue
		case !a.Passed:
			line = firstLineOf(a.CheckOutput)
		default:
			continue
		}
		if line != "" && !seen[line] {
			seen[line] = true
			out = append(out, line)
		}
	}
	return out
}

func firstLineOf(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return "the check failed and printed nothing"
}

// compareReport is this run read against a baseline: a row per case, what
// moved, and which way.
//
// It is the run's own report shape again rather than a table of its own. A
// reader has just read the report above it, and a second grid to learn is a
// second grid to misread — the delta's rows differ only in carrying two
// numbers where the report carried one.
// See docs/capabilities/evals.md#a-run-can-be-compared-with-the-last-one.
func compareReport(cmp eval.Comparison, now time.Time) report.Report {
	r := report.Report{Title: "shhh eval --compare",
		Subject: "against a run from " + historyAgo(cmp.Before.Recorded, now)}

	section := report.Section{}
	withheld := 0
	for _, d := range cmp.Cases {
		section.Rows = append(section.Rows, compareRow(d))
		// Only a row that shows a pair of counts is a row with a rate
		// missing from it. A case attempted once has none to withhold, and
		// one that is not comparable prints neither.
		if d.Change != eval.Incomparable && carriesCounts(d) && !d.ReadableRate() {
			withheld++
		}
	}
	r.Sections = []report.Section{section}

	// A comparison across models is a legitimate thing to want — it is most
	// of why anyone keeps a baseline — but a reader looking for the effect of
	// a prompt edit should not have to discover the model moved underneath
	// them by reading two file headers.
	if cmp.Before.Model != cmp.After.Model {
		r.Notes = append(r.Notes, report.Note{State: report.Warn,
			Text: fmt.Sprintf("the baseline was measured on %s and this run on %s, so every row carries that change too",
				modelOrUnknown(cmp.Before.Model), modelOrUnknown(cmp.After.Model))})
	}
	if withheld > 0 {
		r.Notes = append(r.Notes, report.Note{State: report.Skip,
			Text: fmt.Sprintf("counts and no rate on %s: under %d samples a side, one sample moves a percentage "+
				"further than anything being measured here does",
				countOf(withheld, "row", "rows"), eval.MinRateSamples)})
	}

	improved, regressed, unchanged, incomparable := cmp.Tally()
	// A run that moved nothing gets a sentence rather than a count of
	// zeroes: it is the answer the reader ran the comparison for, and
	// "0 regressed · 0 improved · 5 unchanged" makes them work it out.
	if regressed == 0 && improved == 0 && incomparable == 0 {
		r.Tally = "no change across " + countOf(unchanged, "case", "cases")
		return r
	}
	var parts []string
	if regressed > 0 {
		parts = append(parts, fmt.Sprintf("%d regressed", regressed))
	}
	if improved > 0 {
		parts = append(parts, fmt.Sprintf("%d improved", improved))
	}
	parts = append(parts, fmt.Sprintf("%d unchanged", unchanged))
	if incomparable > 0 {
		parts = append(parts, fmt.Sprintf("%d not comparable", incomparable))
	}
	r.Tally = strings.Join(parts, " · ")
	return r
}

func modelOrUnknown(model string) string {
	if model == "" {
		return "an unnamed model"
	}
	return model
}

// compareRow is one case between the two runs.
func compareRow(d eval.Delta) report.Row {
	row := report.Row{Name: d.Name, Subject: compareSubject(d), Detail: compareDetail(d)}
	switch d.Change {
	case eval.Regressed:
		row.State, row.Outcome, row.Consequence = report.Fail, "regressed", d.Why
	case eval.Improved:
		row.State, row.Outcome = report.Pass, "improved"
	case eval.Incomparable:
		row.State, row.Outcome, row.Consequence = report.Skip, "not comparable", d.Why
	default:
		row.State, row.Outcome = report.Pass, "unchanged"
	}
	return row
}

// compareSubject is what the case was decided as, both times.
//
// A side that was skipped or never ran has no counts, and a pair like
// `0 of 3 → 0 of 0` reads as three attempts that stopped passing rather than
// as a case the machine did not run. Those rows carry the two words alone.
func compareSubject(d eval.Delta) string {
	if d.Change == eval.Incomparable {
		return d.Before.Verdict + " → " + d.After.Verdict
	}
	if before, after := d.Before.Table, d.After.Table; before != nil && after != nil {
		return fmt.Sprintf("%d of %d → %d of %d correct%s", before.Correct, before.Rows,
			after.Correct, after.Rows, rateShift(d, before.Correct, before.Rows, after.Correct, after.Rows))
	}
	line := d.Before.Verdict + " → " + d.After.Verdict
	if carriesCounts(d) {
		line += fmt.Sprintf(" · %d of %d → %d of %d passed%s", d.Before.Passes, d.Before.Attempts,
			d.After.Passes, d.After.Attempts,
			rateShift(d, d.Before.Passes, d.Before.Attempts, d.After.Passes, d.After.Attempts))
	}
	return line
}

// carriesCounts reports whether the row shows a pair of counts a rate could
// be drawn from. A table row always does; a workspace row does once the case
// was attempted more than once, and a single attempt has no rate to print or
// to withhold.
func carriesCounts(d eval.Delta) bool {
	if d.Before.Table != nil && d.After.Table != nil {
		return true
	}
	return d.Before.Attempts > 1 || d.After.Attempts > 1
}

// rateShift is the pair of percentages, or nothing where there are too few
// samples for one to mean anything. The counts are printed either way, so a
// row that withholds its rate is still a row with numbers in it.
func rateShift(d eval.Delta, beforeN, beforeOf, afterN, afterOf int) string {
	if !d.ReadableRate() || beforeOf == 0 || afterOf == 0 {
		return ""
	}
	return fmt.Sprintf(" (%.0f%% → %.0f%%)", 100*float64(beforeN)/float64(beforeOf), 100*float64(afterN)/float64(afterOf))
}

// compareDetail is what the verdict cost, both times: the outcomes counted
// apart for a table, then the two numbers a comparison is read on.
//
// False allow leads the line for a table because it is the one number here
// that costs something outside the suite — a control that lets one more
// action through has failed a little further open, whatever the totals did.
//
// A figure that is the same on both sides is left out. The row is clipped to
// the terminal from the target end, so `6 → 6 rounds` is width taken from the
// numbers that actually moved, which are the only reason the row is here.
func compareDetail(d eval.Delta) string {
	var parts []string
	if before, after := d.Before.Table, d.After.Table; before != nil && after != nil {
		parts = append(parts, countShift(before.FalseAllow, after.FalseAllow, "false allow")...)
		parts = append(parts, countShift(before.FalseDeny, after.FalseDeny, "false deny")...)
		parts = append(parts, countShift(before.Unanswered, after.Unanswered, "with no answer")...)
	}
	if before, after := d.Before.Research, d.After.Research; before != nil && after != nil {
		parts = append(parts, countShift(before.Citations, after.Citations, "cited, not read")...)
		parts = append(parts, researchShift(before.Facts, after.Facts, "facts")...)
		parts = append(parts, researchShift(before.Quoted, after.Quoted, "quotes found")...)
	}
	if before, after := eval.FormatRounds(d.Before.Rounds), eval.FormatRounds(d.After.Rounds); before != after {
		parts = append(parts, before+" → "+after+" rounds")
	}
	if before, after := spendPair(d); before != after {
		parts = append(parts, before+" → "+after)
	}
	return strings.Join(parts, " · ")
}

// spendPair is what the case cost either side, or two empty strings where
// nothing was priced — which the caller reads as a pair that did not move and
// so prints nothing, rather than as two dashes taking the width the numbers
// that did move needed.
func spendPair(d eval.Delta) (before, after string) {
	if !d.Before.Priced || !d.After.Priced {
		return "", ""
	}
	return metricsSpend(d.Before.Cost, true), metricsSpend(d.After.Cost, true)
}

// researchShift is one research rate either side, and nothing where it did not
// move — a pair of identical fractions is width taken from the ones that did.
func researchShift(before, after eval.Rate, label string) []string {
	if before == after {
		return nil
	}
	return []string{before.String() + " → " + after.String() + " " + label}
}

// countShift is one outcome either side, and nothing at all where it never
// happened in either run: a row of zeroes is the width the numbers that did
// move needed.
func countShift(before, after int, label string) []string {
	if before == 0 && after == 0 {
		return nil
	}
	return []string{fmt.Sprintf("%d → %d %s", before, after, label)}
}
