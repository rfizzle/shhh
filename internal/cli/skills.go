package cli

// Skills wiring: a session discovers its skills once at startup, tells the
// model their names and descriptions, and registers the activation tool.
// `shhh skills` is the same catalog printed for the user, diagnostics
// included — the place to learn why a skill did not load.

import (
	"fmt"
	"os"
	"strings"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/spf13/cobra"
)

// loadSkills discovers the skills a session opened in the working directory
// can see. Nil when there are none, so callers register nothing — a skill
// tool with an empty enum, or a Skills section with no skills, is a promise
// the model would try to keep.
//
// An untrusted checkout contributes none of its own: the user's skills are
// still there, and the withheld list on the start screen says what was left
// out rather than letting a session look as though the repository had
// written nothing.
func loadSkills() *skill.Catalog {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	c := skill.Discover(skill.Roots(cwd, config.SkillDirs(), projectTrust().Allows()))
	if c.Len() == 0 && len(c.Diagnostics) == 0 {
		return nil
	}
	return c
}

// skillsListing is the /skills answer and the `shhh skills` output: every
// skill with its scope and where it was read from, then the diagnostics.
func skillsListing(c *skill.Catalog) string {
	return skillsReport(c).String()
}

// skillsReport is the catalog as a report. A skill that would not load is a
// note rather than a line printed after the listing: it is the reason
// somebody ran this command, and it should read in the same voice as
// everything else on the screen.
func skillsReport(c *skill.Catalog) report.Report {
	r := report.Report{Title: "shhh skills"}
	if c == nil || (c.Len() == 0 && len(c.Diagnostics) == 0) {
		// The way out is short enough to survive a narrow terminal and the
		// places that were looked in go on the lines under it, because a
		// reader with no skills needs the whole search order and a clipped
		// row would give them half of it.
		empty := report.Empty("no skills found", "a skill is a directory holding a SKILL.md")
		empty.Body = []string{
			"in the project, once you trust the checkout: .shhh/skills, .agents/skills, .claude/skills",
			"for you: " + strings.Join(config.SkillDirs(), ", ") + ", ~/.agents/skills, ~/.claude/skills",
		}
		r.Sections = append(r.Sections, report.Section{Rows: []report.Row{empty}})
		return r
	}
	r.Subject = countOf(c.Len(), "skill", "skills")
	rows := make([]report.Row, 0, c.Len())
	for _, sk := range c.Skills {
		rows = append(rows, report.Row{
			State: report.Pass, Name: sk.Name,
			Subject: clipRunes(sk.Description, 96), Outcome: string(sk.Scope),
		})
		for _, w := range sk.Warnings {
			r.Notes = append(r.Notes, report.Note{State: report.Warn, Text: sk.Location + ": " + w})
		}
	}
	if len(rows) > 0 {
		r.Sections = []report.Section{{Rows: rows}}
		r.Tally = "/skill <name> [task] activates one now; the model activates them itself when a task matches"
	}
	for _, d := range c.Diagnostics {
		r.Notes = append(r.Notes, report.Note{State: report.Fail, Text: d})
	}
	return r
}

// skillDetail is `shhh skills show <name>`: the frontmatter as read, and
// where the body is.
func skillDetail(s skill.Skill) string {
	r := report.Report{Title: "shhh skills " + s.Name, Subject: string(s.Scope)}
	pairs := []report.Pair{
		{Key: "location", Value: s.Location},
		{Key: "description", Value: s.Description},
	}
	for _, p := range []report.Pair{
		{Key: "license", Value: s.License},
		{Key: "compatibility", Value: s.Compatibility},
		{Key: "allowed-tools", Value: s.AllowedTools},
	} {
		if p.Value != "" {
			pairs = append(pairs, p)
		}
	}
	if s.AllowedTools != "" {
		r.Notes = append(r.Notes, report.Note{State: report.Skip,
			Text: "allowed-tools is informational; shhh grants nothing from it"})
	}
	for k, v := range s.Metadata {
		pairs = append(pairs, report.Pair{Key: "metadata." + k, Value: v})
	}
	if files, partial := skill.Resources(s.Dir); len(files) > 0 {
		value := strings.Join(files, ", ")
		if partial {
			value += ", …"
		}
		pairs = append(pairs, report.Pair{Key: "resources", Value: value})
	}
	for _, w := range s.Warnings {
		r.Notes = append(r.Notes, report.Note{State: report.Warn, Text: w})
	}
	r.Sections = []report.Section{{Pairs: pairs}}
	return r.String()
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
			return report.Fprint(cmd.OutOrStdout(), skillsReport(loadSkills()))
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
