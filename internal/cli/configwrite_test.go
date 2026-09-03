package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// A hand-written file, with a comment saying why, is what every writer in
// the CLI has to hand back unchanged but for the key it was asked to change.
const handWrittenConfig = `# pinned: the default moved under me
[provider]
default = "anthropic"
model = "claude-sonnet-5"

[appearance]
mouse = false   # I select with the terminal
`

// pointConfigAt makes path the config file every command reads and writes,
// and returns it.
func pointConfigAt(t *testing.T, text string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	must(t, os.MkdirAll(filepath.Join(dir, "shhh"), 0o755))
	path := filepath.Join(dir, "shhh", "config.toml")
	if text != "" {
		must(t, os.WriteFile(path, []byte(text), 0o644))
	}
	return path
}

func runRoot(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("shhh %s: %v\n%s", strings.Join(args, " "), err, out.String())
	}
	return out.String()
}

func TestConfigSet_ChangesOneLine(t *testing.T) {
	path := pointConfigAt(t, handWrittenConfig)
	runRoot(t, "config", "set", "provider.model", "claude-opus-5")
	got, err := os.ReadFile(path)
	must(t, err)
	want := strings.Replace(handWrittenConfig, `model = "claude-sonnet-5"`, `model = "claude-opus-5"`, 1)
	if string(got) != want {
		t.Fatalf("--- want\n%s--- got\n%s", want, got)
	}
}

func TestConfigSet_CreatesTheFileWithOnlyTheKey(t *testing.T) {
	path := pointConfigAt(t, "")
	runRoot(t, "config", "set", "appearance.notify", "false")
	got, err := os.ReadFile(path)
	must(t, err)
	if want := "[appearance]\nnotify = false\n"; string(got) != want {
		t.Fatalf("a new file holds the key alone:\n%s", got)
	}
}

// [w] writes the keys the screen staged and nothing else: a key edited and
// put back is not rewritten, and a reset takes its line out.
func TestConfigModel_WritesOnlyTheStagedKeys(t *testing.T) {
	cfg, err := config.LoadFrom(pointConfigAt(t, handWrittenConfig))
	must(t, err)
	m := newConfigModel(cfg)
	m.apply(components.ConfigChange{Key: "behavior.default_mode", Value: "auto"})
	m.apply(components.ConfigChange{Key: "appearance.mouse", Reset: true})
	m.apply(components.ConfigChange{Key: "provider.model", Value: "claude-opus-5"})
	m.apply(components.ConfigChange{Key: "provider.model", Value: "claude-sonnet-5"})
	edits := m.edits()
	if len(edits) != 2 {
		t.Fatalf("two keys stand against the file, got %v", edits)
	}
	if edits[0] != (config.Edit{Key: "behavior.default_mode", Value: "auto"}) || edits[1] != (config.Edit{Key: "appearance.mouse"}) {
		t.Fatalf("edits = %v", edits)
	}
	if m.screen.Changed != 2 {
		t.Fatalf("the header counts what the write will change, got %d", m.screen.Changed)
	}
	must(t, config.Write(config.WritePath(), edits...))
	got, err := os.ReadFile(config.WritePath())
	must(t, err)
	want := "# pinned: the default moved under me\n[provider]\ndefault = \"anthropic\"\nmodel = \"claude-sonnet-5\"\n\n[appearance]\n\n[behavior]\ndefault_mode = \"auto\"\n"
	if string(got) != want {
		t.Fatalf("--- want\n%s--- got\n%s", want, got)
	}
}

// The first-run card writes the provider it was answered with and leaves
// the rest of the file alone.
func TestSaveProviderChoice_WritesTheChoiceAlone(t *testing.T) {
	path := pointConfigAt(t, handWrittenConfig)
	done := captureStderr(t)
	saveProviderChoice(providerRequest{Provider: "openai", Model: "gpt-5"})
	if msg := done(); !strings.Contains(msg, "saved the provider") {
		t.Fatalf("stderr = %q", msg)
	}
	got, err := os.ReadFile(path)
	must(t, err)
	want := strings.NewReplacer(`default = "anthropic"`, `default = "openai"`, `model = "claude-sonnet-5"`, `model = "gpt-5"`).Replace(handWrittenConfig)
	if string(got) != want {
		t.Fatalf("--- want\n%s--- got\n%s", want, got)
	}
}

