package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/web"
)

// verdictJudge answers every classifier request with one decision and counts
// how often it was asked.
type verdictJudge struct {
	decision string
	calls    *int
}

func (v verdictJudge) Name() string { return "judge" }

func (v verdictJudge) StreamCompletion(context.Context, []provider.Message, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	*v.calls++
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{Done: true, ToolCalls: []provider.ToolCall{{ID: "d", Name: agent.DecisionToolName,
		Arguments: `{"decision":"` + v.decision + `","reason":"reads a page"}`}}}
	close(ch)
	return ch, nil
}

// An unattended run in auto mode judges a fetch the way the session does:
// the person's host lists first, then the reading, then the classifier —
// whose yes a warned host turns into the refusal a run with nobody to ask
// gives.
func TestAutoJudgeReadsTheHostBeforeTheClassifier(t *testing.T) {
	fetch := func(host string, r web.Reading) agent.Action {
		return agent.Action{Kind: agent.ActionFetch, Host: host, Reading: r}
	}
	known := web.Reading{Standing: web.StandingKnown, Source: web.BuiltinHosts}
	listed := web.Reading{Standing: web.StandingListed, Source: "urlhaus"}
	for _, c := range []struct {
		name     string
		action   agent.Action
		want     agent.Decision
		code     string
		asked    bool
		allow    []string
		deny     []string
		decision string
	}{
		{name: "known", action: fetch("go.dev", known), want: agent.Allow, code: observe.ReasonHostKnown},
		{name: "listed, the classifier said yes", action: fetch("bad.test", listed), want: agent.Deny,
			code: observe.ReasonHostListed, asked: true, decision: "allow"},
		{name: "unknown, the classifier said yes", action: fetch("nowhere.test", web.Reading{Standing: web.StandingUnknown}),
			want: agent.Allow, code: observe.ReasonClassifier, asked: true, decision: "allow"},
		{name: "the allow list over listed", action: fetch("bad.test", listed), allow: []string{"bad.test"},
			want: agent.Allow, code: observe.ReasonSessionScope},
		{name: "the deny list over known", action: fetch("go.dev", known), deny: []string{"go.dev"},
			want: agent.Deny, code: observe.ReasonDenylist},
	} {
		t.Run(c.name, func(t *testing.T) {
			calls := 0
			j := &autoJudge{ctx: t.Context(), allowHosts: c.allow, denyHosts: c.deny,
				classifier: agent.NewClassifier(verdictJudge{decision: c.decision, calls: &calls}, agent.ClassifierConfig{Model: "m"})}
			got, _, code := j.decide(provider.ToolCall{Name: web.FetchToolName, Arguments: `{"url":"https://h/"}`}, c.action)
			if got != c.want || code != c.code {
				t.Fatalf("decide = %v %q, want %v %q", got, code, c.want, c.code)
			}
			if asked := calls > 0; asked != c.asked {
				t.Fatalf("classifier asked = %v, want %v", asked, c.asked)
			}
		})
	}
}

func TestDoctorHostsNamesEachListsAge(t *testing.T) {
	ok := doctorHosts("/home/me/.cache/shhh/hosts", []web.ListState{
		{Name: "builtin", From: "binary"},
		{Name: "urlhaus", From: "download", Age: 5 * time.Hour},
		{Name: "stevenblack", Off: true},
		{Name: "disposable", From: "snapshot", Age: 12 * 24 * time.Hour},
		{Name: "nrd"},
		{Name: "tranco", From: "download", Age: 30 * time.Minute},
	})
	want := "builtin built in · urlhaus 5h · stevenblack off · disposable shipped 12d · nrd not fetched · tranco <1h"
	if ok.Detail != want || ok.Outcome != "ok" || ok.State == components.DoctorWarned {
		t.Fatalf("finding = %+v\nwant detail %q", ok, want)
	}

	stale := doctorHosts("/c/hosts", []web.ListState{
		{Name: "builtin", From: "binary"},
		{Name: "nrd", Stale: true, Age: 4 * 24 * time.Hour, Failed: true},
		{Name: "urlhaus", From: "download", Age: 2 * 24 * time.Hour, Failed: true},
	})
	// A refresh that failed over a copy still inside its window is named,
	// and that list is not one that cannot answer.
	if stale.State != components.DoctorWarned || stale.Outcome != "stale" ||
		!strings.Contains(stale.Detail, "nrd stale 4d, last download failed") ||
		!strings.Contains(stale.Detail, "urlhaus 2d, last download failed") ||
		!strings.HasPrefix(stale.Consequence, "nrd cannot answer") || len(stale.Fix) == 0 {
		t.Fatalf("finding = %+v", stale)
	}
	failedOnly := doctorHosts("/c/hosts", []web.ListState{
		{Name: "urlhaus", From: "download", Age: 2 * 24 * time.Hour, Failed: true},
	})
	if failedOnly.State == components.DoctorWarned || failedOnly.Outcome != "ok" {
		t.Fatalf("a list still answering was reported as unable to: %+v", failedOnly)
	}
	if nowhere := doctorHosts("", nil); nowhere.Subject != "nowhere to cache them" {
		t.Fatalf("finding = %+v", nowhere)
	}
}

// The probe reads the lists the suite seeded, and downloads nothing: they
// are fresh and empty, so every list answers from its download.
func TestProbeHostsReadsTheSeededLists(t *testing.T) {
	f := probeHosts(t.Context(), config.Config{Web: config.WebConfig{ReputationOff: []string{"stevenblack"}}})
	if f.State == components.DoctorWarned || !strings.Contains(f.Detail, "stevenblack off") ||
		!strings.Contains(f.Detail, "tranco <1h") {
		t.Fatalf("finding = %+v", f)
	}
	if _, err := os.Stat(filepath.Join(hostListsDir(), "tranco.hosts")); err != nil {
		t.Fatalf("the seeded list is not where the row looked: %v", err)
	}
}

func TestReputationOffIsJudged(t *testing.T) {
	if err := checkConfigValue("web.reputation_off", "tranco, NRD"); err != nil {
		t.Fatalf("a list's own names were refused: %v", err)
	}
	if err := checkConfigValue("web.reputation_off", "tranco, alexa"); err == nil ||
		!strings.Contains(err.Error(), "alexa") {
		t.Fatalf("an unknown list was accepted: %v", err)
	}
}
