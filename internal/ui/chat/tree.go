package chat

// Delivering the tree reading.
//
// Whether the tree moved, and what to say about it, is agent.NextTreeNotice,
// shared with every headless run (internal/agent/tree.go). What a session
// adds is the subtrahend and the showing: the changeset is the record of what
// this session wrote, so it is what the reading subtracts, and the reader is
// told what their agent was told.

import (
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/project"
)

// WithTreeCheck turns the reading on; nil leaves it off. Own is filled from
// the session's changeset when the caller left it unset, so wire the
// changeset first, and Instructions from the same walk the prompt made.
func (m Model) WithTreeCheck(c *agent.TreeCheck) Model {
	if c == nil {
		return m
	}
	cfg := *c
	if cfg.Own == nil {
		store := m.changes
		cfg.Own = func() []string { return writtenPaths(store) }
	}
	if cfg.Instructions == nil {
		cfg.Instructions = instructionFiles(cfg.Dir)
	}
	m.agent.SetTreeCheck(cfg)
	return m
}

// instructionFiles is the project's own instruction files, found the way the
// prompt found them — the same walk from the session's directory up to the
// project root — so what the notice calls the older reading is the block the
// model is holding rather than a second guess at it.
//
// The user's own file is left out for the reason the survey leaves it out of
// what a project said about itself: it is the person's writing rather than
// the checkout's, and it sits outside the tree this reading can see.
func instructionFiles(dir string) []string {
	if dir == "" {
		dir = "."
	}
	var paths []string
	for _, ins := range project.Instructions(dir, "") {
		paths = append(paths, ins.Path)
	}
	return paths
}

// writtenPaths is every path the session's changeset has recorded, across
// all of its turns. It is deliberately the whole history rather than the
// last boundary's worth: a path this session wrote and somebody else then
// reverted or committed is a change the fingerprint at the next edit will
// catch, and reporting it here would name the session's own file back to it.
func writtenPaths(store *changeset.Store) []string {
	if store == nil {
		return nil
	}
	var paths []string
	for _, t := range store.Turns() {
		for _, r := range t.Records {
			paths = append(paths, r.Path)
		}
	}
	return paths
}

// injectTreeNotice delivers what this boundary owes about the tree, and
// shows it. turnStart is the boundary before the user's message joins; the
// other is between tool rounds. The notice never touches the round counter:
// it is a fact, not a message from the person.
func (m *Model) injectTreeNotice(turnStart bool) {
	n, ok := m.agent.NextTreeNotice(turnStart)
	if !ok {
		return
	}
	m.agent.AppendMachine(n.Message)
	m.appendEntry(entry{kind: entrySystem, text: n.Notice})
	m.signal(observe.SignalTree, n.Signal())
	// A row was appended, so the pane is redrawn the way every other system
	// row is; the resize hook alone would leave it unseen until the next
	// stream flush.
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
}
