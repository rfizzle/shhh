package chat

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// toolsHost stands in for the CLI's reading: servers as the rail reads them,
// a language server and the web tools, read again on every call, with a trust
// answer that is recorded and flips the offers the way the checkout's does.
type toolsHost struct {
	servers []components.ToolsSource
	trusted bool
	asked   [][]string
}

func (h *toolsHost) read() []components.ToolsSource {
	out := make([]components.ToolsSource, 0, len(h.servers)+2)
	for _, s := range h.servers {
		if s.Offer != components.ToolsOfferNone {
			s.Offer = components.ToolsOfferTrust
			if h.trusted {
				s.Offer = components.ToolsOfferDistrust
			}
			s.Ask = "Answer for the checkout?"
		}
		out = append(out, s)
	}
	return append(out,
		components.ToolsSource{Group: components.ToolsWeb, Tools: []string{"web_fetch"},
			Source: components.InspectorToolSource{Name: "fetch", State: components.ToolSourceUp, Note: "1 tool"}},
		components.ToolsSource{Group: components.ToolsLanguage, Tools: []string{"lsp_definition"},
			Source: components.InspectorToolSource{Name: "gopls", State: components.ToolSourceUp, Note: "1 tool"}},
	)
}

func (h *toolsHost) trust(args []string) string {
	h.asked = append(h.asked, args)
	h.trusted = len(args) == 0
	if h.trusted {
		return "trusted /repo — it takes effect in the next session."
	}
	return "withdrew trust from /repo — it takes effect in the next session."
}

// server is one server row as the host reads it.
func server(name string, state components.ToolSourceState, note string, project bool) components.ToolsSource {
	s := components.ToolsSource{Group: components.ToolsServers,
		Source: components.InspectorToolSource{Name: name, State: state, Note: note}}
	if state == components.ToolSourceUp {
		s.Tools = []string{name + "__search", name + "__read"}
	} else {
		s.Consequence = "its tools are not in this session"
		s.Fix = []string{"server " + name + ": connect: EOF"}
	}
	if project {
		s.Offer = components.ToolsOfferTrust
	}
	return s
}

// withTools wires a host into a model, the rail's servers read from the same
// reading the way the session's assembly reads both from one set of reports.
func withTools(m Model, h *toolsHost) Model {
	m.toolDefs = []ToolTokens{{Name: "read_file"}, {Name: "edit_file"}, {Name: "docs__search"}}
	m = m.WithMCP(MCP{
		Has:     func(name string) bool { return strings.Contains(name, "__") },
		Manage:  func(args []string) string { return "manage " + strings.Join(args, " ") },
		Sources: railSources(h.read()),
	})
	return m.WithToolSources(ToolSources{Read: h.read, Trust: h.trust})
}

func toolsFixture() *toolsHost {
	return &toolsHost{servers: []components.ToolsSource{
		server("docs", components.ToolSourceUp, "2 tools", false),
		server("github", components.ToolSourceUp, "2 tools", false),
		server("tracker", components.ToolSourceBlocked, "untrusted", true),
		server("linear", components.ToolSourceFailed, "server linear: connect: EOF", false),
		server("legacy", components.ToolSourceUp, "2 tools", false),
	}}
}

// railToolsModel is railDoorModel with five servers configured, so the rail
// draws a TOOLS block that folds behind its own `… N more`.
func railToolsModel(t *testing.T) Model {
	t.Helper()
	return withTools(railDoorModel(t), toolsFixture())
}

func toolsView(m Model) string {
	return stripANSI(strings.Join(overlays()[stateTools].lines(m, 160, 40), "\n"))
}

