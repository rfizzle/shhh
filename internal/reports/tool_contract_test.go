//go:build contract

package reports

import (
	"encoding/json"
	"strings"
	"testing"
)

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

// The same page, published by a surface that will still be there, is a link.
func TestPublish_LeadsWithWhateverTheSurfaceCanHonour(t *testing.T) {
	resident, _ := testPublisherFor(t, Resident, false)
	line, err := resident.Publish(sampleDocument())
	if err != nil || !strings.HasPrefix(line, "http://127.0.0.1:") {
		t.Fatalf("resident Publish = %q, %v", line, err)
	}
	oneShot, _ := testPublisherFor(t, OneShot, false)
	line, err = oneShot.Publish(sampleDocument())
	if err != nil || line != "shhh reports open "+oneShot.store.List()[0].ID {
		t.Fatalf("one-shot Publish = %q, %v", line, err)
	}
}
