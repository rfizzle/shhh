package agent

import (
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

// What opens the messages a reopening puts in front of a conversation. They
// are constants because they are also how a later reopening recognises the
// block it is replacing: a conversation opened three times must be told about
// the tree in front of it once, not told three times about three commits, two
// of which nobody is looking at any more.
const (
	ResumeMessagePrefix = "[resume: "
	ResumeSummaryPrefix = "Where this conversation stood when it was last written down:"
	// CarriedStepsPrefix opens the carried working list. It lives here so the
	// strip can recognise it; plan.CarriedStepsPrefix is this, for the code
	// that builds one.
	CarriedStepsPrefix = "Your working list, as your last steps call left it:"
)

// StripResumeContext drops the reading a reopening put at the head of a
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
func StripResumeContext(msgs []provider.Message) []provider.Message {
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
		strings.HasPrefix(msgs[end].Content, ResumeSummaryPrefix) {
		end++
	}
	// And the list, which is only ever the last part of one.
	if end < len(msgs) && msgs[end].Role == provider.RoleUser &&
		strings.HasPrefix(msgs[end].Content, CarriedStepsPrefix) {
		end++
	}
	kept := make([]provider.Message, 0, len(msgs)-(end-at))
	kept = append(kept, msgs[:at]...)
	return append(kept, msgs[end:]...)
}

// opensAReading reports the survey's shape: the bracketed facts, whole, on a
// line of their own. Not the raw first line the transcript would print, which
// marks a line it cut short — this is a question about the bytes.
func opensAReading(content string) bool {
	if !strings.HasPrefix(content, ResumeMessagePrefix) {
		return false
	}
	line, _, _ := strings.Cut(content, "\n")
	return strings.HasSuffix(line, "]")
}

// FoldedWithoutReading is the folded turns without the reading a reopening put
// in front of the conversation before it was compacted: it was rebuilt from
// the checkout every time and is not part of what was said.
func FoldedWithoutReading(folded []provider.Message) []provider.Message {
	if len(folded) == 0 {
		return folded
	}
	withPrompt := append([]provider.Message{{Role: provider.RoleSystem}}, folded...)
	return StripResumeContext(withPrompt)[1:]
}
