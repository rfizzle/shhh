package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/profile"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/testhttp"
	"github.com/rfizzle/shhh/internal/todo"
)

// registerProfile writes body as a profile file, loads it and registers it
// as startup does, withdrawing it when the test ends.
func registerProfile(t *testing.T, body string) []profile.Profile {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gw.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := profile.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	profile.Register(loaded)
	t.Cleanup(func() { profile.Register(nil) })
	return loaded
}

// From a profile file, through profile.Register and the profile's own
// provider, to the bodies the classifier and the backlog reading send on one
// model: the scoped flow gets its tool and no format, the other its format.
func TestProfile_AFlowScopeReachesTheRequest(t *testing.T) {
	var fixtures testhttp.Registry
	var bodies []map[string]any
	srv := fixtures.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-5-5\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	loaded := registerProfile(t, `
name     = "gw"
base_url = "`+srv.URL+`"
api      = "anthropic-messages"
api_key  = "sk-test"

[[models]]
id    = "claude-sonnet-5-5"
flows = { classifier = { structured_outputs = false } }
`)
	p, err := profile.New(loaded[0], provider.ResolveOpts{Model: "claude-sonnet-5-5", HTTPClient: fixtures.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	agent.NewClassifier(p, agent.ClassifierConfig{Model: "claude-sonnet-5-5"}).Judge(ctx, agent.ClassifierRequest{Tool: "execute_command", Arguments: `{"command":"ls"}`})
	if len(bodies) == 0 {
		t.Fatal("the classifier sent nothing")
	}
	classifier := bodies[0]
	outCfg, _ := classifier["output_config"].(map[string]any)
	if _, ok := outCfg["format"]; ok {
		t.Errorf("classifier: no format may be sent, got %v", outCfg["format"])
	}
	if tools, _ := classifier["tools"].([]any); len(tools) != 1 {
		t.Errorf("classifier: its tool must be offered, got %v", classifier["tools"])
	}

	bodies = nil
	todo.NewExtractor(p, todo.ExtractConfig{Model: "claude-sonnet-5-5"}, todo.BuiltinCode()).Extract(ctx, todo.ExtractRequest{})
	if len(bodies) == 0 {
		t.Fatal("the backlog reading sent nothing")
	}
	reading := bodies[0]
	outCfg, _ = reading["output_config"].(map[string]any)
	if format, _ := outCfg["format"].(map[string]any); format["type"] != "json_schema" {
		t.Errorf("backlog: the format must be sent, got %v", reading["output_config"])
	}
	if _, ok := reading["tools"]; ok {
		t.Error("backlog: a request carrying a schema must not also offer tools")
	}
}
