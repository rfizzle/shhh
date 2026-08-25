package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// stripFixture is a five-deep queue with the current item and two more in the
// batch, one flagged item outside it, and one edit.
func stripFixture() QueueStrip {
	return QueueStrip{
		Items: []QueueItem{
			{Number: 1, Label: "go test ./internal/agent/...", Severity: SeverityLow, Batch: true},
			{Number: 2, Label: "npm run build", Severity: SeverityLow, Batch: true},
			{Number: 3, Label: "edit internal/ui/chat/model.go", Detail: "+9 −1", Severity: SeverityMedium},
			{Number: 4, Label: "rm -rf ./dist", Severity: SeverityHigh},
			{Number: 5, Label: "write docs/loop.md", Detail: "+12 −0", Severity: SeverityMedium},
		},
		Note: "[A] answers the 2 marked",
	}
}

func plainRows(rows []string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = ansi.Strip(r)
	}
	return out
}

func TestQueueStrip_ListsTheStackInOrder(t *testing.T) {
	rows := plainRows(stripFixture().View(80))
	if len(rows) != 6 {
		t.Fatalf("expected a header and five items, got %d rows:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	if !strings.Contains(rows[0], "5 pending") || !strings.Contains(rows[0], "[A] answers the 2 marked") {
		t.Fatalf("header should count the queue and name the batch, got %q", rows[0])
	}
	// One filled dot for the current decision, one hollow for each behind it.
	if !strings.Contains(rows[0], "●○○○○") {
		t.Fatalf("header should draw one dot per pending decision, got %q", rows[0])
	}
	if !strings.Contains(rows[1], "▸") {
		t.Fatalf("the current item should carry the pointer, got %q", rows[1])
	}
	for _, row := range rows[2:] {
		if strings.Contains(row, "▸") {
			t.Fatalf("only the current item should carry the pointer, got %q", row)
		}
	}
	// Membership is a mark on the row, not a colour.
	if !strings.Contains(rows[1], "[A]") || !strings.Contains(rows[2], "[A]") {
		t.Fatal("batch members should be marked with the key that answers them")
	}
	for _, row := range rows[3:] {
		if strings.Contains(row, "[A]") {
			t.Fatalf("non-members must carry no batch mark, got %q", row)
		}
	}
	if !strings.Contains(rows[4], "⚠ HIGH") {
		t.Fatalf("a flagged item should be rated on the strip, got %q", rows[4])
	}
	if !strings.Contains(rows[3], "+9 −1") {
		t.Fatalf("an edit's stats belong beside its rating, got %q", rows[3])
	}
}

func TestQueueStrip_OneDecisionIsNotAQueue(t *testing.T) {
	q := QueueStrip{Items: []QueueItem{{Number: 1, Label: "go test ./..."}}}
	if rows := q.View(80); rows != nil {
		t.Fatalf("a single decision should render no strip, got %v", rows)
	}
	if q.Rows() != 0 {
		t.Fatalf("a single decision should reserve no rows, got %d", q.Rows())
	}
}

func TestQueueStrip_CountsWhatItCannotShow(t *testing.T) {
	q := stripFixture()
	q.MaxRows = 3
	rows := plainRows(q.View(80))
	if len(rows) != 4 || len(rows) != q.Rows() {
		t.Fatalf("bounded strip should render 1+MaxRows rows and say so, got %d / %d", len(rows), q.Rows())
	}
	last := rows[len(rows)-1]
	// Two hidden, and the header claimed a batch of two — so the row has to
	// account for how many of the hidden ones are marked.
	if !strings.Contains(last, "3 more") {
		t.Fatalf("the overflow row should count what is not shown, got %q", last)
	}
	if strings.Contains(last, "marked") {
		t.Fatalf("none of the hidden items are in the batch, got %q", last)
	}

	q.Items[4].Batch = true
	rows = plainRows(q.View(80))
	if last = rows[len(rows)-1]; !strings.Contains(last, "3 more, 1 marked") {
		t.Fatalf("the overflow row should count hidden batch members, got %q", last)
	}
}

func TestQueueStrip_StaysInsideItsWidth(t *testing.T) {
	for _, width := range []int{40, 60, 80, 130} {
		for _, row := range stripFixture().View(width) {
			if w := lipgloss.Width(row); w > width {
				t.Fatalf("row overflows width %d by %d: %q", width, w-width, ansi.Strip(row))
			}
		}
	}
}

func TestApprovalCard_BatchKey(t *testing.T) {
	c := &ApprovalCard{
		Variant:  ApprovalCommand,
		Title:    "Approve command",
		Headline: "Assistant wants to run: go test ./...",
		Question: "Run this command?",
	}
	// Without a queue behind it, [A] stays the shifted spelling of [a].
	c.AllowAlways = true
	if done, result := c.Update(key("A")); !done || result != ApprovalAlways {
		t.Fatalf("[A] without a batch should take the session grant, got %v %v", done, result)
	}

	c.Batch, c.BatchHint = true, "A: approve 3 like this"
	view := c.View(80)
	if !strings.Contains(view, "[y/n/a/A]") {
		t.Fatalf("a batch should put [A] on the key list:\n%s", view)
	}
	if !strings.Contains(view, "A: approve 3 like this") {
		t.Fatalf("the count belongs on the key:\n%s", view)
	}
	if done, result := c.Update(key("A")); !done || result != ApprovalBatch {
		t.Fatalf("[A] with a batch should answer the batch, got %v %v", done, result)
	}
	// The lower-case key keeps its own meaning.
	if done, result := c.Update(key("a")); !done || result != ApprovalAlways {
		t.Fatalf("[a] should still take the session grant, got %v %v", done, result)
	}

	// A card with a batch but no session grant offers the batch alone.
	c.AllowAlways = false
	if view = c.View(80); !strings.Contains(view, "[y/n/A]") {
		t.Fatalf("a batch without a session grant should offer [y/n/A]:\n%s", view)
	}
}
