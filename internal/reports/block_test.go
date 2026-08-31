package reports

import (
	"strings"
	"testing"
)

func TestDocumentValidate_Shapes(t *testing.T) {
	cases := []struct {
		name string
		doc  Document
		want string // substring of the error; "" means valid
	}{
		{"valid stats", Document{Title: "t", Blocks: []Block{{Type: BlockStats, Stats: []Stat{{Label: "runs", Value: "40"}}}}}, ""},
		{"valid table", Document{Title: "t", Blocks: []Block{{Type: BlockTable, Columns: []string{"a"}, Rows: [][]string{{"1"}}}}}, ""},
		{"valid chart", Document{Title: "t", Blocks: []Block{{Type: BlockBarChart, Series: []Series{{Values: []float64{1, 2}}}}}}, ""},
		{"valid diff", Document{Title: "t", Blocks: []Block{{Type: BlockDiff, Diff: "+x"}}}, ""},
		{"valid tree", Document{Title: "t", Blocks: []Block{{Type: BlockTree, Tree: []TreeItem{{Label: "root"}}}}}, ""},
		{"valid prose", Document{Title: "t", Blocks: []Block{{Type: BlockProse, Text: "hello"}}}, ""},

		{"no title", Document{Blocks: []Block{{Type: BlockProse, Text: "x"}}}, "title is required"},
		{"no blocks", Document{Title: "t"}, "at least one block"},
		{"unknown type", Document{Title: "t", Blocks: []Block{{Type: "gauge"}}}, `unknown type "gauge"`},
		{"names the index", Document{Title: "t", Blocks: []Block{{Type: BlockProse, Text: "x"}, {Type: BlockStats}}}, "block 2 (stats)"},
		{"ragged table", Document{Title: "t", Blocks: []Block{{Type: BlockTable, Columns: []string{"a", "b"}, Rows: [][]string{{"1"}}}}}, "row 1 has 1 cells for 2 columns"},
		{"chart without series", Document{Title: "t", Blocks: []Block{{Type: BlockLineChart}}}, "at least one series"},
		{"labels mismatch", Document{Title: "t", Blocks: []Block{{Type: BlockBarChart, XLabels: []string{"a"}, Series: []Series{{Values: []float64{1, 2}}}}}}, "2 values for 1 x_labels"},
		{"ninth series", Document{Title: "t", Blocks: []Block{{Type: BlockBarChart, Series: make([]Series, 9)}}}, "fold the rest"},
		{"empty freehand", Document{Title: "t", Blocks: []Block{{Type: BlockFreehand}}}, "freehand requires html"},
		{"deep tree", Document{Title: "t", Blocks: []Block{{Type: BlockTree, Tree: []TreeItem{{Label: "x", Depth: 40}}}}}, "outside 0–12"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.doc.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate = %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestDocumentValidate_BlockCap(t *testing.T) {
	doc := Document{Title: "t"}
	for range MaxBlocks + 1 {
		doc.Blocks = append(doc.Blocks, Block{Type: BlockProse, Text: "x"})
	}
	err := doc.Validate()
	if err == nil || !strings.Contains(err.Error(), "a page holds") {
		t.Fatalf("Validate = %v, want the block cap named", err)
	}
}
