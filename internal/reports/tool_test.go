package reports

import (
	"encoding/json"
	"strings"
	"testing"
)

func testPublisher(t *testing.T, open bool) (*Publisher, *[]string) {
	t.Helper()
	s := openTestStore(t, t.TempDir())
	p := NewPublisher(s, "code", "/home/u/proj", open)
	t.Cleanup(func() { _ = p.Close() })
	var opened []string
	p.openFn = func(url string) error {
		opened = append(opened, url)
		return nil
	}
	return p, &opened
}

func TestExecuteTool_PublishesAndAnswersWithTheURL(t *testing.T) {
	p, opened := testPublisher(t, true)
	args, _ := json.Marshal(sampleDocument())
	out, err := p.ExecuteTool(args)
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	first, rest, _ := strings.Cut(out, "\n")
	if !strings.HasPrefix(first, "http://127.0.0.1:") || !strings.Contains(first, "/r/rp-") {
		t.Fatalf("first line is not the URL: %q", first)
	}
	if !strings.Contains(rest, "shhh reports open rp-") {
		t.Fatalf("result does not name the durable reopen path: %q", rest)
	}
	if len(*opened) != 1 || (*opened)[0] != first {
		t.Fatalf("browser open = %v, want the result URL once", *opened)
	}

	entries := p.store.List()
	if len(entries) != 1 || entries[0].Origin != "code" || entries[0].Project != "/home/u/proj" {
		t.Fatalf("stored meta = %+v", entries)
	}
}

func TestExecuteTool_HeadlessNeverOpensABrowser(t *testing.T) {
	p, opened := testPublisher(t, false)
	args, _ := json.Marshal(sampleDocument())
	if _, err := p.ExecuteTool(args); err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if len(*opened) != 0 {
		t.Fatalf("headless publish opened a browser: %v", *opened)
	}
}

func TestExecuteTool_FreehandIsFrozenAtStoreTime(t *testing.T) {
	p, _ := testPublisher(t, false)
	raw := `<p>x<!-- note --></p>`
	args, _ := json.Marshal(Document{Title: "t", Blocks: []Block{{Type: BlockFreehand, HTML: raw}}})
	if _, err := p.ExecuteTool(args); err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	doc, _, err := p.store.Load(p.store.List()[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	frozen, _ := ValidateFreehand(raw)
	if doc.Blocks[0].HTML != frozen {
		t.Fatalf("store holds %q, want the validated serialization %q", doc.Blocks[0].HTML, frozen)
	}
}

func TestExecuteTool_ViolationsComeBackNamed(t *testing.T) {
	p, _ := testPublisher(t, false)
	cases := []struct {
		doc  Document
		want string
	}{
		{Document{Title: "t", Blocks: []Block{{Type: BlockFreehand, HTML: `<script>x</script>`}}}, "<script> is not allowed"},
		{Document{Title: "t", Blocks: []Block{{Type: "gauge"}}}, "unknown type"},
		{Document{Title: ""}, "title is required"},
	}
	for _, tc := range cases {
		args, _ := json.Marshal(tc.doc)
		_, err := p.ExecuteTool(args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("ExecuteTool = %v, want %q", err, tc.want)
		}
	}
	if got := len(p.store.List()); got != 0 {
		t.Fatalf("a refused report was stored anyway (%d entries)", got)
	}
}

func TestWrapExecutor_PassesOtherToolsThrough(t *testing.T) {
	p, _ := testPublisher(t, false)
	exec := p.WrapExecutor(func(name string, _ json.RawMessage) (string, error) {
		return "handled " + name, nil
	})
	out, err := exec("read_file", nil)
	if err != nil || out != "handled read_file" {
		t.Fatalf("passthrough = %q, %v", out, err)
	}
	args, _ := json.Marshal(sampleDocument())
	out, err = exec(ToolName, args)
	if err != nil || !strings.HasPrefix(out, "http://127.0.0.1:") {
		t.Fatalf("dispatch = %q, %v", out, err)
	}
}

func TestToolDefinition_SchemaIsConservative(t *testing.T) {
	def := ToolDefinition()
	if def.Name != ToolName {
		t.Fatalf("name = %q", def.Name)
	}
	var schema map[string]any
	if err := json.Unmarshal(def.Parameters, &schema); err != nil {
		t.Fatalf("parameters are not valid JSON: %v", err)
	}
	raw := string(def.Parameters)
	for _, bad := range []string{"oneOf", "anyOf", "allOf", "additionalProperties", "$ref"} {
		if strings.Contains(raw, bad) {
			t.Fatalf("schema uses %q, which the strictest provider converter rejects", bad)
		}
	}
}
