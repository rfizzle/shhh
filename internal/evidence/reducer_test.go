package evidence

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func testReducer(t *testing.T) *Reducer {
	t.Helper()
	s, err := Open(t.TempDir(), "sess-test")
	if err != nil {
		t.Fatal(err)
	}
	return NewReducer(s)
}

var noticeIDRe = regexp.MustCompile(`evidence (ev-[0-9a-f]{16})`)

func bigOutput() string {
	var lines []string
	for i := 0; i < 400; i++ {
		lines = append(lines, fmt.Sprintf("line %d with plenty of padding to cross the threshold", i))
	}
	return strings.Join(lines, "\n")
}

func TestReducer_ProcessSmallUntouched(t *testing.T) {
	r := testReducer(t)
	if got := r.Process("execute_command", "short output"); got != "short output" {
		t.Fatalf("small result must pass through, got %q", got)
	}
	if st := r.Store().Stats(); st.Entries != 0 {
		t.Fatal("small results must not create evidence entries")
	}
	if rs := r.Stats(); rs.Reductions != 0 {
		t.Fatal("pass-through must not count as a reduction")
	}
}

func TestReducer_ProcessStoresOriginalVerbatim(t *testing.T) {
	r := testReducer(t)
	in := bigOutput()
	got := r.Process("execute_command", in)
	if got == in {
		t.Fatal("large result should have been reduced")
	}
	m := noticeIDRe.FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("reduced result must carry an evidence id notice:\n%s", got[:200])
	}
	if !strings.HasPrefix(got, "[output reduced:") {
		t.Fatal("the notice must lead the reduced view")
	}

	data, meta, err := r.Store().Read(m[1], 0, len(in)+1)
	if err != nil {
		t.Fatalf("stored original unreadable: %v", err)
	}
	if string(data) != in {
		t.Fatal("stored original must be byte-identical to the tool result")
	}
	if meta.Tool != "execute_command" {
		t.Fatalf("meta.Tool = %q", meta.Tool)
	}

	rs := r.Stats()
	if rs.Reductions != 1 || rs.OriginalBytes != int64(len(in)) || rs.ReducedBytes >= rs.OriginalBytes {
		t.Fatalf("stats = %+v", rs)
	}
}

func TestReducer_NilSafe(t *testing.T) {
	var r *Reducer
	if got := r.Process("x", "anything"); got != "anything" {
		t.Fatal("nil reducer must pass results through")
	}
}

func TestReducer_WrapExecutorReduces(t *testing.T) {
	r := testReducer(t)
	in := bigOutput()
	exec := r.WrapExecutor(func(name string, args json.RawMessage) (string, error) {
		return in, nil
	})
	out, err := exec("execute_command", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out == in || !strings.Contains(out, "stored as evidence ev-") {
		t.Fatal("wrapped executor must reduce large results")
	}
}

func TestReducer_WrapExecutorPassesErrors(t *testing.T) {
	r := testReducer(t)
	exec := r.WrapExecutor(func(name string, args json.RawMessage) (string, error) {
		return "", fmt.Errorf("boom")
	})
	if _, err := exec("execute_command", nil); err == nil || err.Error() != "boom" {
		t.Fatalf("errors must pass through unreduced, got %v", err)
	}
}

func TestReducer_WrapExecutorDispatchesEvidenceTool(t *testing.T) {
	r := testReducer(t)
	id, _ := r.Store().Put("exec", []byte("stored original body"))
	exec := r.WrapExecutor(func(name string, args json.RawMessage) (string, error) {
		t.Fatal("evidence tool calls must not reach the inner executor")
		return "", nil
	})
	out, err := exec(ToolName, json.RawMessage(`{"action":"read","id":"`+id+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "stored original body") {
		t.Fatalf("evidence read = %q", out)
	}
}

func TestReducer_StatusReport(t *testing.T) {
	r := testReducer(t)
	r.Process("execute_command", bigOutput())
	report := r.StatusReport()
	for _, want := range []string{"stored originals: 1", "reductions this session: 1", "/evidence purge"} {
		if !strings.Contains(report, want) {
			t.Fatalf("status report missing %q:\n%s", want, report)
		}
	}
}

// read_file is told to return a whole file in one call and is sized to do it.
// A head-and-tail cut through that result hands back a fragment and sends the
// reader to the evidence store to page for the rest, which is the loop the
// tool's description exists to prevent.
func TestReducer_SelfBoundingToolsAreNotReduced(t *testing.T) {
	in := bigOutput()
	for _, tool := range []string{"read_file", "list_directory", "search", "glob"} {
		r := testReducer(t)
		if got := r.Process(tool, in); got != in {
			t.Errorf("%s result must reach the model whole, got %d of %d bytes", tool, len(got), len(in))
		}
		if st := r.Store().Stats(); st.Entries != 0 {
			t.Errorf("%s: an unreduced result has no original to store", tool)
		}
		if rs := r.Stats(); rs.Reductions != 0 {
			t.Errorf("%s: pass-through must not count as a reduction", tool)
		}
	}

	// The pipeline still runs for output nothing else bounds.
	r := testReducer(t)
	if got := r.Process("execute_command", in); got == in {
		t.Fatal("command output is what the reduction pipeline is for")
	}
}
