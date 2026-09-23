package cli

// `shhh agents` is the roles a coding session here would spawn, printed for
// the user the way `shhh skills` prints the skills: what each is called,
// what it is for, and where the file that says so lives. It reads the same
// load a session does, so a profile that would stop a session stops this
// command with the same sentence.

import (
	"os"
	"slices"
	"strings"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/spf13/cobra"
)

// agentsReport is the roles as a report. withheld says the checkout carries
// profiles of its own that were not read because nobody has trusted it: the
// listing is then missing rows a trusted session would have, and a reader
// comparing it with the repository deserves to know why.
func agentsReport(roles []chat.SpawnableRole, withheld bool) report.Report {
	r := report.Report{Title: "shhh agents"}
	if withheld {
		r.Notes = append(r.Notes, report.Note{State: report.Skip,
			Text: "this checkout's agent profiles are withheld until you trust it: `shhh trust`"})
	}
	if len(roles) == 0 {
		empty := report.Empty("no roles found", "a profile is a TOML file named for its role")
		empty.Body = []string{
			"in the project, once you trust the checkout: .shhh/agents",
			"for you: " + strings.Join(config.AgentDirs(), ", "),
		}
		r.Sections = append(r.Sections, report.Section{Rows: []report.Row{empty}})
		return r
	}
	r.Subject = countOf(len(roles), "role", "roles")
	rows := make([]report.Row, 0, len(roles))
	for _, role := range roles {
		rows = append(rows, report.Row{
			State: report.Pass, Name: role.Name,
			Subject: clipRunes(role.Description, 96), Outcome: role.Scope,
		})
	}
	r.Sections = []report.Section{{Rows: rows}}
	r.Tally = "a session spawns one by name; a file of the same name replaces a built-in"
	return r
}

// agentsListing reads the roles a coding session opened in cwd would have.
// A coding session and not a conversation, because it is the one that can
// spawn every role; a conversation's set is the readers among these.
func agentsListing(cwd string) (report.Report, error) {
	profiles, err := loadAgentProfilesIn(cwd, true)
	if err != nil {
		return report.Report{}, err
	}
	withheld := slices.Contains(projectTrust().Withheld(), project.KindAgents)
	return agentsReport(profiles.roles(cwd), withheld), nil
}

func newAgentsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agents",
		Short: "List the roles a session here would spawn",
		Long: "List the sub-agent roles a coding session opened in the current directory could spawn: " +
			"the built-in ones and the profiles read from .shhh/agents and the agents directory beside the config file.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				cwd = "."
			}
			r, err := agentsListing(cwd)
			if err != nil {
				return err
			}
			return report.Fprint(cmd.OutOrStdout(), r)
		},
	}
}