// Bare /mcp opens the screen on the servers, over the rail, and esc leaves it
// with the draft as it was.
func TestToolsScreen_McpOpensOnTheServersAndLeavesTheDraft(t *testing.T) {
	m := withTools(inspectorModel(t, 160, 50), toolsFixture())
	m.input.SetValue("half a sentence")
	next, _ := m.runCommand("/mcp", "/mcp")
	m = next.(Model)
	screen := m.screens.tools()
	if m.state != stateTools || screen == nil || !m.inspectorHidden() {
		t.Fatalf("/mcp should put the screen up over the rail, state %d", m.state)
	}
	if got := screen.Sources[screen.Focus]; got.Group != components.ToolsServers || got.Source.Name != "docs" {
		t.Fatalf("/mcp should open on the first server, opened on %+v", got.Source)
	}
	view := toolsView(m)
	for _, want := range []string{"/mcp", "5 servers", "built-in", "mcp servers", "language servers", "web", "docs__search"} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen lacks %q:\n%s", want, view)
		}
	}
	next, _ = m.updateTools(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	if m.state == stateTools || m.screens.tools() != nil {
		t.Fatalf("esc should leave the screen, state %d", m.state)
	}
	if got := m.input.Value(); got != "half a sentence" {
		t.Fatalf("the screen took the draft: %q", got)
	}
}

// With words after it /mcp keeps its text: the listing's verbs answer, and
// the screen does not open.
func TestToolsScreen_McpWithWordsKeepsItsText(t *testing.T) {
	m := withTools(inspectorModel(t, 160, 50), toolsFixture())
	next, _ := m.runCommand("/mcp trust tracker", "/mcp")
	m = next.(Model)
	if m.state == stateTools {
		t.Fatal("/mcp with words opened the screen")
	}
	last := m.transcript[len(m.transcript)-1]
	if last.kind != entrySystem || last.text != "manage trust tracker" {
		t.Fatalf("/mcp trust did not reach the listing's own answer: %+v", last)
	}
}

// Every server the screen lists is the rail's own reading of it, and the
// built-in row states the count the rail's does.
func TestToolsScreen_ServersReadAsTheRailReadsThem(t *testing.T) {
	m := withTools(inspectorModel(t, 160, 50), toolsFixture())
	next, _ := m.openTools()
	m = next.(Model)
	screen := m.screens.tools()
	rail := m.inspectorTools()
	var servers []components.InspectorToolSource
	for _, src := range screen.Sources {
		switch src.Group {
		case components.ToolsServers:
			servers = append(servers, src.Source)
		case components.ToolsBuiltin:
			if src.Source != rail.Sources[0] {
				t.Errorf("the screen's built-in row %+v, the rail's %+v", src.Source, rail.Sources[0])
			}
		}
	}
	if len(servers) != len(m.mcp.Sources) {
		t.Fatalf("the screen lists %d servers, the rail holds %d", len(servers), len(m.mcp.Sources))
	}
	for i := range servers {
		if servers[i] != m.mcp.Sources[i] {
			t.Errorf("server %d: screen %+v, rail %+v", i, servers[i], m.mcp.Sources[i])
		}
	}
	// The header states the heading's own ratio.
	ratio := fmt.Sprintf("%d of %d up", rail.Up, len(rail.Sources)+rail.More)
	if view := toolsView(m); !strings.Contains(view, "/mcp · 5 servers · "+ratio) {
		t.Errorf("the header does not state the rail's %q:\n%s", ratio, view)
	}
	// The door opens on the first source that is not up: the one thing
	// standing in the way.
	if got := screen.Sources[screen.Focus].Source.Name; got != "tracker" {
		t.Errorf("the door opened on %q", got)
	}
}

