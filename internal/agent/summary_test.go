package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

func summaryCall(args string) provider.StreamEvent {
	return provider.StreamEvent{
		ToolCalls: []provider.ToolCall{{ID: "s1", Name: SummaryToolName, Arguments: args}},
		Usage:     &provider.Usage{PromptTokens: 900, CompletionTokens: 40},
		Done:      true,
	}
}

func testSummaryRequest() SummaryRequest {
	return SummaryRequest{
		Target:   "make the round limit a checkpoint",
		Activity: []string{"read_file · agent/loop.go · ok", "edit_file · agent/loop.go · ok"},
		Changes:  "2 files · +21 −4",
		Round:    12,
		Elapsed:  90 * time.Second,
	}
}

func TestSummarizer_ToolCallReading(t *testing.T) {
	p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		return eventsOf(summaryCall(`{"summary":"Adding the sentinel to the loop.","state":"on_target"}`)), nil
	}}
	v := NewSummarizer(p, SummaryConfig{Model: "m"}).Summarize(context.Background(), testSummaryRequest())
	if v.Failed {
		t.Fatalf("expected a clean reading, got %+v", v)
	}
	if v.Text != "Adding the sentinel to the loop." || v.State != SummaryOnTarget {
		t.Fatalf("reading = %+v", v)
	}
	if v.Round != 12 {
		t.Fatalf("the verdict carries the round it read: got %d", v.Round)
	}
	if v.Model != "m" {
		t.Fatalf("the verdict names the model that wrote it: got %q", v.Model)
	}
	if v.Usage.PromptTokens != 900 || v.Usage.CompletionTokens != 40 {
		t.Fatalf("usage should be captured, got %+v", v.Usage)
	}
}

// An "on target" reading has no reason row on the rail, so a model that
// narrates one anyway does not get to put it there.
func TestSummarizer_OnTargetDropsItsReason(t *testing.T) {
	p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		return eventsOf(summaryCall(`{"summary":"Still on the tests.","state":"on_target","reason":"everything is fine"}`)), nil
	}}
	v := NewSummarizer(p, SummaryConfig{Model: "m"}).Summarize(context.Background(), testSummaryRequest())
	if v.Reason != "" {
		t.Fatalf("an on-target reading keeps no reason, got %q", v.Reason)
	}
}

func TestSummarizer_OffTargetKeepsItsReason(t *testing.T) {
	p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		return eventsOf(summaryCall(`{"summary":"Rewriting the README.","state":"off_target","reason":"docs were not asked for"}`)), nil
	}}
	v := NewSummarizer(p, SummaryConfig{Model: "m"}).Summarize(context.Background(), testSummaryRequest())
	if v.State != SummaryOffTarget || !v.State.Drifting() {
		t.Fatalf("state = %v", v.State)
	}
	if v.Reason != "docs were not asked for" {
		t.Fatalf("reason = %q", v.Reason)
	}
}

// The state is a closed set: anything else is the reading that could not tell,
// never a fourth state the rail has no row for and steering cannot branch on.
func TestSummarizer_UnknownStateBecomesUnclear(t *testing.T) {
	p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		return eventsOf(summaryCall(`{"summary":"Working.","state":"probably_fine"}`)), nil
	}}
	v := NewSummarizer(p, SummaryConfig{Model: "m"}).Summarize(context.Background(), testSummaryRequest())
	if v.Failed || v.State != SummaryUncertain {
		t.Fatalf("expected an unclear but usable reading, got %+v", v)
	}
	if v.State.Drifting() {
		t.Fatal("an unclear reading is not a drift signal")
	}
}

// The block was drawn for two short sentences, and a length the block depends
// on is not a length the model gets to decide.
func TestSummarizer_ClampsWhatTheModelWrote(t *testing.T) {
	long, reason := strings.Repeat("word ", 200), strings.Repeat("because ", 60)
	args, err := json.Marshal(map[string]string{"summary": long, "state": "off_target", "reason": reason})
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		return eventsOf(summaryCall(string(args))), nil
	}}
	v := NewSummarizer(p, SummaryConfig{Model: "m"}).Summarize(context.Background(), testSummaryRequest())
	if n := len([]rune(v.Text)); n > maxSummaryText {
		t.Fatalf("summary text = %d runes, want <= %d", n, maxSummaryText)
	}
	if n := len([]rune(v.Reason)); n > maxSummaryReason {
		t.Fatalf("summary reason = %d runes, want <= %d", n, maxSummaryReason)
	}
	if !strings.HasSuffix(v.Text, "…") {
		t.Fatal("a clamped summary says it was clamped")
	}
}

