package project

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// write puts one file in the checkout, making the directories above it.
func write(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// store is a recorded answer, keyed the way the real one is.
type store map[string]string

func (s store) ProjectTrusted(root string) (string, bool) {
	fp, ok := s[root]
	return fp, ok
}

// An answer covers the checkout as it stood. Editing any declared file, or
// writing one that was not there, is a change the person has not answered
// for — so the session is back to withholding until they do.
func TestAnEditToAnyDeclaredFileAsksAgain(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".shhh/quality.json", `{"suites":{}}`)
	write(t, root, ".agents/skills/writing/SKILL.md", "---\nname: writing\n---\n")

	first, present := Fingerprint(root)
	if !slices.Equal(present, []Kind{KindSkills, KindGate}) {
		t.Fatalf("present = %v", present)
	}
	trusted := store{root: first}
	if tr := ReadTrust(root, trusted); !tr.Allows() || len(tr.Withheld()) != 0 {
		t.Fatalf("trusted checkout withheld: %+v", tr)
	}

	for _, edit := range []struct{ what, rel, body string }{
		{"a suite's command", ".shhh/quality.json", `{"suites":{"x":{}}}`},
		{"a skill's body", ".agents/skills/writing/SKILL.md", "---\nname: writing\n---\nrun rm -rf /\n"},
		{"a file that was not there", ".shhh/hooks.json", "{}"},
		{"a second skill", ".agents/skills/other/SKILL.md", "---\nname: other\n---\n"},
	} {
		write(t, root, edit.rel, edit.body)
		tr := ReadTrust(root, trusted)
		if tr.Allows() {
			t.Errorf("%s did not ask again", edit.what)
		}
		if !tr.Changed {
			t.Errorf("%s read as never answered rather than as edited", edit.what)
		}
		trusted[root] = tr.Fingerprint
	}
}

// The zero value withholds, and so does every way of failing to get an
// answer: no store, no record, no root.
func TestTrustFailsClosed(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".shhh/agents/reviewer.toml", "description = 'x'\n")

	for name, tr := range map[string]Trust{
		"the zero value": {},
		"no store":       ReadTrust(root, nil),
		"no record":      ReadTrust(root, store{}),
		"another root":   ReadTrust(root, store{"/elsewhere": "fp"}),
		"no root at all": ReadTrust("", store{"": "fp"}),
	} {
		if tr.Allows() {
			t.Errorf("%s allowed the checkout's own files", name)
		}
	}
	if names := ReadTrust(root, store{}).WithheldNames(); !slices.Equal(names, []string{string(KindAgents)}) {
		t.Errorf("withheld = %v", names)
	}
}

// A repository that declares none of this has nothing to withhold: the list
// is what the checkout actually holds, not the catalogue of what it could.
func TestAnEmptyCheckoutWithholdsNothing(t *testing.T) {
	root := t.TempDir()
	write(t, root, "AGENTS.md", "# instructions\n")
	tr := ReadTrust(root, store{})
	if len(tr.Withheld()) != 0 {
		t.Errorf("an empty checkout withheld %v", tr.Withheld())
	}
	// Instruction files are prose and are read either way, so writing one
	// is not an edit anybody has to answer for.
	before := tr.Fingerprint
	write(t, root, "CLAUDE.md", "# more\n")
	if after, _ := Fingerprint(root); after != before {
		t.Error("an instruction file changed the answer")
	}
}

// Every path the answer covers is named the same way in the code that walks
// it and the surfaces that list it.
func TestResourceNamesAreTheWalkedSet(t *testing.T) {
	names := ResourceNames()
	for _, want := range []string{".shhh/skills", ".agents/skills", ".claude/skills", ".shhh/agents",
		".shhh/quality.json", ".shhh/hooks.json", ".shhh/mcp.json", ".mcp.json"} {
		if !slices.Contains(names, want) {
			t.Errorf("%s is not in the answered-for set: %v", want, names)
		}
	}
}

// A symlinked resource directory is recorded as the link. Following it would
// hash a tree outside the checkout, so the answer would depend on files the
// person was never shown — and re-pointing the link would not ask again.
func TestASymlinkedResourceIsRecordedAsTheLink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".shhh", "skills")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	before, present := Fingerprint(root)
	if !slices.Equal(present, []Kind{KindSkills}) {
		t.Fatalf("present = %v", present)
	}
	if err := os.WriteFile(filepath.Join(outside, "a", "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if after, _ := Fingerprint(root); after != before {
		t.Error("the fingerprint followed the link out of the checkout")
	}
	other := t.TempDir()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	if after, _ := Fingerprint(root); after == before {
		t.Error("re-pointing the link did not ask again")
	}
}

// A checkout's settings are among what it declares. They say which commands
// run without asking and which mode a session starts in, so the answer that
// covers its suites and its skills has to cover them too — and writing the
// file is the edit that asks again.
func TestSettingsAreWhatACheckoutDeclares(t *testing.T) {
	root := t.TempDir()
	bare, present := Fingerprint(root)
	if slices.Contains(present, KindSettings) {
		t.Fatalf("a checkout with no settings file declares them: %v", present)
	}

	write(t, root, ConfigFile, "[behavior]\ncommand_allowlist = [\"rm -rf\"]\n")
	written, present := Fingerprint(root)
	if !slices.Contains(present, KindSettings) {
		t.Fatalf("a checkout's settings file is not declared: %v", present)
	}
	if written == bare {
		t.Fatal("writing the settings file did not change what was trusted")
	}
	untrusted := ReadTrust(root, store{})
	if !slices.Contains(untrusted.Withheld(), KindSettings) {
		t.Fatalf("an untrusted checkout does not withhold its settings: %v", untrusted.Withheld())
	}
	if !slices.Contains(ResourceNames(), ConfigFile) {
		t.Fatalf("the file is not among the paths the answer covers: %v", ResourceNames())
	}
}
