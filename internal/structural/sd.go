package structural

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

// The how of a bounded answer lives here, beside the tool: sd's preview
// prints every named file whole, as it would read after the replacement,
// whether or not anything in it matched, so the preview is as large as the
// files named and only naming fewer of them shrinks it — max_replacements
// changes what is replaced, not what is printed. Output past MaxOutputBytes is
// cut off and not kept anywhere, because this tool is exempt from the
// evidence pipeline.
// See docs/capabilities/evidence.md#reduction-is-for-unbounded-output.
var sdTool = provider.Tool{
	Name: SdToolName,
	Description: "PREVIEW a find-and-replace across files with sd. This tool never modifies files: it always runs sd with --preview " +
		"and returns each named file in full as it would read after the replacement, under a \"----- FILE <path> -----\" line. Use it to check a transform across several files, then apply the changes you want with edit_file. " +
		"The pattern is a regular expression unless fixed_strings is set; the replacement may use capture groups like $1. " +
		"Every named file is printed whole, matched or not, so name only the files that hold a match (find them with a search first); max_replacements does not shorten the preview. " +
		"Output past 64 KiB is cut off and lost, so a result that says it was truncated is not every file: preview the rest in smaller batches rather than acting on the part you saw.",
	Parameters: json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "Regex (or fixed string) to find"},
			"replacement": {"type": "string", "description": "Replacement text; may reference capture groups ($1, $name). Empty deletes the match"},
			"paths": {"type": "array", "items": {"type": "string"}, "description": "Files to preview the replacement in, relative to the workspace root (at least one); each is printed whole, so name only files that hold a match"},
			"fixed_strings": {"type": "boolean", "description": "Treat pattern and replacement as literal strings"},
			"ignore_case": {"type": "boolean", "description": "Match the pattern without regard to letter case"},
			"multiline": {"type": "boolean", "description": "Let ^ and $ match line boundaries"},
			"dot_all": {"type": "boolean", "description": "Let . match newlines, so a pattern can span lines"},
			"word_boundary": {"type": "boolean", "description": "Match the pattern only where it is a whole word"},
			"max_replacements": {"type": "integer", "description": "Stop after this many replacements in each file; the file is still printed whole"}
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
