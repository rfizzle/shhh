package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/spf13/cobra"
)

const verdictSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["verdict", "count"],
  "properties": {
    "verdict": {"type": "string", "enum": ["pass", "fail"]},
    "count": {"type": "integer"},
    "notes": {"type": ["string", "null"], "description": "free text"}
  }
}`

func writeSchema(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustSchema(t *testing.T, body string) *answerSchema {
	t.Helper()
	s, err := loadAnswerSchema(writeSchema(t, body))
	if err != nil {
		t.Fatalf("loadAnswerSchema: %v", err)
	}
	return s
}

// A schema the run could never be held to is refused when it is read, and
// the refusal names what is wrong with it: a keyword the validator would
// skip is a promise the run would say it kept and did not.
func TestLoadAnswerSchema_RefusesWhatItCannotHoldAnAnswerTo(t *testing.T) {
	cases := map[string]string{
		"not json":         `{"type":`,
		"not an object":    `["object"]`,
		"unknown keyword":  `{"type": "array", "items": {"type": "string"}}`,
		"unknown type":     `{"type": "text"}`,
		"type not a name":  `{"type": 3}`,
		"required shape":   `{"required": "name"}`,
		"properties shape": `{"properties": []}`,
		"nested keyword":   `{"properties": {"a": {"minimum": 1}}}`,
		"empty enum":       `{"enum": []}`,
		"impossible enum":  `{"type": "integer", "enum": ["one", "two"]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadAnswerSchema(writeSchema(t, body)); err == nil || !strings.Contains(err.Error(), "--output-schema") {
				t.Fatalf("err = %v, want a refusal naming the flag", err)
			}
		})
	}
	if _, err := loadAnswerSchema(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("a schema that is not there must be refused")
	}
	if _, err := loadAnswerSchema(writeSchema(t, verdictSchema)); err != nil {
		t.Fatalf("annotations are read past, not refused: %v", err)
	}
}

func TestAnswerSchema_ChecksTheFourKeywords(t *testing.T) {
	s := mustSchema(t, verdictSchema)
	cases := []struct {
		answer string
		ok     bool
		says   string
	}{
		{`{"verdict":"pass","count":3}`, true, ""},
		{"  {\"verdict\": \"fail\", \"count\": 0, \"notes\": null}\n", true, ""},
		{"```json\n{\"verdict\":\"pass\",\"count\":1.0}\n```", true, ""},
		{`{"verdict":"pass"}`, false, `"count" is required`},
		{`{"verdict":"maybe","count":1}`, false, "$.verdict: not one of the enum's values"},
		{`{"verdict":"pass","count":1.5}`, false, "$.count: want integer, got number"},
		{`{"verdict":"pass","count":1,"notes":7}`, false, "$.notes: want string or null"},
		{`["pass"]`, false, "$: want object, got array"},
		{`The verdict is pass.`, false, "not a JSON value"},
		{`{"verdict":"pass","count":1} {}`, false, "more than one value"},
	}
	for _, c := range cases {
		got, err := s.check(c.answer)
		if c.ok {
			if err != nil {
				t.Errorf("check(%q) = %v, want a pass", c.answer, err)
				continue
			}
			var obj map[string]any
			if json.Unmarshal(got, &obj) != nil {
				t.Errorf("check(%q) returned %s, want the answer as JSON", c.answer, got)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("check(%q) = %v, want a miss saying %q", c.answer, err, c.says)
		}
	}
}

// runUnderSchema drives a headless turn through the schema's close hook the
// way runPrintSession wires it, and reads the answer back the way it does.
func runUnderSchema(t *testing.T, s *answerSchema, rounds ...[]provider.StreamEvent) (*agent.Agent, json.RawMessage, error) {
	t.Helper()
	a := agent.New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, scriptedStream(rounds...))
	h := &agent.Headless{Agent: a, Gate: func(provider.ToolCall) bool { return false }}
	shape := &schemaClose{schema: s, truncated: h.TruncatedReply}
	h.OnClose = shape.close
	final, err := h.Run("grade it" + s.instruction())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	answer, err := shape.check(final)
	return a, answer, err
}

func handBacks(a *agent.Agent) []string {
	var out []string
	for _, m := range a.Messages() {
		if m.Role == provider.RoleUser && m.Machine {
			out = append(out, m.Content)
		}
	}
	return out
}

