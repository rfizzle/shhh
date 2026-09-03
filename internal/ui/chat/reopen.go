package chat

// What a conversation is told when it is opened again
// (docs/capabilities/sessions-and-memory.md#a-resumed-session-sees-the-tree-as-it-is).
//
// The transcript is not the gap. A saved conversation comes back whole, so
// the decisions are still in the messages that made them. What comes back
// wrong is the checkout: the survey a session reads is read once, at launch,
// and a conversation reopened the next morning reasons from that picture
// after a pull, a rebase or somebody else's session has moved the tree
// underneath it. It edits a file that moved, or re-fixes what is already
// fixed, and nothing in front of it says otherwise.
//
// So a reopened conversation is given the checkout as it stands now, ahead of
// everything it remembers, and the summary its last compaction wrote where
// there is one. Nothing is summarized here: /compact already asks for the
// goals, the decisions, the work done and what is open, and a second
// summarizer run at quit would be a request the person did not make and did
// not wait for.

import (
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
)

// resumeVerb opens the row, and is closed like every other
// (docs/interface/principles.md#closed-vocabularies).
const resumeVerb = "resumed"

// resumeMessagePrefix and resumeSummaryPrefix open the two injected messages.
// They are constants because they are also how a later reopening recognises
// the block it is replacing: a conversation opened three times must be told
// about the tree in front of it once, not told three times about three
// commits, two of which nobody is looking at any more.
const (
	resumeMessagePrefix = "[resume: "
	resumeSummaryPrefix = "Summary written when this conversation was last compacted:"
)

// ResumeNotice is what a reopened conversation starts with, ready to deliver:
// the messages that go in front of the restored transcript, the one line the
// surface shows for them, and that line's expansion. It is the tree reading's
// shape (internal/agent/tree.go) for the same reason — what the model is told
// and what the reader is shown are two renderings of one finding, and a
// surface that built the second from the first would be paraphrasing it.
type ResumeNotice struct {
	Messages []provider.Message
	Notice   string
	Text     string
	// Summary is the stored handoff the messages carry, handed back so the
	// session that took it can go on saving it. A resumed conversation that
	// never compacts again would otherwise write an empty one over the slot
	// and lose the handoff on its first save.
	Summary string
}

// ResumeContext is what slot's conversation should be told about dir as it
// stands now. It is one function over a store and a directory rather than a
// method on the session so that every front-end which can reopen a
// conversation sends the same context: the interactive `--continue` and
// `--resume`, `/load`, and the unattended run handed a conversation to carry
// on. A run that reasoned from the transcript's picture of the tree is the
// failure this exists to prevent, and it is no less a failure with nobody
// watching.
//
// A nil store or an unnamed slot still surveys: the tree is the half that
// does not come from the store, and it is the half that moves.
func ResumeContext(db *storage.DB, slot, dir string) ResumeNotice {
	var saved storage.ChatResume
	if db != nil && slot != "" {
		saved, _ = db.ChatResume(slot)
	}
	return resumeNotice(project.Survey(dir), saved)
}

// resumeNotice assembles the notice from a survey and what the slot
// remembered, separately from the reading of either so it can be tested and
// captured on facts built by hand.
func resumeNotice(info project.Info, saved storage.ChatResume) ResumeNotice {
	n := ResumeNotice{Notice: resumeRow(info)}
	n.Messages = append(n.Messages, provider.Message{
		Role: provider.RoleUser, Content: resumeSurveyMessage(info, saved.Head)})
	// No placeholder for a conversation that never compacted. A line saying
	// there is no summary is a line the model has to read and can do nothing
	// with.
	if summary := strings.TrimSpace(saved.Summary); summary != "" {
		n.Summary = summary
		n.Messages = append(n.Messages, provider.Message{
			Role: provider.RoleUser, Content: resumeSummaryPrefix + "\n\n" + summary})
	}
	texts := make([]string, 0, len(n.Messages))
	for _, msg := range n.Messages {
		texts = append(texts, msg.Content)
	}
	n.Text = strings.Join(texts, "\n\n")
	return n
}

