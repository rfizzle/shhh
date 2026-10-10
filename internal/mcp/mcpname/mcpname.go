// Package mcpname is how an MCP tool's session name is spelled and read back:
// the separator that joins a server to a tool, the split that recognises a
// server's tool without a registry lookup, and the one-line rendering of a
// call's arguments.
//
// It is a leaf of its own so that a package which only reads these names,
// such as the activity digest, does not take a dependency on the whole MCP
// client and everything that client reaches. internal/mcp re-exports each
// name, so its other callers are unchanged.
package mcpname

import (
	"encoding/json"
	"sort"
	"strings"
)

// Separator joins a server name to a tool name. No tool of shhh's own
// contains it, which is what lets a name be recognised as a server's
// without a registry lookup.
const Separator = "__"

// SplitName returns the server part of a session tool name, and whether
// the name is a server's at all.
func SplitName(name string) (server string, ok bool) {
	server, _, ok = strings.Cut(name, Separator)
	if !ok || server == "" {
		return "", false
	}
	return server, true
}

// CompactArgs renders arguments as `key=value` pairs on one line, values
// clipped, keys sorted — enough to recognise a call, never a page of it.
func CompactArgs(raw json.RawMessage) string {
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil || len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		var v string
		switch x := args[k].(type) {
		case string:
			v = x
		default:
			b, _ := json.Marshal(x)
			v = string(b)
		}
		v = strings.ReplaceAll(v, "\n", " ")
		if r := []rune(v); len(r) > 60 {
			v = string(r[:57]) + "..."
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, " ")
}
