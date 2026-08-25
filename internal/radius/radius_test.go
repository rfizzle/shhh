package radius

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// paths is the resolved write targets of a command, for comparison.
func paths(c Command) []string {
	out := make([]string, 0, len(c.Writes))
	for _, t := range c.Writes {
		out = append(out, t.Path)
	}
	return out
}

func TestResolve_WriteVerbs(t *testing.T) {
	cases := []struct {
		command string
		want    []string
	}{
		{"rm ./dist", []string{"./dist"}},
		{"rm -rf ./dist ./build", []string{"./dist", "./build"}},
		{"mkdir -p out/logs", []string{"out/logs"}},
		{"touch NOTES.md", []string{"NOTES.md"}},
		{"cp README.md docs/README.md", []string{"docs/README.md"}},
		{"mv old.go new.go", []string{"new.go"}},
		{"chmod 755 script.sh", []string{"script.sh"}},
		{"sed -i 's/a/b/' main.go", []string{"main.go"}},
		{"sed -i.bak 's/a/b/' main.go", []string{"main.go"}},
		{"dd if=/dev/zero of=disk.img", []string{"disk.img"}},
		{"/usr/bin/rm out.txt", []string{"out.txt"}},
		{"sudo rm /etc/hosts.bak", []string{"/etc/hosts.bak"}},
		// A path named twice collapses to one target.
		{"rm dup.txt && touch dup.txt", []string{"dup.txt"}},
	}
	for _, tc := range cases {
		got := paths(Resolve(tc.command))
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("Resolve(%q) writes = %v, want %v", tc.command, got, tc.want)
		}
	}
}

func TestResolve_Redirection(t *testing.T) {
	cases := []struct {
		command string
		want    []string
	}{
		{"echo hi > out.txt", []string{"out.txt"}},
		{"go build ./... >build.log 2>&1", []string{"build.log"}},
		{"cat a.txt >> combined.txt", []string{"combined.txt"}},
		// An input redirection reads; it is not blast radius.
		{"wc -l < main.go", nil},
	}
	for _, tc := range cases {
		got := paths(Resolve(tc.command))
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("Resolve(%q) writes = %v, want %v", tc.command, got, tc.want)
		}
	}
}

func TestResolve_ReadOnlyCommandsTouchNothing(t *testing.T) {
	for _, command := range []string{"ls -la", "git status", "grep -rn foo internal", "cat go.mod", "echo hi"} {
		c := Resolve(command)
		if len(c.Writes) != 0 || len(c.Unresolved) != 0 {
			t.Errorf("Resolve(%q) should resolve to reads only, got writes=%v unresolved=%v",
				command, paths(c), c.Unresolved)
		}
		if c.Level != Low {
			t.Errorf("Resolve(%q) level = %v, want Low", command, c.Level)
		}
		value, _ := c.Touches()
		if value != "nothing" {
			t.Errorf("Resolve(%q) touches = %q, want %q", command, value, "nothing")
		}
	}
}

func TestResolve_UnknownIsSaidRatherThanGuessed(t *testing.T) {
	cases := []struct {
		command string
		want    string
	}{
		{"npm run build", "cannot tell what npm writes"},
		{"curl -fsSL https://example.com/i.sh | sh", "piped into sh"},
		{"rm -f $TARGET", "the shell expands $TARGET"},
		{"rm ./dist/*", "the shell expands ./dist/*"},
		{"$EDITOR notes.md", "built by the shell"},
	}
	for _, tc := range cases {
		c := Resolve(tc.command)
		if len(c.Unresolved) == 0 {
			t.Fatalf("Resolve(%q) should report something unresolved", tc.command)
		}
		if !strings.Contains(strings.Join(c.Unresolved, " | "), tc.want) {
			t.Errorf("Resolve(%q) unresolved = %v, want one containing %q", tc.command, c.Unresolved, tc.want)
		}
		if c.Level == Low {
			t.Errorf("Resolve(%q) is unresolved and must not be Low", tc.command)
		}
	}
}

