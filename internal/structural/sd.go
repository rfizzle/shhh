package structural

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

var sdTool = provider.Tool{
	Name: SdToolName,
	Description: "PREVIEW a find-and-replace across files with sd. This tool never modifies files: it always runs sd with --preview " +
		"and returns what would change. Use it to check a transform across many files, then apply the changes you want with edit_file. " +
		"The pattern is a regular expression unless fixed_strings is set; the replacement may use capture groups like $1.",
	Parameters: json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "Regex (or fixed string) to find"},
			"replacement": {"type": "string", "description": "Replacement text; may reference capture groups ($1, $name). Empty deletes the match"},
			"paths": {"type": "array", "items": {"type": "string"}, "description": "Files to preview the replacement in, relative to the workspace root (at least one)"},
			"fixed_strings": {"type": "boolean", "description": "Treat pattern and replacement as literal strings"},
			"ignore_case": {"type": "boolean", "description": "Match case-insensitively"},
			"multiline": {"type": "boolean", "description": "Let ^ and $ match line boundaries"},
			"dot_all": {"type": "boolean", "description": "Let . match newlines"},
			"word_boundary": {"type": "boolean", "description": "Match full words only"},
			"max_replacements": {"type": "integer", "description": "Limit replacements per file"}
		},
		"required": ["pattern", "replacement", "paths"]
	}`),
}

type sdArgs struct {
	Pattern         string   `json:"pattern"`
	Replacement     string   `json:"replacement"`
	Paths           []string `json:"paths"`
	FixedStrings    bool     `json:"fixed_strings"`
	IgnoreCase      bool     `json:"ignore_case"`
	Multiline       bool     `json:"multiline"`
	DotAll          bool     `json:"dot_all"`
	WordBoundary    bool     `json:"word_boundary"`
	MaxReplacements int      `json:"max_replacements"`
}

// buildSdArgv constructs sd's argv. Invariants: --preview is always present —
// sd writes files in place by default, so this is load-bearing, not
// defense-in-depth — and pattern, replacement, and every resolved path follow
// a literal "--" delimiter: a pattern colliding with a value-taking flag name
// (like "-f") is otherwise silently consumed as that flag's value, shifting
// sd into blocking on stdin it never receives. Option values ride attached as
// --flag=value.
func buildSdArgv(a sdArgs, resolvedPaths []string) []string {
	argv := []string{"--preview"}
	if a.FixedStrings {
		argv = append(argv, "--fixed-strings")
	}
	var flags strings.Builder
	if a.IgnoreCase {
		flags.WriteByte('i')
	}
	if a.Multiline {
		flags.WriteByte('m')
	}
	if a.DotAll {
		flags.WriteByte('s')
	}
	if a.WordBoundary {
		flags.WriteByte('w')
	}
	if flags.Len() > 0 {
		argv = append(argv, "--flags="+flags.String())
	}
	if a.MaxReplacements > 0 {
		argv = append(argv, "--max-replacements="+strconv.Itoa(a.MaxReplacements))
	}
	argv = append(argv, "--", a.Pattern, a.Replacement)
	return append(argv, resolvedPaths...)
}

func (t *Toolset) executeSd(raw json.RawMessage) (string, error) {
	var args sdArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	resolved, err := t.resolvePaths(args.Paths)
	if err != nil {
		return "", err
	}
	out, err := t.run(SdToolName, buildSdArgv(args, resolved))
	if err != nil {
		return "", err
	}
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return "No replacements: the pattern did not match.", nil
	}
	return "Preview only — no file was changed. Apply wanted changes with edit_file.\n\n" + out, nil
}
