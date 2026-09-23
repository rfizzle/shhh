package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
)

// A role edited from the manager is read back through the registration a
// drafted profile's save ends on, so the next spawn card describes the role
// as the file now reads and the spawn tool the model holds says the same. A
// file the loader refuses changes nothing the session is running.
// See docs/capabilities/subagents.md#a-profile-is-a-file.
func TestPersonasReloadMakesAnEditedRoleTheRunningSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	path := filepath.Join(t.TempDir(), "critic.toml")
	write := func(text string) {
		t.Helper()
		must(t, os.WriteFile(path, []byte(text), 0o644))
	}

	agents := &agentProfiles{profiles: subagent.BuiltinProfiles(), definitions: map[string]config.AgentDefinition{}}
	sup := subagent.New(t.Context(), subagent.Options{Root: t.TempDir(), Profiles: agents.profiles})
	t.Cleanup(sup.Close)
	tools := subagent.Definitions(agents.profiles)
	env := &sessionEnv{
		prov: assemblyProvider{},
		replaceTools: func(edit func([]provider.Tool) []provider.Tool) {
			tools = edit(tools)
		},
	}
	p := buildPersonas(chatSession{}, env, agents, sup, meter.New(nil))

	// What the spawn card's About row says about critic: the same plan the
	// session's gated preview builds the card's row from.
	card := func() string {
		t.Helper()
		plan, err := subagent.SpawnPlan(agents.profiles, json.RawMessage(`{"role":"critic","task":"read the change"}`))
		if err != nil {
			t.Fatalf("critic should be spawnable: %v", err)
		}
		return plan.About
	}
	spawnTool := func() provider.Tool {
		t.Helper()
		for _, tool := range tools {
			if tool.Name == subagent.SpawnToolName {
				return tool
			}
		}
		t.Fatal("the spawn tool went missing")
		return provider.Tool{}
	}

	write("description = \"reads a diff for style\"\npermissions = [\"read\"]\n")
	must(t, p.Reload(path))
	if got := card(); !strings.Contains(got, "reads a diff for style") {
		t.Fatalf("the spawn card should describe the role as written, got %q", got)
	}

	// The edit the manager's editor makes, then the reload its exit runs.
	write("description = \"reads a diff for security\"\npermissions = [\"read\"]\n")
	must(t, p.Reload(path))
	if got := card(); !strings.Contains(got, "reads a diff for security") {
		t.Fatalf("the next spawn card should carry the edited description, got %q", got)
	}
	if got := sup.Profiles()["critic"].Description; got != "reads a diff for security" {
		t.Fatalf("the supervisor should spawn the edited role, got %q", got)
	}
	tool := spawnTool()
	if !strings.Contains(tool.Description, "reads a diff for security") || strings.Contains(tool.Description, "for style") {
		t.Fatalf("the spawn tool should describe the role as edited:\n%s", tool.Description)
	}
	if !strings.Contains(string(tool.Parameters), `"critic"`) {
		t.Fatalf("the spawn tool's role enum should name the role:\n%s", tool.Parameters)
	}

	// A file the loader refuses leaves the role, the card and the tool as
	// they were, and says why.
	write("description = \"half an edit\"\ncolour = \"red\"\n")
	err := p.Reload(path)
	if err == nil || !strings.Contains(err.Error(), "colour") {
		t.Fatalf("the loader's refusal should come back, got %v", err)
	}
	if got := card(); !strings.Contains(got, "reads a diff for security") {
		t.Fatalf("a refused file should leave the role as it was, got %q", got)
	}
	if got := spawnTool().Description; strings.Contains(got, "half an edit") {
		t.Fatalf("a refused file should leave the spawn tool as it was:\n%s", got)
	}
}

// A conversation spawns the roles that read, so an edit that grants a
// writing tier is refused there rather than let in by the reload.
func TestPersonasReloadKeepsAConversationsRolesReaders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	path := filepath.Join(t.TempDir(), "critic.toml")
	must(t, os.WriteFile(path, []byte("description = \"reads\"\npermissions = [\"read\"]\n"), 0o644))

	agents := (&agentProfiles{profiles: subagent.BuiltinProfiles(), definitions: map[string]config.AgentDefinition{}}).readers()
	sup := subagent.New(t.Context(), subagent.Options{Root: t.TempDir(), Profiles: agents.profiles})
	t.Cleanup(sup.Close)
	env := &sessionEnv{prov: assemblyProvider{}, replaceTools: func(func([]provider.Tool) []provider.Tool) {}}
	p := buildPersonas(chatSession{conversation: true}, env, agents, sup, meter.New(nil))
	must(t, p.Reload(path))

	must(t, os.WriteFile(path, []byte("description = \"writes now\"\npermissions = [\"read\", \"write\"]\n"), 0o644))
	if err := p.Reload(path); err == nil {
		t.Fatal("a conversation should refuse a role edited to write")
	}
	if prof := agents.profiles["critic"]; prof.Writes || prof.Description != "reads" {
		t.Fatalf("the conversation's role should be as it was: %+v", prof)
	}
}
