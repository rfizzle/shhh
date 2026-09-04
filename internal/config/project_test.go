package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeProject puts a checkout's settings file at the root of a temporary
// repository and answers with its path.
func writeProject(t *testing.T, text string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".shhh")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The whole shape in one reading: a scalar the checkout sets replaces the
// person's, a key it says nothing about is left exactly as they wrote it,
// and the file says which keys it decided.
func TestLayerProject_ScalarOverridesAndAnUnsetKeyIsLeftAlone(t *testing.T) {
	path := writeProject(t, `
[provider]
model = "claude-sonnet-5"

[behavior]
default_mode = "auto"
`)
	user := Config{}
	user.Provider.Model = "gpt-4o"
	user.Provider.Default = "openai"
	user.Behavior.Shell = "/bin/zsh"

	cfg, proj, err := LayerProject(user, path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Model != "claude-sonnet-5" {
		t.Errorf("the checkout's model did not override: %q", cfg.Provider.Model)
	}
	if cfg.Provider.Default != "openai" || cfg.Behavior.Shell != "/bin/zsh" {
		t.Errorf("a key the checkout says nothing about was changed: %+v", cfg.Provider)
	}
	if !proj.Sets("provider.model") || !proj.Sets("behavior.default_mode") {
		t.Errorf("the keys the checkout set are not reported: %v", proj.Keys)
	}
	if proj.Sets("provider.default") {
		t.Errorf("a key the checkout never wrote is reported as its own: %v", proj.Keys)
	}
	if proj.Display != ".shhh/config.toml" {
		t.Errorf("the file is not stated from the root: %q", proj.Display)
	}
}

// The three allowlist-shaped keys extend rather than replace: a checkout
// cannot know what is on the person's list, and a replacement would quietly
// take away commands that have nothing to do with this repository.
func TestLayerProject_TheThreeAllowlistsUnionAndScopeDirsAreRooted(t *testing.T) {
	path := writeProject(t, `
[behavior]
command_allowlist = ["go test", "ls"]
read_only_commands = ["rg"]
scope_dirs = ["../shared", "/opt/fixed"]
mode_cycle = ["manual", "auto"]
`)
	user := Config{}
	user.Behavior.CommandAllowlist = []string{"ls", "git status"}
	user.Behavior.ModeCycle = []string{"manual", "accept-edits", "auto", "plan"}

	cfg, _, err := LayerProject(user, path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.Behavior.CommandAllowlist, ","); got != "ls,git status,go test" {
		t.Errorf("the allowlist is not the person's with the checkout's added: %q", got)
	}
	if got := strings.Join(cfg.Behavior.ReadOnlyCommands, ","); got != "rg" {
		t.Errorf("an empty list on the person's side did not take the checkout's: %q", got)
	}
	// Every other list is a complete answer and overrides like a scalar.
	if got := strings.Join(cfg.Behavior.ModeCycle, ","); got != "manual,auto" {
		t.Errorf("an ordinary list unioned instead of overriding: %q", got)
	}
	root := filepath.Dir(filepath.Dir(path))
	want := filepath.Clean(filepath.Join(root, "..", "shared"))
	if len(cfg.Behavior.ScopeDirs) != 2 || cfg.Behavior.ScopeDirs[0] != want {
		t.Errorf("a relative scope directory is not resolved against the checkout: %v", cfg.Behavior.ScopeDirs)
	}
	if cfg.Behavior.ScopeDirs[1] != "/opt/fixed" {
		t.Errorf("an absolute scope directory was rewritten: %v", cfg.Behavior.ScopeDirs)
	}
}

// A key the checkout writes at its zero value is still the checkout's
// answer. Reading zero as unset would make "off" the one thing a repository
// could not say.
func TestLayerProject_AZeroTheCheckoutWroteIsAnAnswer(t *testing.T) {
	path := writeProject(t, "[behavior]\nsilent_mode = false\n")
	user := Config{}
	user.Behavior.SilentMode = true

	cfg, proj, err := LayerProject(user, path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Behavior.SilentMode {
		t.Error("the checkout's false did not override the person's true")
	}
	if !proj.Sets("behavior.silent_mode") {
		t.Errorf("a key written at its zero is not reported as set: %v", proj.Keys)
	}
}

// Each refused key refuses the load with the reason and the file it belongs
// in. Dropping the key and starting would leave a person reading their
// checkout's file and a session running without what it says.
func TestLayerProject_EachRefusedKeyRefusesWithItsReason(t *testing.T) {
	for _, tc := range []struct{ name, text, key string }{
		{"provider key", "[provider]\napi_key = \"sk-in-the-repo\"\n", "provider.api_key"},
		{"provider key variable", "[provider]\napi_key_env = \"THEIR_KEY\"\n", "provider.api_key_env"},
		{"search key", "[web]\nsearch_api_key = \"brave\"\n", "web.search_api_key"},
		{"search key variable", "[web]\nsearch_api_key_env = \"THEIR_KEY\"\n", "web.search_api_key_env"},
		{"declared secrets", "[secrets]\nenv = [\"AWS_SECRET_ACCESS_KEY\"]\n", "secrets.env"},
		{"containment", "[sandbox]\nprofile = \"workspace\"\n", "sandbox"},
		{"an empty containment table", "[sandbox]\n", "sandbox"},
		{"servers", "[mcp.servers.theirs]\ncommand = \"node\"\n", "mcp.servers.theirs"},
		{"wordings", "[prompts]\nsteer = \"/etc/passwd\"\n", "prompts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeProject(t, tc.text)
			_, _, err := LayerProject(Config{}, path)
			var refused *ProjectKeyError
			if !errors.As(err, &refused) {
				t.Fatalf("the checkout's file loaded: %v", err)
			}
			if len(refused.Keys) != 1 || refused.Keys[0].Key != tc.key {
				t.Fatalf("the refusal does not name %s: %+v", tc.key, refused.Keys)
			}
			msg := refused.Error()
			if !strings.Contains(msg, refused.Keys[0].Reason) {
				t.Errorf("the refusal does not say why: %s", msg)
			}
			if refused.User != "" && !strings.Contains(msg, refused.User) {
				t.Errorf("the refusal does not name the file the key belongs in: %s", msg)
			}
		})
	}
}

// A refused table is one mistake and reads as one: naming the keys beneath
// it as well would report several.
func TestLayerProject_ARefusedTableIsNamedOnceWithTheKeysUnderIt(t *testing.T) {
	path := writeProject(t, "[sandbox]\nprofile = \"workspace-netless\"\ncontainer_pids = 64\n")
	_, _, err := LayerProject(Config{}, path)
	var refused *ProjectKeyError
	if !errors.As(err, &refused) {
		t.Fatalf("the containment table loaded: %v", err)
	}
	if len(refused.Keys) != 1 {
		t.Fatalf("a table and its keys read as %d mistakes: %+v", len(refused.Keys), refused.Keys)
	}
}

// The key beside a refused one is not refused with it: the set is exact, and
// `api_key_env` and `api_key` are two entries rather than a prefix.
func TestRefusedInProject_MatchesAKeyOrTheTableAboveIt(t *testing.T) {
	for _, key := range []string{"provider.api_key", "provider.api_key_env", "sandbox.profile", "prompts.steer", "mcp.servers.x.command"} {
		if RefusedInProject(key) == "" {
			t.Errorf("%s is not refused in a checkout's file", key)
		}
	}
	for _, key := range []string{"provider.model", "provider.base_url", "mcp.disabled", "behavior.default_mode", "web.search_provider"} {
		if reason := RefusedInProject(key); reason != "" {
			t.Errorf("%s is refused in a checkout's file: %s", key, reason)
		}
	}
}

// A checkout's file gets the same reading a user's does: a key no setting
// reads stops the load with the nearest key it might have been.
func TestLayerProject_AnUnknownKeyIsRefusedTheSameWay(t *testing.T) {
	path := writeProject(t, "[behaviour]\nsilent_mode = true\n")
	_, _, err := LayerProject(Config{}, path)
	var unknown *UnknownKeyError
	if !errors.As(err, &unknown) {
		t.Fatalf("a misspelled table in a checkout's file loaded: %v", err)
	}
}

// A checkout with no file of its own leaves the person's settings and says
// nothing, which is what almost every repository is.
func TestLayerProject_NoFileIsNoChange(t *testing.T) {
	root := t.TempDir()
	cfg, proj, err := LayerProject(Config{Provider: ProviderConfig{Model: "gpt-4o"}},
		filepath.Join(root, ".shhh", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Model != "gpt-4o" || proj.Loaded() || len(proj.Keys) != 0 {
		t.Errorf("a checkout with no file changed something: %+v %+v", cfg.Provider, proj)
	}
}

// ProjectFileAt is the stat every command pays instead of establishing trust
// for a checkout with nothing to gate.
func TestProjectFileAt_IsTheFileOrNothing(t *testing.T) {
	path := writeProject(t, "[behavior]\ndefault_mode = \"plan\"\n")
	root := filepath.Dir(filepath.Dir(path))
	if got := ProjectFileAt(root); got != path {
		t.Errorf("the checkout's file was not found: %q", got)
	}
	if got := ProjectFileAt(t.TempDir()); got != "" {
		t.Errorf("a directory with no file answered %q", got)
	}
}

// A write to the user's file for a key the checkout overrides says so. The
// write stands — that file is read in every other checkout — and what must
// not happen is a confirmation reading as though the value took effect here.
func TestProject_OverriddenNoteNamesTheFileAndTheKeys(t *testing.T) {
	proj := Project{Path: "/repo/.shhh/config.toml", Display: ".shhh/config.toml",
		Keys: []string{"provider.model", "behavior.default_mode"}}
	note := proj.OverriddenNote("provider.model")
	if !strings.Contains(note, "provider.model") || !strings.Contains(note, ".shhh/config.toml") {
		t.Errorf("the note does not name the key and the file: %q", note)
	}
	if two := proj.OverriddenNote("provider.model", "behavior.default_mode"); !strings.Contains(two, "are set by") {
		t.Errorf("two keys do not read as two: %q", two)
	}
	if got := proj.OverriddenNote("appearance.mouse"); got != "" {
		t.Errorf("a key the checkout says nothing about got a note: %q", got)
	}
	if got := (Project{}).OverriddenNote("provider.model"); got != "" {
		t.Errorf("a session with no checkout file got a note: %q", got)
	}
}
