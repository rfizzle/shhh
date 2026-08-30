package cli

// Skills wiring: a session discovers its skills once at startup, tells the
// model their names and descriptions, and registers the activation tool.
// `shhh skills` is the same catalog printed for the user, diagnostics
// included — the place to learn why a skill did not load.

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/spf13/cobra"
)

// loadSkills discovers the skills a session opened in the working directory
// can see. Nil when there are none, so callers register nothing — a skill
// tool with an empty enum, or a Skills section with no skills, is a promise
// the model would try to keep.
func loadSkills() *skill.Catalog {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	c := skill.Discover(skill.Roots(cwd, config.SkillDirs()))
	if c.Len() == 0 && len(c.Diagnostics) == 0 {
		return nil
	}
	return c
}

// skillsListing is the /skills answer and the `shhh skills` output: every
// skill with its scope and where it was read from, then the diagnostics.
func skillsListing(c *skill.Catalog) string {
	if c.Len() == 0 && (c == nil || len(c.Diagnostics) == 0) {
		return "No skills found. A skill is a directory holding a SKILL.md, under .shhh/skills, .agents/skills or .claude/skills in the project, or under " +
			strings.Join(config.SkillDirs(), " or ") + ", ~/.agents/skills or ~/.claude/skills."
	}
	var b strings.Builder
	if c.Len() > 0 {
		tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		for _, s := range c.Skills {
			desc := clipRunes(s.Description, 96)
			fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Name, s.Scope, desc)
		}
		tw.Flush()
		fmt.Fprintf(&b, "\n%d skill(s). /skill <name> [task] activates one now; the model activates them itself when a task matches.", c.Len())
		for _, s := range c.Skills {
			for _, w := range s.Warnings {
				fmt.Fprintf(&b, "\nwarning: %s: %s", s.Location, w)
			}
		}
	}
	for _, d := range c.Diagnostics {
		fmt.Fprintf(&b, "\n%s", d)
	}
	return strings.TrimRight(b.String(), "\n")
}

// skillDetail is `shhh skills show <name>`: the frontmatter as read, and
// where the body is.
func skillDetail(s skill.Skill) string {
	var b strings.Builder
	fmt.Fprintf(&b, "name:          %s\nscope:         %s\nlocation:      %s\ndescription:   %s\n", s.Name, s.Scope, s.Location, s.Description)
	if s.License != "" {
		fmt.Fprintf(&b, "license:       %s\n", s.License)
	}
	if s.Compatibility != "" {
		fmt.Fprintf(&b, "compatibility: %s\n", s.Compatibility)
	}
	if s.AllowedTools != "" {
		fmt.Fprintf(&b, "allowed-tools: %s (informational; shhh grants nothing from it)\n", s.AllowedTools)
	}
	for k, v := range s.Metadata {
		fmt.Fprintf(&b, "metadata.%s: %s\n", k, v)
	}
	files, partial := skill.Resources(s.Dir)
	if len(files) > 0 {
		fmt.Fprintf(&b, "resources:     %s", strings.Join(files, ", "))
		if partial {
			b.WriteString(", ...")
		}
		b.WriteString("\n")
	}
	for _, w := range s.Warnings {
		fmt.Fprintf(&b, "warning:       %s\n", w)
	}
	return strings.TrimRight(b.String(), "\n")
}

// clipRunes shortens s to at most n runes with an ellipsis — runes, because
// a description cut on a byte boundary ends in half a character.
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}

func newSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "List the skills a session here would load",
		Long:  "List the Agent Skills (SKILL.md directories) visible from the current directory, project and user scope, with why any of them failed to load.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), skillsListing(loadSkills()))
			return nil
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show <name>",
		Short: "Show a skill's frontmatter, location and bundled files",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, ok := loadSkills().Find(args[0])
			if !ok {
				return fmt.Errorf("no skill named %q; `shhh skills` lists them", args[0])
			}
			fmt.Fprintln(cmd.OutOrStdout(), skillDetail(s))
			return nil
		},
	})
	return cmd
}