// A server that stops answering while the screen is up is read as stopped on
// the next frame, and the rail the screen closes onto says so too.
func TestToolsScreen_ADeathIsReadWhileTheScreenIsUp(t *testing.T) {
	h := toolsFixture()
	m := withTools(inspectorModel(t, 160, 50), h)
	next, _ := m.openMCPScreen()
	m = next.(Model)
	h.servers[0].Source = components.InspectorToolSource{Name: "docs", State: components.ToolSourceFailed, Note: "stopped answering"}
	if view := toolsView(m); !strings.Contains(view, "stopped answering") {
		t.Fatalf("the screen did not read the death:\n%s", view)
	}
	next, _ = m.updateTools(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	if got := m.mcp.Sources[0]; got.State != components.ToolSourceFailed || got.Note != "stopped answering" {
		t.Fatalf("the rail the screen closed onto still reads %+v", got)
	}
}

// [a] on a server the checkout declared asks first, and a yes goes through
// the trust seam — /trust's own function — and is said on the screen and in
// the transcript. The answer flips the offer; the second [a] withdraws it.
func TestToolsScreen_TrustIsAskedAndGoesThroughTheTrustSeam(t *testing.T) {
	h := toolsFixture()
	m := withTools(inspectorModel(t, 160, 50), h)
	next, _ := m.openTools()
	m = next.(Model)
	if got := m.screens.tools().Sources[m.screens.tools().Focus].Source.Name; got != "tracker" {
		t.Fatalf("fixture opened on %q", got)
	}
	press := func(r rune) {
		next, _ := m.updateTools(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = next.(Model)
	}
	if view := toolsView(m); !strings.Contains(view, "[a] trust this checkout") {
		t.Fatalf("the project server does not offer the answer:\n%s", view)
	}
	press('a')
	if len(h.asked) != 0 {
		t.Fatal("[a] answered without asking")
	}
	press('n')
	if len(h.asked) != 0 || m.state != stateTools {
		t.Fatalf("a declined confirm answered anyway: %v", h.asked)
	}
	press('a')
	press('y')
	if len(h.asked) != 1 || len(h.asked[0]) != 0 {
		t.Fatalf("a confirmed [a] should trust the checkout, asked %v", h.asked)
	}
	view := toolsView(m)
	if !strings.Contains(view, "trusted /repo") || !strings.Contains(view, "[a] withdraw trust") {
		t.Fatalf("the answer is not on the screen, or the offer did not flip:\n%s", view)
	}
	if last := m.transcript[len(m.transcript)-1]; !strings.Contains(last.text, "trusted /repo") {
		t.Errorf("the transcript does not record the answer: %+v", last)
	}
	press('a')
	press('y')
	if len(h.asked) != 2 || len(h.asked[1]) != 1 || h.asked[1][0] != "off" {
		t.Fatalf("the second answer should withdraw it, asked %v", h.asked)
	}
	// A row the checkout did not declare offers nothing to answer.
	next, _ = m.updateTools(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(Model)
	press('a')
	if len(h.asked) != 2 || strings.Contains(toolsView(m), "[a]") {
		t.Errorf("a user server offered the checkout's answer")
	}
}

// A session with no server, no language server and no optional binary still
// opens the screen, on its own tools and the web.
func TestToolsScreen_ASessionWithNothingConfiguredOpensOnItsOwnTools(t *testing.T) {
	m := inspectorModel(t, 160, 50)
	m.toolDefs = []ToolTokens{{Name: "read_file"}, {Name: "web_fetch"}}
	m = m.WithToolSources(ToolSources{Read: func() []components.ToolsSource {
		return []components.ToolsSource{{Group: components.ToolsWeb, Tools: []string{"web_fetch"},
			Source: components.InspectorToolSource{Name: "fetch", State: components.ToolSourceUp, Note: "1 tool"}}}
	}})
	if m.inspectorTools() != nil {
		t.Fatal("the rail's block is up in a session with nothing outside shhh configured")
	}
	next, _ := m.runCommand("/mcp", "/mcp")
	m = next.(Model)
	if m.state != stateTools {
		t.Fatalf("/mcp with no servers should still open the screen, state %d", m.state)
	}
	view := toolsView(m)
	for _, want := range []string{"built-in", "2 tools", "read_file", "fetch"} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "mcp servers") {
		t.Errorf("a session with no servers draws a servers heading:\n%s", view)
	}
}
