package components

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// toolsFixture is a session's sources: the built-in toolset, three servers —
// one up, one waiting on the checkout's trust, one that did not start — a
// definition that could not be read, a language server not yet started, two
// binaries found and two missing, and the web with fetch and no search.
func toolsFixture() []ToolsSource {
	return []ToolsSource{
		{Group: ToolsBuiltin, Source: InspectorToolSource{Name: "built-in", State: ToolSourceUp, Note: "14 tools"},
			Detail: "the toolset shhh registers itself, with what it found on this machine",
			Tools: []string{"read_file", "list_directory", "search", "glob", "write_file", "edit_file",
				"execute_command", "process", "steps", "question", "remember", "evidence", "report", "query"}},
		{Group: ToolsServers, Source: InspectorToolSource{Name: "docs", State: ToolSourceUp, Note: "2 tools, 1 prompt"},
			Detail: "npx -y @example/docs-mcp · read-only", Tools: []string{"docs__search", "docs__read"}},
		{Group: ToolsServers, Source: InspectorToolSource{Name: "tracker", State: ToolSourceBlocked, Note: "untrusted"},
			Detail:      "npx -y tracker-mcp · project",
			Consequence: "a project server does not start until you trust the checkout",
			Fix:         []string{"shhh mcp show tracker   # what it is, before you trust the checkout", "shhh trust   # or [a] on this row"},
			Offer:       ToolsOfferTrust, Ask: "Trust ~/src/app? tracker starts from .mcp.json and runs npx -y tracker-mcp as you."},
		{Group: ToolsServers, Source: InspectorToolSource{Name: "linear", State: ToolSourceFailed, Note: "server linear: connect: exec: \"linear-mcp\": executable file not found in $PATH"},
			Detail:      "linear-mcp",
			Consequence: "its tools are not in this session",
			Fix: []string{"server linear: connect: exec: \"linear-mcp\": executable file not found in $PATH",
				"the server's own output is above; check the command or the url",
				"shhh mcp show linear connects it alone and prints what it says"}},
		{Group: ToolsUnloaded, Source: InspectorToolSource{Name: "definition", State: ToolSourceFailed, Note: ".mcp.json: server Bad Name: not a name"},
			Consequence: "it is not a server in this session", Fix: []string{".mcp.json: server Bad Name: not a name"}},
		{Group: ToolsLanguage, Source: InspectorToolSource{Name: "gopls", State: ToolSourceUp, Note: "5 tools · starts on the first file it owns"},
			Detail: "gopls · .go", Tools: []string{"lsp_definition", "lsp_references", "lsp_symbols", "lsp_outline", "lsp_hover"}},
		{Group: ToolsBinaries, Source: InspectorToolSource{Name: "fd", State: ToolSourceUp, Note: "1 tool"}, Detail: "/usr/bin/fd", Tools: []string{"fd"}},
		{Group: ToolsBinaries, Source: InspectorToolSource{Name: "git", State: ToolSourceUp, Note: "2 tools"}, Detail: "/usr/bin/git", Tools: []string{"git", "git_write"}},
		{Group: ToolsBinaries, Source: InspectorToolSource{Name: "ast-grep, sd", State: ToolSourceOff, Note: "not on PATH"},
			Consequence: "the tools over them are not registered; the built-in tools do their work",
			Fix:         []string{"install them and start a new session; shhh doctor names what this machine has"}},
		{Group: ToolsWeb, Source: InspectorToolSource{Name: "fetch", State: ToolSourceUp, Note: "1 tool"},
			Detail: "public http and https pages, read against the host lists", Tools: []string{"web_fetch"}},
		{Group: ToolsWeb, Source: InspectorToolSource{Name: "search", State: ToolSourceOff, Note: "no web search"},
			Consequence: "web_search is not registered; a session reads the URLs it is given and the workspace",
			Fix: []string{"shhh config set --global web.search_api_key_env BRAVE_API_KEY   a paid key, named rather than held",
				"shhh config set --global web.search_provider searxng            an instance you run, which takes none"}},
	}
}

// toolsScreen is the screen over the fixture with the pointer on focus.
func toolsScreen(focus int) *ToolsScreen {
	s := &ToolsScreen{Sources: toolsFixture(), maxLines: 30}
	s.Focus = focus
	return s
}

// The list is every source under its group's heading, each the rail row's
// mark, name, state word and note, and the header counts the servers and the
// sources that are up.
func TestToolsScreen_ListsEverySourceUnderItsGroup(t *testing.T) {
	view := ansi.Strip(toolsScreen(0).View(130))
	for _, want := range []string{
		"/mcp", "3 servers", "2 of 4 up", "[q] back",
		"✓ built-in", "mcp servers", "✓ docs", "⚠ tracker", "blocked", "✗ linear", "error",
		"definitions not loaded", "language servers", "✓ gopls", "binaries on PATH", "⊘ ast-grep, sd",
		"web", "✓ fetch", "⊘ search",
		"read_file", "execute_command",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen is missing %q:\n%s", want, view)
		}
	}
}

