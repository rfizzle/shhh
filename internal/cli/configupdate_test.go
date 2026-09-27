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

// dropKey takes one key's block out of a scaffold — the blank line, the
// sentence above the key and the commented row — which is the file as a
// scaffold wrote it before that key arrived.
func dropKey(t *testing.T, text, row string) string {
	t.Helper()
	at := strings.Index(text, "\n"+row)
	if at < 0 {
		t.Fatalf("the scaffold has no row %q", row)
	}
	end := at + 1 + strings.Index(text[at+1:], "\n")
	start := strings.LastIndex(text[:at], "\n\n")
	return text[:start+1] + text[end+1:]
}

// An older file — two keys it predates, a value set with a note beside it,
// a key that has since moved, a wording with no file — is refused by every
// command but the update, and the update brings it level: every value kept,
// the moved one under its new name, every key present, the wording written.
func TestConfigInitUpdate_BringsAnOlderPairUpToDate(t *testing.T) {
	path := pointConfigAt(t, "")
	runRoot(t, "config", "init")
	older := config.Scaffold(config.Config{}, false)
	older = dropKey(t, older, "#stream_idle_seconds = ")
	older = dropKey(t, older, "#timeout_seconds = 30")
	older = strings.Replace(older, `#model = ""`, `model = "claude-sonnet-5" # the one the team pays for`, 1)
	older = strings.Replace(older, "[agents]\n", "[agents]\nwriter_model = \"tiny\"\n", 1)
	must(t, os.WriteFile(path, []byte(older), 0o600))
	steer := filepath.Join(filepath.Dir(path), "prompts", "steer.md")
	must(t, os.Remove(steer))

	err := runRootErr(t, "config", "list")
	if err == nil || !strings.Contains(err.Error(), "agents.profiles.writer.model") ||
		!strings.Contains(err.Error(), config.UpdateUser) {
		t.Fatalf("a moved key's refusal = %v", err)
	}

	out := runRoot(t, "config", "init", "--update")
	for _, want := range []string{"updated", "added 2 keys · 1 renamed", "wrote", "1 wording"} {
		if !strings.Contains(out, want) {
			t.Errorf("the confirmation does not say %q:\n%s", want, out)
		}
	}
	cfg, err := config.LoadFrom(path)
	must(t, err)
	if cfg.Provider.Model != "claude-sonnet-5" || cfg.Agents.Profiles["writer"].Model != "tiny" {
		t.Fatalf("a value was lost: model=%q writer=%q", cfg.Provider.Model, cfg.Agents.Profiles["writer"].Model)
	}
	raw, err := os.ReadFile(path)
	must(t, err)
	if !strings.Contains(string(raw), `model = "claude-sonnet-5" # the one the team pays for`) {
		t.Error("the note beside a set key was lost")
	}
	if b, err := config.Outdated(path, false); err != nil || len(b.New) > 0 || len(b.Renamed) > 0 {
		t.Errorf("still behind: %+v %v", b, err)
	}
	if !exists(steer) {
		t.Error("the missing wording was not written")
	}
}

// A pair that is current is left exactly as it is, and says so.
func TestConfigInitUpdate_LeavesACurrentFileByteForByte(t *testing.T) {
	path := pointConfigAt(t, "")
	runRoot(t, "config", "init")
	runRoot(t, "config", "set", "--global", "provider.model", "claude-opus-5")
	before, err := os.ReadFile(path)
	must(t, err)
	out := runRoot(t, "config", "init", "--update")
	after, err := os.ReadFile(path)
	must(t, err)
	if string(before) != string(after) {
		t.Fatal("a current file was rewritten")
	}
	if !strings.Contains(out, "already up to date") {
		t.Errorf("the confirmation does not say the file was current:\n%s", out)
	}
}

