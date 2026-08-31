package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/spf13/cobra"
)

// openMemoryStore builds the durable-memory store for the current
// workspace; nil when persistence is unavailable.
func openMemoryStore(db *storage.DB) *memory.Store {
	if db == nil {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return memory.NewStore(db, memory.ProjectScope(cwd))
}

// memoryManager backs the /memory slash command: list (default), add, forget.
// Entries added here are user-stated, so they persist directly — the confirm
// flow exists for agent proposals, not for the user's own words.
func memoryManager(store *memory.Store) func(args []string) string {
	return func(args []string) string {
		const usage = "Usage: /memory [list] · /memory add [global] [preference|convention|correction|lesson] <text> · /memory forget <id>"
		if len(args) == 0 || (len(args) == 1 && args[0] == "list") {
			return memoryListing(store, "/memory add [global] [kind] <text>")
		}
		switch args[0] {
		case "add":
			scope, kind, text := parseMemoryAdd(store, args[1:])
			if strings.TrimSpace(text) == "" {
				return usage
			}
			e, err := store.Add(scope, kind, text, memory.ProvenanceUser)
			if err != nil {
				return "Error: " + err.Error()
			}
			return memoryAdded(e).String()
		case "forget":
			if len(args) != 2 {
				return usage
			}
			id, err := memory.ParseID(args[1])
			if err != nil {
				return "Error: " + err.Error()
			}
			if err := store.Forget(id); err != nil {
				return "Error: " + err.Error()
			}
			return report.Report{Sections: []report.Section{{Rows: []report.Row{
				report.Done("forgot", memoryID(id))}}}}.String()
		}
		return usage
	}
}

// parseMemoryAdd interprets /memory add arguments: an optional leading scope
// token (global/project), an optional kind token, then the entry text.
func parseMemoryAdd(store *memory.Store, args []string) (scope, kind, text string) {
	scope = store.Project()
	kind = memory.KindPreference
	if len(args) > 0 {
		switch args[0] {
		case memory.GlobalScope:
			scope = memory.GlobalScope
			args = args[1:]
		case "project":
			args = args[1:]
		}
	}
	if len(args) > 0 && memory.ValidKind(args[0]) {
		kind = args[0]
		args = args[1:]
	}
	return scope, kind, strings.Join(args, " ")
}

// memorySaver persists a user-confirmed agent proposal — the chat flow calls
// it only after explicit approval on the memory prompt — and formats the tool
// result the model receives.
func memorySaver(store *memory.Store) func(scope, kind, text string) (string, error) {
	return func(scope, kind, text string) (string, error) {
		e, err := store.Add(scope, kind, text, memory.ProvenanceAgent)
		if err != nil {
			return "", err
		}
		return memoryAdded(e).String() + "\n" + e.Text, nil
	}
}

// memoryListing renders the entries visible to this workspace. The way out of
// an empty listing is the form of the command the reader is already using —
// a shell user told to type a slash command has been told nothing.
func memoryListing(store *memory.Store, wayOut string) string {
	entries, err := store.List()
	if err != nil {
		return "Error: " + err.Error()
	}
	return memoryReport(store, entries, wayOut, time.Now()).String()
}

// memoryReport is the entries as a report: project scope and global scope as
// their own sections, the kind and who said it as fixed columns, and the text
// itself on the line under them — it is the thing, not a field of it.
func memoryReport(store *memory.Store, entries []memory.Entry, wayOut string, now time.Time) report.Report {
	r := report.Report{
		Title:   "shhh memory",
		Subject: joinDetail(countOf(len(entries), "memory", "memories"), store.Project()),
	}
	if len(entries) == 0 {
		return emptyInto(r, "nothing remembered yet", wayOut)
	}
	project := report.Section{Header: "PROJECT"}
	global := report.Section{Header: "GLOBAL"}
	for _, e := range entries {
		row := report.Row{
			State:   report.Pass,
			Name:    memoryID(e.ID),
			Subject: e.Kind,
			Detail:  joinDetail(e.Provenance, historyAgo(e.UpdatedAt, now)),
			Body:    []string{e.Text},
		}
		if e.Scope == memory.GlobalScope {
			global.Rows = append(global.Rows, row)
			continue
		}
		project.Rows = append(project.Rows, row)
	}
	for _, section := range []report.Section{project, global} {
		if len(section.Rows) > 0 {
			r.Sections = append(r.Sections, section)
		}
	}
	return r
}

// memoryAdded is the confirmation an entry was saved, in the same shape every
// other write in the CLI confirms itself.
func memoryAdded(e memory.Entry) report.Report {
	return report.Report{Sections: []report.Section{{Rows: []report.Row{
		report.Done("remembered", memoryID(e.ID)+" · "+memory.ScopeLabel(e.Scope)+" "+e.Kind)}}}}
}

// memoryID is how an entry is named everywhere: `m12`, the id the forget
// command takes.
func memoryID(id int64) string { return fmt.Sprintf("m%d", id) }

// newMemoryCmd is the `shhh memory` CLI, the out-of-session equivalent of
// /memory: list, add, forget.
func newMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Manage durable agent memories",
		Long:  "List, add, and remove the durable memories agent sessions recall: preferences, project conventions, corrections, and lessons, scoped globally or to the current project.",
	}

	var listJSON bool
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List memories visible to this project (project + global)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withMemoryStore(func(store *memory.Store) error {
				entries, err := store.List()
				if err != nil {
					return err
				}
				if listJSON {
					return writeJSON(cmd, memoryJSON(store, entries))
				}
				return report.Fprint(cmd.OutOrStdout(),
					memoryReport(store, entries, memoryWayOut, time.Now()))
			})
		},
	}
	listCmd.Flags().BoolVar(&listJSON, "json", false, "emit the memories as JSON")
	cmd.AddCommand(listCmd)

	var addGlobal bool
	var addKind string
	addCmd := &cobra.Command{
		Use:   "add <text>",
		Short: "Add a memory (user-stated, persisted directly)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withMemoryStore(func(store *memory.Store) error {
				scope := store.Project()
				if addGlobal {
					scope = memory.GlobalScope
				}
				e, err := store.Add(scope, addKind, strings.Join(args, " "), memory.ProvenanceUser)
				if err != nil {
					return err
				}
				return report.Fprint(cmd.OutOrStdout(), memoryAdded(e))
			})
		},
	}
	addCmd.Flags().BoolVar(&addGlobal, "global", false, "save with global scope instead of this project")
	addCmd.Flags().StringVar(&addKind, "kind", memory.KindPreference, "entry kind: preference, convention, correction, or lesson")
	cmd.AddCommand(addCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "forget <id>",
		Short: "Delete a memory by id (e.g. m12)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withMemoryStore(func(store *memory.Store) error {
				id, err := memory.ParseID(args[0])
				if err != nil {
					return err
				}
				if err := store.Forget(id); err != nil {
					return err
				}
				return report.Fprintln(cmd.OutOrStdout(), report.Done("forgot", memoryID(id)))
			})
		},
	})

	return cmd
}

