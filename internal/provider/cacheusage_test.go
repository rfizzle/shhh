package provider

import (
	"context"
	"net/http"
	"testing"
)

// The Messages API says which lifetime it wrote the cache for. A request
// asking for an hour that the provider honoured as five minutes is read here
// and nowhere else.
func TestAnthropic_UsageSplitsTheLeaseConfirmed(t *testing.T) {
	for _, tc := range []struct {
		name, usage string
		want        Usage
	}{
		{"an hour honoured",
			`"usage":{"input_tokens":12,"output_tokens":1,"cache_read_input_tokens":900,"cache_creation_input_tokens":88,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":88}}`,
			Usage{PromptTokens: 1000, CompletionTokens: 5, CachedTokens: 900, CacheCreationTokens: 88, CacheCreation1h: 88,
				Reports: FieldCacheRead | FieldCacheCreation | FieldCacheLease}},
		{"an hour written as five minutes",
			`"usage":{"input_tokens":12,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":988,"cache_creation":{"ephemeral_5m_input_tokens":988,"ephemeral_1h_input_tokens":0}}`,
			Usage{PromptTokens: 1000, CompletionTokens: 5, CacheCreationTokens: 988, CacheCreation5m: 988,
				Reports: FieldCacheRead | FieldCacheCreation | FieldCacheLease}},
		{"a gateway that strips the fields",
			`"usage":{"input_tokens":1000,"output_tokens":1}`,
			Usage{PromptTokens: 1000, CompletionTokens: 5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := anthropicSSEServer(t, func(w http.ResponseWriter) {
				sseEvent(w, "message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-opus-5","stop_reason":null,`+tc.usage+`}}`)
				sseEvent(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
				sseEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`)
				sseEvent(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
				sseEvent(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}`)
				sseEvent(w, "message_stop", `{"type":"message_stop"}`)
			})
			defer srv.Close()
			p := newTestAnthropic(ResolveOpts{APIKey: "sk-test", BaseURL: srv.URL})
			events, err := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, CompletionOpts{})
			if err != nil {
				t.Fatal(err)
			}
			_, _, usage, streamErr := drainAnthropic(t, events)
			if streamErr != nil || usage == nil {
				t.Fatalf("usage %v, err %v", usage, streamErr)
			}
			if *usage != tc.want {
				t.Errorf("usage = %+v, want %+v", *usage, tc.want)
			}
		})
	}
}

func TestCacheFields_NamesReadBack(t *testing.T) {
	all := FieldCacheRead | FieldCacheCreation | FieldCacheLease | FieldCachedTokens | FieldCachedContent
	if got := CacheFieldsNamed(all.Names()); got != all {
		t.Errorf("round trip: %v", got)
	}
	if got := CacheFieldsNamed([]string{"cached_tokens", "something_else"}); got != FieldCachedTokens {
		t.Errorf("an unknown name: %v", got)
	}
}
