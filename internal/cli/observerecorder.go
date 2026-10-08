package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/logs"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/todo"
)

// observeRecorder writes one agent session's content-free events to storage.
// A nil recorder is a no-op, so callers can wire it unconditionally.
type observeRecorder struct {
	db     *storage.DB
	id     int64
	prices *pricing.Table
	model  string
	// kind and provider are what the row was opened with, kept so a session
	// boundary can open the next row the same way rather than being handed
	// the three of them again by a caller that would then be free to change
	// one (restart).
	kind, provider string
	// parent is the row this session hangs under where a driver named one,
	// kept for the same reason kind and provider are: a session boundary
	// opens the next row the same way, and one that lost the parenthood
	// would be the one row of a sprint the tree cannot reach.
	parent int64
	// linked is the saved conversation the row was last linked to, so an
	// autosave that lands in the same slot costs no write.
	linked string
	// quietTurn and quietTook are the turn whose longest quiet stretch was
	// last written and how long it was, so a turn paused at its cap and
	// then granted more rounds writes its stretch again only if it grew
	// (closeTurn).
	quietTurn int64
	quietTook time.Duration
	// outcome is the session outcome the last closing turn wrote, so the
	// end knows whether anything ever said how the session came out.
	outcome string
	// span is the row said out loud to a collector, or nil when no endpoint
	// is configured — which is the ordinary case. Every event the recorder
	// writes to the store is written to it as well, from the same arguments,
	// so the two records cannot disagree about what happened.
	span *observe.SessionSpan
}

// observeExport is the collector every session opened in this process
// reports to, or nil when the record stays on this machine. It is a value of
// the package rather than an argument because the four surfaces that open a
// recorder resolve their own model, provider and prices and have no reason
// to know about a collector — and the root, which reads the config, is the
// one place that does.
var observeExport *observe.Exporter

// setObserveExport is the root's half: the endpoint, read once from the
// config, turned into the exporter every recorder afterwards hangs its span
// on. An endpoint that will not parse costs one log record and leaves export
// off, which is the same answer a collector that will not answer gets — the
// record is a by-product of a session and never a reason for one to refuse
// to start.
// See docs/capabilities/sessions-and-memory.md#the-record-can-leave-this-machine.
func setObserveExport(endpoint string) {
	observeExport = nil
	if strings.TrimSpace(endpoint) == "" {
		return
	}
	exp, err := observe.NewExporter(context.Background(), endpoint, version)
	if err != nil {
		logs.Logger().Warn("session record not exported", "error", err)
		return
	}
	observeExport = exp
}

// observeShutdownTimeout bounds the close. It is the export timeout, because
// the two are the same round trip to the same collector, and it is bounded at
// all because this runs as the process is leaving: a collector that will not
// answer must not be able to hold the shell prompt.
const observeShutdownTimeout = 2 * time.Second

// closeObserveExport is the process's last word to the collector. Nothing is
// queued — a span is sent as its session ends — so what this closes is the
// connection, and it is deferred on the command tree's return (root.go).
func closeObserveExport() {
	if observeExport == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), observeShutdownTimeout)
	defer cancel()
	if err := observeExport.Shutdown(ctx); err != nil {
		logs.Logger().Warn("session record export not closed cleanly", "error", err)
	}
}

// parentSessionEnv names the record row this process's own session hangs
// under, for the driver whose children are processes rather than a
// supervisor's. A sprint is dozens of runs and every one of them is a stage
// of one piece of work: without the link the record holds forty-eight
// unrelated one-shot runs, and neither "what did that item cost" nor the
// retention prune's family sweep can see the tree.
//
// It is an environment variable because that is the only channel a driver
// that starts a process has to it, and it is read here rather than by each
// surface so a stage spent as a conversation and a stage spent as a coding
// run link the same way. A number that names no row is an unlinked session,
// which is what a stale value inherited by a shell would otherwise become.
// See docs/capabilities/sessions-and-memory.md#a-sprint-is-one-tree.
const parentSessionEnv = "SHHH_PARENT_SESSION"

// todoItemEnv and todoStageEnv are the backlog item a process was started to
// work and the step of it this process is. They ride beside the parent id
// because they answer the questions the link alone cannot: the tree says the
// stages belong together and these say which item they were for and which of
// them burned the rounds.
const (
	todoItemEnv  = "SHHH_TODO_ITEM"
	todoStageEnv = "SHHH_TODO_STAGE"
)

