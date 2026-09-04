package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/hook"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// jsonTags is a struct's JSON field names, mapped to the Go type behind each.
func jsonTags(t *testing.T, v any) map[string]string {
	t.Helper()
	rt := reflect.TypeOf(v)
	out := map[string]string{}
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		out[name] = rt.Field(i).Type.String()
	}
	return out
}

// A hook author should learn one shape, and it should be the one the record
// already fixes. Every field the payload shares with the event stream is
// spelled the same way and carries the same kind of value; a rename on either
// side would otherwise leave a hook reading a field that stopped arriving.
// See docs/capabilities/hooks.md#the-payload-is-the-event-stream.
func TestHookPayload_SharesTheEventStreamsSpelling(t *testing.T) {
	stream := jsonTags(t, jsonEvent{})
	payload := jsonTags(t, hook.Payload{})
	shared := []string{"turn", "round", "id", "tool", "arguments", "result", "outcome", "final"}
	for _, name := range shared {
		want, ok := stream[name]
		if !ok {
			t.Fatalf("the event stream no longer has a %q field; the payload still promises one", name)
		}
		got, ok := payload[name]
		if !ok {
			t.Fatalf("the payload no longer carries %q, which the stream does", name)
		}
		if got != want {
			t.Errorf("%q is %s on the stream and %s in the payload", name, want, got)
		}
	}
	// And the payload's own three: what a hook cannot work out for itself,
	// because it is a separate process.
	for _, name := range []string{"event", "session", "cwd"} {
		if _, ok := payload[name]; !ok {
			t.Errorf("the payload should carry %q", name)
		}
	}
}