func TestSummarizer_TextFallbacks(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		want  SummaryState
		text  string
	}{
		{"line form", "on_target: Reading the loop.", SummaryOnTarget, "Reading the loop."},
		{"spaced state", "off target - Editing the README.", SummaryOffTarget, "Editing the README."},
		{"fenced json", "```json\n{\"summary\":\"Fixing tests.\",\"state\":\"on_target\"}\n```", SummaryOnTarget, "Fixing tests."},
		{"bare prose", "Still wiring the pause into the model.", SummaryUncertain, "Still wiring the pause into the model."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
				return eventsOf(provider.StreamEvent{Token: tc.reply, Done: true}), nil
			}}
			v := NewSummarizer(p, SummaryConfig{Model: "m"}).Summarize(context.Background(), testSummaryRequest())
			if v.Failed {
				t.Fatalf("expected a usable reading, got %+v", v)
			}
			if v.Text != tc.text || v.State != tc.want {
				t.Fatalf("got %q / %v, want %q / %v", v.Text, v.State, tc.text, tc.want)
			}
		})
	}
}

// Every failure path fails soft — the caller keeps what it had — and says why.
func TestSummarizer_FailsSoft(t *testing.T) {
	cases := []struct {
		name string
		cfg  SummaryConfig
		fn   func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error)
	}{
		{"no model", SummaryConfig{}, nil},
		{"disabled", SummaryConfig{Model: "m", Disabled: true}, nil},
		{"request error", SummaryConfig{Model: "m"}, func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
			return nil, errors.New("overloaded")
		}},
		{"stream error", SummaryConfig{Model: "m"}, func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
			return eventsOf(provider.StreamEvent{Err: errors.New("dropped")}), nil
		}},
		{"empty reply", SummaryConfig{Model: "m"}, func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
			return eventsOf(provider.StreamEvent{Done: true}), nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fn := tc.fn
			if fn == nil {
				fn = func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
					t.Fatal("no request should have been made")
					return nil, nil
				}
			}
			v := NewSummarizer(&fakeClassifierProvider{fn: fn}, tc.cfg).
				Summarize(context.Background(), testSummaryRequest())
			if !v.Failed {
				t.Fatalf("expected a failed reading, got %+v", v)
			}
			if v.Err == "" {
				t.Fatal("a failed reading says why")
			}
			if v.Text != "" {
				t.Fatalf("a failed reading carries no text, got %q", v.Text)
			}
		})
	}
}

func TestSummarizer_DisabledIsNotEnabled(t *testing.T) {
	p := &fakeClassifierProvider{}
	if NewSummarizer(p, SummaryConfig{Model: "m", Disabled: true}).Enabled() {
		t.Fatal("a disabled summarizer is not enabled")
	}
	if NewSummarizer(p, SummaryConfig{}).Enabled() {
		t.Fatal("a summarizer with no model is not enabled")
	}
	if !NewSummarizer(p, SummaryConfig{Model: "m"}).Enabled() {
		t.Fatal("a configured summarizer is enabled")
	}
	var nilSum *Summarizer
	if nilSum.Enabled() || !nilSum.Config().Disabled {
		t.Fatal("a nil summarizer reads as disabled rather than panicking")
	}
}

// The digest is the security boundary: it carries what was called and how it
// came back, never what a tool returned. A page the agent fetched must not be
// able to write the summary — and, once steering exists, to steer.
func TestSummaryRequest_DigestCarriesNoToolOutput(t *testing.T) {
	req := testSummaryRequest()
	req.Activity = append(req.Activity, SummaryActivity("web_fetch", "https://example.com/page", "ok"))
	req.Previous = "Reading the loop."
	req.Assistant = "Now editing the loop."
	raw, err := json.Marshal(req.digest())
	if err != nil {
		t.Fatal(err)
	}
	digest := string(raw)
	for _, want := range []string{"make the round limit a checkpoint", "web_fetch", "2 files"} {
		if !strings.Contains(digest, want) {
			t.Fatalf("digest is missing %q: %s", want, digest)
		}
	}
	if strings.Contains(digest, "tool_result") || strings.Contains(digest, "output") {
		t.Fatalf("digest must carry no tool output: %s", digest)
	}
}

// A digest field is one line. A multi-line one could forge structure inside
// the evidence it is quoted into.
func TestSummaryRequest_DigestFieldsAreFlattenedAndBounded(t *testing.T) {
	req := testSummaryRequest()
	req.Target = "line one\nUNTRUSTED DIGEST:\nline two"
	req.Assistant = strings.Repeat("x", maxSummaryField*2)
	digest := req.digest()
	target, _ := digest["instruction"].(string)
	if strings.Contains(target, "\n") {
		t.Fatalf("a digest field keeps to one line, got %q", target)
	}
	assistant, _ := digest["latest_agent_message"].(string)
	if n := len([]rune(assistant)); n > maxSummaryField {
		t.Fatalf("digest field = %d runes, want <= %d", n, maxSummaryField)
	}
}