// memoryWayOut is what an empty `shhh memory` points at: the form of the
// command a reader at a shell prompt can actually type.
const memoryWayOut = "shhh memory add \"<text>\""

// memoryJSON is the listing as data, in the store's own field names.
func memoryJSON(store *memory.Store, entries []memory.Entry) memoryDoc {
	doc := memoryDoc{Project: store.Project(), Memories: []memoryEntryDoc{}}
	for _, e := range entries {
		doc.Memories = append(doc.Memories, memoryEntryDoc{
			ID: memoryID(e.ID), Scope: memory.ScopeLabel(e.Scope), Kind: e.Kind,
			Provenance: e.Provenance, Text: e.Text, UpdatedAt: e.UpdatedAt,
		})
	}
	return doc
}

type memoryDoc struct {
	Project  string           `json:"project"`
	Memories []memoryEntryDoc `json:"memories"`
}

type memoryEntryDoc struct {
	ID         string    `json:"id"`
	Scope      string    `json:"scope"`
	Kind       string    `json:"kind"`
	Provenance string    `json:"provenance"`
	Text       string    `json:"text"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// withMemoryStore opens storage for one CLI invocation and runs fn against
// the workspace's memory store.
func withMemoryStore(fn func(*memory.Store) error) error {
	db, err := openStore()
	if err != nil {
		return fmt.Errorf("storage unavailable: %w", err)
	}
	defer db.Close()
	return fn(openMemoryStore(db))
}
