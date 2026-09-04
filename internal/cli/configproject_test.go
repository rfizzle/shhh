package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// aCheckout writes a repository carrying its own settings file and answers
// with its root. The directory is handed to the code under test rather than
// moved into: a test that chdirs records the target as one of its package's
// cache inputs.
func aCheckout(t *testing.T, text string) string {
	t.Helper()
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, ".shhh"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, ".shhh", "config.toml"), []byte(text), 0o644))
	return root
}

// trusting states this process's answer for one checkout.
func trusting(t *testing.T, root string, granted bool) {
	t.Helper()
	withProjectTrust(t, project.Trust{
		Root: root, Granted: granted, Present: []project.Kind{project.KindSettings},
	})
}

// The load every command comes through: the person's file, then the
// checkout's over it key by key, with what the checkout decided beside it.
func TestLoadLayeredConfig_TheCheckoutsFileLayersOverTheUsers(t *testing.T) {
	pointConfigAt(t, "[provider]\nmodel = \"gpt-4o\"\ndefault = \"openai\"\n")
	root := aCheckout(t, "[provider]\nmodel = \"claude-sonnet-5\"\n")
	trusting(t, root, true)

	cfg, proj, err := loadLayeredConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Model != "claude-sonnet-5" || cfg.Provider.Default != "openai" {
		t.Fatalf("the two files did not merge key by key: %+v", cfg.Provider)
	}
	if !proj.Sets("provider.model") || proj.Display != ".shhh/config.toml" {
		t.Fatalf("what the checkout set is not reported: %+v", proj)
	}
}

// An untrusted checkout's settings are withheld with the rest of what it
// declares: the session runs on the person's file alone, and the withheld
// list names them — which is what the start screen, `/status` and the
// doctor's trust row all draw.
func TestLoadLayeredConfig_AnUntrustedCheckoutsFileIsWithheld(t *testing.T) {
	pointConfigAt(t, "[provider]\nmodel = \"gpt-4o\"\n")
	root := aCheckout(t, "[provider]\nmodel = \"claude-sonnet-5\"\n")
	trusting(t, root, false)

	cfg, proj, err := loadLayeredConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Model != "gpt-4o" || proj.Loaded() {
		t.Fatalf("an untrusted checkout's settings were read: %+v %+v", cfg.Provider, proj)
	}
	if names := projectTrust().WithheldNames(); len(names) != 1 || names[0] != string(project.KindSettings) {
		t.Fatalf("the settings file is not among what the checkout withholds: %v", names)
	}
}

// A directory that is not a checkout, and one whose checkout has no file of
// its own, both come to the person's settings alone — which is almost every
// directory shhh is run in.
func TestLoadLayeredConfig_NoCheckoutFileIsTheUsersSettingsAlone(t *testing.T) {
	pointConfigAt(t, "[provider]\nmodel = \"gpt-4o\"\n")
	trusting(t, "", true)

	for _, dir := range []string{t.TempDir(), ""} {
		cfg, proj, err := loadLayeredConfig(dir)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Provider.Model != "gpt-4o" || proj.Loaded() {
			t.Fatalf("a directory with no checkout file changed something: %+v %+v", cfg.Provider, proj)
		}
	}
}

// The listing's source column: a key the checkout set says `project`, the
// person's own says `user`, and the header names both files.
func TestConfigReading_NamesTheCheckoutAsASource(t *testing.T) {
	cfg := config.Config{}
	cfg.Provider.Model = "claude-sonnet-5"
	cfg.Provider.Default = "openai"
	proj := config.Project{Path: "/repo/.shhh/config.toml", Display: ".shhh/config.toml",
		Keys: []string{"provider.model"}}

	fromProject := configReadingOf(cfg, proj, mustLookup(t, "provider.model"))
	if fromProject.Source != "project" || fromProject.Project != ".shhh/config.toml" {
		t.Errorf("the checkout's key does not name the checkout: %+v", fromProject)
	}
	own := configReadingOf(cfg, proj, mustLookup(t, "provider.default"))
	if own.Source != "user" {
		t.Errorf("the person's own key says %q", own.Source)
	}
	if got := configListSubject("~/.config/shhh/config.toml", proj); !strings.Contains(got, ".shhh/config.toml") {
		t.Errorf("the listing's header does not name both files: %q", got)
	}
	if got := configListSubject("~/.config/shhh/config.toml", config.Project{}); strings.Contains(got, ".shhh") {
		t.Errorf("a session with no checkout file named one: %q", got)
	}
}

