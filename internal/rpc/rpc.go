// Package rpc is the protocol in front of the agent: a JSON-RPC surface a
// client that is not this program's own terminal drives a session through.
//
// It assembles no agent of its own and holds no policy. What it is handed is
// the same passive loop and the same seams an unattended run is built around,
// so a turn a client starts runs under the containment, the trust answer and
// the deny mask that run would have had. The protocol decides who is asked;
// it never decides what the answer is allowed to be.
// See docs/architecture.md#one-agent-several-front-ends.
package rpc

import (
	"encoding/json"
	"fmt"

	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/observe"
)

// Version is the only value the jsonrpc member ever holds, in both
// directions. A message that says anything else is not this protocol.
const Version = "2.0"

// The methods a client calls. They are the vocabulary of the surface a script
// already knows: a session to work in, a turn inside it, and an answer to a
// call the turn may not make unasked.
const (
	// MethodSessionStart opens a session over the checkout the server was
	// started in, optionally carrying on a saved conversation.
	MethodSessionStart = "session/start"
	// MethodSessionResume attaches this connection to a session that already
	// exists, which is how a second client joins the first's work.
	MethodSessionResume = "session/resume"
	// MethodSessionFork opens a session whose conversation begins as a copy
	// of another's: the same history, a separate future.
	MethodSessionFork = "session/fork"
	// MethodSessionEnd ends a session and releases what it was assembled
	// over. A turn still running is interrupted and waited for, so the client
	// is told the session is over only once it is.
	MethodSessionEnd = "session/end"

	// MethodTurnStart puts one prompt to a session and returns as soon as
	// the turn is under way. How it ends arrives on the event stream, because
	// that is where everything else about the turn arrives.
	MethodTurnStart = "turn/start"
	// MethodTurnSteer joins text to a running turn, which the loop reads at
	// its next round boundary.
	MethodTurnSteer = "turn/steer"
	// MethodTurnInterrupt stops a running turn at its next checkpoint.
	MethodTurnInterrupt = "turn/interrupt"

	// MethodApprovalAnswer answers one approval request by the id it was
	// shown under.
	MethodApprovalAnswer = "approval/answer"

	// MethodQuestionAnswer answers one question the model asked, by the id it
	// was put under.
	//
	// It is a second pair beside the approval one rather than a widening of
	// it, because an approval's answer is allow or deny by design and a
	// question has no such answer: what comes back is the labels the reader
	// took and the words they wrote beside them. A client that had to spell
	// "which of these three designs" as a decision would be spelling it as
	// something it is not
	// (docs/capabilities/coding-agent.md#the-model-can-ask).
	MethodQuestionAnswer = "question/answer"
)

// The notifications a server sends. None carries an id: nothing is owed a
// reply to an event, and a request put to the clients is answered by a call of
// their own rather than by a response to this one — several clients may be
// watching one session and only one of them need answer.
const (
	// MethodSessionEvent carries one line of the stream an unattended run
	// writes, verbatim.
	MethodSessionEvent = "session/event"
	// MethodApprovalRequest puts one approval-gated call to every client
	// attached to the session.
	MethodApprovalRequest = "approval/request"
	// MethodQuestionRequest puts one question the model asked to every client
	// attached to the session, on the same terms: the first structured answer
	// wins, because two clients on one session are two views of one
	// conversation and that includes one question.
	MethodQuestionRequest = "question/request"
)

// The error codes. The first five are JSON-RPC's own; the rest are this
// server's, from the range the specification leaves to an implementation.
const (
	CodeParse          = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603

	// CodeUnknownSession: no session answers to that name on this server.
	CodeUnknownSession = -32001
	// CodeTurnRunning: the session already has a turn, and a second one
	// would be two conversations sharing a message list.
	CodeTurnRunning = -32002
	// CodeNoTurn: there is no turn to steer or interrupt.
	CodeNoTurn = -32003
	// CodeUnknownApproval: no approval request is waiting under that id.
	// It is what an answer to a request nobody was shown gets, which
	// includes every attempt to approve a call before it is asked for.
	CodeUnknownApproval = -32004
	// CodeUnknownQuestion: no question is waiting under that id. Questions
	// are named in a series of their own, so an approval's id cannot answer
	// one and an answer that named the wrong series is told so rather than
	// resolving the other request.
	CodeUnknownQuestion = -32005
)

// The two answers an approval request takes. They are the record's own words
// for the same verdict — taken from it rather than spelled again here, so a
// client that has read a decision event on the stream already knows how to
// spell one back and the two cannot drift apart.
const (
	DecisionAllow = observe.DecisionAllow
	DecisionDeny  = observe.DecisionDeny
)

