package chat

// What comes back with a resumed session (
// docs/interface/surfaces.md#the-input-frame).
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
// It looked like one only recently. ↑ on an empty draft with no
// history to recall handed the keyboard to the transcript, so the key still
// did something visible on a resumed session. That was taken away on purpose
// — a key that changes surface depending on how much history a session
// happens to have is unlearnable — and what was left behind was a key
// that did nothing at all, on exactly the sessions a reader most wants to
// repeat a prompt in.
//
// So a loaded conversation seeds the ring, out of the same messages the
// transcript is rebuilt from and in the same pass (loadConversation), because
// a conversation put back on screen and the history behind it must not be
// able to drift apart.
//
// Some user-role messages are the session talking to itself, not lines
// anyone typed: the summary a compaction restarts from (/compact), the
// output /run feeds back, the nudge that continues a reply a dropped
// connection cut off, and everything the steering machinery says in the
// reader's role — a check-in, a steer, a gate verdict, a secret's
// announcement, a tree notice. Recalling one would put a sentence in the
// draft that nobody wrote — a whole compaction summary, in the worst case —
// so the message carries who wrote it (provider.Message.Machine) and the
// ring skips the ones the session did. This was once a list of the openings
// those messages start with, and the list did not know about the steering
// machinery: a shape nobody had remembered to add to came back as the
// reader's own words. What ↑ offers is what a reader could have typed, or it
// is not a history of anything.

import (
	"strings"

	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
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
		if text == "" || !typedByHand(msg) {
			continue
		}
		m.recordInput(text)
	}
	m.historyIdx = len(m.inputHistory)
}

// typedByHand reports whether a user-role message is a line the reader typed
// rather than one the session wrote on their behalf. The flag is set where
// the message is written and stored beside it, so a reworded message cannot
// quietly start being recalled and a resumed conversation answers the same
// way a live one does.
func typedByHand(msg provider.Message) bool { return !msg.Machine }

// A recalled sentence carries its pastes back with it.
//
// ↑ puts a line back in the draft exactly as it was sent, folds and all. What
// the folds stood for left with the send — takeAttachments empties the staging
// area — so `⟨paste 1 · 214 lines⟩` came back painted as a thing the session
// is carrying, priced by nothing, opened by no key, and about to go out as
// those five words instead of two hundred lines. A mark that is drawn nowhere
// else in the product must not be readable as a fold when there is no fold
// behind it (docs/interface/surfaces.md#the-input-frame).
//
// So a recalled fold is a paste again. The bytes are on the transcript row the
// send left, which keeps the whole log precisely so the row is an account of
// what was sent (attachments.go), and recall stages them back under the next
// free paste name — renumbering the token in the sentence when the number it
// had is taken. It then opens with the same key, prices on the same rail and
// rides out with the next send: the line as it was, ready to go again, which
// is what recall means everywhere else in this surface.
//
// A conversation loaded from storage is the same case: the bytes were saved
// with the message that carried them, and its rows are rebuilt through the
// same userEntry the send used (newsession.go), so a reopened session's folds
// have their logs behind them too.
//
// Where the bytes are not there the fold loses its quotes instead and stays as
// its count in plain words — a paste that no longer fits beside what is
// already staged. `paste 1 · 214 lines` is prose about a log that is not
// riding, and prose is what it now is.
//
// Walking on drops what walking here staged. ↑ again, or ↓ back out to the
// empty draft, replaces the sentence the fold was in, and a paste whose fold
// left the draft with it is bytes the next message would carry and no longer
// mentions — the rule a backspace over a token already follows.
//
// Attached, none of it happens. The staging area is the orchestrator's and
// the keyboard is pointed at a child, which is why every other key that reads
// it stands down there (keyroute.go); a sentence recalled into a steer is
// carrying nothing, so its folds are counts.

// recallDraft puts a remembered line in the draft with its folds settled: the
// pastes it mentions staged, the ones the line being left behind mentioned
// dropped, and whatever is left over reading as its count. Every step of the
// walk goes through it, the empty draft ↓ ends on included, because that step
// is a fold leaving the draft like any other.
func (m *Model) recallDraft(text string) {
	if m.attachedTo == "" {
		m.unstageRecalled(m.input.Value(), text)
		text = m.restageRecalled(text)
	}
	text = m.stripDeadFolds(text)
	if text == "" {
		m.input.Reset()
	} else {
		m.input.SetValue(text)
	}
	// The staged rail may have arrived or gone, and it is a row the draft
	// does not have.
	m.syncViewport()
}

