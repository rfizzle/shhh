package todo

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// fakeProvider answers one request with a scripted stream.
type fakeProvider struct {
	events []provider.StreamEvent
	err    error
	got    provider.CompletionOpts
	prompt string
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) StreamCompletion(_ context.Context, msgs []provider.Message, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	f.got = opts
	f.prompt = msgs[0].Content
	if f.err != nil {
		return nil, f.err
	}
	ch := make(chan provider.StreamEvent, len(f.events)+1)
	for _, ev := range f.events {
		ch <- ev
	}
	ch <- provider.StreamEvent{Done: true, Usage: &provider.Usage{PromptTokens: 10, CompletionTokens: 5}}
	close(ch)
	return ch, nil
}

const proposalsJSON = `{"items": [
	{"title": "Show the backlog in the rail", "kind": "story", "priority": "High", "size": "m",
	 "story": "As a user, I want the list visible so that what is next never needs asking.",
	 "acceptance_criteria": ["A block appears", "Four rows then a count"], "tasks": ["todoBlock", "feed it"],
	 "tests": ["go test ./internal/ui/components"], "notes": ["Passive block, no keys\nnever"], "depends_on": ["Build the store"]},
	{"title": "Build the store", "kind": "chore", "priority": "medium", "size": "S", "acceptance_criteria": ["loads"]},
	{"title": "", "kind": "bug"}
]}`

func TestExtract_ToolCallIsRead(t *testing.T) {
	fp := &fakeProvider{events: []provider.StreamEvent{{ToolCalls: []provider.ToolCall{{Name: ExtractToolName, Arguments: proposalsJSON}}}}}
	e := NewExtractor(fp, ExtractConfig{Model: "m"})
	r := e.Extract(context.Background(), ExtractRequest{Instructions: []string{"do the thing"}, Existing: []string{"old — Old one"}})
	if r.Failed || len(r.Proposals) != 2 {
		t.Fatalf("result = %+v", r)
	}
	p := r.Proposals[0]
	if p.Priority != "high" || p.Size != "M" || p.Notes[0] != "Passive block, no keys never" || p.DependsOn[0] != "Build the store" {
		t.Errorf("normalised proposal = %+v", p)
	}
	if r.Usage.PromptTokens != 10 || r.Model != "m" {
		t.Errorf("accounting = %+v", r)
	}
	if fp.got.Tools[0].Name != ExtractToolName || !strings.Contains(fp.prompt, "UNTRUSTED DIGEST") || !strings.Contains(fp.prompt, "old — Old one") {
		t.Errorf("request = %+v / %q", fp.got, fp.prompt)
	}
}

func TestExtract_TextFallbackAndFailures(t *testing.T) {
	fp := &fakeProvider{events: []provider.StreamEvent{{Token: "Here you go:\n```json\n" + proposalsJSON + "\n```"}}}
	r := NewExtractor(fp, ExtractConfig{Model: "m"}).Extract(context.Background(), ExtractRequest{})
	if r.Failed || len(r.Proposals) != 2 {
		t.Fatalf("text fallback = %+v", r)
	}
	cases := map[string]*fakeProvider{
		"provider error": {err: errors.New("boom")},
		"stream error":   {events: []provider.StreamEvent{{Err: errors.New("mid")}}},
		"nothing":        {events: []provider.StreamEvent{{Token: "no items here"}}},
	}
	for name, fp := range cases {
		r := NewExtractor(fp, ExtractConfig{Model: "m"}).Extract(context.Background(), ExtractRequest{})
		if !r.Failed || r.Err == "" {
			t.Errorf("%s: result = %+v", name, r)
		}
	}
	var none *Extractor
	if r := none.Extract(context.Background(), ExtractRequest{}); !r.Failed {
		t.Error("a nil extractor should fail soft")
	}
}

func TestProposal_ItemRendersTheSections(t *testing.T) {
	ps, _ := ParseProposals(proposalsJSON)
	it := ps[0].Item("show-the-backlog-in-the-rail", "2026-08-30", "2026-08-30 12:00:00")
	back, err := Parse("/x/show-the-backlog-in-the-rail.md", Render(it))
	if err != nil {
		t.Fatal(err)
	}
	if back.Priority != PriorityHigh || back.Size != SizeM || back.Kind != KindStory || back.Session != "2026-08-30 12:00:00" {
		t.Errorf("header = %+v", back)
	}
	for _, want := range []string{"As a user, I want", "## Acceptance criteria\n- [ ] A block appears", "## Tasks\n- [ ] todoBlock", "## Tests\n- go test", "## Notes\n- Passive"} {
		if !strings.Contains(back.Body, want) {
			t.Errorf("body lacks %q:\n%s", want, back.Body)
		}
	}
	bare := ps[1].Item("build-the-store", "", "")
	if bare.Kind != KindChore || bare.Priority != PriorityMedium || strings.Contains(bare.Body, "## Tasks") {
		t.Errorf("bare item = %+v", bare)
	}
}

func TestParseProposals_Bounds(t *testing.T) {
	var items []string
	for i := 0; i < MaxProposals+3; i++ {
		items = append(items, `{"title": "t`+strings.Repeat("x", 500)+`"}`)
	}
	ps, ok := ParseProposals(`{"items": [` + strings.Join(items, ",") + `]}`)
	if !ok || len(ps) != MaxProposals || len([]rune(ps[0].Title)) != maxProposalLine {
		t.Errorf("bounds: %d proposals, title %d runes", len(ps), len([]rune(ps[0].Title)))
	}
	if _, ok := ParseProposals("not json"); ok {
		t.Error("garbage parsed")
	}
}
