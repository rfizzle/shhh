package cli

// What an unattended run writes: the observer that reports each event to
// the record and the stream, the --json transcript, and the --output jsonl
// event stream with its writers.

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/hook"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
)

// headlessObserver is the headless run's adaptation to the observer contract
// in internal/observe, the way internal/ui/chat/observe.go is the chat
// model's. The codes live there because every surface reports the same ones;
// what lives here is where a headless run's own accounting — its single
// turn, the round the loop has reached — is read off to fill them in.
//
// A run recorded without a position cannot be told apart from one that
// circled for forty rounds, which is the shape the record exists to show.
// See docs/capabilities/sessions-and-memory.md#every-composition-is-one-population.
type headlessObserver struct {
	rec *observeRecorder
	// rounds is the tool round the loop has reached. It is a function rather
	// than the agent itself because that is the whole of what a position
	// needs from it.
	rounds func() int
	// turn is which turn of the conversation the events belong to. A run with
	// nobody in front of it is one turn by construction — one prompt in, one
	// answer out — and leaves this nil; a surface that carries several turns
	// over one conversation fills it in, because a record that filed all of
	// them under turn 1 could not tell a long session from a stalled one.
	turn func() int64
	// stream is the event stream the run was asked for, or nil where it was
	// not. It hangs here rather than beside the hooks so that what is written
	// to it and what is written to the record leave from one place: an event
	// that reaches the table and not the stream is how the two vocabularies
	// come apart, and nothing fails when they do.
	stream *jsonlStream
	// sources is the run's sources ledger as the stream is told about it:
	// each row once, as it lands. Nil is a run that keeps no ledger.
	sources *sourceFeed
}

// sourceFeed is how far the stream has been told about a ledger. It is a
// pointer on the observer, not a count in it, because the observer is a value
// handed out by copy (inTurn) and every copy is reporting the one ledger.
//
// What it has told is a set of row ids rather than a count, because a ledger
// is not append-only across a bind: the slot's older rows are loaded in front
// of this session's, and a count would take the tail of those for rows the
// stream had not been told about.
type sourceFeed struct {
	mu     sync.Mutex
	ledger *web.Ledger
	sent   map[int64]bool
	// slot is the slot the feed last bound the ledger to, so a save that left
	// the slot where it was binds nothing.
	slot string
}

// newSourceFeed follows a ledger; nil for a run that keeps none.
func newSourceFeed(l *web.Ledger) *sourceFeed {
	if l == nil {
		return nil
	}
	return &sourceFeed{ledger: l, sent: map[int64]bool{}}
}

// fresh is the rows the stream has not been told about yet, oldest first.
func (f *sourceFeed) fresh() []web.Source {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []web.Source
	for _, s := range f.ledger.List() {
		if f.sent[s.ID] {
			continue
		}
		f.sent[s.ID] = true
		out = append(out, s)
	}
	return out
}