// clearDraft empties the draft and takes the pastes whose folds were in it
// out of the staging area with it. A cleared sentence is a fold leaving the
// draft like a step of the walk is, and the paste it stood for would
// otherwise ride out with the next message, mentioned by nothing — the rule
// a backspace over a token already follows. Only a paste the draft named
// goes: a file the reader attached by hand has no fold in the sentence and
// was never the sentence's to take. Attached, the staging area is the
// orchestrator's and is left alone, as the walk leaves it.
func (m *Model) clearDraft() {
	if m.attachedTo == "" {
		m.unstageRecalled(m.input.Value(), "")
	}
	m.input.Reset()
	m.historyIdx = len(m.inputHistory)
	m.syncViewport()
}

// unstageRecalled drops the pastes whose folds are in the sentence being
// walked away from and not in the one arriving. A paste staged by some other
// door and left without a fold is not this walk's to drop.
func (m *Model) unstageRecalled(prev, next string) {
	for i := len(m.attachments) - 1; i >= 0; i-- {
		p, ok := pasteOf(m.attachments[i])
		if !ok || !strings.Contains(prev, p.token) || strings.Contains(next, p.token) {
			continue
		}
		// A full slice expression, for takeAttachments' reason: the staged
		// set is handed off whole and must not be shortened through a shared
		// array.
		m.attachments = append(m.attachments[:i:i], m.attachments[i+1:]...)
	}
}

// restageRecalled stages what the folds in a recalled sentence stand for and
// answers the sentence, its tokens renumbered where the name a paste had is
// taken by one already staged. What it could not stage it leaves alone, for
// stripDeadFolds to read as a count.
//
// Newest row first, because that is the direction the walk itself goes: two
// sends of the same log leave two rows carrying the same token, and the one
// the reader is stepping back through is the later one.
func (m *Model) restageRecalled(text string) string {
	if !strings.ContainsRune(text, components.PasteFoldOpen) {
		return text
	}
	for i := len(m.transcript) - 1; i >= 0; i-- {
		for _, p := range m.transcript[i].pastes {
			token := components.PasteToken(p.label, p.lines)
			if !strings.Contains(text, token) || m.stagedFold(token) {
				continue
			}
			if staged, ok := m.restagePaste(p); ok {
				text = strings.Replace(text, token, staged, 1)
			}
		}
	}
	return text
}

// restagePaste stages one sent paste again and answers the token that now
// stands for it. The ceiling is asked the way every other door asks it, so a
// recall cannot put more in the staging area than a paste could; the refusal
// is quiet here, because the sentence says what happened by keeping the count
// and losing the quotes.
func (m *Model) restagePaste(p pasteFold) (string, bool) {
	// The row holds the log split into lines with the newline a file ends on
	// taken off; joining it puts back the bytes the request carried.
	a, err := attachment.FromBytes(nextPasteName(m.attachments),
		[]byte(strings.Join(p.body, "\n")+"\n"))
	if err != nil {
		return "", false
	}
	q, ok := pasteOf(a)
	if !ok {
		return "", false
	}
	staged, _ := m.stageQuietly([]provider.Attachment{a})
	if !staged.isStaged(a.Name) {
		return "", false
	}
	*m = staged
	return q.token, true
}

// stagedFold reports whether a token stands for something in the staging area.
func (m Model) stagedFold(token string) bool {
	for _, p := range m.stagedPastes() {
		if p.token == token {
			return true
		}
	}
	return false
}

// stripDeadFolds takes the quotes off every fold in a sentence that no staged
// paste stands for, leaving the count as words.
//
// It reads a run between the two marks as a fold without asking what is
// written inside it, because that is what the draft does with one: the frame
// paints anything between them in the tone of a thing being carried
// (frame.go), so a run the staging area cannot account for is exactly the
// state this exists to make unreachable.
func (m Model) stripDeadFolds(text string) string {
	openMark, closeMark := string(components.PasteFoldOpen), string(components.PasteFoldClose)
	var out strings.Builder
	for {
		from := strings.Index(text, openMark)
		if from < 0 {
			break
		}
		to := strings.Index(text[from:], closeMark)
		if to < 0 {
			break
		}
		to += from
		fold := text[from : to+len(closeMark)]
		out.WriteString(text[:from])
		if m.stagedFold(fold) {
			out.WriteString(fold)
		} else {
			out.WriteString(text[from+len(openMark) : to])
		}
		text = text[to+len(closeMark):]
	}
	out.WriteString(text)
	return out.String()
}
