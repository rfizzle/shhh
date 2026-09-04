package chat

// Session branching and rewind. A checkpoint marks the start of each
// user turn; /rewind truncates the working conversation back to a chosen
// checkpoint and preserves the abandoned tail as a branch session in storage,
// parent-linked to the current session. /branches opens a picker over the
// current session's branch family and switches between branches. Rewind
// restores conversation state only — files on disk are untouched, and the
// rewind message says so — and each checkpoint records the git HEAD + dirty
// status at the time so the message can show what diverged since.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// GitSnapshot is the workspace's git state at a checkpoint: HEAD plus a
// digest of the porcelain status. The zero value means "not a git repo".
type GitSnapshot struct {
	Repo       bool
	Head       string
	StatusHash string
	DirtyPaths int
}

// checkpoint marks where one user turn starts in the conversation.
type checkpoint struct {
	index   int    // conversation index of the turn's user message
	preview string // first line of the user text
	git     GitSnapshot
	hasGit  bool // a snapshot function was wired when this was recorded
}

// WithGitSnapshots wires the git-state capture recorded on each rewind
// checkpoint; nil (the default) records no git state.
func (m Model) WithGitSnapshots(fn func() GitSnapshot) Model {
	m.gitSnapshot = fn
	return m
}

// recordCheckpoint marks the start of a user turn. Call it before the user
// message joins the conversation, so the checkpoint index points at it.
func (m *Model) recordCheckpoint(text string) {
	cp := checkpoint{index: len(m.agent.Messages()), preview: firstLine(text)}
	if m.gitSnapshot != nil {
		cp.git = m.gitSnapshot()
		cp.hasGit = true
	}
	m.checkpoints = append(m.checkpoints, cp)
}

// checkpointsFromMessages derives checkpoints from a stored conversation:
// every user message is a rewind point. Git snapshots are unknown for rebuilt
// checkpoints — the rewind message says so instead of guessing.
func checkpointsFromMessages(msgs []provider.Message) []checkpoint {
	var cps []checkpoint
	for i, msg := range msgs {
		if msg.Role == provider.RoleUser {
			cps = append(cps, checkpoint{index: i, preview: firstLine(msg.Content)})
		}
	}
	return cps
}

// openRewindPick opens the interactive /rewind picker over the recorded
// checkpoints, latest first.
//
// It is a card of the selector family and not one of its own: a list of turns
// is a catalog, and the first thing a reader looking for one does is name
// what they were doing — so it opens with the query row the family's search
// cards open with, and the terminal's own cursor sits on it
// (docs/interface/surfaces.md#selectors). Its own Select was the same card
// with none of that: no filter, no cursor, and a second key handler and a
// second render to keep in step with the family's.
func (m Model) openRewindPick() (tea.Model, tea.Cmd) {
	if len(m.checkpoints) == 0 {
		m.appendEntry(entry{kind: entrySystem, text: "No checkpoints to rewind to yet."})
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		return m, nil
	}
	opts := make([]components.SelectOption, 0, len(m.checkpoints))
	for i := len(m.checkpoints) - 1; i >= 0; i-- {
		cp := m.checkpoints[i]
		opts = append(opts, components.SelectOption{
			Label: fmt.Sprintf("turn %d · %s", i+1, cp.preview),
			Desc:  m.checkpointGitDesc(cp),
		})
	}
	return m.openSearchPicker("Rewind to before which turn? (the abandoned tail is kept as a branch)",
		opts, 0, func(m *Model, idx int) (string, tea.Cmd) {
			// The options are latest-first, so the row is counted back from
			// the end rather than into it.
			turn := len(m.checkpoints) - idx
			// The conversation this session would be reopened on has just
			// changed shape, so the slot is written before anything else can
			// be added to it.
			return m.rewindToTurn(turn), m.autosaveCmd()
		})
}

// rewindToTurn truncates the conversation back to just before turn n
// (1-based), preserving the abandoned tail as a branch, and returns the
// message for the transcript.
func (m *Model) rewindToTurn(n int) string {
	if n < 1 || n > len(m.checkpoints) {
		return fmt.Sprintf("Usage: /rewind [1-%d]", len(m.checkpoints))
	}
	cp := m.checkpoints[n-1]
	msgs := m.agent.Messages()
	if cp.index > len(msgs) {
		return "Rewind failed: the checkpoint no longer matches the conversation."
	}
	full := make([]provider.Message, len(msgs))
	copy(full, msgs)
	dropped := len(full) - cp.index

	branchNote := "Chat persistence is unavailable, so the abandoned tail was discarded."
	if m.db != nil {
		branch := branchName(m.sessionName, n)
		if err := m.db.SaveChatBranch(m.sessionName, branch, stripResumeContext(full)); err != nil {
			branchNote = "Failed to preserve the abandoned tail as a branch: " + err.Error()
		} else {
			branchNote = fmt.Sprintf("The abandoned tail (%d message(s)) is kept as branch %q — /branches to switch back.", dropped, branch)
		}
	}

	// loadConversation rebuilds checkpoints from the messages alone; restore
	// the live ones so their git snapshots survive the rewind.
	kept := append([]checkpoint(nil), m.checkpoints[:n-1]...)
	m.loadConversation(full[:cp.index])
	m.checkpoints = kept
	// The rewound conversation is not the one the provider reported on.
	m.contextTokens = 0
	m.resetRounds()

	lines := []string{
		fmt.Sprintf("Rewound to before turn %d (%q).", n, cp.preview),
		branchNote,
		"Only the conversation was rewound — files on disk were not restored.",
	}
	if g := m.gitDivergence(cp); g != "" {
		lines = append(lines, g)
	}
	return strings.Join(lines, "\n")
}

