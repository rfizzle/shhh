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
	"text/tabwriter"
	"time"

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

// chatMessage is one message as `shhh chats show --json` emits it: the
// role and the words, and a tool call's name and arguments where there was
// one. Attachments are left out — a transcript for a script is text.
type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// chatToolCall is one call the assistant made, name and arguments apart so
// a script does not have to split a string.
type chatToolCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
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
			fmt.Fprint(cmd.OutOrStdout(), chatListing(entries))
			return nil
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
				return writeJSON(cmd, chatMessages(msgs))
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
					fmt.Fprintln(cmd.OutOrStdout(), "Kept.")
					return nil
				}
			}
			if err := db.DeleteChat(name); err != nil {
				return chatError(err)
			}
			with := ""
			if branches > 0 {
				with = " and its " + branchCount(branches)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted %q%s.\n", name, with)
			return nil
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
			fmt.Fprintf(cmd.OutOrStdout(), "Renamed %q to %q.\n", args[0], next)
			return nil
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

// chatListing is the listing as text: one row per chat, columns aligned,
// the title where there is one and a dash where there is not — a column
// that is sometimes empty is a column the eye cannot find.
func chatListing(entries []storage.ChatListEntry) string {
	if len(entries) == 0 {
		return "No saved chats. A session saves itself as it goes; `shhh chat` starts one.\n"
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTITLE\tTURNS\tUPDATED")
	for _, e := range entries {
		title := e.Title
		if title == "" {
			title = "—"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", e.Name, title, e.Turns, e.UpdatedAt.Local().Format("2006-01-02 15:04"))
	}
	tw.Flush()
	return b.String()
}

// chatMessages is the transcript as data.
func chatMessages(msgs []provider.Message) []chatMessage {
	out := make([]chatMessage, 0, len(msgs))
	for _, m := range msgs {
		row := chatMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			row.ToolCalls = append(row.ToolCalls, chatToolCall{Name: tc.Name, Arguments: tc.Arguments})
		}
		out = append(out, row)
	}
	return out
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