// resumeRow is the folded row's line: the verb, the branch, and how much is
// changed. The three facts a person checks before typing the next
// instruction, in the order they check them — and the same three the message
// under it opens with, so the row is an account of what was injected rather
// than a second finding.
func resumeRow(info project.Info) string {
	if !info.Repo {
		return resumeVerb + " · no git here"
	}
	return fmt.Sprintf("%s · %s · %d changed", resumeVerb, resumeBranch(info), info.Dirty)
}

// resumeBranch names the branch the way the tree reading names it: a detached
// head and a repository with no branch yet each say so rather than rendering
// as an empty field between two separators.
func resumeBranch(info project.Info) string {
	switch {
	case info.Detached:
		return "(detached)"
	case info.Branch == "":
		return "(none)"
	}
	return info.Branch
}

// resumeSurveyMessage is the checkout as it stands now, addressed to the
// model: the bracketed facts, then what to do about them. was is the commit
// the slot was last written on, and the sentence changes on one thing only —
// whether it is still the commit in front of the reader.
//
// The moved case is the whole point. A transcript that describes a tree which
// has since moved does not merely go stale, it actively misleads: it names
// paths, line numbers and states that were true and are not, and the model
// has no way to tell that from the tree it is looking at.
func resumeSurveyMessage(info project.Info, was string) string {
	var b strings.Builder
	b.WriteString(resumeMessagePrefix)
	if !info.Repo {
		b.WriteString("not a git repository]\n")
		b.WriteString("This conversation is being continued from an earlier sitting. " +
			"Nothing here records what the workspace looked like then, so read a file before you act on what the transcript says about it.")
		return b.String()
	}
	parts := []string{"branch " + resumeBranch(info), fmt.Sprintf("%d changed", info.Dirty)}
	if info.Head != "" {
		parts = append(parts, "HEAD "+shortCommit(info.Head))
	}
	b.WriteString(strings.Join(parts, " · ") + "]\n")
	if was != "" && info.Head != "" && was != info.Head {
		fmt.Fprintf(&b, "HEAD has moved since this conversation was saved: %s → %s. "+
			"The transcript below describes the checkout at %s, so re-read a file before editing it, "+
			"and do not revert or explain changes you did not make.",
			shortCommit(was), shortCommit(info.Head), shortCommit(was))
		return b.String()
	}
	b.WriteString("This is the checkout as it stands now, read when the conversation was reopened. " +
		"The transcript below is from an earlier sitting.")
	return b.String()
}

// shortCommit is a commit as this message names one. Seven characters, which
// is where git's own abbreviation starts and what the tree reading uses when
// it tells a running turn that HEAD moved: this is the same statement to the
// same reader at the other end of the session, and two abbreviations of one
// fact is a difference the reader has to account for and cannot.
//
// It is deliberately not the package's shortHead, which is twelve. That one
// draws a git line on the rewind screen for a person with the whole row to
// spend; this one sits inside a sentence that has to stay on one line in a
// sixty-column terminal.
func shortCommit(commit string) string {
	const width = 7
	if len(commit) > width {
		return commit[:width]
	}
	return commit
}

// storedChatSummary is the handoff a slot already holds. A slot with none,
// and a store that cannot be read, both answer with nothing: a conversation
// opened without its summary loses a reading it would have had, while one
// left holding the summary of the conversation before it would put words
// about work nobody did in front of the model and then save them there.
func storedChatSummary(db *storage.DB, slot string) string {
	if db == nil || slot == "" {
		return ""
	}
	saved, err := db.ChatResume(slot)
	if err != nil {
		return ""
	}
	return saved.Summary
}

