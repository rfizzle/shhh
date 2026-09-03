package structural

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

var yqTool = provider.Tool{
	Name: YqToolName,
	Description: "Query YAML and XML files with yq. Give a yq expression — jq-style path syntax — and the files to run it over. " +
		"This is the structured tool for YAML and XML: CI workflows, manifests, linter configuration. Use jaq for JSON. " +
		"Prefer it over improvising shell pipelines for structured-data questions: it answers at the right nesting level, where a text search returns whichever indentation happened to match. " +
		"Read-only: it cannot modify files.",
	Parameters: json.RawMessage(`{
		"type": "object",
		"properties": {
			"expression": {"type": "string", "description": "yq expression, e.g. \".jobs | keys\""},
			"paths": {"type": "array", "items": {"type": "string"}, "description": "YAML or XML files to query, relative to the workspace root (at least one)"},
			"all_documents": {"type": "boolean", "description": "Evaluate every document of every file as one input, rather than each on its own"},
			"input_format": {"type": "string", "enum": ["yaml", "json", "xml", "props", "csv", "tsv"], "description": "Parse the input as this rather than guessing from the extension"},
			"output_format": {"type": "string", "enum": ["yaml", "json", "xml", "props", "csv", "tsv"], "description": "Print the result in this format"},
			"pretty_print": {"type": "boolean", "description": "Format the result rather than emitting it flat"},
			"indent": {"type": "integer", "description": "Indentation width for the output"},
			"no_document_separators": {"type": "boolean", "description": "Omit the --- separators between documents"}
		},
		"required": ["expression", "paths"]
	}`),
}

type yqArgs struct {
	Expression           string   `json:"expression"`
	Paths                []string `json:"paths"`
	AllDocuments         bool     `json:"all_documents"`
	InputFormat          string   `json:"input_format"`
	OutputFormat         string   `json:"output_format"`
	PrettyPrint          bool     `json:"pretty_print"`
	Indent               int      `json:"indent"`
	NoDocumentSeparators bool     `json:"no_document_separators"`
}

// The two flags that make the containment check in front of this tool mean
// anything. yq's expression language has operator families that open a file
// (load, load_str) and read the environment (env, strenv, envsubst) from
// inside the expression, so a caller that never names a second path can still
// read one — and paths are the only thing resolvePaths can check. Without
// both flags the most permissive field of the schema is an arbitrary file
// read wearing a query's clothes.
// See docs/capabilities/coding-agent.md#two-programs-answer-to-yq.
const (
	yqDisableFileOps = "--security-disable-file-ops"
	yqDisableEnvOps  = "--security-disable-env-ops"
)

// yqFormats is the closed set both format fields accept. The value rides
// attached as --flag=value, so this is not what keeps a leading "-" out of
// the argv; it is what turns a typo into a sentence naming the six formats
// instead of yq's own parse error.
var yqFormats = []string{"yaml", "json", "xml", "props", "csv", "tsv"}

// buildYqArgv constructs yq's argv. Invariants: both security flags lead
// every argv this function can produce, including the minimal one; the
// expression and every resolved path follow a literal "--" delimiter, since a
// dash-prefixed expression is otherwise parsed as an unknown flag; and format
// and indent values ride attached as --flag=value. The file-writing flags
// (-i/--inplace) and the ones that read a program or expression from
// elsewhere (-f/--from-file) are not in this tool's vocabulary, so there is no
// field a caller could put one in.
func buildYqArgv(a yqArgs, resolvedPaths []string) ([]string, error) {
	// eval reads each document on its own; eval-all loads them all as one
	// input, which is what a query spanning documents needs.
	subcommand := "eval"
	if a.AllDocuments {
		subcommand = "eval-all"
	}
	argv := []string{subcommand, yqDisableFileOps, yqDisableEnvOps}

	if a.InputFormat != "" {
		if !slices.Contains(yqFormats, a.InputFormat) {
			return nil, fmt.Errorf("invalid input_format %q: use %s", a.InputFormat, strings.Join(yqFormats, ", "))
		}
		argv = append(argv, "--input-format="+a.InputFormat)
	}
	if a.OutputFormat != "" {
		if !slices.Contains(yqFormats, a.OutputFormat) {
			return nil, fmt.Errorf("invalid output_format %q: use %s", a.OutputFormat, strings.Join(yqFormats, ", "))
		}
		argv = append(argv, "--output-format="+a.OutputFormat)
	}
	if a.PrettyPrint {
		argv = append(argv, "--prettyPrint")
	}
	if a.Indent > 0 {
		argv = append(argv, "--indent="+strconv.Itoa(a.Indent))
	}
	if a.NoDocumentSeparators {
		argv = append(argv, "--no-doc")
	}

	argv = append(argv, "--", a.Expression)
	return append(argv, resolvedPaths...), nil
}

func (t *Toolset) executeYq(raw json.RawMessage) (string, error) {
	var args yqArgs
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
	argv, err := buildYqArgv(args, resolved)
	if err != nil {
		return "", err
	}
	out, err := t.run(YqToolName, argv)
	if err != nil {
		return "", err
	}
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return "(no output)", nil
	}
	return out, nil
}

// UnsupportedBinary reports why the binary at path cannot back the named
// tool, or "" when it can. Registration and `shhh doctor` both ask through
// here, so the two cannot disagree about what this machine has.
//
// Every tool but yq answers "": for the others, being on PATH under the
// expected name is the whole question.
func UnsupportedBinary(name, path string) string {
	// The tool and the binary it wraps share this name, so registration
	// passing the tool name and doctor passing the binary name reach the same
	// probe.
	if name != YqToolName {
		return ""
	}
	return yqFlavor(path)
}

// yqProbeTimeout bounds the one version probe registration makes. It is short
// for the reason the repository probe is: this runs at session start, and a
// binary that never answers would otherwise hold the whole startup.
const yqProbeTimeout = 5 * time.Second

// mikefarahMarker is what the Go yq prints in its version banner and the
// Python one has no reason to. Matching on the author rather than on a
// version number keeps the probe working across releases.
const mikefarahMarker = "mikefarah"

// yqFlavor reports why the yq at path is not the program this package wraps,
// or "" when it is. Two unrelated programs install as `yq`, only one of them
// takes the flags that shut off reading files and the environment from inside
// an expression, and a silent success under the other one would mean nothing
// was ever disabled — so anything this probe cannot confirm is treated as
// absent.
// See docs/capabilities/coding-agent.md#two-programs-answer-to-yq.
//
// A variable so tests can answer without either program installed.
var yqFlavor = func(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), yqProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return "did not answer --version"
	}
	if !strings.Contains(string(out), mikefarahMarker) {
		return "not mikefarah's Go yq"
	}
	return ""
}
