package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"net/http"
	"net/http/httptest"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/web"
)

func execCall(command string) provider.ToolCall {
	args, _ := json.Marshal(map[string]string{"command": command})
	return provider.ToolCall{ID: "c1", Name: "execute_command", Arguments: string(args)}
}

// fakeRun records executed commands and returns a fixed result.
func fakeRun(ran *[]string) func(context.Context, string) (string, int) {
	return func(_ context.Context, command string) (string, int) {
		*ran = append(*ran, command)
		return "ok", 0
	}
}

func TestHeadlessApprover_DeniesCommandByDefault(t *testing.T) {
	var ran []string
	resolve := headlessApprover(context.Background(), printOpts{}, nil, fakeRun(&ran), nil, nil, nil)

	result := resolve(execCall("echo hi"))
	if !strings.HasPrefix(result, "error:") || !strings.Contains(result, "--yes") {
		t.Fatalf("default must deny commands with guidance, got %q", result)
	}
	if len(ran) != 0 {
		t.Fatalf("command must not run, ran %v", ran)
	}
}

func TestHeadlessApprover_YesRunsCommand(t *testing.T) {
	var ran []string
	resolve := headlessApprover(context.Background(), printOpts{yes: true}, nil, fakeRun(&ran), nil, nil, nil)

	result := resolve(execCall("echo hi"))
	if len(ran) != 1 || ran[0] != "echo hi" {
		t.Fatalf("--yes must run the command, ran %v", ran)
	}
	if !strings.Contains(result, "exit code: 0") || !strings.Contains(result, "ok") {
		t.Fatalf("unexpected exec result %q", result)
	}
}

func TestHeadlessApprover_AllowlistRunsMatchingCommand(t *testing.T) {
	var ran []string
	resolve := headlessApprover(context.Background(), printOpts{}, []string{"go test"}, fakeRun(&ran), nil, nil, nil)

	if result := resolve(execCall("go test ./...")); strings.HasPrefix(result, "error:") {
		t.Fatalf("allowlisted command must run, got %q", result)
	}
	if result := resolve(execCall("go build")); !strings.HasPrefix(result, "error:") {
		t.Fatalf("non-matching command must be denied, got %q", result)
	}
	if len(ran) != 1 || ran[0] != "go test ./..." {
		t.Fatalf("expected only the allowlisted command to run, ran %v", ran)
	}
}

func TestHeadlessApprover_SafetyFlaggedDeniedEvenWithYes(t *testing.T) {
	var ran []string
	resolve := headlessApprover(context.Background(), printOpts{yes: true}, nil, fakeRun(&ran), nil, nil, nil)

	result := resolve(execCall("git reset --hard"))
	if !strings.HasPrefix(result, "error:") || !strings.Contains(result, "safety-flagged") {
		t.Fatalf("safety-flagged command must be denied under --yes, got %q", result)
	}
	if len(ran) != 0 {
		t.Fatalf("safety-flagged command must not run, ran %v", ran)
	}
}

func TestHeadlessApprover_InvalidCommandArguments(t *testing.T) {
	var ran []string
	resolve := headlessApprover(context.Background(), printOpts{yes: true}, nil, fakeRun(&ran), nil, nil, nil)

	tc := provider.ToolCall{ID: "c1", Name: "execute_command", Arguments: `{"command":""}`}
	if result := resolve(tc); !strings.HasPrefix(result, "error:") {
		t.Fatalf("empty command must be an error result, got %q", result)
	}
}

func TestHeadlessApprover_DeniesEditsByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	args, _ := json.Marshal(map[string]string{"path": path, "content": "hi"})
	tc := provider.ToolCall{ID: "c1", Name: "write_file", Arguments: string(args)}

	resolve := headlessApprover(context.Background(), printOpts{}, nil, fakeRun(&[]string{}), nil, nil, nil)
	if result := resolve(tc); !strings.HasPrefix(result, "error:") || !strings.Contains(result, "--yes") {
		t.Fatalf("default must deny edits with guidance, got %q", result)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file must not be written on deny")
	}
}

func TestHeadlessApprover_YesAppliesEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	args, _ := json.Marshal(map[string]string{"path": path, "content": "hi"})
	tc := provider.ToolCall{ID: "c1", Name: "write_file", Arguments: string(args)}

	resolve := headlessApprover(context.Background(), printOpts{yes: true}, nil, fakeRun(&[]string{}), nil, nil, nil)
	if result := resolve(tc); strings.HasPrefix(result, "error:") {
		t.Fatalf("--yes must apply the edit, got %q", result)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "hi" {
		t.Fatalf("file not written: %v %q", err, data)
	}
}

