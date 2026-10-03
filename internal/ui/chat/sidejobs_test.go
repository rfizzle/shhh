package chat

import (
	"context"
	"testing"
)

// TestStopSideJobs_CancelsEveryOne holds that a leaving session stops every
// side job it started, each exactly once, and drops every cancel it ran.
func TestStopSideJobs_CancelsEveryOne(t *testing.T) {
	calls := map[string]int{}
	record := func(name string) func() {
		return func() { calls[name]++ }
	}
	var m Model
	m.runCancel = record("run")
	m.classifier.cancel = record("classifier")
	m.picker.models.cancel = record("models")
	m.summary.cancel = record("summary")
	m.titles.cancel = record("title")

	m.stopSideJobs()

	for _, name := range []string{"run", "classifier", "models", "summary", "title"} {
		if calls[name] != 1 {
			t.Errorf("%s cancel ran %d times, want 1", name, calls[name])
		}
	}
	for name, cancel := range map[string]context.CancelFunc{
		"run":        m.runCancel,
		"classifier": m.classifier.cancel,
		"models":     m.picker.models.cancel,
		"summary":    m.summary.cancel,
		"title":      m.titles.cancel,
	} {
		if cancel != nil {
			t.Errorf("%s cancel is still set after stopSideJobs, want nil", name)
		}
	}
}
