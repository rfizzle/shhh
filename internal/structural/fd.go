package structural

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

var fdTool = provider.Tool{
	Name: FdToolName,
	Description: "Find files and directories by name with fd: fast, gitignore-aware. Prefer this over shelling out to find or ls pipelines. " +
		"The pattern is a regular expression by default (smart case); set glob for glob syntax or literal for a fixed string. " +
		"Returns matching paths, one per line.",
	Parameters: json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "Name pattern to match (regex by default). Omit to list everything under path, bounded by limit"},
			"path": {"type": "string", "description": "Directory to search, relative to the workspace root (default: the workspace root)"},
			"extension": {"type": "string", "description": "Only files with this extension, e.g. \"go\" (no leading dot)"},
			"type": {"type": "string", "enum": ["file", "directory"], "description": "Only entries of this type"},
			"glob": {"type": "boolean", "description": "Treat pattern as a glob instead of a regex"},
			"literal": {"type": "boolean", "description": "Treat pattern as a fixed string instead of a regex"},
			"ignore_case": {"type": "boolean", "description": "Match case-insensitively (default: smart case)"},
			"hidden": {"type": "boolean", "description": "Include hidden files and directories"},
			"no_ignore": {"type": "boolean", "description": "Include files that .gitignore excludes"},
			"max_depth": {"type": "integer", "description": "Limit directory recursion depth"},
			"limit": {"type": "integer", "description": "Maximum results (default 200, max 500)"}
		}
	}`),
}

type fdArgs struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path"`
	Extension  string `json:"extension"`
	Type       string `json:"type"`
	Glob       bool   `json:"glob"`
	Literal    bool   `json:"literal"`
	IgnoreCase bool   `json:"ignore_case"`
	Hidden     bool   `json:"hidden"`
	NoIgnore   bool   `json:"no_ignore"`
	MaxDepth   int    `json:"max_depth"`
	Limit      int    `json:"limit"`
}

// buildFdArgv constructs fd's argv. Invariants: the search path rides
// attached as --search-path=<value> — never fd's bare positional, which is
// ambiguous between a pattern and a path — and the pattern always follows a
// literal "--" delimiter, so a leading "-" can never become an option.
func buildFdArgv(a fdArgs, searchPath string) ([]string, error) {
	if a.Glob && a.Literal {
		return nil, fmt.Errorf("glob and literal are mutually exclusive")
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > MaxFindResults {
		limit = MaxFindResults
	}

	argv := []string{"--color=never"}
	switch a.Type {
	case "":
	case "file":
		argv = append(argv, "--type=f")
	case "directory":
		argv = append(argv, "--type=d")
	default:
		return nil, fmt.Errorf("invalid type %q: use \"file\" or \"directory\"", a.Type)
	}
	if a.Extension != "" {
		argv = append(argv, "--extension="+a.Extension)
	}
	if a.Glob {
		argv = append(argv, "--glob")
	}
	if a.Literal {
		argv = append(argv, "--fixed-strings")
	}
	if a.IgnoreCase {
		argv = append(argv, "--ignore-case")
	}
	if a.Hidden {
		argv = append(argv, "--hidden")
	}
	if a.NoIgnore {
		argv = append(argv, "--no-ignore")
	}
	if a.MaxDepth > 0 {
		argv = append(argv, "--max-depth="+strconv.Itoa(a.MaxDepth))
	}
	argv = append(argv, "--max-results="+strconv.Itoa(limit))
	argv = append(argv, "--search-path="+searchPath)
	if a.Pattern != "" {
		argv = append(argv, "--", a.Pattern)
	}
	return argv, nil
}

func (t *Toolset) executeFd(raw json.RawMessage) (string, error) {
	var args fdArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	searchPath, err := t.resolvePath(args.Path)
	if err != nil {
		return "", err
	}
	argv, err := buildFdArgv(args, searchPath)
	if err != nil {
		return "", err
	}
	out, err := t.run(FdToolName, argv)
	if err != nil {
		return "", err
	}
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return "No files matched.", nil
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > MaxFindResults {
		limit = MaxFindResults
	}
	if strings.Count(out, "\n")+1 >= limit {
		out += fmt.Sprintf("\n… (results capped at %d; narrow the pattern or path to see more)", limit)
	}
	return out, nil
}
