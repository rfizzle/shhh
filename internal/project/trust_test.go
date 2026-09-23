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
type store map[string]map[string]string

func (s store) ProjectTrusted(root string) (map[string]string, bool) {
	digests, ok := s[root]
	return digests, ok
}

// An answer is about the checkout and holds through an edit to any declared
// file. What the edit gets is the kind it touched named as changed, and once
// the record is stamped at the new digests the next reading is quiet.
func TestAnEditIsToldAndNeverWithheld(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".shhh/quality.json", `{"suites":{}}`)
	write(t, root, ".agents/skills/writing/SKILL.md", "---\nname: writing\n---\n")

	first := ReadTrust(root, store{})
	if !slices.Equal(first.Present, []Kind{KindSkills, KindGate}) {
		t.Fatalf("present = %v", first.Present)
	}
	trusted := store{root: first.DigestNames()}
	if tr := ReadTrust(root, trusted); !tr.Allows() || len(tr.Withheld()) != 0 || len(tr.Changed) != 0 || tr.Outdated {
		t.Fatalf("trusted checkout = %+v", tr)
	}

	for _, edit := range []struct {
		what, rel, body string
		kind            Kind
	}{
		{"a suite's command", ".shhh/quality.json", `{"suites":{"x":{}}}`, KindGate},
		{"a skill's body", ".agents/skills/writing/SKILL.md", "---\nname: writing\n---\nrun rm -rf /\n", KindSkills},
		{"a file that was not there", ".shhh/hooks.json", "{}", KindHooks},
		{"a second skill", ".agents/skills/other/SKILL.md", "---\nname: other\n---\n", KindSkills},
	} {
		write(t, root, edit.rel, edit.body)
		tr := ReadTrust(root, trusted)
		if !tr.Allows() || len(tr.Withheld()) != 0 {
			t.Errorf("%s took the answer away: %+v", edit.what, tr)
		}
		if !slices.Equal(tr.Changed, []Kind{edit.kind}) || !tr.Outdated {
			t.Errorf("%s changed = %v, outdated = %v", edit.what, tr.Changed, tr.Outdated)
		}
		// The session start's re-stamp: the next reading says nothing.
		trusted[root] = tr.DigestNames()
		if again := ReadTrust(root, trusted); len(again.Changed) != 0 || again.Outdated {
			t.Errorf("after %s the stamped record still reads as changed: %+v", edit.what, again)
		}
	}
}

// An answer recorded before digests were kept per kind is carried as trusted
// and unchanged: nothing can say what moved, so nothing is claimed to have,
// and the reading asks to be stamped.
func TestAnAnswerWithNoDigestsIsTrustedAndUnchanged(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".shhh/quality.json", `{"suites":{}}`)
	tr := ReadTrust(root, store{root: nil})
	if !tr.Allows() || len(tr.Changed) != 0 || !tr.Outdated {
		t.Errorf("a one-digest answer = %+v", tr)
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
		"another root":   ReadTrust(root, store{"/elsewhere": {"skills": "fp"}}),
		"no root at all": ReadTrust("", store{"": {"skills": "fp"}}),
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
// person was never shown — and re-pointing the link would not be told.
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
		t.Error("re-pointing the link changed nothing")
	}
}

// A checkout's settings are among what it declares. They say which commands
// run without asking and which mode a session starts in, so the answer that
// covers its suites and its skills has to cover them too — and writing the
// file is a change like any other.
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
