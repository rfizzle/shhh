package storage

import (
	"database/sql"
	"os"
	"time"

	"github.com/rfizzle/shhh/internal/observe"
)

// StartAgentSession opens a session row and returns its id. kind is the entry
// point ("chat", "code", "print").
//
// The row is stamped with this process's id and a first heartbeat, which is
// what later lets another session tell a sitting that is still going from
// one that was killed with its end time never written
// (docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone).
func (db *DB) StartAgentSession(kind, provider, model string) (int64, error) {
	res, err := db.sql.Exec(
		`INSERT INTO agent_sessions (kind, provider, model, pid, heartbeat)
		 VALUES (?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
		kind, provider, model, os.Getpid(),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// StartChildAgentSession opens a session row linked to a parent session, so
// sub-agent spend is attributable. A non-positive parentID records an
// unlinked session. name is the agent's name as the supervisor knows it
// (`writer-3`, `reader-3a`), which is what lets a fan-out's rows be read back
// as the tree the map drew; an empty one is stored as NULL, which is what
// every row that is not a child's holds.
func (db *DB) StartChildAgentSession(parentID int64, kind, provider, model, name string) (int64, error) {
	var parent, named any
	if parentID > 0 {
		parent = parentID
	}
	if name != "" {
		named = name
	}
	res, err := db.sql.Exec(
		`INSERT INTO agent_sessions (kind, provider, model, parent_id, name, pid, heartbeat)
		 VALUES (?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
		kind, provider, model, parent, named, os.Getpid(),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// StampAgentSession records a session's provenance and its settings in one
// statement, so a row is never left with one half and not the other. A stamp
// that carries no settings writes NULL to every settings column, which is
// what an unstamped row holds and what the reader takes for "none".
func (db *DB) StampAgentSession(id int64, p AgentProvenance) error {
	c := p.Settings
	settings := []any{nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil}
	if c.ConfigHash != "" {
		settings = []any{c.Mode, c.Reasoning, c.MaxRounds, c.SummaryModel, c.SummaryInterval, c.SummaryEnabled,
			c.ClassifierModel, c.SandboxProfile, c.Item, c.Stage, c.ConfigHash, c.CheckInInterval,
			c.AgentsRequireSandbox, c.ClassifierBackend}
	}
	args := append([]any{p.Version, p.PromptHash, p.Skills, p.Project}, settings...)
	_, err := db.sql.Exec(
		`UPDATE agent_sessions SET version = ?, prompt_hash = ?, skills = ?, project = ?,
		        mode = ?, reasoning = ?, max_rounds = ?,
		        summary_model = ?, summary_interval = ?, summary_enabled = ?,
		        classifier_model = ?, sandbox_profile = ?, item = ?, stage = ?, config_hash = ?,
		        check_in_interval = ?, agents_require_sandbox = ?, classifier_backend = ?
		 WHERE id = ?`,
		append(args, id)...,
	)
	return err
}

// LinkAgentSession names the saved conversation this session is the metadata
// of, so the two can be joined when someone deliberately wants to read what
// a session said. The name is a timestamp or a name the user chose, not
// content.
//
// The row's own reference is written beside the name, and it is what every
// join uses. The name is what a person types and what the export has always
// carried; it is not an identity — a renamed conversation, or a session moved
// to a fresh slot because another process took the one it was in, leaves the
// name pointing at nothing.
//
// The first result says whether the reference resolved. A slot is claimed
// before a session writes to it, so it almost always does; a caller that
// links a name no row carries yet is told so rather than left holding a link
// that will never join, and can ask again at the next save.
// See docs/capabilities/sessions-and-memory.md#a-round-can-be-read-back.
func (db *DB) LinkAgentSession(id int64, chatSession string) (bool, error) {
	if _, err := db.sql.Exec(
		`UPDATE agent_sessions SET chat_session = ?,
		        chat_session_id = (SELECT c.id FROM chat_sessions c WHERE c.name = ?)
		 WHERE id = ?`,
		chatSession, chatSession, id,
	); err != nil {
		return false, err
	}
	var resolved sql.NullInt64
	if err := db.sql.QueryRow(`SELECT chat_session_id FROM agent_sessions WHERE id = ?`, id).
		Scan(&resolved); err != nil {
		return false, err
	}
	return resolved.Valid, nil
}

// UpdateAgentSession sets a session's cumulative totals (idempotent: callers
// pass running totals, not deltas).
//
// It beats the row as well. Totals arrive with every request the provider
// answered, which is the one signal every surface already sends while a turn
// is under way — the turn close alone would leave a session forty rounds into
// a turn reading as one nobody has touched since the last one ended, and
// `shhh sessions` tells working from idle by exactly this column
// (docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone).
func (db *DB) UpdateAgentSession(id, turns, tokensIn, tokensOut int64, estCost float64) error {
	_, err := db.sql.Exec(
		`UPDATE agent_sessions SET turns = ?, tokens_in = ?, tokens_out = ?, est_cost = ?,
		        heartbeat = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		 WHERE id = ?`,
		turns, tokensIn, tokensOut, estCost, id,
	)
	return err
}

// SetAgentSessionOutcome records how the session came out so far. It is
// written at every turn close and overwritten by the next one, because the
// session the record most needs an outcome for is the one whose exit path
// never runs: a run the user gave up on and killed writes nothing on the way
// out, and an outcome stamped only at the end would describe the sessions
// that ended well and say nothing about the rest. The optimistic write
// leaves the last turn's reading standing instead.
// See docs/capabilities/sessions-and-memory.md#whether-it-worked.
func (db *DB) SetAgentSessionOutcome(id int64, outcome string) error {
	_, err := db.sql.Exec(`UPDATE agent_sessions SET outcome = ? WHERE id = ?`, outcome, id)
	return err
}

// EndAgentSession stamps the session's end time, and its outcome when the
// caller has one to correct the standing reading with. An empty outcome
// leaves whatever the turns wrote alone, which is the ordinary case: a
// session that finished a turn has already said how it came out.
func (db *DB) EndAgentSession(id int64, outcome string) error {
	if outcome == "" {
		_, err := db.sql.Exec(
			`UPDATE agent_sessions SET ended_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`, id,
		)
		return err
	}
	_, err := db.sql.Exec(
		`UPDATE agent_sessions SET ended_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'), outcome = ? WHERE id = ?`,
		outcome, id,
	)
	return err
}

// EndChildAgentSession closes a sub-agent's attempt: the end time and the
// outcome EndAgentSession writes, and beside them how the attempt ended,
// what the last reading made of its work, how many steers it was given and
// which attempt the row is.
//
// It is a second closing statement rather than four more arguments to the
// first because only a child has any of these, and a session that ends
// having never spawned anything must not write four NULLs it would then be
// read as having answered.
// See docs/capabilities/sessions-and-memory.md#a-child-ends-for-a-reason.
func (db *DB) EndChildAgentSession(id int64, outcome string, e observe.ChildEnd) error {
	set := `ended_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'),
	        end_reason = ?, verdict = ?, steers = ?, attempt = ?,
	        child_budget = ?, child_admission_floor = ?,
	        child_tokens_inherited = ?, child_tokens_setup = ?,
	        child_tokens_tools = ?, child_tokens_analysis = ?, child_tokens_handoff = ?,
	        child_tokens_fresh = ?`
	args := []any{e.Reason, e.Verdict, e.Steers, e.Attempt, e.Budget, e.AdmissionFloor,
		e.Tokens.Inherited, e.Tokens.Setup, e.Tokens.Tools, e.Tokens.Analysis, e.Tokens.Handoff,
		e.Tokens.Fresh}
	if outcome != "" {
		set += `, outcome = ?`
		args = append(args, outcome)
	}
	_, err := db.sql.Exec(`UPDATE agent_sessions SET `+set+` WHERE id = ?`, append(args, id)...)
	return err
}

// BeatAgentSession says the session is still there. It is called at every
// turn boundary, which is the coarsest beat that still tracks a person
// working: a session between turns is a session somebody is reading.
//
// It writes to whichever row the recorder holds now, so a conversation that
// ended and opened another inside one process keeps the beat on the row that
// is actually open rather than on the one it left behind.
//
// It is written under the busy retry (storage.go) for a reason the other two
// steady-state writes share: a beat nobody made is a session another process
// reads as gone, and a lock refused for a hundredth of a second is not the
// session ending.
func (db *DB) BeatAgentSession(id int64) error {
	return db.execRetry(
		`UPDATE agent_sessions SET heartbeat = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`, id,
	)
}

// LiveSession is another sitting open in this checkout right now, described
// by the only thing a notice needs: when it started.
type LiveSession struct {
	Since time.Time
}

// LiveSibling reports another session open in the checkout that project
// fingerprints, and when it started. The oldest one wins, because the fact
// being reported is that somebody else is here and not how many.
//
// A sibling is another *process*: a row this process opened — the one it is
// in, or the one a new conversation left behind — is never its own sibling,
// and neither is a sub-agent, whose row hangs off a parent and shares its
// id. What is left is a row with the same fingerprint, no end time, a
// heartbeat inside the window and a process that answers.
// See docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone.
func (db *DB) LiveSibling(project string, now time.Time) (LiveSession, bool, error) {
	if project == "" {
		// No fingerprint is not "every checkout" — it is "we do not know
		// which checkout", and matching on it would report a session in
		// somebody else's directory as a sibling here.
		return LiveSession{}, false, nil
	}
	open, err := queryRows(db, scanFields(func(o *pidRow) []any { return []any{&o.key, &o.pid} }),
		`SELECT started_at, pid FROM agent_sessions
		 WHERE project = ? AND ended_at IS NULL AND parent_id IS NULL
		   AND pid > 0 AND pid != ? AND heartbeat >= ?
		 ORDER BY started_at`,
		project, os.Getpid(), heartbeatCutoff(now),
	)
	if err != nil {
		return LiveSession{}, false, err
	}
	for _, o := range open {
		if !pidRunning(o.pid) {
			continue
		}
		since, _ := time.Parse(observeTimeFormat, o.key)
		return LiveSession{Since: since}, true, nil
	}
	return LiveSession{}, false, nil
}

// pidRow is an open row's process and the one column a reader keys it by.
type pidRow struct {
	key string
	pid int
}

// agentWorkingWindow is how recently a session must have beaten to be read
// as working rather than idle. A beat is taken at every answered request and
// every turn close, so a turn that is making progress beats every few
// seconds; five minutes leaves room for one long tool call — a build, a test
// suite — without calling the session idle in the middle of it.
const agentWorkingWindow = 5 * time.Minute

// RunningSession is one session a person has open on this machine, as
// `shhh sessions` lists it: a conversation or a coding session with no
// parent, whose process answers and whose beat is inside the window.
// See docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone.
type RunningSession struct {
	ID   int64
	Kind string
	PID  int
	// Slot is the saved conversation the session is writing, empty until
	// its first save links one.
	Slot string
	// Root is the checkout the slot was last written down in, read from the
	// slot rather than the record, which stores no paths. Empty where the
	// session has not saved yet, or its slot predates the column.
	Root string
	// Project is the row's checkout fingerprint, which is what a caller that
	// knows its own checkout can name an unsaved session's directory by.
	Project string
	Started time.Time
	Beat    time.Time
	// Working is a beat inside agentWorkingWindow, the session's own or one
	// of its children's; anything older is idle.
	Working bool
	// Own is this process's row.
	Own bool
	// Children are the open rows hanging under this one at any depth — its
	// sub-agents and the unattended runs it started — in the order they
	// started. None of them is a session a person can open, so none is
	// listed as a row of its own.
	Children []RunningChild
}

// RunningChild is a sub-agent or a headless run under a running session.
type RunningChild struct {
	// Name is the agent's name as the supervisor knows it, empty for a row
	// that carries none (a headless run), which a listing names by Kind.
	Name    string
	Kind    string
	Started time.Time
	Working bool
}

// LiveSessions lists the sessions running on this machine, oldest first,
// read the way LiveSibling reads one: an open row, a beat inside the
// heartbeat window, and a process that answers. Only a chat or a code row
// with no parent is a session of its own; every other open row is listed
// under the top-level row it descends from, and one that descends from none
// of them — an unattended run nobody opened a session around — is left out,
// because nobody can open it.
// See docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone.
func (db *DB) LiveSessions(now time.Time) ([]RunningSession, error) {
	type openRow struct {
		RunningSession
		parent        int64
		name          string
		started, beat string
	}
	open, err := queryRows(db, scanFields(func(o *openRow) []any {
		return []any{&o.ID, &o.Kind, &o.PID, &o.Slot, &o.Root, &o.Project,
			&o.started, &o.beat, &o.parent, &o.name}
	}), `SELECT a.id, a.kind, a.pid, a.chat_session, COALESCE(c.root, ''), a.project,
		        a.started_at, a.heartbeat, COALESCE(a.parent_id, 0), COALESCE(a.name, '')
		 FROM agent_sessions a LEFT JOIN chat_sessions c ON c.id = a.chat_session_id
		 WHERE a.ended_at IS NULL AND a.pid > 0 AND a.heartbeat >= ?
		 ORDER BY a.started_at, a.id`,
		heartbeatCutoff(now),
	)
	if err != nil {
		return nil, err
	}
	var all []openRow
	for _, o := range open {
		if !pidRunning(o.PID) {
			continue
		}
		o.Started, _ = time.Parse(observeTimeFormat, o.started)
		o.Beat, _ = time.Parse(observeTimeFormat, o.beat)
		// The first beat is written with the row, so a beat that is still
		// the start time is a session that has not been answered once yet —
		// sitting at its start screen, not working.
		o.Working = o.beat != o.started && now.Sub(o.Beat) < agentWorkingWindow
		o.Own = o.PID == os.Getpid()
		all = append(all, o)
	}
	parentOf := make(map[int64]int64, len(all))
	for _, o := range all {
		parentOf[o.ID] = o.parent
	}
	// top walks a row up to the top-level row it hangs under, or answers 0
	// where the chain leaves the open rows (a parent that ended) or loops,
	// which the schema does not rule out.
	top := func(id int64) int64 {
		seen := map[int64]bool{}
		for !seen[id] {
			seen[id] = true
			up := parentOf[id]
			if up == 0 {
				return id
			}
			if _, ok := parentOf[up]; !ok {
				return 0
			}
			id = up
		}
		return 0
	}
	var out []RunningSession
	index := map[int64]int{}
	for _, o := range all {
		if o.parent == 0 && (o.Kind == "chat" || o.Kind == "code") {
			index[o.ID] = len(out)
			out = append(out, o.RunningSession)
		}
	}
	for _, o := range all {
		if _, isTop := index[o.ID]; isTop {
			continue
		}
		i, ok := index[top(o.ID)]
		if !ok {
			continue
		}
		out[i].Children = append(out[i].Children, RunningChild{
			Name: o.name, Kind: o.Kind, Started: o.Started, Working: o.Working})
		// A session waiting on its children is not idle: the work is
		// theirs, and their beats are what say it is going on.
		if o.Working {
			out[i].Working = true
		}
	}
	return out, nil
}

// LiveSessionPID is the process running the session that is writing slot,
// found the way LiveSessions finds it. It is the one lookup from a name a
// person types to a process on this machine, so anything that has to reach
// a running session by its slot asks here.
func (db *DB) LiveSessionPID(slot string, now time.Time) (int, bool, error) {
	if slot == "" {
		return 0, false, nil
	}
	sessions, err := db.LiveSessions(now)
	if err != nil {
		return 0, false, err
	}
	for _, s := range sessions {
		if s.Slot == slot {
			return s.PID, true, nil
		}
	}
	return 0, false, nil
}

// CloseCrashedAgentSessions ends every open row whose process is gone and
// returns how many it closed, the way sandbox ownership records are
// reconciled against the engine at startup: the record outlives the thing it
// describes, so something has to bring the two back in line.
//
// It leaves the outcome alone. A killed session's last turn already said how
// the work was going, and a row that never closed a turn reads as unknown,
// which is the honest answer about a process nobody heard from again.
//
// Rows with no recorded id are left open. They were written by a build that
// recorded none, and closing a row because it cannot vouch for itself would
// rewrite history the reader can still see.
func (db *DB) CloseCrashedAgentSessions() (int, error) {
	type openRow struct {
		id  int64
		pid int
	}
	// The rows are read out before anything is written: the store runs on
	// one connection, so an update issued while the cursor is open waits on
	// a cursor that is waiting on it.
	open, err := queryRows(db, scanFields(func(o *openRow) []any { return []any{&o.id, &o.pid} }),
		`SELECT id, pid FROM agent_sessions WHERE ended_at IS NULL AND pid > 0 AND pid != ?`,
		os.Getpid(),
	)
	if err != nil {
		return 0, err
	}
	closed := 0
	for _, o := range open {
		if pidRunning(o.pid) {
			continue
		}
		if err := db.EndAgentSession(o.id, ""); err != nil {
			return closed, err
		}
		closed++
	}
	return closed, nil
}

// liveChatSlots is the set of saved-conversation names another running
// session is writing to. A slot in it is one an autosave in another process
// is about to overwrite, which is why nothing offers to open it.
func (db *DB) liveChatSlots(now time.Time) (map[string]bool, error) {
	open, err := queryRows(db, scanFields(func(o *pidRow) []any { return []any{&o.key, &o.pid} }),
		`SELECT chat_session, pid FROM agent_sessions
		 WHERE chat_session != '' AND ended_at IS NULL
		   AND pid > 0 AND pid != ? AND heartbeat >= ?`,
		os.Getpid(), heartbeatCutoff(now),
	)
	if err != nil {
		return nil, err
	}
	live := map[string]bool{}
	for _, o := range open {
		if pidRunning(o.pid) {
			live[o.key] = true
		}
	}
	return live, nil
}

// heartbeatCutoff is the oldest beat still trusted, in the column's own
// layout so the comparison is the string one SQLite makes. now is the
// caller's clock and has to be a real one: a zero time would put the cutoff
// two thousand years before any row was written, and every stale row in the
// store would pass the check the window exists to apply.
func heartbeatCutoff(now time.Time) string {
	return now.UTC().Add(-agentHeartbeatWindow).Format(observeTimeFormat)
}
