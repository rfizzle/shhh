package skill

import (
	"fmt"
	"strings"
)

// PromptBlock is the first tier of disclosure: every skill's name and
// description, with the instruction to activate one through the skill tool
// when a task matches. It names the tool, which the base prompts never do —
// but this block only exists when the tool was registered, so the promise
// holds. Empty when there are no skills: an empty catalog would be a
// section the model reads and can do nothing with.
func PromptBlock(c *Catalog) string {
	if c.Len() == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Skills\n")
	b.WriteString("The skills below are specialized instructions for specific kinds of task. ")
	b.WriteString("When a task matches a skill's description, call the " + ToolName + " tool with the skill's name before proceeding, and follow the instructions it returns. ")
	b.WriteString("Relative paths in a skill resolve against its directory, which the tool result names; use absolute paths when reading or running them.\n")
	b.WriteString("<available_skills>\n")
	for _, s := range c.Skills {
		fmt.Fprintf(&b, "  <skill>\n    <name>%s</name>\n    <description>%s</description>\n    <location>%s</location>\n  </skill>\n",
			escape(s.Name), escape(s.Description), escape(s.Location))
	}
	b.WriteString("</available_skills>")
	return b.String()
}

// contentOpen is how activated skill content begins, in the tool result and
// in a user-activated message alike; the tag is what lets the rest of the
// session tell skill instructions from any other text.
const contentOpen = "<skill_content name=\""

// IsContent reports whether a message body is activated skill content. It
// is what context trimming checks before eliding a tool result: a skill's
// instructions are guidance for the rest of the session, and losing them
// to trimming would degrade every later turn without any visible error.
// See docs/capabilities/skills.md#a-skill-is-read-in-three-tiers.
func IsContent(body string) bool {
	return strings.HasPrefix(body, contentOpen)
}

// Content renders the second tier: the skill's body wrapped in an
// identifying tag, followed by its directory and the files beside it —
// listed, not read, so the model loads what the instructions actually
// point at and nothing else.
func Content(s Skill) (string, error) {
	body, err := s.Body()
	if err != nil {
		return "", fmt.Errorf("skill %s: %w", s.Name, err)
	}
	var b strings.Builder
	b.WriteString(contentOpen + escape(s.Name) + "\">\n")
	b.WriteString(body)
	b.WriteString("\n\nSkill directory: " + s.Dir + "\n")
	b.WriteString("Relative paths in this skill are relative to the skill directory.")
	if s.Compatibility != "" {
		b.WriteString("\nCompatibility: " + s.Compatibility)
	}
	files, partial := Resources(s.Dir)
	if len(files) > 0 {
		b.WriteString("\n<skill_resources>\n")
		for _, f := range files {
			b.WriteString("  <file>" + escape(f) + "</file>\n")
		}
		if partial {
			b.WriteString("  <!-- listing truncated -->\n")
		}
		b.WriteString("</skill_resources>")
	}
	b.WriteString("\n</skill_content>")
	return b.String(), nil
}

// UserMessage is what a user's explicit activation sends the model: the
// skill content, then whatever the user typed after the name as the task.
// The content leads so the instruction reads in the light of the skill,
// which is how the user framed it.
func UserMessage(s Skill, task string) (string, error) {
	content, err := Content(s)
	if err != nil {
		return "", err
	}
	task = strings.TrimSpace(task)
	if task == "" {
		return content + "\n\nThe user activated the " + s.Name + " skill. Follow its instructions for what comes next.", nil
	}
	return content + "\n\n" + task, nil
}

func escape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
