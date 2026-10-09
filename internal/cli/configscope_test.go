package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// scopeCase is one place a bare write can be run from, and whether it lands
// in the checkout's file. The four are the whole rule: a checkout, not a
// checkout, the home directory — here a home that is itself a repository,
// the case the rule is for — and a checkout with `--global`.
type scopeCase struct {
	name      string
	global    bool
	toProject bool
	// where answers the directory the command stands in, given the home
	// directory and a checkout elsewhere.
	where func(t *testing.T, home, checkout string) string
}

var scopeCases = []scopeCase{
	{"in a checkout", false, true, func(_ *testing.T, _, checkout string) string { return checkout }},
	{"outside a checkout", false, false, func(t *testing.T, _, _ string) string { return outsideAnyCheckout(t) }},
	{"in the home directory", false, false, func(_ *testing.T, home, _ string) string { return home }},
	{"--global in a checkout", true, false, func(_ *testing.T, _, checkout string) string { return checkout }},
}

// outsideAnyCheckout is a scratch directory that no marker above it claims,
// or a skip where there is none to be had. The walk to a project root has no
// ceiling, so with TMPDIR (or GOTMPDIR) inside a checkout every scratch
// directory is in that checkout: a marker of the test's own would make it a
// checkout too, and standing in it unmarked would write the scaffold into
// whatever repository the run was started from.
func outsideAnyCheckout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if root, found := project.RootFound(dir); found {
		t.Skipf("the temporary directory is inside the checkout at %s, so no directory here is outside one", root)
	}
	return dir
}

// scopeFixture is a home directory holding the user's config under
// XDG_CONFIG_HOME and a .git of its own, and a checkout beside it, with the
// commands stood in where the case says.
func scopeFixture(t *testing.T, c scopeCase) (userPath, checkout string) {
	t.Helper()
	userPath = pointConfigAt(t, "")
	home := filepath.Dir(filepath.Dir(userPath))
	must(t, os.MkdirAll(filepath.Join(home, ".git"), 0o755))
	checkout = t.TempDir()
	must(t, os.MkdirAll(filepath.Join(checkout, ".git"), 0o755))
	trusting(t, checkout, true)
	standIn(t, c.where(t, home, checkout))
	return userPath, checkout
}

// exists reports whether a path is there.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestConfigInit_WritesThePairOfWhereItIsRun(t *testing.T) {
	for _, c := range scopeCases {
		t.Run(c.name, func(t *testing.T) {
			userPath, checkout := scopeFixture(t, c)
			args := []string{"config", "init"}
			if c.global {
				args = append(args, "--global")
			}
			out := runRoot(t, args...)

			projectPath := filepath.Join(checkout, filepath.FromSlash(project.ConfigFile))
			wantHere, wantAbsent := userPath, projectPath
			pair := "[user]"
			if c.toProject {
				wantHere, wantAbsent, pair = projectPath, userPath, "[project]"
			}
			if !exists(wantHere) {
				t.Fatalf("the settings were not written to %s:\n%s", wantHere, out)
			}
			if exists(wantAbsent) {
				t.Fatalf("the other pair's file was written: %s", wantAbsent)
			}
			// The confirmation says which pair, since the bare command
			// decided it from where it was run.
			if !strings.Contains(out, pair) {
				t.Errorf("the confirmation does not say it wrote the %s pair:\n%s", pair, out)
			}
		})
	}
}

func TestConfigSet_WritesTheFileOfWhereItIsRun(t *testing.T) {
	for _, c := range scopeCases {
		t.Run(c.name, func(t *testing.T) {
			userPath, checkout := scopeFixture(t, c)
			args := []string{"config", "set", "behavior.default_mode", "plan"}
			if c.global {
				args = append(args, "--global")
			}
			runRoot(t, args...)

			projectPath := filepath.Join(checkout, filepath.FromSlash(project.ConfigFile))
			wantHere, wantAbsent := userPath, projectPath
			if c.toProject {
				wantHere, wantAbsent = projectPath, userPath
			}
			got, err := os.ReadFile(wantHere)
			if err != nil {
				t.Fatalf("the write did not land in %s: %v", wantHere, err)
			}
			if !strings.Contains(string(got), `default_mode = "plan"`) {
				t.Fatalf("%s does not hold the value:\n%s", wantHere, got)
			}
			if exists(wantAbsent) {
				t.Fatalf("the other file was written: %s", wantAbsent)
			}
		})
	}
}