// A source that is not up says what that costs and what would move it, and
// a server the checkout declared offers its answer; one it did not, none.
func TestToolsScreen_ADownSourceSaysWhyAndWhatWouldMoveIt(t *testing.T) {
	view := ansi.Strip(toolsScreen(3).View(130))
	for _, want := range []string{"costs", "its tools are not in this session", "what would move it", "executable file not found", "shhh mcp show linear"} {
		if !strings.Contains(view, want) {
			t.Errorf("the failed server's account is missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "[a]") {
		t.Errorf("a user server offers the checkout's answer:\n%s", view)
	}
	if view := ansi.Strip(toolsScreen(2).View(130)); !strings.Contains(view, "[a] trust this checkout") {
		t.Errorf("the project server does not offer the answer:\n%s", view)
	}
}

// [a] asks before anything is handed over; a decline hands over nothing and
// a yes hands over the offer and the row; moving drops what the last answer
// said, and esc leaves.
func TestToolsScreen_TheOfferIsAskedFirst(t *testing.T) {
	s := toolsScreen(2)
	s.View(130)
	if _, r := s.Update(tea.KeyPressMsg{Code: 'a', Text: "a"}); r.Offer != ToolsOfferNone {
		t.Fatal("[a] handed the offer over without asking")
	}
	if !strings.Contains(ansi.Strip(s.View(130)), "Trust ~/src/app?") {
		t.Fatalf("the confirm is not on the foot row:\n%s", ansi.Strip(s.View(130)))
	}
	if _, r := s.Update(tea.KeyPressMsg{Code: 'n', Text: "n"}); r.Offer != ToolsOfferNone {
		t.Fatal("a declined confirm handed the offer over")
	}
	s.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if _, r := s.Update(tea.KeyPressMsg{Code: 'y', Text: "y"}); r.Offer != ToolsOfferTrust || r.at != 2 {
		t.Fatalf("a confirmed [a] handed over %+v", r)
	}
	s.Said = "trusted ~/src/app — it takes effect in the next session."
	if !strings.Contains(ansi.Strip(s.View(130)), "it takes effect in the next session") {
		t.Fatal("the answer is not under the row it was taken on")
	}
	s.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if s.Said != "" || s.Focus != 3 {
		t.Fatalf("moving should drop the answer, focus %d said %q", s.Focus, s.Said)
	}
	s.Focus = 0
	if _, r := s.Update(tea.KeyPressMsg{Code: 'a', Text: "a"}); r.Offer != ToolsOfferNone || s.confirm != nil {
		t.Fatal("[a] asked on a row with nothing to answer")
	}
	if done, _ := s.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); !done {
		t.Fatal("esc should leave")
	}
}

// /mcp opens on the servers: the pointer goes to the first of a group.
func TestToolsScreen_FocusGroupFindsTheServers(t *testing.T) {
	s := toolsScreen(0)
	if !s.FocusGroup(ToolsServers) || s.Focus != 1 {
		t.Fatalf("the servers start at row 1, focus %d", s.Focus)
	}
	s = &ToolsScreen{Sources: toolsFixture()[:1]}
	if s.FocusGroup(ToolsServers) || s.Focus != 0 {
		t.Fatal("a screen with no servers found one")
	}
}

// Nothing a narrow screen draws runs past the terminal.
func TestToolsScreen_NarrowStaysInside(t *testing.T) {
	s := toolsScreen(3)
	s.maxLines = 50
	for _, line := range strings.Split(s.View(60), "\n") {
		if lipgloss.Width(line) > 60 {
			t.Errorf("a row ran past the terminal: %q", ansi.Strip(line))
		}
	}
}

// TestGolden_ToolsScreen captures bare `/mcp` and the TOOLS door: the
// pointer on the built-in toolset, on a server waiting on the checkout's
// trust, on one that did not start, and a session with nothing configured
// outside shhh — its own tools and the web.
func TestGolden_ToolsScreen(t *testing.T) {
	bare := func() *ToolsScreen {
		all := toolsFixture()
		return &ToolsScreen{Sources: []ToolsSource{all[0], all[9], all[10]}, maxLines: 20}
	}
	captureGolden(t, "tools-screen", "the tools screen", goldenWidths, func(width int) []golden.Panel {
		return []golden.Panel{
			{Label: "the built-in toolset · every tool it registered", View: toolsScreen(0).View(width)},
			{Label: "a project server · waiting on the checkout, [a] answers after asking", View: toolsScreen(2).View(width)},
			{Label: "a server that did not start · what it costs, and /mcp's words for what would move it", View: toolsScreen(3).View(width)},
			{Label: "nothing outside shhh · the session's own tools and the web", View: bare().View(width)},
		}
	})
}
