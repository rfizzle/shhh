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
// tool's resolved path must stay inside root. Tools without a path argument
// pass through untouched, as do arguments that don't parse (the executor
// reports those itself).
func RootArgs(root, name string, args json.RawMessage) (json.RawMessage, error) {
	if root == "" {
		return args, nil
	}
	optionalPath := false
	switch name {
	case "read_file", "list_directory", tools.WriteFileName, tools.EditFileName:
	case "search", "glob":
		optionalPath = true
	default:
		return args, nil
	}

	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return args, nil
	}
	p, _ := m["path"].(string)
	switch {
	case p == "":
		if !optionalPath {
			return args, nil
		}
		m["path"] = root
	case !filepath.IsAbs(p):
		m["path"] = filepath.Join(root, p)
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

// displayPath renders a child path workspace-relative for approval cards.
func displayPath(root, p string) string {
	if root == "" {
		return p
	}
	absRoot, err1 := filepath.Abs(root)
	absPath, err2 := filepath.Abs(p)
	if err1 != nil || err2 != nil {
		return p
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	return rel
}
