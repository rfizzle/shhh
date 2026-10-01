package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// The keymap goes with your own pair and never with a checkout's: the file
// is the user's alone. Where it is written it is the shipped keyboard with
// every line commented, which a session start reads as moving nothing.
func TestConfigInit_WritesTheKeymapWithYourOwnPairOnly(t *testing.T) {
	for _, c := range scopeCases {
		t.Run(c.name, func(t *testing.T) {
			userPath, checkout := scopeFixture(t, c)
			args := []string{"config", "init"}
			if c.global {
				args = append(args, "--global")
			}
			out := runRoot(t, args...)

			keymap := filepath.Join(filepath.Dir(userPath), "keybindings.toml")
			if c.toProject {
				if exists(keymap) || exists(filepath.Join(checkout, ".shhh", "keybindings.toml")) {
					t.Fatal("a checkout's pair wrote a keymap")
				}
				if !strings.Contains(out, "no keybindings.toml") {
					t.Errorf("the checkout's confirmation does not say it wrote no keymap:\n%s", out)
				}
				return
			}
			text, err := os.ReadFile(keymap)
			if err != nil {
				t.Fatalf("the keymap was not written beside the settings: %v\n%s", err, out)
			}
			if string(text) != keys.Scaffold() {
				t.Fatal("the keymap written is not the scaffold")
			}
			if path, err := keys.Check(keymap); err != nil || path != keymap {
				t.Fatalf("a session would not run the scaffold: %q, %v", path, err)
			}
			if !strings.Contains(out, "keybindings.toml") {
				t.Errorf("the confirmation does not name the keymap:\n%s", out)
			}
		})
	}
}

// A keymap already there stops the command like a settings file does, and is
// named in the refusal.
func TestConfigInit_RefusesAKeymapThatIsAlreadyThere(t *testing.T) {
	path := pointConfigAt(t, "")
	keymap := filepath.Join(filepath.Dir(path), "keybindings.toml")
	must(t, os.WriteFile(keymap, []byte("[reading]\ncopy = \"c\"\n"), 0o600))

	got := runRootErr(t, "config", "init").Error()
	if !strings.Contains(got, shortPath(keymap)) {
		t.Fatalf("the refusal does not name the keymap: %s", got)
	}
	if exists(path) {
		t.Fatal("the settings were written past a keymap in the way")
	}
	text, err := os.ReadFile(keymap)
	must(t, err)
	if string(text) != "[reading]\ncopy = \"c\"\n" {
		t.Fatalf("the keymap was rewritten:\n%s", text)
	}
}

// `shhh keys defaults` is the same file for the person who already has one.
func TestKeysDefaults_PrintsTheScaffold(t *testing.T) {
	pointConfigAt(t, "")
	if got := runRoot(t, "keys", "defaults"); got != keys.Scaffold() {
		t.Fatal("keys defaults does not print the scaffold")
	}
}

// The check says ok, or the refusal and a failing status, and a file named
// and missing is not "no keymap".
func TestKeysCheck(t *testing.T) {
	pointConfigAt(t, "")
	dir := t.TempDir()
	good := filepath.Join(dir, "good.toml")
	must(t, os.WriteFile(good, []byte("[reading]\ncopy = \"x\"\n"), 0o600))
	bad := filepath.Join(dir, "bad.toml")
	must(t, os.WriteFile(bad, []byte("[draft]\npalette = \"p\"\n"), 0o600))

	if out := runRoot(t, "keys", "check", good); !strings.Contains(out, "[ok]") {
		t.Errorf("a valid file is not ok:\n%s", out)
	}
	if err := runRootErr(t, "keys", "check", bad); !strings.Contains(err.Error(), "a letter while the draft can take text") {
		t.Errorf("the refusal is not the file's: %v", err)
	}
	if err := runRootErr(t, "keys", "check", filepath.Join(dir, "none.toml")); !strings.Contains(err.Error(), "no file at") {
		t.Errorf("a missing file named on the line: %v", err)
	}
	// With nothing named and nothing written, there is no keymap and that
	// is not a refusal.
	if out := runRoot(t, "keys", "check"); !strings.Contains(out, "no keybindings.toml") {
		t.Errorf("no file is not said:\n%s", out)
	}

	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"keys", "check", bad, "--json"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("a refused file exits zero with --json")
	}
	// The first value on stdout: cobra, undressed here, prints its usage
	// after a failing command, which fang does not.
	var doc keysCheckJSON
	must(t, json.NewDecoder(&out).Decode(&doc))
	if doc.OK || doc.File != bad || !strings.HasPrefix(doc.Refusal, `"p" is a letter`) {
		t.Errorf("the JSON answer is %+v", doc)
	}
}

// `shhh keys --json` is every key, grouped as a file names them.
func TestKeys_JSONListsEveryGroup(t *testing.T) {
	pointConfigAt(t, "")
	var doc keysJSON
	must(t, json.Unmarshal([]byte(runRoot(t, "keys", "--json")), &doc))
	if len(doc.Groups) != len(keys.Keyboard()) || doc.Groups[0].Name != "draft" || len(doc.Groups[0].Keys) == 0 {
		t.Fatalf("the JSON listing is %+v", doc.Groups[:1])
	}
}

