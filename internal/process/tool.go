package process

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

// ToolName is the model-facing process tool. Only its start action is
// approval-gated (NeedsApproval); status, read, input, and stop auto-run
// against processes the user already approved starting.
const ToolName = "process"

// Definition is the process tool registered for agent sessions.
func Definition() provider.Tool {
	return provider.Tool{
		Name: ToolName,
		Description: "Manage named long-running processes (dev servers, watchers, test runners) owned by this session. " +
			"Action \"start\" launches a command under a name — the user must approve it like any command — with cwd contained to the workspace and an environment of PATH and HOME plus any env vars you pass. " +
			"\"status\" reports one process (or all, when name is omitted); \"read\" pages a stream's captured output (byte-clamped; use offset from a previous read to continue, omit it for the tail); " +
			"\"input\" writes text to the process's stdin verbatim (include a trailing newline to submit a line); \"stop\" terminates the whole process tree. " +
			"Recent output lives in a bounded buffer; the full log (bounded) is stored as evidence when the process ends. " +
			"Every process is terminated when the session ends. Prefer this over execute_command for anything that must keep running while you work (start a server, probe it, read its logs, stop it).",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"action": {"type": "string", "enum": ["start", "status", "read", "input", "stop"], "description": "What to do"},
				"name": {"type": "string", "description": "Process name (letters, digits, . _ -); required except for status of all processes"},
				"command": {"type": "string", "description": "start: the shell command to run"},
				"cwd": {"type": "string", "description": "start: working directory, relative to the workspace root (default: the root)"},
				"env": {"type": "object", "additionalProperties": {"type": "string"}, "description": "start: extra environment variables; PATH and HOME cannot be overridden"},
				"stream": {"type": "string", "enum": ["stdout", "stderr"], "description": "read: which stream (default stdout)"},
				"offset": {"type": "integer", "description": "read: absolute byte offset to read from (omit for the tail)"},
				"limit": {"type": "integer", "description": "read: max bytes to return (default 4096, max 16384)"},
				"text": {"type": "string", "description": "input: text written to stdin verbatim"}
			},
			"required": ["action"]
		}`),
	}
}

type toolArgs struct {
	Action  string            `json:"action"`
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Cwd     string            `json:"cwd"`
	Env     map[string]string `json:"env"`
	Stream  string            `json:"stream"`
	Offset  *int64            `json:"offset"`
	Limit   int               `json:"limit"`
	Text    string            `json:"text"`
}

// NeedsApproval reports whether one process tool call must go through the
// approval queue: only the start action launches anything, so only it gates —
// and unparsable arguments gate too (fail closed; the preview builder then
// rejects them with a clean error result).
func NeedsApproval(args json.RawMessage) bool {
	var a toolArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return true
	}
	return a.Action == "start"
}

// StartSummary extracts the name and command of a start call for the approval
// preview; an error means the arguments are invalid and the call is skipped.
func StartSummary(args json.RawMessage) (name, command string, err error) {
	var a toolArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", "", fmt.Errorf("invalid arguments: %w", err)
	}
	if a.Action != "start" {
		return "", "", fmt.Errorf("not a start action")
	}
	if a.Name == "" || strings.TrimSpace(a.Command) == "" {
		return "", "", fmt.Errorf("start needs both name and command")
	}
	return a.Name, a.Command, nil
}

// Execute dispatches one process tool call.
func (s *Supervisor) Execute(args json.RawMessage) (string, error) {
	var a toolArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	switch a.Action {
	case "start":
		if a.Name == "" {
			return "", fmt.Errorf("name is required")
		}
		return s.start(a.Name, a.Command, a.Cwd, a.Env)
	case "status":
		return s.status(a.Name)
	case "read":
		offset := int64(-1)
		if a.Offset != nil {
			offset = *a.Offset
		}
		return s.read(a.Name, a.Stream, offset, a.Limit)
	case "input":
		return s.input(a.Name, a.Text)
	case "stop":
		return s.stop(a.Name)
	}
	return "", fmt.Errorf("unknown action %q (valid: start, status, read, input, stop)", a.Action)
}

// WrapExecutor returns an executor that dispatches process calls and hands
// everything else to next.
func (s *Supervisor) WrapExecutor(next func(name string, args json.RawMessage) (string, error)) func(string, json.RawMessage) (string, error) {
	return func(name string, args json.RawMessage) (string, error) {
		if name == ToolName {
			return s.Execute(args)
		}
		return next(name, args)
	}
}
