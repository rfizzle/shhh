package chat

// What comes back with a resumed session (S-162, DESIGN-TUI.md §12i).
//
// Input recall is the surface's memory of what was typed into it: every
// submitted line goes to recordInput, and ↑ walks back through them
// (model.go). It was a memory of this sitting only. A session that came back
// through `--continue`, `--resume`, /load or a rewind's branch switch had
// typed lines too — they are in the conversation it just loaded, on screen in
// the transcript — but nothing put them back in the ring, so ↑ found it empty
// and fell through to the textarea, where it moved the cursor inside a
// one-line draft and looked like a dead key.
//
// It looked like one only recently. Until S-140, ↑ on an empty draft with no
// history to recall handed the keyboard to the transcript, so the key still
// did something visible on a resumed session. S-140 took that away on purpose
// — a key that changes surface depending on how much history a session
// happens to have is unlearnable (§7a) — and what was left behind was a key
// that did nothing at all, on exactly the sessions a reader most wants to
// repeat a prompt in.
//
// So a loaded conversation seeds the ring, out of the same messages the
// transcript is rebuilt from and in the same pass (loadConversation), because
// a conversation put back on screen and the history behind it must not be
// able to drift apart.
//
// Three user-role messages are the session talking to itself, not lines
// anyone typed: the summary a compaction restarts from (§10 /compact), the
// output /run feeds back, and the nudge that continues a reply a dropped
// connection cut off (§17b). Recalling one would put a sentence in the draft
// that nobody wrote — a whole compaction summary, in the worst case — so each
// declares its opening as a constant beside the code that writes it, and the
// ring skips them. What ↑ offers is what a reader could have typed, or it is
// not a history of anything.

import (
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

// recallFromMessages rebuilds the input history from a loaded conversation.
//
// It replaces rather than appends: loading is a change of conversation, and
// the ring belongs to the one on screen. recordInput does the appending, so
// the rules a live session's history follows — consecutive repeats collapse,
// the cursor parks past the newest entry — are the rules a loaded one gets,
// from the same code rather than from a second description of it.
func (m *Model) recallFromMessages(msgs []provider.Message) {
	m.inputHistory = nil
	for _, msg := range msgs {
		if msg.Role != provider.RoleUser {
			continue
		}
		text := strings.TrimSpace(msg.Content)
		if text == "" || !typedByHand(text) {
			continue
		}
		m.recordInput(text)
	}
	m.historyIdx = len(m.inputHistory)
}

// typedByHand reports whether a user-role message is a line the reader typed
// rather than one the session wrote on their behalf. The three openings it
// knows are declared next to the code that writes each of them, so a reworded
// message cannot quietly start being recalled.
func typedByHand(text string) bool {
	switch {
	case strings.HasPrefix(text, compactContextPrefix):
		return false
	case strings.HasPrefix(text, commandContextPrefix):
		return false
	case text == continuePrompt:
		return false
	}
	return true
}
