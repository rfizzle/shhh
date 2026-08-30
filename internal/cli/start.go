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

// buildStartInfo surveys the workspace for the start screen. Every source is
// optional: a missing gate config, an unavailable database, or a directory
// that cannot be surveyed each cost their own clause and nothing else.
func buildStartInfo(db *storage.DB, gateEnabled bool) chat.StartInfo {
	wd, err := os.Getwd()
	if err != nil {
		wd = ""
	}
	info := chat.StartInfo{Project: project.Survey(wd)}
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
