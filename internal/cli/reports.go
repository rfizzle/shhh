package cli

// The report store's seat in the CLI: the directory everything asks one
// function for, the publisher sessions register the report tool from, and
// `shhh reports` — the listing that makes a persistent store findable and
// the `open` that re-serves a page long after the session that made it
// (docs/capabilities/reports.md#a-report-outlives-its-session).

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/reports"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/spf13/cobra"
)

// reportsDir is where reports live: beside the store, in the state directory
// the rest of what shhh records goes to
// (docs/capabilities/configuration.md#one-layout-everywhere). The doctor row,
// the listing and the publisher all ask this, so a row that names a directory
// the listing does not read is impossible.
func reportsDir() (string, error) {
	dir, err := storage.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "reports"), nil
}

// openReportsPublisher opens the report store and wires the report tool for
// one session. Failure withholds the tool with a warning instead of blocking
// the session; openBrowser is off for headless runs, where nobody is at the
// desktop a browser would open on.
func openReportsPublisher(cfg config.Config, origin string, openBrowser bool) *reports.Publisher {
	dir, err := reportsDir()
	if err == nil {
		var store *reports.Store
		if store, err = reports.Open(dir, cfg.EffectiveReportsRetentionDays()); err == nil {
			wd, _ := os.Getwd()
			return reports.NewPublisher(store, origin, project.Root(wd), openBrowser)
		}
	}
	fmt.Fprintf(os.Stderr, "warning: report store unavailable, the report tool is not offered: %v\n", err)
	return nil
}

func newReportsCmd() *cobra.Command {
	var asJSON bool
	var all bool

	cmd := &cobra.Command{
		Use:   "reports [--flags]",
		Short: "List the report pages sessions built",
		Long: "List the graphical report pages sessions have published for this project, newest first. " +
			"Reports live in shhh's own state directory, never in the checkout, and are pruned after " +
			"`reports.retention_days`. `shhh reports open <id>` serves one again.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openReportsStore(cmd)
			if err != nil {
				return err
			}
			entries := store.List()
			wd, _ := os.Getwd()
			if !all {
				entries = filterProject(entries, project.Root(wd))
			}
			if asJSON {
				return writeJSON(cmd, reportsJSON(entries))
			}
			return report.Fprint(cmd.OutOrStdout(), reportsReport(entries, all, time.Now()))
		},
	}

	// Declaration order is reading order: widen the listing, then reshape it.
	cmd.Flags().SortFlags = false
	cmd.Flags().BoolVarP(&all, "all", "a", false, "list every project's reports, not just this project's")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the reports as JSON")

	cmd.AddCommand(newReportsOpenCmd())

	return cmd
}

// newReportsOpenCmd serves one stored report again. It blocks while it
// serves: the port is ephemeral, so a command that printed a URL and exited
// would be handing over a dead link.
func newReportsOpenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "open <id>",
		Short: "Serve one report again and open it in the browser",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openReportsStore(cmd)
			if err != nil {
				return err
			}
			id := args[0]
			if _, _, err := store.Load(id); err != nil {
				return fmt.Errorf("%s", notFound("report", id, "`shhh reports`"))
			}
			srv := reports.NewServer(store)
			defer srv.Close()
			url, err := srv.URL(id)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			row := report.Row{State: report.Run, Subject: "serving " + url}
			row.Body = []string{"ctrl+c stops serving"}
			if err := report.Fprintln(out, row); err != nil {
				return err
			}
			_ = reports.OpenBrowser(url)

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			<-ctx.Done()
			return nil
		},
	}
}

// openReportsStore opens the store for a record command; the prune rides the
// open, so listing is also what keeps retention honest.
func openReportsStore(cmd *cobra.Command) (*reports.Store, error) {
	dir, err := reportsDir()
	if err != nil {
		return nil, err
	}
	return reports.Open(dir, ConfigFrom(cmd.Context()).EffectiveReportsRetentionDays())
}

func filterProject(entries []reports.Entry, root string) []reports.Entry {
	var out []reports.Entry
	for _, e := range entries {
		if e.Project == root {
			out = append(out, e)
		}
	}
	return out
}

// reportsReport is the listing as text: the id is the name — it is what
// `open` takes — the title is the subject, and when the listing crosses
// projects the project is the detail beside it.
func reportsReport(entries []reports.Entry, all bool, now time.Time) report.Report {
	r := report.Report{Title: "shhh reports", Subject: countOf(len(entries), "report", "reports")}
	if len(entries) == 0 {
		return emptyInto(r, "no reports made yet",
			"a session's report tool publishes one when an answer is a page")
	}
	var rows []report.Row
	for _, e := range entries {
		row := report.Row{
			State:   report.Pass,
			Name:    e.ID,
			Subject: e.Title,
			Detail:  e.Origin,
			Outcome: historyAgo(e.Created, now),
		}
		if all {
			row.Detail = shortPath(e.Project)
		}
		rows = append(rows, row)
	}
	r.Sections = []report.Section{{Rows: rows}}
	return r
}

// reportJSON is the domain shape `--json` emits — the stored fact, never the
// listing's presentation.
type reportJSON struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Project string    `json:"project"`
	Origin  string    `json:"origin"`
	Created time.Time `json:"created"`
	Size    int64     `json:"size"`
}

func reportsJSON(entries []reports.Entry) []reportJSON {
	out := make([]reportJSON, 0, len(entries))
	for _, e := range entries {
		out = append(out, reportJSON{
			ID: e.ID, Title: e.Title, Project: e.Project,
			Origin: e.Origin, Created: e.Created, Size: e.Size,
		})
	}
	return out
}

// sprintReportPublisher is the door a session's own page goes through, or
// nil where this session has no report store. It is a function rather than
// the publisher itself because the surface that writes the page must not be
// able to reach the rest of the publisher: what it may do is publish one
// document, and everything else on that type belongs to the tool.
func sprintReportPublisher(p *reports.Publisher) func(reports.Document) (string, error) {
	if p == nil {
		return nil
	}
	return p.Publish
}
