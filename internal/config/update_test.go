package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// olderScaffold is the file an older table wrote: the scaffold as it read
// before the keys drop keeps out had arrived.
func olderScaffold(t *testing.T, project bool, drop func(Setting) bool) string {
	t.Helper()
	was := settings
	t.Cleanup(func() { settings = was })
	var older []Setting
	for _, s := range was {
		if !drop(s) {
			older = append(older, s)
		}
	}
	settings = older
	text := Scaffold(Config{}, project)
	settings = was
	return text
}

// A file the scaffold wrote today is behind by nothing, and an update leaves
// it as it is — which is also the check that every row the scaffold writes,
// the per-role shapes included, is read back as the key it lists.
func TestOutdated_TodaysScaffoldIsCurrent(t *testing.T) {
	for _, project := range []bool{false, true} {
		path := writeTemp(t, Scaffold(Config{}, project))
		b, err := Outdated(path, project)
		if err != nil {
			t.Fatal(err)
		}
		if b.Due() || len(b.New) > 0 || !b.Listed {
			t.Fatalf("project=%v: a fresh scaffold reads as behind: %+v", project, b)
		}
		if _, changed, err := Updated(path, project); err != nil || changed {
			t.Fatalf("project=%v: a fresh scaffold was updated: changed=%v err=%v", project, changed, err)
		}
	}
}

// Whatever keys arrived since a scaffold was written, the update puts each
// back where today's scaffold has it, so the updated file is today's
// scaffold to the byte. Keys are dropped from the middle of a table, from
// its start, and a whole table at once.
func TestUpdated_AnOlderScaffoldBecomesTodays(t *testing.T) {
	for name, drop := range map[string]func(Setting) bool{
		"scattered keys":      func(s Setting) bool { return len(s.Key)%5 == 0 },
		"a table's first key": func(s Setting) bool { return s.Key == settings[0].Key },
		"a whole table":       func(s Setting) bool { return s.Group() == "web" },
		"the first table":     func(s Setting) bool { return s.Group() == settings[0].Group() },
		"the last table":      func(s Setting) bool { return s.Group() == settings[len(settings)-1].Group() },
	} {
		t.Run(name, func(t *testing.T) {
			for _, project := range []bool{false, true} {
				path := writeTemp(t, olderScaffold(t, project, drop))
				b, err := Outdated(path, project)
				if err != nil {
					t.Fatal(err)
				}
				if b.KeysBehind() == 0 {
					t.Fatalf("project=%v: an older scaffold reads as current", project)
				}
				got, changed, err := Updated(path, project)
				if err != nil || !changed {
					t.Fatalf("project=%v: changed=%v err=%v", project, changed, err)
				}
				if want := Scaffold(Config{}, project); got != want {
					t.Fatalf("project=%v: the updated file is not today's scaffold:\n%s", project, firstDiff(got, want))
				}
			}
		})
	}
}

