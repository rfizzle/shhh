package subagent

// The orchestration tools the parent model sees: spawn_agent (approval-gated;
// starts a background child and returns immediately), agent_report (auto-run;
// status overview or a blocking wait for one child's report) and agent_steer
// (auto-run; puts the parent's words in front of a running child).

import (
	"encoding/json"
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
	// SteerToolName runs on the auto-run path too. A steer spends no money
	// and creates no worktree — it is a message onto the path the child's
	// own lane already writes to — so there is nothing for a card to put to
	// a person, and a redirect that has to wait for one is a redirect that
	// arrives after the rounds it was meant to save.
	SteerToolName = "agent_steer"
)

// SteerSource is where a message put in front of a child came from. It is a
// closed set of three because the surfaces that draw it — the roster the
// orchestrator reads, the lane the person reads, the session record — must
// all say the same word for the same event, and a child that has been
// redirected by the orchestrator is a different thing to read than one its
// own reader steered.
type SteerSource string

const (
	// SteerFromLane: the person opened the child's lane and typed.
	SteerFromLane SteerSource = "lane"
	// SteerFromParent: the orchestrator that wrote the task redirected it.
	SteerFromParent SteerSource = "parent"
	// SteerFromReading: the child's own summariser read its work as off the
	// task and the machinery interrupted it. Unlike the other two this is
	// nobody's message — which is exactly why the surfaces name it.
	SteerFromReading SteerSource = "reading"
)

