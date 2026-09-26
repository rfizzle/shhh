//go:build contract

package cli

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/storage"
)

// modelEndpoint is the fake provider with the one thing it does not keep: the
// model each request named. It answers every request the same way, through
// the fake provider's own writer, and is handed to newPrintSession inside a
// fakeProvider because the session reads nothing of that but the address.
type modelEndpoint struct {
	srv *httptest.Server

	mu     sync.Mutex
	models []string
}

func startModelEndpoint(t *testing.T) *modelEndpoint {
	t.Helper()
	e := &modelEndpoint{}
	e.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		e.mu.Lock()
		e.models = append(e.models, body.Model)
		e.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		writeReply(w, reply{text: "the answer"})
	}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("CLI provider contract tests require a loopback listener: %v", err)
	}
	e.srv.Listener = ln
	e.srv.Start()
	t.Cleanup(e.srv.Close)
	return e
}

// asked is every model the run's requests named, in order.
func (e *modelEndpoint) asked() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.models...)
}

// A `shhh code -p` run under a settings file that names provider.code_model
// and provider.model differently runs on the surface key's model, and the
// record says so: the row the run's --output json names carries the model
// the request went to. The variable outranks the key the way it outranks
// provider.model. The resolution itself is held hermetically beside
// fillConfigHalf; this is the built binary's whole start, which surveys a
// real checkout and so is not a hermetic test's to make.
// See docs/capabilities/configuration.md#each-surface-can-have-a-model-of-its-own.
func TestPrintRun_ACodeRunRecordsTheModelItsSurfaceKeyChose(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
		want string
	}{
		// SHHH_MODEL is set empty rather than dropped: the session's
		// environment pins it, the last pair of a name is the one a process
		// is given, and an empty variable names no model.
		{"the surface key", "SHHH_MODEL=", "code-model"},
		{"the variable outranks it", "SHHH_MODEL=env-model", "env-model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := startModelEndpoint(t)
			s := newPrintSession(t, &fakeProvider{srv: e.srv})
			body := "[behavior]\nprovider_retries = 0\n\n" +
				"[provider]\nmodel = \"shared-model\"\ncode_model = \"code-model\"\n"
			write(t, filepath.Join(s.home, "config", "shhh", "config.toml"), body)

			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			cmd, out, errs := s.command(ctx, "", "code", "-p", "--output", "json", "say hi")
			cmd.Env = append(cmd.Env, tc.env)
			stdout, stderr, code := finished(t, cmd, cmd.Run(), out, errs)
			if code != 0 {
				t.Fatalf("the run exited %d\nstderr: %s", code, stderr)
			}

			asked := e.asked()
			if len(asked) == 0 {
				t.Fatal("the run never reached the provider")
			}
			for _, m := range asked {
				if m != tc.want {
					t.Fatalf("the run asked the provider for %q, want %q (every request: %q)", m, tc.want, asked)
				}
			}

			// The transcript carries no model field; what it names is the
			// record row, and the row is where the model is stated.
			var transcript struct {
				Session string `json:"session"`
			}
			if err := json.Unmarshal([]byte(stdout), &transcript); err != nil {
				t.Fatalf("the transcript did not parse: %v\n%s", err, stdout)
			}
			id, err := strconv.ParseInt(transcript.Session, 10, 64)
			if err != nil {
				t.Fatalf("the transcript named its record row as %q: %v", transcript.Session, err)
			}

			db, err := storage.OpenPath(filepath.Join(s.home, "data", "shhh", "shhh.db"))
			if err != nil {
				t.Fatalf("opening the store the run wrote to: %v", err)
			}
			defer db.Close()
			rows, err := db.AgentSessions(time.Now().Add(-time.Hour), 10)
			if err != nil {
				t.Fatal(err)
			}
			var got *storage.AgentSessionSummary
			for i := range rows {
				if rows[i].ID == id {
					got = &rows[i]
				}
			}
			if got == nil {
				t.Fatalf("no agent_sessions row has the id %d the transcript named", id)
			}
			if got.Model != tc.want {
				t.Fatalf("the run's row records %q, want %q", got.Model, tc.want)
			}
		})
	}
}
