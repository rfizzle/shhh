package cli

import (
	"context"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

// descriptionChars bounds what a snippet is filed under. The description is a
// phrase on one row of the listing beside the name, and the listing clips a
// longer one to the terminal anyway, so the store keeps the phrase rather
// than the paragraph an explanation can be.
const descriptionChars = 100

// descriptionMaxTokens caps the whole response, the reasoning included: every
// dialect spends the thought and the answer from one ceiling. Ten words is a
// dozen tokens, so nearly all of this is room for the thought — the smallest
// budget any dialect asks for at low is four thousand tokens, and a ceiling
// under that ends mid-thought and describes nothing. Sending no ceiling at
// all was worse still: the Anthropic path fell back to its own default of
// sixty-four thousand, which is a request the model may spend minutes in.
const descriptionMaxTokens = 8192

// descriptionTimeout is how long a save waits for the phrase. It is the
// titler's window rather than a shorter one because this is the same shape of
// request — a small model, a shallow thought, one line back — and a window
// that expires mid-thought files the snippet under nothing.
const descriptionTimeout = 15 * time.Second

// snippetDescription is the line a saved snippet is filed under.
//
// explanation is the sentence the surface already showed under the command,
// and it says of that command what a summarising request would be asked to
// say — so a save that has one writes it down and finishes rather than
// standing in front of another round trip. Only a save with nothing to reuse
// pays for the request: silent mode draws no line, and an answer can come
// back without one.
// See docs/capabilities/generation.md#explanation-is-on-request-not-by-default.
//
// model is the session's own, which the request falls back to only where the
// provider names no small one of its own (summarizer.go).
func snippetDescription(ctx context.Context, p provider.Provider, model, command, explanation string) string {
	if explanation != "" {
		return clampDescription(explanation)
	}
	return generateDescription(ctx, p, model, command)
}

func generateDescription(ctx context.Context, p provider.Provider, model, command string) string {
	ctx, cancel := context.WithTimeout(ctx, descriptionTimeout)
	defer cancel()

	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "Summarize this shell command in under 10 words. Output only the summary, no quotes or punctuation at the end."},
		{Role: provider.RoleUser, Content: command},
	}

	// A bounded call: the small model, a ceiling, and a shallow thought asked
	// for explicitly — which is the only way to bound one on a model that
	// thinks whether or not it was asked.
	// See docs/capabilities/providers.md#a-bounded-call-runs-on-the-small-model.
	events, err := p.StreamCompletion(ctx, msgs, provider.CompletionOpts{
		Model:     auxiliaryModel(p.Name(), model),
		MaxTokens: descriptionMaxTokens,
		Effort:    provider.EffortLow,
	})
	if err != nil {
		return ""
	}

	var sb strings.Builder
	for ev := range events {
		if ev.Err != nil {
			return ""
		}
		sb.WriteString(ev.Token)
	}

	return clampDescription(sb.String())
}

// clampDescription folds a description onto the one row the listing gives it
// and bounds it. Both halves earn their place on the explanation rather than
// on the summary: a sentence written to be read under a command runs long,
// and a newline in it would break the row it is drawn on. The cut falls on a
// rune boundary, because a cut through a multi-byte character puts a broken
// one in the store, and it is marked, because an unmarked cut reads as a
// model that stopped mid-word.
func clampDescription(s string) string {
	s = oneLineText(s)
	runes := []rune(s)
	if len(runes) <= descriptionChars {
		return s
	}
	return strings.TrimRight(string(runes[:descriptionChars-1]), " ") + "…"
}
