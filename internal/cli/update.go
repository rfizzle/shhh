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
	switch {
	case current == "dev" || current == "":
		fmt.Fprintln(out, "shhh: a dev build has no released version to compare")
	case r == nil && update.Latest() == "":
		fmt.Fprintln(out, "shhh: no answer from the release feed; this says nothing about your install")
	case r == nil:
		fmt.Fprintf(out, "shhh %s is the latest release\n", current)
	default:
		fmt.Fprintf(out, "shhh %s is out; this machine is on %s\n", r.Latest, r.Current)
		fmt.Fprintln(out, "  brew upgrade shhh                       if it came from the tap")
		fmt.Fprintln(out, "  go install github.com/rfizzle/shhh/cmd/shhh@latest")
	}
}

// refreshModelData downloads the table and says what came of it. A failed
// download is reported and is the command's error — this is the one place
// the download is the point — but the session is no worse for it: the
// snapshot and whatever was cached before still answer.
func refreshModelData(out io.Writer) error {
	table, err := pricing.Refresh()
	if err != nil {
		fmt.Fprintf(out, "model data: %v\n", err)
		if !pricing.FetchedAt().IsZero() {
			fmt.Fprintf(out, "  the copy from %s is still in use\n", pricing.FetchedAt().Format(time.RFC1123))
		} else {
			fmt.Fprintf(out, "  the built-in snapshot (%d models) is still in use\n", pricing.Snapshot().Len())
		}
		return err
	}
	fmt.Fprintf(out, "model data: %d models, fetched %s\n", table.Len(), pricing.FetchedAt().Format(time.RFC1123))
	return nil
}
