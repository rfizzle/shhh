package lsp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

// DefinitionToolName and ReferencesToolName are the navigation tools (S-071).
// Both are read-only and auto-run like the other read-only tools.
const (
	DefinitionToolName = "definition"
	ReferencesToolName = "references"
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
	}
}

// Has reports whether name is an LSP tool this session registered.
func (t *Toolset) Has(name string) bool {
	return name == DefinitionToolName || name == ReferencesToolName
}

type navigateArgs struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Symbol string `json:"symbol"`
}

// Execute dispatches an LSP tool call.
func (t *Toolset) Execute(name string, args json.RawMessage) (string, error) {
	var a navigateArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if a.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if a.Line < 1 {
		return "", fmt.Errorf("line must be a 1-based line number")
	}
	if strings.TrimSpace(a.Symbol) == "" {
		return "", fmt.Errorf("symbol is required")
	}
	switch name {
	case DefinitionToolName:
		return t.Manager.Definition(a.Path, a.Line, a.Symbol)
	case ReferencesToolName:
		return t.Manager.References(a.Path, a.Line, a.Symbol)
	}
	return "", fmt.Errorf("unknown lsp tool: %s", name)
}

// WrapExecutor returns an executor that dispatches LSP tools and hands
// everything else to next.
func (t *Toolset) WrapExecutor(next func(name string, args json.RawMessage) (string, error)) func(string, json.RawMessage) (string, error) {
	return func(name string, args json.RawMessage) (string, error) {
		if t.Has(name) {
			return t.Execute(name, args)
		}
		return next(name, args)
	}
}

// Close shuts down every started server.
func (t *Toolset) Close() { t.Manager.Shutdown() }
