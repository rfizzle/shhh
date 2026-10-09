package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// One request on the configured model, forbidding any tool, with the
// instruction and the evidence apart; the evidence is the pattern's facts and
// its lines, bounded, and nothing else.
func TestPatternWriter_WordsAMemoryFromTheFactsAndLinesAlone(t *testing.T) {
	p := &fakeClassifierProvider{fn: func(_ int, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		if opts.Model != "small" || opts.ToolChoice != provider.ToolChoiceNone || len(opts.Tools) != 0 {
			t.Errorf("opts = %+v", opts)
		}
		return eventsOf(provider.StreamEvent{Token: `{"text":"internal/cli/session.go\nbuilds every session."}`, Done: true}), nil
	}}
	var lines []string
	for range 30 {
		lines = append(lines, "read_file internal/cli/session.go")
	}
	v := NewPatternWriter(p, PatternsConfig{Model: "small"}).Word(context.Background(),
		PatternRequest{Want: WordMemory, Facts: "internal/cli/session.go read in 4 sessions", Lines: lines})
	if v.Failed || v.Text != "internal/cli/session.go builds every session." {
		t.Fatalf("verdict = %+v", v)
	}
	if len(p.msgs) != 2 || p.msgs[0].Role != provider.RoleSystem {
		t.Fatalf("messages = %+v", p.msgs)
	}
	ev := p.msgs[1].Content
	if !strings.Contains(ev, `"pattern_facts":"internal/cli/session.go read in 4 sessions"`) ||
		strings.Count(ev, "read_file internal/cli/session.go") != maxPatternLines {
		t.Fatalf("evidence = %s", ev)
	}
}

// A skill's parts are made fit for the file: a name in the specification's
// alphabet, one-line steps, and an answer missing a part is not usable.
func TestPatternWriter_ASkillAnswerIsBoundedOrRefused(t *testing.T) {
	v, ok := ParsePatternWording(WordSkill, "```json\n"+`{"name":"Check, Then Build!","description":"Use when the tree changed","steps":["go vet ./...","","go\nbuild ./..."]}`+"\n```")
	if !ok || v.Name != "check-then-build" || len(v.Steps) != 2 || v.Steps[1] != "go build ./..." {
		t.Fatalf("parsed %+v (%v)", v, ok)
	}
	for _, bad := range []string{`{"name":"x","steps":["a"]}`, `{"name":"!!","description":"d","steps":["a"]}`, `not json`} {
		if _, ok := ParsePatternWording(WordSkill, bad); ok {
			t.Errorf("%s was usable", bad)
		}
	}
	if _, ok := ParsePatternWording(WordMemory, `{"text":"  "}`); ok {
		t.Error("an empty memory was usable")
	}
}

// A request that fails, or a writer with no model, is a failed reading, and
// the caller keeps the code's words.
func TestPatternWriter_AFailedReadingIsFailed(t *testing.T) {
	p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		return nil, errors.New("boom")
	}}
	if v := NewPatternWriter(p, PatternsConfig{Model: "small"}).Word(context.Background(), PatternRequest{Want: WordMemory}); !v.Failed {
		t.Fatalf("verdict = %+v", v)
	}
	if v := NewPatternWriter(p, PatternsConfig{}).Word(context.Background(), PatternRequest{Want: WordMemory}); !v.Failed || p.calls != 1 {
		t.Fatalf("an unconfigured writer asked: %+v, %d calls", v, p.calls)
	}
}
