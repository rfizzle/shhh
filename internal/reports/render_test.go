package reports

import (
	"os"
	"strings"
	"testing"
	"time"
)

func sampleDocument() Document {
	return Document{
		Title: "Suite timing breakdown",
		Blocks: []Block{
			{Type: BlockStats, Stats: []Stat{
				{Label: "wall time", Value: "94s", Delta: "+31s over two weeks"},
				{Label: "slowest package", Value: "storage", Delta: "22s"},
				{Label: "packages", Value: "40"},
			}},
			{Type: BlockBarChart, Heading: "Wall time by package", XLabels: []string{"storage", "cli", "ui", "agent"},
				Series: []Series{{Name: "this run", Values: []float64{22, 18, 15, 9}}, {Name: "two weeks ago", Values: []float64{12, 14, 13, 8}}}},
			{Type: BlockTable, Heading: "The three that grew", Columns: []string{"package", "then", "now"},
				Rows: [][]string{{"storage", "12s", "22s"}, {"cli", "14s", "18s"}, {"ui", "13s", "15s"}}},
			{Type: BlockDiff, Heading: "The commit that did it", Diff: "--- a/store.go\n+++ b/store.go\n@@ -1 +1 @@\n-fast()\n+slow()"},
			{Type: BlockTree, Heading: "Where the time sits", Tree: []TreeItem{{Label: "storage"}, {Label: "migrate_test.go", Depth: 1}}},
			{Type: BlockProse, Text: "Not new.\n\nIt has been creeping for two weeks."},
			{Type: BlockFreehand, Heading: "Drawn freehand", HTML: `<svg viewBox="0 0 40 10"><rect x="0" y="0" width="20" height="8" fill="var(--series-1)"/></svg>`},
		},
	}
}

func testMeta() Meta {
	return Meta{Title: "Suite timing breakdown", Project: "/home/u/proj", Origin: "code", Created: time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)}
}

func TestRender_WholePage(t *testing.T) {
	out, err := Render(sampleDocument(), testMeta(), "rp-0123456789abcdef")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	page := string(out)
	for _, want := range []string{
		"<!doctype html>",
		"<title>Suite timing breakdown · shhh</title>",
		"--series-1: #0081be",              // the token block is embedded
		`<div class="value">94s</div>`,     // stats
		"<svg",                             // chart
		`class="swatch s1"`,                // legend in fixed slot order
		`<span class="del">-fast()</span>`, // diff classified
		"padding-left: 16px",               // tree indent
		"It has been creeping",             // prose
		"shhh reports open rp-0123456789abcdef",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("rendered page missing %q", want)
		}
	}
	if strings.Contains(page, "<script") {
		t.Fatal("a report page must never carry a script")
	}
	if path := os.Getenv("REPORTS_DUMP"); path != "" {
		_ = os.WriteFile(path, out, 0o600)
	}
}

func TestRender_EscapesModelText(t *testing.T) {
	doc := Document{Title: `<img src=x>`, Blocks: []Block{
		{Type: BlockProse, Text: `<script>alert(1)</script>`},
		{Type: BlockTable, Columns: []string{`<b>col</b>`}, Rows: [][]string{{`<i>cell</i>`}}},
	}}
	out, err := Render(doc, testMeta(), "rp-0123456789abcdef")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	page := string(out)
	for _, bad := range []string{"<img", "<script>alert", "<b>col</b>", "<i>cell</i>"} {
		if strings.Contains(page, bad) {
			t.Fatalf("model text landed unescaped: %q", bad)
		}
	}
}

func TestRender_FrozenFreehandReplaysVerbatim(t *testing.T) {
	frozen, err := ValidateFreehand(`<p>the drawing <em>is</em> the artifact</p>`)
	if err != nil {
		t.Fatalf("ValidateFreehand: %v", err)
	}
	doc := Document{Title: "t", Blocks: []Block{{Type: BlockFreehand, HTML: frozen}}}
	out, err := Render(doc, testMeta(), "rp-0123456789abcdef")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(out), frozen) {
		t.Fatalf("frozen markup did not replay byte-for-byte:\nwant %s", frozen)
	}
}

func TestSplitDiff_Classification(t *testing.T) {
	lines := splitDiff("diff --git a b\n--- a\n+++ b\n@@ -1 +1 @@\n ctx\n+new\n-old")
	classes := make([]string, len(lines))
	for i, l := range lines {
		classes[i] = l.Class
	}
	want := []string{"file", "file", "file", "hunk", "ctx", "add", "del"}
	if strings.Join(classes, " ") != strings.Join(want, " ") {
		t.Fatalf("splitDiff classes = %v, want %v", classes, want)
	}
}
