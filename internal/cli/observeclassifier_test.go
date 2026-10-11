package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/storage"
)

// classifierFixture records two sessions through the recorder every surface
// uses, one per backend, with the person's answers as the card writes them:
// the answer to a denial is the next decision under reason user, and the
// answer to an allow is a deny under reason user-stop.
func classifierFixture(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	session := func(backend string, cost float64, rows func(obs observe.Observer)) {
		rec := startObserveRecorder(db, "code", "openai", "gpt-test", nil)
		var cfg config.Config
		cfg.Behavior.ClassifierBackend = backend
		rec.stamp("the prompt", 0, "/repo", sessionSettings(cfg, runSettings{classifier: true, model: "gpt-6-luna"}))
		obs := rec.observer()
		rows(obs)
		obs.ClassifierCost(cost)
		rec.end()
	}
	at := func(round int64) observe.Pos { return observe.Pos{Turn: 1, Round: round} }

	session("completion", 0.09, func(obs observe.Observer) {
		// A denial the person allowed at the card.
		obs.Decided(at(1), observe.DecisionDeny, observe.ReasonClassifier, time.Second)
		obs.Decision(at(1), observe.DecisionAllow, observe.ReasonUser)
		// A denial the person upheld.
		obs.Decided(at(2), observe.DecisionDeny, observe.ReasonClassifier, 3*time.Second)
		obs.Decision(at(2), observe.DecisionDeny, observe.ReasonUser)
		// An allow the person stopped.
		obs.Decided(at(3), observe.DecisionAllow, observe.ReasonClassifier, 2*time.Second)
		obs.Decision(at(3), observe.DecisionDeny, observe.ReasonUserStop)
		// A failure reaches no verdict.
		obs.Decided(at(4), observe.DecisionAsk, observe.ReasonClassifierFailed, 30*time.Second)
	})
	session("decisions", 0.03, func(obs observe.Observer) {
		obs.Decided(at(1), observe.DecisionAllow, observe.ReasonClassifier, 400*time.Millisecond)
		obs.Decided(at(2), observe.DecisionAllow, observe.ReasonClassifier, 600*time.Millisecond)
		obs.Decided(at(3), observe.DecisionDeny, observe.ReasonClassifier, 900*time.Millisecond)
		obs.Decision(at(3), observe.DecisionDeny, observe.ReasonUser)
	})
	return db
}

// Each backend is drawn on its own: verdicts, allows and denies, the median
// and worst time, the classifier origin's cost per verdict, and the two
// overturns the record holds, a denial then allowed at the card (the next
// decision row under reason user) and an allow then stopped (a deny row under
// reason user-stop).
func TestObserve_ClassifierBackendsSideBySide(t *testing.T) {
	db := classifierFixture(t)
	backends, err := db.AgentClassifierBackends(time.Now().Add(-time.Hour))
	if err != nil || len(backends) != 2 {
		t.Fatalf("backends = %+v, %v", backends, err)
	}
	by := map[string]storage.AgentClassifierBackend{}
	for _, b := range backends {
		by[b.Backend] = b
	}
	c, d := by["completion"], by["decisions"]
	if c.Verdicts != 3 || c.Allows != 1 || c.Denies != 2 || c.MedianMs != 2000 || c.WorstMs != 3000 {
		t.Errorf("completion = %+v", c)
	}
	if c.Overturns.Judged != 2 || c.Overturns.Overturned != 1 || c.Stopped != 1 {
		t.Errorf("completion overturns = %+v, stopped %d", c.Overturns, c.Stopped)
	}
	if d.Verdicts != 3 || d.Allows != 2 || d.Denies != 1 || d.MedianMs != 600 || d.WorstMs != 900 {
		t.Errorf("decisions = %+v", d)
	}
	if d.Overturns.Judged != 1 || d.Overturns.Overturned != 0 || d.Stopped != 0 {
		t.Errorf("decisions overturns = %+v, stopped %d", d.Overturns, d.Stopped)
	}
	if c.Cost != 0.09 || d.Cost != 0.03 {
		t.Errorf("costs = %v, %v", c.Cost, d.Cost)
	}

	body := observeClassifierReport(backends, "7d").Render(100)
	for _, want := range []string{"COMPLETION", "DECISIONS", "3 verdicts", "1 allowed · 2 denied",
		"median 2.0s", "worst 3.0s", "$0.03 per verdict", "$0.01 per verdict",
		"1 of 2 judged denials allowed at the card", "1 of 1 allow stopped while the call ran"} {
		if !strings.Contains(body, want) {
			t.Errorf("report lacks %q:\n%s", want, body)
		}
	}
}

// Two backends in the window split a comparison by backend, and the header
// says so; one backend leaves it as it was.
func TestObserveCompare_SplitsByClassifierBackend(t *testing.T) {
	db := observeCompareTestDB(t)
	for _, backend := range []string{"completion", "decisions"} {
		for _, hash := range []string{"aaa", "bbb"} {
			for i := 0; i < compareMinSessions; i++ {
				id, err := db.StartAgentSession("chat", "openai", "gpt-test")
				if err != nil {
					t.Fatal(err)
				}
				if err := db.StampAgentSession(id, storage.AgentProvenance{PromptHash: hash,
					Settings: storage.AgentSettings{ConfigHash: "cfg", ClassifierBackend: backend}}); err != nil {
					t.Fatal(err)
				}
				if err := db.RecordAgentEvent(id, storage.AgentEvent{Kind: storage.AgentEventTurn,
					Outcome: observe.TurnDone, Turn: 1, Round: 2}); err != nil {
					t.Fatal(err)
				}
				if err := db.EndAgentSession(id, observe.SessionCompleted); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	data, err := readObserveCompare(db, "30d", "prompt_hash", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Backends) != 2 || data.Earlier != nil {
		t.Fatalf("backends = %d, pooled earlier = %v", len(data.Backends), data.Earlier)
	}
	for _, b := range data.Backends {
		if !b.Comparable || b.Earlier.Sessions != compareMinSessions || b.Later.Sessions != compareMinSessions {
			t.Errorf("%s: %+v", b.Backend, b.observeCompareData)
		}
	}
	body := observeCompareReport(data).Render(100)
	for _, want := range []string{"by classifier backend: completion, decisions", "COMPLETION · COHORTS", "DECISIONS · COHORTS"} {
		if !strings.Contains(body, want) {
			t.Errorf("report lacks %q:\n%s", want, body)
		}
	}

	// A window on one backend is one comparison, unlabelled.
	one := observeCompareTestDB(t)
	writeObserveCohort(t, one, "aaa", 11, 2, 8, observe.SessionCompleted)
	writeObserveCohort(t, one, "bbb", 12, 2, 4, observe.SessionCompleted)
	data, err = readObserveCompare(one, "30d", "prompt_hash", time.Now().Add(-time.Hour))
	if err != nil || len(data.Backends) != 0 || data.Earlier == nil {
		t.Fatalf("one backend: %+v, %v", data, err)
	}
}
