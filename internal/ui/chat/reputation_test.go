package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/web"
)

// useFixtureReputation installs a host reading over fresh fixture lists for
// one test: each list names what lists gives it and nothing else, written in
// the on-disk form and dated now, so nothing is stale and nothing is asked
// for again.
func useFixtureReputation(t *testing.T, lists map[string][]string) {
	t.Helper()
	dir := t.TempDir()
	stamp := time.Now().UTC().Format(time.RFC3339)
	for _, name := range web.HostListNames() {
		if name == web.BuiltinHosts {
			continue
		}
		body := "#shhh-hosts 1 " + name + " " + stamp + "\n"
		for _, h := range lists[name] {
			body += h + "\n"
		}
		if err := os.WriteFile(filepath.Join(dir, name+".hosts"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	web.UseReputation(web.OpenReputation(dir, nil))
	t.Cleanup(func() { web.UseReputation(nil) })
}

func recordingDecisions(m Model, into *[][2]string) Model {
	return m.WithObserver(observe.Observer{Decision: func(_ observe.Pos, decision, reason string) {
		*into = append(*into, [2]string{decision, reason})
	}})
}

// In auto mode a host the lists know is let through without the classifier
// being asked, and the row says which list vouched for it.
func TestReputation_AutoModeLetsAKnownHostThrough(t *testing.T) {
	useFixtureReputation(t, nil)
	judge := &verdictProvider{decision: "deny", reason: "should not be asked"}
	ledger := meter.New(nil)
	var fetched int
	executor := func(string, json.RawMessage) (string, error) { fetched++; return "page text", nil }
	var decisions [][2]string
	m := recordingDecisions(gatedModel(t, executor, fetchPreviews()).
		WithClassifier(agent.NewClassifier(ledger.For(judge, meter.SourceClassifier), agent.ClassifierConfig{Model: "judge"})),
		&decisions)
	m.policy.mode = agent.ModeAuto

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		fetchCall("call_1", "https://pkg.go.dev/context"),
	}})
	m = updated.(Model)
	if m.state == stateConfirmRun || m.state == stateClassifying || judge.calls != 0 {
		t.Fatalf("a known host was put to a card or a classifier (state %d, judged %d)", m.state, judge.calls)
	}
	if m.pendingApproval == nil || m.pendingApproval.autoRule != "known host (built-in list)" {
		t.Fatalf("the fetch was not allowed by the reading: %+v", m.pendingApproval)
	}
	m = drainApproved(t, m, cmd)
	if fetched != 1 {
		t.Fatalf("fetched %d times, want once", fetched)
	}
	var allowed string
	for _, e := range m.transcript {
		if row := m.activityRowFor(e); strings.Contains(row.Target, "pkg.go.dev") {
			allowed = row.Allowed
		}
	}
	if allowed != "auto-allowed · known host (built-in list)" {
		t.Fatalf("the row does not say which list vouched for the host: %q", allowed)
	}
	want := [2]string{observe.DecisionAllow, observe.ReasonHostKnown}
	if len(decisions) != 1 || decisions[0] != want {
		t.Fatalf("recorded %v, want %v", decisions, want)
	}
}

// The reading is for a classifier, not a person: manual mode asks about a
// known host as it always has.
func TestReputation_ManualModeStillAsks(t *testing.T) {
	useFixtureReputation(t, nil)
	m := gatedModel(t, func(string, json.RawMessage) (string, error) { return "", nil }, fetchPreviews())
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{fetchCall("call_1", "https://pkg.go.dev/context")}})
	if m = updated.(Model); m.state != stateConfirmRun {
		t.Fatalf("manual mode let a known host through (state %d)", m.state)
	}
}

// Where the classifier would have let a fetch through, a host a list warns
// about is put to the person instead, and the card says which list and why.
func TestReputation_AWarnedHostIsCardedWhereTheClassifierAllowed(t *testing.T) {
	for _, c := range []struct {
		name, list, host, reason, code string
	}{
		{"listed", "urlhaus", "bad.test", "listed host (urlhaus)", observe.ReasonHostListed},
		{"young", "nrd", "brand-new.test", "registered in the last 10 days (nrd)", observe.ReasonHostYoung},
		{"disposable", "disposable", "throwaway.test", "disposable domain (disposable list)", observe.ReasonHostDisposable},
	} {
		t.Run(c.name, func(t *testing.T) {
			useFixtureReputation(t, map[string][]string{c.list: {c.host}})
			judge := &verdictProvider{decision: "allow", reason: "reads a page"}
			ledger := meter.New(nil)
			var decisions [][2]string
			m := recordingDecisions(gatedModel(t, func(string, json.RawMessage) (string, error) { return "", nil }, fetchPreviews()).
				WithClassifier(agent.NewClassifier(ledger.For(judge, meter.SourceClassifier), agent.ClassifierConfig{Model: "judge"})),
				&decisions)
			m.policy.mode = agent.ModeAuto

			updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{fetchCall("call_1", "https://"+c.host+"/x")}})
			m = updated.(Model)
			if m.state != stateClassifying {
				t.Fatalf("an unknown-to-policy fetch in auto mode should be classified, got state %d", m.state)
			}
			updated, _ = m.Update(driveClassifierDone(t, cmd))
			m = updated.(Model)
			if m.state != stateConfirmRun {
				t.Fatalf("the classifier's yes let a %s host through (state %d)", c.name, m.state)
			}
			found := false
			for _, f := range m.pendingApproval.fields {
				if f.Label == "standing" && f.Value == c.reason {
					found = true
				}
			}
			if !found {
				t.Fatalf("the card does not say the standing: %+v", m.pendingApproval.fields)
			}
			want := [2]string{observe.DecisionAsk, c.code}
			if len(decisions) != 1 || decisions[0] != want {
				t.Fatalf("recorded %v, want %v", decisions, want)
			}
		})
	}
}

// An unknown host changes nothing: the classifier's yes stands.
func TestReputation_AnUnknownHostLeavesTheClassifiersAnswer(t *testing.T) {
	useFixtureReputation(t, nil)
	judge := &verdictProvider{decision: "allow", reason: "reads a page"}
	ledger := meter.New(nil)
	m := gatedModel(t, func(string, json.RawMessage) (string, error) { return "", nil }, fetchPreviews()).
		WithClassifier(agent.NewClassifier(ledger.For(judge, meter.SourceClassifier), agent.ClassifierConfig{Model: "judge"}))
	m.policy.mode = agent.ModeAuto
	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{fetchCall("call_1", "https://nowhere.test/x")}})
	m = updated.(Model)
	updated, _ = m.Update(driveClassifierDone(t, cmd))
	m = updated.(Model)
	if m.state == stateConfirmRun || m.pendingApproval == nil || m.pendingApproval.autoRule != classifierRule {
		t.Fatalf("an unknown host changed the classifier's answer (state %d)", m.state)
	}
}
