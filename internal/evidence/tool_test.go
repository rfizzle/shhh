package evidence

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func toolCall(t *testing.T, s *Store, args string) (string, error) {
	t.Helper()
	return s.ExecuteTool(json.RawMessage(args))
}

func TestTool_Info(t *testing.T) {
	s := openTestStore(t, t.TempDir(), "sess-a")
	id, _ := s.Put("read_file", []byte("twelve bytes"))

	out, err := toolCall(t, s, `{"action":"info","id":"`+id+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "tool read_file") || !strings.Contains(out, "12 bytes stored") {
		t.Fatalf("info = %q", out)
	}
}

func TestTool_ReadPagesAndClamps(t *testing.T) {
	s := openTestStore(t, t.TempDir(), "sess-a")
	body := strings.Repeat("x", 10000)
	id, _ := s.Put("exec", []byte(body))

	out, err := toolCall(t, s, `{"action":"read","id":"`+id+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, fmt.Sprintf("bytes 0-%d of 10000", DefaultReadBytes)) {
		t.Fatalf("default read window wrong:\n%s", out[:120])
	}
	if !strings.Contains(out, fmt.Sprintf("continue with offset=%d", DefaultReadBytes)) {
		t.Fatal("paged read must tell the model how to continue")
	}

	// The byte clamp holds even when the model asks for more.
	out, err = toolCall(t, s, `{"action":"read","id":"`+id+`","limit":999999}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "x") > MaxReadBytes {
		t.Fatalf("read returned more than MaxReadBytes: %d", strings.Count(out, "x"))
	}

	// The last page ends cleanly with no continuation notice.
	out, _ = toolCall(t, s, `{"action":"read","id":"`+id+`","offset":9990}`)
	if !strings.Contains(out, "bytes 9990-10000 of 10000") || strings.Contains(out, "continue with") {
		t.Fatalf("final page = %q", out)
	}
}

func TestTool_Search(t *testing.T) {
	s := openTestStore(t, t.TempDir(), "sess-a")
	id, _ := s.Put("exec", []byte("one\ntwo FAIL two\nthree\n"))

	out, err := toolCall(t, s, `{"action":"search","id":"`+id+`","query":"fail"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "L2 (offset 4): two FAIL two") {
		t.Fatalf("search = %q", out)
	}

	out, _ = toolCall(t, s, `{"action":"search","id":"`+id+`","query":"absent"}`)
	if !strings.Contains(out, "no lines contain") {
		t.Fatalf("empty search = %q", out)
	}

	if _, err := toolCall(t, s, `{"action":"search","id":"`+id+`"}`); err == nil {
		t.Fatal("search without a query must error")
	}
}

// The definition says which action to try first and what the store cannot
// give back, and says nothing that would make search a pattern language.
func TestToolDefinition_SendsAKnownTermToSearchBeforeRead(t *testing.T) {
	def := ToolDefinition()
	desc := def.Description
	for _, want := range []string{
		"use action \"search\" first",
		"not a regular expression",
		"line number and byte offset",
		"to see the lines around a match",
		"when there is nothing to search for",
		"already cut off before it was stored is not in it",
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("description should say %q, got:\n%s", want, desc)
		}
	}
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(def.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	if q := schema.Properties["query"].Description; !strings.Contains(q, "not a regular expression") {
		t.Errorf("query should say it is literal text, got %q", q)
	}
	if o := schema.Properties["offset"].Description; !strings.Contains(o, "a little before the offset it reported") {
		t.Errorf("offset should say how to read around a match, got %q", o)
	}
	for _, words := range []string{desc, schema.Properties["query"].Description} {
		if strings.Contains(strings.ToLower(words), "regex ") || strings.Contains(words, "pattern") {
			t.Errorf("the search must not read as a pattern language: %q", words)
		}
	}
}

// A long test run with more failures than the reduction keeps: the one the
// question is about is not in the reduced view. One search finds it and the
// offset it reports addresses a read that shows its assertion, where paging
// from the start would have spent a call per DefaultReadBytes to get there.
func TestTool_SearchThenReadFindsWhatTheReductionLeftOut(t *testing.T) {
	r := testReducer(t)
	var lines []string
	for i := 0; i < 1000; i++ {
		lines = append(lines, fmt.Sprintf("=== RUN   TestCase%03d", i), fmt.Sprintf("--- PASS: TestCase%03d (0.00s)", i))
		if i%5 == 0 {
			lines = append(lines,
				fmt.Sprintf("    case_test.go:%d: got %d, want %d", 10+i, i, i+1),
				fmt.Sprintf("--- FAIL: TestCase%03dBroken (0.01s)", i))
		}
	}
	original := strings.Join(lines, "\n")
	reduced := r.Process("execute_command", original)
	if reduced == original {
		t.Fatal("the fixture should be reduced")
	}
	const target = "TestCase985Broken"
	if strings.Contains(reduced, target) {
		t.Fatalf("the fixture should lose %s from the reduced view:\n%s", target, reduced)
	}
	id := noticeIDRe.FindStringSubmatch(reduced)[1]
	if pages := len(original) / DefaultReadBytes; pages < 5 {
		t.Fatalf("the fixture should take many default reads to page, got %d", pages)
	}

	out, err := toolCall(t, r.store, `{"action":"search","id":"`+id+`","query":"`+target+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`L\d+ \(offset (\d+)\): --- FAIL: ` + target).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("search should find the failure with its offset, got:\n%s", out)
	}
	var at int
	if _, err := fmt.Sscan(m[1], &at); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(original[at:], "--- FAIL: "+target) {
		t.Fatalf("the reported offset %d should start the matching line, got %q", at, original[at:at+30])
	}

	out, err = toolCall(t, r.store, fmt.Sprintf(`{"action":"read","id":"%s","offset":%d,"limit":200}`, id, at-100))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "got 985, want 986") || !strings.Contains(out, target) {
		t.Fatalf("a read just before the match should show its assertion, got:\n%s", out)
	}
}

func TestTool_Errors(t *testing.T) {
	s := openTestStore(t, t.TempDir(), "sess-a")
	id, _ := s.Put("exec", []byte("body"))

	cases := []string{
		`not json`,
		`{"action":"read"}`,
		`{"action":"launch","id":"` + id + `"}`,
		`{"action":"read","id":"../../secret"}`,
		`{"action":"read","id":"ev-ffffffffffffffff"}`,
	}
	for _, args := range cases {
		if _, err := toolCall(t, s, args); err == nil {
			t.Fatalf("args %q must error", args)
		}
	}
}
