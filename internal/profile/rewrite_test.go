package profile

import (
	"encoding/json"
	"testing"
)

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(s), &body); err != nil {
		t.Fatalf("bad fixture: %v", err)
	}
	return body
}

func encode(t *testing.T, body map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

// The motivating quirk: LiteLLM appends "__thought__<base64>" to Gemini
// tool-call ids, and Vertex rejects the fabricated ones when they come back.
func TestApply_CutAtStripsToolCallIDs(t *testing.T) {
	body := decode(t, `{
		"model": "gemini-3.1-pro",
		"messages": [
			{"role": "user", "content": "hi"},
			{"role": "assistant", "tool_calls": [
				{"id": "call_1__thought__YWJj", "function": {"name": "ls"}},
				{"id": "call_2", "function": {"name": "cat"}}
			]},
			{"role": "tool", "tool_call_id": "call_1__thought__YWJj", "content": "ok"}
		]
	}`)
	rules := []Rule{
		{When: Match{Model: "gemini-*"}, Op: OpCutAt, Path: "messages[].tool_calls[].id", Value: "__thought__", Direction: DirectionRequest},
		{When: Match{Model: "gemini-*"}, Op: OpCutAt, Path: "messages[].tool_call_id", Value: "__thought__", Direction: DirectionRequest},
	}

	if edits := Apply(rules, DirectionRequest, "gemini-3.1-pro", body); edits != 2 {
		t.Fatalf("expected 2 edits, got %d", edits)
	}
	got := encode(t, body)
	want := `{"messages":[{"content":"hi","role":"user"},{"role":"assistant","tool_calls":[{"function":{"name":"ls"},"id":"call_1"},{"function":{"name":"cat"},"id":"call_2"}]},{"content":"ok","role":"tool","tool_call_id":"call_1"}],"model":"gemini-3.1-pro"}`
	if got != want {
		t.Fatalf("unexpected body:\n got %s\nwant %s", got, want)
	}
}

func TestApply_ModelGlobNarrowsTheRule(t *testing.T) {
	rules := []Rule{{When: Match{Model: "claude-*"}, Op: OpDelete, Path: "top_p", Direction: DirectionRequest}}

	claude := decode(t, `{"model":"claude-opus-5","top_p":0.9}`)
	if edits := Apply(rules, DirectionRequest, "claude-opus-5", claude); edits != 1 {
		t.Fatalf("the rule should match claude, got %d edits", edits)
	}
	if _, present := claude["top_p"]; present {
		t.Fatal("top_p should be gone")
	}

	gpt := decode(t, `{"model":"gpt-4o","top_p":0.9}`)
	if edits := Apply(rules, DirectionRequest, "gpt-4o", gpt); edits != 0 {
		t.Fatalf("the rule should not match gpt-4o, got %d edits", edits)
	}
	if _, present := gpt["top_p"]; !present {
		t.Fatal("top_p should survive on an unmatched model")
	}
}

func TestApply_DirectionSeparatesRules(t *testing.T) {
	rules := []Rule{{Op: OpSet, Path: "stream", Value: false, Direction: DirectionResponse}}
	body := decode(t, `{"stream":true}`)
	if edits := Apply(rules, DirectionRequest, "any", body); edits != 0 {
		t.Fatalf("a response rule must not run on a request, got %d edits", edits)
	}
	if edits := Apply(rules, DirectionResponse, "any", body); edits != 1 {
		t.Fatalf("expected the response rule to run, got %d edits", edits)
	}
}

func TestApply_Operations(t *testing.T) {
	tests := []struct {
		name string
		rule Rule
		body string
		want string
	}{
		{
			name: "set replaces an existing value",
			rule: Rule{Op: OpSet, Path: "max_tokens", Value: int64(4096)},
			body: `{"max_tokens":100}`,
			want: `{"max_tokens":4096}`,
		},
		{
			name: "set creates a missing field",
			rule: Rule{Op: OpSet, Path: "max_tokens", Value: int64(4096)},
			body: `{}`,
			want: `{"max_tokens":4096}`,
		},
		{
			name: "set-default leaves an existing value",
			rule: Rule{Op: OpSetDefault, Path: "max_tokens", Value: int64(4096)},
			body: `{"max_tokens":100}`,
			want: `{"max_tokens":100}`,
		},
		{
			name: "set-default fills a null",
			rule: Rule{Op: OpSetDefault, Path: "max_tokens", Value: int64(4096)},
			body: `{"max_tokens":null}`,
			want: `{"max_tokens":4096}`,
		},
		{
			name: "nested set reaches into an object",
			rule: Rule{Op: OpSet, Path: "chat_template_kwargs.enable_thinking", Value: false},
			body: `{"chat_template_kwargs":{"enable_thinking":true}}`,
			want: `{"chat_template_kwargs":{"enable_thinking":false}}`,
		},
		{
			name: "rename moves a field within its object",
			rule: Rule{Op: OpRename, Path: "max_tokens", To: "max_completion_tokens"},
			body: `{"max_tokens":100}`,
			want: `{"max_completion_tokens":100}`,
		},
		{
			name: "trim-prefix strips a routing prefix",
			rule: Rule{Op: OpTrimPrefix, Path: "model", Value: "vertex_ai/"},
			body: `{"model":"vertex_ai/gemini-3.1-pro"}`,
			want: `{"model":"gemini-3.1-pro"}`,
		},
		{
			name: "trim-suffix strips an exact suffix",
			rule: Rule{Op: OpTrimSuffix, Path: "model", Value: "-maas"},
			body: `{"model":"deepseek-r1-0528-maas"}`,
			want: `{"model":"deepseek-r1-0528"}`,
		},
		{
			name: "replace rewrites every occurrence",
			rule: Rule{Op: OpReplace, Path: "model", Value: ".", To: "-"},
			body: `{"model":"gpt-5.6.sol"}`,
			want: `{"model":"gpt-5-6-sol"}`,
		},
		{
			name: "delete on a missing field is a no-op",
			rule: Rule{Op: OpDelete, Path: "top_p"},
			body: `{"model":"m"}`,
			want: `{"model":"m"}`,
		},
		{
			name: "a path through a missing branch is a no-op",
			rule: Rule{Op: OpDelete, Path: "messages[].tool_calls[].id"},
			body: `{"messages":[{"role":"user"}]}`,
			want: `{"messages":[{"role":"user"}]}`,
		},
		{
			name: "string surgery ignores a non-string value",
			rule: Rule{Op: OpCutAt, Path: "max_tokens", Value: "x"},
			body: `{"max_tokens":100}`,
			want: `{"max_tokens":100}`,
		},
		{
			name: "cut-at without the marker leaves the value",
			rule: Rule{Op: OpCutAt, Path: "model", Value: "__thought__"},
			body: `{"model":"gemini-3.1-pro"}`,
			want: `{"model":"gemini-3.1-pro"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule := tc.rule
			if err := rule.validate(); err != nil {
				t.Fatalf("rule should be valid: %v", err)
			}
			body := decode(t, tc.body)
			Apply([]Rule{rule}, DirectionRequest, "any-model", body)
			if got := encode(t, body); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestApply_RulesRunInOrder(t *testing.T) {
	rules := []Rule{
		{Op: OpRename, Path: "max_tokens", To: "max_completion_tokens"},
		{Op: OpSet, Path: "max_completion_tokens", Value: int64(1)},
	}
	for i := range rules {
		if err := rules[i].validate(); err != nil {
			t.Fatalf("rule %d: %v", i, err)
		}
	}
	body := decode(t, `{"max_tokens":100}`)
	Apply(rules, DirectionRequest, "m", body)
	if got := encode(t, body); got != `{"max_completion_tokens":1}` {
		t.Fatalf("the second rule should see the first rule's edit, got %s", got)
	}
}

func TestRuleValidate_RejectsUnusableRules(t *testing.T) {
	tests := map[string]Rule{
		"unknown op":        {Op: "explode", Path: "x"},
		"missing op":        {Path: "x"},
		"missing path":      {Op: OpDelete},
		"set without value": {Op: OpSet, Path: "x"},
		"rename without to": {Op: OpRename, Path: "x"},
		"cut-at non-string": {Op: OpCutAt, Path: "x", Value: 3},
		"bad direction":     {Op: OpDelete, Path: "x", Direction: "sideways"},
		"bad model glob":    {Op: OpDelete, Path: "x", When: Match{Model: "[bad"}},
	}
	for name, rule := range tests {
		t.Run(name, func(t *testing.T) {
			r := rule
			if err := r.validate(); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
}

func TestRuleValidate_DefaultsToRequestDirection(t *testing.T) {
	r := Rule{Op: OpDelete, Path: "top_p"}
	if err := r.validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Direction != DirectionRequest {
		t.Fatalf("expected the request direction by default, got %q", r.Direction)
	}
}

func TestParsePath(t *testing.T) {
	got := parsePath("messages[].tool_calls[].id")
	want := []segment{{key: "messages", array: true}, {key: "tool_calls", array: true}, {key: "id"}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("segment %d: got %v, want %v", i, got[i], want[i])
		}
	}
}
