package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeAgent(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadAgentsFromReadsAndDefaults(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir, "reviewer.toml", `
description = "reviews a diff"
model = "claude-haiku-4-5-20251001"
reasoning = "low"
permissions = ["read", "web"]
tools = ["read_file", "search", "web_search"]
mode = "plan"
prompt = "Be terse."
max_tokens = 300000
max_rounds = 20
`)
	defs, err := LoadAgentsFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	def, ok := defs["reviewer"]
	if !ok {
		t.Fatalf("profile not loaded: %v", defs)
	}
	if def.Name != "reviewer" || def.Path == "" {
		t.Fatalf("name defaults to the file stem and the path is kept: %+v", def)
	}
	if def.Writes() {
		t.Fatal("read+web must not count as writing")
	}
	if !def.Has(PermissionRead) || !def.Has(PermissionWeb) || def.Has(PermissionExecute) {
		t.Fatalf("permissions misread: %v", def.Permissions)
	}
	if def.Allows("glob") || !def.Allows("search") {
		t.Fatal("the tool allowlist must narrow within the granted tiers")
	}
	if def.InheritsReasoning() || def.ProfileModel() != "claude-haiku-4-5-20251001" {
		t.Fatalf("reasoning/model misread: %+v", def)
	}
}

func TestLoadAgentsReadIsImplicit(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir, "fixer.toml", `permissions = ["write", "execute"]`)
	defs, err := LoadAgentsFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	def := defs["fixer"]
	if !def.Has(PermissionRead) {
		t.Fatal("read is always granted")
	}
	if !def.Writes() || !def.Allows("write_file") {
		t.Fatal("write and execute make the agent one that changes things")
	}
	if !def.InheritsReasoning() || def.ProfileModel() != "" {
		t.Fatal("an unset model and reasoning inherit")
	}
}

// Running the project's declared suite is a read of the project's health, so
// a profile granting nothing but read may name the gate — which is the whole
// point of it: a role drawn so that it can never run an arbitrary command can
// still verify that a change compiles and its tests pass.
func TestLoadAgentsQualityGateIsARead(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir, "critic.toml", `tools = ["read_file", "quality_gate"]`)
	defs, err := LoadAgentsFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	def := defs["critic"]
	if def.Writes() || !def.Allows(QualityGateTool) || def.Allows("search") {
		t.Fatalf("a read-only profile may name the gate and nothing it did not list: %+v", def)
	}
	if !slices.Contains(KnownAgentTools(), QualityGateTool) {
		t.Errorf("the gate must be listable by name: %v", KnownAgentTools())
	}
}

func TestLoadAgentsInheritSpellings(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir, "kid.toml", `model = "inherit"
reasoning = "Inherit"`)
	defs, err := LoadAgentsFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if defs["kid"].ProfileModel() != "" || !defs["kid"].InheritsReasoning() {
		t.Fatal("\"inherit\" must defer at both fields")
	}
}

func TestLoadAgentsFirstDirectoryWins(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	writeAgent(t, first, "dup.toml", `description = "first"`)
	writeAgent(t, second, "dup.toml", `description = "second"`)
	writeAgent(t, second, "only.toml", `description = "only"`)
	defs, err := LoadAgentsFrom(first, second, filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if defs["dup"].Description != "first" {
		t.Fatalf("the earlier directory must shadow the later: %+v", defs["dup"])
	}
	if _, ok := defs["only"]; !ok {
		t.Fatal("profiles from the later directory still load")
	}
}

func TestLoadAgentsPromptFile(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir, "docs.md", "You write docs.")
	writeAgent(t, dir, "docs.toml", `prompt_file = "docs.md"`)
	defs, err := LoadAgentsFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if defs["docs"].Prompt != "You write docs." || defs["docs"].PromptFile != "" {
		t.Fatalf("prompt_file is read relative to the profile: %+v", defs["docs"])
	}
}

func TestLoadAgentsRejectsBadFiles(t *testing.T) {
	cases := []struct{ name, file, body, want string }{
		{"unknown key", "a.toml", `modle = "x"`, "unknown key"},
		{"unknown tier", "a.toml", `permissions = ["admin"]`, "unknown tier"},
		{"unknown tool", "a.toml", `tools = ["rm"]`, "unknown tool"},
		{"tool without tier", "a.toml", `tools = ["write_file"]`, `needs the "write" permission`},
		{"name mismatch", "a.toml", `name = "b"`, "does not match the file name"},
		{"bad name", "Bad Name.toml", ``, "lowercase letters"},
		{"prompt mode", "a.toml", `prompt_mode = "sideways"`, "prompt_mode"},
		{"replace without prompt", "a.toml", `prompt_mode = "replace"`, "needs a prompt"},
		{"both prompts", "a.toml", "prompt = \"x\"\nprompt_file = \"y\"", "both set"},
		{"missing prompt file", "a.toml", `prompt_file = "nope.md"`, "prompt_file"},
		{"bad toml", "a.toml", `= =`, "a.toml"},
		{"negative budget", "a.toml", `max_tokens = -1`, "max_tokens"},
		{"undersized default budget", "a.toml", `max_tokens = 200000`, "at least 300000"},
		{"reviewing writer", "a.toml", "reviews = true\npermissions = [\"write\"]", "cannot also be handed them"},
		// The gate is a read, so read-only profiles may name it; a profile
		// that writes may not, because it would run over the checkout its
		// worktree was copied from. Refused here rather than dropped when
		// the child is built, so the author reads it as a line in a file.
		{"gate for a writer", "a.toml", "tools = [\"quality_gate\"]\npermissions = [\"write\"]", "changes nothing"},
		{"gate for an executor", "a.toml", "tools = [\"quality_gate\"]\npermissions = [\"execute\"]", "changes nothing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeAgent(t, dir, tc.file, tc.body)
			_, err := LoadAgentsFrom(dir)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q should mention %q", err, tc.want)
			}
		})
	}
}

func TestAgentDirsFollowConfigPaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-test")
	dirs := AgentDirs()
	if len(dirs) == 0 || dirs[0] != filepath.Join("/tmp/xdg-test", "shhh", "agents") {
		t.Fatalf("the agents directory sits beside config.toml: %v", dirs)
	}
}
