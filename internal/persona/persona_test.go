package persona

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
)

func TestNormaliseChatDropsWriting(t *testing.T) {
	d := Draft{Name: "The Skeptic!", Description: "  checks   claims ", Permissions: []string{"write", "Web", "execute", "web"}, Prompt: " be sure ", Reasoning: "Medium"}
	if err := d.Normalise(KindChat); err != nil {
		t.Fatal(err)
	}
	if d.Name != "the-skeptic" || d.Description != "checks claims" || d.Prompt != "be sure" || d.Reasoning != "medium" {
		t.Fatalf("normalised = %+v", d)
	}
	if strings.Join(d.Permissions, ",") != "web" {
		t.Fatalf("chat persona kept writing tiers: %v", d.Permissions)
	}
	if d.Tier() != "read + web" {
		t.Fatalf("tier = %q", d.Tier())
	}
}

func TestNormaliseCodeKeepsTiersInOrder(t *testing.T) {
	d := Draft{Name: "test-writer", Description: "adds tests", Permissions: []string{"execute", "write"}, Prompt: "write tests"}
	if err := d.Normalise(KindCode); err != nil {
		t.Fatal(err)
	}
	if strings.Join(d.Permissions, ",") != "write,execute" || !d.Writes() {
		t.Fatalf("permissions = %v", d.Permissions)
	}
	bad := Draft{Name: "x", Description: "d", Prompt: "p", Reasoning: "lots"}
	if err := bad.Normalise(KindCode); err == nil {
		t.Fatal("bad reasoning accepted")
	}
	empty := Draft{Name: "x", Description: "d"}
	if err := empty.Normalise(KindCode); err == nil {
		t.Fatal("empty prompt accepted")
	}
}

func TestWriteRoundTrips(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agents")
	d := Draft{
		Name: "skeptic", Description: `checks "claims" against sources`, Model: "claude-haiku-4-5-20251001",
		Reasoning: "low", Permissions: []string{"web"}, MaxTokens: 50000, Why: "cheap model: wide reads",
		Prompt: "You are the skeptic.\nFind the primary source. Say \"\"\"sure\"\"\" only when it is.",
	}
	path, err := Write(dir, d, KindChat, false)
	if err != nil {
		t.Fatal(err)
	}
	def, err := config.LoadAgentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "skeptic" || def.Description != d.Description || def.Model != d.Model || def.Reasoning != "low" || def.MaxTokens != 50000 {
		t.Fatalf("loaded = %+v", def)
	}
	if !def.Has(config.PermissionWeb) || def.Writes() {
		t.Fatalf("permissions = %v", def.Permissions)
	}
	if !strings.Contains(def.Prompt, "primary source") {
		t.Fatalf("prompt = %q", def.Prompt)
	}
	if _, err := Write(dir, d, KindChat, false); err == nil {
		t.Fatal("overwrote an existing profile")
	}
	if _, err := Write(dir, d, KindChat, true); err != nil {
		t.Fatal(err)
	}
}

func TestParseDraftAndQuestions(t *testing.T) {
	o, ok := parse(`{"profile":{"name":"Docs Keeper","description":"updates stale docs","permissions":["write"],"prompt":"Fix docs."}}`, KindCode)
	if !ok || o.Failed || o.Draft == nil || o.Draft.Name != "docs-keeper" {
		t.Fatalf("draft = %+v ok=%v", o, ok)
	}
	o, ok = parse("Sure, here you go: {\"questions\":[\"Which language?\",\"  \",\"Run tests too?\"]} done", KindCode)
	if !ok || len(o.Questions) != 2 {
		t.Fatalf("questions = %+v ok=%v", o, ok)
	}
	o, ok = parse(`{"profile":{"name":"","description":"x","permissions":[],"prompt":"p"}}`, KindChat)
	if !ok || !o.Failed {
		t.Fatalf("unloadable draft not reported: %+v", o)
	}
	if _, ok := parse("not json", KindChat); ok {
		t.Fatal("garbage parsed")
	}
}

func TestPromptsLeanBySession(t *testing.T) {
	chat, code := systemPrompt(KindChat), systemPrompt(KindCode)
	if !strings.Contains(chat, "never grant write") || !strings.Contains(chat, "notebook") {
		t.Fatal("chat prompt does not read as a read-only persona")
	}
	if !strings.Contains(code, "patch") || !strings.Contains(code, "verifies") {
		t.Fatal("code prompt does not read as an engineering role")
	}
	u := userPrompt(Request{Kind: KindChat, Brief: "a skeptic", Existing: []string{"researcher"}, Current: &Draft{Name: "skeptic"}, Feedback: "gentler"})
	for _, want := range []string{"researcher", "a skeptic", "CURRENT DRAFT", "gentler"} {
		if !strings.Contains(u, want) {
			t.Errorf("user prompt missing %q", want)
		}
	}
	if len(Suggestions(KindChat)) == 0 || Suggestions(KindChat)[0] == Suggestions(KindCode)[0] {
		t.Fatal("suggestions do not differ by session")
	}
}

func TestExistingSorted(t *testing.T) {
	got := Existing(map[string]config.AgentDefinition{"zed": {}, "researcher": {}}, "writer", "researcher")
	if strings.Join(got, ",") != "researcher,writer,zed" {
		t.Fatalf("existing = %v", got)
	}
}
