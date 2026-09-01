package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

type Definition struct {
	Tool    provider.Tool
	Execute func(args json.RawMessage) (string, error)
}

// The read-only tool names. They are constants because two other decisions
// are made by name: the reduction pipeline asks which results are already
// bounded (SelfBounding), and the mutation rail asks which are not.
const (
	ReadFileName      = "read_file"
	ListDirectoryName = "list_directory"
	SearchName        = "search"
	GlobName          = "glob"
)

func ReadOnly() []Definition {
	return []Definition{readFile, listDirectory, search, globFiles}
}

// SelfBounding reports whether a tool already bounds its own output.
//
// Every tool named here has a cap in limits.go chosen for the shape of what
// it returns, and a truncation notice that says how to continue past it. A
// second, shape-blind reduction on top of that is not a saving: it takes a
// result the tool sized deliberately and cuts a head and a tail out of it,
// dropping the middle of a file the model was told to read in one call. The
// reduction pipeline exists for output nothing else bounds — a command's, a
// server's, a page's — and that is the only place it earns its cost.
// See docs/capabilities/evidence.md#reduction-is-for-unbounded-output.
func SelfBounding(name string) bool {
	switch name {
	case ReadFileName, ListDirectoryName, SearchName, GlobName:
		return true
	}
	return false
}

// skipWalk reports whether a directory is one no tool walks into. glob,
// search and list_directory all skip the same three, so they say so in one
// place: a directory missing from one of the three lists is a tool that
// floods its own cap with objects nobody asked for.
func skipWalk(name string) bool {
	return name == ".git" || name == "node_modules" || name == "vendor"
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

// What a description tells a model to do.
//
// This one used to lead with "Large files are truncated with a notice; use
// start_line/end_line to page through the rest", and paging was what it got:
// sessions reading twenty and forty line windows out of files well under the
// cap, a round apiece. Nothing was wrong with the tool — MaxReadFileLines is
// two thousand — but the salient sentence of a description is the
// instruction, and that sentence described the exception. The common case
// leads now, and paging is what it actually is: how to continue through a
// file the tool has already told you it could not finish.
var readFile = Definition{
	Tool: provider.Tool{
		Name: ReadFileName,
		Description: "Read the contents of a file. Each line is returned as `<line number>\t<text>`; the number is a reading aid, not part of the file. " +
			"Read the whole file by default — it is one call, and reading it in small windows is not cheaper. " +
			"start_line/end_line are for continuing through a file too large to return at once, which the result says explicitly when it happens.",
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
	shown := strings.Count(content, "\n") + 1
	// Number after the caps, never before: MaxReadFileBytes is a budget for
	// the file, and spending part of it on the numbering would mean a reader
	// asking for a whole file got less of one than limits.go promises.
	content = numberLines(content, start)
	if truncated {
		lastLine := start + shown - 1
		content += fmt.Sprintf("\n… (truncated: showing lines %d-%d of %d; call read_file again with start_line=%d to continue)", start, lastLine, total, lastLine+1)
	}

	return content, nil
}

// numberLines prefixes each line with its 1-based number in the file and a
// tab, so a reader can cite file:line without counting and can point an edit
// at the right place. The separator is a tab because the prefix has to be
// unambiguously strippable from a line of source that may start with
// anything — including spaces.
func numberLines(content string, start int) string {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	b.Grow(len(content) + len(lines)*6)
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strconv.Itoa(start + i))
		b.WriteByte('\t')
		b.WriteString(line)
	}
	return b.String()
}

var listDirectory = Definition{
	Tool: provider.Tool{
		Name: ListDirectoryName,
		Description: "List files and directories at a given path. Returns one entry per line with type prefix (file: or dir:). " +
			"Reports .git, node_modules and vendor without descending into them.",
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
			// The directory is named but not entered. Descending into .git
			// spent most of a 500-entry budget on object shards — a listing
			// the reader then had to narrow by hand, one wasted round for a
			// question nobody asked.
			if depth > 1 && !skipWalk(e.Name()) {
				_ = walkDir(base, rel, depth-1, lines)
			}
		} else {
			*lines = append(*lines, "file: "+rel)
		}
	}
	return nil
}