// A keymap written before a key arrived gets the key back as a commented
// row, and the key the person bound stays as they wrote it; a current keymap
// is not written.
func TestConfigInitUpdate_BringsTheKeymapUpToDate(t *testing.T) {
	path := pointConfigAt(t, "")
	runRoot(t, "config", "init")
	keymap := filepath.Join(filepath.Dir(path), "keybindings.toml")
	lines := strings.SplitAfter(keys.Scaffold(), "\n")
	var arrived, bound string
	var older strings.Builder
	inTable := false
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "["):
			inTable = true
		case inTable && strings.HasPrefix(line, "# ") && arrived == "":
			arrived = line
			continue
		case inTable && strings.HasPrefix(line, "# ") && bound == "":
			bound = strings.TrimPrefix(line, "# ")
			line = bound
		}
		older.WriteString(line)
	}
	must(t, os.WriteFile(keymap, []byte(older.String()), 0o600))
	if b, err := keys.KeymapOutdated(keymap); err != nil || b.KeysBehind() != 1 {
		t.Fatalf("the older keymap reads as %+v, %v", b, err)
	}

	out := runRoot(t, "config", "init", "--update")
	if !strings.Contains(out, "added 1 key") {
		t.Errorf("the confirmation does not name the key added:\n%s", out)
	}
	raw, err := os.ReadFile(keymap)
	must(t, err)
	if !strings.Contains(string(raw), "\n"+arrived) || !strings.Contains(string(raw), "\n"+bound) {
		t.Fatalf("the key that arrived is not back, or the bound one moved:\n%s", raw)
	}
	if b, err := keys.KeymapOutdated(keymap); err != nil || len(b.Missing) != 0 {
		t.Errorf("still behind: %+v %v", b, err)
	}
	if _, err := keys.Check(keymap); err != nil {
		t.Errorf("the updated keymap is refused: %v", err)
	}

	runRoot(t, "config", "init", "--update")
	again, err := os.ReadFile(keymap)
	must(t, err)
	if string(again) != string(raw) {
		t.Fatal("a current keymap was rewritten")
	}
}

// --stdout with --update prints what would be written and writes nothing.
func TestConfigInitUpdate_StdoutPrintsWithoutWriting(t *testing.T) {
	path := pointConfigAt(t, "[agents]\nreviewer_model = \"x\"\n")
	out := runRoot(t, "config", "init", "--update", "--stdout")
	if !strings.Contains(out, "# agents.reviewer_model moved here in") || !strings.Contains(out, "[provider]") {
		t.Fatalf("the printed file is not the update:\n%s", out)
	}
	raw, err := os.ReadFile(path)
	must(t, err)
	if string(raw) != "[agents]\nreviewer_model = \"x\"\n" {
		t.Fatal("--stdout wrote the file")
	}
}

// Without --update a file that is there is refused as it always was.
func TestConfigInitUpdate_WithoutTheFlagAFileIsStillRefused(t *testing.T) {
	pointConfigAt(t, "[provider]\nmodel = \"x\"\n")
	if err := runRootErr(t, "config", "init"); err == nil || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("err = %v", err)
	}
}

// Bare in a checkout, the update is the checkout's pair's, and the keys a
// checkout may not decide stay out of it.
func TestConfigInitUpdate_TheCheckoutsPairLeavesTheRefusedKeysOut(t *testing.T) {
	userPath, checkout := scopeFixture(t, scopeCases[0])
	projPath := config.ProjectPath(checkout)
	must(t, os.MkdirAll(filepath.Dir(projPath), 0o755))
	must(t, os.WriteFile(projPath, []byte("[behavior]\n#silent_mode = false\n"), 0o644))

	runRoot(t, "config", "init", "--update")
	if exists(userPath) {
		t.Fatal("the update in a checkout wrote the person's own file")
	}
	raw, err := os.ReadFile(projPath)
	must(t, err)
	if _, _, err := config.LayerProject(config.Config{}, projPath); err != nil {
		t.Fatalf("the checkout's updated file does not load: %v", err)
	}
	if strings.Contains(string(raw), "api_key") {
		t.Errorf("the checkout's file lists a credential:\n%s", raw)
	}
	if !exists(filepath.Join(checkout, filepath.FromSlash(project.PromptsDir), "steer.md")) {
		t.Error("the checkout's wordings were not written")
	}
}

