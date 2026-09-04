package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
)

// An empty config directory gets the whole file and the whole directory of
// wordings, and what it wrote loads.
func TestConfigInit_WritesTheSettingsAndTheWordings(t *testing.T) {
	path := pointConfigAt(t, "")
	runRoot(t, "config", "init")

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

	dir := filepath.Join(filepath.Dir(path), "prompts")
	for _, w := range wordingKeys() {
		body, err := os.ReadFile(filepath.Join(dir, w.key+".md"))
		if err != nil {
			t.Fatalf("%s: %v", w.key, err)
		}
		if strings.TrimSpace(string(body)) != strings.TrimSpace(w.builtin()) {
			t.Errorf("%s does not hold the built-in wording", w.key)
		}
	}
}

// The whole point of a scaffold of the built-in wordings is that running one
// changes nothing. A session started after it must land in the same cohort
// as one started before, or the command divides the record on a change
// nobody made.
func TestConfigInit_ASessionAfterItHashesAsOneBefore(t *testing.T) {
	path := pointConfigAt(t, "")
	const sys = "the system prompt"

	before, err := loadPrompts(config.PromptsConfig{}, "")
	must(t, err)
	runRoot(t, "config", "init")
	after, err := loadPrompts(config.PromptsConfig{}, "")
	must(t, err)

	// The wordings are read, so the session is running the files.
	if after.steer == "" || after.todo["commit"] == "" {
		t.Fatalf("the scaffolded wordings were not read back: %+v", after)
	}
	if fingerprint(after.fingerprintOf(sys)) != fingerprint(before.fingerprintOf(sys)) {
		t.Fatal("a scaffold of the built-in wordings divided the record")
	}
	// An edit to one of them does divide it, which is what the files are for.
	must(t, os.WriteFile(filepath.Join(filepath.Dir(path), "prompts", "steer.md"), []byte("steer as edited"), 0o600))
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

// One wording in the way is enough to stop it, so a directory half full of
// somebody's own prose never gains files they did not ask for.
func TestConfigInit_RefusesAWordingThatIsAlreadyThere(t *testing.T) {
	path := pointConfigAt(t, "")
	dir := filepath.Join(filepath.Dir(path), "prompts")
	must(t, os.MkdirAll(dir, 0o700))
	must(t, os.WriteFile(filepath.Join(dir, "steer.md"), []byte("mine"), 0o600))

	got := runRootErr(t, "config", "init").Error()
	// Named, not counted: the reader's next act is to look at that file and
	// decide whether to keep it.
	if !strings.Contains(got, "steer") || !strings.Contains(got, shortPath(dir)) {
		t.Fatalf("the refusal does not name the wording in the way: %s", got)
	}
	if strings.Contains(got, "check_in") {
		t.Fatalf("the refusal names a wording that was not in the way: %s", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the settings file was written past a wording that was in the way")
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
	if !strings.Contains(out, "#default = \"openai\"") {
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
// decide left out, and its wordings under the directory a trusted checkout
// hands the session.
func TestConfigInit_ProjectWritesTheCheckoutsPair(t *testing.T) {
	pointConfigAt(t, "")
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))

	plan, err := configInit(true, root)
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
	// The wordings go under the directory a checkout's files live in, which
	// is what makes them the checkout's wordings rather than a path in it.
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(project.PromptsDir), "steer.md")); err != nil {
		t.Fatalf("the checkout's wordings were not written: %v", err)
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
