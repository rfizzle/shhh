package structural

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

var tokeiTool = provider.Tool{
	Name: TokeiToolName,
	Description: "Summarize codebase composition with tokei: per-language file, line, code, comment, and blank counts. " +
		"Use it to get oriented in an unfamiliar codebase before reading files.",
	Parameters: json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Directory to summarize, relative to the workspace root (default: the workspace root)"},
			"exclude": {"type": "array", "items": {"type": "string"}, "description": "Glob patterns to exclude, e.g. [\"*.min.js\"]"},
			"hidden": {"type": "boolean", "description": "Include hidden files and directories"},
			"no_ignore": {"type": "boolean", "description": "Include files that .gitignore excludes"},
			"sort": {"type": "string", "enum": ["files", "lines", "code", "comments", "blanks"], "description": "Sort languages by this column"}
		}
	}`),
}

type tokeiArgs struct {
	Path     string   `json:"path"`
	Exclude  []string `json:"exclude"`
	Hidden   bool     `json:"hidden"`
	NoIgnore bool     `json:"no_ignore"`
	Sort     string   `json:"sort"`
}

// buildTokeiArgv constructs tokei's argv. Invariants: every exclude pattern
// rides attached as --exclude=<value> so a leading "-" cannot inject an
// option, and the path follows a literal "--" delimiter. Per-file and
// serialized output modes are deliberately absent, keeping this an overview
// rather than an unbounded listing.
func buildTokeiArgv(a tokeiArgs, path string) ([]string, error) {
	var argv []string
	switch a.Sort {
	case "":
	case "files", "lines", "code", "comments", "blanks":
		argv = append(argv, "--sort="+a.Sort)
	default:
		return nil, fmt.Errorf("invalid sort %q: use files, lines, code, comments, or blanks", a.Sort)
	}
	for _, pattern := range a.Exclude {
		if pattern == "" {
			return nil, fmt.Errorf("exclude entries must not be empty")
		}
		argv = append(argv, "--exclude="+pattern)
	}
	if a.Hidden {
		argv = append(argv, "--hidden")
	}
	if a.NoIgnore {
		argv = append(argv, "--no-ignore")
	}
	return append(argv, "--", path), nil
}

func (t *Toolset) executeTokei(raw json.RawMessage) (string, error) {
	var args tokeiArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	path, err := t.resolvePath(args.Path)
	if err != nil {
		return "", err
	}
	argv, err := buildTokeiArgv(args, path)
	if err != nil {
		return "", err
	}
	out, err := t.run(TokeiToolName, argv)
	if err != nil {
		return "", err
	}
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return "No source files found.", nil
	}
	return out, nil
}
