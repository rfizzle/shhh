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
	return []Definition{readFile, listDirectory, search}
}

func Definitions() []provider.Tool {
	defs := ReadOnly()
	tools := make([]provider.Tool, len(defs))
	for i, d := range defs {
		tools[i] = d.Tool
	}
	return tools
}

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
		Description: "Read the contents of a file. Returns the file content as text.",
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

	content := string(data)
	if args.StartLine > 0 || args.EndLine > 0 {
		lines := strings.Split(content, "\n")
		start := args.StartLine
		if start < 1 {
			start = 1
		}
		end := args.EndLine
		if end < 1 || end > len(lines) {
			end = len(lines)
		}
		if start > len(lines) {
			return "", fmt.Errorf("start_line %d exceeds file length (%d lines)", start, len(lines))
		}
		content = strings.Join(lines[start-1:end], "\n")
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

var search = Definition{
	Tool: provider.Tool{
		Name:        "search",
		Description: "Search for a text pattern in files. Returns matching lines with file path and line number.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"pattern": {"type": "string", "description": "Text pattern to search for (case-insensitive substring match)"},
				"path": {"type": "string", "description": "Optional directory or file path to search in (defaults to current directory)"}
			},
			"required": ["pattern"]
		}`),
	},
	Execute: executeSearch,
}

type searchArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

const maxSearchResults = 50

func executeSearch(raw json.RawMessage) (string, error) {
	var args searchArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	if args.Path == "" {
		args.Path = "."
	}

	pattern := strings.ToLower(args.Pattern)
	var results []string

	info, err := os.Stat(args.Path)
	if err != nil {
		return "", fmt.Errorf("cannot access path: %w", err)
	}

	if !info.IsDir() {
		results, err = searchFile(args.Path, pattern, results)
		if err != nil {
			return "", err
		}
	} else {
		err = filepath.WalkDir(args.Path, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if name == ".git" || name == "node_modules" || name == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if len(results) >= maxSearchResults {
				return filepath.SkipAll
			}
			if isBinary(path) {
				return nil
			}
			results, _ = searchFile(path, pattern, results)
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("search error: %w", err)
		}
	}

	if len(results) == 0 {
		return "No matches found.", nil
	}
	out := strings.Join(results, "\n")
	if len(results) >= maxSearchResults {
		out += fmt.Sprintf("\n... (truncated at %d results)", maxSearchResults)
	}
	return out, nil
}

func searchFile(path, pattern string, results []string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return results, nil
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if len(results) >= maxSearchResults {
			break
		}
		if strings.Contains(strings.ToLower(line), pattern) {
			results = append(results, fmt.Sprintf("%s:%d: %s", path, i+1, strings.TrimRight(line, "\r")))
		}
	}
	return results, nil
}

func isBinary(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return true
	}
	for _, b := range buf[:n] {
		if b == 0 {
			return true
		}
	}
	return false
}