// bind files the ledger under slot and carries what the stream has been told
// across the bind. The rows the slot already held are its history and were
// never this session's to report, so they count as told. This session's own
// rows are written through again under the slot, in the order they were
// recorded and with the ids the store gives them, so each keeps what the
// stream had been told about it by position: held is asked first for how
// many rows the bind will load in front, which is where this session's begin.
func (f *sourceFeed) bind(slot string, held web.LedgerBackend) error {
	if f == nil || held == nil || slot == "" {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if slot == f.slot {
		return nil
	}
	loaded, err := held.LoadSources(slot)
	if err != nil {
		return err
	}
	before := f.ledger.List()
	bindErr := f.ledger.Bind(slot)
	f.slot = slot
	after := f.ledger.List()
	sent := make(map[int64]bool, len(after))
	for i, s := range after {
		switch mine := i - len(loaded); {
		case mine < 0:
			sent[s.ID] = true
		case mine < len(before):
			if f.sent[before[mine].ID] {
				sent[s.ID] = true
			}
		}
		// Anything past those was recorded while the bind ran, and is
		// left for the next fresh to tell.
	}
	f.sent = sent
	return bindErr
}

// sourcesRead puts every ledger row recorded since the last call on the
// stream, one line each. It is asked after each tool result and once more
// before the close line: the orchestrator's own reads land inside the call
// that made them, and a child's — it fetches through this run's toolset —
// by the time the call that waited on it returns, or by the close at the
// latest. The record is not told: a row carries an address, and the record
// is content-free by construction.
// See docs/capabilities/headless.md#a-run-says-what-it-read.
func (h headlessObserver) sourcesRead() {
	if h.stream == nil {
		return
	}
	for _, s := range h.sources.fresh() {
		h.stream.source(h.pos(), jsonSourceOf(s))
	}
}

// pos is where the run is now.
func (h headlessObserver) pos() observe.Pos {
	return h.at(h.rounds())
}

// at is the run's turn with a round the caller already holds.
func (h headlessObserver) at(round int) observe.Pos {
	turn := int64(1)
	if h.turn != nil {
		turn = h.turn()
	}
	return observe.Pos{Turn: turn, Round: int64(round)}
}

// inTurn is the observer with its turn fixed, for a report that can arrive
// after the turn it is about has ended.
func (h headlessObserver) inTurn(turn int64) headlessObserver {
	h.turn = func() int64 { return turn }
	return h
}

// childLives is what a `-p` run's stream is told about the children it
// spawns: an agent line as each one starts and another as it ends, spelled as
// a served session's are, so the child seams a hook is told about have a line
// on every unattended stream to share their spelling with. The state changes
// between are left to a served session, whose client draws lanes from them; a
// script reading this stream wants to know a child exists and how it ended.
// A child that is retried or spoken to again after it ended starts again.
//
// It is nil where the run streams nothing. The function is called from the one
// goroutine that reads the supervisor's events, so the map needs no lock.
// See docs/capabilities/headless.md#the-stream-is-the-record-as-it-happens.
func (h headlessObserver) childLives(sup *subagent.Supervisor) func(subagent.Status) {
	if h.stream == nil || sup == nil {
		return nil
	}
	// live is absent for a child nothing has been written about, true for one
	// whose start was written and false for one whose end was.
	live := map[string]bool{}
	return func(st subagent.Status) {
		ended := st.State == subagent.StateDone || st.State == subagent.StateFailed
		running, seen := live[st.Name]
		if ended && seen && !running || !ended && running {
			return
		}
		live[st.Name] = !ended
		parent, _ := sup.Parent(st.Name)
		h.stream.agent(childPos(h.at(0).Turn), agentLine(st, parent))
	}
}

// signal records one of the loop's own safeguards firing, and puts it on the
// stream under the same code. Every signal below goes through here, so a code
// cannot reach one and not the other.
func (h headlessObserver) signal(code, reason string) {
	h.signalAt(h.pos(), code, reason)
}

// signalAt is signal at a position the caller already holds.
func (h headlessObserver) signalAt(at observe.Pos, code, reason string) {
	h.rec.signal(at, code, reason)
	h.stream.signal(at, code, reason)
}

// text is one piece of the answer as it was written. It reaches the stream
// and not the record: what the model said is content, and the record is
// content-free by construction.
func (h headlessObserver) text(s string) {
	h.stream.text(h.pos(), s)
}

// progress writes public status only to JSONL. Text output is deliberately the
// final answer alone, while the content-free record observes the checkpoint.
func (h headlessObserver) progress(s string) {
	at := h.pos()
	h.rec.signal(at, observe.SignalProgress, observe.ProgressCheckpoint)
	h.stream.progress(at, s)
}

// call is one call the model asked for, before it ran or was resolved. The
// record keeps a call and its result as one row, written when the result
// lands; the stream carries them as two, because between them is the wait
// that is the reason to read a stream at all.
func (h headlessObserver) call(tc provider.ToolCall) {
	h.stream.call(h.pos(), tc)
}

// toolResult records one executed call from its result text: the outcome
// and, for a failure, its class — and the repeat detector's notice where the
// result carries one, since being told it is circling is a thing that
// happened to the run and not a property of the call.
func (h headlessObserver) toolResult(r agent.ToolResult) {
	outcome, class := observe.ToolOutcome(r.Result)
	h.rec.toolCallAt(h.pos(), r.Call.Name, r.Duration, outcome, class,
		observe.ToolPurpose(r.Call.Name, r.Call.Arguments))
	h.stream.result(h.pos(), r, outcome, class)
	if agent.IsRepeatNotice(r.Result) {
		h.signal(observe.SignalRepeat, r.Call.Name)
	}
	h.sourcesRead()
}

// decision records what the approver resolved a gated call to. A headless
// run resolves every one of them from policy rather than from a person, so
// this is the only place its approval rate can come from.
func (h headlessObserver) decision(decision, reason string) {
	at := h.pos()
	h.rec.decisionAt(at, decision, reason)
	h.stream.decision(at, decision, reason)
}

// usage is what the run has spent so far. The record takes it priced; the
// stream carries the tokens, cached ones included, because a script totalling
// a night of runs is doing the pricing itself.
func (h headlessObserver) usage(u provider.Usage) {
	h.stream.usage(h.pos(), u)
}

// summary records a reading. Every reading lands here and not only the ones
// that go on to interrupt the turn: a drift rate is a fraction, and this is
// its denominator.
//
// It is filed at the round the reading states rather than at h.pos(): a
// closing reading is delivered on the summariser's own goroutine after Run
// has returned, where the agent's round counter is the next turn's to write,
// and the verdict is about the round its evidence was taken at anyway. The
// turn is the same question on a surface with more than one: a served
// session hands the run an observer fixed to the turn it was built for
// (inTurn), since by the time a closing reading lands the loop may be
// counting the next.
func (h headlessObserver) summary(v agent.SummaryVerdict) {
	h.signalAt(h.at(v.Round), observe.SignalSummary, observe.SummaryCode(v.State))
}

// intervene records the run interrupting its own turn to ask it to take
// stock.
func (h headlessObserver) intervene(iv agent.Intervention) {
	h.signal(observe.SignalIntervene, iv.Kind.Signal())
}

// withheld records an interruption the policy owed a reading and did not
// deliver, under the same code as the ones that were: a reader asking how a
// run's interruptions were decided is asking about one population, and a
// withheld one is the answer to why a run that drifted was never steered.
func (h headlessObserver) withheld(reason string) {
	h.signal(observe.SignalIntervene, reason)
}

// tree records the run being told the tree moved under it.
func (h headlessObserver) tree(n agent.TreeNotice) {
	if n.Unavailable {
		return
	}
	h.signal(observe.SignalTree, n.Signal())
}

// compact records what a window-recovery step did. Both halves are recorded
// and each under its own code: a trim spends the provider's cached prefix and
// a compaction spends a request and the conversation, and a rate that added
// them together could not tell a run that shaved itself once from one that
// threw its history away.
func (h headlessObserver) compact(n agent.CompactNotice) {
	if n.Elided > 0 {
		h.signal(observe.SignalTrim, observe.TrimReason(n.Elided, n.BeforePct, n.AfterPct))
	}
	if n.Compacted {
		at := h.pos()
		h.rec.signal(at, observe.SignalCompact, observe.CompactPressure)
		h.stream.compacted(at, n)
	}
}

// retry records one wait the run sat out after a request the provider never
// answered. It is recorded per attempt, so a population of unattended runs
// can be asked how much of its wall clock was a provider's and not its own.
func (h headlessObserver) retry(n agent.RetryNotice) {
	h.signal(observe.SignalRetry, n.Signal())
}

// failureClass is the provider's own word for what went wrong, for the JSON
// shapes to state beside the error text. It is empty for an ending that was
// not a provider call — a failing suite, a standing refusal, a run that
// finished — which is how a consumer tells the two apart without parsing the
// sentence.
func failureClass(err error) string {
	if f, ok := provider.AsFailure(err); ok {
		return string(f.Class)
	}
	var miss *schemaMiss
	if errors.As(err, &miss) {
		return errorClassSchema
	}
	return ""
}

// clipActivityLine renders text as a single bounded line for stderr activity
// logging.
func clipActivityLine(raw string) string {
	s := strings.Join(strings.Fields(raw), " ")
	if cut, truncated := tools.TruncateOutput(s, 120); truncated {
		return cut + "…"
	}
	return s
}

// jsonTranscript is the --json output: the outcome, usage totals, and the
// full conversation including tool calls and results.
type jsonTranscript struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	// ErrorClass is the provider's own name for what went wrong, where the
	// run ended on a provider call: `unauthorized`, `rate limited`,
	// `context too long`. It is the field that lets a consumer tell a stall
	// it should sit out from a request the provider will refuse again,
	// without waiting for a status to be minted for every class.
	// See docs/capabilities/headless.md#the-exit-code-is-the-contract.
	ErrorClass string `json:"error_class,omitempty"`
	// Final is the answer as the model wrote it, or — where the run was
	// given --output-schema and the answer satisfied it — the answer as JSON
	// (answer, below, which MarshalJSON puts in its place).
	// See docs/capabilities/headless.md#an-answer-can-be-held-to-a-schema.
	Final string `json:"final"`
	// Truncated qualifies Final: the answer stopped at the model's output
	// ceiling and the run's one continuation had already been spent, so what
	// is quoted is the first half of an answer. It is stated because nothing
	// downstream can see it in the words — the sentence simply ends — and a
	// caller that grades the answer grades half the work. It is absent when
	// the answer is whole, which is how every earlier reader of this shape
	// already reads it.
	// See docs/capabilities/providers.md#a-reply-says-why-it-stopped.
	Truncated bool `json:"truncated,omitempty"`
	// Gate is what the turn's close did about the project's checks:
	// `passed`, `failed`, or `not-run` for a turn that ran none — no suite
	// configured, or nothing changed for one to have an opinion about. It is
	// stated rather than left to the exit status because the status cannot
	// carry it: a turn that checked nothing and a turn whose checks passed
	// both exit 0, and a caller that took that for a pass would be reading a
	// tree nothing has looked at.
	// See docs/capabilities/headless.md#three-shapes-for-the-same-run.
	Gate string `json:"gate"`
	// Written is the paths the run's own mutating calls wrote, in the order
	// they were written. It is the unattended stand-in for a session's
	// changeset, and the reason it is here is the caller that has to commit
	// the run's work: the tree says what is changed, not who changed it, and
	// a file somebody had already left modified is one only this list can
	// claim for the run.
	Written []string `json:"written,omitempty"`
	// Chat, Session and Resume are the handles the run leaves behind: the
	// slot the conversation was saved to, the record row that says what it
	// cost, and the command that opens the conversation again.
	//
	// They are stated because the alternative a caller has is `--continue`,
	// which is a race — "the most recent conversation" on a machine running
	// two of these is whichever finished last, and a script that read exit
	// 2 and wanted to carry that run on would carry on the other one. The
	// slot is a name, so it is a handle; the row id is what joins the answer
	// to what it cost in `shhh observe`, and it is spelled as the hook
	// payload spells the same id.
	// See docs/capabilities/headless.md#the-run-says-where-it-left-off.
	Chat    string `json:"chat,omitempty"`
	Session string `json:"session,omitempty"`
	Resume  string `json:"resume,omitempty"`
	// Sources is what the run read, from its sources ledger and never from
	// the answer's prose: one row per fetch, per search and per page a
	// server's tool handed back, in the order they were recorded. It is here
	// so a write-up nobody watched being written can be held to what was
	// actually read — a URL the answer cites that no fetch row answered for
	// is cited and not read. It is absent for a run that read nothing.
	// See docs/capabilities/headless.md#a-run-says-what-it-read.
	Sources  []jsonSource  `json:"sources,omitempty"`
	Usage    jsonUsage     `json:"usage"`
	Messages []jsonMessage `json:"messages"`

	answer json.RawMessage
}

