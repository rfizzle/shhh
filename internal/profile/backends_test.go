package profile

// The built-in names this package's tests refuse and read the defaults of
// are registered by these backends' inits, as internal/cli/backends.go
// registers them for the binary.
import (
	_ "github.com/rfizzle/llm/anthropic"
	_ "github.com/rfizzle/llm/openai"
)