// parentSession is the row this process was told to hang its session under,
// or 0 where nothing told it. Anything that is not a positive number is
// nothing told it: the value crosses a process boundary as text, and a row
// id read out of a typo would file a session under a stranger's parent.
//
// A well-formed number can still name no row — a variable a shell kept from
// a run that is over, a store purged between the driver's row and this
// stage's — which is why the caller is prepared to open the row without it.
func parentSession() int64 {
	id, err := strconv.ParseInt(os.Getenv(parentSessionEnv), 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

// todoStageStamp is the item and stage this process was started for, for the
// surface filling the settings a row is stamped with.
func todoStageStamp() (item, stage string) {
	return os.Getenv(todoItemEnv), os.Getenv(todoStageEnv)
}

// startObserveRecorder opens a session row; any failure disables recording
// for the session rather than blocking it.
//
// A run started by a driver that passed its own row id opens the row as that
// row's child, so the whole sprint is one tree. The span is not linked with
// it: the parent is another process, and the parenthood a trace carries is a
// span context this one was never handed. The row and the span therefore
// disagree about the parenthood by construction here, which is the one place
// that is true and the reason the linked-in-process path takes a recorder
// rather than an id (startChildObserveRecorder).
func startObserveRecorder(db *storage.DB, kind, provider, model string, prices *pricing.Table) *observeRecorder {
	if db == nil {
		return nil
	}
	// Every start reconciles first: a session that was killed outright never
	// wrote its end time, and a row left open reads to the next session as
	// somebody still working in this checkout. Best effort and quiet, the way
	// sandbox ownership records are reaped — a store that will not answer is
	// never a reason to refuse to start
	// (docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone).
	_, _ = db.CloseCrashedAgentSessions()
	parent := parentSession()
	id, err := db.StartChildAgentSession(parent, kind, provider, model, "")
	if err != nil && parent > 0 {
		// The parent is a foreign key, so an id naming no row is refused
		// rather than stored. What fails there is the link and not the
		// record: an unlinked row still says what this run cost, and a
		// session with no record at all is the one reading nobody can get
		// back.
		parent = 0
		id, err = db.StartAgentSession(kind, provider, model)
	}
	if err != nil {
		return nil
	}
	return &observeRecorder{db: db, id: id, parent: parent, prices: prices, model: model, kind: kind, provider: provider,
		span: observeExport.Session(kind, provider, model)}
}

// startChildObserveRecorder opens a sub-agent's session row linked to its
// parent session; failures disable recording for that child only.
//
// It takes the parent's recorder rather than its row id because the link is
// made twice — once in the table, once in the trace — and a caller handed the
// two separately could link the row to one session and the span to another.
//
// name is the child's name as the supervisor knows it — a role and a counter,
// never the task, so the record stays content-free — written on the row so
// the tree the parent link builds reads back with the names the map drew.
func startChildObserveRecorder(db *storage.DB, kind, provider, model, name string, prices *pricing.Table, parent *observeRecorder) *observeRecorder {
	if db == nil {
		return nil
	}
	id, err := db.StartChildAgentSession(parent.sessionID(), kind, provider, model, name)
	if err != nil {
		return nil
	}
	return &observeRecorder{db: db, id: id, prices: prices, model: model, kind: kind, provider: provider,
		span: observeExport.Child(parent.sessionSpan(), kind, provider, model)}
}

// agentRows is the record row each agent of one session opened, kept by the
// name the supervisor knows the agent by, so that a child a child spawned is
// recorded under the agent that spawned it rather than under the session.
//
// The link is the record's copy of the one the breadcrumb, the esc-pop and
// the rail's map all read: a delegated child flattened onto the session reads
// back as one more of the session's own children, and nothing in the row says
// otherwise, so a fan-out of three that delegated twice can never be told
// afterwards from a fan-out of five
// (docs/capabilities/subagents.md#a-child-may-delegate-to-a-configured-depth).
// The rows are written from the supervisor's own goroutines, one per agent,
// so both halves take the lock.
type agentRows struct {
	mu   sync.Mutex
	rows map[string]*observeRecorder
}

// under is the recorder a child spawned by parent hangs its own row off:
// parent's own row where that agent recorded one, and the session's
// otherwise. An agent that could not open a row must not cost its
// descendants the whole lineage, which is what an unlinked row would.
func (a *agentRows) under(parent string, session *observeRecorder) *observeRecorder {
	if parent == "" {
		return session
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if r, ok := a.rows[parent]; ok && r != nil {
		return r
	}
	return session
}

// keep records the row an agent is running on. A retry replaces it, because
// what a further delegation belongs under is the attempt that is running and
// not the one it replaced.
func (a *agentRows) keep(name string, r *observeRecorder) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rows == nil {
		a.rows = map[string]*observeRecorder{}
	}
	a.rows[name] = r
}

// sessionID is the recorder's session row id (0 when recording is disabled),
// used to link child sessions to their parent.
func (r *observeRecorder) sessionID() int64 {
	if r == nil {
		return 0
	}
	return r.id
}

// sessionSpan is the recorder's exported span, or nil where nothing is being
// exported — which is the ordinary case, and the reason this exists rather
// than the field being read directly: the caller that wants it is opening a
// child, and a parent that never recorded at all is one of the ways it gets
// none.
func (r *observeRecorder) sessionSpan() *observe.SessionSpan {
	if r == nil {
		return nil
	}
	return r.span
}

// stamp records what the session ran under: the build, a fingerprint of the
// system prompt as sent, how many skills loaded, a fingerprint of the
// checkout, and the settings it was configured with. Fingerprints rather
// than the things themselves: the prompt carries the project context and the
// path names the machine, and neither belongs in a table that is
// content-free by construction. A hash still splits "before the edit" from
// "after it", which is all a comparison needs.
func (r *observeRecorder) stamp(sysPrompt string, skills int, root string, settings storage.AgentSettings) {
	if r == nil {
		return
	}
	_ = r.db.StampAgentSession(r.id, storage.AgentProvenance{
		Version:    version,
		PromptHash: fingerprint(sysPrompt),
		Skills:     skills,
		Project:    fingerprint(root),
		Settings:   settings,
	})
}

// runSettings are the values a surface resolved for itself before it opened
// its row — the ones the config file alone cannot answer, because a flag, a
// profile, a clamp to a parent or a mechanism check had the last word.
type runSettings struct {
	// mode is the permission mode the surface starts in, or empty where no
	// permission mode applies: a one-shot has no policy to be in a mode of,
	// and a headless run answers with --yes and --allow instead.
	mode string
	// effort is the reasoning level the surface starts at.
	effort provider.Effort
	// rounds is the per-turn tool-round cap in force, 0 for none; a
	// surface that resolves its cap through maxRoundsFor spells it with
	// roundCapFor first.
	rounds int
	// sandbox is the containment profile in force, or empty when nothing
	// contains the surface's commands — an unconfined session runs under
	// no profile, whatever the config asked for.
	sandbox string
	// item and stage are the backlog item this session was started to work
	// and the step of it this session is, both empty on a session that is
	// not a stage of a run. They are read off the environment by the surface
	// rather than here so this stays a function of the config and the flags.
	item, stage string
	// model is what the summariser and the classifier fall back to when
	// their own keys are unset — auxiliaryModel's answer: provider.cheap_model,
	// the provider's small model, else the session's own. It is resolved by
	// the surface rather than here so the record states the model that was
	// actually asked.
	model string
	// checkIn is how many rounds pass before this surface asks a turn to
	// take stock, and 0 where it never asks. It is the surface's own answer
	// rather than the config's: a child's interval is shorter than a
	// session's whatever the config says, because a child has none of what
	// makes a session's long interval safe.
	checkIn int
	// summary and classifier say whether each mechanism exists on this
	// surface at all. A one-shot takes no readings and asks no classifier,
	// and recording the model it would have used is recording a setting
	// that was not in force.
	summary, classifier bool
}

// sessionSettings is the allowlist: every config value the record keeps
// whole, resolved to what was actually in force, and the hash over the rest.
//
// Nothing here is a path, a command or a secret. The scope directories, the
// sandbox's deny and write lists, the command allowlists and the API keys
// reach the store only through the hash. A config value joins the stamped
// set by being read here — or, for the sandbox profile, by the surface that
// parsed it through the profile's closed set before filling runSettings —
// and nowhere else, so a field added to the config is excluded until someone
// decides otherwise. The test beside this fills every config field with a
// marker and checks that none of them comes through except the ones named
// here.
// See docs/capabilities/sessions-and-memory.md#what-a-session-ran-under.
func sessionSettings(cfg config.Config, run runSettings) storage.AgentSettings {
	out := storage.AgentSettings{
		Mode:            run.mode,
		Reasoning:       run.effort.String(),
		MaxRounds:       run.rounds,
		CheckInInterval: run.checkIn,
		SummaryEnabled:  run.summary,
		SandboxProfile:  run.sandbox,
		Item:            run.item,
		Stage:           run.stage,
		ConfigHash:      configHash(cfg),
		// Whether a writer's commands had to be contained is a switch, not
		// a path or a command, and it decides whether a writer on a host
		// with no mechanism runs anything at all — a cohort worth telling
		// apart (docs/capabilities/containment.md#containment-can-be-required).
		AgentsRequireSandbox: cfg.AgentsRequireSandboxEnabled(),
	}
	if run.summary {
		out.SummaryModel = modelOr(cfg.Summary.Model, run.model)
		out.SummaryInterval = agent.SummaryConfig{IntervalRounds: cfg.Summary.IntervalRounds}.Interval()
	}
	if run.classifier {
		out.ClassifierModel = modelOr(cfg.Behavior.ClassifierModel, run.model)
	}
	return out
}

// checkInFor is the interval a surface that asks the question at all runs
// under: what the config named, or the built-in one. It is spelled here
// rather than read off the agent because the record is stamped before a turn
// has been taken, and the agent's own reading of it widens as a turn goes on
// — the number this records is the one somebody set.
func checkInFor(rounds int) int {
	if rounds <= 0 {
		return agent.DefaultCheckInInterval
	}
	return rounds
}

// roundCapFor turns maxRoundsFor's three-way answer into the cap in force as
// the record spells it: the number, or 0 for none.
func roundCapFor(rounds int) int {
	switch {
	case rounds < 0:
		return 0
	case rounds == 0:
		return agent.DefaultMaxToolRounds
	default:
		return rounds
	}
}

// configHash fingerprints the whole effective config, secrets and paths
// included, so a change to any field splits sessions on either side of it
// even when the field is one the allowlist does not keep. Going through a
// hash is what makes that safe: nothing in the config is recoverable from
// twelve hex digits of a digest over the whole document.
//
// The encoding is JSON rather than TOML for its determinism: struct fields
// go out in declaration order and map keys sorted, so the same config hashes
// the same on every run. Marshalling a config cannot fail — every field is a
// scalar, a slice, a map of strings or a pointer to one — so an error here
// would be a new field of a shape the encoder refuses, and it is reported
// as an empty hash rather than hidden, because an empty hash is the one
// value the store reads as "no settings were taken".
func configHash(cfg config.Config) string {
	data, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	return fingerprint(string(data))
}

// projectFingerprintRoot is the checkout the session runs in — its
// repository root when there is one, else the working directory — as the
// string the project fingerprint hashes.
func projectFingerprintRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return todo.Root(cwd)
}

// liveSibling reports another session already open in this checkout, and
// when it started. One reading answers for every surface that says so — the
// start screen's fact, the workspace block's line, the tree reading's last
// clause — because three readings of the same question are three chances to
// disagree about whether anybody else is here.
//
// A store that will not answer costs the fact and nothing else: two sessions
// in one checkout is a decision the person made, and nothing here refuses to
// start over it.
// See docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone.
func liveSibling(db *storage.DB) (time.Time, bool) {
	if db == nil {
		return time.Time{}, false
	}
	sib, ok, err := db.LiveSibling(fingerprint(projectFingerprintRoot()), time.Now())
	if err != nil || !ok {
		return time.Time{}, false
	}
	return sib.Since, true
}

// fingerprint is a short stable hash of a string, or empty for empty input.
func fingerprint(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}

// observer adapts the recorder to the observer contract every surface
// reports a session through.
func (r *observeRecorder) observer() observe.Observer {
	if r == nil {
		return observe.Observer{}
	}
	return observe.Observer{
		Usage:     r.usagePriced,
		ToolCall:  r.toolCallAt,
		Decision:  r.decisionAt,
		Turn:      r.turn,
		TurnTimed: r.turnTimed,
		Signal:    r.signal,
		Gate:      r.gate,
		Session:   r.link,
	}
}

// usage records a session's running totals, pricing them against the model
// the session was opened on. It is the fallback for a run nothing priced as
// it went, and it charges the whole input at the fresh rate — a bare pair
// carries no cache split, so every token the provider served from its prompt
// cache is billed here as if it had been read fresh. That is several times
// the real figure on anything whose prompt prefix is re-sent each round, so
// every caller that has the split passes a priced cost instead
// (docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once).
func (r *observeRecorder) usage(turns, tokensIn, tokensOut int64) {
	if r == nil {
		return
	}
	var cost float64
	if r.prices != nil {
		if in, out, found := r.prices.Cost(r.model, tokensIn, tokensOut); found {
			cost = in + out
		}
	}
	r.span.Usage(turns, tokensIn, tokensOut, cost)
	_ = r.db.UpdateAgentSession(r.id, turns, tokensIn, tokensOut, cost)
}

// usagePriced records totals that arrive already priced, which is what both a
// parent session and a child report. A parent's spend is a mixture — several
// models, the classifier and the summary among them — and only the ledger
// that billed each request knows what rate each one went out at; a child runs
// on one model but re-sends its prompt every round, so what it owes turns on
// how much of each request the provider served from its cache, and that is
// gone by the time the totals are a sum. Either way the split survives only
// where the request arrived, so it is priced there and the cost travels with
// the tokens.
func (r *observeRecorder) usagePriced(turns, tokensIn, tokensOut int64, cost float64, priced bool) {
	if r == nil {
		return
	}
	if !priced {
		r.usage(turns, tokensIn, tokensOut)
		return
	}
	r.span.Usage(turns, tokensIn, tokensOut, cost)
	_ = r.db.UpdateAgentSession(r.id, turns, tokensIn, tokensOut, cost)
}

// toolCallAt records one call. The purpose goes to the table and not to the
// span: the word is the dashboard's reading of the local record, and the
// export's attribute set is closed on its own terms (otel.go).
func (r *observeRecorder) toolCallAt(at observe.Pos, tool string, duration time.Duration, outcome, class, purpose string) {
	if r == nil {
		return
	}
	r.span.ToolCall(at, tool, duration, outcome, class)
	ms := duration.Milliseconds()
	_ = r.db.RecordAgentEvent(r.id, storage.AgentEvent{
		Kind: storage.AgentEventTool, Tool: tool, DurationMs: &ms, Outcome: outcome, Reason: class,
		Turn: at.Turn, Round: at.Round, Purpose: purpose,
	})
}

func (r *observeRecorder) decisionAt(at observe.Pos, decision, reason string) {
	if r == nil {
		return
	}
	r.span.Decision(at, decision, reason)
	_ = r.db.RecordAgentEvent(r.id, storage.AgentEvent{
		Kind: storage.AgentEventDecision, Outcome: decision, Reason: reason, Turn: at.Turn, Round: at.Round,
	})
}

// turn records a turn closing: the rounds it took ride in the event's
// round column, its wall time in the duration. The turn also says how the
// session has come out so far, which is written now rather than at the exit
// because the session that most needs an outcome is the one whose exit never
// runs (docs/capabilities/sessions-and-memory.md#whether-it-worked).
func (r *observeRecorder) turn(turn, rounds int64, duration time.Duration, outcome string) {
	r.closeTurn(turn, rounds, duration, outcome, nil)
}

// turnTimed is turn from a surface that split the turn's time: the four
// waits ride the turn row, in milliseconds that add up to its duration, and
// the turn's longest quiet stretch is a row of its own. A pause at the round
// cap writes the split it has so far and no stretch, because the turn is not
// over: the stretch is written once, when it is
// (docs/capabilities/sessions-and-memory.md#startup-and-waits-are-timed).
func (r *observeRecorder) turnTimed(turn, rounds int64, duration time.Duration, outcome string, split agent.TurnSplit) {
	r.closeTurn(turn, rounds, duration, outcome, &split)
}

// startupRow writes one startup phase. A server's row carries its name in the
// tool column, where an MCP tool's name already sits, and its outcome word;
// nothing the server said is among them. The span takes none of these: the
// export's attribute set is closed on its own terms (otel.go).
func (r *observeRecorder) startupRow(row observe.StartupRow) {
	if r == nil {
		return
	}
	ms := row.Took.Milliseconds()
	_ = r.db.RecordAgentEvent(r.id, storage.AgentEvent{
		Kind: storage.AgentEventStartup, Tool: row.Name, Outcome: row.Outcome, Reason: row.Phase, DurationMs: &ms,
	})
}

func (r *observeRecorder) closeTurn(turn, rounds int64, duration time.Duration, outcome string, split *agent.TurnSplit) {
	if r == nil {
		return
	}
	r.span.Turn(turn, rounds, duration, outcome)
	// The heartbeat rides the turn close rather than being called from each
	// front-end's own boundary: this callback is the one every surface
	// already reports a finished turn through, so there is one site to keep
	// pointing at the row the recorder holds now — which a new conversation
	// inside the same process replaces.
	_ = r.db.BeatAgentSession(r.id)
	ms := duration.Milliseconds()
	ev := storage.AgentEvent{
		Kind: storage.AgentEventTurn, Outcome: outcome, DurationMs: &ms, Turn: turn, Round: rounds,
	}
	if split != nil {
		parts := observe.TurnMillis(duration, *split)
		ev.ModelFirstMs, ev.ModelStreamMs, ev.ToolMs, ev.PersonMs = &parts[0], &parts[1], &parts[2], &parts[3]
	}
	_ = r.db.RecordAgentEvent(r.id, ev)
	// The stretch is written at a pause as well as at the close, because a
	// paused turn the person never grants more rounds to has no close. A
	// turn that is granted them reports its whole span again at its end, so
	// the close writes a stretch only where one longer than the pause's came
	// after it: one row per turn, unless the turn's longest moved.
	if split != nil && split.Quiet.Took > 0 && (r.quietTurn != turn || split.Quiet.Took > r.quietTook) {
		q := split.Quiet
		r.quietTurn, r.quietTook = turn, q.Took
		took, delivered := q.Took.Milliseconds(), int64(q.Delivered)
		_ = r.db.RecordAgentEvent(r.id, storage.AgentEvent{
			Kind: storage.AgentEventQuiet, Outcome: observe.StretchWord(q), Reason: observe.WaitWord(q.On),
			DurationMs: &took, Delivered: &delivered, Turn: turn, Round: rounds,
		})
	}
	if o := observe.SessionOutcome(outcome); o != "" {
		if err := r.db.SetAgentSessionOutcome(r.id, o); err == nil {
			r.outcome = o
		}
	}
}

func (r *observeRecorder) signal(at observe.Pos, code, reason string) {
	if r == nil {
		return
	}
	r.span.Signal(at, code, reason)
	_ = r.db.RecordAgentEvent(r.id, storage.AgentEvent{
		Kind: storage.AgentEventSignal, Outcome: code, Reason: reason, Turn: at.Turn, Round: at.Round,
	})
}

// gate records one quality-gate run. The suite rides in the event's tool
// column — it is what the verdict is a verdict of, the way a tool event's
// tool is what the outcome is an outcome of — and the verdict is the
// signal's qualifier, which is what a pass rate groups by.
//
// It carries no position, because a gate run has none: /gate run starts one
// in the background between turns, and a turn and a round would be real for
// the runs the model asked for and invented for the rest. The zero position
// is what the store already reads as "the recorder had no position".
func (r *observeRecorder) gate(suite, verdict string) {
	if r == nil {
		return
	}
	r.span.Gate(suite, verdict)
	_ = r.db.RecordAgentEvent(r.id, storage.AgentEvent{
		Kind: storage.AgentEventSignal, Tool: suite, Outcome: observe.SignalGate, Reason: verdict,
	})
}

// link names the saved conversation the session is writing. It is the one
// callback with no exported half: the name is the join from the record to
// what was actually said, and putting the two side by side is a deliberate
// act taken at the export command rather than a thing that happens to every
// collector on the network
// (docs/capabilities/sessions-and-memory.md#observations-are-what-the-session-did).
func (r *observeRecorder) link(name string) {
	if r == nil || name == "" || name == r.linked {
		return
	}
	// The name is remembered only once the reference behind it resolved, so a
	// link taken before the slot's row existed is asked for again at the next
	// save rather than leaving the record joined to the conversation by a
	// name and nothing else
	// (docs/capabilities/sessions-and-memory.md#a-round-can-be-read-back).
	if resolved, err := r.db.LinkAgentSession(r.id, name); err == nil && resolved {
		r.linked = name
	}
}

// end closes the session row, correcting the standing outcome only when
// there is none to stand. A session that reached its own exit having never
// closed a turn is abandoned: the process survived to say something, and
// what it says is that nothing was finished. Leaving the field empty is
// reserved for the session that never got to say anything at all, which
// reads as unknown — a different fact, and one about the record rather than
// about the work.
func (r *observeRecorder) end() {
	if r == nil {
		return
	}
	outcome := ""
	if r.outcome == "" {
		outcome = observe.SessionAbandoned
	}
	_ = r.db.EndAgentSession(r.id, outcome)
	// Cleared after the send because a span is sent when it ends, so there
	// is nothing left to say through this one.
	r.span.End(r.closingOutcome())
	r.span = nil
}

// endChild closes a sub-agent's row the way end closes a session's, and
// records how the attempt came out beside it. It is the child's own row and
// not the parent's, because what the end says — a budget spent, a kill, the
// steers it took — is only answerable beside the spend and the model on that
// same row (docs/capabilities/sessions-and-memory.md#a-child-ends-for-a-reason).
func (r *observeRecorder) endChild(e observe.ChildEnd) {
	if r == nil {
		return
	}
	outcome := ""
	if r.outcome == "" {
		outcome = observe.SessionAbandoned
	}
	_ = r.db.EndChildAgentSession(r.id, outcome, e)
	r.span.End(r.closingOutcome())
	r.span = nil
}

// closingOutcome is the outcome a row closed without a surface naming one
// settles on: the standing reading where a turn wrote one, the abandonment
// where none did.
func (r *observeRecorder) closingOutcome() string {
	if r.outcome != "" {
		return r.outcome
	}
	return observe.SessionAbandoned
}

// endWith closes the row with an outcome the surface knows better than its
// turns do. A session whose program failed did not come out the way its last
// turn did, and the exit that reports the failure leaves no deferred close
// to run — so without this the row would keep the last turn's reading and a
// crashed session would be indistinguishable from one that finished well.
func (r *observeRecorder) endWith(outcome string) {
	if r == nil {
		return
	}
	r.outcome = outcome
	_ = r.db.EndAgentSession(r.id, outcome)
	r.span.End(outcome)
	r.span = nil
}

// restart closes this row and opens another for the conversation that
// follows it, reporting whether it could. The recorder itself is not
// replaced: every surface holding its observer goes on reporting through the
// same callbacks, which is what keeps "which row does this event belong to"
// a question with one answer rather than one per front-end.
//
// A row that cannot be opened leaves the recorder on the row it just closed.
// The alternative is a recorder that silently writes nothing for the rest of
// the process, and a session whose events land on a closed row is a smaller
// wrong than a session with no record at all.
func (r *observeRecorder) restart() bool {
	if r == nil {
		return false
	}
	// The closing conversation's span goes out on a goroutine of its own,
	// and this is the one place that is true. A session boundary is answered
	// inside the update loop — `/new`, the pressure card's recovery, a run
	// that starts a fresh conversation — so a collector that is slow rather
	// than absent would hold the screen still for as long as an export is
	// allowed to take. The process exit keeps the synchronous send, because
	// there a span nobody waits for is a span the process outlives.
	if closing := r.span; closing != nil {
		outcome := r.closingOutcome()
		r.span = nil
		go closing.End(outcome)
	}
	r.end()
	// The second row hangs where the first one did. A boundary crossed
	// inside a stage is still that stage of that run, and a row that lost
	// the link would be the one session of the sprint the tree cannot reach.
	//
	// It falls back the way the first row did, and for a reason the first
	// row does not have: the parent was there when this session started and
	// a sweep can have taken it since. A boundary that lost the record
	// altogether is worse than one that lost the link.
	id, err := r.db.StartChildAgentSession(r.parent, r.kind, r.provider, r.model, "")
	if err != nil && r.parent > 0 {
		r.parent = 0
		id, err = r.db.StartAgentSession(r.kind, r.provider, r.model)
	}
	if err != nil {
		return false
	}
	r.id, r.linked, r.outcome = id, "", ""
	r.span = observeExport.Session(r.kind, r.provider, r.model)
	return true
}
