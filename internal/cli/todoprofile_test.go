package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// aProfileDir writes a profile directory holding a shortest-possible profile
// under the name given, and answers with the directory. The noun is what
// tells two of them apart on screen.
func aProfileDir(t *testing.T, dir, name, noun string) string {
	t.Helper()
	must(t, os.MkdirAll(filepath.Join(dir, run.ProfileWordings), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, run.ProfileFile), []byte(`
name = "`+name+`"
noun = "`+noun+`"

[[field]]
name = "kind"
values = [{ name = "reading" }]

[[field]]
name = "priority"

[[step]]
name = "read"
kind = "turn"
mode = "read"

[[step]]
name = "file"
kind = "finish"
finish = "archive"
`), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, run.ProfileWordings, "read.md"), []byte("READ.\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, run.ProfileWordings, run.WordingStandards+".md"), []byte("STANDARDS.\n"), 0o644))
	return dir
}

// withoutHeldProfile drops what the process resolved, so one test's answer is
// not another's.
func withoutHeldProfile(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		profileHeld.mu.Lock()
		profileHeld.read = nil
		profileHeld.mu.Unlock()
	})
}

// A project that says nothing runs the profile a checkout of code has always
// run, and it is the one this binary holds rather than a file it has to find.
func TestBacklogProfile_TheDefaultIsTheOneShhhShips(t *testing.T) {
	withoutHeldProfile(t)
	pointConfigAt(t, "")
	trusting(t, "", true)
	p := backlogProfileFor(config.Config{})
	if p.err != nil || p.name() != "code" || p.words.Noun != "item" {
		t.Fatalf("the default profile is %+v (%v)", p.words, p.err)
	}
	if !p.pipeline.Runs() || p.pipeline.Name != "code" {
		t.Fatalf("the default run is %q", p.pipeline.Name)
	}
}

// The three places, most specific first: the checkout's own directory, the
// person's by name, then the profiles in the binary. What is true of a
// repository travels with the repository, which is why its own wins.
func TestBacklogProfile_TheCheckoutsOwnBeatsThePersonsByTheSameName(t *testing.T) {
	withoutHeldProfile(t)
	home := pointConfigAt(t, "")
	mine := filepath.Join(filepath.Dir(home), todoProfileDirName, "reading")
	aProfileDir(t, mine, "reading", "reading of mine")

	root := t.TempDir()
	aProfileDir(t, filepath.Join(root, filepath.FromSlash(project.TodoProfileDir)), "reading", "reading of theirs")
	trusting(t, root, true)

	cfg := config.Config{}
	cfg.Todo.Profile = "reading"
	if got := backlogProfileFor(cfg); got.words.Noun != "reading of theirs" {
		t.Fatalf("the checkout's own profile did not win: %+v %v", got.words, got.err)
	}

	// And outside that checkout the person's own stands: the directory is a
	// fact about the repository, not about the machine.
	trusting(t, "", true)
	if got := backlogProfileFor(cfg); got.words.Noun != "reading of mine" {
		t.Fatalf("outside the checkout the profile is %+v %v", got.words, got.err)
	}
}

// A checkout's profile is text and a run shape it hands the session, so it is
// withheld until the person has answered for the checkout — the same rule the
// wordings are under, and the withheld list names it in the same sentence.
func TestBacklogProfile_AnUntrustedCheckoutsProfileIsWithheld(t *testing.T) {
	withoutHeldProfile(t)
	pointConfigAt(t, "")
	root := t.TempDir()
	aProfileDir(t, filepath.Join(root, filepath.FromSlash(project.TodoProfileDir)), "reading-theirs", "reading")
	withProjectTrust(t, project.Trust{Root: root, Present: []project.Kind{project.KindProfile}})

	if got := backlogProfileFor(config.Config{}); got.name() != "code" {
		t.Fatalf("an untrusted checkout's profile was read: %+v %v", got.words, got.err)
	}
	if note := trustStartupNote(); !strings.Contains(note, string(project.KindProfile)) {
		t.Fatalf("the withheld list does not name the profile: %q", note)
	}
}

// A name nothing answers to stops every command, and names all three places
// rather than the last one it looked in: the person who typed the name is
// deciding where to put the directory.
func TestBacklogProfile_AnUnknownNameNamesTheThreePlaces(t *testing.T) {
	withoutHeldProfile(t)
	pointConfigAt(t, "")
	trusting(t, "", true)
	cfg := config.Config{}
	cfg.Todo.Profile = "reserach"
	err := backlogProfileFor(cfg).err
	if err == nil {
		t.Fatal("an unknown profile name loaded")
	}
	for _, want := range []string{project.TodoProfileDir, filepath.Join(todoProfileDirName, "reserach"), "code", "research"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not name %q: %v", want, err)
		}
	}
}