// branchName names the branch that preserves an abandoned tail. The timestamp
// keeps names unique per rewind (SaveChat overwrites same-named sessions).
func branchName(session string, turn int) string {
	return fmt.Sprintf("%s@turn%d-%s", session, turn, time.Now().Format("20060102-150405.000"))
}

// checkpointGitDesc is the picker row's one-line git state for a checkpoint.
func (m Model) checkpointGitDesc(cp checkpoint) string {
	switch {
	case !cp.hasGit:
		return "no git snapshot recorded"
	case !cp.git.Repo:
		return "not a git repository"
	case cp.git.DirtyPaths == 0:
		return fmt.Sprintf("git: HEAD %s, clean tree", shortHead(cp.git.Head))
	default:
		return fmt.Sprintf("git: HEAD %s, %d dirty path(s)", shortHead(cp.git.Head), cp.git.DirtyPaths)
	}
}

// gitDivergence describes how the workspace's git state has moved since the
// checkpoint; empty when there is nothing meaningful to say.
func (m Model) gitDivergence(cp checkpoint) string {
	if m.gitSnapshot == nil {
		return ""
	}
	if !cp.hasGit {
		return "Git: no snapshot was recorded for this checkpoint, so divergence is unknown."
	}
	now := m.gitSnapshot()
	switch {
	case !cp.git.Repo || !now.Repo:
		return ""
	case cp.git.Head != now.Head:
		return fmt.Sprintf("Git: HEAD has moved since this checkpoint (%s → %s).", shortHead(cp.git.Head), shortHead(now.Head))
	case cp.git.StatusHash != now.StatusHash:
		return fmt.Sprintf("Git: the working tree has changed since this checkpoint (%d dirty path(s) then, %d now).", cp.git.DirtyPaths, now.DirtyPaths)
	default:
		return fmt.Sprintf("Git: HEAD %s and the working tree match this checkpoint.", shortHead(now.Head))
	}
}

func shortHead(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// branchFamily is the session's branch family, or the one line saying why
// there is none to pick from. The picker and the text answer of /branches
// both ask it, so what counts as a family, and what is said when there is
// not one, is defined once.
func (m Model) branchFamily() ([]storage.ChatBranch, string) {
	if m.db == nil {
		return nil, "Chat persistence is unavailable."
	}
	branches, err := m.db.ListChatBranches(m.sessionName)
	if err != nil {
		return nil, "Error: " + err.Error()
	}
	if len(branches) < 2 {
		return nil, "This session has no branches yet — /rewind creates one."
	}
	return branches, ""
}

// switchBranch resolves the /branches argument — a number or an exact name —
// to a branch and switches to it. The number addresses the family in the
// order the picker draws it, so the digit typed at the card and the digit
// typed after the command mean the same branch.
func (m *Model) switchBranch(branches []storage.ChatBranch, arg string) string {
	target := ""
	if n, err := strconv.Atoi(arg); err == nil {
		if n < 1 || n > len(branches) {
			return fmt.Sprintf("Usage: /branches [1-%d]", len(branches))
		}
		target = branches[n-1].Name
	} else {
		for _, b := range branches {
			if b.Name == arg {
				target = b.Name
				break
			}
		}
		if target == "" {
			return fmt.Sprintf("No branch %q in this session's family.", arg)
		}
	}
	return m.switchToBranch(target)
}

// switchToBranch saves the current conversation to its own slot (nothing is
// lost on switch), then loads the named branch as the working conversation.
// The /branches picker selects a branch by name, so it comes here
// directly rather than through switchBranch's number-or-name resolution.
func (m *Model) switchToBranch(target string) string {
	if target == m.sessionName {
		return fmt.Sprintf("Already on %q.", target)
	}
	if len(m.agent.Messages()) > 1 {
		if err := m.db.SaveChat(m.sessionName, stripResumeContext(m.agent.Messages())); err != nil {
			return "Error saving the current branch before switching: " + err.Error()
		}
	}
	msgs, err := m.db.LoadChat(target)
	if err != nil {
		return "Error: " + err.Error()
	}
	m.loadConversation(msgs)
	m.adoptSlot(target)
	// The title stays: a branch is the same conversation, and the next
	// autosave stamps it on the branch's row so the listing shows both
	// members of the family under the same words.
	//
	// The handoff does not: it is the summary of one branch's past, and the
	// next save would otherwise stamp the branch just left onto the branch
	// just opened, over the one the fork carried there (reopen.go). No
	// reading of the checkout is taken either, unlike opening a conversation
	// by name — this is a move inside one sitting, on the tree that sitting
	// already surveyed, so there is nothing new to say about it.
	m.compactSummary = storedChatSummary(m.db, target)
	m.contextTokens = 0
	m.resetRounds()
	return fmt.Sprintf("Switched to branch %q (%d messages).", target, len(msgs))
}