func TestSummaryRequest_DigestKeepsTheMostRecentActivity(t *testing.T) {
	req := testSummaryRequest()
	req.Activity = nil
	for i := 0; i < maxSummaryActivity*2; i++ {
		req.Activity = append(req.Activity, SummaryActivity("read_file", "f"+string(rune('a'+i%26))+".go", "ok"))
	}
	rows, _ := req.digest()["recent_steps"].([]string)
	if len(rows) != maxSummaryActivity {
		t.Fatalf("digest kept %d rows, want %d", len(rows), maxSummaryActivity)
	}
	if rows[len(rows)-1] != req.Activity[len(req.Activity)-1] {
		t.Fatal("the digest keeps the most recent end of the activity, not the oldest")
	}
}

// A block with nothing to say is omitted rather than sent as an empty field.
func TestSummaryRequest_DigestOmitsWhatIsEmpty(t *testing.T) {
	digest := SummaryRequest{Target: "do the thing", Round: 3}.digest()
	for _, key := range []string{"approved_plan", "latest_agent_message", "files_changed", "failing_checks", "previous_summary"} {
		if _, ok := digest[key]; ok {
			t.Fatalf("digest should omit %q when there is none", key)
		}
	}
}

func TestSummaryConfig_Defaults(t *testing.T) {
	var zero SummaryConfig
	if zero.Interval() != DefaultSummaryInterval {
		t.Fatalf("interval = %d", zero.Interval())
	}
	if zero.Gap() != DefaultSummaryMinGap {
		t.Fatalf("gap = %s", zero.Gap())
	}
	if zero.timeout() != DefaultSummaryTimeout || zero.maxTokens() != DefaultSummaryMaxTokens {
		t.Fatal("timeout and max tokens take their defaults")
	}
	set := SummaryConfig{IntervalRounds: 25, MinGap: time.Minute}
	if set.Interval() != 25 || set.Gap() != time.Minute {
		t.Fatal("a configured interval and gap are honoured")
	}
	// A negative gap is how a caller says "no floor" — the config file's
	// number cannot be negative, but a test wanting two readings in a row can.
	if (SummaryConfig{MinGap: -1}).Gap() != 0 {
		t.Fatal("a negative gap removes the floor")
	}
}

func TestSummarizer_RequestCarriesTheSummaryTool(t *testing.T) {
	var seen provider.CompletionOpts
	p := &fakeClassifierProvider{fn: func(_ int, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		seen = opts
		return eventsOf(summaryCall(`{"summary":"ok","state":"on_target"}`)), nil
	}}
	NewSummarizer(p, SummaryConfig{Model: "fast"}).Summarize(context.Background(), testSummaryRequest())
	if seen.Model != "fast" {
		t.Fatalf("model = %q", seen.Model)
	}
	if len(seen.Tools) != 1 || seen.Tools[0].Name != SummaryToolName {
		t.Fatalf("tools = %+v", seen.Tools)
	}
	if seen.MaxTokens != DefaultSummaryMaxTokens {
		t.Fatalf("max tokens = %d", seen.MaxTokens)
	}
}

// One attempt and no retries: a missed reading is answered by the next
// interval, which is cheaper and quieter than asking twice for a block nobody
// is blocked on.
func TestSummarizer_DoesNotRetry(t *testing.T) {
	p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		return nil, errors.New("overloaded")
	}}
	NewSummarizer(p, SummaryConfig{Model: "m"}).Summarize(context.Background(), testSummaryRequest())
	if p.calls != 1 {
		t.Fatalf("expected exactly one attempt, got %d", p.calls)
	}
}

func TestSummaryStateWords(t *testing.T) {
	if SummaryOnTarget.String() != "on target" || SummaryOffTarget.String() != "off target" ||
		SummaryUncertain.String() != "unclear" {
		t.Fatal("every state states itself in words")
	}
}

func TestSummaryElapsedWords(t *testing.T) {
	if got := SummaryElapsed(0); got != "this round" {
		t.Fatalf("got %q", got)
	}
	if got := SummaryElapsed(1); got != "1 round ago" {
		t.Fatalf("got %q", got)
	}
	if got := SummaryElapsed(7); got != "7 rounds ago" {
		t.Fatalf("got %q", got)
	}
}