// A profile of the person's own is named by its directory, because that is
// the name the settings key holds. A table calling itself something else
// would leave the key naming one thing and every sentence about the profile
// naming another.
func TestBacklogProfile_ThePersonsProfileIsNamedByItsDirectory(t *testing.T) {
	withoutHeldProfile(t)
	home := pointConfigAt(t, "")
	aProfileDir(t, filepath.Join(filepath.Dir(home), todoProfileDirName, "reading"), "readings", "reading")
	trusting(t, "", true)

	cfg := config.Config{}
	cfg.Todo.Profile = "reading"
	err := backlogProfileFor(cfg).err
	if err == nil {
		t.Fatal("a profile under one name calling itself another loaded")
	}
	if !strings.Contains(err.Error(), "readings") || !strings.Contains(err.Error(), run.ProfileFile) {
		t.Fatalf("the refusal does not say what disagrees: %v", err)
	}
}

// A profile that will not load stops the command rather than being half
// applied: a run built on the steps of one profile and the fields of another
// is what the validator exists to stop.
func TestBacklogProfile_ABrokenProfileStopsTheCommand(t *testing.T) {
	withoutHeldProfile(t)
	home := pointConfigAt(t, "")
	mine := aProfileDir(t, filepath.Join(filepath.Dir(home), todoProfileDirName, "reading"), "reading", "reading")
	must(t, os.WriteFile(filepath.Join(mine, run.ProfileFile), []byte("name = \"reading\"\n"), 0o644))
	trusting(t, "", true)

	cfg := config.Config{}
	cfg.Todo.Profile = "reading"
	got := backlogProfileFor(cfg)
	if got.err == nil {
		t.Fatal("a profile with no fields loaded")
	}
	if got.pipeline.Stated() {
		t.Fatalf("a refused profile left a run behind: %+v", got.pipeline)
	}
}

// The refusal arrives at startup, before a command has done anything: every
// command comes through the one place the settings are read, and a project
// whose items would be read under words nobody chose is one no verb should
// run in.
func TestBacklogProfile_AnUnknownNameStopsEveryCommand(t *testing.T) {
	withoutHeldProfile(t)
	pointConfigAt(t, "[todo]\nprofile = \"reserach\"\n")
	trusting(t, "", true)

	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"todo"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("a command ran under a profile that is not there")
	}
	if !strings.Contains(err.Error(), "reserach") || !strings.Contains(err.Error(), project.TodoProfileDir) {
		t.Fatalf("the refusal does not say what is missing or where it looked: %v", err)
	}
}

// The key is a settings row like any other, so the scaffold writes it
// commented at its default and the listing states where its value came from.
func TestBacklogProfile_TheKeyIsInTheScaffold(t *testing.T) {
	if !strings.Contains(config.Scaffold(config.Config{}, false), "#profile = \"code\"") {
		t.Fatal("`shhh config init` does not write the profile key at its default")
	}
}

// Every reader is handed the one profile this process resolved: a screen
// drawing an item's fields from one profile while the runner spends turns
// against another would be showing a backlog nobody has.
func TestBacklogProfile_EveryReaderGetsTheOneAnswer(t *testing.T) {
	withoutHeldProfile(t)
	home := pointConfigAt(t, "")
	aProfileDir(t, filepath.Join(filepath.Dir(home), todoProfileDirName, "reading"), "reading", "reading")
	trusting(t, "", true)

	cfg := config.Config{}
	cfg.Todo.Profile = "reading"
	if err := backlogProfileFor(cfg).err; err != nil {
		t.Fatal(err)
	}
	if todoProfile().Noun != "reading" || todoPipeline().Name != "reading" {
		t.Fatalf("the readers disagree: %q %q", todoProfile().Noun, todoPipeline().Name)
	}
}

// Where a backlog goes when the working directory is part of no project.
// See docs/capabilities/todo.md#where-the-backlog-lives.
func TestBacklogElsewhere_ResolvesTheRootTheSettingsName(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", filepath.Join(dir, "home"))

	bare := backlogElsewhere(config.Config{})
	if want := filepath.Join(dir, "shhh", "todo", "backlog"); bare.Global != want {
		t.Errorf("the global backlog is %s, want %s", bare.Global, want)
	}
	if bare.Setting != "" {
		t.Errorf("settings that say nothing name no root, got %s", bare.Setting)
	}

	// A place on the person's machine is written the way they write one.
	home := backlogElsewhere(config.Config{Todo: config.TodoConfig{Root: filepath.Join("~", "notes")}})
	if want := filepath.Join(dir, "home", "notes"); home.Setting != want {
		t.Errorf("a ~ path resolves to %s, want %s", home.Setting, want)
	}

	// A relative one is resolved against the settings file, not against
	// whichever directory the session was opened in.
	rel := backlogElsewhere(config.Config{Todo: config.TodoConfig{Root: "notes"}})
	if want := filepath.Join(dir, "shhh", "notes"); rel.Setting != want {
		t.Errorf("a relative path resolves to %s, want %s", rel.Setting, want)
	}
}
