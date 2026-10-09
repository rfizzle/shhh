package chat

// The tools screen (docs/interface/surfaces.md#the-supporting-screens): bare
// `/mcp`, and the rail's TOOLS heading and its fold marker, open every place
// this session's tools came from. The block draws the first few sources and
// counts the rest; the screen lists them all — the built-in toolset, each MCP
// server, the language servers, the binaries found on PATH and the web tools
// — with every tool each registered, and for one that is not up what that
// costs and what would move it.
//
// A server row is the rail's own reading of that server, not a second one:
// the host reads the reports the way Restate does, and the screen hands what
// it read back to the rail, so the block, the screen and the listing behind
// `/mcp <verb>` cannot come to three answers about one server
// (docs/capabilities/mcp.md#a-server-that-dies-is-noticed).

import (
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// ToolSources is what the tools screen reads that the chat cannot read
// itself. The zero value is a session whose screen holds the built-in
// toolset alone and offers no answer.
type ToolSources struct {
	// Read is every source outside the built-in toolset, read again on every
	// call: a server that stopped answering, a language server that failed
	// to start and a trust answer given a moment ago are all things that
	// change while the screen is up.
	Read func() []components.ToolsSource
	// Trust records the checkout's answer — nothing for trust, "off" to
	// withdraw it — and says what it recorded. It is the function behind
	// /trust, so the screen's [a] and the command are one act
	// (docs/capabilities/mcp.md#a-checkout-cannot-start-a-process).
	Trust func(args []string) string
}

// toolsReading is every source, in the screen's order: the built-in toolset
// as the rail's row states it, then what the host read.
func (m Model) toolsReading() []components.ToolsSource {
	var out []components.ToolsSource
	if names := m.builtinToolNames(); len(names) > 0 {
		out = append(out, components.ToolsSource{
			Group: components.ToolsBuiltin,
			Source: components.InspectorToolSource{
				Name: "built-in", State: components.ToolSourceUp, Note: plural(len(names), "tool"),
			},
			Detail: "the toolset shhh registers itself, with what it found on this machine",
			Tools:  names,
		})
	}
	if m.wiring.ToolSources.Read != nil {
		for _, src := range m.wiring.ToolSources.Read() {
			src.Source = startingNote(src.Source)
			out = append(out, src)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Group < out[b].Group })
	return out
}

// railSources is the servers of a reading as the rail's TOOLS block holds
// them. Nil where the reading has none, which leaves a session with no
// servers with no block — the block is only up when something outside shhh
// was configured.
func railSources(reading []components.ToolsSource) []components.InspectorToolSource {
	var out []components.InspectorToolSource
	for _, src := range reading {
		if src.Group == components.ToolsServers {
			out = append(out, src.Source)
		}
	}
	return out
}

// restateServers hands a reading's servers back to the rail, so the block the
// screen closes onto says what the screen said. A session with no servers
// keeps the block it had: a reading with none has nothing to tell it.
func (m *Model) restateServers(reading []components.ToolsSource) {
	if len(m.mcp.Sources) == 0 || m.mcpStarting() {
		// A row still starting is read live and carries a clock; a
		// reading's copy of its note would be a clock stopped at the
		// moment the screen opened.
		return
	}
	if servers := railSources(reading); len(servers) == len(m.mcp.Sources) {
		m.mcp.Sources = servers
	}
}

// openTools is the door's opening: the screen on the first source that is
// not up, since that is the one thing standing in the way, or the first.
func (m Model) openTools() (tea.Model, tea.Cmd) {
	reading := m.toolsReading()
	screen := components.ToolsScreen{Sources: reading}
	for i, src := range reading {
		if src.Source.State != components.ToolSourceUp {
			screen.Focus = i
			break
		}
	}
	return m.showTools(screen)
}

// openMCPScreen is bare /mcp: the same screen, on the servers.
func (m Model) openMCPScreen() (tea.Model, tea.Cmd) {
	screen := components.ToolsScreen{Sources: m.toolsReading()}
	if !screen.FocusGroup(components.ToolsServers) {
		return m.openTools()
	}
	return m.showTools(screen)
}

// showTools puts a built screen up.
func (m Model) showTools(screen components.ToolsScreen) (tea.Model, tea.Cmd) {
	m.restateServers(screen.Sources)
	m.screens = m.screens.with(stateTools, &screen)
	m.enterSurface(stateTools)
	return m, nil
}

// toolsLines draws the screen, reading the sources again first: the screen
// stays up across a turn's rounds, and a server that stops answering while it
// is open is read as stopped on the next frame rather than on the next
// opening.
func (m Model) toolsLines(width, height int) []string {
	screen := m.screens.tools()
	if screen == nil {
		return nil
	}
	screen.Sources = m.toolsReading()
	screen.SetSize(width, height)
	return strings.Split(screen.View(width), "\n")
}

// updateTools routes keys while the screen is up. The one act it hands back
// is the checkout's answer, recorded through the seam /trust uses and said on
// the screen and in the transcript, the way /trust says it.
func (m Model) updateTools(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	screen := m.screens.tools()
	if screen == nil {
		return m.closeToolsScreen()
	}
	done, result := screen.Update(msg)
	if done {
		return m.closeToolsScreen()
	}
	if result.Offer == components.ToolsOfferNone || m.wiring.ToolSources.Trust == nil {
		return m, nil
	}
	var args []string
	if result.Offer == components.ToolsOfferDistrust {
		args = []string{"off"}
	}
	said := m.wiring.ToolSources.Trust(args)
	screen.Said = said
	m.appendEntry(entry{kind: entrySystem, text: said})
	return m, nil
}

// closeToolsScreen hands the screen back to the turn, the way its own esc
// does and the way the rail cell that opened it does, and leaves the rail
// holding what the screen last read.
func (m Model) closeToolsScreen() (tea.Model, tea.Cmd) {
	m.restateServers(m.toolsReading())
	m.screens = m.screens.without(stateTools)
	m.leaveSurface()
	m.syncViewport()
	return m, nil
}

// toolsShowing is the door's reading of whether its surface is up.
func toolsShowing(m Model) any {
	if m.state != stateTools || m.screens.tools() == nil {
		return nil
	}
	return m.screens.tools()
}

// renderToolsHint is the one line the screen leaves where the draft box was:
// the way out and nothing else, the way the spend screen's does.
func (m Model) renderToolsHint() string {
	return sty.SystemMsg.Render("tools · ") + segAs(keys.Screen.Quit, "back to the prompt").render()
}