func TestSchemaClose_AMissIsAskedForOnceMoreWithTheValidatorsWords(t *testing.T) {
	s := mustSchema(t, verdictSchema)
	a, answer, err := runUnderSchema(t, s,
		[]provider.StreamEvent{{Token: `{"verdict":"pass"}`}, {Done: true}},
		[]provider.StreamEvent{{Token: `{"verdict":"pass","count":2}`}, {Done: true}},
	)
	if err != nil || string(answer) != `{"verdict":"pass","count":2}` {
		t.Fatalf("answer = %s, %v; want the second attempt, which satisfies the schema", answer, err)
	}
	backs := handBacks(a)
	if len(backs) != 1 || !strings.Contains(backs[0], `"count" is required`) {
		t.Fatalf("hand-backs = %q, want one user message carrying the validator's reason", backs)
	}
	if !strings.Contains(a.Messages()[1].Content, `"required":["verdict","count"]`) {
		t.Fatalf("the prompt should close on the schema: %q", a.Messages()[1].Content)
	}
}

// A second miss ends the turn, and the run's ending is a failed turn: the
// exit code projected from it is 4 and the class beside it is the schema's.
func TestSchemaClose_ASecondMissIsAFailedTurn(t *testing.T) {
	s := mustSchema(t, verdictSchema)
	a, answer, err := runUnderSchema(t, s,
		[]provider.StreamEvent{{Token: "pass"}, {Done: true}},
		[]provider.StreamEvent{{Token: "still pass"}, {Done: true}},
		[]provider.StreamEvent{{Token: `{"verdict":"pass","count":1}`}, {Done: true}},
	)
	if answer != nil || err == nil {
		t.Fatalf("answer = %s, %v; want the second miss to stand as the run's error", answer, err)
	}
	if n := len(handBacks(a)); n != 1 {
		t.Fatalf("%d hand-backs, want exactly one", n)
	}
	outcome := headlessTurnOutcome(err)
	if outcome != observe.TurnFailed || headlessExitCode(outcome, false, false) != exitProvider {
		t.Fatalf("outcome %q exits %d, want failed and 4", outcome, headlessExitCode(outcome, false, false))
	}
	if failureClass(err) != errorClassSchema {
		t.Fatalf("error class = %q, want %q", failureClass(err), errorClassSchema)
	}
}

// Half an object is not an answer: a ceiling under a schema takes the
// continuation the run already appends, and a reply cut again is a miss
// rather than a label — even where what was cut still parses.
func TestSchemaClose_ACutAnswerIsAMiss(t *testing.T) {
	s := mustSchema(t, `{"type": "integer"}`)
	cut := func(text string) []provider.StreamEvent {
		return []provider.StreamEvent{{Token: text}, {Done: true, Stop: provider.StopLength}}
	}
	a, _, err := runUnderSchema(t, s, cut("12"), cut("34"), cut("56"), cut("78"))
	if err == nil || !strings.Contains(err.Error(), "output ceiling") {
		t.Fatalf("err = %v, want the cut answer refused", err)
	}
	var continued bool
	for _, m := range a.Messages() {
		if m.Content == agent.ContinueAfterCeiling {
			continued = true
		}
	}
	if !continued {
		t.Fatal("the ceiling should take the continuation the run already appends")
	}
}

// Under a schema `.final` is the answer as JSON in both shapes; without one
// it is the string it always was.
func TestFinalIsTheAnswerAsJSONUnderASchema(t *testing.T) {
	var sb strings.Builder
	if err := writeJSONTranscript(&sb, jsonRun{final: `{"verdict":"pass","count":2}`,
		answer: json.RawMessage(`{"verdict":"pass","count":2}`)}); err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(sb.String()), &doc); err != nil {
		t.Fatalf("transcript is not JSON: %v\n%s", err, sb.String())
	}
	var obj map[string]any
	if json.Unmarshal(doc["final"], &obj) != nil || obj["verdict"] != "pass" {
		t.Fatalf(".final = %s, want an object", doc["final"])
	}

	var lines strings.Builder
	newJSONLStream(&lines).closed(observe.Pos{Turn: 1}, observe.TurnDone, exitDone, `{"count":2}`,
		json.RawMessage(`{"count":2}`), provider.Usage{}, headlessHandles{}, nil)
	var ev map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lines.String()), &ev); err != nil {
		t.Fatal(err)
	}
	if string(ev["final"]) != `{"count":2}` {
		t.Fatalf("close line final = %s, want the object", ev["final"])
	}

	sb.Reset()
	if err := writeJSONTranscript(&sb, jsonRun{final: "done"}); err != nil {
		t.Fatal(err)
	}
	var plain jsonTranscript
	if err := json.Unmarshal([]byte(sb.String()), &plain); err != nil || plain.Final != "done" {
		t.Fatalf("without a schema final is a string: %v %+v", err, plain)
	}
}

func TestPrintCmdsRefuseAnUnreadableSchemaBeforeTheRun(t *testing.T) {
	for _, cmd := range []*cobra.Command{newCodeCmd(), newChatCmd()} {
		cmd.SetArgs([]string{"--output-schema", writeSchema(t, `{"type": "array", "items": {}}`), "do a thing"})
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), `"items"`) {
			t.Fatalf("%s: err = %v, want the keyword the validator does not check named", cmd.Name(), err)
		}
	}
}