// MarshalJSON states the checked answer as JSON in Final's place, so that
// `.final` is the value a schema was given for and not a string holding it.
func (t jsonTranscript) MarshalJSON() ([]byte, error) {
	type plain jsonTranscript
	if t.answer == nil {
		return json.Marshal(plain(t))
	}
	return json.Marshal(struct {
		plain
		Final json.RawMessage `json:"final"`
	}{plain(t), t.answer})
}

// jsonSource is one ledger row as both JSON shapes state it: the fields the
// sources screen draws a row from, in the ledger's own words. Kind is the
// ledger's closed set — a fetch, a search, or "mcp" for a page a server's
// tool handed back, which shhh made no request for and which is therefore
// never one of the pages a citation is checked against. URL is the address
// that answered, or the one asked for where nothing did; Status is what it
// answered with, and is what tells a page that was read from an error page.
// Title is the extraction's own title — half of how a page is cited — and
// the one field here that came from the page.
type jsonSource struct {
	Kind     string `json:"kind"`
	URL      string `json:"url,omitempty"`
	Query    string `json:"query,omitempty"`
	Title    string `json:"title,omitempty"`
	Agent    string `json:"agent"`
	Status   int    `json:"status,omitempty"`
	Results  int    `json:"results,omitempty"`
	Bytes    int    `json:"bytes,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

// jsonSourceOf is the one reading of a ledger row into that shape.
func jsonSourceOf(s web.Source) jsonSource {
	url := s.FinalURL
	if url == "" {
		url = s.Requested
	}
	return jsonSource{Kind: s.Kind, URL: url, Query: s.Query, Title: s.Title, Agent: s.Agent,
		Status: s.Status, Results: s.Results, Bytes: s.Bytes, Evidence: s.Evidence}
}

// jsonSources is a ledger's rows in that shape; nil for none, so the field
// stays absent on a run that read nothing.
func jsonSources(rows []web.Source) []jsonSource {
	if len(rows) == 0 {
		return nil
	}
	out := make([]jsonSource, 0, len(rows))
	for _, s := range rows {
		out = append(out, jsonSourceOf(s))
	}
	return out
}

// jsonUsage is what the run cost, as every JSON shape reports it. The cached
// tokens are part of the prompt total and stated separately because they are
// billed at a fraction of it: a script totalling a night of runs against a
// price list cannot work out what it spent from the other two figures, and
// the run already knows.
// See docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once.
type jsonUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	CachedTokens     int `json:"cached_tokens"`
}

// usageOf is the one reading of a run's totals into the shape both the
// transcript and the stream state them in.
func usageOf(u provider.Usage) jsonUsage {
	return jsonUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		CachedTokens:     u.CachedTokens,
	}
}

// jsonMessage is one message as every JSON transcript emits it: the role and
// the words, and a tool call's name and arguments where there was one.
// Attachments are left out — a transcript for a script is text.
//
// `shhh chats show --json` and `shhh code --print --json` are two views of the
// same conversation, so they are one projection: two structs of the same
// fields built by two loops drift the moment either grows a field.
type jsonMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []jsonToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// jsonToolCall is one call the assistant made, name and arguments apart so a
// script does not have to split a string. The id is what a tool result refers
// back to, and is always present — a consumer that keys on it should find an
// empty string rather than no field where one was never recorded.
type jsonToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// jsonMessages is the conversation as data.
func jsonMessages(msgs []provider.Message) []jsonMessage {
	out := make([]jsonMessage, 0, len(msgs))
	for _, m := range msgs {
		row := jsonMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			row.ToolCalls = append(row.ToolCalls, jsonToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
		}
		out = append(out, row)
	}
	return out
}

// jsonRun is one unattended run as the transcript states it. It is a struct
// and not six parameters because the last three arrived together and a
// caller that transposed two of them would be writing a transcript nothing
// downstream could tell was wrong.
type jsonRun struct {
	messages []provider.Message
	final    string
	// answer is final as JSON, where a schema checked it.
	answer    json.RawMessage
	truncated bool
	usage     provider.Usage
	gate      quality.Closing
	written   []string
	// sources is the run's sources ledger, oldest row first.
	sources []web.Source
	// left is where the run can be picked up from: the slot, the record row
	// and the command.
	left headlessHandles
	err  error
}

// headlessHandles is where an unattended run left off, in the three forms a
// caller needs it in. It is a value of its own because the three are one
// fact and every shape that states it states all three: a transcript with a
// slot and no row, or a close line with a row and no command to open it, is
// half a handle.
type headlessHandles struct {
	chat string
	// session is the record row rendered the way a hook is handed it — the
	// same id, under the same key, in the same shape — because a separate
	// process joining its own notes to shhh's table reads one of these or
	// the other and there is one vocabulary across them
	// (docs/capabilities/hooks.md#the-payload-is-the-event-stream).
	session string
	resume  string
}

func writeJSONTranscript(w io.Writer, r jsonRun) error {
	// A run built with no gate at all still answers the question, in the
	// same word a run whose gate found nothing to check answers it with:
	// the field is the contract, and an empty one would leave the reader
	// back at the exit status.
	gate := r.gate
	if gate == "" {
		gate = quality.ClosingNotRun
	}
	t := jsonTranscript{
		Success:   r.err == nil,
		Final:     r.final,
		Truncated: r.truncated,
		Gate:      string(gate),
		Written:   r.written,
		Chat:      r.left.chat,
		Session:   r.left.session,
		Resume:    r.left.resume,
		Sources:   jsonSources(r.sources),
		Usage:     usageOf(r.usage),
		Messages:  jsonMessages(r.messages),
		answer:    r.answer,
	}
	if r.err != nil {
		t.Error, t.ErrorClass = r.err.Error(), failureClass(r.err)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(t)
}

// jsonlStream is what `--output jsonl` writes: one JSON object per line as
// the run happens, in the vocabulary internal/observe already fixes for the
// record. A reader of one shape therefore already knows the other, and a hook
// written against the record's codes matches the stream's without a second
// table to learn.
//
// The lines are the event and nothing else — no wrapping, no indentation, no
// trailing summary — because the thing reading them is a loop over a pipe
// that has to be able to act on a line before the run is over.
// See docs/capabilities/headless.md#the-stream-is-the-record-as-it-happens.
type jsonlStream struct {
	// mu is around the encoder rather than around each caller. Everything
	// reaches it on the run's own goroutine today, and interleaved halves of
	// two objects would be a corrupt stream that nothing reports — the
	// cheapest possible insurance against the round that dispatches its
	// callbacks the way it already dispatches its calls.
	mu  sync.Mutex
	enc *json.Encoder
}

func newJSONLStream(w io.Writer) *jsonlStream {
	return &jsonlStream{enc: json.NewEncoder(w)}
}

// jsonEvent is one line of that stream. Every field that names a kind, an
// outcome, a decision, a reason or a signal holds a constant from
// internal/observe and never text this run composed: a script matches on
// them, and a code it has to parse prose out of is not a code.
type jsonEvent struct {
	Kind  string `json:"kind"`
	Turn  int64  `json:"turn"`
	Round int64  `json:"round"`

	Text       string `json:"text,omitempty"`
	ID         string `json:"id,omitempty"`
	Tool       string `json:"tool,omitempty"`
	Arguments  string `json:"arguments,omitempty"`
	Result     string `json:"result,omitempty"`
	Outcome    string `json:"outcome,omitempty"`
	Class      string `json:"class,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Decision   string `json:"decision,omitempty"`
	Code       string `json:"code,omitempty"`
	Reason     string `json:"reason,omitempty"`
	// Trigger, BeforePct and AfterPct belong to a compaction's signal line:
	// who asked for it and how full the window was either side, the figures
	// the log already writes. A compaction hook is told the same three under
	// the same names (docs/capabilities/hooks.md#the-payload-is-the-event-stream).
	Trigger   string `json:"trigger,omitempty"`
	BeforePct int    `json:"before_pct,omitempty"`
	AfterPct  int    `json:"after_pct,omitempty"`

	Usage *jsonUsage `json:"usage,omitempty"`
	// Agent belongs to the agent line alone: one child of this session as it
	// stands right now.
	Agent *jsonAgent `json:"agent,omitempty"`
	// Source belongs to the source line alone: one row of the sources
	// ledger, in the shape the transcript's sources field holds.
	Source *jsonSource `json:"source,omitempty"`
	// Document belongs to the document/* kinds, which are reserved and not
	// yet written: nil on every line today.
	// See docs/capabilities/headless.md#the-stream-is-the-record-as-it-happens.
	Document *jsonDocument `json:"document,omitempty"`
	// Exit and Final belong to the close line alone. Exit is a pointer so
	// that the code the run is about to exit with is stated even when it is
	// zero, which is the one value a reader most needs to see written down.
	Exit  *int   `json:"exit,omitempty"`
	Final string `json:"final,omitempty"`
	Error string `json:"error,omitempty"`
	// ErrorClass qualifies Error on the close line, in the provider's own
	// vocabulary, so a reader acting on the stream knows whether waiting is
	// a plan before the process has even exited. Class above is the tool
	// vocabulary and belongs to a result line; these are two vocabularies
	// and the field each is named on is which one it is.
	ErrorClass string `json:"error_class,omitempty"`
	// Chat, Session and Resume belong to the close line too, and say the
	// same three things the transcript's do: the slot the conversation was
	// left in, the record row, and the command that opens it again. A reader
	// that acts on the stream reads only lines, so a handle stated in the
	// other shape alone would not reach it.
	// See docs/capabilities/headless.md#the-run-says-where-it-left-off.
	Chat    string `json:"chat,omitempty"`
	Session string `json:"session,omitempty"`
	Resume  string `json:"resume,omitempty"`

	// answer is the close line's Final as JSON, where a schema checked it.
	answer json.RawMessage
}

