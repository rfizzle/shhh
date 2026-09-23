package subagent

// The orchestration tools the parent model sees: spawn_agent (approval-gated;
// starts a background child and returns immediately), agent_report (auto-run;
// status overview or a blocking wait for one child's report), agent_steer
// (auto-run; puts the parent's words in front of a running child) and
// agent_retry (auto-run; runs a failed child again on the task it has).

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
	// RetryToolName runs on the auto-run path too. It starts no agent: it
	// runs one this session already approved, on the task it was already
	// given, in the slot it already spent, under the paths it already
	// claimed — every question a spawn card asks was asked and answered
	// when that agent was spawned. What it does spend is the child's budget
	// again, and that is the session's cap and its ledger to count, as it is
	// for a child that simply runs long.
	RetryToolName = "agent_retry"
)

// SteerSource is where a message put in front of a child came from. It is a
// closed set of three because the surfaces that draw it — the roster the
// orchestrator reads, the lane the person reads, the session record — must
// all say the same word for the same event, and a child that has been
// redirected by the orchestrator is a different thing to read than one its
// own reader steered.
type SteerSource string

const (
	// SteerFromLane: the person typed it — at the child's lane, or into the
	// field the manager opens on its row. One word for both because it is
	// one author: which surface they were looking at is not a thing the
	// child, the roster or the record has any use for.
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
			Description: "Delegate a scoped task to a background sub-agent. Roles: " + profiles.describe() + ". The user must approve each spawn. Returns immediately — the agent works in the background; collect its final report with agent_report in a LATER step (never in the same round as the spawn). Give each agent a complete, self-contained task prompt: it cannot see this conversation. To ask more of an agent that has already reported, send it a follow-up with agent_steer instead: it already knows the ground, so a follow-up is cheaper than a second spawn that has to read it all again.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"role": {"type": "string", "enum": ` + string(names) + `, "description": "Which agent profile to run, from the roles listed above"},
					"task": {"type": "string", "description": "Complete, self-contained task prompt for the agent"},
					"name": {"type": "string", "description": "Optional short name (letters, digits, dashes); auto-generated like researcher-1 when omitted"},
					"paths": {"type": "array", "items": {"type": "string"}, "description": "The paths or globs this agent's work is scoped to (e.g. [\"internal/ui/**\", \"README.md\"]). For an agent that changes files, they are what it may change: two concurrent writing agents may not claim overlapping paths, so declare them whenever you fan out more than one. For a reviewing agent, they are the evidence: it is handed those paths and their diff before its task and reports once it has examined them, so declaring them is what keeps a review from surveying the repository to find the change.", "maxItems": 32},
					"model": {"type": "string", "description": "Optional model for this agent (defaults to the profile's model, then the configured agent model, then the session model). Use a smaller, cheaper model for wide mechanical work and the session model for reasoning-heavy work."},
					"steps": {"type": "integer", "description": "Optional number of steps this task breaks into (max 20). Pass it when you can name the steps up front: the agent's lane then shows progress against it instead of a spinner. Leave it out rather than guessing — an invented denominator is worse than none."},
					"max_rounds": {"type": "integer", "description": "Optional: make the agent pause every N tool rounds to take stock — what it has done, what is left, what it is doing next — before carrying on with a larger budget. Omitted (the default) it runs to completion without pausing, which is what you want for most tasks. Pass it for long open-ended work where an agent quietly drifting off the task would otherwise go unnoticed. It is a pacing choice, not a limit: it never stops the agent, and the token budget is what bounds it."},
					"max_tokens": {"type": "integer", "description": "Optional token budget (default 300000; minimum 200000 before prompt admission). It counts new tokens — the part of each prompt the provider did not serve from its cache, plus the completion. The inherited prompt and declared task must still leave a 200000-token working reserve."},
					"resume_handoff": {"type": "string", "description": "Optional opaque handoff handle from a failed child. The replacement keeps that handoff's original task and declared scope, and receives only its bounded verified context."}
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
			Description: "Redirect a running sub-agent: your message reaches it as an instruction from you, the way one typed into its lane does. Two things on the roster call for it: an agent listed as steered more than once, which is not answering the check that steers it; and an agent whose line has not moved between two reads several rounds apart, which is the one to watch for when the roster's header says readings are off and nothing but you is checking. You wrote its task, so say what it should do instead. The agent's own reading is judged against the task plus your message, so it will not be told it has drifted for doing what you just asked. It reaches an agent that has finished too: there your message is a follow-up — one more turn on the agent's own conversation, with everything it already read — and its answer replaces the report; collect it with agent_report. Refused for an agent that failed (agent_retry runs that one again) and for one whose remaining budget is below the working reserve; ending an agent is the user's, not yours.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Name of the agent to redirect or follow up with"},
					"message": {"type": "string", "description": "What it should do instead, in your own words — a complete instruction, since the agent cannot see this conversation"}
				},
				"required": ["name", "message"]
			}`),
		},
		{
			Name:        RetryToolName,
			Description: "Run a failed sub-agent again on the same task. Only an agent agent_report lists as failed can be retried; it keeps its name, its slot and any paths it claimed, so a retry costs no agent slot and a replacement spawn does. The new attempt is a fresh conversation that opens with how the last one ended and whatever handoff it left, so it does not spend itself the same way, and one that ran out of budget is given a larger one. Returns immediately — collect the result with agent_report in a later step. It cannot change the task: to ask for something else, retry it and then agent_steer, or spawn a new agent.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Name of the failed agent to run again"}
				},
				"required": ["name"]
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

// retryArgs is an agent_retry call: which agent, and nothing else. There is
// deliberately no task field — a retry that could rewrite the task would be a
// spawn with no card, into a slot the user approved for something else. What
// the second attempt should do differently is said with agent_steer once it
// is running, or by spawning an agent for it.
type retryArgs struct {
	Name string `json:"name"`
}

// parseRetryArgs validates agent_retry's arguments. As with a steer, the
// name is not checked against validName: an unknown name is refused by the
// supervisor, which sends the caller to the roster rather than to a spelling
// rule.
func parseRetryArgs(raw json.RawMessage) (retryArgs, error) {
	var args retryArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, fmt.Errorf("invalid arguments: %w", err)
	}
	args.Name = strings.TrimSpace(args.Name)
	if args.Name == "" {
		return args, fmt.Errorf("name is required: call agent_report with no arguments for the roster")
	}
	return args, nil
}

type spawnArgs struct {
	Role          string   `json:"role"`
	Task          string   `json:"task"`
	Name          string   `json:"name"`
	Model         string   `json:"model"`
	Paths         []string `json:"paths"`
	Steps         int      `json:"steps"`
	MaxRounds     int      `json:"max_rounds"`
	MaxTokens     int64    `json:"max_tokens"`
	ResumeHandoff string   `json:"resume_handoff"`

	role          Role
	profile       Profile
	paths         []string
	steps         int
	maxRounds     int
	maxTokens     int64
	resumeHandoff string
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
// profiles and bounds the budgets. A budget the call leaves out falls back to the
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
	args.resumeHandoff = strings.TrimSpace(args.ResumeHandoff)
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
	// Paths mean one of two things and never both: the files a writer claims
	// against every other writer, or the evidence a review is handed and
	// bounded to. A role that neither writes nor reviews has no use for
	// them, and silently dropping them would leave the caller believing it
	// had scoped something.
	if !profile.Writes && !profile.Reviews && len(args.paths) > 0 {
		return args, fmt.Errorf("paths apply to agents that change files or review them; a %s does neither", args.role)
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
	case args.maxTokens < MinChildMaxTokens:
		return args, fmt.Errorf("max_tokens %d is below the minimum %d", args.maxTokens, MinChildMaxTokens)
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
	// Name is the name this call gave the child, empty where it named none
	// and the supervisor will generate one. A card asking about three
	// children at once needs a word per row to tell them apart, and the task
	// is not it: three writers over one file open on the same clause.
	Name string
	// About is the profile's own account of the role, cut to its leading
	// clause. The whole of it is written for the orchestrating model choosing
	// between roles; a card gives it one row.
	About string
	// Task is the first line of what this child was asked to do.
	Task string
	// Scope is the paths a writer claimed, or the phrase for a child that
	// changes nothing. Each is a whole statement, because it is the value of
	// a field whose label is one word: a value needing a second clause beside
	// it would be two sentences written for one slot. A review's paths never
	// appear here as a scope: the card answers "what can this change", and
	// paths that are evidence answer a different question — one the reader
	// would take for the first.
	Scope string
	// Writer marks a child that produces a patch; a researcher never does.
	Writer bool
	// Budget is the child's round and token ceiling.
	Budget string
}

// aboutRole is the one clause of a profile's description a card has room for.
// The description qualifies itself over several clauses because it is written
// for the model choosing between roles; a card gives it a row, and a row that
// printed the whole would be cut mid-word by the frame rather than ending on
// a statement (docs/interface/principles.md#fold-never-hide).
func aboutRole(p Profile) string {
	desc := strings.TrimSpace(p.Description)
	if head, _, ok := strings.Cut(desc, "; "); ok {
		return head
	}
	return desc
}

// SpawnPlan describes a spawn_agent call the way its approval card needs it.
func SpawnPlan(profiles Profiles, raw json.RawMessage) (Spawn, error) {
	args, err := parseSpawnArgs(profiles, raw)
	if err != nil {
		return Spawn{}, err
	}
	p := Spawn{
		Role:   args.role,
		Name:   args.Name,
		About:  aboutRole(args.profile),
		Task:   firstLine(args.Task),
		Writer: args.profile.Writes,
		Budget: fmt.Sprintf("%s, ~%s new tokens", roundBudgetLabel(args.maxRounds), formatTokens(args.maxTokens)),
	}
	switch {
	case args.profile.Reviews && len(args.paths) > 0:
		p.Scope = "nothing — it is handed " + strings.Join(args.paths, ", ") + " and reports"
	case len(args.paths) > 0:
		// The worktree rides the same line as the claim. The paths on their
		// own read as paths on this checkout, which is the one thing a
		// writer's spawn must not be taken for.
		p.Scope = "its own worktree · claims " + strings.Join(args.paths, ", ")
	case p.Writer:
		p.Scope = "unknown — this agent claimed no paths"
	default:
		p.Scope = "reads only — a " + string(args.role) + " changes nothing"
	}
	return p, nil
}
