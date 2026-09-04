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
)

// The notifications a server sends. Neither carries an id: nothing is owed a
// reply to an event, and an approval request is answered by a call of the
// client's own rather than by a response to this one — several clients may be
// watching one session and only one of them need answer.
const (
	// MethodSessionEvent carries one line of the stream an unattended run
	// writes, verbatim.
	MethodSessionEvent = "session/event"
	// MethodApprovalRequest puts one approval-gated call to every client
	// attached to the session.
	MethodApprovalRequest = "approval/request"
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
