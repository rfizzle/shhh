package structural

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

var jaqTool = provider.Tool{
	Name: JaqToolName,
	Description: "Query JSON files with jaq, a fast jq-compatible engine. Give a jq-style filter expression and the files to run it over. " +
		"This is the structured tool for JSON. Use yq for YAML and XML. " +
		"Prefer this over improvising shell pipelines for structured-data questions. Read-only: it cannot modify files.",
	Parameters: json.RawMessage(`{
		"type": "object",
		"properties": {
			"expression": {"type": "string", "description": "jq-style filter expression, e.g. \".dependencies | keys\""},
			"paths": {"type": "array", "items": {"type": "string"}, "description": "JSON files to query, relative to the workspace root (at least one)"},
			"slurp": {"type": "boolean", "description": "Read all inputs into one array before filtering"},
			"raw_output": {"type": "boolean", "description": "Output strings without JSON quotes"},
			"compact": {"type": "boolean", "description": "Compact single-line output"},
			"indent": {"type": "integer", "description": "Indentation width for pretty output"}
		},
		"required": ["expression", "paths"]
	}`),
}

type jaqArgs struct {
	Expression string   `json:"expression"`
	Paths      []string `json:"paths"`
	Slurp      bool     `json:"slurp"`
	RawOutput  bool     `json:"raw_output"`
	Compact    bool     `json:"compact"`
	Indent     int      `json:"indent"`
}

// buildJaqArgv constructs jaq's argv. Invariants: the expression and every
// resolved path follow a literal "--" delimiter — a dash-prefixed expression
// is otherwise parsed as an unknown flag — and --indent rides as two separate
// argv tokens, since jaq rejects the attached --flag=value form outright. The
// flags that read arbitrary files or modules by name (-L, -f/--from-file,
// --slurpfile, --rawfile) and the file-mutating --in-place are never in this
// tool's vocabulary, which is what keeps the paths containment check
// airtight: jaq's filter language has no builtin reachable without those
// flags that opens a file.
func buildJaqArgv(a jaqArgs, resolvedPaths []string) []string {
	var argv []string
	if a.Slurp {
		argv = append(argv, "--slurp")
	}
	if a.RawOutput {
		argv = append(argv, "--raw-output")
	}
	if a.Compact {
		argv = append(argv, "--compact-output")
	}
	if a.Indent > 0 {
		argv = append(argv, "--indent", strconv.Itoa(a.Indent))
	}
	argv = append(argv, "--", a.Expression)
	return append(argv, resolvedPaths...)
}

func (t *Toolset) executeJaq(raw json.RawMessage) (string, error) {
	var args jaqArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Expression == "" {
		return "", fmt.Errorf("expression is required")
	}
	resolved, err := t.resolvePaths(args.Paths)
	if err != nil {
		return "", err
	}
	out, err := t.run(JaqToolName, buildJaqArgv(args, resolved))
	if err != nil {
		return "", err
	}
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return "(no output)", nil
	}
	return out, nil
}
