package cli

// Backlog wiring: `shhh todo` prints the project's backlog the way a session
// here would read it — ready items first, then what each waiting item is
// waiting on, then the files that could not be read.

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

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
