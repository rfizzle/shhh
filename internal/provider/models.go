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
		"claude-fable-5-1",
		"claude-opus-5",
		"claude-sonnet-5",
		"claude-opus-4-8",
		"claude-opus-4-7",
		"claude-haiku-4-5",
	},
	"openai": {
		"gpt-5.6",
		"gpt-5.5",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.4-nano",
		"gpt-4.1",
		"gpt-4o",
	},
	// The Responses API serves the reasoning families; the chat-completions
	// models are reachable through it too.
	"openai-responses": {
		"gpt-5.6",
		"gpt-5.5",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.4-nano",
		"gpt-4.1",
		"o3",
	},
	"gemini": {
		"gemini-3.7-flash",
		"gemini-3.1-pro-preview",
		"gemini-2.5-pro",
		"gemini-2.5-flash",
	},
	"openrouter": {
		"anthropic/claude-opus-5",
		"anthropic/claude-sonnet-4.6",
		"anthropic/claude-haiku-4.5",
		"openai/gpt-5.2",
		"google/gemini-3.1-pro-preview",
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

// anthropicRouted reports whether a gateway model id names a model the
// gateway hands to the Messages API.
//
// The vendor segment is the whole test. A gateway id is `vendor/model` and
// the vendor is what it routes on, so an id under any other vendor reaches an
// endpoint that is not that API however the model is named; and a bare name
// with no vendor at all is a request the gateway itself will not route.
func anthropicRouted(model string) bool {
	vendor, _, ok := strings.Cut(strings.ToLower(strings.TrimSpace(model)), "/")
	return ok && vendor == "anthropic"
}

// ModelLister is implemented by providers whose endpoint can enumerate the
// models it actually hosts — the OpenAI GET /v1/models shape. It backs the
// interactive /model picker for endpoints the curated catalog can't know:
// Ollama, vLLM, LiteLLM, and other openai-compatible gateways.
type ModelLister interface {
	ListModels(ctx context.Context) ([]string, error)
}

// ModelWindower is implemented by a provider whose endpoint reports the
// context length it serves each model at, keyed by the id it answers requests
// under. A runtime that loaded the weights knows the number exactly, and it
// is a better source than any table: the id it reports is the local one
// ("qwen3:8b", a path to a checkout), which the public table has never seen
// and never will.
//
// The answer is the whole catalog rather than one model's window, because
// that is the one request the endpoint serves — asking per model would be the
// same round trip again for every /model switch.
type ModelWindower interface {
	ModelWindows(ctx context.Context) (map[string]int64, error)
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

// What a session assumes when the pricing table has never heard of the model.
//
// The context window drives trimming: below the threshold nothing happens,
// above it the oldest tool results are replaced with a placeholder. A session
// that assumed 32k against a model with a million was trimming away findings
// it had all the room in the world to keep, and the model rediscovered them
// the next turn. The pricing table is still the authority — it is fetched and
// current, and an endpoint reporting its own window outranks even that — and
// this is only the floor under them, by family, for the models neither could
// describe.
//
// Wrong-low costs recall, every turn, silently; wrong-high costs one request
// the endpoint refuses or truncates, visibly. So each row is the conservative
// end of its generation's published window, and which generations get a row
// depends on what an unrecognised name in the family means — see below.
// See docs/capabilities/providers.md#model-data-is-fetched-and-a-snapshot-ships.
var knownContextWindows = []struct {
	prefix string
	window int64
}{
	// The current Claudes are a million, so "claude-" is a million and the
	// generations that are not carry their own longer prefixes. The other
	// way round — a 200k catch-all with the current generation spelled out —
	// is the arrangement this replaced, and it made every unrecognised name
	// a wrong-low one: a gateway alias, a suffix nobody parsed, a model
	// released this morning.
	{"claude-", 1_000_000},
	{"claude-3", 200_000},
	{"claude-opus-4", 200_000},
	{"claude-opus-4-6", 1_000_000},
	{"claude-opus-4-7", 1_000_000},
	{"claude-opus-4-8", 1_000_000},
	{"claude-sonnet-4-5", 200_000},
	{"claude-haiku-4", 200_000},
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
	// The self-hosted families, which are the ones an openai-compatible
	// endpoint actually serves. The fetched table keys these under
	// gateway-qualified ids ("openrouter/qwen/qwen3-coder") and a local
	// runtime reports the bare weight name ("qwen3:8b"), so without a row
	// here every locally served model lands on the floor and a 128k model is
	// trimmed at 26k. Hugging Face writes the vendor into the repository
	// name, which survives the vendor strip, hence "meta-llama" beside
	// "llama".
	//
	// An older generation gets a row of its own here wherever it is both
	// still widely pulled and much smaller than the current one, which the
	// hosted families do not need. For a hosted family the floor is a
	// backstop the table stands in front of, and a name it has never heard
	// of is something announced this morning — the largest thing the family
	// has. For a local one the floor is the whole answer: the table has no
	// key for a bare weight name, the tag an older build answers to is still
	// in every library, and Ollama, which is what the default endpoint is,
	// reports no window to correct it with.
	{"llama", 128_000},
	{"llama-2", 4_096},
	{"llama-3", 8_192},
	{"llama-3.1", 128_000},
	{"llama-3.2", 128_000},
	{"llama-3.3", 128_000},
	{"meta-llama", 128_000},
	{"qwen", 32_768},
	{"deepseek", 65_536},
	{"mistral", 32_768},
	{"mixtral", 32_768},
	{"gemma", 8_192},
	{"gemma-3", 128_000},
	{"gemma-4", 262_144},
	{"phi", 4_096},
	{"phi-4", 16_384},
}

// separateDigits writes a family and the version after it the way a table
// spells them, so one row answers for both spellings. A local runtime names a
// model the way the weights are packaged — "llama3.1", "qwen2.5", "phi4" —
// and every vendor's own documentation writes the hyphen. The alternative is
// a row per spelling per generation, which is the same table to maintain
// twice and rots at twice the rate.
//
// Only a digit right after a letter is separated. Every hosted row already
// writes the hyphen, so the second spelling of those names either is the
// first one ("gpt-4o", every Claude) or matches no row at all ("o3" becomes
// "o-3", which nothing is keyed under) and the first spelling answers.
func separateDigits(name string) string {
	var out strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if i > 0 && c >= '0' && c <= '9' && name[i-1] >= 'a' && name[i-1] <= 'z' {
			out.WriteByte('-')
		}
		out.WriteByte(c)
	}
	return out.String()
}

// ContextWindowFor is the assumed context window for a model name, by family,
// for callers that could not find a real one. A vendor-qualified name
// (openrouter's "google/gemini-2.5-pro") is matched on its model half, an
// unhyphenated version ("llama3.1") matches the hyphenated row, and a "[1m]"
// marker on any name means what it says. The longest matching prefix wins, so
// a generation's own row outranks its family's.
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
	for _, spelling := range [...]string{name, separateDigits(name)} {
		for _, e := range knownContextWindows {
			if strings.HasPrefix(spelling, e.prefix) && len(e.prefix) > bestLen {
				best, bestLen = e.window, len(e.prefix)
			}
		}
	}
	if bestLen == 0 {
		return 0, false
	}
	return best, true
}
