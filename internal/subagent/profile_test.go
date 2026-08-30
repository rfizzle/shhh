package subagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
)

func customProfiles() Profiles {
	p := BuiltinProfiles()
	p["critic"] = Profile{Name: "critic", Description: "reads a diff and judges it", Mode: agent.ModePlan, HasMode: true, MaxTokens: 50000, MaxRounds: 7}
	p["fixer"] = Profile{Name: "fixer", Writes: true}
	return p
}

func TestProfilesParseAndNames(t *testing.T) {
	p := customProfiles()
	if got := p.Names(); strings.Join(got, ",") != "researcher,writer,reviewer,critic,fixer" {
		t.Fatalf("built-ins first, then custom alphabetically: %v", got)
	}
	if prof, err := p.Parse(" Critic "); err != nil || prof.Name != "critic" {
		t.Fatalf("parse is case- and space-insensitive: %v %v", prof, err)
	}
	if _, err := p.Parse("admin"); err == nil || !strings.Contains(err.Error(), "fixer") {
		t.Fatalf("an unknown role lists the valid ones: %v", err)
	}
	if _, err := BuiltinProfiles().Parse("critic"); err == nil {
		t.Fatal("a custom role is unknown to the built-in set")
	}
	if prof, err := BuiltinProfiles().Parse("reviewer"); err != nil || !prof.HasMode || prof.Mode != agent.ModePlan || prof.Writes {
		t.Fatalf("the reviewer is built in, read-only, in plan mode: %+v %v", prof, err)
	}
}

func TestDefinitionsListProfiles(t *testing.T) {
	defs := Definitions(customProfiles())
	var spawn provider.Tool
	for _, d := range defs {
		if d.Name == SpawnToolName {
			spawn = d
		}
	}
	if !strings.Contains(spawn.Description, "'critic' (reads a diff and judges it)") {
		t.Fatalf("the description tells the model what each profile is for: %s", spawn.Description)
	}
	var schema struct {
		Properties struct {
			Role struct {
				Enum []string `json:"enum"`
			} `json:"role"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(spawn.Parameters, &schema); err != nil {
		t.Fatalf("parameters must stay valid JSON: %v", err)
	}
	if strings.Join(schema.Properties.Role.Enum, ",") != "researcher,writer,reviewer,critic,fixer" {
		t.Fatalf("the enum is the profile set: %v", schema.Properties.Role.Enum)
	}
}

func TestParseSpawnArgsUsesProfileDefaults(t *testing.T) {
	p := customProfiles()
	args, err := parseSpawnArgs(p, json.RawMessage(`{"role":"critic","task":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if args.maxTokens != 50000 || args.maxRounds != 7 {
		t.Fatalf("a spawn naming no budget takes the profile's: %d tokens, %d rounds", args.maxTokens, args.maxRounds)
	}
	args, err = parseSpawnArgs(p, json.RawMessage(`{"role":"critic","task":"x","max_tokens":60000,"max_rounds":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if args.maxTokens != 60000 || args.maxRounds != 3 {
		t.Fatalf("the spawn's own budget outranks the profile's: %d tokens, %d rounds", args.maxTokens, args.maxRounds)
	}
	if _, err := parseSpawnArgs(p, json.RawMessage(`{"role":"critic","task":"x","paths":["a/**"]}`)); err == nil {
		t.Fatal("a profile that changes nothing cannot claim paths")
	}
	args, err = parseSpawnArgs(p, json.RawMessage(`{"role":"fixer","task":"x","paths":["a/**"]}`))
	if err != nil || !args.profile.Writes {
		t.Fatalf("a writing profile claims paths like the built-in writer: %v", err)
	}
	plan, err := SpawnPlan(p, json.RawMessage(`{"role":"fixer","task":"x"}`))
	if err != nil || !plan.Writer {
		t.Fatalf("the approval card sees a writing profile as one: %+v %v", plan, err)
	}
}

func TestProfileModeIsClampedToParent(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{{text: "done"}}}
	sup := New(t.Context(), Options{Root: t.TempDir(), NewEnv: env.factory(), Profiles: customProfiles()})
	t.Cleanup(sup.Close)
	sup.SetParentMode(agent.ModeAuto)

	execTool(t, sup, SpawnToolName, `{"role":"critic","task":"judge it","name":"r"}`)
	if mode, ok := sup.AgentMode("r"); !ok || mode != agent.ModePlan {
		t.Fatalf("a profile's mode is the child's starting mode: %v %v", mode, ok)
	}

	p := customProfiles()
	p["loose"] = Profile{Name: "loose", Mode: agent.ModeAuto, HasMode: true}
	sup2 := New(t.Context(), Options{Root: t.TempDir(), NewEnv: env.factory(), Profiles: p})
	t.Cleanup(sup2.Close)
	sup2.SetParentMode(agent.ModeManual)
	execTool(t, sup2, SpawnToolName, `{"role":"loose","task":"try","name":"l"}`)
	if mode, _ := sup2.AgentMode("l"); mode != agent.ModeManual {
		t.Fatalf("a profile can never start looser than its parent: %v", mode)
	}
}

func TestBuiltinReviewerCanBeOverridden(t *testing.T) {
	p := BuiltinProfiles()
	p[RoleReviewer] = Profile{Name: RoleReviewer, Description: "mine", Writes: false}
	if got := strings.Join(p.Names(), ","); got != "researcher,writer,reviewer" {
		t.Fatalf("an override keeps the built-in's place, got %s", got)
	}
	if prof, _ := p.Parse("reviewer"); prof.Description != "mine" {
		t.Fatal("the file's profile should replace the built-in")
	}
}
