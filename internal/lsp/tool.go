package lsp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

// The six questions a language server is asked. All of them are read-only
// and auto-run like the other read-only tools.
// See docs/capabilities/coding-agent.md#six-questions-for-the-language-server.
const (
	DefinitionToolName      = "definition"
	ReferencesToolName      = "references"
	WorkspaceSymbolToolName = "workspace_symbol"
	DocumentSymbolToolName  = "document_symbol"
	HoverToolName           = "hover"
	DiagnosticsToolName     = "diagnostics"
)

// Toolset exposes a Manager as agent tools. It is only registered when at
// least one language server was detected, so the tools never exist without a
// server to back them.
type Toolset struct {
	Manager *Manager
}

// NewToolset wraps a manager for tool registration.
func NewToolset(mgr *Manager) *Toolset { return &Toolset{Manager: mgr} }

// Definitions returns the provider tool definitions to register.
func (t *Toolset) Definitions() []provider.Tool {
	positionProps := `{
		"path": {"type": "string", "description": "File the symbol appears in (absolute or workspace-relative)"},
		"line": {"type": "integer", "description": "1-based line number where the symbol appears"},
		"symbol": {"type": "string", "description": "The identifier text on that line to look up"}
	}`
	return []provider.Tool{
		{
			Name: DefinitionToolName,
			Description: "Jump to the definition of a symbol using the project's language server. " +
				"Point at any occurrence of the symbol (path + line + its text) and get the definition as file:line references. " +
				"Prefer this over searching when you need where something is defined.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": ` + positionProps + `,
				"required": ["path", "line", "symbol"]
			}`),
		},
		{
			Name: ReferencesToolName,
			Description: "List every reference to a symbol using the project's language server. " +
				"Point at any occurrence of the symbol (path + line + its text) and get bounded file:line references, declaration included. " +
				"Prefer this over text search when you need actual usages, not string matches.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": ` + positionProps + `,
				"required": ["path", "line", "symbol"]
			}`),
		},
		{
			Name: WorkspaceSymbolToolName,
			Description: "Search the project's symbol index by name using the language server. " +
				"Give a name or part of one and get the matching declarations as file:line references with their kind. " +
				"Prefer this over search for \"where is X declared\": search finds the word wherever it is written, this finds the declaration exactly.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Symbol name, or part of one, to look for across the project"}
				},
				"required": ["query"]
			}`),
		},
		{
			Name: DocumentSymbolToolName,
			Description: "Outline a file with the language server: every declaration in it, with its kind and line, nested as it is nested. " +
				"Prefer this over read_file when the question is what is in a file — the outline is a fraction of the file and usually settles which part to read.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "File to outline (absolute or workspace-relative)"}
				},
				"required": ["path"]
			}`),
		},
		{
			Name: HoverToolName,
			Description: "Get a symbol's type, signature and documentation from the language server. " +
				"Point at any occurrence of the symbol (path + line + its text). " +
				"Use it instead of opening the file a symbol is declared in when what you need is what it is.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": ` + positionProps + `,
				"required": ["path", "line", "symbol"]
			}`),
		},
		{
			Name: DiagnosticsToolName,
			Description: "Ask the language server what is currently wrong with a file, or with everything it has checked this session. " +
				"Give a path for one file, or omit it for the workspace; errors come first. " +
				"Use it to confirm a change compiles before moving on, or to pick up a check that had not finished when an edit was applied.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "File to report on (absolute or workspace-relative); omit for every file checked this session"}
				}
			}`),
		},
	}
}

// Has reports whether name is an LSP tool this session registered.
func (t *Toolset) Has(name string) bool {
	switch name {
	case DefinitionToolName, ReferencesToolName, WorkspaceSymbolToolName, DocumentSymbolToolName, HoverToolName, DiagnosticsToolName:
		return true
	}
	return false
}

type navigateArgs struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Symbol string `json:"symbol"`
}

// parseNavigateArgs validates the path+line+symbol triple the three
// position-addressed tools share.
func parseNavigateArgs(args json.RawMessage) (navigateArgs, error) {
	var a navigateArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return a, fmt.Errorf("invalid arguments: %w", err)
	}
	if a.Path == "" {
		return a, fmt.Errorf("path is required")
	}
	if a.Line < 1 {
		return a, fmt.Errorf("line must be a 1-based line number")
	}
	if strings.TrimSpace(a.Symbol) == "" {
		return a, fmt.Errorf("symbol is required")
	}
	return a, nil
}

// Execute dispatches an LSP tool call.
func (t *Toolset) Execute(name string, args json.RawMessage) (string, error) {
	switch name {
	case WorkspaceSymbolToolName:
		var a struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		if strings.TrimSpace(a.Query) == "" {
			return "", fmt.Errorf("query is required")
		}
		return t.Manager.WorkspaceSymbol(strings.TrimSpace(a.Query))
	case DocumentSymbolToolName:
		var a struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Path == "" {
			return "", fmt.Errorf("path is required")
		}
		return t.Manager.DocumentSymbol(a.Path)
	case DiagnosticsToolName:
		var a struct {
			Path string `json:"path"`
		}
		// An absent path is the workspace, so an empty argument object is a
		// question rather than a mistake and the field is not required.
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		return t.Manager.Diagnostics(strings.TrimSpace(a.Path))
	case DefinitionToolName:
		a, err := parseNavigateArgs(args)
		if err != nil {
			return "", err
		}
		return t.Manager.Definition(a.Path, a.Line, a.Symbol)
	case ReferencesToolName:
		a, err := parseNavigateArgs(args)
		if err != nil {
			return "", err
		}
		return t.Manager.References(a.Path, a.Line, a.Symbol)
	case HoverToolName:
		a, err := parseNavigateArgs(args)
		if err != nil {
			return "", err
		}
		return t.Manager.Hover(a.Path, a.Line, a.Symbol)
	}
	return "", fmt.Errorf("unknown lsp tool: %s", name)
}

// WrapExecutor returns an executor that dispatches LSP tools and hands
// everything else to next.
//
// It is also where a late answer catches up with the session. Diagnostics an
// edit stopped waiting for ride in front of whatever result the model reads
// next, because there is no other message going its way: the round that made
// the edit is over, and a language server publishing on its own schedule has
// nobody to publish to. An errored call is left alone — its result is the
// error the model is about to read, and a block in front of that reads as
// part of the failure.
// See docs/capabilities/coding-agent.md#diagnostics-that-arrive-late-still-arrive.
func (t *Toolset) WrapExecutor(next func(name string, args json.RawMessage) (string, error)) func(string, json.RawMessage) (string, error) {
	return func(name string, args json.RawMessage) (string, error) {
		var result string
		var err error
		if t.Has(name) {
			result, err = t.Execute(name, args)
		} else {
			result, err = next(name, args)
		}
		// The diagnostics tool has just reported the current set itself;
		// prefixing its own answer with a held copy of part of it would say
		// the same thing twice.
		if err != nil || name == DiagnosticsToolName {
			return result, err
		}
		if held := t.Manager.TakeHeldDiagnostics(); held != "" {
			return held + "\n\n" + result, nil
		}
		return result, nil
	}
}

// Close shuts down every started server.
func (t *Toolset) Close() { t.Manager.Shutdown() }