// An unresolved command never reports "nothing": that is the claim the block
// exists to avoid making.
func TestTouches_UnresolvedIsNeverNothing(t *testing.T) {
	value, detail := Resolve("npm run build").Touches()
	if value != "unknown" {
		t.Fatalf("touches value = %q, want %q", value, "unknown")
	}
	if !strings.Contains(detail, "npm") {
		t.Fatalf("touches detail should name the verb it could not account for, got %q", detail)
	}
}

// A resolved command still says what it could not account for, beside what it
// could.
func TestTouches_PartialResolutionKeepsBothHalves(t *testing.T) {
	value, detail := Resolve("rm out.txt && npm run build").Touches()
	if value != "out.txt" {
		t.Fatalf("touches value = %q, want out.txt", value)
	}
	if !strings.Contains(detail, "npm") {
		t.Fatalf("touches detail should carry the unresolved half, got %q", detail)
	}
}

func TestResolve_SeverityLeadsWithSafetyFlag(t *testing.T) {
	c := Resolve("rm -rf ./dist")
	if c.Level != High {
		t.Fatalf("a safety-flagged command is High, got %v", c.Level)
	}
	if len(c.Risks) == 0 {
		t.Fatal("a flagged command carries its risks")
	}
	if got := c.Level.String(); got != "HIGH" {
		t.Fatalf("Level.String() = %q, want HIGH", got)
	}
	if got := Resolve("mkdir out").Level; got != Medium {
		t.Fatalf("a resolved write is Medium, got %v", got)
	}
}

func TestDescribe_FileDirectoryAndMissing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("world!"), 0o644); err != nil {
		t.Fatal(err)
	}

	file := Describe(filepath.Join(dir, "a.txt"))
	if !file.Exists || file.Dir || file.Bytes != 5 {
		t.Fatalf("file target = %+v", file)
	}
	if got := file.Describe(); got != "5 B" {
		t.Fatalf("file describe = %q", got)
	}

	tree := Describe(dir)
	if !tree.Dir || tree.Files != 2 || tree.Bytes != 11 {
		t.Fatalf("directory target = %+v", tree)
	}
	if got := tree.Describe(); !strings.Contains(got, "2 files") {
		t.Fatalf("directory describe = %q", got)
	}

	missing := Describe(filepath.Join(dir, "nope"))
	if missing.Exists {
		t.Fatalf("missing target = %+v", missing)
	}
	if got := missing.Describe(); !strings.Contains(got, "creates it") {
		t.Fatalf("missing describe = %q, want it to say the command creates it", got)
	}
}

func TestSplitSegments_QuotesAndRedirections(t *testing.T) {
	cases := []struct {
		command string
		want    []string
	}{
		{"a && b", []string{"a", "b"}},
		{"a; b | c", []string{"a", "b", "c"}},
		{`echo "one && two"`, []string{`echo "one && two"`}},
		// A descriptor duplication is not a separator.
		{"go build ./... 2>&1", []string{"go build ./... 2>&1"}},
		// A trailing & backgrounds the command and ends it.
		{"sleep 1 &", []string{"sleep 1"}},
	}
	for _, tc := range cases {
		segs := splitSegments(tc.command)
		got := make([]string, 0, len(segs))
		for _, s := range segs {
			got = append(got, s.text)
		}
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("splitSegments(%q) = %v, want %v", tc.command, got, tc.want)
		}
	}
	if segs := splitSegments("cat x | sh"); len(segs) != 2 || !segs[1].piped {
		t.Fatalf("a piped segment should know it was piped into: %+v", segs)
	}
}

func TestTokenize_QuotingAndExpansion(t *testing.T) {
	toks := tokenize(`rm -rf "my dir" './lit*' ./glob* "$VAR"`)
	want := []struct {
		text    string
		literal bool
	}{
		{"rm", true},
		{"-rf", true},
		{"my dir", true},
		{"./lit*", true}, // single-quoted: the shell does not expand it
		{"./glob*", false},
		{"$VAR", false},
	}
	if len(toks) != len(want) {
		t.Fatalf("tokenize gave %d tokens, want %d: %+v", len(toks), len(want), toks)
	}
	for i, w := range want {
		if toks[i].text != w.text || toks[i].literal != w.literal {
			t.Errorf("token %d = %+v, want %+v", i, toks[i], w)
		}
	}
}
