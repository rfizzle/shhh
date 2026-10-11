// Package chat is the terminal conversation: the Bubble Tea model behind
// `shhh chat` and `shhh code`, and every screen that opens over it — the
// transcript, the composer, the cards a call is put to a person on, the rail
// of children, and the pickers and takeover surfaces reached from a command or
// a key.
//
// It owns what is drawn and how a key or a click is routed to it. Geometry is
// rectangles resolved once, and a renderer draws into the one it is handed;
// colour is resolved at the top, and the key register decides what a key
// means. It is handed its wiring as one value and constructed from nothing
// else, so a test can build the screen without a terminal.
//
// It does not own the decision a card asks for: the rules every door asks
// before it answers live in internal/approval, and the agent loop, the tools,
// the stores and the sub-agents are reached through the values the wiring
// carries. It does not assemble a session; internal/cli does, and then hands
// the result in.
//
// See docs/capabilities/chat.md, docs/interface/surfaces.md and
// docs/architecture.md#the-screen-is-a-rectangle-and-so-is-everything-in-it.
package chat
