package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/spf13/cobra"
)

// openMemoryStore builds the durable-memory store (S-070) for the current
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
			return memoryListing(store)
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
			return fmt.Sprintf("Saved memory [m%d] (%s %s): %s", e.ID, memory.ScopeLabel(e.Scope), e.Kind, e.Text)
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
			return fmt.Sprintf("Forgot memory [m%d].", id)
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
		return fmt.Sprintf("Saved memory [m%d] (%s %s): %s", e.ID, memory.ScopeLabel(e.Scope), e.Kind, e.Text), nil
	}
}

// memoryListing renders the entries visible to this workspace.
func memoryListing(store *memory.Store) string {
	entries, err := store.List()
	if err != nil {
		return "Error: " + err.Error()
	}
	if len(entries) == 0 {
		return "No memories yet. Add one with /memory add [global] [kind] <text>, or let the agent propose one."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Memories (project: %s):\n", store.Project())
	for _, e := range entries {
		fmt.Fprintf(&sb, "  [m%d] (%s %s, %s, %s) %s\n",
			e.ID, memory.ScopeLabel(e.Scope), e.Kind, e.Provenance, e.UpdatedAt.Local().Format("Jan 2"), e.Text)
	}
	sb.WriteString("Remove one with /memory forget <id>.")
	return sb.String()
}

// newMemoryCmd is the `shhh memory` CLI, the out-of-session equivalent of
// /memory: list, add, forget.
func newMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Manage durable agent memories",
		Long:  "List, add, and remove the durable memories agent sessions recall: preferences, project conventions, corrections, and lessons, scoped globally or to the current project.",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List memories visible to this project (project + global)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withMemoryStore(func(store *memory.Store) error {
				fmt.Fprintln(cmd.OutOrStdout(), memoryListing(store))
				return nil
			})
		},
	})

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
				fmt.Fprintf(cmd.OutOrStdout(), "Saved memory [m%d] (%s %s): %s\n", e.ID, memory.ScopeLabel(e.Scope), e.Kind, e.Text)
				return nil
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
				fmt.Fprintf(cmd.OutOrStdout(), "Forgot memory [m%d].\n", id)
				return nil
			})
		},
	})

	return cmd
}

// withMemoryStore opens storage for one CLI invocation and runs fn against
// the workspace's memory store.
func withMemoryStore(fn func(*memory.Store) error) error {
	db, err := storage.Open()
	if err != nil {
		return fmt.Errorf("storage unavailable: %w", err)
	}
	defer db.Close()
	return fn(openMemoryStore(db))
}