// The case the command is for: a file from an older scaffold, fewer keys,
// values set, a comment beside one of them, and a key that has moved since.
// It comes back holding every value it held, the moved one under its new
// name, every other key present, and the person's comments where they were.
func TestUpdated_KeepsEveryValueAndMovesARenamedKey(t *testing.T) {
	older := olderScaffold(t, false, func(s Setting) bool { return s.Key == "provider.stream_idle_seconds" || s.Group() == "hooks" })
	older = strings.Replace(older, `#model = ""`, `model = "claude-sonnet-5" # the one the team pays for`, 1)
	older = strings.Replace(older, "[agents]\n", "[agents]\nresearcher_model = \"tiny\" # cheap reads\n", 1)
	older += "\n# my servers\n[mcp.servers.docs]\ncommand = \"docs-mcp\"\n"
	path := writeTemp(t, older)

	if _, err := LoadFrom(path); err == nil {
		t.Fatal("a file holding a renamed key loaded")
	}
	b, err := Outdated(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Renamed) != 1 || b.Renamed[0].To != "agents.profiles.researcher.model" {
		t.Fatalf("renamed = %+v", b.Renamed)
	}
	if b.KeysBehind() != 3 {
		t.Fatalf("keys behind = %d (%v), want the stream idle key and the two hooks keys", b.KeysBehind(), b.New)
	}

	if _, err := UpdateFile(path, false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("the updated file does not load: %v\n%s", err, text)
	}
	if cfg.Provider.Model != "claude-sonnet-5" {
		t.Errorf("provider.model = %q", cfg.Provider.Model)
	}
	if got := cfg.Agents.Profiles["researcher"].Model; got != "tiny" {
		t.Errorf("the renamed key's value = %q, want tiny", got)
	}
	if cfg.MCP.Servers["docs"].Command != "docs-mcp" {
		t.Errorf("the MCP server was lost:\n%s", text)
	}
	for _, want := range []string{
		`model = "claude-sonnet-5" # the one the team pays for`,
		"# agents.researcher_model moved here in v0.9.5\nmodel = \"tiny\" # cheap reads\n",
		"# my servers\n[mcp.servers.docs]",
		"#stream_idle_seconds = ",
		"#timeout_seconds = 30",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the updated file does not hold %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "researcher_model =") {
		t.Errorf("the old spelling is still set:\n%s", text)
	}
	after, err := Outdated(path, false)
	if err != nil || after.Due() || len(after.New) > 0 {
		t.Fatalf("the updated file is still behind: %+v %v", after, err)
	}
}

