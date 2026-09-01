package cli

// First-contact facts. Everything the empty session's start screen
// states is gathered here, once, while the session is being built: the
// project survey, the quality-gate configuration in effect, and the most
// recent saved session. Nothing on that screen is computed again while it is
// on the terminal.

import (
	"os"

	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// buildStartInfo assembles the start screen from the survey the session
// already took. Every source is optional: a missing gate config or an
// unavailable database costs its own clause and nothing else.
//
// The survey is handed in rather than taken here because the model's prompt
// block is built from the same answer, and the tree walk behind it is not
// worth doing twice.
func buildStartInfo(survey project.Info, db *storage.DB, gateEnabled bool) chat.StartInfo {
	wd := survey.Dir
	info := chat.StartInfo{Project: survey}
	if gateEnabled {
		info.Gate = startGate(wd)
	}
	if db != nil {
		if recent, ok, err := db.MostRecentChat(); err == nil && ok {
			info.Recent = chat.StartRecent{
				Present: true,
				Name:    recent.Name,
				Title:   recent.Title,
				Turns:   recent.Turns,
				Updated: recent.UpdatedAt,
				Cost:    recent.Cost,
				Priced:  recent.Priced,
			}
		}
	}
	return info
}

// scaffoldOffer names the offer in the store. It is a value in a table, so
// it is a constant rather than a phrase: the wording of the row on screen is
// free to change without forgetting who already said no.
const scaffoldOffer = "scaffold"

// buildScaffold wires the start screen's scaffolding offer. The offer is
// answered here, once: a checkout that can take it, and no refusal already
// on record for it (docs/interface/surfaces.md#the-start-screen). Without a
// store the refusal has nowhere to live, so nothing is offered — an offer
// that cannot be refused for good is a nag.
func buildScaffold(db *storage.DB) chat.Scaffold {
	wd, err := os.Getwd()
	if err != nil {
		return chat.Scaffold{}
	}
	// The repository root, not the working directory: the offer is the
	// project's and so is the answer, and a session started two directories
	// down that scaffolded where it stood would put a second context file
	// nearer than the project's own — which the nearest-first read would
	// then prefer. `shhh init --project` still writes where it is told,
	// because a flag that says "the current directory" has to mean it.
	root := project.Root(wd)
	s := chat.Scaffold{
		Paths: project.ScaffoldPaths(root, wd),
		Write: func() (string, error) { return project.Scaffold(root) },
	}
	if db == nil {
		return s
	}
	s.Decline = func() error { return db.DeclineOffer(root, scaffoldOffer) }
	s.Offer = project.NeedsScaffold(root) && !db.OfferDeclined(root, scaffoldOffer)
	return s
}

// startGate reads the workspace's quality config for the screen's gate line.
// A config that exists but does not load is reported as unreadable rather
// than as absent — a broken gate is the more urgent of the two.
func startGate(workspace string) chat.StartGate {
	g := chat.StartGate{Path: quality.ConfigRelPath}
	cfg, err := quality.LoadConfig(workspace)
	if err != nil {
		if !os.IsNotExist(err) {
			g.Err = err.Error()
		}
		return g
	}
	names := cfg.SuiteNames()
	g.Suites = len(names)
	suite := quality.DefaultSuite
	if _, ok := cfg.Suites[suite]; !ok && len(names) > 0 {
		suite = names[0]
	}
	s, ok := cfg.Suites[suite]
	if !ok {
		return g
	}
	g.Suite = suite
	for _, c := range s.Checks {
		g.Checks = append(g.Checks, c.Name)
	}
	return g
}
