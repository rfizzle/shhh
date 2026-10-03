package subagent

// Path rooting: a child's tool calls resolve relative paths against the
// child's own workspace root (the worktree for writers), and file mutations
// may never escape it. Read-only absolute paths stay allowed — the parent
// session can read anywhere too.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/tools"
)

// RootedExecutor rewrites path arguments against root before dispatching, so
// a child's auto-run tools operate on its own workspace.
func RootedExecutor(root string, next agent.ToolExecutor) agent.ToolExecutor {
	return func(name string, args json.RawMessage) (string, error) {
		rooted, err := RootArgs(root, name, args)
		if err != nil {
			return "", err
		}
		return next(name, rooted)
	}
}

// RootArgs resolves a tool call's "path" argument against root: relative
// paths join root, an absent optional path defaults to root, and a mutating
// tool's resolved path must stay inside root. query names its files in a
// "paths" array, and each entry is resolved the same way. Tools without a
// path argument pass through untouched, as do arguments that don't parse
// (the executor reports those itself).
func RootArgs(root, name string, args json.RawMessage) (json.RawMessage, error) {
	if root == "" {
		return args, nil
	}
	optionalPath := false
	switch name {
	case "read_file", "list_directory", tools.SqliteName, tools.WriteFileName, tools.EditFileName:
	// The language server's questions take the same argument and mean the
	// same thing by it. A child shares its parent's server rather than
	// starting one of its own, and that server resolves a relative path
	// against the parent's checkout — so a writer that asked about
	// `internal/foo.go` would be answered about the copy it is not editing.
	// diagnostics is here for its path and not for its absence: called with
	// none it means every file the server has checked, which is not the
	// workspace and must not be turned into it.
	case lsp.DefinitionToolName, lsp.ReferencesToolName, lsp.DocumentSymbolToolName,
		lsp.HoverToolName, lsp.DiagnosticsToolName:
	case "search", "glob":
		optionalPath = true
	case tools.QueryName:
		return rootPaths(root, args), nil
	default:
		return args, nil
	}

	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return args, nil
	}
	p, _ := m["path"].(string)
	if p == "" {
		if !optionalPath {
			return args, nil
		}
		m["path"] = root
	} else {
		m["path"] = rootPath(root, p)
	}
	if tools.IsMutating(name) {
		final, _ := m["path"].(string)
		if !withinRoot(root, final) {
			return nil, fmt.Errorf("path %q escapes the agent workspace; file changes must stay under %s", p, root)
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return args, nil
	}
	return b, nil
}

// rootPath resolves one path against root: a relative one joins it and an
// absolute one is left as it is.
func rootPath(root, p string) string {
	switch {
	case filepath.IsAbs(p):
		return p
	case strings.Contains(p, "!/"):
		// An archive entry, `x.zip!/path/in/it`: only the archive is a path
		// on disk. Join would clean the whole string, and `x.zip!/../y`
		// would come back as the file y beside the archive — a read of the
		// disk under a name the model meant as a name inside the archive.
		return root + string(filepath.Separator) + p
	default:
		return filepath.Join(root, p)
	}
}

// rootPaths resolves every entry of query's "paths" array against root, the
// way RootArgs resolves a single path. A glob is rooted like any other
// entry: the tool walks from its literal leading directories, so a pattern
// joined to the child's copy is walked in that copy. Arguments that don't
// parse, and entries that are not strings, are left for the tool to refuse.
func rootPaths(root string, args json.RawMessage) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return args
	}
	paths, ok := m["paths"].([]any)
	if !ok {
		return args
	}
	for i, e := range paths {
		if p, ok := e.(string); ok && p != "" {
			paths[i] = rootPath(root, p)
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return args
	}
	return b
}

// withinRoot reports whether p (already cleaned/joined) is root or inside it.
func withinRoot(root, p string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
