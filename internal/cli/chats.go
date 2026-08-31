package cli

// `shhh chats`: the saved conversations from outside a session
// (docs/capabilities/sessions-and-memory.md#housekeeping). Bare, it is the
// browser — pick a chat and the conversation opens on it, which is what
// `shhh chat --resume` does. With a verb it is the same store for a script:
// list, show, delete and rename, with --json where a program is reading.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/spf13/cobra"
)

// chatRow is one saved chat as `shhh chats list --json` emits it.
type chatRow struct {
	Name      string    `json:"name"`
	Title     string    `json:"title,omitempty"`
	Turns     int       `json:"turns"`
	UpdatedAt time.Time `json:"updated_at"`
}

func newChatsCmd() *cobra.Command {
	var flags resolve.Opts
	var addDirs []string
	var secretFlags []string

	cmd := &cobra.Command{
		Use:   "chats",
		Short: "Browse, resume and tidy saved chats",
		Long:  "Open the saved-chat browser: pick a conversation to resume it, [x] deletes one after asking, [r] renames it. The subcommands do the same from a script.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The browser comes first and the session after, so browsing
			// and tidying cost nothing beyond the store — no provider is
			// resolved until a chat is actually picked.
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			name, err := pickSavedChat(db)
			db.Close()
			if err != nil || name == "" {
				return err
			}
			session := conversationSession(cmd, &flags, false, false, addDirs, secretFlags)
			session.resumeName = name
			return runChatSession(cmd, args, session)
		},
	}
	cmd.Flags().StringVar(&flags.FlagProvider, "provider", "", "provider to send the resumed session to")
	cmd.Flags().StringVar(&flags.FlagModel, "model", "", "model name to use")
	cmd.Flags().StringVar(&flags.FlagAPIKey, "api-key", "", "key for the provider, overriding the env var")
	addDirFlag(cmd, &addDirs)
	addSecretFlag(cmd, &secretFlags)

	var listJSON bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List saved chats: name, title, turns, last written",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()
			entries, err := db.ListChats()
			if err != nil {
				return err
			}
			if listJSON {
				return writeJSON(cmd, chatRows(entries))
			}
			return report.Fprint(cmd.OutOrStdout(), chatsReport(entries, time.Now()))
		},
	}
	list.Flags().BoolVar(&listJSON, "json", false, "emit the list as a JSON array")

	var showJSON bool
	show := &cobra.Command{
		Use:   "show <name>",
		Short: "Print a saved chat's transcript",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()
			msgs, err := db.LoadChat(args[0])
			if err != nil {
				return chatError(err)
			}
			if showJSON {
				return writeJSON(cmd, jsonMessages(msgs))
			}
			fmt.Fprint(cmd.OutOrStdout(), chatTranscript(msgs))
			return nil
		},
	}
	show.Flags().BoolVar(&showJSON, "json", false, "emit the transcript as a JSON array")

	var yes bool
	del := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a saved chat and its branches, after asking",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()
			name := args[0]
			if ok, err := db.HasChat(name); err != nil {
				return err
			} else if !ok {
				return chatError(storage.ChatNotFoundError{Name: name})
			}
			branches, _ := db.CountChatBranches(name)
			if !yes {
				ok, err := confirmDelete(cmd, name, branches)
				if err != nil {
					return err
				}
				if !ok {
					return report.Fprintln(cmd.OutOrStdout(), report.Row{
						State: report.Skip, Subject: "kept " + name, Detail: "nothing was deleted"})
				}
			}
			if err := db.DeleteChat(name); err != nil {
				return chatError(err)
			}
			with := ""
			if branches > 0 {
				with = " and its " + branchCount(branches)
			}
			return report.Fprintln(cmd.OutOrStdout(), report.Done("deleted chat", name+with))
		},
	}
	del.Flags().BoolVarP(&yes, "yes", "y", false, "delete without asking")

	rename := &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "Rename a saved chat, keeping its branches",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()
			next := strings.TrimSpace(args[1])
			if next == "" {
				return fmt.Errorf("the new name is empty — give the chat a name to rename it to")
			}
			if err := db.RenameChat(args[0], next); err != nil {
				return chatError(err)
			}
			return report.Fprintln(cmd.OutOrStdout(), report.Done("renamed chat", args[0]+" → "+next))
		},
	}

	cmd.AddCommand(list, show, del, rename)
	return cmd
}

// chatError is a store's answer as a failure with one way out
// (docs/interface/surfaces.md#outside-the-tui): a name that is not there
// points at the list, a name that is taken points at another name.
func chatError(err error) error {
	switch e := err.(type) {
	case storage.ChatNotFoundError:
		return fmt.Errorf("chat %q not found — `shhh chats list` shows what is saved", e.Name)
	case storage.ChatExistsError:
		return fmt.Errorf("a chat named %q already exists — choose another name", e.Name)
	}
	return err
}

// confirmDelete asks before a delete that was not given --yes. It reads the
// answer from the command's input. A script's stdin — a file that is not a
// terminal — is refused outright rather than read: a question nobody will
// see gets no answer, and the answer that loses nothing is to stop and say
// what flag was missing. Anything short of a yes is a No.
func confirmDelete(cmd *cobra.Command, name string, branches int) (bool, error) {
	in := cmd.InOrStdin()
	if f, ok := in.(*os.File); ok && !isTerminal(f) {
		return false, fmt.Errorf("refusing to delete %q without asking — pass --yes from a script", name)
	}
	with := ""
	if branches > 0 {
		with = " and its " + branchCount(branches)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Delete %q%s? Files on disk are untouched. [y/N] ", name, with)
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

// chatRows is the listing as data.
func chatRows(entries []storage.ChatListEntry) []chatRow {
	rows := make([]chatRow, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, chatRow{Name: e.Name, Title: e.Title, Turns: e.Turns, UpdatedAt: e.UpdatedAt})
	}
	return rows
}

// chatsReport is the listing as text: one row per chat, the title where a
// session has been given one and the name alone where it has not.
func chatsReport(entries []storage.ChatListEntry, now time.Time) report.Report {
	r := report.Report{Title: "shhh chats", Subject: countOf(len(entries), "chat", "chats")}
	if len(entries) == 0 {
		return emptyInto(r, "nothing saved yet", "shhh chat")
	}
	rows := make([]report.Row, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, report.Row{
			State:   report.Pass,
			Name:    e.Name,
			Subject: e.Title,
			Detail:  joinDetail(countOf(e.Turns, "turn", "turns"), historyAgo(e.UpdatedAt, now)),
		})
	}
	r.Sections = []report.Section{{Rows: rows}}
	return r
}

// chatTranscript is the transcript as text: each message under its role,
// the system prompt left out because it is the session's and not the
// conversation's, and a tool call named on the line it was made.
func chatTranscript(msgs []provider.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		if m.Role == provider.RoleSystem {
			continue
		}
		fmt.Fprintf(&b, "%s:", m.Role)
		if strings.TrimSpace(m.Content) != "" {
			b.WriteString(" " + strings.TrimRight(m.Content, "\n"))
		}
		b.WriteString("\n")
		for _, tc := range m.ToolCalls {
			fmt.Fprintf(&b, "  → %s %s\n", tc.Name, tc.Arguments)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// writeJSON emits v indented, with the trailing newline a shell expects.
func writeJSON(cmd *cobra.Command, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = cmd.OutOrStdout().Write(append(data, '\n'))
	return err
}