// `config get` is where one key is read at length, so it is where the union
// rule has to be said: `project` on an allowlist means the checkout added to
// the person's list rather than took it away.
func TestConfigGet_SaysTheCheckoutSetItAndHowListsMerge(t *testing.T) {
	cfg := config.Config{}
	cfg.Behavior.CommandAllowlist = []string{"ls", "go test"}
	proj := config.Project{Path: "/repo/.shhh/config.toml", Display: ".shhh/config.toml",
		Keys: []string{"behavior.command_allowlist"}}

	body := strings.Join(configGetReport(
		configReadingOf(cfg, proj, mustLookup(t, "behavior.command_allowlist"))).
		Sections[0].Rows[0].Body, "\n")
	if !strings.Contains(body, ".shhh/config.toml") {
		t.Errorf("the reading does not name the checkout's file:\n%s", body)
	}
	if !strings.Contains(body, "adds to your list") {
		t.Errorf("the reading does not say the checkout extended it:\n%s", body)
	}
}

// `config set --project` writes the checkout's file through the same
// targeted rewrite the user's gets, so it keeps every line it did not touch,
// and what it wrote is what the next load has in force.
func TestConfigSetProject_WritesTheCheckoutsFileAndRoundTrips(t *testing.T) {
	pointConfigAt(t, "")
	held := "# what is true of this repository\n[behavior]\ndefault_mode = \"plan\"   # deliberate\n"
	root := aCheckout(t, held)
	trusting(t, root, true)

	path, err := projectWritePath("behavior.default_mode", root)
	must(t, err)
	note, err := writeConfigEdits(config.Project{}, path,
		config.Edit{Key: "behavior.default_mode", Value: "auto"})
	must(t, err)
	if note != "" {
		t.Errorf("a write to the checkout's own file reported an override: %q", note)
	}

	got, readErr := os.ReadFile(path)
	must(t, readErr)
	if want := strings.Replace(held, `"plan"`, `"auto"`, 1); string(got) != want {
		t.Fatalf("the write did not change the one line:\n--- want\n%s--- got\n%s", want, got)
	}
	cfg, proj, err := loadLayeredConfig(root)
	must(t, err)
	if cfg.Behavior.DefaultMode != "auto" || !proj.Sets("behavior.default_mode") {
		t.Fatalf("what was written is not what is in force: %q %v", cfg.Behavior.DefaultMode, proj.Keys)
	}
	// The write changed a file the checkout declares, so the answer that
	// covered it no longer does — and the confirmation says so rather than
	// leaving the reader with a value written down and not in force.
	if note := projectTrustNote(); !strings.Contains(note, "trusted again") {
		t.Errorf("the write does not say the checkout has to be trusted again: %q", note)
	}
	trusting(t, root, false)
	if note := projectTrustNote(); !strings.Contains(note, "not trusted") {
		t.Errorf("an untrusted checkout is not told its settings do not load: %q", note)
	}
}

// A key a checkout may not decide is refused before anything is created:
// writing it would leave a file that then stops every command in the
// repository.
func TestConfigSetProject_RefusesAKeyACheckoutMayNotSet(t *testing.T) {
	pointConfigAt(t, "")
	root := aCheckout(t, "")

	_, err := projectWritePath("provider.api_key", root)
	if err == nil {
		t.Fatal("a credential was accepted into the checkout's file")
	}
	if !strings.Contains(err.Error(), config.RefusedInProject("provider.api_key")) {
		t.Errorf("the refusal does not say why: %v", err)
	}
	if !strings.Contains(err.Error(), shortPath(config.WritePath())) {
		t.Errorf("the refusal does not name the file the key belongs in: %v", err)
	}
	// And every other key is written where it was asked for.
	if _, err := projectWritePath("behavior.default_mode", root); err != nil {
		t.Errorf("an ordinary key was refused: %v", err)
	}
}

// A write to the user's file for a key the checkout overrides is made — that
// file is read in every other checkout — and says so, because a confirmation
// letting the reader believe it took effect here is the one thing it must
// not do.
func TestWriteConfigEdits_SaysWhenTheCheckoutOverridesTheKey(t *testing.T) {
	user := pointConfigAt(t, "")
	proj := config.Project{Path: "/repo/.shhh/config.toml", Display: ".shhh/config.toml",
		Keys: []string{"provider.model"}}

	note, err := writeConfigEdits(proj, user, config.Edit{Key: "provider.model", Value: "gpt-4o"})
	must(t, err)
	if !strings.Contains(note, "provider.model") || !strings.Contains(note, ".shhh/config.toml") {
		t.Errorf("the confirmation does not say the checkout overrides it: %q", note)
	}
	got, readErr := os.ReadFile(user)
	must(t, readErr)
	if !strings.Contains(string(got), "gpt-4o") {
		t.Fatalf("the write was refused rather than reported:\n%s", got)
	}
	quiet, err := writeConfigEdits(proj, user, config.Edit{Key: "behavior.shell", Value: "/bin/zsh"})
	must(t, err)
	if quiet != "" {
		t.Errorf("a key the checkout never set was reported as overridden: %q", quiet)
	}
}

