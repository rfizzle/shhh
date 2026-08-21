package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

type Definition struct {
	Tool    provider.Tool
	Execute func(args json.RawMessage) (string, error)
}

func ReadOnly() []Definition {
	return []Definition{readFile, listDirectory, search, globFiles}
}

func Definitions() []provider.Tool {
	defs := ReadOnly()
	tools := make([]provider.Tool, len(defs))
	for i, d := range defs {
		tools[i] = d.Tool
	}
	return tools
}

// ExecCommandName is the tool the chat UI intercepts for user approval; it is
// deliberately not executable through Execute.
const ExecCommandName = "execute_command"

func ExecCommandTool() provider.Tool {
	return provider.Tool{
		Name:        ExecCommandName,
		Description: "Execute a shell command on the user's machine. The user is shown the command and must approve it before it runs; a declined call returns an error result. Returns combined stdout/stderr and the exit code.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"command": {"type": "string", "description": "The shell command to run"}
			},
			"required": ["command"]
		}`),
	}
}

// DefinitionsWithExec returns the read-only tool definitions plus the
// user-approved execute_command tool.
func DefinitionsWithExec() []provider.Tool {
	return append(Definitions(), ExecCommandTool())
}

// Execute dispatches an auto-run tool call. Only read-only tools are
// reachable here: mutating tools (Mutating) and execute_command must go
// through user approval and are deliberately unknown to this path.
func Execute(name string, args json.RawMessage) (string, error) {
	for _, d := range ReadOnly() {
		if d.Tool.Name == name {
			return d.Execute(args)
		}
	}
	return "", fmt.Errorf("unknown tool: %s", name)
}

var readFile = Definition{
	Tool: provider.Tool{
		Name:        "read_file",
		Description: "Read the contents of a file. Returns the file content as text. Large files are truncated with a notice; use start_line/end_line to page through the rest.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Absolute or relative file path to read"},
				"start_line": {"type": "integer", "description": "Optional 1-based start line"},
				"end_line": {"type": "integer", "description": "Optional 1-based end line (inclusive)"}
			},
			"required": ["path"]
		}`),
	},
	Execute: executeReadFile,
}

type readFileArgs struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

func executeReadFile(raw json.RawMessage) (string, error) {
	var args readFileArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	data, err := os.ReadFile(args.Path)
	if err != nil {
		return "", fmt.Errorf("cannot read file: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	total := len(lines)
	start := 1
	end := total
	if args.StartLine > 0 || args.EndLine > 0 {
		if args.StartLine > 1 {
			start = args.StartLine
		}
		if args.EndLine > 0 && args.EndLine < end {
			end = args.EndLine
		}
		if start > total {
			return "", fmt.Errorf("start_line %d exceeds file length (%d lines)", start, total)
		}
		if end < start {
			return "", fmt.Errorf("end_line %d is before start_line %d", end, start)
		}
	}
	selected := lines[start-1 : end]

	truncated := false
	if len(selected) > MaxReadFileLines {
		selected = selected[:MaxReadFileLines]
		truncated = true
	}
	content := strings.Join(selected, "\n")
	if cut, wasCut := TruncateOutput(content, MaxReadFileBytes); wasCut {
		// Drop the trailing partial line so the paging notice counts whole
		// lines — unless the cut landed inside the only line we have.
		if i := strings.LastIndexByte(cut, '\n'); i > 0 {
			cut = cut[:i]
		}
		content = cut
		truncated = true
	}
	if truncated {
		shown := strings.Count(content, "\n") + 1
		lastLine := start + shown - 1
		content += fmt.Sprintf("\n… (truncated: showing lines %d-%d of %d; call read_file again with start_line=%d to continue)", start, lastLine, total, lastLine+1)
	}

	return content, nil
}

var listDirectory = Definition{
	Tool: provider.Tool{
		Name:        "list_directory",
		Description: "List files and directories at a given path. Returns one entry per line with type prefix (file: or dir:).",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Directory path to list"},
				"depth": {"type": "integer", "description": "Optional max recursion depth (default 1, max 3)"}
			},
			"required": ["path"]
		}`),
	},
	Execute: executeListDirectory,
}

type listDirectoryArgs struct {
	Path  string `json:"path"`
	Depth int    `json:"depth"`
}

func executeListDirectory(raw json.RawMessage) (string, error) {
	var args listDirectoryArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if args.Depth < 1 {
		args.Depth = 1
	}
	if args.Depth > 3 {
		args.Depth = 3
	}

	var lines []string
	err := walkDir(args.Path, "", args.Depth, &lines)
	if err != nil {
		return "", err
	}
	if len(lines) > MaxListEntries {
		lines = lines[:MaxListEntries]
		return strings.Join(lines, "\n") + fmt.Sprintf("\n… (truncated at %d entries; list a subdirectory or use a smaller depth to see more)", MaxListEntries), nil
	}
	return strings.Join(lines, "\n"), nil
}

func walkDir(base, prefix string, depth int, lines *[]string) error {
	if depth < 1 {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(base, prefix))
	if err != nil {
		return fmt.Errorf("cannot list directory: %w", err)
	}
	for _, e := range entries {
		rel := filepath.Join(prefix, e.Name())
		if e.IsDir() {
			*lines = append(*lines, "dir: "+rel)
			if depth > 1 {
				_ = walkDir(base, rel, depth-1, lines)
			}
		} else {
			*lines = append(*lines, "file: "+rel)
		}
	}
	return nil
}
