package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/ui/golden"
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
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream, Wiring{
		Skills:     skillCatalog(t),
		SkillsList: func(c *skill.Catalog) string { return strings.Join(c.Names(), " ") },
	})
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
	m = sendText(t, m, "/help commands")
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
	if len(m.steering) != 1 || !skill.IsContent(m.steering[0].text) {
		t.Fatalf("mid-turn activation should queue the content as steering, got %v", m.steering)
	}
}

func TestSkillCommand_UnknownAndListing(t *testing.T) {
	m := skillModel(t)
	m = sendText(t, m, "/skill nope")
	if !transcriptContains(m, "no skill named nope") {
		t.Fatal("an unknown skill is answered, not sent")
	}
	m = sendText(t, m, "/skills")
	if !transcriptContains(m, "documentation help") {
		t.Fatal("/skills prints the listing it was given")
	}

	bare := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream, Wiring{})
	updated, _ := bare.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	bare = sendText(t, updated.(Model), "/skills")
	if !transcriptContains(bare, "no skills loaded") {
		t.Fatal("a session without skills says so")
	}
}

// builtinListing is a session's catalog holding one user skill and the
// built-ins, and what /skills prints for it: the command's own fixture,
// read rather than drawn again here, so the screen is checked against the
// listing the command really prints (internal/cli renders it, and this
// package cannot import that one).
func builtinListing(t *testing.T) (*skill.Catalog, func(*skill.Catalog) string) {
	t.Helper()
	listing, err := os.ReadFile(filepath.Join("..", "..", "cli", "testdata", "report", "skills.txt"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "changelog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: changelog\ndescription: Use when writing the release notes for a tagged version.\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := skill.Load([]skill.Root{{Path: root, Scope: skill.ScopeUser}})
	return c, func(*skill.Catalog) string { return strings.TrimRight(string(listing), "\n") }
}

// TestGolden_SkillsBuiltin captures /skills in a session whose catalog
// carries the binary's own skills after the user's, as the transcript draws
// the listing.
func TestGolden_SkillsBuiltin(t *testing.T) {
	c, list := builtinListing(t)
	captureGolden(t, "skills-builtin", "/skills with the built-in skills", goldenWidths, func(width int) []golden.Panel {
		m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream, Wiring{Skills: c, SkillsList: list})
		updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		m = sendText(t, updated.(Model), "/skills")
		return []golden.Panel{{Label: "a user skill, then the two the binary carries",
			View: strings.Join(m.renderHistoryLines(), "\n")}}
	})
}

// /skills reaches the listing through the real program with the built-ins
// on it. The scene `skills-builtin` is the same route in a terminal.
func TestProgram_SkillsListsTheBuiltins(t *testing.T) {
	c, list := builtinListing(t)
	tm := runProgramAt(t, New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream,
		Wiring{Skills: c, SkillsList: list}), 110, 40)
	tm.Type("/skills")
	tm.Send(programEnter)
	waitForText(t, tm, "shhh-quality-gate")
	if frame := finalFrame(t, tm); !strings.Contains(frame, "[built-in]") {
		t.Errorf("the built-in rows carry their scope word:\n%s", frame)
	}
}
