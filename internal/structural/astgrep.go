package structural

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

var astGrepTool = provider.Tool{
	Name: AstGrepToolName,
	Description: "Language-aware structural code search with ast-grep. Prefer this over regex search for structural questions " +
		"(find every call of a function, match a syntax shape regardless of formatting). The pattern is code with metavariables, " +
		"e.g. \"foo($$$ARGS)\" or \"if $COND { $$$BODY }\". With rewrite set, returns a PREVIEW diff of the proposed transform — " +
		"it never modifies files; apply changes with edit_file.",
	Parameters: json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "Structural pattern to search for (code with $META and $$$MULTI metavariables)"},
			"rewrite": {"type": "string", "description": "Optional rewrite template; the result is a preview diff, no file is changed"},
			"lang": {"type": "string", "description": "Language to parse, e.g. \"go\", \"ts\", \"py\" (recommended; inferred from extensions otherwise)"},
			"path": {"type": "string", "description": "File or directory to search, relative to the workspace root (default: the workspace root)"},
			"context": {"type": "integer", "description": "Lines of context to show around each match"}
		},
		"required": ["pattern"]
	}`),
}

type astGrepArgs struct {
	Pattern string `json:"pattern"`
	Rewrite string `json:"rewrite"`
	Lang    string `json:"lang"`
	Path    string `json:"path"`
	Context int    `json:"context"`
}

// buildAstGrepArgv constructs ast-grep's argv. Invariants: pattern, rewrite,
// and lang always ride attached as --flag=value so a leading "-" can never
// inject an option; the search path follows a literal "--" delimiter; and
// -U/--update-all is never passed, so rewrite only ever previews a diff.
func buildAstGrepArgv(a astGrepArgs, searchPath string) []string {
	argv := []string{"run", "--color=never", "--pattern=" + a.Pattern}
	if a.Rewrite != "" {
		argv = append(argv, "--rewrite="+a.Rewrite)
	}
	if a.Lang != "" {
		argv = append(argv, "--lang="+a.Lang)
	}
	if a.Context > 0 {
		argv = append(argv, "--context="+strconv.Itoa(a.Context))
	}
	return append(argv, "--", searchPath)
}

func (t *Toolset) executeAstGrep(raw json.RawMessage) (string, error) {
	var args astGrepArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	searchPath, err := t.resolvePath(args.Path)
	if err != nil {
		return "", err
	}
	out, err := t.run(AstGrepToolName, buildAstGrepArgv(args, searchPath))
	if err != nil {
		return "", err
	}
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return "No matches.", nil
	}
	if args.Rewrite != "" {
		out = "Preview only — no file was changed. Apply wanted changes with edit_file.\n\n" + out
	}
	return out, nil
}