// A checkout's file holding a moved key stops every command with the
// checkout's own spelling of the update, and that update is let past the
// refusal to move it.
func TestConfigInitUpdate_TheCheckoutsMovedKeyIsMovedFromInsideTheCheckout(t *testing.T) {
	_, checkout := scopeFixture(t, scopeCases[0])
	projPath := config.ProjectPath(checkout)
	must(t, os.MkdirAll(filepath.Dir(projPath), 0o755))
	must(t, os.WriteFile(projPath, []byte("[agents]\nwriter_model = \"tiny\"\n"), 0o644))

	err := runRootErr(t, "config", "list")
	if err == nil || !strings.Contains(err.Error(), "`"+config.UpdateProject+"`") {
		t.Fatalf("the checkout's refusal = %v", err)
	}
	runRoot(t, "config", "init", "--update")
	cfg, _, err := config.LayerProject(config.Config{}, projPath)
	must(t, err)
	if cfg.Agents.Profiles["writer"].Model != "tiny" {
		t.Fatalf("the moved value = %q", cfg.Agents.Profiles["writer"].Model)
	}
}

// The doctor's row is where a file behind the table is noticed: the three
// counts, the warning, and the offer to update — and a current file reads as
// it always did.
func TestDoctorConfig_SaysWhenTheFileIsBehind(t *testing.T) {
	ok := doctorConfig("~/.config/shhh/config.toml", nil, config.Config{}, config.Project{}, nil)
	plan := initPlan{settings: filepath.Join(t.TempDir(), "config.toml")}

	if f := withBehind(ok, configBehind{Behind: config.Behind{Listed: true}}, plan); f.State != ok.State || f.Detail != ok.Detail || f.Action != "" {
		t.Fatalf("a current file changed the row: %+v", f)
	}
	b := configBehind{
		Behind: config.Behind{
			New:     []string{"a.b", "a.c", "a.d", "a.e"},
			Renamed: config.Renames()[:1],
			Listed:  true,
		},
		wordings: []string{"steer", "summary"}, wordingsListed: true,
	}
	f := withBehind(ok, b, plan)
	if f.State != components.DoctorWarned || f.Outcome != "behind" {
		t.Fatalf("state=%v outcome=%q", f.State, f.Outcome)
	}
	if !strings.HasPrefix(f.Detail, "behind by 4 keys · 1 renamed · 2 wordings") {
		t.Errorf("detail = %q", f.Detail)
	}
	if f.Action == "" || f.Apply == nil {
		t.Error("the row does not offer the update")
	}
	// A hand-written file and a directory nobody filled count no absent key
	// and no absent wording against themselves.
	b.Listed, b.wordingsListed = false, false
	if got := withBehind(ok, b, plan).Detail; !strings.HasPrefix(got, "behind by 1 renamed ·") {
		t.Errorf("unlisted detail = %q", got)
	}
}

// The doctor's [a] runs the update and the row reads clean after it.
func TestDoctorConfig_TheOfferUpdatesTheFile(t *testing.T) {
	path := pointConfigAt(t, "")
	runRoot(t, "config", "init")
	must(t, os.WriteFile(path, []byte(dropKey(t, config.Scaffold(config.Config{}, false), "#timeout_seconds = 30")), 0o600))

	f := probeConfig(t.Context(), config.Config{})
	if f.State != components.DoctorWarned || f.Apply == nil {
		t.Fatalf("the row is %v with no offer: %+v", f.State, f)
	}
	lines, err := f.Apply()
	must(t, err)
	if len(lines) != 1 || !strings.Contains(lines[0], "added 1 key") {
		t.Errorf("the offer said %q", lines)
	}
	if again := probeConfig(t.Context(), config.Config{}); again.State == components.DoctorWarned {
		t.Errorf("still behind after the update: %+v", again)
	}
}
