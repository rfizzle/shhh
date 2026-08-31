package cli

// Backlog wiring: `shhh todo` prints the project's backlog the way a session
// here would read it — ready items first, then what each waiting item is
// waiting on, then the files that could not be read.

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/spf13/cobra"
)

// loadTodos reads the backlog of the checkout the working directory is in.
func loadTodos() *todo.Store {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return todo.Load(todo.Root(cwd))
}

// todoListing is the `shhh todo` output: one row per active item in backlog
// order, marked ready or naming what it waits on, then warnings and
// diagnostics. Ready is stated per row rather than as a separate list so a
// blocked item and a waiting item are told apart where they sit.
func todoListing(s *todo.Store) string { return todoReport(s).String() }

// todoReport is the backlog as a report. A file that would not load is a note
// rather than a line after the listing: it is why an item the reader expected
// is not on the screen.
func todoReport(s *todo.Store) report.Report {
	r := report.Report{Title: "shhh todo"}
	if s.Len() == 0 && len(s.Diagnostics) == 0 {
		// The way out is short enough to survive a narrow terminal, and the
		// directory it resolves to goes on the line under it: a way out that
		// clips is a way out the reader cannot take.
		empty := report.Empty("no backlog here", "write .shhh/todo/<name>.md with a --- header")
		empty.Body = []string{todo.Dir(s.Root)}
		r.Sections = append(r.Sections, report.Section{Rows: []report.Row{empty}})
		return r
	}
	tally := []string{fmt.Sprintf("%d ready", len(s.Ready()))}
	if n := s.Count(todo.StatusBlocked); n > 0 {
		tally = append(tally, fmt.Sprintf("%d blocked", n))
	}
	if n := len(s.Done); n > 0 {
		tally = append(tally, fmt.Sprintf("%d archived", n))
	}
	r.Subject = countOf(s.Len(), "item", "items")
	r.Tally = strings.Join(tally, " · ")

	rows := make([]report.Row, 0, s.Len())
	for _, it := range s.Items {
		rows = append(rows, report.Row{
			State:   todoRowState(s, it),
			Name:    it.Slug,
			Subject: clipRunes(it.Title, 72),
			Detail:  joinDetail(string(it.Kind), joinDetail(string(it.Priority), todoSize(it))),
			Outcome: todoState(s, it),
		})
		for _, w := range it.Warnings {
			r.Notes = append(r.Notes, report.Note{State: report.Warn, Text: it.Path + ": " + w})
		}
	}
	if len(rows) > 0 {
		r.Sections = []report.Section{{Rows: rows}}
	}
	for _, d := range s.Diagnostics {
		r.Notes = append(r.Notes, report.Note{State: report.Fail, Text: d})
	}
	return r
}

// todoRowState is the glyph an item wears: what it is waiting on is the whole
// reason to scan this listing, so a blocked item and one waiting on a
// dependency do not look like one that could be started now.
func todoRowState(s *todo.Store, it todo.Item) report.State {
	switch {
	case it.Status == todo.StatusBlocked:
		return report.Fail
	case it.Status == todo.StatusInProgress:
		return report.Run
	case it.Status != todo.StatusOpen:
		return report.Pass
	case len(s.Waiting(it)) > 0:
		return report.Skip
	}
	return report.Queue
}

// todoSize is the item's size where it declared one; an item that did not
// says nothing rather than a dash.
func todoSize(it todo.Item) string { return string(it.Size) }

// todoState is the row's state column: the status, or for an open item
// whether it is ready and if not what it waits on.
func todoState(s *todo.Store, it todo.Item) string {
	if it.Status != todo.StatusOpen {
		return string(it.Status)
	}
	if waiting := s.Waiting(it); len(waiting) > 0 {
		return "waits on " + strings.Join(waiting, ", ")
	}
	return "ready"
}

// todoDetail is `shhh todo show <slug>`: the header as read, then the body
// as written.
func todoDetail(s *todo.Store, it todo.Item) string {
	r := report.Report{
		Title:   "shhh todo " + it.Slug,
		Subject: clipRunes(it.Title, 72),
	}
	pairs := []report.Pair{{Key: "status", Value: todoState(s, it)}, {Key: "priority", Value: string(it.Priority)}}
	for _, p := range []report.Pair{
		{Key: "kind", Value: string(it.Kind)},
		{Key: "size", Value: string(it.Size)},
		{Key: "depends on", Value: strings.Join(it.DependsOn, ", ")},
		{Key: "created", Value: it.Created},
		{Key: "session", Value: it.Session},
	} {
		if p.Value != "" {
			pairs = append(pairs, p)
		}
	}
	for _, f := range it.Extra {
		pairs = append(pairs, report.Pair{Key: f.Key, Value: f.Value})
	}
	pairs = append(pairs, report.Pair{Key: "file", Value: it.Path})
	r.Sections = []report.Section{{Pairs: pairs}}
	if body := strings.TrimSpace(it.Body); body != "" {
		r.Sections = append(r.Sections, report.Section{Body: body})
	}
	for _, w := range it.Warnings {
		r.Notes = append(r.Notes, report.Note{State: report.Warn, Text: w})
	}
	return r.String()
}

func newTodoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "todo",
		Short: "List the project's backlog",
		Long:  "List the backlog items under the checkout's .shhh/todo directory in the order a session would work them, with what each is waiting on and why any file failed to load.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return report.Fprint(cmd.OutOrStdout(), todoReport(loadTodos()))
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show <slug>",
		Short: "Show one backlog item, header and body",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s := loadTodos()
			it, ok := s.Find(args[0])
			if !ok {
				return fmt.Errorf("no backlog item %q; `shhh todo` lists them", args[0])
			}
			fmt.Fprintln(cmd.OutOrStdout(), todoDetail(s, it))
			return nil
		},
	})
	return cmd
}

// todoManager backs the textual /todo subcommands. Every write goes through
// the store's line edits, so a command changes the fact it names and nothing
// else in the file. See docs/capabilities/todo.md#an-item-is-a-file-you-can-edit.
func todoManager(root string) func(args []string) string {
	return func(args []string) string {
		const usage = "Usage: /todo [list] · /todo show <slug> · /todo add <text> · /todo block <slug> [why] · /todo open <slug> · /todo done <slug> · /todo drop <slug> · /todo edit <slug>"
		s := todo.Load(root)
		if len(args) == 0 || (len(args) == 1 && args[0] == "list") {
			return todoListing(s)
		}
		slug := ""
		if len(args) > 1 {
			slug = args[1]
		}
		switch args[0] {
		case "show":
			if slug == "" {
				return usage
			}
			it, ok := s.Find(slug)
			if !ok {
				return notFound("backlog item", slug, "/todo")
			}
			return todoDetail(s, it)
		case "add":
			title := strings.TrimSpace(strings.Join(args[1:], " "))
			if title == "" {
				return usage
			}
			it := todo.Item{
				Slug:     todo.Slugify(title),
				Title:    title,
				Kind:     todo.KindStory,
				Priority: todo.PriorityMedium,
				Created:  time.Now().Format("2006-01-02"),
				Body:     todoTemplate,
			}
			path, err := todo.Create(root, it)
			if err != nil {
				return "Error: " + err.Error()
			}
			return report.Report{Sections: []report.Section{{Rows: []report.Row{
				report.Done("added", it.Slug+" · "+string(it.Kind)+" · medium"),
				{State: report.Run, Subject: "fill in the criteria", Detail: "/todo edit " + it.Slug, Body: []string{path}},
			}}}}.String()
		case "block":
			if slug == "" {
				return usage
			}
			it, ok := activeItem(s, slug)
			if !ok {
				return notFound("active backlog item", slug, "/todo")
			}
			if err := todo.SetStatus(it.Path, todo.StatusBlocked); err != nil {
				return "Error: " + err.Error()
			}
			why := strings.TrimSpace(strings.Join(args[2:], " "))
			if why != "" {
				if err := todo.Append(it.Path, "## Blocked\n"+why); err != nil {
					return "Error: " + err.Error()
				}
			}
			return report.Report{Sections: []report.Section{{Rows: []report.Row{report.Done("blocked", slug)}}}}.String()
		case "open":
			if slug == "" {
				return usage
			}
			it, ok := activeItem(s, slug)
			if !ok {
				return notFound("active backlog item", slug, "/todo")
			}
			if err := todo.SetStatus(it.Path, todo.StatusOpen); err != nil {
				return "Error: " + err.Error()
			}
			return report.Report{Sections: []report.Section{{Rows: []report.Row{report.Done("reopened", slug)}}}}.String()
		case "done":
			if slug == "" {
				return usage
			}
			to, err := todo.Archive(root, slug, "")
			if err != nil {
				return "Error: " + err.Error()
			}
			return report.Report{Sections: []report.Section{{Rows: []report.Row{report.Done("archived", slug+" → "+to)}}}}.String()
		case "drop":
			if slug == "" {
				return usage
			}
			if err := todo.Remove(root, slug); err != nil {
				return "Error: " + err.Error()
			}
			return report.Report{Sections: []report.Section{{Rows: []report.Row{report.Done("dropped", slug+" · the file is deleted")}}}}.String()
		}
		return usage
	}
}

// activeItem finds an item that is not archived.
func activeItem(s *todo.Store, slug string) (todo.Item, bool) {
	it, ok := s.Find(slug)
	if !ok || it.Archived {
		return todo.Item{}, false
	}
	return it, true
}

// todoTemplate is the body a one-line /todo add starts with: the sections
// a worked item carries, empty, so the file says what is still to be
// written rather than looking complete.
const todoTemplate = `**As a** …, **I want** … **so that** ….

## Acceptance criteria
- [ ]

## Tasks
- [ ]

## Tests
-

## Notes
`
