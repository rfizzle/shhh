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
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/eval"
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

	cmd := &cobra.Command{
		Use:   "eval [suite]",
		Short: "Run a suite of coding tasks and report what the session did",
		Long: "Run each case in a suite: copy its workspace, hand the session the task, then run the case's own check " +
			"over what it left behind. The check decides — nothing here grades the transcript — and the rounds, tokens " +
			"and cost beside each verdict are what make two runs comparable.\n\n" +
			"Every case costs real requests. A suite is a way to find out whether a model, a prompt or a setting change " +
			"actually did the work, and it is not part of `make ci` for that reason.",
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

			cfg := ConfigFrom(cmd.Context())
			flags.ConfigProvider = cfg.Provider.Default
			flags.ConfigModel = cfg.Provider.Model
			flags.ConfigReasoning = cfg.Provider.Reasoning
			resolved := resolve.Resolve(flags)

			prices := loadPricing()
			opts := eval.Options{
				Repeat:  repeat,
				Timeout: timeout,
				Args:    sessionArgs(resolved),
				Model:   resolved.Model,
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
			return report.Fprint(cmd.OutOrStdout(), evalReport(sum))
		},
	}

	addModelFlags(cmd, &flags)
	cmd.Flags().IntVar(&repeat, "repeat", 1, "attempts per case; more than one is what tells a flaky case from a failing one")
	cmd.Flags().DurationVar(&timeout, "timeout", defaultEvalTimeout, "ceiling on one attempt (0 removes it)")
	cmd.Flags().StringArrayVar(&only, "case", nil, "run only this case, by name (repeatable)")
	return cmd
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
func evalReport(sum eval.Summary) report.Report {
	r := report.Report{Title: "shhh eval"}
	if sum.Model != "" {
		r.Subject = sum.Model
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
	}

	row.Subject = evalDetail(res)
	row.Body = evalBody(res)
	return row
}

// evalDetail is the numbers that make two runs comparable, in the spellings
// the rest of the CLI already uses for them: a run reported in one vocabulary
// here and another in `shhh metrics` is two numbers a reader has to reconcile.
func evalDetail(res eval.Result) string {
	var parts []string
	if rounds := res.MedianRounds(); rounds > 0 {
		parts = append(parts, fmt.Sprintf("%.0f rounds", rounds))
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