// Definitions returns the orchestration tool definitions the parent session
// registers. The role enum and its description are built from the profiles
// the session loaded, so a profile the user wrote is one the model can see
// and choose between; nil means the built-in two.
func Definitions(profiles Profiles) []provider.Tool {
	if profiles == nil {
		profiles = BuiltinProfiles()
	}
	names, _ := json.Marshal(profiles.Names())
	return []provider.Tool{
		{
			Name:        SpawnToolName,
			Description: "Delegate a scoped task to a background sub-agent. Roles: " + profiles.describe() + ". The user must approve each spawn. Returns immediately — the agent works in the background; collect its final report with agent_report in a LATER step (never in the same round as the spawn). Give each agent a complete, self-contained task prompt: it cannot see this conversation.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"role": {"type": "string", "enum": ` + string(names) + `, "description": "Which agent profile to run"},
					"task": {"type": "string", "description": "Complete, self-contained task prompt for the agent"},
					"name": {"type": "string", "description": "Optional short name (letters, digits, dashes); auto-generated like researcher-1 when omitted"},
					"paths": {"type": "array", "items": {"type": "string"}, "description": "For agents that change files: the paths or globs this agent may change (e.g. [\"internal/ui/**\", \"README.md\"]). Two concurrent writing agents may not claim overlapping paths — declare them whenever you fan out more than one, so their patches cannot collide.", "maxItems": 32},
					"model": {"type": "string", "description": "Optional model for this agent (defaults to the profile's model, then the configured agent model, then the session model). Use a smaller, cheaper model for wide mechanical work and the session model for reasoning-heavy work."},
					"steps": {"type": "integer", "description": "Optional number of steps this task breaks into (max 20). Pass it when you can name the steps up front: the agent's lane then shows progress against it instead of a spinner. Leave it out rather than guessing — an invented denominator is worse than none."},
					"max_rounds": {"type": "integer", "description": "Optional: make the agent pause every N tool rounds to take stock — what it has done, what is left, what it is doing next — before carrying on with a larger budget. Omitted (the default) it runs to completion without pausing, which is what you want for most tasks. Pass it for long open-ended work where an agent quietly drifting off the task would otherwise go unnoticed. It is a pacing choice, not a limit: it never stops the agent, and the token budget is what bounds it."},
					"max_tokens": {"type": "integer", "description": "Optional token budget (default 200000). It counts new tokens — the part of each prompt the provider did not serve from its cache, plus the completion — so re-reading a cached prompt does not spend it."}
				},
				"required": ["role", "task"]
			}`),
		},
		{
			Name:        ReportToolName,
			Description: "Check on background sub-agents. With no arguments: a status overview of every agent. With a name: waits until that agent finishes and returns its final report (pass wait=false for a non-blocking status peek). An agent's report is returned verbatim; a writing agent's report also states what happened to its patch.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Agent to report on; omit for a status overview of all agents"},
					"wait": {"type": "boolean", "description": "Wait for the named agent to finish (default true)"}
				}
			}`),
		},
		{
			Name:        SteerToolName,
			Description: "Redirect a running sub-agent: your message reaches it as an instruction from you, the way one typed into its lane does. Two things on the roster call for it: an agent listed as steered more than once, which is not answering the check that steers it; and an agent whose line has not moved between two reads several rounds apart, which is the one to watch for when the roster's header says readings are off and nothing but you is checking. You wrote its task, so say what it should do instead. The agent's own reading is judged against the task plus your message, so it will not be told it has drifted for doing what you just asked. Refused once the agent has finished; ending one is the user's, not yours.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Agent to redirect"},
					"message": {"type": "string", "description": "What it should do instead, in your own words — a complete instruction, since the agent cannot see this conversation"}
				},
				"required": ["name", "message"]
			}`),
		},
	}
}

// steerArgs is an agent_steer call. Both fields are required: a steer with
// no name has nowhere to go, and one with no message is a round spent saying
// nothing to a child that is already not answering.
type steerArgs struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

// parseSteerArgs validates agent_steer's arguments. The name is not checked
// against validName: a name the supervisor does not know is refused by the
// supervisor, naming what it has, which is the more useful answer than a
// spelling rule.
func parseSteerArgs(raw json.RawMessage) (steerArgs, error) {
	var args steerArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, fmt.Errorf("invalid arguments: %w", err)
	}
	args.Name = strings.TrimSpace(args.Name)
	args.Message = strings.TrimSpace(args.Message)
	if args.Name == "" {
		return args, fmt.Errorf("name is required: call agent_report with no arguments for the roster")
	}
	if args.Message == "" {
		return args, fmt.Errorf("message is required: say what the agent should do instead")
	}
	return args, nil
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
	profile   Profile
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

// parseSpawnArgs validates spawn_agent arguments against the session's
// profiles and clamps the budgets to their ceilings — the model can lower
// them, never remove them. A budget the call leaves out falls back to the
// profile's, then to the package default. nil profiles means the built-ins.
func parseSpawnArgs(profiles Profiles, raw json.RawMessage) (spawnArgs, error) {
	var args spawnArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, fmt.Errorf("invalid arguments: %w", err)
	}
	if profiles == nil {
		profiles = BuiltinProfiles()
	}
	profile, err := profiles.Parse(args.Role)
	if err != nil {
		return args, err
	}
	args.role = profile.Name
	args.profile = profile
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
			return args, fmt.Errorf("path %q must be relative to the workspace and cannot contain \"..\"", raw)
		}
		args.paths = append(args.paths, filepath.ToSlash(p))
	}
	if len(args.paths) > maxClaimedPaths {
		return args, fmt.Errorf("too many paths (%d; max %d) — claim directories, not individual files", len(args.paths), maxClaimedPaths)
	}
	if !profile.Writes && len(args.paths) > 0 {
		return args, fmt.Errorf("paths apply to agents that can change files; a %s changes nothing", args.role)
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
		args.maxRounds = profile.MaxRounds
	}
	if args.maxRounds <= 0 {
		args.maxRounds = DefaultMaxRounds
	}
	args.maxTokens = args.MaxTokens
	if args.maxTokens <= 0 {
		args.maxTokens = profile.MaxTokens
	}
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
func SpawnSummary(profiles Profiles, raw json.RawMessage) (string, error) {
	args, err := parseSpawnArgs(profiles, raw)
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
	// Role is which profile is being spawned, for the facts a card states
	// that this package cannot resolve — what a child of that role is given
	// is the session's answer, not the supervisor's.
	Role Role
	// Scope is the paths a writer claimed, or the phrase for a child that
	// changes nothing.
	Scope string
	// Writer marks a child that produces a patch; a researcher never does.
	Writer bool
	// Budget is the child's round and token ceiling.
	Budget string
}

// SpawnPlan describes a spawn_agent call the way its approval card needs it.
func SpawnPlan(profiles Profiles, raw json.RawMessage) (Spawn, error) {
	args, err := parseSpawnArgs(profiles, raw)
	if err != nil {
		return Spawn{}, err
	}
	p := Spawn{
		Role:   args.role,
		Writer: args.profile.Writes,
		Budget: fmt.Sprintf("%s, ~%s new tokens", roundBudgetLabel(args.maxRounds), formatTokens(args.maxTokens)),
	}
	switch {
	case len(args.paths) > 0:
		p.Scope = strings.Join(args.paths, ", ")
	case p.Writer:
		p.Scope = "unknown — this agent claimed no paths"
	default:
		p.Scope = "nothing — a " + string(args.role) + " reads and reports"
	}
	return p, nil
}
