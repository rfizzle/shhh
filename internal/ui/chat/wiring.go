package chat

// What a built model was given.
//
// A session is assembled by a long chain of With… calls in another package —
// sub-agents, memory, the classifier, the titler, the changeset, the hooks,
// the store the window trim recovers what it elided from — and every one of
// them is conditional on something: a flag, a config key, a store that
// opened, a binary on PATH. A condition that quietly stops being true drops a
// mechanism out of the session and nothing says so: the model simply never
// spawns a child, or the trim starts costing what it elides, and the surface
// looks exactly the same.
//
// Nothing outside this package can see any of that, because what the chain
// sets are fields nobody exports. This is the reading it can take instead: a
// yes or a no per mechanism, so an assembly can be asserted where it is
// built. It is a report on what was wired and never a way to wire anything —
// there is no setter here, and there must not be one.

// Wiring is one model's answer to what it was given.
type Wiring struct {
	// Subagents, Memory, Classifier and Titler are the four the session
	// builds from the provider it resolved.
	Subagents  bool
	Memory     bool
	Classifier bool
	Titler     bool
	// Changeset is the store review, undo and a writer child's starting
	// point are read from, together with the git tracker beside it. The pair
	// and not the store alone: every model builds a store for itself, so the
	// tracker is the half that says a session wired its own — which is what
	// a conversation, having nothing to change, does not do.
	Changeset bool
	// Hooks is the person's own commands at the session's seams. It is false
	// where nobody wrote one, which is most sessions: what it reports is
	// that the runner reached the model, not that hooks exist.
	Hooks bool
	// RecoverableTrim is a store behind the window trim: what it elides can
	// be asked for again, rather than being gone. It is the store and not
	// the reduction, because a session can reduce a long result and still
	// lose it permanently at the trim — they are wired from the same struct
	// and are different promises.
	RecoverableTrim bool
	// The rest are the conditional surfaces a session offers, in the order
	// they are wired.
	Gate      bool
	Processes bool
	Skills    bool
	Todos     bool
	Scope     bool
	Notebook  bool
	// Ask is the question tool: the model can put a fork it cannot decide to
	// the person. It is false wherever there is nobody to answer one, which
	// is the whole of how that is decided.
	Ask bool
}

// Wiring reports what this model was given.
func (m Model) Wiring() Wiring {
	return Wiring{
		Subagents:       m.subagents != nil,
		Memory:          m.memory.Manage != nil,
		Classifier:      m.classifier != nil,
		Titler:          m.titler != nil,
		Changeset:       m.changes != nil && m.tracker != nil,
		Hooks:           m.hooks != nil,
		RecoverableTrim: m.evidence.Keep != nil,
		Gate:            m.gate.Run != nil,
		Processes:       m.processes.Manage != nil,
		Skills:          m.skills != nil,
		Todos:           m.todos.Manage != nil,
		Scope:           m.scope != nil,
		Notebook:        m.notebook != nil,
		Ask:             m.asks,
	}
}
