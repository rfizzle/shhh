package profile

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// gatewayProfile is the profile the transport tests exercise: an openai-chat
// endpoint whose proxy layer mangles Gemini tool-call ids.
func gatewayProfile(baseURL string) Profile {
	return Profile{
		Name:    "test-gateway",
		API:     APIOpenAIChat,
		BaseURL: baseURL,
		Headers: map[string]string{"X-Title": "shhh"},
		Rewrite: []Rule{
			{When: Match{Model: "gemini-*"}, Direction: DirectionRequest, Op: OpCutAt, Path: "messages[].tool_calls[].id", Value: "__thought__"},
			{When: Match{Model: "gemini-*"}, Direction: DirectionRequest, Op: OpCutAt, Path: "messages[].tool_call_id", Value: "__thought__"},
		},
	}
}

func TestTransport_RewritesTheRequestBody(t *testing.T) {
	var got map[string]any
	var header string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("X-Title")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client := &http.Client{Transport: NewTransport(gatewayProfile(srv.URL), nil)}
	body := `{"model":"gemini-3.1-pro","messages":[{"role":"tool","tool_call_id":"call_1__thought__YWJj"}]}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if header != "shhh" {
		t.Fatalf("the profile's header should reach the gateway, got %q", header)
	}
	messages := got["messages"].([]any)
	id := messages[0].(map[string]any)["tool_call_id"]
	if id != "call_1" {
		t.Fatalf("the id should have been cut, got %v", id)
	}
}

func TestTransport_LeavesUnmatchedModelsAlone(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	client := &http.Client{Transport: NewTransport(gatewayProfile(srv.URL), nil)}
	body := `{"model":"gpt-4o","messages":[{"role":"tool","tool_call_id":"call_1__thought__YWJj"}]}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	id := got["messages"].([]any)[0].(map[string]any)["tool_call_id"]
	if id != "call_1__thought__YWJj" {
		t.Fatalf("a model outside the rule's glob should be untouched, got %v", id)
	}
}

func TestTransport_IgnoresNonJSONBodies(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		raw = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := gatewayProfile(srv.URL)
	p.Rewrite = append(p.Rewrite, Rule{Direction: DirectionRequest, Op: OpDelete, Path: "anything"})
	client := &http.Client{Transport: NewTransport(p, nil)}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/upload", strings.NewReader("not json at all"))
	req.Header.Set("Content-Type", "text/plain")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if raw != "not json at all" {
		t.Fatalf("a non-JSON body must pass through verbatim, got %q", raw)
	}
}

func TestTransport_RewritesStreamedEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, ": keep-alive comment\n")
		fmt.Fprint(w, "data: {\"model\":\"gemini-3.1-pro\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}],\"finish_reason\":\"eos\"}\n")
		fmt.Fprint(w, "data: [DONE]\n")
		flusher.Flush()
	}))
	defer srv.Close()

	p := gatewayProfile(srv.URL)
	// A gateway that reports a finish reason the client doesn't know: rewrite
	// it on the way in rather than teaching every caller about the gateway.
	p.Rewrite = append(p.Rewrite, Rule{Direction: DirectionResponse, Op: OpSet, Path: "finish_reason", Value: "stop"})

	client := &http.Client{Transport: NewTransport(p, nil)}
	resp, err := client.Get(srv.URL + "/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var lines []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if line := scanner.Text(); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) != 3 {
		t.Fatalf("every line should survive, got %v", lines)
	}
	if lines[0] != ": keep-alive comment" {
		t.Fatalf("non-data lines pass through, got %q", lines[0])
	}
	if !strings.Contains(lines[1], `"finish_reason":"stop"`) {
		t.Fatalf("the event should have been rewritten, got %q", lines[1])
	}
	if lines[2] != "data: [DONE]" {
		t.Fatalf("the terminator must survive untouched, got %q", lines[2])
	}
}

func TestTransport_RewritesAJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gemini-3.1-pro","usage":null}`))
	}))
	defer srv.Close()

	p := gatewayProfile(srv.URL)
	p.Rewrite = append(p.Rewrite, Rule{Direction: DirectionResponse, Op: OpSetDefault, Path: "usage", Value: map[string]any{"prompt_tokens": 0}})
	client := &http.Client{Transport: NewTransport(p, nil)}
	resp, err := client.Get(srv.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), `"prompt_tokens":0`) {
		t.Fatalf("the response should have been filled in, got %s", raw)
	}
	if resp.ContentLength != int64(len(raw)) {
		t.Fatalf("Content-Length should match the rewritten body: %d vs %d", resp.ContentLength, len(raw))
	}
}

func TestTransport_UnchangedWithoutHeadersOrRules(t *testing.T) {
	p := Profile{Name: "plain", BaseURL: "http://example.test"}
	if got := NewTransport(p, http.DefaultTransport); got != http.RoundTripper(http.DefaultTransport) {
		t.Fatal("a profile with nothing to change should not wrap the transport")
	}
}

// The whole path: a profile-registered provider streaming a completion
// through the rewriting transport to a gateway that would reject the
// un-rewritten body.
func TestNew_StreamsThroughTheRewrites(t *testing.T) {
	var seen map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Errorf("decode: %v", err)
		}
		if id := toolCallID(seen); strings.Contains(id, "__thought__") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Corrupted thought signature"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	p := gatewayProfile(srv.URL)
	prov, err := New(p, provider.ResolveOpts{Model: "gemini-3.1-pro", APIKey: "k"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prov.Name() != "test-gateway" {
		t.Fatalf("the session should show the profile's name, got %q", prov.Name())
	}

	ch, err := prov.StreamCompletion(context.Background(), []provider.Message{
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call_1__thought__YWJj", Name: "ls", Arguments: "{}"}}},
		{Role: provider.RoleTool, ToolCallID: "call_1__thought__YWJj", Content: "ok"},
	}, provider.CompletionOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var text string
	for ev := range ch {
		if ev.Err != nil {
			t.Fatalf("the gateway rejected the request: %v", ev.Err)
		}
		text += ev.Token
	}
	if text != "ok" {
		t.Fatalf("expected the streamed token, got %q", text)
	}
	if id := toolCallID(seen); id != "call_1" {
		t.Fatalf("the gateway should have seen a cut id, got %q", id)
	}
}

// toolCallID digs the first tool-call id out of a decoded request body.
func toolCallID(body map[string]any) string {
	messages, _ := body["messages"].([]any)
	for _, m := range messages {
		msg, _ := m.(map[string]any)
		calls, _ := msg["tool_calls"].([]any)
		for _, c := range calls {
			call, _ := c.(map[string]any)
			if id, ok := call["id"].(string); ok {
				return id
			}
		}
	}
	return ""
}
