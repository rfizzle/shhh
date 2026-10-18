package cli

import (
	"slices"
	"sort"
	"testing"

	"github.com/rfizzle/llm"
)

// The built-in providers are the backends imported in backends.go, listed
// the way `shhh providers` lists them: sorted by name.
func TestShippedBackendsAreTheBuiltIns(t *testing.T) {
	var got []string
	for _, name := range llm.Available() {
		if llm.IsBuiltin(name) {
			got = append(got, name)
		}
	}
	sort.Strings(got)
	want := []string{"anthropic", "chatgpt", "gemini", "openai", "openai-compatible", "openai-responses", "openrouter"}
	if !slices.Equal(got, want) {
		t.Errorf("built-in providers = %v, want %v", got, want)
	}
}
