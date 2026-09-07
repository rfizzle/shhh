package cli

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rfizzle/shhh/internal/provider"
)

// The sentence under the command is the same sentence a summarising request
// would come back with, so a save that has one asks nobody anything: the
// name is typed, the row is written and the save is over.
func TestSnippetDescriptionReusesTheSentenceOnScreen(t *testing.T) {
	var asked []time.Time
	p := oneShotProvider{answer: "summary nobody should have paid for", asked: &asked}

	got := snippetDescription(context.Background(), p, "gpt-5", "lsof -i -P -n", "Lists every open network port and the process holding it")
	if want := "Lists every open network port and the process holding it"; got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
	if len(asked) != 0 {
		t.Errorf("a save with a sentence in hand reached the provider %d times", len(asked))
	}
}

// Silent mode draws no sentence, and neither does an answer that came back
// without one. There is nothing to reuse in either, so the save falls back to
// the request it always made.
func TestSnippetDescriptionAsksWhenNothingWasShown(t *testing.T) {
	var asked []time.Time
	p := oneShotProvider{answer: "  list open network ports  ", asked: &asked}

	got := snippetDescription(context.Background(), p, "gpt-5", "lsof -i -P -n", "")
	if want := "list open network ports"; got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
	if len(asked) != 1 {
		t.Errorf("a save with nothing to reuse asked the provider %d times, want 1", len(asked))
	}
}

// A provider with nothing to say leaves the snippet undescribed rather than
// filed under an empty phrase; the caller writes no row for that.
func TestSnippetDescriptionIsEmptyWhenTheRequestFails(t *testing.T) {
	var asked []time.Time
	p := oneShotProvider{asked: &asked}

	if got := snippetDescription(context.Background(), p, "gpt-5", "lsof -i -P -n", ""); got != "" {
		t.Errorf("a refused request produced %q", got)
	}
}

// The explanation was written to be read under a command, not inside a
// column, so it arrives wrapped and it arrives long. Both are the store's
// problem before they are the listing's.
func TestSnippetDescriptionIsClampedToTheColumn(t *testing.T) {
	wrapped := snippetDescription(context.Background(), nil, "gpt-5", "", "Lists every open port\nand the process holding it")
	if want := "Lists every open port and the process holding it"; wrapped != want {
		t.Errorf("wrapped explanation = %q, want %q", wrapped, want)
	}

	// A multi-byte character straddling the cut is the case a byte slice
	// gets wrong: the row comes back with half a rune in it.
	long := snippetDescription(context.Background(), nil, "gpt-5", "", strings.Repeat("é", 200))
	if n := utf8.RuneCountInString(long); n > descriptionChars {
		t.Errorf("clamped explanation is %d runes, want at most %d", n, descriptionChars)
	}
	if !utf8.ValidString(long) {
		t.Errorf("the clamp cut through a character: %q", long)
	}
	if !strings.HasSuffix(long, "…") {
		t.Errorf("a clamped explanation should say it was cut, got %q", long)
	}
}

// The point of either branch is the row it writes. What the save hands the
// store is what `shhh snippets` reads back, and a save that fell back to
// asking files the snippet just as completely as one that reused a sentence.
func TestSavedSnippetHoldsTheDescription(t *testing.T) {
	const command = "lsof -i -P -n"
	for _, c := range []struct {
		name        string
		explanation string
		want        string
	}{
		{"reused", "Lists every open network port", "Lists every open network port"},
		{"asked for", "", "list open network ports"},
	} {
		t.Run(c.name, func(t *testing.T) {
			db := fixtureStore(t)
			var asked []time.Time
			p := oneShotProvider{answer: "list open network ports", asked: &asked}

			if err := db.SaveSnippet(c.name, command); err != nil {
				t.Fatalf("save the snippet: %v", err)
			}
			desc := snippetDescription(context.Background(), p, "gpt-5", command, c.explanation)
			if err := db.UpdateSnippetDescription(c.name, desc); err != nil {
				t.Fatalf("write the description: %v", err)
			}

			s, err := db.GetSnippet(c.name)
			if err != nil {
				t.Fatalf("read the snippet back: %v", err)
			}
			if s.Description != c.want {
				t.Errorf("the row holds description %q, want %q", s.Description, c.want)
			}
			if s.Command != command {
				t.Errorf("the row holds command %q", s.Command)
			}
		})
	}
}

// describeProvider answers a description request and keeps the options it
// was asked under.
type describeProvider struct {
	name string
	opts *provider.CompletionOpts
}

func (p describeProvider) Name() string { return p.name }

func (p describeProvider) StreamCompletion(_ context.Context, _ []provider.Message, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	*p.opts = opts
	events := make(chan provider.StreamEvent, 2)
	events <- provider.StreamEvent{Token: "list open network ports"}
	events <- provider.StreamEvent{Done: true}
	close(events)
	return events, nil
}

// The description is a bounded call and is sent as one. Sent bare it went out
// with no model, no ceiling and no effort — which on a dialect whose default
// ceiling is sixty-four thousand tokens, against a model that thinks whether
// or not it was asked, is a ten-word phrase bought at the price of a round of
// the session's own work.
func TestSnippetDescriptionIsAskedAsABoundedCall(t *testing.T) {
	var opts provider.CompletionOpts
	p := describeProvider{name: "anthropic", opts: &opts}

	if got := snippetDescription(context.Background(), p, "claude-opus-5", "lsof -i -P -n", ""); got != "list open network ports" {
		t.Fatalf("description = %q", got)
	}
	if want := provider.Defaults("anthropic").CheapModel; opts.Model != want {
		t.Errorf("model = %q, want the provider's small one, %q", opts.Model, want)
	}
	if opts.MaxTokens != descriptionMaxTokens {
		t.Errorf("max tokens = %d, want %d", opts.MaxTokens, descriptionMaxTokens)
	}
	if opts.Effort != provider.EffortLow {
		t.Errorf("effort = %v, want %v", opts.Effort, provider.EffortLow)
	}
}

// A provider that names no small model of its own is answered on the
// session's, which is the same rule every other bounded call follows.
func TestSnippetDescriptionFallsBackToTheSessionModel(t *testing.T) {
	var opts provider.CompletionOpts
	p := describeProvider{name: "no-cheap-model-of-its-own", opts: &opts}

	if got := snippetDescription(context.Background(), p, "llama3", "lsof -i -P -n", ""); got == "" {
		t.Fatal("the description should still be asked for")
	}
	if opts.Model != "llama3" {
		t.Errorf("model = %q, want the session's own", opts.Model)
	}
}
