package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/spf13/cobra"
)

// The reading of what repeats across this checkout's sessions: the files read
// in session after session, the commands a person was asked about in session
// after session, and the suites that failed before they passed in session
// after session. It is arithmetic over the record and the conversations this
// machine saved, with no model, and it writes nothing: it is a report for
// the person (docs/capabilities/sessions-and-memory.md#what-repeats-is-counted).

// observePatternsMinSessions is how many distinct sessions a thing has to
// have happened in before it is a pattern rather than a coincidence, unless
// the person says otherwise. Three is the smallest count where "every time"
// and "twice" are different claims.
const observePatternsMinSessions = 3

// observePatterns is the three tables of one reading, with the part of each
// that no conversation could name. It is the value a proposal is made from,
// so a reader of the patterns takes this rather than the SQL beneath it.
type observePatterns struct {
	Window      string
	MinSessions int
	// Files is each path read, searched or globbed in at least MinSessions
	// sessions; FilesUnjoined is the reads whose conversation is gone.
	Files         []storage.AgentFilePattern
	FilesUnjoined storage.AgentUnjoined
	// Commands is each command a person was asked about in at least
	// MinSessions sessions, by its first two words; CommandsUnjoined is the
	// asks that could not be put to one command.
	Commands         []storage.AgentCommandPattern
	CommandsUnjoined storage.AgentUnjoined
	// Suites is each gate suite that failed before it passed in at least
	// MinSessions sessions.
	Suites []storage.AgentSuitePattern
}

// readObservePatterns runs the three readings for one checkout. A checkout
// with no fingerprint — the working directory could not be read — has no
// sessions to read, and answers with none rather than with every session
// that was stamped with no checkout.
func readObservePatterns(db *storage.DB, window string, since time.Time, project string, minSessions int) (observePatterns, error) {
	p := observePatterns{Window: window, MinSessions: minSessions}
	if project == "" {
		return p, nil
	}
	var err error
	if p.Files, p.FilesUnjoined, err = db.AgentFilePatterns(since, project, minSessions); err != nil {
		return observePatterns{}, fmt.Errorf("query file patterns: %w", err)
	}
	if p.Commands, p.CommandsUnjoined, err = db.AgentCommandPatterns(since, project, minSessions); err != nil {
		return observePatterns{}, fmt.Errorf("query command patterns: %w", err)
	}
	if p.Suites, err = db.AgentSuitePatterns(since, project, minSessions); err != nil {
		return observePatterns{}, fmt.Errorf("query suite patterns: %w", err)
	}
	return p, nil
}

// newObservePatternsCmd is `observe patterns`. It takes its window from
// `observe`, the way the comparison does.
func newObservePatternsCmd(window *string) *cobra.Command {
	var minSessions int
	cmd := &cobra.Command{
		Use:   "patterns",
		Short: "Count what repeats across this checkout's sessions",
		Long: "For this checkout's sessions in the window, list the files read, the commands you were asked about and the gate suites that failed first, " +
			"each in at least --min-sessions distinct sessions. The paths and commands are read from the conversations this machine saved; nothing is written.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if minSessions < 1 {
				return fmt.Errorf("invalid --min-sessions %d (use 1 or more)", minSessions)
			}
			since, err := parseObserveWindow(*window)
			if err != nil {
				return err
			}
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()
			p, err := readObservePatterns(db, *window, since, fingerprint(projectFingerprintRoot()), minSessions)
			if err != nil {
				return err
			}
			return report.Fprint(cmd.OutOrStdout(), renderObservePatterns(p))
		},
	}
	cmd.Flags().IntVar(&minSessions, "min-sessions", observePatternsMinSessions,
		"how many distinct sessions a thing must have happened in to be listed")
	return cmd
}