// MarshalJSON states the checked answer as JSON in Final's place, the way
// the transcript states it.
func (e jsonEvent) MarshalJSON() ([]byte, error) {
	type plain jsonEvent
	if e.answer == nil {
		return json.Marshal(plain(e))
	}
	return json.Marshal(struct {
		plain
		Final json.RawMessage `json:"final"`
	}{plain(e), e.answer})
}

// jsonDocument is the payload the reserved document/* kinds will carry: which
// document, what the finding is about, and the finding in the record's own
// words. Nothing builds one yet.
type jsonDocument struct {
	Path    string `json:"path"`
	Subject string `json:"subject,omitempty"`
	Verdict string `json:"verdict,omitempty"`
	Score   *int   `json:"score,omitempty"`
}

// jsonAgent is one child of the session as a reader of the stream is told
// about it: the fields shhh's own lane and map are drawn from, in the record
// the supervisor already keeps rather than a second reading of it.
//
// Parent is what nests them. It is beside the name rather than a depth
// because a depth is a fact about the set a surface is drawing — a fan-out
// block holding one round's children counts from its own top — and a parent
// is a fact about the child, which is what a line reporting one child at a
// time can honestly carry.
type jsonAgent struct {
	Name   string `json:"name"`
	Parent string `json:"parent,omitempty"`
	Role   string `json:"role"`
	State  string `json:"state"`
	Task   string `json:"task,omitempty"`
	Model  string `json:"model,omitempty"`
	// Detail is the child's own line — what it says it is doing — and is the
	// one field here that is prose. It is not a code and nothing matches on
	// it; the state above is what a reader acts on.
	Detail string `json:"detail,omitempty"`
	// Handoff is the handle of the record a failed child left, the one
	// resume_handoff takes. It is a field of its own rather than a clause
	// of the detail, so a client decides whether it has room to draw it.
	Handoff string `json:"handoff,omitempty"`
	// Paths is a writer's declared write scope, empty for an unscoped child.
	Paths []string `json:"paths,omitempty"`
	// Batch groups the children one parent tool round spawned, so a fan-out
	// can be drawn as one block rather than as interleaved rows.
	Batch     int   `json:"batch,omitempty"`
	Step      int   `json:"step,omitempty"`
	Steps     int   `json:"steps,omitempty"`
	ToolCalls int   `json:"tool_calls,omitempty"`
	ElapsedMS int64 `json:"elapsed_ms,omitempty"`
	// ChildTurn and ChildRound are where the child is in its own
	// conversation, the position its own events are filed at. They are
	// named apart from the line's turn, which is the parent's: a child
	// spawned in the parent's third turn is on its first, and a reader
	// filing the child's news under the parent's number files it wrong.
	// A hook's agent object does not carry them, because a child seam's
	// payload is already placed at the child's own turn and round.
	ChildTurn  int64 `json:"child_turn,omitempty"`
	ChildRound int64 `json:"child_round,omitempty"`
	// Budget is the effective fresh-token budget and Tokens the fresh total
	// that has gone against it. The record's own split of that total by phase
	// is not here: what a lane draws is the pair, and the phases are
	// estimates a reader would have to be told not to add up.
	Budget int64 `json:"budget,omitempty"`
	Tokens int64 `json:"tokens,omitempty"`
	// Summary is the first line of a finished child's report, Verdict the
	// last reading of its work in the summariser's own closed vocabulary,
	// and End how the attempt stopped, from the closed set in
	// internal/observe. End is empty while the child is still running.
	Summary string `json:"summary,omitempty"`
	Verdict string `json:"verdict,omitempty"`
	End     string `json:"end,omitempty"`
	// Steers is how many times this turn the child has been told it looks to
	// have left its task, and SteerFrom where the last message in front of it
	// came from. Held says the child has parked at its round boundary on the
	// session's hold, which is a running child and not a state of its own.
	Steers    int    `json:"steers,omitempty"`
	SteerFrom string `json:"steer_from,omitempty"`
	Held      bool   `json:"held,omitempty"`
	// LaneSteers and ParentSteers are the rest of this turn's steers by the
	// party that gave them — a person at the lane or over the protocol, and
	// the orchestrator that wrote the task — beside Steers, which is the
	// check's alone. They are counts per party because a total of one of
	// yours and one of the check's cannot be split back into the "1 yours" a
	// lane says. A hook's agent object does not carry them: it names whose
	// seam fired, and carries no other field a lane is drawn from either.
	LaneSteers   int `json:"lane_steers,omitempty"`
	ParentSteers int `json:"parent_steers,omitempty"`
}

