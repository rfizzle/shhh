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
	"github.com/rfizzle/shhh/internal/provider"
)

// WithTreeCheck turns the reading on; nil leaves it off. Own is filled from
// the session's changeset when the caller left it unset, so wire the
// changeset first.
func (m Model) WithTreeCheck(c *agent.TreeCheck) Model {
	if c == nil {
		return m
	}
	cfg := *c
	if cfg.Own == nil {
		store := m.changes
		cfg.Own = func() []string { return writtenPaths(store) }
	}
	m.agent.SetTreeCheck(cfg)
	return m
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
	m.agent.Append(provider.Message{Role: provider.RoleUser, Content: n.Message})
	m.appendEntry(entry{kind: entrySystem, text: n.Notice})
	m.signal(observe.SignalTree, n.Signal())
	m.syncViewport()
}