// The doctor's row names a refused file and quotes the refusal, which is the
// one place a keyboard that went back to the shipped one is explained after
// the line at process start.
func TestDoctorKeymap(t *testing.T) {
	path := "/home/dev/.config/shhh/keybindings.toml"
	f := doctorKeymap(path, 0, 0, nil, errors.New(path+`: "p" is a letter while the draft can take text`))
	if f.State != components.DoctorWarned || f.Outcome != "refused" || len(f.Fix) == 0 ||
		!strings.HasPrefix(f.Fix[0], `"p" is a letter`) {
		t.Fatalf("the refused row is %+v", f)
	}
	if f := doctorKeymap("", 0, 0, nil, nil); f.State != components.DoctorPassed || !strings.Contains(f.Subject, "no keybindings.toml") {
		t.Fatalf("the row for no file is %+v", f)
	}
	if f := doctorKeymap(path, 2, 0, nil, nil); f.State != components.DoctorPassed || f.Detail != "2 keys moved" {
		t.Fatalf("the applied row is %+v", f)
	}
	// A file behind the register is counted the way the config row counts
	// settings, and its fix is the update.
	if f := doctorKeymap(path, 2, 3, nil, nil); f.State != components.DoctorWarned || f.Outcome != "behind" ||
		f.Detail != "behind by 3 keys · 2 keys moved" || len(f.Fix) == 0 ||
		!strings.HasPrefix(f.Fix[0], config.UpdateUser) {
		t.Fatalf("the behind row is %+v", f)
	}
	// A file naming keys shhh has given up was read without them, and the
	// row names the lines that do nothing now.
	if f := doctorKeymap(path, 1, 0, []string{"row.commit"}, nil); f.State != components.DoctorWarned ||
		f.Outcome != "stale" || f.Detail != "1 line doing nothing · 1 key moved" || len(f.Fix) != 1 ||
		!strings.HasPrefix(f.Fix[0], "row.commit name") {
		t.Fatalf("the stale row is %+v", f)
	}
	// Behind as well, the update stays the row's fix and the lines join it.
	if f := doctorKeymap(path, 0, 3, []string{"row.commit"}, nil); f.Outcome != "behind" || len(f.Fix) != 2 {
		t.Fatalf("a behind row naming dead lines is %+v", f)
	}
}

// A keymap behind on its own is answered on its own row. The config row
// offers its update only where the settings or wordings are behind, so with
// those current the keymap row's [a] is the offer, and it writes the keymap
// and nothing beside it.
func TestDoctorKeymap_BehindOnItsOwnIsOfferedOnItsRow(t *testing.T) {
	dir := t.TempDir()
	plan := initPlan{
		settings: filepath.Join(dir, "config.toml"), prompts: filepath.Join(dir, "prompts"),
		keymap: filepath.Join(dir, "keybindings.toml"), keymapHeld: true,
	}
	must(t, os.WriteFile(plan.settings, []byte("# my settings\n"), 0o600))
	// The scaffold less its first commented row: a list of every key, one
	// short.
	rows := strings.Split(keys.Scaffold(), "\n")
	for i, row := range rows {
		if strings.HasPrefix(row, "# ") && strings.Contains(row, " = ") {
			rows = append(rows[:i], rows[i+1:]...)
			break
		}
	}
	must(t, os.WriteFile(plan.keymap, []byte(strings.Join(rows, "\n")), 0o600))

	b, err := behindOf(plan)
	must(t, err)
	ok := doctorConfig(plan.settings, nil, config.Config{}, config.Project{}, nil)
	if f := withBehind(ok, b, plan); f.Action != "" {
		t.Fatalf("current settings, yet the config row offers %q", f.Action)
	}
	kb, err := keys.KeymapOutdated(plan.keymap)
	must(t, err)
	if kb.KeysBehind() != 1 {
		t.Fatalf("the fixture keymap is behind by %d keys, want 1", kb.KeysBehind())
	}
	f := doctorKeymap(plan.keymap, 0, kb.KeysBehind(), nil, nil)
	if f.Action == "" || f.Apply == nil || !strings.Contains(f.ActionPrompt, shortPath(plan.keymap)) {
		t.Fatalf("the behind keymap row offers nothing: %+v", f)
	}
	lines, err := f.Apply()
	must(t, err)
	if len(lines) != 1 || !strings.Contains(lines[0], "added 1 key") {
		t.Errorf("the update says %q", lines)
	}
	if kb, err := keys.KeymapOutdated(plan.keymap); err != nil || kb.KeysBehind() != 0 {
		t.Errorf("the keymap is still behind after the offer: %+v, %v", kb, err)
	}
	if raw, _ := os.ReadFile(plan.settings); string(raw) != "# my settings\n" {
		t.Errorf("the keymap's offer wrote the settings: %q", raw)
	}
	// A current keymap offers nothing.
	if f := doctorKeymap(plan.keymap, 0, 0, nil, nil); f.Action != "" {
		t.Errorf("a current keymap offers %q", f.Action)
	}
}
