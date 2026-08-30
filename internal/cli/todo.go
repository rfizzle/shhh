package cli

// Backlog wiring: `shhh todo` prints the project's backlog the way a session
// here would read it — ready items first, then what each waiting item is
// waiting on, then the files that could not be read.

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

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
func todoListing(s *todo.Store) string {
	if s.Len() == 0 && len(s.Diagnostics) == 0 {
		return fmt.Sprintf("No backlog. Items are Markdown files under %s, one per item, with a --- header naming at least a title.", todo.Dir(s.Root))
	}
	var b strings.Builder
	if s.Len() > 0 {
		tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		for _, it := range s.Items {
			size := string(it.Size)
			if size == "" {
				size = "-"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", it.Slug, it.Priority, size, todoState(s, it), it.Kind, clipRunes(it.Title, 72))
		}
		tw.Flush()
		ready := len(s.Ready())
		fmt.Fprintf(&b, "\n%d item(s), %d ready", s.Len(), ready)
		if n := s.Count(todo.StatusBlocked); n > 0 {
			fmt.Fprintf(&b, ", %d blocked", n)
		}
		if n := len(s.Done); n > 0 {
			fmt.Fprintf(&b, ", %d archived", n)
		}
		b.WriteString(".")
		for _, it := range s.Items {
			for _, w := range it.Warnings {
				fmt.Fprintf(&b, "\nwarning: %s: %s", it.Path, w)
			}
		}
	}
	for _, d := range s.Diagnostics {
		fmt.Fprintf(&b, "\n%s", d)
	}
	return strings.TrimRight(b.String(), "\n")
}

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
	var b strings.Builder
	fmt.Fprintf(&b, "slug:       %s\ntitle:      %s\nstatus:     %s\n", it.Slug, it.Title, todoState(s, it))
	if it.Kind != "" {
		fmt.Fprintf(&b, "kind:       %s\n", it.Kind)
	}
	fmt.Fprintf(&b, "priority:   %s\n", it.Priority)
	if it.Size != "" {
		fmt.Fprintf(&b, "size:       %s\n", it.Size)
	}
	if len(it.DependsOn) > 0 {
		fmt.Fprintf(&b, "depends on: %s\n", strings.Join(it.DependsOn, ", "))
	}
	if it.Created != "" {
		fmt.Fprintf(&b, "created:    %s\n", it.Created)
	}
	if it.Session != "" {
		fmt.Fprintf(&b, "session:    %s\n", it.Session)
	}
	for _, f := range it.Extra {
		fmt.Fprintf(&b, "%s: %s\n", f.Key, f.Value)
	}
	fmt.Fprintf(&b, "file:       %s\n", it.Path)
	for _, w := range it.Warnings {
		fmt.Fprintf(&b, "warning:    %s\n", w)
	}
	if body := strings.TrimSpace(it.Body); body != "" {
		b.WriteString("\n" + body + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func newTodoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "todo",
		Short: "List the project's backlog",
		Long:  "List the backlog items under the checkout's .shhh/todo directory in the order a session would work them, with what each is waiting on and why any file failed to load.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), todoListing(loadTodos()))
			return nil
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
				return fmt.Sprintf("No backlog item %q; /todo lists them.", slug)
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
			return fmt.Sprintf("Added %s (%s, medium). Fill in the criteria: /todo edit %s — or open %s.", it.Slug, it.Kind, it.Slug, path)
		case "block":
			if slug == "" {
				return usage
			}
			it, ok := activeItem(s, slug)
			if !ok {
				return fmt.Sprintf("No active backlog item %q; /todo lists them.", slug)
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
			return fmt.Sprintf("Blocked %s.", slug)
		case "open":
			if slug == "" {
				return usage
			}
			it, ok := activeItem(s, slug)
			if !ok {
				return fmt.Sprintf("No active backlog item %q; /todo lists them.", slug)
			}
			if err := todo.SetStatus(it.Path, todo.StatusOpen); err != nil {
				return "Error: " + err.Error()
			}
			return fmt.Sprintf("Reopened %s.", slug)
		case "done":
			if slug == "" {
				return usage
			}
			to, err := todo.Archive(root, slug, "")
			if err != nil {
				return "Error: " + err.Error()
			}
			return fmt.Sprintf("Archived %s to %s.", slug, to)
		case "drop":
			if slug == "" {
				return usage
			}
			if err := todo.Remove(root, slug); err != nil {
				return "Error: " + err.Error()
			}
			return fmt.Sprintf("Dropped %s; the file is deleted.", slug)
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
