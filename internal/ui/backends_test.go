package ui

// The setup wizard's provider list reads each built-in's defaults, which
// these backends' inits register, as internal/cli/backends.go registers them
// for the binary.
import (
	_ "github.com/rfizzle/llm/anthropic"
	_ "github.com/rfizzle/llm/compat"
	_ "github.com/rfizzle/llm/gemini"
	_ "github.com/rfizzle/llm/openai"
	_ "github.com/rfizzle/llm/openai/responses"
	_ "github.com/rfizzle/llm/openrouter"
)