func TestHeadlessApprover_UnknownGatedToolDenied(t *testing.T) {
	resolve := headlessApprover(context.Background(), printOpts{yes: true}, nil, fakeRun(&[]string{}), nil, nil, nil)
	tc := provider.ToolCall{ID: "c1", Name: "mystery_tool", Arguments: `{}`}
	if result := resolve(tc); !strings.HasPrefix(result, "error:") {
		t.Fatalf("unknown gated tool must be denied, got %q", result)
	}
}

func TestHeadlessGate(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"execute_command", true},
		{"write_file", true},
		{"edit_file", true},
		{"read_file", false},
		{"search", false},
		{"glob", false},
		{"list_directory", false},
	}
	for _, c := range cases {
		if got := headlessGate(c.name); got != c.want {
			t.Errorf("headlessGate(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestWriteJSONTranscript(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hi"},
		{Role: provider.RoleAssistant, Content: "checking", ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}}},
		{Role: provider.RoleTool, Content: "contents", ToolCallID: "c1"},
		{Role: provider.RoleAssistant, Content: "done"},
	}
	var sb strings.Builder
	if err := writeJSONTranscript(&sb, msgs, "done", provider.Usage{PromptTokens: 10, CompletionTokens: 5}, nil); err != nil {
		t.Fatalf("writeJSONTranscript: %v", err)
	}

	var got jsonTranscript
	if err := json.Unmarshal([]byte(sb.String()), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if !got.Success || got.Final != "done" || got.Error != "" {
		t.Fatalf("unexpected outcome fields: %+v", got)
	}
	if got.Usage.PromptTokens != 10 || got.Usage.CompletionTokens != 5 {
		t.Fatalf("unexpected usage: %+v", got.Usage)
	}
	if len(got.Messages) != len(msgs) {
		t.Fatalf("expected %d messages, got %d", len(msgs), len(got.Messages))
	}
	if got.Messages[2].ToolCalls[0].Name != "read_file" || got.Messages[3].ToolCallID != "c1" {
		t.Fatalf("tool call plumbing lost: %+v", got.Messages)
	}

	sb.Reset()
	if err := writeJSONTranscript(&sb, nil, "", provider.Usage{}, fmt.Errorf("boom")); err != nil {
		t.Fatalf("writeJSONTranscript: %v", err)
	}
	if err := json.Unmarshal([]byte(sb.String()), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if got.Success || got.Error != "boom" {
		t.Fatalf("failure transcript wrong: %+v", got)
	}
}

func TestHeadlessApprover_WebFetchDeniedByDefault(t *testing.T) {
	webTools := web.NewToolset(web.NewFetcher(web.Policy{AllowPrivate: true}), nil)
	resolve := headlessApprover(context.Background(), printOpts{}, nil, fakeRun(&[]string{}), nil, nil, webTools)
	tc := provider.ToolCall{ID: "c1", Name: web.FetchToolName, Arguments: `{"url":"https://example.com/"}`}
	if result := resolve(tc); !strings.HasPrefix(result, "error:") || !strings.Contains(result, "--yes") {
		t.Fatalf("default must deny web fetch with guidance, got %q", result)
	}
}

func TestHeadlessApprover_WebFetchRunsWithYes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "fetched body")
	}))
	defer srv.Close()

	webTools := web.NewToolset(web.NewFetcher(web.Policy{AllowPrivate: true}), nil)
	resolve := headlessApprover(context.Background(), printOpts{yes: true}, nil, fakeRun(&[]string{}), nil, nil, webTools)
	tc := provider.ToolCall{ID: "c1", Name: web.FetchToolName, Arguments: `{"url":"` + srv.URL + `"}`}
	result := resolve(tc)
	if strings.HasPrefix(result, "error:") || !strings.Contains(result, "fetched body") {
		t.Fatalf("--yes must fetch, got %q", result)
	}
}

func TestHeadlessApprover_WebFetchUnregisteredWithoutToolset(t *testing.T) {
	resolve := headlessApprover(context.Background(), printOpts{yes: true}, nil, fakeRun(&[]string{}), nil, nil, nil)
	tc := provider.ToolCall{ID: "c1", Name: web.FetchToolName, Arguments: `{"url":"https://example.com/"}`}
	if result := resolve(tc); !strings.HasPrefix(result, "error:") {
		t.Fatalf("web fetch without a toolset must be denied, got %q", result)
	}
}
