package cli

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/storage"
)

// A verdict the classifier reached carries the time it took as the row's
// duration, whichever backend reached it, and a verdict nothing timed leaves
// the column empty. The session row names the backend beside the model, and
// a comparison can split on it.
func TestObserve_ClassifierVerdictsCarryTheirTime(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	for _, backend := range []string{"", "decisions"} {
		rec := startObserveRecorder(db, "code", "openai", "gpt-test", nil)
		var cfg config.Config
		cfg.Behavior.ClassifierBackend = backend
		rec.stamp("the prompt", 0, "/repo", sessionSettings(cfg, runSettings{classifier: true, model: "gpt-6-luna"}))
		obs := rec.observer()
		obs.Decided(observe.Pos{Turn: 1, Round: 1}, observe.DecisionDeny, observe.ReasonClassifier, 1500*time.Millisecond)
		obs.Decided(observe.Pos{Turn: 1, Round: 2}, observe.DecisionAsk, observe.ReasonClassifierFailed, 30*time.Second)
		obs.Decided(observe.Pos{Turn: 1, Round: 2}, observe.DecisionAllow, observe.ReasonUser, 0)
		rec.end()

		events, err := db.AgentSessionEvents(rec.id)
		if err != nil || len(events) != 3 {
			t.Fatalf("%q: events = %+v, %v", backend, events, err)
		}
		for i, want := range []int64{1500, 30000, -1} {
			got := events[i].DurationMs
			switch {
			case want < 0 && got != nil:
				t.Errorf("%q: an untimed verdict carries %d ms", backend, *got)
			case want >= 0 && (got == nil || *got != want):
				t.Errorf("%q: verdict %d carries %v, want %d ms", backend, i, got, want)
			}
		}

		s, ok, err := db.AgentSession(rec.id)
		if err != nil || !ok || s.Settings == nil {
			t.Fatalf("%q: session = %+v, %v, %v", backend, s, ok, err)
		}
		want := "completion"
		if backend != "" {
			want = backend
		}
		if s.Settings.ClassifierBackend != want || s.Settings.ClassifierModel != "gpt-6-luna" {
			t.Fatalf("%q: settings = %+v", backend, s.Settings)
		}
	}
	if !slices.Contains(storage.AgentSplitKeys(), "classifier_backend") {
		t.Fatal("a comparison cannot split on the classifier's backend")
	}
	// A surface with no classifier records no backend.
	if got := sessionSettings(config.Config{}, runSettings{}); got.ClassifierBackend != "" {
		t.Fatalf("no classifier, backend %q", got.ClassifierBackend)
	}
}
