package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"testing"
)

// gatewayStream is a text round whose usage chunk is the one given, served
// by a gateway that keeps the body of the request it was sent.
func gatewayStream(t *testing.T, usageChunk string) (Usage, []byte) {
	t.Helper()
	var body []byte
	srv := providerTestHTTP.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			`{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
			`{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			usageChunk,
		} {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	p := newTestCompat(srv.URL+"/v1", "anthropic/claude-sonnet-5-5")
	ch, err := p.StreamCompletion(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, CompletionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	_, final := drainFragments(t, ch)
	if final.Usage == nil {
		t.Fatal("the round reported no usage")
	}
	return *final.Usage, body
}

// A LiteLLM gateway forwarding to the Messages API reports what it wrote into
// the cache at the top of the usage, beside the read OpenAI's own shape
// carries. Read as fresh input, every write was billed at the base rate where
// the gateway charged the write premium.
func TestOpenAI_StreamCompletion_ReadsAGatewaysCacheWrites(t *testing.T) {
	usage, _ := gatewayStream(t, `{"id":"c","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":2400,"completion_tokens":9,"total_tokens":2409,"prompt_tokens_details":{"cached_tokens":1800},"cache_creation_input_tokens":500}}`)
	want := Usage{PromptTokens: 2400, CompletionTokens: 9, CachedTokens: 1800, CacheCreationTokens: 500,
		Reports: FieldCachedTokens | FieldCacheCreation}
	if usage != want {
		t.Errorf("usage = %+v, want %+v", usage, want)
	}
	if got := usage.Reports.Names(); !reflect.DeepEqual(got, []string{"cache_creation_input_tokens", "cached_tokens"}) {
		t.Errorf("reports %v", got)
	}

	// A gateway that counts the write beside the prompt rather than inside
	// it is folded the way the Messages API's converter folds it, so the
	// fresh part is never priced below nothing.
	beside, _ := gatewayStream(t, `{"id":"c","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":9,"prompt_tokens_details":{"cached_tokens":80},"cache_creation_input_tokens":500}}`)
	if beside.PromptTokens != 600 || beside.PromptTokens-beside.CachedTokens-beside.CacheCreationTokens != 20 {
		t.Errorf("a write counted beside the prompt: %+v", beside)
	}
}

// Without the field nothing changes: the request is the request it was, and
// the usage is the usage the client's own struct reads.
func TestOpenAI_StreamCompletion_UsageUnchangedWithoutTheField(t *testing.T) {
	const tail = `"usage":{"prompt_tokens":2400,"completion_tokens":9,"total_tokens":2409,"prompt_tokens_details":{"cached_tokens":1800}`
	without, sentWithout := gatewayStream(t, `{"id":"c","object":"chat.completion.chunk","choices":[],`+tail+`}}`)
	_, sentWith := gatewayStream(t, `{"id":"c","object":"chat.completion.chunk","choices":[],`+tail+`,"cache_creation_input_tokens":500}}`)
	if !bytes.Equal(sentWithout, sentWith) || len(sentWith) == 0 {
		t.Errorf("the request changed with the response:\n%s\n%s", sentWithout, sentWith)
	}
	want := Usage{PromptTokens: 2400, CompletionTokens: 9, CachedTokens: 1800, Reports: FieldCachedTokens}
	if without != want {
		t.Errorf("usage = %+v, want %+v", without, want)
	}

	none, _ := gatewayStream(t, `{"id":"c","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":2400,"completion_tokens":9}}`)
	if none != (Usage{PromptTokens: 2400, CompletionTokens: 9}) || none.Reports.Names() != nil {
		t.Errorf("a gateway that sends no cache field reports one: %+v", none)
	}
}
