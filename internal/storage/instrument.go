package storage

import (
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

type StreamMetrics struct {
	TTFT      *time.Duration
	Duration  *time.Duration
	TokensIn  *int64
	TokensOut *int64
	Success   bool
}

func InstrumentStream(events <-chan provider.StreamEvent) (<-chan provider.StreamEvent, *StreamMetrics) {
	metrics := &StreamMetrics{}
	out := make(chan provider.StreamEvent)
	go func() {
		defer close(out)
		start := time.Now()
		firstToken := false
		for ev := range events {
			if ev.Token != "" && !firstToken {
				firstToken = true
				ttft := time.Since(start)
				metrics.TTFT = &ttft
			}
			if ev.Done {
				dur := time.Since(start)
				metrics.Duration = &dur
				metrics.Success = ev.Err == nil
			}
			if ev.Usage != nil {
				in := int64(ev.Usage.PromptTokens)
				out := int64(ev.Usage.CompletionTokens)
				metrics.TokensIn = &in
				metrics.TokensOut = &out
			}
			out <- ev
		}
		if metrics.Duration == nil {
			dur := time.Since(start)
			metrics.Duration = &dur
		}
	}()
	return out, metrics
}