// write puts one event on the stream. A nil stream is the run that asked for
// another shape, and writes nothing.
func (s *jsonlStream) write(ev jsonEvent) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A stream nobody is reading any more — a consumer that stopped at the
	// first line it wanted — is not a reason to end the run: the record and
	// the exit code are still owed.
	_ = s.enc.Encode(ev)
}

func (s *jsonlStream) text(at observe.Pos, text string) {
	if s == nil || text == "" {
		return
	}
	s.write(jsonEvent{Kind: observe.EventText, Turn: at.Turn, Round: at.Round, Text: text})
}

func (s *jsonlStream) progress(at observe.Pos, text string) {
	if s == nil || text == "" {
		return
	}
	s.write(jsonEvent{Kind: observe.EventProgress, Turn: at.Turn, Round: at.Round, Text: text})
}

func (s *jsonlStream) call(at observe.Pos, tc provider.ToolCall) {
	if s == nil {
		return
	}
	s.write(jsonEvent{Kind: observe.EventToolCall, Turn: at.Turn, Round: at.Round,
		ID: tc.ID, Tool: tc.Name, Arguments: tc.Arguments})
}

func (s *jsonlStream) result(at observe.Pos, r agent.ToolResult, outcome, class string) {
	if s == nil {
		return
	}
	s.write(jsonEvent{Kind: observe.EventToolResult, Turn: at.Turn, Round: at.Round,
		ID: r.Call.ID, Tool: r.Call.Name, Result: r.Result,
		Outcome: outcome, Class: class, DurationMS: r.Duration.Milliseconds()})
}

