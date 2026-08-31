package reports

import (
	"fmt"
	"strings"
)

// Block type discriminators — the typed vocabulary. Block is a flat struct
// with per-type fields rather than a sum type because the tool schema must
// stay conservative (no oneOf/anyOf; Gemini's converter is the strictest
// consumer) and the stored JSON mirrors the schema exactly.
const (
	BlockStats     = "stats"
	BlockTable     = "table"
	BlockBarChart  = "bar_chart"
	BlockLineChart = "line_chart"
	BlockDiff      = "diff"
	BlockTree      = "tree"
	BlockProse     = "prose"
	BlockFreehand  = "freehand"
)

// Limits on one document. A report is a page, not a database dump; a block
// past these caps is refused with the cap named so the model can trim.
const (
	MaxBlocks     = 24
	MaxTableRows  = 200
	MaxSeries     = 8
	MaxPoints     = 120
	MaxTreeItems  = 200
	MaxTitleRunes = 120
)

// Document is one report as the model supplied it.
type Document struct {
	Title  string  `json:"title"`
	Blocks []Block `json:"blocks"`
}

// Block is one section of a report. Type says which of the per-type fields
// carry the content; the rest stay empty.
type Block struct {
	Type    string `json:"type"`
	Heading string `json:"heading,omitempty"`

	Stats   []Stat     `json:"stats,omitempty"`   // stats
	Columns []string   `json:"columns,omitempty"` // table
	Rows    [][]string `json:"rows,omitempty"`    // table
	XLabels []string   `json:"x_labels,omitempty"`
	Series  []Series   `json:"series,omitempty"` // bar_chart, line_chart
	Diff    string     `json:"diff,omitempty"`   // unified diff text
	Tree    []TreeItem `json:"tree,omitempty"`
	Text    string     `json:"text,omitempty"` // prose; blank-line paragraphs
	HTML    string     `json:"html,omitempty"` // freehand; validated then frozen
}

// Stat is one large number in a stat band.
type Stat struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Delta string `json:"delta,omitempty"`
}

// Series is one named run of values in a chart.
type Series struct {
	Name   string    `json:"name,omitempty"`
	Values []float64 `json:"values"`
}

// TreeItem is one row of a depth-indented tree.
type TreeItem struct {
	Label string `json:"label"`
	Depth int    `json:"depth,omitempty"`
}

// Validate checks the document's shape. Errors name the block index and the
// violation, because the tool result is the model's only feedback channel.
func (d Document) Validate() error {
	if strings.TrimSpace(d.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if len([]rune(d.Title)) > MaxTitleRunes {
		return fmt.Errorf("title is longer than %d characters", MaxTitleRunes)
	}
	if len(d.Blocks) == 0 {
		return fmt.Errorf("a report needs at least one block")
	}
	if len(d.Blocks) > MaxBlocks {
		return fmt.Errorf("%d blocks is more than the %d a page holds", len(d.Blocks), MaxBlocks)
	}
	for i, b := range d.Blocks {
		if err := b.validate(); err != nil {
			return fmt.Errorf("block %d (%s): %w", i+1, b.Type, err)
		}
	}
	return nil
}

func (b Block) validate() error {
	switch b.Type {
	case BlockStats:
		if len(b.Stats) == 0 {
			return fmt.Errorf("stats requires at least one {label, value}")
		}
		for _, s := range b.Stats {
			if strings.TrimSpace(s.Label) == "" || strings.TrimSpace(s.Value) == "" {
				return fmt.Errorf("every stat needs a label and a value")
			}
		}
	case BlockTable:
		if len(b.Columns) == 0 || len(b.Rows) == 0 {
			return fmt.Errorf("table requires columns and rows")
		}
		if len(b.Rows) > MaxTableRows {
			return fmt.Errorf("%d rows is more than the %d a table holds", len(b.Rows), MaxTableRows)
		}
		for i, row := range b.Rows {
			if len(row) != len(b.Columns) {
				return fmt.Errorf("row %d has %d cells for %d columns", i+1, len(row), len(b.Columns))
			}
		}
	case BlockBarChart, BlockLineChart:
		if len(b.Series) == 0 {
			return fmt.Errorf("a chart requires at least one series")
		}
		if len(b.Series) > MaxSeries {
			return fmt.Errorf("%d series is more than the %d a chart holds; fold the rest into \"other\"", len(b.Series), MaxSeries)
		}
		for i, s := range b.Series {
			if len(s.Values) == 0 {
				return fmt.Errorf("series %d has no values", i+1)
			}
			if len(s.Values) > MaxPoints {
				return fmt.Errorf("series %d has %d points, more than the %d a chart holds", i+1, len(s.Values), MaxPoints)
			}
			if len(b.XLabels) > 0 && len(s.Values) != len(b.XLabels) {
				return fmt.Errorf("series %d has %d values for %d x_labels", i+1, len(s.Values), len(b.XLabels))
			}
		}
	case BlockDiff:
		if strings.TrimSpace(b.Diff) == "" {
			return fmt.Errorf("diff requires unified diff text")
		}
	case BlockTree:
		if len(b.Tree) == 0 {
			return fmt.Errorf("tree requires at least one item")
		}
		if len(b.Tree) > MaxTreeItems {
			return fmt.Errorf("%d items is more than the %d a tree holds", len(b.Tree), MaxTreeItems)
		}
		for i, item := range b.Tree {
			if strings.TrimSpace(item.Label) == "" {
				return fmt.Errorf("tree item %d has no label", i+1)
			}
			if item.Depth < 0 || item.Depth > 12 {
				return fmt.Errorf("tree item %d depth %d is outside 0–12", i+1, item.Depth)
			}
		}
	case BlockProse:
		if strings.TrimSpace(b.Text) == "" {
			return fmt.Errorf("prose requires text")
		}
	case BlockFreehand:
		if strings.TrimSpace(b.HTML) == "" {
			return fmt.Errorf("freehand requires html")
		}
	default:
		return fmt.Errorf("unknown type %q (valid: stats, table, bar_chart, line_chart, diff, tree, prose, freehand)", b.Type)
	}
	return nil
}
