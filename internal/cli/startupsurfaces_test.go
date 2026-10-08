package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/web"
)

// runSurface runs one command through the real root, the way the binary
// does, with stdin holding input and stdout set aside, against a home of its
// own whose config names one server that cannot start. It hands back the
// timing rows written to the session rows of kind the run left in the store.
func runSurface(t *testing.T, kind, input string, args ...string) []storage.AgentTiming {
	t.Helper()
	home := t.TempDir()
	// As buildSession does: a session installs its host reading for the
	// whole process, and left standing it outlives the home it reads from.
	t.Cleanup(func() { web.UseReputation(nil) })
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	seedModelData(t, filepath.Join(home, "cache"))
	t.Setenv("SHHH_PROVIDER", "")
	t.Setenv("SHHH_MODEL", "")
	t.Setenv("SHHH_REASONING", "")
	cfgDir := filepath.Join(home, "config", "shhh")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"),
		[]byte("[mcp.servers.broken]\ncommand = \"/nonexistent/mcp-server\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	provider.Register(oneShotProviderName, func(provider.ResolveOpts) (provider.Provider, error) {
		return oneShotProvider{answer: "ls -la", asked: &[]time.Time{}}, nil
	})

	in := filepath.Join(home, "stdin")
	if err := os.WriteFile(in, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	stdinFile, err := os.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	stdoutFile, err := os.Create(filepath.Join(home, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	// The session's own notes go to stderr; set aside with stdout.
	stdin, stdout, stderr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = stdinFile, stdoutFile, stdoutFile
	t.Cleanup(func() {
		os.Stdin, os.Stdout, os.Stderr = stdin, stdout, stderr
		stdinFile.Close()
		stdoutFile.Close()
	})

	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append(args, "--provider", oneShotProviderName, "--model", "test-model"))
	if err := execute(context.Background(), cmd); err != nil {
		t.Fatalf("shhh %v: %v\n%s", args, err, out.String())
	}
	os.Stdin, os.Stdout, os.Stderr = stdin, stdout, stderr

	db, err := storage.OpenPath(filepath.Join(home, "data", "shhh", "shhh.db"))
	if err != nil {
		t.Fatalf("open the store the run wrote to: %v", err)
	}
	defer db.Close()
	sessions, err := db.AgentSessions(time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	var rows []storage.AgentTiming
	found := false
	for _, s := range sessions {
		if s.Kind != kind {
			continue
		}
		found = true
		got, err := db.AgentTimings(s.ID)
		if err != nil {
			t.Fatalf("timings: %v", err)
		}
		rows = append(rows, got...)
	}
	if !found {
		t.Fatalf("no %s session row among %+v", kind, sessions)
	}
	return rows
}

// startupSurface is runSurface's count of each startup phase.
func startupSurface(t *testing.T, kind, input string, args ...string) map[string]int {
	t.Helper()
	phases := map[string]int{}
	for _, r := range runSurface(t, kind, input, args...) {
		if r.Kind == storage.AgentEventStartup {
			phases[r.Reason]++
		}
	}
	return phases
}

// A served session and a one-shot pay for their start the way a chat does,
// and the record says what each paid for: the configuration and the store
// for both, and for the served session its server's connect and the
// language-server lookup as well. A one-shot connects no servers and looks
// for no language servers, so it has no row for either.
func TestStartup_ServeAndCmdWriteTheirPhases(t *testing.T) {
	t.Run("serve", func(t *testing.T) {
		got := startupSurface(t, "serve", `{"jsonrpc":"2.0","id":1,"method":"session/start","params":{}}`+"\n", "serve", "--stdio")
		for _, phase := range []string{observe.PhaseConfig, observe.PhaseStore, observe.PhaseMCP, observe.PhaseLSP} {
			if got[phase] != 1 {
				t.Errorf("%s: %d rows, want 1 (all: %v)", phase, got[phase], got)
			}
		}
	})
	t.Run("cmd", func(t *testing.T) {
		got := startupSurface(t, "cmd", "", "cmd", "--raw", "list files")
		want := map[string]int{observe.PhaseConfig: 1, observe.PhaseStore: 1}
		if len(got) != len(want) || got[observe.PhaseConfig] != 1 || got[observe.PhaseStore] != 1 {
			t.Errorf("phases %v, want %v", got, want)
		}
	})
}
