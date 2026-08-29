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
					"steps": {"type": "integer", "description": "Optional number of steps this task breaks into (max 20). Pass it when you can name the steps up front: the agent's lane then shows progress against it instead of a spinner. Leave it out rather than guessing — an invented denominator is worse than none."},
					"max_rounds": {"type": "integer", "description": "Optional: make the agent pause every N tool rounds to take stock — what it has done, what is left, what it is doing next — before carrying on with a larger budget. Omitted (the default) it runs to completion without pausing, which is what you want for most tasks. Pass it for long open-ended work where an agent quietly drifting off the task would otherwise go unnoticed. It is a pacing choice, not a limit: it never stops the agent, and the token budget is what bounds it."},
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
	Steps     int      `json:"steps"`
	MaxRounds int      `json:"max_rounds"`
	MaxTokens int64    `json:"max_tokens"`

	role      Role
	paths     []string
	steps     int
	maxRounds int
	maxTokens int64
}

// MaxDeclaredSteps bounds the step count a spawn may declare. A lane
// is five cells wide; a task claiming more steps than this is describing its
// tool calls, not its shape, and the lane falls back to the spinner.
const MaxDeclaredSteps = 20

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
	// A step count outside the useful range is dropped rather than clamped:
	// the lane's rule is that a denominator nobody supplied is not invented,
	// and a clamped one is invented.
	if args.Steps > 0 && args.Steps <= MaxDeclaredSteps {
		args.steps = args.Steps
	}
	// A named interval is honoured as given: it decides how often the child
	// takes stock, not what it is allowed to do, so there is nothing a
	// ceiling would protect.
	args.maxRounds = args.MaxRounds
	if args.maxRounds <= 0 {
		args.maxRounds = DefaultMaxRounds
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

// roundBudgetLabel renders a child's round setting for the surfaces that
// price a spawn. The unbounded child is the ordinary one now, and it
// has to read as a deliberate default rather than a missing number — while
// the bounded one is describing a rhythm, not a ceiling, so it must not be
// rendered as "max N" beside a token budget that really is one.
func roundBudgetLabel(maxRounds int) string {
	if maxRounds <= 0 {
		return "no round limit"
	}
	return fmt.Sprintf("checks in every %d rounds", maxRounds)
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
	return fmt.Sprintf("%s agent%s (%s, ~%s tokens)%s — %s", args.role, model, roundBudgetLabel(args.maxRounds), formatTokens(args.maxTokens), scope, task), nil
}

// Spawn is what spawning a child would cost, for the approval card's
// blast-radius block: the scope it may change, whether its work
// reaches the checkout without another decision, and its token ceiling.
type Spawn struct {
	// Scope is the paths a writer claimed, or the phrase for a child that
	// changes nothing.
	Scope string
	// Writer marks a child that produces a patch; a researcher never does.
	Writer bool
	// Budget is the child's round and token ceiling.
	Budget string
}

// SpawnPlan describes a spawn_agent call the way its approval card needs it.
func SpawnPlan(raw json.RawMessage) (Spawn, error) {
	args, err := parseSpawnArgs(raw)
	if err != nil {
		return Spawn{}, err
	}
	p := Spawn{
		Writer: args.role == RoleWriter,
		Budget: fmt.Sprintf("%s, ~%s tokens", roundBudgetLabel(args.maxRounds), formatTokens(args.maxTokens)),
	}
	switch {
	case len(args.paths) > 0:
		p.Scope = strings.Join(args.paths, ", ")
	case p.Writer:
		p.Scope = "unknown — this writer claimed no paths"
	default:
		p.Scope = "nothing — a researcher reads and reports"
	}
	return p, nil
}
