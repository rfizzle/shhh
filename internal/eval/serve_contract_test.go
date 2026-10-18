//go:build contract

package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The real binary, served and driven by the persona, against a fake model on
// loopback: the model asks which package, the script answers, and the answer
// reaches the model as the result of its question. It is the one place the
// client is held against the server it was written for rather than a fake of
// it.
func TestAServedAttemptDrivesTheRealServer(t *testing.T) {
	requireGit(t)
	tool, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("no go toolchain on PATH to build the binary with: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "shhh")
	build := exec.Command(tool, "build", "-o", bin, "./cmd/shhh")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/shhh: %v: %s", err, out)
	}

	var mu sync.Mutex
	var answered []string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		offersAsk, hasResult := false, false
		for _, tl := range body.Tools {
			offersAsk = offersAsk || tl.Function.Name == "ask"
		}
		for _, m := range body.Messages {
			if m.Role == "tool" {
				hasResult = true
				mu.Lock()
				answered = append(answered, string(m.Content))
				mu.Unlock()
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(v any) {
			b, _ := json.Marshal(v)
			fmt.Fprintf(w, "data: %s\n\n", b)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		chunk := func(delta any, finish string) map[string]any {
			choice := map[string]any{"index": 0, "delta": delta}
			if finish != "" {
				choice["finish_reason"] = finish
			}
			return map[string]any{"id": "fake", "object": "chat.completion.chunk", "model": "fake-model", "choices": []any{choice}}
		}
		if offersAsk && !hasResult {
			args := `{"question":"Which package holds the Sum to fix?","shape":"choose","options":[{"label":"calc"},{"label":"stats"}]}`
			send(chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call-1", "type": "function",
				"function": map[string]string{"name": "ask", "arguments": args}}}}, ""))
			send(chunk(map[string]any{}, "tool_calls"))
		} else {
			send(chunk(map[string]any{"content": "Fixed."}, ""))
			end := chunk(map[string]any{}, "stop")
			end["usage"] = map[string]int{"prompt_tokens": 10, "completion_tokens": 3, "total_tokens": 13}
			send(end)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("this test needs a loopback listener: %v", err)
	}
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config", "shhh"), 0o755); err != nil {
		t.Fatal(err)
	}
	config := "[summary]\nresume_interval_turns = -1\n\n[behavior]\nprovider_retries = 0\n"
	if err := os.WriteFile(filepath.Join(home, "config", "shhh", "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("SHHH_PROVIDER", "openai-compatible")
	t.Setenv("SHHH_BASE_URL", srv.URL+"/v1")
	t.Setenv("SHHH_API_KEY", "fake-key")
	t.Setenv("SHHH_MODEL", "fake-model")
	t.Setenv("SHHH_REASONING", "medium")

	cases := servedSuite(t, 1, "assert = [\"asks\"]\n", `question (?i)which package => calc`, `close .* => stop`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sum, err := Run(ctx, cases, Options{Binary: bin, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	a := sum.Results[0].Attempts[0]
	if a.Err != nil {
		t.Fatalf("the attempt broke: %v", a.Err)
	}
	if a.Turns != 1 || a.Unscripted != 0 || len(a.Findings) != 1 || !a.Findings[0].Held || !a.Passed {
		t.Errorf("turns %d, unscripted %d, findings %+v, passed %v", a.Turns, a.Unscripted, a.Findings, a.Passed)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(answered) == 0 || !strings.Contains(strings.Join(answered, "\n"), "calc") {
		t.Errorf("the model was never handed the answer: %q", answered)
	}
}
