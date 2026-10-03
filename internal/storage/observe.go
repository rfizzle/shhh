package storage

// Session observability: agent sessions record content-free events — tokens,
// cost, model, tool-call counts/durations/outcomes, mode decisions with
// enum-like reason codes, turn ends, and the signals the loop's own
// safeguards raise. Never prompts, outputs, paths, or commands: every stored
// string is either a fixed identifier (provider, model, tool name, skill
// name) or a code from a closed set, so the content-free guarantee holds
// structurally. The one deliberate exception is the export's transcript
// join, which the caller has to ask for.
// See docs/capabilities/sessions-and-memory.md#observations-are-what-the-session-did.

import "time"

const observeTimeFormat = "2006-01-02T15:04:05.000Z"

// agentHeartbeatWindow is how long a session's row may go unrefreshed before
// its process id stops being taken as evidence that the session is running.
//
// The id is the liveness check; the window only bounds the one way that
// check can lie, which is a dead session's id being reused by something
// else. It is deliberately generous, because the opposite failure is the one
// that matters here: a second session sitting at its start screen while
// somebody works in the first refreshes nothing for as long as it is idle,
// and a short window would hide exactly the session this reading exists to
// reveal. Nothing stays stale for long either way — the next session's start
// closes every row whose process is gone.
const agentHeartbeatWindow = 12 * time.Hour

// pidRunning answers whether a process is still there. It is a variable
// because "a process that is definitely gone" has no portable spelling a
// test can write down — an id that is free on this machine is in use on the
// next, and each platform disagrees about which ids are even possible.
var pidRunning = pidAlive

// Agent event kinds.
const (
	AgentEventTool     = "tool"
	AgentEventDecision = "decision"
	// AgentEventTurn closes one turn: outcome is how it ended, round is how
	// many tool rounds it took, duration_ms its wall time.
	AgentEventTurn = "turn"
	// AgentEventSignal is one of the loop's own safeguards or a workflow
	// transition firing: outcome is the signal code, reason its qualifier.
	AgentEventSignal = "signal"
)

// AgentEvent is one content-free event. Turn and Round place it in the
// session — a tool call in round 40 of turn 3 is a different fact from the
// same call in round 2 — and are zero only where the recorder that wrote the
// row kept no such accounting, which every surface shipping today does.
type AgentEvent struct {
	Kind       string
	Tool       string
	DurationMs *int64
	Outcome    string
	Reason     string
	Turn       int64
	Round      int64
	// Purpose is what an execute_command did, as a word from observe's
	// closed set, and empty for every other tool. It is a column of its own
	// rather than the reason because a command that failed carries its
	// failure's class there, and one row has to say both.
	Purpose string
}

// AgentProvenance is what a session ran under, stamped once it is known.
// It is what makes a before/after comparison of a prompt or a workflow
// change possible: without it two weeks of sessions are one population.
type AgentProvenance struct {
	// Version is the shhh build.
	Version string
	// PromptHash fingerprints the system prompt as sent, so an edit to it
	// splits the sessions on either side.
	PromptHash string
	// Skills is how many skills the catalog loaded.
	Skills int
	// Project fingerprints the checkout the session ran in.
	Project string
	// Settings is what the session was configured with. It is written only
	// when its ConfigHash is set: a hash is what says the set was taken, so
	// a stamp that carries none leaves the columns NULL rather than writing
	// a row of zero values that would read as "manual, off, uncapped".
	Settings AgentSettings
}

// AgentSettings is what a session was configured with: the tuning values a
// comparison can group and filter by, and one hash over the whole effective
// config for everything it cannot.
//
// The two halves answer different questions and neither replaces the other.
// A hash tells "before I changed something" from "after", for a setting
// nobody thought to enumerate — but it has no order and no meaning, so it
// cannot answer "interval 10 against interval 20", and that is the question
// a tuning loop actually asks. The scalars can; the hash catches what they
// miss.
//
// The scalars are an allowlist, and that is what keeps the record
// content-free: every value here is a mode name, a level, a model name, a
// count or a profile name — a fixed identifier or a code from a closed set,
// never a path, a command or a secret. A config field is stamped by being
// named here and nowhere else, so a new field is excluded until someone
// decides otherwise; it still reaches the hash, so a change to it is never
// invisible.
// See docs/capabilities/sessions-and-memory.md#what-a-session-ran-under.
type AgentSettings struct {
	// Mode is the permission mode the session started in, empty on a
	// surface with no permission mode (a one-shot, a headless run).
	Mode string `json:"mode,omitempty"`
	// Reasoning is the level the session started thinking at.
	Reasoning string `json:"reasoning"`
	// MaxRounds is the per-turn tool-round cap in force; 0 is no cap.
	MaxRounds int `json:"max_rounds"`
	// SummaryModel and SummaryInterval are the summariser's model and
	// reading interval when SummaryEnabled, and empty otherwise.
	SummaryModel    string `json:"summary_model,omitempty"`
	SummaryInterval int    `json:"summary_interval,omitempty"`
	SummaryEnabled  bool   `json:"summary_enabled"`
	// CheckInInterval is how many rounds pass before the surface asks a turn
	// to take stock, 0 on a surface that never asks. It is the interval in
	// force rather than the one configured: a child's is its own, shorter,
	// because a child has none of what makes a session's long interval safe.
	CheckInInterval int `json:"check_in_interval,omitempty"`
	// AgentsRequireSandbox is agents.require_sandbox as it stood: whether a
	// writer's commands had to run contained, refused where nothing could
	// contain them.
	AgentsRequireSandbox bool `json:"agents_require_sandbox"`
	// ClassifierModel is the model auto mode's classifier asks, empty on a
	// surface that has none.
	ClassifierModel string `json:"classifier_model,omitempty"`
	// SandboxProfile is the containment profile in force, empty when
	// nothing contains the session's commands.
	SandboxProfile string `json:"sandbox_profile,omitempty"`
	// Item and Stage are the backlog item this session was working and the
	// step of it this session was, both empty on a session that was not a
	// stage of a run. They are not configuration and they are here for the
	// same reason the rest is: this is the set a window's sessions may be
	// grouped by, and "what did that item cost" and "which stage burns the
	// rounds" are the two questions a sprint's record exists to answer.
	//
	// They keep the allowlist's rule. A stage name is a word from the
	// profile's own closed set, and an item slug is the identifier a person
	// files work under — the same class of name as the saved conversation
	// the row already links to, and like that one it stays on this machine:
	// nothing here is exported (otel.go).
	Item  string `json:"item,omitempty"`
	Stage string `json:"stage,omitempty"`
	// ConfigHash fingerprints the whole effective config.
	ConfigHash string `json:"config_hash"`
}

func observeCutoff(since time.Time) string {
	return since.UTC().Format(observeTimeFormat)
}
