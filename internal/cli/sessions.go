package cli

// The sessions running on this machine. The record already knows which rows
// are live — the process answers and the beat is inside the window — so the
// listing is that reading taken over every checkout at once rather than over
// this one, which is all the sibling notice asks.
// See docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone.

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/storage"
)

// runningSession is one row of the listing, and the one list both the report
// and --json are rendered from, so the screen and the file cannot come to
// hold different sessions.
type runningSession struct {
	// Slot is the saved conversation the session writes, empty until its
	// first save.
	Slot string `json:"slot"`
	Kind string `json:"kind"`
	PID  int    `json:"pid"`
	// Root is the checkout, read from the slot the session last saved; for
	// a session that has not saved yet it is known only when it is this
	// checkout, and empty otherwise.
	Root string `json:"root"`
	// Branch is read from the checkout now, not recorded.
	Branch   string         `json:"branch"`
	Started  time.Time      `json:"started"`
	State    string         `json:"state"`
	Own      bool           `json:"this_session"`
	Children []runningChild `json:"children"`
}

// runningChild is a sub-agent or a headless run, listed under its session.
type runningChild struct {
	Name    string    `json:"name"`
	Kind    string    `json:"kind"`
	Started time.Time `json:"started"`
	State   string    `json:"state"`
}

// sessionStates are the two words a listing says about a session.
const (
	sessionWorking = "working"
	sessionIdle    = "idle"
)

func sessionState(working bool) string {
	if working {
		return sessionWorking
	}
	return sessionIdle
}

// runningSessions reads the live rows and says where each one stands. The
// checkout comes from the slot, because the record stores no paths; a
// session that has not saved yet has no slot to say it, and the one thing
// that can still name its checkout is the fingerprint matching the one this
// process is standing in.
func runningSessions(db *storage.DB, now time.Time) ([]runningSession, error) {
	live, err := db.LiveSessions(now)
	if err != nil {
		return nil, err
	}
	here := fingerprint(projectFingerprintRoot())
	cwd, _ := os.Getwd()
	out := make([]runningSession, 0, len(live))
	for _, s := range live {
		root := s.Root
		if root == "" && here != "" && s.Project == here && cwd != "" {
			root = project.Root(cwd)
		}
		row := runningSession{
			Slot: s.Slot, Kind: s.Kind, PID: s.PID, Root: root,
			Started: s.Started, State: sessionState(s.Working), Own: s.Own,
			Children: make([]runningChild, 0, len(s.Children)),
		}
		if root != "" {
			row.Branch = project.Branch(root)
		}
		for _, c := range s.Children {
			row.Children = append(row.Children, runningChild{
				Name: c.Name, Kind: c.Kind, Started: c.Started, State: sessionState(c.Working)})
		}
		out = append(out, row)
	}
	return out, nil
}

// sessionsReport is the listing as text: one row per session a person can
// open — its kind, the slot it saves to, when it started — with where it is
// standing on the line beneath and its children under that. The checkout is
// body rather than target because a path is the longest thing on the row and
// the one a reader needs whole; a clipped one names no directory at all. The
// outcome carries the state and the mark on this process's own row, because
// it is the field that never clips.
func sessionsReport(sessions []runningSession, now time.Time) report.Report {
	r := report.Report{Title: "shhh sessions", Subject: countOf(len(sessions), "session", "sessions")}
	if len(sessions) == 0 {
		return emptyInto(r, "no session running on this machine", "shhh code")
	}
	rows := make([]report.Row, 0, len(sessions))
	for _, s := range sessions {
		row := report.Row{
			State:   report.Pass,
			Name:    s.Kind,
			Subject: s.Slot,
			Detail:  "started " + historyAgo(s.Started, now),
			Outcome: s.State,
		}
		if s.Slot == "" {
			row.Subject = "not saved yet"
		}
		if s.State == sessionWorking {
			row.State = report.Run
		}
		if s.Own {
			row.Outcome += " · this session"
		}
		where := "checkout unknown until it saves"
		if s.Root != "" {
			where = joinDetail(project.Abbreviate(s.Root), s.Branch)
		}
		row.Body = append(row.Body, where)
		for _, c := range s.Children {
			name := c.Name
			if name == "" {
				name = c.Kind + " run"
			}
			row.Body = append(row.Body, fmt.Sprintf("↳ %s · %s · started %s", name, c.State, historyAgo(c.Started, now)))
		}
		rows = append(rows, row)
	}
	r.Sections = []report.Section{{Rows: rows}}
	return r
}

// sessionsListing is /sessions: the same report, as the row a slash command
// leaves in the transcript.
func sessionsListing(db *storage.DB) string {
	now := time.Now()
	sessions, err := runningSessions(db, now)
	if err != nil {
		return "The sessions on this machine could not be read: " + err.Error()
	}
	return sessionsReport(sessions, now).String()
}

// sessionsFor is what a session hands its chat model for /sessions: the
// listing over the session's own store, read when it is asked because
// sessions come and go under this one — or nil without a store.
func sessionsFor(db *storage.DB) func() string {
	if db == nil {
		return nil
	}
	return func() string { return sessionsListing(db) }
}

// newSessionsCmd is `shhh sessions`.
func newSessionsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "List the sessions running on this machine",
		Long: "List every chat and coding session running on this machine: the conversation it is saving to, " +
			"the checkout and branch it is standing in, when it started and whether it is working or idle. " +
			"Sub-agents and unattended runs are listed under the session that started them.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()
			now := time.Now()
			sessions, err := runningSessions(db, now)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd, sessions)
			}
			return report.Fprint(cmd.OutOrStdout(), sessionsReport(sessions, now))
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the list as a JSON array")
	return cmd
}
