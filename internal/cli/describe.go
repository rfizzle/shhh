package cli

import (
	"context"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

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

	desc := strings.TrimSpace(sb.String())
	if len(desc) > 100 {
		desc = desc[:100]
	}
	return desc
}
