package cli

// The backends shhh ships. Each registers its provider names from its own
// init, so these imports are the whole list `shhh providers` shows, and a
// backend left out here is a provider name that does not resolve.
import (
	_ "github.com/rfizzle/llm/anthropic"  // anthropic
	_ "github.com/rfizzle/llm/compat"     // openai-compatible
	_ "github.com/rfizzle/llm/gemini"     // gemini
	_ "github.com/rfizzle/llm/openai"     // openai, openai-responses, chatgpt
	_ "github.com/rfizzle/llm/openrouter" // openrouter
)
