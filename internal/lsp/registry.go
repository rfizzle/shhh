package lsp

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// ServerSpec describes one language server: how to launch it and which file
// extensions it owns.
type ServerSpec struct {
	Name       string
	Command    string
	Args       []string
	Extensions []string
}

// builtinSpecs are the common servers auto-detected on PATH. Absence of a
// binary is a clean no-op: the spec is simply not offered.
func builtinSpecs() []ServerSpec {
	return []ServerSpec{
		{Name: "gopls", Command: "gopls", Extensions: []string{".go"}},
		{Name: "rust-analyzer", Command: "rust-analyzer", Extensions: []string{".rs"}},
		{Name: "typescript-language-server", Command: "typescript-language-server", Args: []string{"--stdio"},
			Extensions: []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}},
		{Name: "pyright", Command: "pyright-langserver", Args: []string{"--stdio"},
			Extensions: []string{".py", ".pyi"}},
	}
}

// DetectServers returns the built-in server specs whose binaries are on PATH.
func DetectServers() []ServerSpec {
	return detectServers(exec.LookPath)
}

func detectServers(lookPath func(string) (string, error)) []ServerSpec {
	var found []ServerSpec
	for _, spec := range builtinSpecs() {
		if _, err := lookPath(spec.Command); err == nil {
			found = append(found, spec)
		}
	}
	return found
}

// languageID maps a file extension to the LSP language identifier sent in
// didOpen.
func languageID(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".rs":
		return "rust"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".py", ".pyi":
		return "python"
	default:
		return "plaintext"
	}
}