// In a checkout a key the checkout may not decide is refused with the
// sentence that says why, and the way to the person's own file is the flag.
func TestConfigSet_InACheckoutRefusesAKeyItMayNotDecideAndNamesGlobal(t *testing.T) {
	userPath, checkout := scopeFixture(t, scopeCases[0])
	err := runRootErr(t, "config", "set", "provider.api_key", "sk-test")
	if !strings.Contains(err.Error(), config.RefusedInProject("provider.api_key")) ||
		!strings.Contains(err.Error(), "--global") {
		t.Fatalf("the refusal does not say why and name --global: %v", err)
	}
	if exists(filepath.Join(checkout, filepath.FromSlash(project.ConfigFile))) || exists(userPath) {
		t.Fatal("a refused key reached a file")
	}
}

// `--stdout` prints the pair the bare command would write, with that file's
// values filled in, and a refusal offers the command that prints the pair
// that is in the way.
func TestConfigInit_StdoutAndTheRefusalFollowTheScope(t *testing.T) {
	_, checkout := scopeFixture(t, scopeCases[0])
	must(t, os.MkdirAll(filepath.Join(checkout, ".shhh"), 0o755))
	must(t, os.WriteFile(filepath.Join(checkout, filepath.FromSlash(project.ConfigFile)),
		[]byte("[behavior]\ndefault_mode = \"plan\"\n"), 0o644))

	out := runRoot(t, "config", "init", "--stdout")
	if !strings.Contains(out, `default_mode = "plan"`) || strings.Contains(out, "[sandbox]") {
		t.Fatalf("--stdout in a checkout is not the checkout's scaffold with its values:\n%s", out)
	}
	if err := runRootErr(t, "config", "init").Error(); strings.Contains(err, "--global") ||
		!strings.Contains(err, "`shhh config init --stdout`") {
		t.Fatalf("the checkout's refusal does not offer the bare command: %s", err)
	}
}

func TestConfigInit_RefusalForTheUsersFileNamesGlobal(t *testing.T) {
	pointConfigAt(t, "[provider]\nmodel = \"claude-sonnet-5\"\n")
	if err := runRootErr(t, "config", "init").Error(); !strings.Contains(err, "`shhh config init --global --stdout`") {
		t.Fatalf("the refusal does not offer the command that prints the user's pair: %s", err)
	}
}

// The screen, `shhh config` and `/config` alike, writes where `config set`
// would, and a staged edit to a key the checkout set is not called a
// collision when the write goes to that same file.
func TestConfigScreen_WritesTheFileOfWhereItIsRun(t *testing.T) {
	for _, c := range scopeCases {
		t.Run(c.name, func(t *testing.T) {
			userPath, checkout := scopeFixture(t, c)
			proj := config.Project{Path: filepath.Join(checkout, ".shhh", "config.toml"),
				Display: ".shhh/config.toml", Keys: []string{"behavior.default_mode"}}
			m := newConfigModel(config.Config{}, proj)
			m.standIn(c.global, workingDir())
			m.apply(components.ConfigChange{Key: "behavior.default_mode", Value: "plan"})

			source := rowFor(m.screen.Rows, "behavior.default_mode").Source
			if want := map[bool]string{true: "unwritten", false: "unwritten · project"}[c.toProject]; source != want {
				t.Errorf("the staged row says %q, want %q", source, want)
			}
			m.answer(true, components.ConfigResult{Write: true})
			if m.err != nil {
				t.Fatal(m.err)
			}
			wantHere := userPath
			if c.toProject {
				wantHere = proj.Path
			}
			if got, err := os.ReadFile(wantHere); err != nil || !strings.Contains(string(got), `default_mode = "plan"`) {
				t.Fatalf("the screen's write did not land in %s: %v %s", wantHere, err, got)
			}
		})
	}
}

// In a checkout the screen refuses to stage a key the checkout may not
// decide, rather than holding an edit its write would stop on.
func TestConfigScreen_InACheckoutRefusesAKeyItMayNotDecide(t *testing.T) {
	scopeFixture(t, scopeCases[0])
	session, err := configSessionOpener(nil)(nil)
	must(t, err)
	if session.Screen.Path != project.ConfigFile {
		t.Fatalf("/config in a checkout names %q as the file it writes", session.Screen.Path)
	}
	session.Answer(false, components.ConfigResult{
		Change: &components.ConfigChange{Key: "provider.api_key", Value: "sk-test"},
	})
	notice := session.Screen.Notice
	if !strings.Contains(notice, keys.Bracket(keys.Screen.Scope)) || strings.Contains(notice, "--global") {
		t.Fatalf("the refusal should name the screen's scope key and no flag the screen cannot pass: %q", notice)
	}
}