// resumeConversation restores a saved conversation and tells it what the
// checkout looks like now. Every path back to a stored conversation goes
// through it — `--continue` and `--resume` on the way in, `/load` once the
// session is running — so a conversation cannot come back without the
// reading, and the row that accounts for it cannot be forgotten by one of
// them.
func (m *Model) resumeConversation(slot string, msgs []provider.Message) {
	m.loadConversation(stripResumeContext(msgs))
	if slot != "" {
		m.adoptSlot(slot)
		m.loadTitle()
	}
	m.injectResumeContext()
}

// injectResumeContext puts the reading in front of the restored transcript
// and shows the row for it. The messages go at the head of the conversation
// rather than at its end because that is where a conversation states its
// standing facts, and because the transcript that follows is what they
// correct; the row goes at the end, where the reader is looking.
func (m *Model) injectResumeContext() {
	n := ResumeContext(m.db, m.sessionName, m.workspace)
	msgs := m.agent.Messages()
	at := 0
	if len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		at = 1
	}
	joined := make([]provider.Message, 0, len(msgs)+len(n.Messages))
	joined = append(joined, msgs[:at]...)
	joined = append(joined, n.Messages...)
	joined = append(joined, msgs[at:]...)
	m.agent.SetMessages(joined)
	// The handoff comes back with the conversation, so this sitting's saves
	// keep putting it on the slot rather than writing an empty one over it.
	m.compactSummary = n.Summary
	// A checkpoint is a conversation index, and every restored turn has just
	// moved down by what was put in front of it. Left alone, a rewind would
	// cut the conversation short of the turn the reader picked.
	for i := range m.checkpoints {
		m.checkpoints[i].index += len(n.Messages)
	}
	// The line is the account and the body is what was actually said, which
	// is the shape every folded row in the transcript has: the reader sees
	// that the tree was read, and can open it to see what the model read.
	m.appendEntry(entry{kind: entrySystem, text: n.Notice, toolResult: n.Text})
	m.syncViewport()
}

// stripResumeContext drops the reading a reopening put at the head of a
// conversation.
//
// Both ends of the conversation's life through the store use it, because the
// reading is not part of the conversation: it is built from the checkout
// every time the conversation is opened, the way the system prompt is. A save
// leaves it out, so a slot never holds a reading of a checkout that has since
// moved — which a transcript rebuilt from that slot would draw as something
// the person said, and ↑ would offer back as one of their prompts. A load
// drops one anyway, because a slot written by a build that kept them is still
// a slot this one has to open.
//
// It recognises the block by shape rather than by a count kept on the
// session, and that is the safe direction rather than the lazy one: a count
// would be wrong the moment a compaction rebuilt the conversation around it,
// and stripping by a stale count takes somebody's words away. The shape is
// three things together — the head position, the bracketed opening line, and
// the summary only ever behind a survey — so a message that merely begins the
// way one does is left where it is. A first turn that opened with a whole
// bracketed line reading `[resume: …]` would still be taken for one, which is
// the accepted cost of not keeping a count that can go stale.
// opensAReading reports the survey's shape: the bracketed facts, whole, on a
// line of their own. Not the raw first line the transcript would print, which
// marks a line it cut short — this is a question about the bytes.
func opensAReading(content string) bool {
	if !strings.HasPrefix(content, resumeMessagePrefix) {
		return false
	}
	line, _, _ := strings.Cut(content, "\n")
	return strings.HasSuffix(line, "]")
}

func stripResumeContext(msgs []provider.Message) []provider.Message {
	at := 0
	if len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		at = 1
	}
	if at >= len(msgs) || msgs[at].Role != provider.RoleUser || !opensAReading(msgs[at].Content) {
		return msgs
	}
	end := at + 1
	// The summary is only ever the second half of a block, so it is only
	// recognised behind a survey. On its own it is a message that happens to
	// open the way one does, which is somebody's turn and stays.
	if end < len(msgs) && msgs[end].Role == provider.RoleUser &&
		strings.HasPrefix(msgs[end].Content, resumeSummaryPrefix) {
		end++
	}
	kept := make([]provider.Message, 0, len(msgs)-(end-at))
	kept = append(kept, msgs[:at]...)
	return append(kept, msgs[end:]...)
}
