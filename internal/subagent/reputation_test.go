package subagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/web"
)

// A child's fetch takes the host's reading from the same function the
// session's does, so a host the lists vouch for is as quiet in an auto-mode
// child as in the session, a host they warn about carries the warning to the
// classifier's resolution, and a manual child still asks
// (docs/capabilities/approvals-and-safety.md#a-host-is-read-against-the-world-before-it-is-judged).
func TestChildFetchIsReadAgainstTheLists(t *testing.T) {
	dir := t.TempDir()
	stamp := time.Now().UTC().Format(time.RFC3339)
	for _, name := range web.HostListNames() {
		if name == web.BuiltinHosts {
			continue
		}
		body := "#shhh-hosts 1 " + name + " " + stamp + "\n"
		if name == "urlhaus" {
			body += "bad.test\n"
		}
		if err := os.WriteFile(filepath.Join(dir, name+".hosts"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	web.UseReputation(web.OpenReputation(dir, nil))
	t.Cleanup(func() { web.UseReputation(nil) })

	sup := New(context.Background(), Options{Root: t.TempDir()})
	t.Cleanup(sup.Close)
	sup.SetParentMode(agent.ModeAuto)
	action := func(rawURL string) agent.Action {
		t.Helper()
		a, err := actionFor(web.FetchToolName, json.RawMessage(`{"url":"`+rawURL+`"}`))
		if err != nil {
			t.Fatalf("actionFor: %v", err)
		}
		return a
	}

	auto := &child{mode: agent.ModeAuto}
	if got, reason := sup.childPolicy(auto).Decide(action("https://pkg.go.dev/context")); got != agent.Allow ||
		reason != "known host (built-in list)" {
		t.Fatalf("an auto child's known host = %v (%q); want Allow by the reading", got, reason)
	}
	if got, _ := sup.childPolicy(auto).Decide(action("https://nowhere.test/")); got != agent.Ask {
		t.Fatalf("an auto child's unknown host = %v; want Ask", got)
	}
	listed := action("https://bad.test/x")
	if listed.Reading.Standing != web.StandingListed {
		t.Fatalf("the child's action did not carry the reading: %+v", listed.Reading)
	}
	if got, _ := agent.ResolveAuto(listed, agent.ClassifierVerdict{Decision: agent.Allow}); got != agent.Ask {
		t.Fatalf("a listed host the classifier allowed = %v; want Ask", got)
	}
	manual := &child{mode: agent.ModeManual}
	sup.SetParentMode(agent.ModeManual)
	if got, _ := sup.childPolicy(manual).Decide(action("https://pkg.go.dev/context")); got != agent.Ask {
		t.Fatalf("a manual child's known host = %v; want Ask", got)
	}
}
