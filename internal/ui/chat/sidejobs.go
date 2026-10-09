package chat

import (
	"context"

	"github.com/rfizzle/shhh/internal/agent"
)

// classifierState is auto mode's permission classifier on the session: the
// judge, which reads gated calls the static policy would ask about, and the
// check it has out. A nil judge falls back to asking the user.
//
// It is a value, held by value on the Model and copied with it every frame
// like the rest of the Model, so it takes no pointer of its own: the judge
// and the cancel were fields on the Model before they were gathered here,
// and nothing keys a memo on their identity.
//
// It has no clear and no update. The judge is wired once (the wiring's Classifier),
// and the cancel is set when a check starts (approval.go) and dropped by
// whatever ends it: the verdict landing (turn.go), the cancel chord skipping
// the check (keyroute.go), a session boundary (newsession.go) or the quit
// (stopSideJobs).
type classifierState struct {
	judge  *agent.Classifier
	cancel context.CancelFunc
}

// stopSideJobs cancels the side jobs a leaving session stops: the
// classifier's check, the model list query, a /run command, and the
// summary's and the title's readings. A reading out when the session leaves
// is about a conversation nobody is left to read it for. A session boundary
// (newsession.go) and a new slot (resetTitle) stop their own sets. Every
// cancel is nil-safe, so the idle path shares it, and each is dropped once
// it has run, so every side job is stopped the same way.
func (m *Model) stopSideJobs() {
	if m.runCancel != nil {
		m.runCancel()
		m.runCancel = nil
	}
	if m.classifier.cancel != nil {
		m.classifier.cancel()
		m.classifier.cancel = nil
	}
	// The model list is a request to the provider that a leaving session has
	// nothing left to do with, and it is the one surface that can be holding
	// one (picker.go).
	m.picker.models.stop()
	if m.summary.cancel != nil {
		m.summary.cancel()
		m.summary.cancel = nil
	}
	if m.titles.cancel != nil {
		m.titles.cancel()
		m.titles.cancel = nil
	}
	// And the start screen's reading, which a session leaving before it
	// lands has no screen left to draw it on.
	m.stopStartOffers()
	// And a proposal's wording, which has no list left to open a card on.
	m.stopPatterns()
}
