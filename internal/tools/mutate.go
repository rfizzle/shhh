package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

// WriteFileName and EditFileName are the file-modification tools the chat UI
// intercepts for user approval, mirroring ExecCommandName.
const (
	WriteFileName = "write_file"
	EditFileName  = "edit_file"
)

// Mutating returns the file-modification tool definitions. They are
// deliberately kept out of ReadOnly(), so Execute — the auto-run path — can
// never dispatch them; approved calls run through ExecuteMutating instead.
func Mutating() []Definition {
	return []Definition{writeFile, editFile}
}

// DefinitionsFull returns the complete agent toolset: read-only tools, the
// user-approved execute_command, and the approval-gated file-modification
// tools. This is what `shhh code` registers; `shhh chat` stays on
// DefinitionsWithExec.
func DefinitionsFull() []provider.Tool {
	defs := DefinitionsWithExec()
	for _, d := range Mutating() {
		defs = append(defs, d.Tool)
	}
	return defs
}

// IsMutating reports whether name is a file-modification tool that must go
// through the user approval queue before it runs.
func IsMutating(name string) bool {
	for _, d := range Mutating() {
		if d.Tool.Name == name {
			return true
		}
	}
	return false
}

// ExecuteMutating dispatches a user-approved file-modification tool call. It
// refuses every other tool name so read-only tools cannot be routed here by
// mistake.
func ExecuteMutating(name string, args json.RawMessage) (string, error) {
	for _, d := range Mutating() {
		if d.Tool.Name == name {
			return d.Execute(args)
		}
	}
	return "", fmt.Errorf("unknown mutating tool: %s", name)
}

// Mutation describes what a mutating tool call would change, for the approval
// prompt's diff preview: the file's current content against the content it
// would have after the call.
type Mutation struct {
	Action  string // short verb for the title, e.g. "write", "edit"
	Path    string
	OldText string
	NewText string
}

// PreviewMutation computes the before/after content for a mutating tool call
// without touching the file. Validation matches execution, so a call that
// previews cleanly only fails later if the file changes underneath it.
func PreviewMutation(name string, raw json.RawMessage) (Mutation, error) {
	switch name {
	case WriteFileName:
		args, err := parseWriteFileArgs(raw)
		if err != nil {
			return Mutation{}, err
		}
		old, existed, err := readIfExists(args.Path)
		if err != nil {
			return Mutation{}, err
		}
		if err := checkSeen(args.Path, []byte(old), existed, true); err != nil {
			return Mutation{}, err
		}
		return Mutation{Action: "write", Path: args.Path, OldText: old, NewText: args.Content}, nil
	case EditFileName:
		args, err := parseEditFileArgs(raw)
		if err != nil {
			return Mutation{}, err
		}
		content, err := os.ReadFile(args.Path)
		if err != nil {
			return Mutation{}, fmt.Errorf("cannot read file: %w", err)
		}
		if err := checkSeen(args.Path, content, true, false); err != nil {
			return Mutation{}, err
		}
		updated, _, err := applyEdit(string(content), args)
		if err != nil {
			return Mutation{}, err
		}
		return Mutation{Action: "edit", Path: args.Path, OldText: string(content), NewText: updated}, nil
	}
	return Mutation{}, fmt.Errorf("unknown mutating tool: %s", name)
}

var writeFile = Definition{
	Tool: provider.Tool{
		Name: WriteFileName,
		Description: "Create or overwrite a file with the given content. content is written verbatim — never include read_file's line-number prefixes. " +
			"Overwriting an existing file requires having read it in full first, and fails if it has changed since: prefer edit_file for changing part of a file you have already read. " +
			"Missing parent directories are created automatically. The user reviews a diff and must approve the change before it is applied; a declined call returns an error result.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "File path to create or overwrite"},
				"content": {"type": "string", "description": "Full content the file will have"}
			},
			"required": ["path", "content"]
		}`),
	},
	Execute: executeWriteFile,
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func parseWriteFileArgs(raw json.RawMessage) (writeFileArgs, error) {
	var args writeFileArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Path == "" {
		return args, fmt.Errorf("path is required")
	}
	return args, nil
}

func executeWriteFile(raw json.RawMessage) (string, error) {
	args, err := parseWriteFileArgs(raw)
	if err != nil {
		return "", err
	}
	old, existed, err := readIfExists(args.Path)
	if err != nil {
		return "", err
	}
	// A full overwrite carries no evidence about what it is overwriting, so
	// it has to have been read, in full, and not changed since (seen.go).
	if err := checkSeen(args.Path, []byte(old), existed, true); err != nil {
		return "", err
	}
	if dir := filepath.Dir(args.Path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("cannot create parent directories: %w", err)
		}
	}
	if err := os.WriteFile(args.Path, []byte(args.Content), 0o644); err != nil {
		forget(args.Path)
		return "", fmt.Errorf("cannot write file: %w", err)
	}
	noteShown(args.Path, []byte(args.Content), true)
	if existed {
		return fmt.Sprintf("Overwrote %s: wrote %d bytes (%d lines), was %d bytes", args.Path, len(args.Content), countLines(args.Content), len(old)), nil
	}
	return fmt.Sprintf("Created %s: wrote %d bytes (%d lines)", args.Path, len(args.Content), countLines(args.Content)), nil
}

var editFile = Definition{
	Tool: provider.Tool{
		Name: EditFileName,
		Description: "Replace an exact text snippet in an existing file. old_text must match the file content exactly (including whitespace) and match exactly once, unless replace_all is set. " +
			"Strip read_file's `<line number>\t` prefix before quoting a line here — the numbers are a reading aid and are not in the file. " +
			"A file that has changed since you last read it is refused: read it again and rebase the edit on what it says now. " +
			"The user reviews a diff and must approve the change before it is applied; a declined call returns an error result.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "File path to edit"},
				"old_text": {"type": "string", "description": "Exact existing text to replace"},
				"new_text": {"type": "string", "description": "Replacement text"},
				"replace_all": {"type": "boolean", "description": "Replace every occurrence instead of requiring a unique match"}
			},
			"required": ["path", "old_text", "new_text"]
		}`),
	},
	Execute: executeEditFile,
}

