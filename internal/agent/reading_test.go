package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/web"
)

// The fixture readings, one per standing, as the lists would have written
// them.
var (
	readKnown      = web.Reading{Standing: web.StandingKnown, Source: "tranco"}
	readBuiltin    = web.Reading{Standing: web.StandingKnown, Source: web.BuiltinHosts}
	readUnknown    = web.Reading{Standing: web.StandingUnknown}
	readYoung      = web.Reading{Standing: web.StandingYoung, Source: "nrd"}
	readDisposable = web.Reading{Standing: web.StandingDisposable, Source: "disposable"}
	readListed     = web.Reading{Standing: web.StandingListed, Source: "urlhaus"}
)

func fetchOf(host string, r web.Reading) Action {
	return Action{Kind: ActionFetch, Host: host, Reading: r}
}

// The reading advises and never widens: a known host is answered in auto mode
// alone, where it stands in for the classifier; every other mode asks exactly
// as it did; and the person's own lists outrank it both ways.
func TestDecideReadsTheHostsStandingInAutoModeOnly(t *testing.T) {
	for _, c := range []struct {
		name   string
		policy ModePolicy
		action Action
		want   Decision
		reason string
	}{
		{"auto, known by the ranking", ModePolicy{Mode: ModeAuto}, fetchOf("example.com", readKnown), Allow, "known host (tranco top 10k)"},
		{"auto, known by shhh's list", ModePolicy{Mode: ModeAuto}, fetchOf("go.dev", readBuiltin), Allow, "known host (built-in list)"},
		{"auto, unknown", ModePolicy{Mode: ModeAuto}, fetchOf("nowhere.test", readUnknown), Ask, ""},
		{"auto, no reading at all", ModePolicy{Mode: ModeAuto}, fetchOf("nowhere.test", web.Reading{}), Ask, ""},
		{"auto, young", ModePolicy{Mode: ModeAuto}, fetchOf("new.test", readYoung), Ask, ""},
		{"auto, disposable", ModePolicy{Mode: ModeAuto}, fetchOf("tmp.test", readDisposable), Ask, ""},
		{"auto, listed", ModePolicy{Mode: ModeAuto}, fetchOf("bad.test", readListed), Ask, ""},
		// The reading is for a classifier, and manual has none: a person
		// still answers for every host.
		{"manual, known", ModePolicy{Mode: ModeManual}, fetchOf("example.com", readKnown), Ask, ""},
		{"accept-edits, known", ModePolicy{Mode: ModeAcceptEdits}, fetchOf("example.com", readBuiltin), Ask, ""},
		{"read-only, known", ModePolicy{Mode: ModeReadOnly}, fetchOf("example.com", readKnown), Deny, "read-only mode"},
		// The person's lists answer first, whatever the reading says.
		{"deny list over known", ModePolicy{Mode: ModeAuto, DenyHosts: []string{"example.com"}},
			fetchOf("example.com", readKnown), Deny, DenyReasonHost},
		{"allow list over listed", ModePolicy{Mode: ModeAuto, AllowHosts: []string{"bad.test"}},
			fetchOf("bad.test", readListed), Allow, "session grant"},
		{"allow list over young, in manual", ModePolicy{Mode: ModeManual, AllowHosts: []string{"new.test"}},
			fetchOf("new.test", readYoung), Allow, "session grant"},
		// A conversation reads without asking, and the reading reaches it only
		// through the deny list it already answers.
		{"conversation, listed", ModePolicy{Conversation: true}, fetchOf("bad.test", readListed), Allow, ConversationReadReason},
		// A reading on anything but a fetch means nothing.
		{"a command is not a fetch", ModePolicy{Mode: ModeAuto},
			Action{Kind: ActionCommand, Command: "make deploy", Reading: readKnown}, Ask, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, reason := c.policy.Decide(c.action)
			if got != c.want || reason != c.reason {
				t.Fatalf("Decide = %v %q, want %v %q", got, reason, c.want, c.reason)
			}
		})
	}
}

// Where the classifier would have let a fetch through, a host the lists warn
// about is put to the person, with the list that said so. The classifier's
// no stands, and a reading that warns about nothing changes nothing.
func TestResolveAutoPutsAWarnedHostToThePerson(t *testing.T) {
	allow := ClassifierVerdict{Decision: Allow, Reason: "reads the docs"}
	deny := ClassifierVerdict{Decision: Deny, Reason: "unrelated"}
	for _, c := range []struct {
		name    string
		action  Action
		verdict ClassifierVerdict
		want    Decision
		reason  string
	}{
		{"young", fetchOf("new.test", readYoung), allow, Ask, "registered in the last 10 days (nrd)"},
		{"disposable", fetchOf("tmp.test", readDisposable), allow, Ask, "disposable domain (disposable list)"},
		{"listed", fetchOf("bad.test", readListed), allow, Ask, "listed host (urlhaus)"},
		{"unknown", fetchOf("nowhere.test", readUnknown), allow, Allow, "reads the docs"},
		{"known", fetchOf("example.com", readKnown), allow, Allow, "reads the docs"},
		{"the classifier's no stands", fetchOf("bad.test", readListed), deny, Deny, "unrelated"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, reason := ResolveAuto(c.action, c.verdict)
			if got != c.want || reason != c.reason {
				t.Fatalf("ResolveAuto = %v %q, want %v %q", got, reason, c.want, c.reason)
			}
		})
	}
	// With nobody to ask, the card is a refusal naming the list.
	got, reason := ResolveUnattended(fetchOf("new.test", readYoung), allow)
	if got != Deny || reason != "registered in the last 10 days (nrd)" {
		t.Fatalf("ResolveUnattended = %v %q", got, reason)
	}
}

// The classifier is shown the standing beside the proposed call, so its
// judgement rests on something about where the request goes.
func TestClassifierIsShownTheHostsStanding(t *testing.T) {
	for _, reading := range []web.Reading{readKnown, readUnknown, readYoung, readDisposable, readListed} {
		p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
			return eventsOf(decisionCall(`{"decision":"allow","reason":"ok"}`)), nil
		}}
		req := ClassifierRequest{Tool: web.FetchToolName, Arguments: `{"url":"https://h.test/"}`, Reading: reading}
		NewClassifier(p, ClassifierConfig{Model: "m"}).Judge(context.Background(), req)
		evidence := p.msgs[1].Content
		if !strings.Contains(evidence, `"host_standing":"`+string(reading.Standing)+`"`) {
			t.Errorf("%s: the evidence does not carry the standing: %s", reading.Standing, evidence)
		}
		if reading.Source != "" && !strings.Contains(evidence, `"host_standing_source":"`+reading.Source+`"`) {
			t.Errorf("%s: the evidence does not name the list: %s", reading.Standing, evidence)
		}
		if !strings.Contains(p.msgs[0].Content, "host_standing") {
			t.Error("the instruction does not say what the standing is")
		}
	}
	// A call that is not a fetch carries none.
	p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		return eventsOf(decisionCall(`{"decision":"allow","reason":"ok"}`)), nil
	}}
	NewClassifier(p, ClassifierConfig{Model: "m"}).Judge(context.Background(), testRequest())
	if strings.Contains(p.msgs[1].Content, "host_standing") {
		t.Error("a command's evidence carries a host standing")
	}
}
