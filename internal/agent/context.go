package agent

// Message-list hygiene for context management (S-055): trimming old tool
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
	}
	return n
}

// TrimOldToolResults elides the oldest tool results until the context
// estimate est is back at or under threshold, returning how many were elided
// and the updated estimate. Messages at or after the last user message (the
// current turn) are never touched, and user/assistant text is always kept.
func (a *Agent) TrimOldToolResults(est, threshold int64) (elided int, newEst int64) {
	if est <= threshold {
		return 0, est
	}
	lastUser := 0
	for i := len(a.messages) - 1; i >= 0; i-- {
		if a.messages[i].Role == provider.RoleUser {
			lastUser = i
			break
		}
	}
	for i := 0; i < lastUser && est > threshold; i++ {
		msg := &a.messages[i]
		if msg.Role != provider.RoleTool || msg.Content == ElidedResult {
			continue
		}
		est -= EstimateTokens(msg.Content) - EstimateTokens(ElidedResult)
		msg.Content = ElidedResult
		elided++
	}
	return elided, est
}
