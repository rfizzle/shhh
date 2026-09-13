package reports

import (
	"encoding/json"
	"strings"
	"testing"
)

func testPublisher(t *testing.T, open bool) (*Publisher, *[]string) {
	t.Helper()
	return testPublisherFor(t, Resident, open)
}

func testPublisherFor(t *testing.T, life Lifetime, open bool) (*Publisher, *[]string) {
	t.Helper()
	s := openTestStore(t, t.TempDir())
	p := NewPublisher(s, "code", "/home/u/proj", life, open)
	t.Cleanup(func() { _ = p.Close() })
	var opened []string
	p.openFn = func(url string) error {
		opened = append(opened, url)
		return nil
	}
	return p, &opened
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

func TestToolDefinition_SchemaIsConservative(t *testing.T) {
	p, _ := testPublisher(t, false)
	def := p.ToolDefinition()
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

// A run that exits with its answer has no address to give: the port would be
// gone before anyone typed it, so the result leads with the command that
// serves the page and nothing listens at all.
func TestExecuteTool_AOneShotRunAnswersWithTheCommandAndOpensNoPort(t *testing.T) {
	p, opened := testPublisherFor(t, OneShot, true)
	args, _ := json.Marshal(sampleDocument())
	out, err := p.ExecuteTool(args)
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	id := p.store.List()[0].ID
	first, _, _ := strings.Cut(out, "\n")
	if first != "shhh reports open "+id {
		t.Fatalf("first line = %q, want the command that serves %s", first, id)
	}
	if strings.Contains(out, "http://") {
		t.Fatalf("a one-shot result quoted an address that dies with it: %q", out)
	}
	if p.server != nil {
		t.Error("a one-shot publisher opened a listener")
	}
	if len(*opened) != 0 {
		t.Fatalf("a one-shot publish opened a browser on a dead port: %v", *opened)
	}
}

// The description is where the model learns which line to quote, so it says
// what this publisher will actually answer with — the defect was a run that
// handed back an id under a description promising a URL.
func TestToolDefinition_DescribesTheResultThisSurfaceWillGive(t *testing.T) {
	resident, _ := testPublisherFor(t, Resident, false)
	if got := resident.ToolDefinition().Description; !strings.Contains(got, "first line is the page URL") {
		t.Fatalf("a serving surface does not offer its URL: %q", got)
	}
	oneShot, _ := testPublisherFor(t, OneShot, false)
	got := oneShot.ToolDefinition().Description
	if strings.Contains(got, "first line is the page URL") {
		t.Fatalf("a one-shot surface still trains the model on a URL: %q", got)
	}
	if !strings.Contains(got, "the command that serves it") {
		t.Fatalf("a one-shot surface does not say what its first line is: %q", got)
	}
}