// renderObservePatterns is the reading as a report: one section per table,
// each row led by the sessions it happened in, then what no conversation
// could name. It is a pure function of the tables, so the whole page is held
// against a fixture.
func renderObservePatterns(p observePatterns) report.Report {
	r := report.Report{Title: "shhh observe patterns",
		Subject: fmt.Sprintf("this checkout · last %s · in %d or more sessions", p.Window, p.MinSessions)}

	var files, commands, suites []report.Row
	for _, f := range p.Files {
		files = append(files, report.Row{State: report.Pass, Name: countOf(f.Sessions, "session", "sessions"),
			Subject: f.Path, Detail: observePerSession(f.Calls, f.Sessions)})
	}
	for _, c := range p.Commands {
		commands = append(commands, report.Row{State: report.Pass, Name: countOf(c.Sessions, "session", "sessions"),
			Subject: c.Command, Detail: "asked " + countOf(c.Asked, "time", "times"),
			Outcome: fmt.Sprintf("allowed %d of %d", c.Allowed, c.Asked)})
	}
	for _, s := range p.Suites {
		suites = append(suites, report.Row{State: report.Warn, Name: countOf(s.Sessions, "session", "sessions"),
			Subject: s.Suite, Detail: "failed before it passed",
			Outcome: "of " + countOf(s.Ran, "session", "sessions") + " that ran it"})
	}
	for _, s := range []report.Section{
		{Header: "FILES READ", Rows: files},
		{Header: "COMMANDS ASKED ABOUT", Rows: commands},
		{Header: "SUITES THAT FAILED FIRST", Rows: suites},
	} {
		if len(s.Rows) > 0 {
			r.Sections = append(r.Sections, s)
		}
	}
	if len(r.Sections) == 0 {
		r = emptyInto(r, fmt.Sprintf("nothing repeated in %d or more sessions", p.MinSessions), "--min-sessions 2")
	}

	// A pruned conversation is a count without a path, and is said rather
	// than dropped: dropped, a checkout that keeps its record longer than its
	// conversations would read as one that repeats itself less.
	if u := p.FilesUnjoined; u.Events > 0 {
		r.Notes = append(r.Notes, report.Note{State: report.Skip, Text: fmt.Sprintf(
			"%s in %s had no conversation to name the file — pruned, or never kept, so counted without a path",
			countOf(u.Events, "read", "reads"), countOf(u.Sessions, "session", "sessions"))})
	}
	if u := p.CommandsUnjoined; u.Events > 0 {
		r.Notes = append(r.Notes, report.Note{State: report.Skip, Text: fmt.Sprintf(
			"%s in %s could not be put to one command — no conversation for the round, or it asked for more than one call",
			countOf(u.Events, "ask", "asks"), countOf(u.Sessions, "session", "sessions"))})
	}
	r.Notes = append(r.Notes, report.Note{State: report.Run,
		Text: "counted from the record and this machine's saved conversations; nothing was written"})
	return r
}

// observePerSession is a count's mean over the sessions it happened in, to
// one decimal where it is not whole.
func observePerSession(calls, sessions int) string {
	if sessions == 0 {
		return ""
	}
	mean := strings.TrimSuffix(strconv.FormatFloat(float64(calls)/float64(sessions), 'f', 1, 64), ".0")
	if mean == "1" {
		return "1 call a session"
	}
	return mean + " calls a session"
}

// observePatternsLine is the dashboard's one line on what repeats: the top
// entry of each table that has one, and the way in. Nothing at all where no
// table has an entry, as the quiet line draws nothing without a stretch.
func observePatternsLine(p observePatterns) []report.Note {
	var parts []string
	if len(p.Files) > 0 {
		parts = append(parts, fmt.Sprintf("%s read in %d sessions", p.Files[0].Path, p.Files[0].Sessions))
	}
	if len(p.Commands) > 0 {
		parts = append(parts, fmt.Sprintf("`%s` asked about in %d", p.Commands[0].Command, p.Commands[0].Sessions))
	}
	if len(p.Suites) > 0 {
		parts = append(parts, fmt.Sprintf("%s failed first in %d", p.Suites[0].Suite, p.Suites[0].Sessions))
	}
	if len(parts) == 0 {
		return nil
	}
	return []report.Note{{State: report.Run, Text: "patterns · " + strings.Join(parts, " · ") +
		" · `shhh observe patterns` lists them"}}
}

// patternTranscriptSessions bounds how many of the window's newest sessions
// the transcript readings below open. Each is one read of a conversation, and
// a habit that only shows past the newest few hundred sessions is not the one
// a proposal is for.
const patternTranscriptSessions = 300

// patternTranscripts is what the saved conversations of a checkout's sessions
// hold for the readings a count cannot answer: each session's commands in the
// order it ran them, and the calls that read each path, as the conversation
// wrote them. A session whose conversation is gone has neither, which is the
// rule the tables keep: a pattern with no transcript to join is a count and
// not a path.
type patternTranscripts struct {
	// Commands is each session's command lines, in order, newest session
	// first.
	Commands [][]string
	// Reads is the read, search and glob calls by the path they named, each
	// as the tool and its arguments.
	Reads map[string][]string
}

