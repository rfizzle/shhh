package agent

// Message-list hygiene for context management: trimming old tool
// results is conversation surgery, so it lives with the message list. The
// thresholds and the /compact flow stay with the front-end, which knows the
// model's context window and drives the summarization stream.

import "github.com/rfizzle/shhh/internal/provider"

// ElidedResult replaces trimmed tool results in the conversation.
const ElidedResult = "[result elided]"

// estimatedBytesPerToken is the rough chars→tokens heuristic used when the
// provider hasn't reported real usage.
const estimatedBytesPerToken = 4

// EstimateTokens roughly estimates the token count of a string.
func EstimateTokens(s string) int64 {
	return int64(len(s) / estimatedBytesPerToken)
}

// EstimateMessageTokens roughly estimates the token count of a conversation.
func EstimateMessageTokens(msgs []provider.Message) int64 {
	var n int64
	for _, msg := range msgs {
		n += EstimateTokens(msg.Content)
		for _, tc := range msg.ToolCalls {
			n += EstimateTokens(tc.Arguments)
		}
		n += EstimateAttachmentTokens(msg.Attachments)
	}
	return n
}

// estimatedImageTokens is what one attached image costs, roughly. Providers
// price an image by its tile count rather than by its bytes, so byte-based
// arithmetic would be wrong in both directions; a flat figure near a
// full-width screenshot keeps the context meter honest enough to be worth
// showing.
const estimatedImageTokens = 1500

// EstimateAttachmentTokens roughly estimates what a message's attachments
// occupy: text goes in verbatim, images cost about a screenshot each.
func EstimateAttachmentTokens(atts []provider.Attachment) int64 {
	var n int64
	for _, a := range atts {
		switch a.Kind {
		case provider.AttachmentText:
			n += EstimateTokens(string(a.Data))
		default:
			n += estimatedImageTokens
		}
	}
	return n
}

// TrimOldToolResults elides the oldest tool results until the context
// estimate est is at or under mark, returning how many were elided and the
// updated estimate. Nothing is elided at all until est is over threshold, so
// the two figures together decide how often a trim happens as well as how
// deep it goes: a trim that stopped at the threshold it was triggered by
// would leave the conversation a few hundred tokens under the line and run
// again next round, and each run costs the caller its whole cached prefix.
// A mark well below the threshold spends that once and buys the gap.
//
// Messages at or after the last user message (the current turn) are never
// touched, user/assistant text is always kept, and so is any result the
// KeepResults predicate claims.
//
// A mark above the threshold would ask the loop to stop before it has
// started, so it is read as the threshold itself — which is exactly the
// behaviour of trimming against one figure.
func (a *Agent) TrimOldToolResults(est, threshold, mark int64) (elided int, newEst int64) {
	if est <= threshold {
		return 0, est
	}
	if mark > threshold {
		mark = threshold
	}
	lastUser := 0
	for i := len(a.messages) - 1; i >= 0; i-- {
		if a.messages[i].Role == provider.RoleUser {
			lastUser = i
			break
		}
	}
	// Oldest first, and the prompt cache is why that costs nothing extra.
	// Every dialect bills a repeat request against the longest prefix it
	// matches, and the markers are placed so that everything before the
	// current turn is inside that prefix — so rewriting the oldest result
	// invalidates no more of the cache than rewriting a later one would,
	// while leaving the recent context, which is what the model is actually
	// working from, whole.
	// See docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once.
	for i := 0; i < lastUser && est > mark; i++ {
		msg := &a.messages[i]
		if msg.Role != provider.RoleTool || msg.Content == ElidedResult {
			continue
		}
		if a.keep != nil && a.keep(msg.Content) {
			continue
		}
		// What rode on the result goes with it. An image a reader attached
		// is the largest thing in the message and the reason the trim was
		// called for; leaving it behind a placeholder that no longer
		// describes it would elide the sentence and keep the megabytes.
		est -= EstimateTokens(msg.Content) - EstimateTokens(ElidedResult)
		est -= EstimateAttachmentTokens(msg.Attachments)
		msg.Content = ElidedResult
		msg.Attachments = nil
		elided++
	}
	return elided, est
}
