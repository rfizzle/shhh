package cli

// The diagnostic log and its reader. `shhh logs` is a tail: the lines go out
// as they were written, because a log is bytes a person greps rather than a
// listing shhh shapes. Only the empty state is a report, and it is one
// because a reader who gets nothing back has to be told which of "nothing has
// gone wrong" and "the file is not where I looked" it was.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"syscall"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/logs"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/spf13/cobra"
)

// logTailDefault is how much of the log a bare `shhh logs` prints. Enough to
// cover the session that just failed, few enough to page.
const logTailDefault = 1000

// logPath is where the log lives: beside the store, in the state directory
// the rest of what shhh records goes to
// (docs/capabilities/configuration.md#one-layout-everywhere). The doctor row
// that names it and the command that opens it both ask this, so a check that
// reports a path the reader cannot tail is impossible.
func logPath() (string, error) {
	dir, err := storage.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "shhh.log"), nil
}

// openLog points this process's log at the file, if the state directory can
// be named at all. A machine with no home directory has nowhere to write and
// still has a session to run, so this reports nothing: the log discards, and
// `shhh doctor` is where the absence is a row.
func openLog() {
	if path, err := logPath(); err == nil {
		logs.To(path)
	}
}

func newLogsCmd() *cobra.Command {
	var tail int
	var follow bool

	cmd := &cobra.Command{
		Use:   "logs [--flags]",
		Short: "Print what a session wrote down when a request failed",
		Long: "Print the tail of shhh's diagnostic log: every request a provider refused, " +
			"and anything a library wrote to the standard logger while the screen was borrowed. " +
			"`shhh doctor` names the file.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := logPath()
			if err != nil {
				return err
			}
			return runLogs(cmd.Context(), cmd.OutOrStdout(), path, tail, follow)
		},
	}

	// Declaration order is reading order: how much of the past, then whether
	// to stay for the future.
	cmd.Flags().SortFlags = false
	cmd.Flags().IntVarP(&tail, "tail", "n", logTailDefault, "how many of the last lines to print")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing as the file grows")

	return cmd
}

// runLogs prints the tail and then, when following, whatever arrives after
// it. The offset the tail read to is where the follow starts, so a line
// written between the two is printed once rather than twice or never.
func runLogs(ctx context.Context, out io.Writer, path string, tail int, follow bool) error {
	lines, at, err := logs.Tail(path, tail)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// A log that is not there yet is not a failure to report. Following
		// carries on and waits for it — that is how you watch a request fail
		// from another pane, with the tail already running when it does.
		if !follow {
			return report.Fprint(out, logsEmpty(path))
		}
	case err != nil:
		return err
	}

	for _, line := range lines {
		fmt.Fprintln(out, line)
	}
	if follow {
		// A reader who walked away closed the pipe, which is how a tail ends
		// and not something to report as a fault. Every other write failure
		// still is one.
		if err := logs.Follow(ctx, path, at, out); err != nil && !errors.Is(err, syscall.EPIPE) {
			return err
		}
		return nil
	}
	// An empty log and `--tail 0` both print no lines and mean opposite
	// things. What tells them apart is what was asked for: a reader who
	// wanted none of the past is not told the log is empty.
	if len(lines) == 0 && tail > 0 {
		return report.Fprint(out, logsEmpty(path))
	}
	return nil
}

// logsEmpty is what an unwritten log says. The path goes on the line beneath
// rather than in the row, because it is the answer to the second half of the
// reader's question and a row would clip it on a narrow terminal.
func logsEmpty(path string) report.Report {
	row := report.Empty("nothing has been written to the log", "shhh logs -f waits for it")
	row.Body = []string{shortPath(path)}
	return report.Report{Title: "shhh logs", Sections: []report.Section{{Rows: []report.Row{row}}}}
}
