package cli

// The shapes and the figures every listing shares: an empty state landing on
// a report that has already stated its title, the labelled block printed when
// the thing that was asked about is not there, and the readings more than one
// command states (docs/interface/surfaces.md#outside-the-tui).

import (
	"fmt"
	"strconv"

	"github.com/rfizzle/shhh/internal/cli/report"
)

// emptyInto puts an empty state on a report that has already stated its
// title, so an empty listing and a full one are the same shape.
func emptyInto(r report.Report, absent, wayOut string) report.Report {
	r.Sections = append(r.Sections, report.Section{Rows: []report.Row{report.Empty(absent, wayOut)}})
	return r
}

// notFound is one shape for every mistyped name: what was looked for, what
// was not found, and the listing that would have said so. A reader who has
// mistyped one slug has mistyped every other kind of name the same way.
func notFound(kind, name, listing string) string {
	return report.Report{Sections: []report.Section{{Rows: []report.Row{{
		State: report.Fail, Subject: "no " + kind + " " + name,
		Consequence: listing + " lists them",
	}}}}}.String()
}

// latencyText is a duration in the words every duration field in the product
// uses — milliseconds under a second, seconds with one decimal above it — and
// nothing at all where nothing was timed: a stat that cannot be reported is
// left out (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
//
// The surface and the text report of one command read the same store, so they
// share this rather than each rounding for themselves.
func latencyText(ms *float64) string {
	if ms == nil {
		return ""
	}
	if *ms < 1000 {
		return fmt.Sprintf("%.0fms", *ms)
	}
	return fmt.Sprintf("%.1fs", *ms/1000)
}

// tokenCount is the vitals rail's own token count: `412`, `41.2k`, `2.9M`.
// It is shared for the same reason latencyText is — `shhh metrics` and `shhh
// metrics --text` disagreeing about what 41,200 tokens is called would be two
// readings of one number.
func tokenCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 100_000:
		// The decimal earns its place at 41.2k and stops earning it at
		// 318.0k, where it is a digit of precision nobody reads and a column
		// of width everybody pays for.
		return fmt.Sprintf("%dk", n/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return strconv.FormatInt(n, 10)
}