func (s *jsonlStream) decision(at observe.Pos, decision, reason string) {
	if s == nil {
		return
	}
	s.write(jsonEvent{Kind: observe.EventDecision, Turn: at.Turn, Round: at.Round,
		Decision: decision, Reason: reason})
}

func (s *jsonlStream) signal(at observe.Pos, code, reason string) {
	if s == nil {
		return
	}
	s.write(jsonEvent{Kind: observe.EventSignal, Turn: at.Turn, Round: at.Round,
		Code: code, Reason: reason})
}

// compacted is the signal line a compaction is, with the figures either side
// of it. Every compaction an unattended surface makes is one its round tail
// asked for, so its trigger is always the automatic one.
func (s *jsonlStream) compacted(at observe.Pos, n agent.CompactNotice) {
	if s == nil {
		return
	}
	s.write(jsonEvent{Kind: observe.EventSignal, Turn: at.Turn, Round: at.Round,
		Code: observe.SignalCompact, Reason: observe.CompactPressure,
		Trigger: hook.TriggerAuto, BeforePct: n.BeforePct, AfterPct: n.AfterPct})
}

// agent puts one child's state on the stream. A served session writes one at
// every state change, because a client draws its lanes from them; a `-p` run
// writes one as a child starts and one as it ends (childLives).
// See docs/capabilities/headless.md#something-else-can-drive-it.
func (s *jsonlStream) agent(at observe.Pos, a jsonAgent) {
	if s == nil {
		return
	}
	s.write(jsonEvent{Kind: observe.EventAgent, Turn: at.Turn, Round: at.Round, Agent: &a})
}

