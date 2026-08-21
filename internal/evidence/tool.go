package evidence

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

// ToolName is the model-facing evidence tool. It is read-only over the
// session's own store, so it runs on the auto-run path without approval.
const ToolName = "evidence"

// Evidence tool bounds: read is byte-clamped and paged, search match counts
// are capped.
const (
	DefaultReadBytes = 4096
	MaxReadBytes     = 16384
	MaxSearchMatches = 50
)

// ToolDefinition is the evidence tool the agent registers alongside the
// session toolset when a store is available.
func ToolDefinition() provider.Tool {
	return provider.Tool{
		Name:        ToolName,
		Description: "Inspect the full original of a reduced tool result. Reduced results carry an opaque evidence id (e.g. ev-1a2b3c4d5e6f7089); pass it here with action \"info\" (metadata), \"read\" (page through the original bytes with offset/limit), or \"search\" (find a literal substring, case-insensitive). Ids are session-scoped tokens, never file paths.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"action": {"type": "string", "enum": ["info", "read", "search"], "description": "What to do with the evidence entry"},
				"id": {"type": "string", "description": "Evidence id from a reduction notice, e.g. ev-1a2b3c4d5e6f7089"},
				"offset": {"type": "integer", "description": "read: byte offset to start from (default 0)"},
				"limit": {"type": "integer", "description": "read: max bytes to return (default 4096, max 16384)"},
				"query": {"type": "string", "description": "search: literal substring to find (case-insensitive)"}
			},
			"required": ["action", "id"]
		}`),
	}
}

type toolArgs struct {
	Action string `json:"action"`
	ID     string `json:"id"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
	Query  string `json:"query"`
}

// ExecuteTool dispatches one evidence tool call against the store.
func (s *Store) ExecuteTool(raw json.RawMessage) (string, error) {
	var args toolArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.ID == "" {
		return "", fmt.Errorf("id is required")
	}

	switch args.Action {
	case "info":
		meta, err := s.Info(args.ID)
		if err != nil {
			return "", err
		}
		out := fmt.Sprintf("evidence %s: tool %s, %d bytes stored (original %d bytes), created %s",
			args.ID, meta.Tool, meta.Stored, meta.Size, meta.Created.Format(time.RFC3339))
		if meta.Truncated {
			out += fmt.Sprintf("\nnote: the original exceeded the %d-byte store cap; only the first %d bytes were kept", MaxStoredBytes, meta.Stored)
		}
		return out, nil

	case "read":
		limit := args.Limit
		if limit <= 0 {
			limit = DefaultReadBytes
		}
		if limit > MaxReadBytes {
			limit = MaxReadBytes
		}
		data, meta, err := s.Read(args.ID, args.Offset, limit)
		if err != nil {
			return "", err
		}
		offset := args.Offset
		if offset < 0 {
			offset = 0
		}
		if int64(offset) > meta.Stored {
			offset = int(meta.Stored)
		}
		out := fmt.Sprintf("evidence %s bytes %d-%d of %d:\n%s", args.ID, offset, offset+len(data), meta.Stored, sanitize(string(data)))
		if int64(offset+len(data)) < meta.Stored {
			out += fmt.Sprintf("\n… (continue with offset=%d)", offset+len(data))
		}
		return out, nil

	case "search":
		if args.Query == "" {
			return "", fmt.Errorf("query is required for search")
		}
		matches, total, err := s.Search(args.ID, args.Query, MaxSearchMatches)
		if err != nil {
			return "", err
		}
		if total == 0 {
			return fmt.Sprintf("evidence %s: no lines contain %q", args.ID, args.Query), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "evidence %s: %d line(s) contain %q", args.ID, total, args.Query)
		if total > len(matches) {
			fmt.Fprintf(&b, " (showing first %d)", len(matches))
		}
		for _, m := range matches {
			fmt.Fprintf(&b, "\nL%d: %s", m.Line, clipLine(sanitize(m.Text)))
		}
		return b.String(), nil
	}
	return "", fmt.Errorf("unknown action %q: use info, read, or search", args.Action)
}
