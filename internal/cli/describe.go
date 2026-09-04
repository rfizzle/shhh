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

// snippetDescription is the line a saved snippet is filed under.
//
// explanation is the sentence the surface already showed under the command,
// and it says of that command what a summarising request would be asked to
// say — so a save that has one writes it down and finishes rather than
// standing in front of another round trip. Only a save with nothing to reuse
// pays for the request: silent mode draws no line, and an answer can come
// back without one.
// See docs/capabilities/generation.md#explanation-is-on-request-not-by-default.
func snippetDescription(ctx context.Context, p provider.Provider, command, explanation string) string {
	if explanation != "" {
		return clampDescription(explanation)
	}
	return generateDescription(ctx, p, command)
}

func generateDescription(ctx context.Context, p provider.Provider, command string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "Summarize this shell command in under 10 words. Output only the summary, no quotes or punctuation at the end."},
		{Role: provider.RoleUser, Content: command},
	}

	events, err := p.StreamCompletion(ctx, msgs, provider.CompletionOpts{})
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
