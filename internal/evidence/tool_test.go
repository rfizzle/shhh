package evidence

import (
	"encoding/json"
	"fmt"
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
	if !strings.Contains(out, "L2: two FAIL two") {
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