// readPatternTranscripts reads the conversations of the window's sessions in
// one checkout.
func readPatternTranscripts(db *storage.DB, since time.Time, project string) (patternTranscripts, error) {
	t := patternTranscripts{Reads: map[string][]string{}}
	if project == "" {
		return t, nil
	}
	sessions, err := db.AgentSessions(since, patternTranscriptSessions)
	if err != nil {
		return t, fmt.Errorf("list sessions: %w", err)
	}
	for _, s := range sessions {
		if s.Project != project || s.ChatSessionID == nil || s.ParentID != nil {
			continue
		}
		calls, err := db.AgentSessionCalls(s.ID)
		if err != nil {
			return t, fmt.Errorf("read session %d: %w", s.ID, err)
		}
		var commands []string
		for _, c := range calls {
			var args struct {
				Command string `json:"command"`
				Path    string `json:"path"`
			}
			_ = json.Unmarshal([]byte(c.Args), &args) // a call that is not JSON names nothing
			switch c.Tool {
			case "execute_command":
				if line := strings.TrimSpace(args.Command); line != "" {
					commands = append(commands, line)
				}
			case "read_file", "search", "glob":
				path := args.Path
				if path == "" && c.Tool != "read_file" {
					path = "."
				}
				if path != "" {
					t.Reads[path] = append(t.Reads[path], c.Tool+" "+c.Args)
				}
			}
		}
		if len(commands) > 0 {
			t.Commands = append(t.Commands, commands)
		}
	}
	return t, nil
}

// commandKey is a command as the command table keys it: its first two words.
func commandKey(line string) string {
	f := strings.Fields(line)
	return strings.Join(f[:min(len(f), 2)], " ")
}

// commandSequence is three commands run in the same order in at least the
// threshold's sessions: their keys, the lines the first session to run them
// wrote, and how many sessions did.
type commandSequence struct {
	Keys     []string
	Lines    []string
	Sessions int
}

// maxCommandSequences bounds how many sequences a reading proposes: a skill
// is a file in the checkout, and the habits worth one are few.
const maxCommandSequences = 3

// commandSequences is the runs of three commands, by their keys, that the
// sessions ran in the same order in at least minSessions sessions. A command
// run twice running counts once, so a retried test is not a step of its own;
// a run that shares two neighbouring commands with one already kept is the
// same habit seen one step along and is dropped, the most repeated kept.
func commandSequences(sessions [][]string, minSessions int) []commandSequence {
	type seen struct {
		lines    []string
		sessions int
	}
	counts := map[string]*seen{}
	var order []string
	for _, lines := range sessions {
		var keys, kept []string
		for _, l := range lines {
			k := commandKey(l)
			if len(keys) > 0 && keys[len(keys)-1] == k {
				continue
			}
			keys, kept = append(keys, k), append(kept, l)
		}
		once := map[string]bool{}
		for i := 0; i+3 <= len(keys); i++ {
			id := strings.Join(keys[i:i+3], "\x00")
			if once[id] {
				continue
			}
			once[id] = true
			if counts[id] == nil {
				counts[id] = &seen{lines: kept[i : i+3]}
				order = append(order, id)
			}
			counts[id].sessions++
		}
	}
	var out []commandSequence
	for _, id := range order {
		if c := counts[id]; c.sessions >= minSessions {
			out = append(out, commandSequence{Keys: strings.Split(id, "\x00"), Lines: c.lines, Sessions: c.sessions})
		}
	}
	slices.SortStableFunc(out, func(a, b commandSequence) int {
		if a.Sessions != b.Sessions {
			return b.Sessions - a.Sessions
		}
		return strings.Compare(strings.Join(a.Keys, " "), strings.Join(b.Keys, " "))
	})
	pairs := map[string]bool{}
	var kept []commandSequence
	for _, s := range out {
		p1, p2 := s.Keys[0]+"\x00"+s.Keys[1], s.Keys[1]+"\x00"+s.Keys[2]
		if pairs[p1] || pairs[p2] {
			continue
		}
		pairs[p1], pairs[p2] = true, true
		kept = append(kept, s)
		if len(kept) == maxCommandSequences {
			break
		}
	}
	return kept
}