// The screen's scope key moves the write between the checkout's file and the
// person's own, so a key the checkout may not decide can be written from
// inside a session; `--global` only chooses which file it opens on, and the
// write cannot be moved back to the checkout over an edit it would refuse.
func TestConfigScreen_TheScopeKeyMovesTheWrite(t *testing.T) {
	userPath, checkout := scopeFixture(t, scopeCases[0])
	session, err := configSessionOpener(nil)(nil)
	must(t, err)
	screen := session.Screen
	if !screen.Scoped || screen.Yours {
		t.Fatalf("in a checkout the screen should open on the checkout's file with the switch offered: scoped %v yours %v",
			screen.Scoped, screen.Yours)
	}
	session.Answer(false, components.ConfigResult{Scope: true})
	if !screen.Yours || screen.Path != shortPath(userPath) {
		t.Fatalf("the scope key left the write at %q (yours %v), want %q", screen.Path, screen.Yours, shortPath(userPath))
	}
	session.Answer(false, components.ConfigResult{
		Change: &components.ConfigChange{Key: "provider.api_key", Value: "sk-test"},
	})
	if screen.Notice != "" || screen.Changed != 1 {
		t.Fatalf("writing to the person's file, the key should stage: notice %q, changed %d", screen.Notice, screen.Changed)
	}
	session.Answer(false, components.ConfigResult{Scope: true})
	if !screen.Yours || !strings.Contains(screen.Notice, "provider.api_key") {
		t.Fatalf("the write moved back to the checkout over a key it may not decide: yours %v, notice %q",
			screen.Yours, screen.Notice)
	}
	if note := session.Answer(false, components.ConfigResult{Write: true}); !strings.Contains(note, "wrote 1 change to") {
		t.Fatalf("the write did not land: %s", note)
	}
	if got, err := os.ReadFile(userPath); err != nil || !strings.Contains(string(got), "sk-test") {
		t.Fatalf("the key did not reach %s: %v %s", userPath, err, got)
	}
	if exists(filepath.Join(checkout, ".shhh", "config.toml")) {
		t.Fatal("the checkout's file was written")
	}

	m := newConfigModel(config.Config{}, config.Project{})
	m.standIn(true, checkout)
	if !m.screen.Scoped || !m.screen.Yours || m.toProject {
		t.Fatalf("--global in a checkout should open on the person's file with the switch still offered")
	}
}

// Outside a checkout there is one file, and nothing to switch to.
func TestConfigScreen_OutsideACheckoutOffersNoScope(t *testing.T) {
	scopeFixture(t, scopeCases[1])
	m := newConfigModel(config.Config{}, config.Project{})
	m.standIn(false, workingDir())
	m.answer(false, components.ConfigResult{Scope: true})
	if m.screen.Scoped || m.toProject {
		t.Fatalf("a screen outside a checkout offered a second file")
	}
}

// A run whose temporary directory sits inside a checkout — somebody's
// worktree, when the tmpfs is full — writes every case's files into that
// case's own scratch and nothing into the checkout around it.
func TestConfigWrites_ATemporaryDirectoryInsideACheckoutLeavesTheCheckoutAlone(t *testing.T) {
	outer := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(outer, ".git"), 0o755))
	tmp := filepath.Join(outer, "tmp")
	must(t, os.MkdirAll(tmp, 0o755))
	t.Setenv("TMPDIR", tmp)
	t.Setenv("GOTMPDIR", "")

	for _, args := range [][]string{{"config", "init"}, {"config", "set", "behavior.default_mode", "plan"}} {
		for _, c := range scopeCases {
			t.Run(strings.Join(args[:2], " ")+"/"+c.name, func(t *testing.T) {
				if _, checkout := scopeFixture(t, c); !strings.HasPrefix(checkout, tmp) {
					t.Fatalf("the scratch is not under the TMPDIR the test set: %s", checkout)
				}
				run := args
				if c.global {
					run = append(args[:len(args):len(args)], "--global")
				}
				runRoot(t, run...)
			})
		}
	}

	entries, err := os.ReadDir(outer)
	must(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, " ") != ".git tmp" {
		t.Fatalf("the checkout around the temporary directory was written: it holds %v", names)
	}
}
