package subagent

// The orchestration tools the parent model sees: spawn_agent (approval-gated;
// starts a background child and returns immediately) and agent_report
// (auto-run; status overview or a blocking wait for one child's report).

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

const (
	// SpawnToolName is intercepted by the parent's approval queue: spawning
	// an agent spends money and (for writers) creates a worktree, so the user
	// sees and approves each spawn like any other external action.
	SpawnToolName = "spawn_agent"
	// ReportToolName runs on the auto-run path: it only reads child state.
	ReportToolName = "agent_report"
)

// Definitions returns the orchestration tool definitions the parent session
// registers.
func Definitions() []provider.Tool {
	return []provider.Tool{
		{
			Name:        SpawnToolName,
			Description: "Delegate a scoped task to a background sub-agent. Roles: 'researcher' (read-only tools plus web; use for parallel research and codebase surveys) and 'writer' (full tools against an isolated copy of the workspace; its file changes come back as a single patch the user reviews before anything touches the real checkout). The user must approve each spawn. Returns immediately — the agent works in the background; collect its final report with agent_report in a LATER step (never in the same round as the spawn). Give each agent a complete, self-contained task prompt: it cannot see this conversation.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"role": {"type": "string", "enum": ["researcher", "writer"], "description": "Toolset scope for the agent"},
					"task": {"type": "string", "description": "Complete, self-contained task prompt for the agent"},
					"name": {"type": "string", "description": "Optional short name (letters, digits, dashes); auto-generated like researcher-1 when omitted"},
					"paths": {"type": "array", "items": {"type": "string"}, "description": "For writers: the paths or globs this agent may change (e.g. [\"internal/ui/**\", \"README.md\"]). Two concurrent writers may not claim overlapping paths — declare them whenever you fan out more than one writer, so their patches cannot collide.", "maxItems": 32},
					"model": {"type": "string", "description": "Optional model for this agent (defaults to the configured agent model, else the session model). Use a smaller, cheaper model for wide mechanical work and the session model for reasoning-heavy work."},
					"max_rounds": {"type": "integer", "description": "Optional tool-round budget (default 25, max 50)"},
					"max_tokens": {"type": "integer", "description": "Optional token budget, prompt+completion (default 200000)"}
				},
				"required": ["role", "task"]
			}`),
		},
		{
			Name:        ReportToolName,
			Description: "Check on background sub-agents. With no arguments: a status overview of every agent. With a name: waits until that agent finishes and returns its final report (pass wait=false for a non-blocking status peek). An agent's report is returned verbatim; a writer's report also states what happened to its patch.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Agent to report on; omit for a status overview of all agents"},
					"wait": {"type": "boolean", "description": "Wait for the named agent to finish (default true)"}
				}
			}`),
		},
	}
}

type spawnArgs struct {
	Role      string   `json:"role"`
	Task      string   `json:"task"`
	Name      string   `json:"name"`
	Model     string   `json:"model"`
	Paths     []string `json:"paths"`
	MaxRounds int      `json:"max_rounds"`
	MaxTokens int64    `json:"max_tokens"`

	role      Role
	paths     []string
	maxRounds int
	maxTokens int64
}

// maxClaimedPaths bounds a writer's declared scope; a claim longer than this
// is a sign the model is listing files instead of scoping work.
const maxClaimedPaths = 32

var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{0,23}$`)

// parseSpawnArgs validates spawn_agent arguments and clamps the budgets to
// their ceilings — the model can lower them, never remove them.
func parseSpawnArgs(raw json.RawMessage) (spawnArgs, error) {
	var args spawnArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, fmt.Errorf("invalid arguments: %w", err)
	}
	role, err := ParseRole(args.Role)
	if err != nil {
		return args, err
	}
	args.role = role
	if strings.TrimSpace(args.Task) == "" {
		return args, fmt.Errorf("task is required")
	}
	if args.Name != "" && !validName.MatchString(args.Name) {
		return args, fmt.Errorf("invalid name %q (letters, digits, dashes; max 24 chars)", args.Name)
	}
	args.Model = strings.TrimSpace(args.Model)
	for _, raw := range args.Paths {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}
		if filepath.IsAbs(p) || strings.Contains(p, "..") {
			return args, fmt.Errorf("path %q must be relative to the workspace and cannot contain ..", raw)
		}
		args.paths = append(args.paths, filepath.ToSlash(p))
	}
	if len(args.paths) > maxClaimedPaths {
		return args, fmt.Errorf("too many paths (%d; max %d) — claim directories, not individual files", len(args.paths), maxClaimedPaths)
	}
	if args.role != RoleWriter && len(args.paths) > 0 {
		return args, errors.New("paths apply to writer agents only; a researcher changes nothing")
	}
	args.maxRounds = args.MaxRounds
	switch {
	case args.maxRounds <= 0:
		args.maxRounds = DefaultMaxRounds
	case args.maxRounds > MaxRoundsCeiling:
		args.maxRounds = MaxRoundsCeiling
	}
	args.maxTokens = args.MaxTokens
	switch {
	case args.maxTokens <= 0:
		args.maxTokens = DefaultMaxTokens
	case args.maxTokens < minChildMaxTokens:
		args.maxTokens = minChildMaxTokens
	case args.maxTokens > MaxTokensCeiling:
		args.maxTokens = MaxTokensCeiling
	}
	return args, nil
}

// SpawnSummary renders the one-line approval preview for a spawn_agent call;
// an argument error skips the call like any other invalid gated call.
func SpawnSummary(raw json.RawMessage) (string, error) {
	args, err := parseSpawnArgs(raw)
	if err != nil {
		return "", err
	}
	task := firstLine(args.Task)
	if len(task) > 120 {
		task = task[:120] + "…"
	}
	scope := ""
	if len(args.paths) > 0 {
		scope = " in " + strings.Join(args.paths, ", ")
	}
	model := ""
	if args.Model != "" {
		model = ", " + args.Model
	}
	return fmt.Sprintf("%s agent%s (max %d rounds, ~%s tokens)%s — %s", args.role, model, args.maxRounds, formatTokens(args.maxTokens), scope, task), nil
}
