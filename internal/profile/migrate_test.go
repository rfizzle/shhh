package profile

// One file for every provider (S-142): the [[provider]] form, the
// providers.toml that holds it, and folding a providers/ directory into one.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const multiTOML = `
[[provider]]
name        = "gateway"
base_url    = "https://gw.example/v1"
api_key_env = "GATEWAY_API_KEY"

  [provider.headers]
  X-Title = "shhh"

  [[provider.models]]
  id             = "gpt-5.2"
  context_window = 400000
  cost           = { input = 1.25, output = 10 }

  [[provider.endpoint]]
  match    = ["claude-*"]
  api      = "anthropic-messages"
  base_url = "https://gw.example/anthropic"

    [[provider.endpoint.models]]
    id = "claude-opus-5"

[[provider]]
name     = "local"
base_url = "http://localhost:11434/v1"
`

func TestLoadFile_ReadsSeveralProvidersFromOneFile(t *testing.T) {
	path := writeProfile(t, t.TempDir(), "providers.toml", multiTOML)
	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(loaded) != 2 || loaded[0].Name != "gateway" || loaded[1].Name != "local" {
		t.Fatalf("expected both providers in file order, got %+v", loaded)
	}
	gw := loaded[0]
	if gw.Path != path {
		t.Fatalf("each provider should name the file it came from, got %q", gw.Path)
	}
	if gw.Headers["X-Title"] != "shhh" || gw.API != APIOpenAIChat {
		t.Fatalf("unexpected gateway: %+v", gw)
	}
	if len(gw.Endpoints) != 1 || gw.Route("claude-opus-5").BaseURL != "https://gw.example/anthropic" {
		t.Fatalf("the nested endpoint should route, got %+v", gw.Endpoints)
	}
	if loaded[1].BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("unexpected second provider: %+v", loaded[1])
	}
}

func TestLoadFile_RejectsMixedAndUnnamedProviders(t *testing.T) {
	tests := []struct{ name, body, want string }{
		{
			"both forms in one file",
			"base_url = \"https://gw.example/v1\"\n\n[[provider]]\nname = \"gw\"\nbase_url = \"https://other.example/v1\"",
			"not both",
		},
		{
			"an unnamed provider",
			"[[provider]]\nbase_url = \"https://gw.example/v1\"",
			"name is required",
		},
		{
			"the same name twice",
			"[[provider]]\nname = \"gw\"\nbase_url = \"https://a.example/v1\"\n\n[[provider]]\nname = \"gw\"\nbase_url = \"https://b.example/v1\"",
			"declared twice",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeProfile(t, t.TempDir(), "providers.toml", tc.body)
			_, err := LoadFile(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error mentioning %q, got %v", tc.want, err)
			}
		})
	}
}

func TestLoad_ReadsTheOneFileBeforeTheDirectoryBesideIt(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "providers")
	writeProfile(t, root, "providers.toml", "[[provider]]\nname = \"gateway\"\nbase_url = \"https://one-file.example/v1\"\n\n[[provider]]\nname = \"only-in-file\"\nbase_url = \"https://x.example/v1\"")
	writeProfile(t, dir, "gateway.toml", "base_url = \"https://directory.example/v1\"")
	writeProfile(t, dir, "only-in-dir.toml", "base_url = \"https://y.example/v1\"")

	profiles, errs := Load([]string{dir})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	byName := map[string]Profile{}
	for _, p := range profiles {
		byName[p.Name] = p
	}
	if len(byName) != 3 {
		t.Fatalf("both sources should load, got %d: %+v", len(byName), profiles)
	}
	if got := byName["gateway"].BaseURL; got != "https://one-file.example/v1" {
		t.Fatalf("the one file should win the name collision, got %q", got)
	}
	if _, ok := byName["only-in-dir"]; !ok {
		t.Fatal("a provider only in the directory should still load")
	}
}

func TestFiles_SitBesideEachProfileDirectory(t *testing.T) {
	got := Files(Dirs([]string{"/home/x/.config/shhh/config.toml"}))
	want := "/home/x/.config/shhh/providers.toml"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v, want [%s]", got, want)
	}
}

func TestEncode_RoundTripsThroughTheLoader(t *testing.T) {
	// The migration's output has to be a file the loader reads back
	// identically, or a migration silently changes what a session talks to.
	dir := t.TempDir()
	original, err := LoadFile(writeProfile(t, dir, "gateway.toml", routedTOML))
	if err != nil {
		t.Fatal(err)
	}
	rendered := Encode(original)
	reloaded, err := LoadFile(writeProfile(t, dir, "providers.toml", rendered))
	if err != nil {
		t.Fatalf("the emitted file should load:\n%s\nerror: %v", rendered, err)
	}
	if len(reloaded) != 1 {
		t.Fatalf("want one provider, got %d", len(reloaded))
	}
	before, after := original[0], reloaded[0]
	if before.Name != after.Name || before.BaseURL != after.BaseURL || before.APIKeyEnv != after.APIKeyEnv ||
		before.ModelsPath != after.ModelsPath || before.Headers["X-Title"] != after.Headers["X-Title"] {
		t.Fatalf("identity changed:\n%+v\n%+v", before, after)
	}
	if len(before.Endpoints) != len(after.Endpoints) || len(before.Rewrite) != len(after.Rewrite) {
		t.Fatalf("structure changed:\n%+v\n%+v", before, after)
	}
	// The metadata that would otherwise quietly disappear.
	claudeBefore, claudeAfter := before.Route("claude-opus-5"), after.Route("claude-opus-5")
	if claudeBefore.BaseURL != claudeAfter.BaseURL || claudeBefore.API != claudeAfter.API {
		t.Fatalf("routing changed: %+v vs %+v", claudeBefore, claudeAfter)
	}
	if claudeAfter.Models[0].Cost.Input != 5.0 || claudeAfter.Models[0].ContextWindow != 1000000 {
		t.Fatalf("model metadata did not survive: %+v", claudeAfter.Models[0])
	}
	if claudeAfter.Headers["anthropic-beta"] == "" {
		t.Fatal("endpoint headers did not survive")
	}
	if before.Rewrite[0].Note != after.Rewrite[0].Note {
		t.Fatal("a rule's note did not survive — it is why the rule exists")
	}
}

func TestPlan_FoldsEveryFileIntoOne(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "providers")
	writeProfile(t, dir, "gateway.toml", routedTOML)
	writeProfile(t, dir, "local.toml", "base_url = \"http://localhost:11434/v1\"")

	plan := Plan([]string{dir})
	if len(plan.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", plan.Errors)
	}
	if plan.Target != filepath.Join(root, "providers.toml") {
		t.Fatalf("unexpected target: %s", plan.Target)
	}
	if len(plan.Profiles) != 2 || plan.Profiles[0].Name != "gateway" || plan.Profiles[1].Name != "local" {
		t.Fatalf("expected both providers in load order, got %+v", plan.Profiles)
	}
	if !plan.NeedsWork() || len(plan.Redundant()) != 2 {
		t.Fatalf("both directory files should be redundant, got %v", plan.Redundant())
	}

	if err := plan.Write(); err != nil {
		t.Fatalf("write: %v", err)
	}
	removed, err := plan.Prune()
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if len(removed) != 3 {
		t.Fatalf("both files and the empty directory should go, got %v", removed)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("the emptied providers directory should have been removed")
	}

	// What the session sees afterwards is what it saw before.
	after, errs := Load([]string{dir})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(after) != 2 || after[0].Name != "gateway" || after[1].Name != "local" {
		t.Fatalf("the consolidated file should load the same providers, got %+v", after)
	}
	if after[0].Route("claude-opus-5").API != APIAnthropicMessage {
		t.Fatal("routing should survive the migration")
	}

	// And running it again is a no-op rather than a second migration.
	if again := Plan([]string{dir}); again.NeedsWork() {
		t.Fatalf("a migrated machine has nothing left to fold: %+v", again)
	}
}

func TestPlan_KeepsTheFileThatAlreadyWonAndReportsTheShadowed(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "providers")
	writeProfile(t, root, "providers.toml", "[[provider]]\nname = \"gateway\"\nbase_url = \"https://one-file.example/v1\"")
	writeProfile(t, dir, "gateway.toml", "base_url = \"https://directory.example/v1\"")

	plan := Plan([]string{dir})
	if len(plan.Profiles) != 1 || plan.Profiles[0].BaseURL != "https://one-file.example/v1" {
		t.Fatalf("the winning provider should be the one Load already used, got %+v", plan.Profiles)
	}
	if len(plan.Shadowed) != 1 || plan.Shadowed[0].Name != "gateway" {
		t.Fatalf("the losing file should be reported, not silently dropped, got %+v", plan.Shadowed)
	}
	// The shadowed file is dead — Load already ignores it — so it is
	// redundant in the same way a folded-in file is. This is the state a
	// machine is left in between `migrate` and `migrate --prune`, and a plan
	// that found nothing to do here would strand the originals forever.
	if !plan.NeedsWork() || len(plan.Redundant()) != 1 {
		t.Fatalf("the dead file should be prunable, got %v", plan.Redundant())
	}
	if len(plan.Sources) != 1 || plan.Sources[0] != plan.Target {
		t.Fatalf("only the target contributed a provider, got %v", plan.Sources)
	}
}

func TestPlan_PrunesAfterAnEarlierMigrationWroteTheFile(t *testing.T) {
	// The two-step flow the command recommends: migrate, then prune. The
	// second run sees every original shadowed by the file the first wrote.
	root := t.TempDir()
	dir := filepath.Join(root, "providers")
	writeProfile(t, dir, "gateway.toml", routedTOML)
	writeProfile(t, dir, "local.toml", "base_url = \"http://localhost:11434/v1\"")

	if err := Plan([]string{dir}).Write(); err != nil {
		t.Fatalf("write: %v", err)
	}
	second := Plan([]string{dir})
	if !second.NeedsWork() {
		t.Fatal("the originals are still on disk and still redundant")
	}
	removed, err := second.Prune()
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if len(removed) != 3 {
		t.Fatalf("both originals and the empty directory should go, got %v", removed)
	}
	profiles, errs := Load([]string{dir})
	if len(errs) != 0 || len(profiles) != 2 {
		t.Fatalf("the consolidated file should still hold everything, got %+v %v", profiles, errs)
	}
	if Plan([]string{dir}).NeedsWork() {
		t.Fatal("a migrated machine has nothing left to do")
	}
}

func TestWrite_RefusesWhenSomethingWouldNotLoad(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "providers")
	writeProfile(t, dir, "good.toml", "base_url = \"https://gw.example/v1\"")
	writeProfile(t, dir, "broken.toml", "base_url = ")

	plan := Plan([]string{dir})
	if len(plan.Errors) == 0 {
		t.Fatal("the broken file should be reported")
	}
	if err := plan.Write(); err == nil {
		t.Fatal("a provider that failed to parse must not silently vanish from the new file")
	}
	if _, err := os.Stat(plan.Target); !os.IsNotExist(err) {
		t.Fatal("nothing should have been written")
	}
}

func TestEncode_KeepsAnUnsetDiscoverySwitchUnset(t *testing.T) {
	// The pointer's whole job is telling "inherit" from "override to off".
	// Emitting `false` for an unset field would turn every migrated endpoint
	// into one that overrides its profile.
	dir := t.TempDir()
	off := true
	profiles := []Profile{{
		Name: "gateway", API: APIOpenAIChat, BaseURL: "https://gw.example/v1",
		DiscoveryDisabled: &off,
		Endpoints: []Endpoint{
			{Match: []string{"quiet-*"}, BaseURL: "https://gw.example/quiet"},
			{Match: []string{"loud-*"}, BaseURL: "https://gw.example/loud", DiscoveryDisabled: new(bool)},
		},
	}}
	rendered := Encode(profiles)
	if strings.Count(rendered, "discovery_disabled") != 2 {
		t.Fatalf("want the profile's true and the endpoint's explicit false, got:\n%s", rendered)
	}

	reloaded, err := LoadFile(writeProfile(t, dir, "providers.toml", rendered))
	if err != nil {
		t.Fatalf("the emitted file should load:\n%s\nerror: %v", rendered, err)
	}
	p := reloaded[0]
	if !p.Route("anything").DiscoveryOff() {
		t.Fatal("the profile's switch did not survive")
	}
	if !p.Route("quiet-1").DiscoveryOff() {
		t.Fatal("the endpoint that said nothing should still inherit off")
	}
	if p.Route("loud-1").DiscoveryOff() {
		t.Fatal("the endpoint's explicit false did not survive")
	}
}