// A checkout's hooks are a command line that runs as whoever cloned it, so
// they load only where the checkout is trusted — and the doctor reads the
// same two files a session does.
func TestHookSet_ReadsTheCheckoutOnlyWhenItIsTrusted(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, project.StateDir)
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"hooks":{"guard":{"event":"pre_tool","command":"guard"}}}`
	if err := os.WriteFile(filepath.Join(state, "hooks.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	cfg.Hooks.Entries = map[string]hook.Entry{"mine": {Event: hook.PostTool, Command: "mine"}}

	withProjectTrust(t, project.Trust{Root: root})
	if got := hookSet(cfg).Len(); got != 1 {
		t.Fatalf("an untrusted checkout contributes nothing, got %d hooks", got)
	}
	withProjectTrust(t, project.Trust{Root: root, Granted: true})
	if got := hookSet(cfg).Len(); got != 2 {
		t.Fatalf("a trusted checkout's hooks should load, got %d", got)
	}

	cfg.Hooks.Disabled = true
	if got := hookSet(cfg).Len(); got != 0 {
		t.Fatalf("hooks.disabled fires nothing, got %d", got)
	}
}

// The doctor states what a session here would fire, and reports an entry that
// will not load as a warning rather than a fault: the session starts, with
// less in it.
func TestDoctorHooks_StatesWhatWouldFire(t *testing.T) {
	empty := doctorHooks(nil, time.Minute)
	if empty.State != components.DoctorSkipped || !strings.Contains(empty.Subject, "no hooks") {
		t.Fatalf("a machine with no hooks is not a fault: %+v", empty)
	}

	set := hook.Load(map[string]hook.Entry{
		"fmt":  {Event: hook.PostTool, Matcher: "edit_file", Command: "gofmt -l ."},
		"bad":  {Event: "after_lunch", Command: "x"},
		"stop": {Event: hook.Stop, Command: "say done"},
	}, "config.toml", "")
	f := doctorHooks(set, 30*time.Second)
	if !strings.Contains(f.Subject, "2 hooks") {
		t.Errorf("the subject should count what loaded: %q", f.Subject)
	}
	for _, want := range []string{hook.PostTool, hook.Stop, "30s"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("the detail should name %q: %q", want, f.Detail)
		}
	}
	if f.Outcome != "unreadable" || len(f.Fix) != 1 {
		t.Errorf("an entry that did not load should be reported: %+v", f)
	}
	if f.State == components.DoctorFailed {
		t.Error("a hook that will not load is a warning, not a failed check")
	}
}

// The mutation seam holds one function and two things want it.
func TestChainMutation_RunsBothInOrder(t *testing.T) {
	first := func(_ string, _ json.RawMessage, r string) string { return r + " one" }
	second := func(_ string, _ json.RawMessage, r string) string { return r + " two" }
	if got := chainMutation(first, second)("edit_file", nil, "ok"); got != "ok one two" {
		t.Errorf("want both in order, got %q", got)
	}
	if got := chainMutation(nil, second)("edit_file", nil, "ok"); got != "ok two" {
		t.Errorf("one hook alone should still run, got %q", got)
	}
	if chainMutation(nil, nil) != nil {
		t.Error("nothing to chain should leave the seam empty")
	}
}

// An unattended run has nobody to ask, so a hook that asked — or that broke
// on a call it was asked about — refuses. That is this surface's answer to
// every question it cannot put to a person.
func TestHookApprover_AnUnattendedRunRefusesWhatItCannotAsk(t *testing.T) {
	set := hook.Load(map[string]hook.Entry{
		"guard": {Event: hook.PreTool, Command: "guard"},
	}, "config.toml", "")
	exec := func(context.Context, string, []byte) (string, int, error) { return "boom\n", 1, nil }
	r := hook.NewRunner(set, exec, time.Second, "/work")

	var decisions []string
	ran := false
	resolve := hookApprover(r, func() hook.Pos { return hook.Pos{Turn: 1} },
		func(decision, reason string) { decisions = append(decisions, decision+"/"+reason) },
		func(provider.ToolCall) string { ran = true; return "ran" })

	got := resolve(provider.ToolCall{Name: "execute_command", Arguments: `{"command":"go build"}`})
	if ran {
		t.Fatal("a run with nobody to ask must not run what a broken hook covered")
	}
	if !strings.Contains(got, "guard") {
		t.Fatalf("the model should be told which hook: %q", got)
	}
	if len(decisions) != 1 || decisions[0] != "deny/hook" {
		t.Fatalf("the refusal should be recorded as the hook's: %v", decisions)
	}
}

// The approver is the dispatcher for a run's gated calls, so it is both
// seams: a command, a fetch or a server call meets the hook behind it there,
// because nothing else in an unattended run ever sees the result.
func TestHookApprover_FiresBothSeamsAroundAGatedCall(t *testing.T) {
	for _, c := range []struct {
		name  string
		tool  string
		seams []string
	}{
		{"a command", "execute_command", []string{hook.PreTool, hook.PostTool}},
		{"a server call", "docs__search", []string{hook.PreTool, hook.PostTool}},
		// A write is dispatched through the mutating tools and meets the seam
		// behind it on the mutation hook, which is the one place a write can
		// be seen; firing here too would put one call to one hook twice.
		{"a write", "write_file", []string{hook.PreTool}},
	} {
		var seen []string
		exec := func(_ context.Context, _ string, stdin []byte) (string, int, error) {
			var p hook.Payload
			if err := json.Unmarshal(stdin, &p); err != nil {
				t.Fatal(err)
			}
			seen = append(seen, p.Event)
			return "", 0, nil
		}
		set := hook.Load(map[string]hook.Entry{
			"before": {Event: hook.PreTool, Command: "before"},
			"after":  {Event: hook.PostTool, Command: "after"},
		}, "config.toml", "")
		r := hook.NewRunner(set, exec, time.Second, "/work")
		resolve := hookApprover(r, func() hook.Pos { return hook.Pos{Turn: 1} }, nil,
			func(provider.ToolCall) string { return "ran" })

		resolve(provider.ToolCall{Name: c.tool, Arguments: `{"path":"a.go"}`})
		if len(seen) != len(c.seams) {
			t.Errorf("%s met %v, want %v", c.name, seen, c.seams)
			continue
		}
		for i, want := range c.seams {
			if seen[i] != want {
				t.Errorf("%s met %v, want %v", c.name, seen, c.seams)
				break
			}
		}
	}
}

// An unattended run cannot put a call to anybody, so a hook that asked stops
// it — and says so as an ask rather than as a permanent refusal.
func TestHookApprover_AnAskIsNotSaidAsARefusal(t *testing.T) {
	set := hook.Load(map[string]hook.Entry{
		"guard": {Event: hook.PreTool, Command: "guard"},
	}, "config.toml", "")
	exec := func(context.Context, string, []byte) (string, int, error) {
		return `{"decision":"ask"}`, 0, nil
	}
	r := hook.NewRunner(set, exec, time.Second, "/work")
	ran := false
	resolve := hookApprover(r, func() hook.Pos { return hook.Pos{} }, nil,
		func(provider.ToolCall) string { ran = true; return "ran" })

	got := resolve(provider.ToolCall{Name: "execute_command", Arguments: `{"command":"go build"}`})
	if ran {
		t.Fatal("a call a hook asked about must not run where nobody can answer")
	}
	if got != hook.AskedResult("guard") {
		t.Fatalf("an ask should not read as a refusal: %q", got)
	}
}
