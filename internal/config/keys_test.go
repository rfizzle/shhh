package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	must(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

// A misspelled table is refused with the path and the table, once — not
// once for the table and again for every key beneath it — and the nearest
// known table is offered.
func TestLoadFrom_UnknownTable(t *testing.T) {
	path := writeConfig(t, "[behaviour]\nsilent_mode = true\nshell = \"zsh\"\n")
	_, err := LoadFrom(path)
	var unknown *UnknownKeyError
	if !errors.As(err, &unknown) {
		t.Fatalf("a misspelled table loaded: %v", err)
	}
	if unknown.Path != path {
		t.Fatalf("the error names %q, not the file", unknown.Path)
	}
	if len(unknown.Keys) != 1 || unknown.Keys[0].Key != "behaviour" || unknown.Keys[0].Nearest != "behavior" {
		t.Fatalf("keys = %+v; want behaviour → behavior alone", unknown.Keys)
	}
	if msg := err.Error(); !strings.Contains(msg, path) || !strings.Contains(msg, `"behaviour"`) || !strings.Contains(msg, `"behavior"`) {
		t.Fatalf("the sentence does not carry the path, the key and the offer: %q", msg)
	}
}

// A misspelled key is refused by its dotted path, and a key under a table
// the user named (a server, a role) is walked through to the field beneath.
func TestLoadFrom_UnknownKey(t *testing.T) {
	for _, tc := range []struct{ body, key, nearest string }{
		{"[provider]\nmodle = \"x\"\n", "provider.modle", "provider.model"},
		{"[mcp.servers.foo]\ncommnd = \"x\"\n", "mcp.servers.foo.commnd", "mcp.servers.foo.command"},
		{"[agents.profiles.writer]\nmodle = \"y\"\n", "agents.profiles.writer.modle", "agents.profiles.writer.model"},
		{"[agents.profles.writer]\nmodel = \"y\"\n", "agents.profles.writer", "agents.profiles.writer"},
		{"[behaviour.deep]\nx = 1\n", "behaviour.deep", "behavior.deep"},
		{"[PROVIDER]\nMODLE = \"x\"\n", "PROVIDER.MODLE", "PROVIDER.model"},
		{"[behavior]\nsilent = true\n", "behavior.silent", ""},
		{"top = 1\n", "top", ""},
		{"[lps]\ndisabled = true\n", "lps", "lsp"},
	} {
		_, err := LoadFrom(writeConfig(t, tc.body))
		var unknown *UnknownKeyError
		if !errors.As(err, &unknown) {
			t.Errorf("%q loaded: %v", tc.body, err)
			continue
		}
		if len(unknown.Keys) != 1 || unknown.Keys[0].Key != tc.key || unknown.Keys[0].Nearest != tc.nearest {
			t.Errorf("%q: keys = %+v; want %s → %q", tc.body, unknown.Keys, tc.key, tc.nearest)
		}
		if tc.nearest == "" && strings.Contains(err.Error(), "did you mean") {
			t.Errorf("%q: an offer with nothing near enough: %v", tc.body, err)
		}
	}
}

// A sub-table written above the table it belongs to is still one mistake,
// named by the table.
func TestLoadFrom_UnknownTable_ChildWrittenFirst(t *testing.T) {
	_, err := LoadFrom(writeConfig(t, "[behaviour.deep]\nx = 1\n\n[behaviour]\ny = 2\n"))
	var unknown *UnknownKeyError
	if !errors.As(err, &unknown) {
		t.Fatalf("loaded: %v", err)
	}
	if len(unknown.Keys) != 1 || unknown.Keys[0].Key != "behaviour" {
		t.Fatalf("keys = %+v; want behaviour alone", unknown.Keys)
	}
}

// Two mistakes are two keys in one sentence, and a key the user names under
// a map is never itself refused.
func TestLoadFrom_UnknownKeys_Several(t *testing.T) {
	path := writeConfig(t, "[behaviour]\nshell = \"zsh\"\n\n[provider]\nmodle = \"x\"\n\n[mcp.servers.anything]\ncommand = \"x\"\n[mcp.servers.anything.env]\nWHATEVER = \"1\"\n")
	_, err := LoadFrom(path)
	var unknown *UnknownKeyError
	if !errors.As(err, &unknown) {
		t.Fatalf("two misspellings loaded: %v", err)
	}
	got := make([]string, len(unknown.Keys))
	for i, k := range unknown.Keys {
		got[i] = k.Key
	}
	if strings.Join(got, " ") != "behaviour provider.modle" {
		t.Fatalf("keys = %v", got)
	}
	if !strings.Contains(err.Error(), ": unknown keys ") {
		t.Fatalf("two keys read as one: %v", err)
	}
}

// A file spelling every key correctly loads as it always did, including the
// tables whose keys are the user's own.
func TestLoadFrom_KnownKeysLoadClean(t *testing.T) {
	path := writeConfig(t, `[provider]
default = "anthropic"
model = "claude-opus-5"

[behavior]
silent_mode = true
tree_check = false

[mcp.servers.docs]
command = "docs-server"
read_only = true
[mcp.servers.docs.env]
TOKEN = "${DOCS_TOKEN}"

[agents.profiles.writer]
model = "inherit"

[prompts]
steer = "steer.md"
`)
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("a correct file was refused: %v", err)
	}
	if cfg.Provider.Model != "claude-opus-5" || !cfg.Behavior.SilentMode || cfg.MCP.Servers["docs"].Command != "docs-server" {
		t.Fatalf("a correct file did not load its values: %+v", cfg)
	}
}

func TestEditDistance(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"model", "model", 0},
		{"modle", "model", 1},
		{"behaviour", "behavior", 1},
		{"commnd", "command", 1},
		{"silent", "silent_mode", 5},
		{"", "abc", 3},
	} {
		if got := editDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
