// Package reports renders, stores and serves report pages: graphical HTML
// views the model builds when an answer is a page rather than a paragraph
// (docs/capabilities/reports.md#what-a-report-is).
//
// A report is a document of typed blocks — stat band, table, chart, diff,
// tree, prose — plus optional freehand sections of static HTML and inline
// SVG. The two halves age differently on purpose: typed blocks are stored as
// data and re-render under today's template and tokens on every serve, while
// a freehand section is validated once, frozen, and replayed exactly as it
// was made — the model's drawing is the artifact
// (docs/capabilities/reports.md#typed-blocks-and-freehand).
//
// Everything on a page draws from one token file, report.css, transcribed
// from the design system's tokens/report.css; freehand markup may name
// colors only as var(--token) and the validator enforces it. Pages are
// self-contained — no scripts, no fetches — served on loopback only under
// unguessable ids, and stored under shhh's state dir at 0600, the same trust
// boundary the diagnostic log gets
// (docs/capabilities/reports.md#the-page-cannot-phone-home).
package reports