// The writer a session's slash commands share edits one key of the file.
func TestConfigWriter_EditsOneKey(t *testing.T) {
	path := pointConfigAt(t, handWrittenConfig)
	must(t, configWriter()("appearance.mouse", "true"))
	got, err := os.ReadFile(path)
	must(t, err)
	want := strings.Replace(handWrittenConfig, "mouse = false", "mouse = true", 1)
	if string(got) != want {
		t.Fatalf("--- want\n%s--- got\n%s", want, got)
	}
}

func TestMCPAddThenRemove_LeavesTheFileAsItWas(t *testing.T) {
	path := pointConfigAt(t, handWrittenConfig)
	runRoot(t, "mcp", "add", "docs", "--env", "TOKEN=${DOCS_TOKEN}", "--read-only", "--", "npx", "-y", "@x/docs")
	added, err := os.ReadFile(path)
	must(t, err)
	if !strings.HasPrefix(string(added), handWrittenConfig) {
		t.Fatalf("the add rewrote what was there:\n%s", added)
	}
	cfg, err := config.LoadFrom(path)
	must(t, err)
	s := cfg.MCP.Servers["docs"]
	if s.Command != "npx" || strings.Join(s.Args, " ") != "-y @x/docs" || s.Env["TOKEN"] != "${DOCS_TOKEN}" || !s.ReadOnly {
		t.Fatalf("read back %+v\n%s", s, added)
	}
	runRoot(t, "mcp", "remove", "docs")
	got, err := os.ReadFile(path)
	must(t, err)
	if string(got) != handWrittenConfig {
		t.Fatalf("--- want\n%s--- got\n%s", handWrittenConfig, got)
	}
}

// runRootErr runs a command that is expected to fail and returns the error.
func runRootErr(t *testing.T, args ...string) error {
	t.Helper()
	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("shhh %s should have failed\n%s", strings.Join(args, " "), out.String())
	}
	return err
}

// `config set` refuses a value the key cannot hold and leaves the file as it
// was. A retention key given a word wrote zero once, which at the next
// startup is "prune everything".
func TestConfigSet_RefusesAValueTheKeyCannotHold(t *testing.T) {
	for _, tc := range []struct{ key, value, want string }{
		{"history.retention_days", "abc", "a whole number"},
		{"appearance.mouse", "yes", "true or false"},
		{"behavior.default_mode", "yolo", "unknown mode"},
		{"provider.reasoning", "sometimes", "unknown reasoning level"},
		{"sandbox.profile", "wide-open", "unknown sandbox profile"},
		{"sandbox.container_engine", "containerd", "unknown container engine"},
		{"sandbox.require_isolation", "some", "unknown isolation level"},
	} {
		path := pointConfigAt(t, handWrittenConfig)
		err := runRootErr(t, "config", "set", tc.key, tc.value)
		if msg := err.Error(); !strings.Contains(msg, tc.key) || !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q: %q, want the key and %q", tc.key, tc.value, msg, tc.want)
		}
		got, readErr := os.ReadFile(path)
		must(t, readErr)
		if string(got) != handWrittenConfig {
			t.Errorf("%s = %q touched the file:\n%s", tc.key, tc.value, got)
		}
	}
}

// The writer a session's slash commands share refuses the same words, so a
// mode saved from inside a session is one the next session can read.
func TestConfigWriter_RefusesAModeOutsideTheFour(t *testing.T) {
	path := pointConfigAt(t, handWrittenConfig)
	if err := configWriter()("behavior.default_mode", "yolo"); err == nil {
		t.Fatal("the writer refuses a mode that is not one")
	}
	got, err := os.ReadFile(path)
	must(t, err)
	if string(got) != handWrittenConfig {
		t.Fatalf("touched:\n%s", got)
	}
}

// The reviewer's model is settable from the command line and reads back as
// the model that role runs.
func TestConfigSet_ReviewerModelRoundTrips(t *testing.T) {
	path := pointConfigAt(t, handWrittenConfig)
	runRoot(t, "config", "set", "agents.reviewer_model", "claude-haiku-4-5")
	cfg, err := config.LoadFrom(path)
	must(t, err)
	if got := cfg.AgentModel("reviewer", "session-model"); got != "claude-haiku-4-5" {
		t.Fatalf("reviewer model = %q", got)
	}
}
