package agent

// Message-list hygiene for context management: trimming old tool results is
// conversation surgery, so it lives with the message list, and so do the
// shares of the window that say when it is due. What the surface keeps is the
// window itself — which model this conversation is on, and what that model
// can hold — because that is the one part of the question a message list
// cannot answer about itself. Replacing a conversation that a trim could not
// rescue is compact.go, in this package, called by whichever driver reached
// the round boundary.

import (
	"fmt"
	"math"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

// How full the window may get, as shares of it. They live here rather than
// with the surface that draws them because they are what every surface
// measures against: a line crossed at one figure in a session and at another
// where nobody is watching is two behaviours wearing one name.
const (
	// TrimThresholdPercent of the context window is where recovery starts.
	// Old tool results are elided first, and where eliding cannot clear the
	// line the conversation is replaced by a summary of itself.
	TrimThresholdPercent = 80
	// WarnThresholdPercent is "filling up, but not yet a problem".
	WarnThresholdPercent = 60
	// TrimLowWaterPercent is where a trim stops. It is the warn line rather
	// than a figure of its own because that is already the place this
	// product calls "filling up but not yet a problem", and a trim has no
	// reason to invent a second one.
	//
	// It is far below the trigger on purpose. A trim that stopped as soon as
	// it was under the threshold would clear the line by a few hundred
	// tokens, cross it again a round later, and pay the price of a trim
	// again — and that price is the whole prompt prefix the provider was
	// caching, because eliding a message invalidates the cache from that
	// message on. Stopping here spends one invalidation on a fifth of the
	// window of headroom.
	// See docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once.
	TrimLowWaterPercent = WarnThresholdPercent
)

// ElidedResult replaces a trimmed tool result whose original nothing kept.
// It is the fallback: a trim that could store the original replaces the
// result with a placeholder naming the id instead, and the bare constant is
// what is left when there is no store or the store refused.
const ElidedResult = "[result elided]"

// elidedPrefix opens both placeholders, and is how the loop tells a result it
// has already elided from one it has not. A prefix rather than an equality
// test, because the placeholder carrying an id is a different string every
// time: matched by equality, the next trim would overwrite a placeholder that
// names an entry with the bare one, throwing the offer of the original away
// and invalidating the provider's cached prefix from there on to do it.
const elidedPrefix = "[result elided"

// minEvidenceBytes is the smallest trimmed result worth putting in the store.
// The placeholder that names an id runs about 140 bytes, so at this size the
// offer already eats better than a quarter of what eliding recovers, and a
// growing share below it — while offering to page back something short
// enough that nothing needed to drop it in the first place. Under this the
// bare placeholder is used and the store is left alone.
//
// It is comfortably above the placeholder's own length, which is what lets
// the trim treat storing as free of the decision to elide: a result the
// store takes always recovers more than the notice replacing it costs.
const minEvidenceBytes = 512

// elidedWithEvidence is the placeholder for a trimmed result the store took.
// It is worded like the notice a reduction leaves so that the one instruction
// the toolbox gives about evidence — a notice carrying an id can be paged
// back with the evidence tool — covers a trimmed result too. A second wording
// for the same offer would be a second thing the model has to be taught.
func elidedWithEvidence(size int, id string) string {
	return fmt.Sprintf(
		"[result elided: %d bytes; full output stored as evidence %s — retrieve it with the evidence tool (info/read/search)]",
		size, id)
}

// estimatedBytesPerToken is the rough chars→tokens heuristic used when the
// provider hasn't reported real usage. It is about right for prose and wrong
// in one direction for everything else a coding session carries, which is
// what Calibration exists to correct.
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

// Calibration corrects the byte-count estimate against what the provider
// actually charged for the same messages.
//
// The estimate is four bytes to the token, which is about right for prose and
// wrong for the part of a coding session that dominates it: source and JSON
// tokenize nearer three, so a tool-heavy conversation is fuller than the
// meter says and the trim the meter triggers fires late. Counting for real
// would mean a token-counting round-trip per request — a request spent to
// find out what a request will cost, and only one of the five dialects offers
// one in a comparable shape. The ratio is free instead: every response
// reports what the prompt it just read actually came to, and the estimate for
// those same messages is already in hand.
//
// It is a fact about a tokenizer, so it belongs to one model. The zero value
// corrects nothing, which is a session that has not been told anything yet.
// See docs/capabilities/providers.md#how-full-the-window-is-corrected-by-what-it-cost.
type Calibration struct {
	factor float64
	model  string
}

// calibrationWeight is how much of one response's ratio the factor takes. A
// half lands within a few percent of a steady ratio in three responses, which
// is soon enough to matter on the turn that trims, and still leaves any one
// request — a cache-warming first round, a turn carrying three screenshots —
// outvoted by the ones around it.
const calibrationWeight = 0.5

// calibrationFloor and calibrationCeiling bound the factor. Four bytes to the
// token is the estimate, so the range spans two bytes to the token at one end
// and eight at the other — wider than any text a conversation carries. A
// ratio outside it is not the estimator being wrong about the messages; it is
// a report describing something other than what was counted (a provider
// billing a cached prefix it did not send, a usage field in other units), and
// against that the last good factor is the better guess.
const (
	calibrationFloor   = 0.5
	calibrationCeiling = 2.0
)

// Observe folds one response into the factor: reported is the provider's
// prompt count and estimated is what this estimator made of the same
// messages. A response that reported nothing, or one there was no
// conversation to compare it against, teaches nothing and is ignored — which
// is what keeps a session on a provider that reports no usage behaving
// exactly as it did before.
//
// A different model resets the factor rather than dragging it: the two
// tokenizers disagree by more than the weight would work off, and a stale
// factor is worse than none because it is confident.
func (c *Calibration) Observe(model string, reported, estimated int64) {
	if c.model != model {
		*c = Calibration{model: model}
	}
	if reported <= 0 || estimated <= 0 {
		return
	}
	f := c.Factor()
	f += calibrationWeight * (float64(reported)/float64(estimated) - f)
	c.factor = min(max(f, calibrationFloor), calibrationCeiling)
}

// Factor is what an estimate is multiplied by; 1 until a response has been
// observed.
func (c Calibration) Factor() float64 {
	if c.factor == 0 {
		return 1
	}
	return c.factor
}

// Corrected reports whether the factor still moves an estimate. Surfaces ask
// so they can say which of the three a figure is — a report, a raw estimate,
// or an estimate this session has measured — because a number that silently
// changed meaning is worse than either of the other two.
func (c Calibration) Corrected() bool { return c.Factor() != 1 }

// Apply scales an estimate by the factor, rounding to the nearest token. It
// takes a difference between two estimates as readily as a total, so the
// arithmetic a caller does in corrected units stays in them throughout.
func (c Calibration) Apply(est int64) int64 {
	if !c.Corrected() {
		return est
	}
	return int64(math.Round(float64(est) * c.Factor()))
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
// est, threshold and mark are all read in whatever units the caller measures
// its window in, and cal is how a message's estimated size is converted into
// them. A caller trimming against a corrected or provider-reported figure and
// shrinking it by raw estimates would watch the number fall slower than the
// conversation does, and elide past the mark it asked for.
//
// Messages at or after the last user message (the current turn) are never
// touched, user/assistant text is always kept, and so is any result the
// KeepResults predicate claims.
//
// A mark above the threshold would ask the loop to stop before it has
// started, so it is read as the threshold itself — which is exactly the
// behaviour of trimming against one figure.
func (a *Agent) TrimOldToolResults(est, threshold, mark int64, cal Calibration) (elided int, newEst int64) {
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
	// A result carries only the id of the call it answers, so the tool that
	// produced it is read off the assistant messages on the way past. The
	// store keeps it as the entry's tool, which is what the evidence tool
	// reports back when the model asks what an id holds — so a session with
	// nowhere to put an original has no use for the names either.
	var called map[string]string
	if a.archive != nil {
		called = map[string]string{}
		for i := 0; i < lastUser; i++ {
			for _, tc := range a.messages[i].ToolCalls {
				called[tc.ID] = tc.Name
			}
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
		if msg.Role != provider.RoleTool || strings.HasPrefix(msg.Content, elidedPrefix) {
			continue
		}
		if a.keep != nil && a.keep(msg.Content) {
			continue
		}
		// A result no longer than the placeholder that would replace it
		// recovers nothing, and rewriting it would spend the cached prefix
		// from here on to make the request no smaller. It is left alone
		// unless something rode on it.
		placeholder := ElidedResult
		saved := cal.Apply(EstimateTokens(msg.Content) - EstimateTokens(placeholder))
		if saved <= 0 && len(msg.Attachments) == 0 {
			continue
		}
		// Whether to elide is settled before the store is asked anything, so
		// a result the loop walks past leaves no entry behind that nothing in
		// the conversation points at. Past that, the original goes somewhere
		// the model can ask for it and the placeholder carries the id that
		// asks; a store that refuses leaves the bare placeholder, because a
		// trim that failed because the disk did would send the very request
		// it was called to shrink.
		// See docs/capabilities/evidence.md#a-trim-makes-the-same-promise.
		if a.archive != nil && len(msg.Content) >= minEvidenceBytes {
			if id, ok := a.archive(called[msg.ToolCallID], msg.Content); ok {
				placeholder = elidedWithEvidence(len(msg.Content), id)
				saved = cal.Apply(EstimateTokens(msg.Content) - EstimateTokens(placeholder))
			}
		}
		// What rode on the result goes with it. An image a reader attached
		// is the largest thing in the message and the reason the trim was
		// called for; leaving it behind a placeholder that no longer
		// describes it would elide the sentence and keep the megabytes.
		est -= saved
		est -= cal.Apply(EstimateAttachmentTokens(msg.Attachments))
		msg.Content = placeholder
		msg.Attachments = nil
		elided++
	}
	return elided, est
}