// A file written by hand, a line or two with no commented rows, is behind by
// nothing — it was never a list of every key — but asked to update, it gets
// every key, and its own lines stay first where it wrote them.
func TestUpdated_AHandWrittenFileIsNotBehindButCanBeFilledIn(t *testing.T) {
	hand := "# mine\n[provider]\nmodel = \"x\"\n"
	path := writeTemp(t, hand)
	b, err := Outdated(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if b.Due() || b.Listed || len(b.New) != len(settings)-1 {
		t.Fatalf("a hand-written file: due=%v listed=%v new=%d", b.Due(), b.Listed, len(b.New))
	}
	got, changed, err := Updated(path, false)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if !strings.HasPrefix(got, "# mine\n[provider]\n") || !strings.Contains(got, "\nmodel = \"x\"\n") {
		t.Fatalf("the file's own lines moved:\n%s", got)
	}
	path2 := writeTemp(t, got)
	if b, _ := Outdated(path2, false); len(b.New) > 0 {
		t.Fatalf("still missing %v", b.New)
	}
}

// A checkout's file is brought up to date with the keys a checkout may
// decide and none of the ones it may not.
func TestUpdated_TheCheckoutsFileLeavesTheRefusedKeysOut(t *testing.T) {
	path := writeTemp(t, "[behavior]\n#silent_mode = false\n")
	got, _, err := Updated(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := LayerProject(Config{}, writeTemp(t, got)); err != nil {
		t.Fatalf("the updated checkout file does not load as one: %v", err)
	}
	doc, err := parseDocument(got)
	if err != nil {
		t.Fatal(err)
	}
	listed := 0
	for _, r := range doc.rows() {
		if r.setting < 0 {
			continue
		}
		listed++
		if key := settings[r.setting].Key; RefusedInProject(key) != "" {
			t.Errorf("the checkout's file lists %s, which a checkout may not decide", key)
		}
	}
	if listed != ScaffoldKeys(true) {
		t.Errorf("the checkout's file lists %d keys, want %d", listed, ScaffoldKeys(true))
	}
}

// A key and its old spelling both set is a choice only the person can make;
// the update writes nothing rather than pick one.
func TestUpdated_RefusesAKeyAndItsOldSpellingBothSet(t *testing.T) {
	text := "[agents]\nresearcher_model = \"a\"\n\n[agents.profiles.researcher]\nmodel = \"b\"\n"
	path := writeTemp(t, text)
	if _, err := UpdateFile(path, false); err == nil || !strings.Contains(err.Error(), "both set") {
		t.Fatalf("err = %v", err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != text {
		t.Fatal("the file was written")
	}
}

// A typo is not a rename. The update refuses it the way the load does, so a
// value is never moved past a key nobody can say the meaning of.
func TestUpdated_RefusesAKeyThatIsNeitherASettingNorARename(t *testing.T) {
	path := writeTemp(t, "[behaviour]\nsilent_mode = true\n")
	_, err := UpdateFile(path, false)
	var unknown *UnknownKeyError
	if !errors.As(err, &unknown) {
		t.Fatalf("err = %v", err)
	}
}

// The load's refusal of a moved key says where it went and which command
// moves it — the command for this file's scope, since the bare spelling in a
// checkout would update the checkout's file instead.
func TestLoad_ARenamedKeyIsRefusedWithItsNewName(t *testing.T) {
	path := writeTemp(t, "[agents]\nwriter_model = \"tiny\"\n")
	_, err := LoadFrom(path)
	if err == nil {
		t.Fatal("a renamed key loaded")
	}
	for _, want := range []string{`"agents.writer_model" (renamed "agents.profiles.writer.model")`, UpdateUser} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not say %q", err, want)
		}
	}
	proj := filepath.Join(t.TempDir(), ".shhh", "config.toml")
	if err := os.MkdirAll(filepath.Dir(proj), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proj, []byte("[agents]\nwriter_model = \"tiny\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LayerProject(Config{}, proj); err == nil || !strings.Contains(err.Error(), "`"+UpdateProject+"`") {
		t.Errorf("the checkout's refusal = %v", err)
	}
}

// A file still setting the key that was removed for reading nothing is
// refused with the sentence saying so, not with a guess at a nearby key.
func TestLoad_ARetiredKeyIsRefusedWithWhatBecameOfIt(t *testing.T) {
	path := writeTemp(t, "[appearance]\naccent_color = \"magenta\"\n")
	_, err := LoadFrom(path)
	if err == nil {
		t.Fatal("a retired key loaded")
	}
	want := `"appearance.accent_color" (removed, it never changed a colour; delete the line)`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal %q does not say %q", err, want)
	}
}

// The written file keeps its mode, and a link to it is still a link after.
func TestUpdateFile_WritesThroughALinkAndKeepsTheMode(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.toml")
	if err := os.WriteFile(real, []byte("[agents]\nreviewer_model = \"x\"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.toml")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks here")
	}
	if wrote, err := UpdateFile(link, false); err != nil || !wrote {
		t.Fatalf("wrote=%v err=%v", wrote, err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced by a file")
	}
	fi, err := os.Stat(real)
	if err != nil || fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
	if _, err := LoadFrom(link); err != nil {
		t.Fatal(err)
	}
}

func firstDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) && i < len(w); i++ {
		if g[i] != w[i] {
			lo := max(i-3, 0)
			return "at line " + itoa(i+1) + "\n got: " + strings.Join(g[lo:min(i+4, len(g))], "\n      ") +
				"\nwant: " + strings.Join(w[lo:min(i+4, len(w))], "\n      ")
		}
	}
	return "lengths differ: got " + itoa(len(g)) + " lines, want " + itoa(len(w))
}

func itoa(n int) string { return strconv.Itoa(n) }

// A rename is the table's entries and nothing shaped like them: the three
// role models move, and any other `agents.*_model` — a real key such as
// agents.drafter_model, or one added later — is not mapped away.
func TestRenamedKey_OnlyTheRolesTheTableLists(t *testing.T) {
	for key, want := range map[string]string{
		"agents.researcher_model": "agents.profiles.researcher.model",
		"agents.Writer_Model":     "agents.profiles.writer.model",
		"agents.reviewer_model":   "agents.profiles.reviewer.model",
		"agents.drafter_model":    "",
		"agents.designer_model":   "",
		"agents.planner_model":    "",
	} {
		if got := RenamedKey(key); got != want {
			t.Errorf("RenamedKey(%q) = %q, want %q", key, got, want)
		}
	}
}
