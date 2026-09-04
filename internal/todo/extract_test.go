package todo

import (
	"bytes"
	"context"
	"encoding/json"
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
	// system is the instruction message and prompt the user turn that
	// carries the untrusted digest; the split is what the test asserts.
	system string
	prompt string
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) StreamCompletion(_ context.Context, msgs []provider.Message, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	f.got = opts
	f.system = msgs[0].Content
	f.prompt = msgs[len(msgs)-1].Content
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

// The instructions go in the dialect's own instruction channel and only the
// untrusted digest is in the user turn, so a digest that tries to give
// orders is not sitting in the same message as the orders that count. The
// reading also asks for a shallow thought outright and leaves it room under
// the ceiling: off means the model's own depth, which on a model that thinks
// by default is the whole ceiling and no proposals.
func TestExtract_InstructionsAreSeparateFromTheDigest(t *testing.T) {
	fp := &fakeProvider{events: []provider.StreamEvent{{ToolCalls: []provider.ToolCall{{Name: ExtractToolName, Arguments: proposalsJSON}}}}}
	NewExtractor(fp, ExtractConfig{Model: "m"}).Extract(context.Background(), ExtractRequest{Instructions: []string{"do the thing"}})
	if !strings.Contains(fp.system, "You turn a coding session into backlog items") {
		t.Errorf("system message = %q", fp.system)
	}
	if strings.Contains(fp.system, "UNTRUSTED DIGEST") || strings.Contains(fp.prompt, "You turn a coding session") {
		t.Errorf("the halves are mixed: system=%q user=%q", fp.system, fp.prompt)
	}
	if fp.got.Effort != provider.EffortLow {
		t.Errorf("effort = %v", fp.got.Effort)
	}
	if fp.got.MaxTokens != DefaultExtractMaxTokens {
		t.Errorf("max tokens = %d", fp.got.MaxTokens)
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

// The reading is asked for twice on one request — as a schema the answer
// must match and as the tool a model that takes no schema is offered — and
// the two describe one shape. A model answering to the schema sends the
// object alone, which the same parser reads.
func TestExtract_OffersASchemaAndTheToolTogether(t *testing.T) {
	fp := &fakeProvider{events: []provider.StreamEvent{{Token: proposalsJSON}}}
	r := NewExtractor(fp, ExtractConfig{Model: "m"}).Extract(context.Background(), ExtractRequest{})
	if r.Failed || len(r.Proposals) != 2 {
		t.Fatalf("result = %+v", r)
	}
	if fp.got.ResponseSchema == nil || fp.got.ResponseSchema.Name != ExtractToolName {
		t.Fatalf("response schema = %+v", fp.got.ResponseSchema)
	}
	if !bytes.Equal(fp.got.ResponseSchema.Schema, extractSchema) {
		t.Errorf("the schema and the tool describe one shape, got %s", fp.got.ResponseSchema.Schema)
	}
	if len(fp.got.Tools) != 1 || fp.got.Tools[0].Name != ExtractToolName {
		t.Errorf("the tool must still be offered, got %+v", fp.got.Tools)
	}
}

// Strict validation is refused on a schema that leaves an object open or
// names a key it does not require, so every object in this one does both.
func TestExtractSchema_ClosesAndRequiresEverything(t *testing.T) {
	var shape map[string]any
	if err := json.Unmarshal(extractSchema, &shape); err != nil {
		t.Fatal(err)
	}
	var walk func(node map[string]any, path string)
	walk = func(node map[string]any, path string) {
		if node["type"] == "object" {
			props, _ := node["properties"].(map[string]any)
			required, _ := node["required"].([]any)
			if node["additionalProperties"] != false {
				t.Errorf("%s: the object must close", path)
			}
			if len(required) != len(props) {
				t.Errorf("%s: %d of %d keys are required", path, len(required), len(props))
			}
			for name, child := range props {
				if c, ok := child.(map[string]any); ok {
					walk(c, path+"."+name)
				}
			}
		}
		if items, ok := node["items"].(map[string]any); ok {
			walk(items, path+"[]")
		}
	}
	walk(shape, "")
}

const draftJSON = `{"items": [
	{"title": "Give the cache a lifetime", "kind": "Story", "priority": "high", "size": "s",
	 "story": "As a user, I want entries to expire so that stale answers stop being served.",
	 "acceptance_criteria": ["An entry past its lifetime is a miss"], "tasks": ["read the setting"],
	 "tests": ["go test ./internal/cache"], "notes": ["the default is an hour"], "depends_on": ["cache-store"]},
	{"title": "A second item nobody asked for", "kind": "chore"}
]}`

func TestDraft_OneItemFromASentence(t *testing.T) {
	fp := &fakeProvider{events: []provider.StreamEvent{{ToolCalls: []provider.ToolCall{{Name: ExtractToolName, Arguments: draftJSON}}}}}
	d := NewDrafter(fp, ExtractConfig{Model: "m"})
	r := d.Draft(context.Background(), DraftRequest{
		Sentence: "the cache never expires anything",
		Existing: []string{"cache-store — Store the entries"},
	})
	if r.Failed {
		t.Fatalf("result = %+v", r)
	}
	// The prompt asks for one item and only one is kept: a model that
	// answered with two answered a question nobody asked.
	if len(r.Proposals) != 1 {
		t.Fatalf("proposals = %d, want 1", len(r.Proposals))
	}
	p := r.Proposals[0]
	if p.Kind != "story" || p.Size != "S" || p.DependsOn[0] != "cache-store" {
		t.Errorf("normalised proposal = %+v", p)
	}
	if !strings.Contains(fp.prompt, "UNTRUSTED REQUEST") ||
		!strings.Contains(fp.prompt, "the cache never expires anything") ||
		!strings.Contains(fp.prompt, "cache-store — Store the entries") {
		t.Errorf("request = %q", fp.prompt)
	}
	// The sentence travels as data in the user turn; the instruction is the
	// system message and says nothing about this request.
	if strings.Contains(fp.system, "the cache never expires anything") {
		t.Errorf("the sentence reached the instruction channel: %q", fp.system)
	}
}

func TestDraft_NothingToDraftFrom(t *testing.T) {
	fp := &fakeProvider{}
	d := NewDrafter(fp, ExtractConfig{Model: "m"})
	r := d.Draft(context.Background(), DraftRequest{Sentence: "   "})
	if !r.Failed || !strings.Contains(r.Err, "nothing to draft") {
		t.Fatalf("result = %+v", r)
	}
	if fp.got.Model != "" {
		t.Error("an empty sentence still reached the provider")
	}
}

func TestDraft_DisabledAndEmptyAnswer(t *testing.T) {
	var off *Drafter
	if off.Enabled() {
		t.Error("a nil drafter reports enabled")
	}
	if r := off.Draft(context.Background(), DraftRequest{Sentence: "x"}); !r.Failed {
		t.Error("a nil drafter drafted something")
	}
	fp := &fakeProvider{events: []provider.StreamEvent{{Token: "sorry, no."}}}
	d := NewDrafter(fp, ExtractConfig{Model: "m"})
	if r := d.Draft(context.Background(), DraftRequest{Sentence: "x"}); !r.Failed || !strings.Contains(r.Err, "proposed nothing") {
		t.Errorf("result = %+v", r)
	}
}
