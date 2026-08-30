package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/skill"
)

func skillCatalog(t *testing.T) *skill.Catalog {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"documentation", "help"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: The " + name + " skill\n---\n# " + name + " body\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := skill.Discover([]skill.Root{{Path: root, Scope: skill.ScopeProject}})
	if c.Len() != 2 {
		t.Fatalf("fixture loaded %d skills: %v", c.Len(), c.Diagnostics)
	}
	return c
}

func skillModel(t *testing.T) Model {
	t.Helper()
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithSkills(skillCatalog(t), func(c *skill.Catalog) string { return strings.Join(c.Names(), " ") })
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	return updated.(Model)
}

func TestSkillCommand_SendsContentShowsCommand(t *testing.T) {
	m := skillModel(t)
	m = sendText(t, m, "/skill documentation write the changelog")

	msgs := m.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != provider.RoleUser || !skill.IsContent(last.Content) {
		t.Fatalf("the model should receive skill content as a user message, got %+v", last)
	}
	if !strings.Contains(last.Content, "# documentation body") || !strings.HasSuffix(last.Content, "write the changelog") {
		t.Fatalf("content should carry the body then the task:\n%s", last.Content)
	}
	if !transcriptContains(m, "/skill documentation write the changelog") {
		t.Fatal("the transcript shows the command the user typed")
	}
	if transcriptContains(m, "# documentation body") {
		t.Fatal("the skill body must not be painted as the user's words")
	}
}

func TestSkillCommand_ShortcutByName(t *testing.T) {
	m := skillModel(t)
	m = sendText(t, m, "/documentation")
	msgs := m.Messages()
	if last := msgs[len(msgs)-1]; last.Role != provider.RoleUser || !skill.IsContent(last.Content) {
		t.Fatalf("/<skill-name> should activate the skill, got %+v", last)
	}

	// A skill named like a real command does not take the command over.
	m = skillModel(t)
	before := len(m.Messages())
	m = sendText(t, m, "/help")
	if len(m.Messages()) != before {
		t.Fatal("/help is the help command, not the help skill")
	}
	if !transcriptContains(m, "/skill <name>") {
		t.Fatal("expected the help text in the transcript")
	}
}

func TestSkillCommand_QueuesWhileWorking(t *testing.T) {
	m := skillModel(t)
	m = sendText(t, m, "do the task")
	if m.state != stateStreaming {
		t.Fatalf("expected a working turn, got state %d", m.state)
	}
	m = sendText(t, m, "/skill documentation")
	if len(m.steering) != 1 || !skill.IsContent(m.steering[0]) {
		t.Fatalf("mid-turn activation should queue the content as steering, got %v", m.steering)
	}
}

func TestSkillCommand_UnknownAndListing(t *testing.T) {
	m := skillModel(t)
	m = sendText(t, m, "/skill nope")
	if !transcriptContains(m, "No skill named nope") {
		t.Fatal("an unknown skill is answered, not sent")
	}
	m = sendText(t, m, "/skills")
	if !transcriptContains(m, "documentation help") {
		t.Fatal("/skills prints the listing it was given")
	}

	bare := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream)
	updated, _ := bare.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	bare = sendText(t, updated.(Model), "/skills")
	if !transcriptContains(bare, "No skills loaded") {
		t.Fatal("a session without skills says so")
	}
}
