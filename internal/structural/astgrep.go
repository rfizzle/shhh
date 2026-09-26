package structural

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

// The how of a bounded answer lives here, beside the tool: output past
// MaxOutputBytes is cut off and not kept anywhere, because this tool is exempt
// from the evidence pipeline, and a common pattern over a whole repository
// runs to hundreds of kilobytes, so the path, the language and the context
// are what bound the answer.
// See docs/capabilities/evidence.md#reduction-is-for-unbounded-output.
var astGrepTool = provider.Tool{
	Name: AstGrepToolName,
	Description: "Language-aware structural code search with ast-grep. Prefer this over regex search for structural questions " +
		"(find every call of a function, match a syntax shape regardless of formatting). The pattern is code with metavariables, " +
		"e.g. \"foo($$$ARGS)\" or \"if $COND { $$$BODY }\". With rewrite set, returns a PREVIEW diff of the proposed transform — " +
		"it never modifies files; apply changes with edit_file. " +
		"Scope the search to the question: point path at the directory or file that holds the matches and set lang, and leave context off unless the lines around a match are what you need. " +
		"Output past 64 KiB is cut off and lost, so a result that says it was truncated is not every match and a truncated rewrite is not the whole diff: run it again over a narrower path rather than acting on the part you saw.",
	Parameters: json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "Structural pattern to search for (code with $META and $$$MULTI metavariables)"},
			"rewrite": {"type": "string", "description": "Optional rewrite template; the result is a preview diff, no file is changed"},
			"lang": {"type": "string", "description": "Language to parse, e.g. \"go\", \"ts\", \"py\" (recommended; inferred from extensions otherwise); it also keeps other languages' files out of the result"},
			"path": {"type": "string", "description": "File or directory to search, relative to the workspace root (default: the workspace root); name the narrowest one that holds the matches"},
			"context": {"type": "integer", "description": "Lines of context to show around each match; each line multiplies the output, so leave it unset unless the surrounding lines are needed"}
		},
		"required": ["pattern"]
	}`),
}

// NoMatches is what ast_grep returns when the pattern matched nothing. It is
// named for the same reason search's and glob's sentences are: anything
// measuring the result has to know that this one line is not one finding.
const NoMatches = "No matches."

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
		return NoMatches, nil
	}
	if args.Rewrite != "" {
		out = "Preview only — no file was changed. Apply wanted changes with edit_file.\n\n" + out
	}
	return out, nil
}
