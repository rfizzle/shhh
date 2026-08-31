package reports

import (
	"encoding/json"
	"fmt"

	"github.com/rfizzle/shhh/internal/provider"
)

// ToolName is the model-facing report tool. It runs on the auto-run path
// without approval: it writes only shhh's own report store, serves on
// loopback only, and a page can never execute or fetch anything.
const ToolName = "report"

// ToolDefinition is the report tool registered for agent sessions.
func ToolDefinition() provider.Tool {
	return provider.Tool{
		Name: ToolName,
		Description: "Publish an answer that is a page rather than a paragraph — timings, comparisons, " +
			"structures, anything a terminal cannot hold — as a graphical page served locally for the user's browser. " +
			"Stay in plain text when a sentence or a short table answers: a page for three rows teaches the user to ignore the link. " +
			"Build from typed blocks: stats (a band of large numbers), table, bar_chart / line_chart (series are colored in fixed order), " +
			"diff (unified diff text), tree (depth-indented rows), prose. " +
			"When the answer is a drawing no block holds — a graph, a timeline, a state machine — add a freehand block of static HTML and inline SVG: " +
			"no scripts, no event handlers, no external references, and every color written as var(--token) from the report stylesheet " +
			"(--heading --prose --secondary --caption for text; --ok --fail --risk --running for state; --add --del --hunk for change; " +
			"--series-1 … --series-8 for categorical data, in order, meaning nothing; --card --rule --track for surfaces). " +
			"The result's first line is the page URL — include it in your answer.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"title": {"type": "string", "description": "Short page title, e.g. \"Suite timing breakdown\""},
				"blocks": {"type": "array", "description": "Sections of the page, in reading order", "items": {
					"type": "object",
					"properties": {
						"type": {"type": "string", "enum": ["stats", "table", "bar_chart", "line_chart", "diff", "tree", "prose", "freehand"]},
						"heading": {"type": "string", "description": "Optional section heading"},
						"stats": {"type": "array", "items": {"type": "object", "properties": {
							"label": {"type": "string"}, "value": {"type": "string"}, "delta": {"type": "string", "description": "Optional secondary line under the value"}},
							"required": ["label", "value"]}},
						"columns": {"type": "array", "items": {"type": "string"}},
						"rows": {"type": "array", "items": {"type": "array", "items": {"type": "string"}}},
						"x_labels": {"type": "array", "items": {"type": "string"}, "description": "Chart x-axis labels, one per point"},
						"series": {"type": "array", "items": {"type": "object", "properties": {
							"name": {"type": "string"}, "values": {"type": "array", "items": {"type": "number"}}},
							"required": ["values"]}},
						"diff": {"type": "string", "description": "Unified diff text"},
						"tree": {"type": "array", "items": {"type": "object", "properties": {
							"label": {"type": "string"}, "depth": {"type": "integer"}},
							"required": ["label"]}},
						"text": {"type": "string", "description": "Prose; blank lines separate paragraphs"},
						"html": {"type": "string", "description": "Freehand static HTML and inline SVG; colors only as var(--token)"}
					},
					"required": ["type"]
				}}
			},
			"required": ["title", "blocks"]
		}`),
	}
}

// Publisher executes report tool calls against one store and one server.
type Publisher struct {
	store  *Store
	server *Server
	origin string
	root   string // the project this session runs in
	open   bool   // pop a browser on publish (a headless run does not)
	openFn func(string) error
}

// NewPublisher wires the report tool for one session. origin names the
// command the session runs under; root is the project key.
func NewPublisher(store *Store, origin, root string, open bool) *Publisher {
	return &Publisher{
		store:  store,
		server: NewServer(store),
		origin: origin,
		root:   root,
		open:   open,
		openFn: OpenBrowser,
	}
}

// Close stops the publisher's server.
func (p *Publisher) Close() error { return p.server.Close() }

// ExecuteTool publishes one report and answers with its URL on the first
// line — the line the TUI row and the model's own answer both lift.
func (p *Publisher) ExecuteTool(args json.RawMessage) (string, error) {
	var doc Document
	if err := json.Unmarshal(args, &doc); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if err := doc.Validate(); err != nil {
		return "", err
	}
	for i, b := range doc.Blocks {
		if b.Type != BlockFreehand {
			continue
		}
		frozen, err := ValidateFreehand(b.HTML)
		if err != nil {
			return "", fmt.Errorf("block %d: %w", i+1, err)
		}
		doc.Blocks[i].HTML = frozen
	}
	id, err := p.store.Put(doc, Meta{Title: doc.Title, Project: p.root, Origin: p.origin})
	if err != nil {
		return "", err
	}
	url, err := p.server.URL(id)
	if err != nil {
		return "", err
	}
	opened := ""
	if p.open && p.openFn(url) == nil {
		opened = " and opened in the user's browser"
	}
	return fmt.Sprintf("%s\nreport %q published (id %s)%s. It outlives this session: `shhh reports open %s` re-serves it. Include the link in your answer.",
		url, doc.Title, id, opened, id), nil
}

// WrapExecutor returns an executor that dispatches report calls and hands
// everything else to next.
func (p *Publisher) WrapExecutor(next func(name string, args json.RawMessage) (string, error)) func(string, json.RawMessage) (string, error) {
	return func(name string, args json.RawMessage) (string, error) {
		if name == ToolName {
			return p.ExecuteTool(args)
		}
		return next(name, args)
	}
}
