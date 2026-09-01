package cmd

import (
	"flag"
	"fmt"
	"io"

	"example.com/ledger/store"
)

// Report prints the entries that fit inside the budget named by --budget.
// The flag is parsed and then ignored, which is the bug.
func Report(w io.Writer, args []string, entries []store.Entry) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	budget := fs.Int64("budget", 0, "ceiling in cents (0 for none)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = budget

	for _, e := range entries {
		fmt.Fprintf(w, "%s\t%d\n", e.Label, e.Cents)
	}
	return nil
}