type editFileArgs struct {
	Path       string `json:"path"`
	OldText    string `json:"old_text"`
	NewText    string `json:"new_text"`
	ReplaceAll bool   `json:"replace_all"`
}

func parseEditFileArgs(raw json.RawMessage) (editFileArgs, error) {
	var args editFileArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Path == "" {
		return args, fmt.Errorf("path is required")
	}
	if args.OldText == "" {
		return args, fmt.Errorf("old_text is required")
	}
	if args.OldText == args.NewText {
		return args, fmt.Errorf("old_text and new_text are identical")
	}
	return args, nil
}

func executeEditFile(raw json.RawMessage) (string, error) {
	args, err := parseEditFileArgs(raw)
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(args.Path)
	if err != nil {
		return "", fmt.Errorf("cannot read file: %w", err)
	}
	// An edit needs no prior read — old_text is its own evidence — but a file
	// that moved since it was read is one this edit was not written against.
	if err := checkSeen(args.Path, content, true, false); err != nil {
		return "", err
	}
	updated, count, err := applyEdit(string(content), args)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(args.Path, []byte(updated), 0o644); err != nil {
		// The file is now of unknown content, so nothing may be claimed about
		// it until something reads it again.
		forget(args.Path)
		return "", fmt.Errorf("cannot write file: %w", err)
	}
	// The model knows exactly what it just wrote, so the next edit is not
	// asked to read it again.
	noteShown(args.Path, []byte(updated), true)
	return fmt.Sprintf("Edited %s: %d replacement(s), file now %d bytes (%d lines)", args.Path, count, len(updated), countLines(updated)), nil
}

// applyEdit performs the replacement on content, enforcing the unique-match
// rule; it is shared by execution and the approval preview.
func applyEdit(content string, args editFileArgs) (string, int, error) {
	count := strings.Count(content, args.OldText)
	switch {
	case count == 0:
		if looksLineNumbered(args.OldText) {
			return "", 0, fmt.Errorf("old_text not found in %s; it looks like it still carries read_file's line-number prefixes — strip the leading digits and tab from each line and try again", args.Path)
		}
		return "", 0, fmt.Errorf("old_text not found in %s; it must match the file content exactly, including whitespace", args.Path)
	case count > 1 && !args.ReplaceAll:
		return "", 0, fmt.Errorf("old_text matches %d locations in %s; provide a longer unique snippet or set replace_all", count, args.Path)
	}
	if args.ReplaceAll {
		return strings.ReplaceAll(content, args.OldText, args.NewText), count, nil
	}
	return strings.Replace(content, args.OldText, args.NewText, 1), 1, nil
}

// readIfExists returns the file's content, or existed=false for a missing
// file; any other read failure is an error.
func readIfExists(path string) (content string, existed bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("cannot read file: %w", err)
	}
	return string(data), true, nil
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// looksLineNumbered reports whether every line of s opens with digits and a
// tab — the shape read_file returns. It is only ever asked after a match has
// already failed, so a file that genuinely looks like this loses nothing but
// a more specific error message.
func looksLineNumbered(s string) bool {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	if len(lines) == 0 {
		return false
	}
	for _, line := range lines {
		tab := strings.IndexByte(line, '\t')
		if tab <= 0 {
			return false
		}
		for _, r := range line[:tab] {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}