// request is one call from a client. Params are held raw so a method decodes
// its own, and id likewise: the specification lets it be a string, a number
// or null, and a server that reinterpreted it would answer a client in a
// spelling it did not use.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// response is one answer. Exactly one of Result and Error is set.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// notification is a message that is owed no answer, which is what an id's
// absence means on the wire.
type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// Error is one failure as the wire carries it.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// errorf builds one, so a handler states the failure in a sentence rather
// than filling in a struct.
func errorf(code int, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// StartParams opens a session. There is no directory in it: the server has
// one working directory and the tools resolve every path against it, so a
// session that named its own would be reading one tree and writing another.
type StartParams struct {
	// Continue carries on the most recent saved conversation, and Resume the
	// one it names — the two spellings an unattended run is given.
	Continue bool   `json:"continue,omitempty"`
	Resume   string `json:"resume,omitempty"`
}

// SessionParams names a session, and is what every method after the first
// takes.
type SessionParams struct {
	Session string `json:"session"`
}

// SessionResult answers the three session methods with the name to use from
// here and the conversation as it stands, which is what makes a second client
// attaching to a session a client that can read it rather than one that has
// only joined it.
type SessionResult struct {
	Session    string          `json:"session"`
	Transcript json.RawMessage `json:"transcript,omitempty"`
}

// TurnParams puts a prompt to a session.
type TurnParams struct {
	Session string `json:"session"`
	Prompt  string `json:"prompt"`
}

// TurnResult says which turn of the session was started. It is the number the
// events of that turn carry, so a client watching several sessions can file
// each line it receives.
type TurnResult struct {
	Turn int64 `json:"turn"`
}

// SteerParams joins text to a running turn.
type SteerParams struct {
	Session string `json:"session"`
	Text    string `json:"text"`
}

// AnswerParams answers one approval request. The id is the one the request
// was shown under and nothing else: an answer that named a call by its tool
// would approve whichever of them happened to be waiting.
type AnswerParams struct {
	Session  string `json:"session"`
	ID       string `json:"id"`
	Decision string `json:"decision"`
}

// EventParams is one line of the run's own stream, addressed to the session
// it came from. The event is passed through as it was written rather than
// decoded and rebuilt, because the whole promise of it is that a reader who
// has learned the unattended stream has learned this one.
// See docs/capabilities/headless.md#the-stream-is-the-record-as-it-happens.
type EventParams struct {
	Session string          `json:"session"`
	Event   json.RawMessage `json:"event"`
}

// ApprovalParams is one call put to the clients. It carries what a decision
// is made from — which tool, on what arguments — and where in the session it
// happened, in the same two fields every event on the stream carries them in.
type ApprovalParams struct {
	Session   string `json:"session"`
	ID        string `json:"id"`
	Tool      string `json:"tool"`
	Arguments string `json:"arguments"`
	Turn      int64  `json:"turn"`
	Round     int64  `json:"round"`
}

// QuestionParams is one question put to the clients: what is being asked, the
// shape of answer it wants, the answers the model could see, and where in the
// session it was asked — the last two fields being the ones every event and
// every approval request carries them in.
//
// The arguments are decoded and rebuilt here rather than forwarded raw, which
// is the opposite of what an event does. An event's promise is that a reader
// who has learned the run's stream has learned this one; a question's is that
// a client can draw the card without knowing the tool's argument schema, and
// a client left to parse the call would be reimplementing the parse — the
// thing the whole tool exists to stop.
type QuestionParams struct {
	Session  string           `json:"session"`
	ID       string           `json:"id"`
	Question string           `json:"question"`
	Shape    ask.Shape        `json:"shape"`
	Options  []QuestionOption `json:"options,omitempty"`
	// Note says whether the reader may leave one beside their pick or has to.
	Note  ask.Note `json:"note"`
	Turn  int64    `json:"turn"`
	Round int64    `json:"round"`
}

// QuestionOption is one answer the model offered, as the card would draw the
// row: the label that comes back, its continuation, its comparable field,
// whether it leads the list, and why it cannot be taken where it cannot.
//
// Unavailable is carried out rather than dropped because a row that only
// disappeared would be an answer the reader was never told about, and one
// that only dimmed would be a refusal stated in a colour
// (docs/interface/principles.md#colour-never-carries-meaning-alone).
type QuestionOption struct {
	Label       string `json:"label"`
	Detail      string `json:"detail,omitempty"`
	Field       string `json:"field,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
	Unavailable string `json:"unavailable,omitempty"`
}

// QuestionAnswerParams answers one question. The id is the one the question
// was put under, for the reason an approval's is.
//
// What comes back is labels and never indices: an index is a fact about the
// list the model wrote, and a client that drew the recommendation first would
// be answering in a numbering the model never had. Answered is the reader's
// own three words for how they answered, from the vocabulary the model is
// told its answer in rather than a second spelling here — `nobody to ask` is
// not among them, because a client cannot report its own absence.
type QuestionAnswerParams struct {
	Session  string       `json:"session"`
	ID       string       `json:"id"`
	Answered ask.Answered `json:"answered"`
	Picked   []string     `json:"picked,omitempty"`
	Note     string       `json:"note,omitempty"`
}
