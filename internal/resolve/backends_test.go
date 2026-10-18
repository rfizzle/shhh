package resolve

// The defaults this package's tests compare against are registered by these
// backends' inits, as internal/cli/backends.go registers them for the binary.
import (
	_ "github.com/rfizzle/llm/anthropic"
	_ "github.com/rfizzle/llm/compat"
	_ "github.com/rfizzle/llm/gemini"
	_ "github.com/rfizzle/llm/openai"
	_ "github.com/rfizzle/llm/openrouter"
)
