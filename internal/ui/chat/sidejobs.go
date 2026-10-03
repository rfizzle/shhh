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
// It has no clear and no update. The judge is wired once (WithClassifier),
// and the cancel is set when a check starts (approval.go) and dropped by
// whatever ends it: the verdict landing (turn.go), the cancel chord skipping
// the check (keyroute.go), a session boundary (newsession.go) or the quit
// (stopSideJobs).
type classifierState struct {
	judge  *agent.Classifier
	cancel context.CancelFunc
}

// stopSideJobs cancels the side jobs a leaving session stops: the
// classifier's check, the model list query and a /run command. They are the
// set quitNow has always cancelled; the summary's and the title's readings
// are not in it, and a session boundary (newsession.go) and a new slot
// (resetTitle) stop their own sets. Every cancel is nil-safe, so the idle
// path shares it.
func (m *Model) stopSideJobs() {
	if m.runCancel != nil {
		m.runCancel()
	}
	if m.classifier.cancel != nil {
		m.classifier.cancel()
	}
	// The model list is a request to the provider that a leaving session has
	// nothing left to do with, and it is the one surface that can be holding
	// one (picker.go).
	if m.picker.models.cancel != nil {
		m.picker.models.cancel()
		m.picker.models.cancel = nil
	}
}
