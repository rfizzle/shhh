package project

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// A declaration rendered from its lists reads back as the same lists, and
// the bytes are what the loader reads: the drafter writes no TOML, so this
// is the whole of what stands between an answer and a file.
func TestARenderedDeclarationReadsBackAsItsLists(t *testing.T) {
	want := Toolchain{
		Packages: []string{"shellcheck"},
		Install: []string{
			"go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.5.0",
			"go install github.com/securego/gosec/v2/cmd/gosec@v2.21.4",
		},
		Hosts: []string{"proxy.golang.org", "sum.golang.org"},
		Check: []string{"golangci-lint", "gosec", "shellcheck"},
	}
	got, err := ParseToolchain(want.Render())
	if err != nil {
		t.Fatalf("a rendered declaration does not load: %v\n%s", err, want.Render())
	}
	if !slices.Equal(got.Packages, want.Packages) || !slices.Equal(got.Install, want.Install) ||
		!slices.Equal(got.Hosts, want.Hosts) || !slices.Equal(got.Check, want.Check) {
		t.Fatalf("read back %+v, want %+v", got, want)
	}
	if strings.Contains(string(Toolchain{Check: []string{"gosec"}}.Render()), "install") {
		t.Fatal("an empty list was written as a key")
	}
}

// ParseToolchain is the loader's reading: an unpinned line is refused in the
// words LoadToolchain uses, naming the file and the entry.
func TestParseToolchainRefusesInTheLoadersWords(t *testing.T) {
	_, err := ParseToolchain(Toolchain{Install: []string{"go install example.com/cmd/tool@latest"}}.Render())
	if err == nil {
		t.Fatal("an unpinned line was read")
	}
	for _, want := range []string{ToolchainFile, `install[0] "go install example.com/cmd/tool@latest"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err, want)
		}
	}
}

// Every pinned line the grammar's table gives as an example is one the
// loader takes, and the example file at its head loads: a grammar the model
// is handed whose own examples are refused would teach it to be refused.
func TestTheGrammarsExamplesLoad(t *testing.T) {
	block := regexp.MustCompile("(?s)```toml\n(.*?)```").FindStringSubmatch(ToolchainGrammar)
	if block == nil {
		t.Fatal("the grammar carries no example file")
	}
	if _, err := ParseToolchain([]byte(block[1])); err != nil {
		t.Fatalf("the grammar's example file does not load: %v", err)
	}
	rows := regexp.MustCompile("(?m)^\\| [^|]+ \\| `([^`]+)`").FindAllStringSubmatch(ToolchainGrammar, -1)
	if len(rows) < 5 {
		t.Fatalf("the installer table has %d rows, want one per installer", len(rows))
	}
	for _, row := range rows {
		if err := pinnedInstall(row[1]); err != nil {
			t.Errorf("the grammar's example %q is refused: %v", row[1], err)
		}
	}
}

// The declaration as it stands is read whatever the checkout's trust, since
// a review reads it as text; a link is no declaration.
func TestDeclaredReadsTheFileAsText(t *testing.T) {
	root := t.TempDir()
	if _, ok := Declared(root); ok {
		t.Fatal("a checkout with no declaration reported one")
	}
	if err := os.MkdirAll(filepath.Join(root, StateDir), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(ToolchainFile))
	if err := os.WriteFile(path, []byte("check = [\"gosec\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if data, ok := Declared(root); !ok || string(data) != "check = [\"gosec\"]\n" {
		t.Fatalf("Declared = %q, %v", data, ok)
	}
	elsewhere := filepath.Join(t.TempDir(), "toolchain.toml")
	if err := os.WriteFile(elsewhere, []byte("check = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, path); err != nil {
		t.Skip("no symlinks here")
	}
	if _, ok := Declared(root); ok {
		t.Fatal("a linked declaration was read")
	}
}

// The evidence is the files that say what the checks run, the workflows
// among them, and the lockfiles by name only; a file past the bound is cut
// and says so.
func TestDraftEvidenceReadsTheChecksAndNamesTheLockfiles(t *testing.T) {
	root := t.TempDir()
	write := func(rel, text string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/x\n\ngo 1.24\n")
	write("Makefile", "lint:\n\tgolangci-lint run\n"+strings.Repeat("#\n", draftFileBytes))
	write(".github/workflows/ci.yml", "jobs: {}\n")
	write(".github/workflows/notes.txt", "not a workflow\n")
	write("go.sum", "example.com/y v1.0.0 h1:x\n")
	ev := ReadDraftEvidence(root)
	var paths []string
	for _, f := range ev.Files {
		paths = append(paths, f.Path)
		if f.Path == "Makefile" && (!f.Cut || len(f.Text) > draftFileBytes) {
			t.Errorf("the long Makefile was not cut at the bound (cut %v, %d bytes)", f.Cut, len(f.Text))
		}
	}
	if !slices.Equal(paths, []string{"go.mod", "Makefile", ".github/workflows/ci.yml"}) {
		t.Fatalf("the evidence read %v", paths)
	}
	if !slices.Equal(ev.Lockfiles, []string{"go.sum"}) {
		t.Fatalf("the lockfiles named are %v", ev.Lockfiles)
	}
}
