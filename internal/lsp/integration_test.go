package lsp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGoplsIntegration exercises the real client path against an actual gopls
// when one is on PATH; everywhere else it skips, keeping CI hermetic.
func TestGoplsIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH")
	}

	root := t.TempDir()
	writeFile := func(name, content string) string {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	writeFile("go.mod", "module example.com/lsptest\n\ngo 1.21\n")
	mainPath := writeFile("main.go", "package main\n\nfunc main() {\n\tgreet()\n}\n\nfunc greet() {\n\tprintln(\"hi\")\n}\n")

	m := NewManager(root, DetectServers(), Options{
		RequestTimeout:     60 * time.Second,
		DiagnosticsTimeout: 60 * time.Second,
	})
	defer m.Shutdown()

	// A broken edit must surface an error diagnostic.
	writeFile("main.go", "package main\n\nfunc main() {\n\tgreeet()\n}\n\nfunc greet() {\n\tprintln(\"hi\")\n}\n")
	out := m.DiagnosticsAfterChange(mainPath)
	if !strings.Contains(out, "error") || !strings.Contains(out, "main.go:4") {
		t.Fatalf("expected an error diagnostic on main.go:4, got %q", out)
	}

	// Fixing the file must clear the diagnostics block.
	writeFile("main.go", "package main\n\nfunc main() {\n\tgreet()\n}\n\nfunc greet() {\n\tprintln(\"hi\")\n}\n")
	if out := m.DiagnosticsAfterChange(mainPath); out != "" {
		t.Fatalf("fixed file should have no diagnostics, got %q", out)
	}

	// definition on the call site lands on the declaration.
	def, err := m.Definition(mainPath, 4, "greet")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, "main.go:7") {
		t.Fatalf("definition of greet should be main.go:7, got %q", def)
	}

	// references sees both the declaration and the call.
	refs, err := m.References(mainPath, 7, "greet")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(refs, "main.go:4") || !strings.Contains(refs, "main.go:7") {
		t.Fatalf("references should include call and declaration, got %q", refs)
	}
}
