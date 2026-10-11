package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
)

// An empty config directory gets the whole settings file and no wording: a
// file under a prompts directory is the override, so a copy of a built-in
// would be a stale override waiting for the built-in to move.
func TestConfigInit_WritesTheSettingsAndNoWordings(t *testing.T) {
	path := pointConfigAt(t, "")
	out := runRoot(t, "config", "init")

	text, err := os.ReadFile(path)
	must(t, err)
	if !strings.HasPrefix(string(text), "# shhh settings.") {
		t.Fatalf("the settings file is not the scaffold:\n%s", text)
	}
	cfg, err := config.LoadFrom(path)
	must(t, err)
	// Nothing is set by it: a commented default is not a value in the file,
	// which is the difference between a scaffold and the flattening a write
	// must never do.
	for _, s := range config.Settings() {
		if strings.Contains(s.Key, "*") {
			continue
		}
		if _, set := config.Value(cfg, s.Key); set {
			t.Errorf("the scaffold set %s", s.Key)
		}
	}

	if exists(filepath.Join(filepath.Dir(path), "prompts")) {
		t.Fatal("init made a prompts directory, which exists only when an override does")
	}
	// The receipt says where an override goes and what a file there does.
	flat := strings.Join(strings.Fields(out), " ")
	if !strings.Contains(flat, "a file at <key>.md here replaces that built-in wording whole") {
		t.Errorf("the receipt does not say where an override goes:\n%s", out)
	}
}

// A session after the command hashes as one before it: nothing was written
// that a session reads. An edit to a wording does divide it, which is what
// the override is for.
func TestConfigInit_ASessionAfterItHashesAsOneBefore(t *testing.T) {
	path := pointConfigAt(t, "")
	const sys = "the system prompt"

	before, err := loadPrompts(config.PromptsConfig{}, "")
	must(t, err)
	runRoot(t, "config", "init")
	after, err := loadPrompts(config.PromptsConfig{}, "")
	must(t, err)
	if fingerprint(after.fingerprintOf(sys)) != fingerprint(before.fingerprintOf(sys)) {
		t.Fatal("init divided the record")
	}
	dir := filepath.Join(filepath.Dir(path), "prompts")
	must(t, os.MkdirAll(dir, 0o700))
	must(t, os.WriteFile(filepath.Join(dir, "steer.md"), []byte("steer as edited"), 0o600))
	edited, err := loadPrompts(config.PromptsConfig{}, "")
	must(t, err)
	if fingerprint(edited.fingerprintOf(sys)) == fingerprint(before.fingerprintOf(sys)) {
		t.Fatal("an edited wording must divide the record")
	}
}

// A file that is already there is never written over, and neither is
// anything beside it: the command stops, names what is in the way, and says
// what to run instead.
func TestConfigInit_RefusesWhatIsAlreadyThere(t *testing.T) {
	path := pointConfigAt(t, "[provider]\nmodel = \"claude-sonnet-5\"\n")
	got := runRootErr(t, "config", "init").Error()
	if !strings.Contains(got, shortPath(path)) || !strings.Contains(got, "--stdout") {
		t.Fatalf("the refusal does not name the file and the way out: %s", got)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "prompts")); !os.IsNotExist(err) {
		t.Fatal("the command wrote the wordings after refusing the settings")
	}
	// The person's file is untouched, which is the whole reason for stopping.
	text, err := os.ReadFile(path)
	must(t, err)
	if string(text) != "[provider]\nmodel = \"claude-sonnet-5\"\n" {
		t.Fatalf("the file was rewritten:\n%s", text)
	}
}

// A wording somebody wrote is an override and not a thing in the way: init
// writes the settings beside it and leaves the file alone.
func TestConfigInit_LeavesAWrittenWordingAlone(t *testing.T) {
	path := pointConfigAt(t, "")
	dir := filepath.Join(filepath.Dir(path), "prompts")
	must(t, os.MkdirAll(dir, 0o700))
	must(t, os.WriteFile(filepath.Join(dir, "steer.md"), []byte("mine"), 0o600))

	runRoot(t, "config", "init")
	if !exists(path) {
		t.Fatal("the settings file was not written")
	}
	body, err := os.ReadFile(filepath.Join(dir, "steer.md"))
	must(t, err)
	if string(body) != "mine" {
		t.Fatalf("the override was rewritten: %q", body)
	}
}

// `--stdout` is what the person with a file already gets: the same scaffold,
// with what they have written filled in uncommented, so expanding a
// three-line file is a print and a paste.
func TestConfigInit_StdoutFillsInTheValuesTheFileHolds(t *testing.T) {
	path := pointConfigAt(t, "[provider]\nmodel = \"claude-sonnet-5\"\n")
	out := runRoot(t, "config", "init", "--stdout")
	if !strings.Contains(out, "model = \"claude-sonnet-5\"") {
		t.Fatalf("the value in the file was not filled in:\n%s", out)
	}
	if !strings.Contains(out, "#default = \"openai-responses\"") {
		t.Fatalf("a key nothing sets is not commented at its default:\n%s", out)
	}
	// Printing is not writing: the file is what it was and nothing else was
	// created beside it.
	text, err := os.ReadFile(path)
	must(t, err)
	if string(text) != "[provider]\nmodel = \"claude-sonnet-5\"\n" {
		t.Fatalf("--stdout wrote the file:\n%s", text)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "prompts")); !os.IsNotExist(err) {
		t.Fatal("--stdout wrote the wordings")
	}
}

// A checkout's pair is the same scaffold with the keys a checkout may not
// decide left out, and no wording.
func TestConfigInit_ProjectWritesTheCheckoutsSettings(t *testing.T) {
	pointConfigAt(t, "")
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))

	plan, err := configInit(false, root)
	must(t, err)
	must(t, plan.write())

	text, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(project.ConfigFile)))
	must(t, err)
	for _, absent := range []string{"[sandbox]", "[prompts]", "api_key"} {
		if strings.Contains(string(text), absent) {
			t.Errorf("the checkout's scaffold holds %q", absent)
		}
	}
	if !strings.Contains(string(text), "#default_mode") {
		t.Errorf("the checkout's scaffold is missing the keys it may decide:\n%s", text)
	}
	if exists(filepath.Join(root, filepath.FromSlash(project.PromptsDir))) {
		t.Fatal("the checkout's prompts directory was made with no override in it")
	}
}

// The wording a checkout supplies is one the reader never wrote, so the
// start screen names it the way it names the checkout's settings file.
func TestProjectWordings_NamesWhatTheCheckoutSupplied(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "steer.md"), []byte("the checkout's own"), 0o600))
	got := projectWordings(config.PromptsConfig{}, dir)
	if len(got) != 1 || got[0] != "steer" {
		t.Fatalf("the checkout's wordings = %v", got)
	}
	if named := projectWordings(config.PromptsConfig{}, ""); named != nil {
		t.Fatalf("a session with no checkout names none, got %v", named)
	}
}
