package provider

import (
	"context"
	"sort"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// knownModels is the curated per-provider model catalog backing the /model
// interactive picker. It is a convenience list, not a gate — /model <name>
// accepts any name, and providers registered without a catalog (e.g.
// openai-compatible endpoints, whose models are whatever the endpoint hosts)
// simply have no picker entries beyond the session's own model.
var knownModels = map[string][]string{
	"anthropic": {
		"claude-fable-5",
		"claude-opus-5",
		"claude-sonnet-5",
		"claude-haiku-4-5",
	},
	"openai": {
		"gpt-4o",
		"gpt-4o-mini",
		"gpt-4.1",
		"gpt-4.1-mini",
		"o3",
		"o4-mini",
	},
	// The Responses API serves the reasoning families; the chat-completions
	// models are reachable through it too.
	"openai-responses": {
		"gpt-4.1",
		"gpt-4.1-mini",
		"o3",
		"o4-mini",
	},
	"gemini": {
		"gemini-2.5-pro",
		"gemini-2.5-flash",
		"gemini-2.5-flash-lite",
	},
	"openrouter": {
		"anthropic/claude-opus-5",
		"anthropic/claude-sonnet-4-6",
		"openai/gpt-4o",
		"google/gemini-2.5-pro",
	},
}

// RegisterModels adds a catalog for a provider registered at runtime — a
// gateway profile declaring what it hosts (internal/profile). It replaces any
// existing catalog for that name.
func RegisterModels(name string, models []string) {
	if len(models) == 0 {
		delete(knownModels, normalizeName(name))
		return
	}
	knownModels[normalizeName(name)] = append([]string(nil), models...)
}

// KnownModels returns the curated model names for a registered provider, or
// nil when the provider's models can't be known ahead of time.
func KnownModels(name string) []string {
	models := knownModels[normalizeName(name)]
	if len(models) == 0 {
		return nil
	}
	return append([]string(nil), models...)
}

// ModelLister is implemented by providers whose endpoint can enumerate the
// models it actually hosts — the OpenAI GET /v1/models shape. It backs the
// interactive /model picker for endpoints the curated catalog can't know:
// Ollama, vLLM, LiteLLM, and other openai-compatible gateways.
type ModelLister interface {
	ListModels(ctx context.Context) ([]string, error)
}

// listOpenAIModels enumerates an openai-compatible endpoint's models, sorted
// and filtered to the chat-capable ones.
func listOpenAIModels(ctx context.Context, c *openai.Client) ([]string, error) {
	list, err := c.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list.Models))
	for _, mdl := range list.Models {
		if mdl.ID != "" {
			names = append(names, mdl.ID)
		}
	}
	names = chatModels(names)
	sort.Strings(names)
	return names, nil
}

// nonChatMarkers are id fragments belonging to families that can't serve a
// chat completion. The big hosted catalogs list embeddings, speech, and image
// models next to the chat ones; local runtimes list embedding models too.
var nonChatMarkers = []string{
	"embed",
	"whisper",
	"tts",
	"audio",
	"speech",
	"transcribe",
	"dall-e",
	"moderation",
	"image",
	"rerank",
	"stable-diffusion",
	"clip",
}

// chatModels drops the ids that name a non-chat family. A filter that would
// empty the list is discarded instead — an unfamiliar naming scheme should
// leave the user with every model, not none.
func chatModels(names []string) []string {
	kept := make([]string, 0, len(names))
	for _, name := range names {
		lower := strings.ToLower(name)
		drop := false
		for _, marker := range nonChatMarkers {
			if strings.Contains(lower, marker) {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, name)
		}
	}
	if len(kept) == 0 {
		return names
	}
	return kept
}

// What a session assumes when the pricing table has never heard of the model
// (S-164).
//
// The context window drives trimming: below the threshold nothing happens,
// above it the oldest tool results are replaced with a placeholder. A session
// that assumed 32k against a model with a million was trimming away findings
// it had all the room in the world to keep, and the model rediscovered them
// the next turn. The pricing table is still the authority — it is fetched and
// current — and this is only the floor under it, by family, for the models it
// has not caught up with.
//
// Wrong-low costs recall; wrong-high costs a request the API refuses. So
// these are the conservative end of each family's published window.
var knownContextWindows = []struct {
	prefix string
	window int64
}{
	{"claude-3-5", 200_000},
	{"claude-3", 200_000},
	{"claude-", 200_000},
	{"gemini-1.5-pro", 2_000_000},
	{"gemini-", 1_000_000},
	{"gpt-4.1", 1_000_000},
	{"gpt-4o", 128_000},
	{"gpt-4-turbo", 128_000},
	{"gpt-4", 8_192},
	{"gpt-5", 400_000},
	{"o1", 200_000},
	{"o3", 200_000},
	{"o4", 200_000},
}

// ContextWindowFor is the assumed context window for a model name, by family,
// for callers that could not find a real one. A vendor-qualified name
// (openrouter's "google/gemini-2.5-pro") is matched on its model half, and a
// "[1m]" marker on any name means what it says.
func ContextWindowFor(model string) (int64, bool) {
	name := strings.ToLower(strings.TrimSpace(model))
	if name == "" {
		return 0, false
	}
	if strings.Contains(name, "[1m]") {
		return 1_000_000, true
	}
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	best, bestLen := int64(0), 0
	for _, e := range knownContextWindows {
		if strings.HasPrefix(name, e.prefix) && len(e.prefix) > bestLen {
			best, bestLen = e.window, len(e.prefix)
		}
	}
	if bestLen == 0 {
		return 0, false
	}
	return best, true
}