// source puts one row of the run's sources ledger on the stream.
func (s *jsonlStream) source(at observe.Pos, row jsonSource) {
	if s == nil {
		return
	}
	s.write(jsonEvent{Kind: observe.EventSource, Turn: at.Turn, Round: at.Round, Source: &row})
}

func (s *jsonlStream) usage(at observe.Pos, u provider.Usage) {
	if s == nil {
		return
	}
	priced := usageOf(u)
	s.write(jsonEvent{Kind: observe.EventUsage, Turn: at.Turn, Round: at.Round, Usage: &priced})
}

// closed is the last line of every stream: how the turn ended, in the same
// word the record keeps, the exit code projected from it, the answer, and
// what the run spent getting there. A consumer that reads only this line has
// everything the exit status says and the answer besides.
func (s *jsonlStream) closed(at observe.Pos, outcome string, code int, final string, answer json.RawMessage, u provider.Usage, left headlessHandles, err error) {
	if s == nil {
		return
	}
	priced := usageOf(u)
	ev := jsonEvent{Kind: observe.EventClose, Turn: at.Turn, Round: at.Round,
		Outcome: outcome, Exit: &code, Final: final, Usage: &priced,
		Chat: left.chat, Session: left.session, Resume: left.resume, answer: answer}
	if err != nil {
		ev.Error, ev.ErrorClass = err.Error(), failureClass(err)
	}
	s.write(ev)
}