// The same fact reaches the slash commands that write a default, through the
// rank they already state: a default written into the user's file and then
// overruled reads exactly like one that was never saved.
func TestOutranking_NamesTheCheckoutWhereNothingAboveItDoes(t *testing.T) {
	proj := config.Project{Path: "/repo/.shhh/config.toml", Display: ".shhh/config.toml",
		Keys: []string{"provider.model"}}
	if got := outranking("", proj, "provider.model"); !strings.Contains(got, ".shhh/config.toml") {
		t.Errorf("the checkout is not named as what outranks the file: %q", got)
	}
	if got := outranking("--model gpt-4o is on the command line", proj, "provider.model"); !strings.HasPrefix(got, "--model") {
		t.Errorf("the flag stopped outranking the checkout: %q", got)
	}
	if got := outranking("", proj, "provider.reasoning"); got != "" {
		t.Errorf("a key the checkout never set is reported as outranked: %q", got)
	}
}

// The screen writes the person's file and carries the note off the surface
// with it: the screen quits on `[w]`, so there is no row left to say that
// what was just saved is not what this directory will use.
func TestConfigScreen_TheWriteCarriesTheOverriddenNote(t *testing.T) {
	user := pointConfigAt(t, "")
	proj := config.Project{Path: "/repo/.shhh/config.toml", Display: ".shhh/config.toml",
		Keys: []string{"provider.model"}}
	m := newConfigModel(config.Config{}, proj)
	m.apply(components.ConfigChange{Key: "provider.model", Value: "gpt-4o"})

	note, err := writeConfigEdits(m.proj, user, m.edits()...)
	must(t, err)
	if !strings.Contains(note, "provider.model") {
		t.Errorf("the screen's write does not say the checkout overrides it: %q", note)
	}
}

// The config screen reads the same two files: a row the checkout decided
// says `project` rather than sending the reader to their own file for it.
func TestConfigScreen_ARowTheCheckoutSetSaysProject(t *testing.T) {
	cfg := config.Config{}
	cfg.Behavior.DefaultMode = "auto"
	proj := config.Project{Path: "/repo/.shhh/config.toml", Display: ".shhh/config.toml",
		Keys: []string{"behavior.default_mode"}}

	if row := rowFor(configRows(cfg, cfg, proj), "behavior.default_mode"); row.Source != "project" {
		t.Errorf("a row the checkout decided says %q", row.Source)
	}
	if own := rowFor(configRows(cfg, cfg, config.Project{}), "behavior.default_mode"); own.Source != "user" {
		t.Errorf("without a checkout file the row says %q", own.Source)
	}
	// A staged edit to a key the checkout claims says both: the write goes
	// to the person's file, and this directory keeps reading the checkout's.
	staged := config.Config{}
	staged.Behavior.DefaultMode = "plan"
	if row := rowFor(configRows(staged, cfg, proj), "behavior.default_mode"); row.Source != "unwritten · project" {
		t.Errorf("a staged edit to a key the checkout claims says %q", row.Source)
	}
}

// The doctor's config row is where both files are read: it names the
// person's and the checkout's, and which keys the checkout decided.
func TestDoctorConfig_NamesBothFilesAndWhatTheCheckoutSet(t *testing.T) {
	proj := config.Project{Path: "/repo/.shhh/config.toml", Display: ".shhh/config.toml",
		Keys: []string{"behavior.default_mode", "provider.model"}}
	f := doctorConfig("/home/u/.config/shhh/config.toml", nil, config.Config{}, proj, nil)
	if !strings.Contains(f.Subject, "config.toml") {
		t.Errorf("the row does not name the person's file: %+v", f)
	}
	if !strings.Contains(f.Detail, ".shhh/config.toml sets 2 keys") {
		t.Errorf("the row does not name the checkout's file and its count: %+v", f)
	}
	if fix := strings.Join(f.Fix, "\n"); !strings.Contains(fix, "behavior.default_mode, provider.model") {
		t.Errorf("the row does not list which keys the checkout set: %+v", f)
	}
	bare := doctorConfig("/home/u/.config/shhh/config.toml", nil, config.Config{}, config.Project{}, nil)
	if strings.Contains(bare.Detail, ".shhh") {
		t.Errorf("a machine with no checkout file named one: %+v", bare)
	}
}
