package cli

// `shhh update` — the manual trigger for the two things shhh otherwise
// refreshes on its own schedule: the release check that nudges about a newer
// binary, and the model-data table that prices a session and tells each
// provider how its models spell a thinking level. Both are pulled routinely
// (once a day) and quietly; this is the way to pull them now and see the
// answer (docs/capabilities/providers.md#model-data-is-fetched-and-a-snapshot-ships).

import (
	"fmt"
	"io"
	"time"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/update"
	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	var dataOnly bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check for a newer shhh and refresh the model data",
		Long: "Ask the release feed whether a newer shhh exists, and download the current model-data table " +
			"(prices, context windows, and each model's thinking levels). Both are also refreshed on their own " +
			"once a day; this does it now. --data refreshes the table and skips the release check.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if !dataOnly {
				printUpdateCheck(out, version, update.Refresh(version))
			}
			return refreshModelData(out)
		},
	}
	cmd.Flags().BoolVar(&dataOnly, "data", false, "refresh the model data only; skip the release check")
	return cmd
}

// printUpdateCheck states the release check's answer. The binary is not
// replaced: shhh is installed by a package manager or `go install`, and
// swapping a running binary out from under the one that owns it is not a
// thing a command should do without being that package manager.
func printUpdateCheck(out io.Writer, current string, r *update.Result) {
	// The doctor's own readings of the same three answers, so the two
	// commands do not describe one machine in two vocabularies.
	switch {
	case current == "dev" || current == "":
		_ = report.Fprintln(out, report.Row{State: report.Skip,
			Subject: "update check", Detail: "a dev build has no released version"})
	case r == nil && update.Latest() == "":
		_ = report.Fprintln(out, report.Row{State: report.Skip, Subject: "update check",
			Detail: "no answer from the release feed; this says nothing about your install"})
	case r == nil:
		_ = report.Fprintln(out, report.Row{State: report.Pass,
			Subject: "shhh " + current, Detail: "the latest release"})
	default:
		_ = report.Fprintln(out, report.Row{State: report.Warn,
			Subject: "shhh " + r.Latest + " is out", Detail: "this machine is on " + r.Current,
			Fix: []string{
				"brew upgrade shhh                       if it came from the tap",
				"go install github.com/rfizzle/shhh/cmd/shhh@latest",
			}})
	}
}

// refreshModelData downloads the table and says what came of it. A failed
// download is reported and is the command's error — this is the one place
// the download is the point — but the session is no worse for it: the
// snapshot and whatever was cached before still answer.
func refreshModelData(out io.Writer) error {
	table, err := pricing.Refresh()
	if err != nil {
		still := fmt.Sprintf("the built-in snapshot (%d models) is still in use", pricing.Snapshot().Len())
		if !pricing.FetchedAt().IsZero() {
			still = "the copy from " + pricing.FetchedAt().Format(time.RFC1123) + " is still in use"
		}
		// The fault goes on the lines beneath rather than into the target: a
		// download error names a URL and would clip, and the whole point of
		// the row is what went wrong.
		_ = report.Fprintln(out, report.Row{State: report.Fail, Name: "model data",
			Subject: "could not be refreshed", Consequence: still, Fix: []string{err.Error()}})
		return err
	}
	return report.Fprintln(out, report.Row{State: report.Pass, Name: "model data",
		Subject: countOf(table.Len(), "model", "models"),
		Detail:  "fetched " + pricing.FetchedAt().Format(time.RFC1123)})
}
