package chat

// MCP servers in a session. The tools a connected server offers are on the
// executor chain like every other optional tool; what the chat model needs
// to know is which names are a server's, which of those the person marked
// read-only, what became of every server the session was told to reach, and
// what /mcp prints
// (docs/capabilities/mcp.md#a-call-is-a-command-unless-you-said-otherwise).

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// MCP wires the session's MCP servers into the chat TUI. The zero value
// means the session connected none.
type MCP struct {
	// Has reports whether a tool name belongs to a connected server.
	Has func(name string) bool
	// ReadOnly reports whether the tool's server was marked read-only by
	// the person, which is what lets its rows draw as reads.
	ReadOnly func(name string) bool
	// Manage backs the /mcp slash command.
	Manage func(args []string) string
	// Prompts are the commands the servers publish. It is a call rather
	// than a value — unlike Sources, which nothing in a session moves —
	// because a server may say its prompt list changed and Refresh takes
	// that at the next boundary
	// (docs/capabilities/mcp.md#a-server-may-change-what-it-offers).
	Prompts func() []mcp.Prompt
	// Render asks a server to fill one of its prompts in. It reaches the
	// server, so it is never called on the UI goroutine.
	Render func(ctx context.Context, name string, args map[string]string) (string, error)
	// Refresh applies whatever the servers have re-listed, and reports
	// whether anything moved. The session calls it where a boundary is: a
	// catalog that moved mid-round would change what a result answers.
	Refresh func() bool
	// Sources is one entry per server the session was told to reach, as the
	// connect left it, in the rail's own vocabulary — a second enum in
	// between would only be this one restated, and a mapping to get it wrong
	// in. It is a value rather than a call because nothing in a session
	// changes it: the servers are dialled once before the first turn, and
	// trusting one takes effect in the next session
	// (docs/capabilities/mcp.md#a-checkout-cannot-start-a-process).
	Sources []components.InspectorToolSource
}

// WithMCP enables /mcp and tells the transcript which rows are server
// calls.
func (m Model) WithMCP(servers MCP) Model {
	m.mcp = servers
	if m.mcp.ReadOnly == nil {
		m.mcp.ReadOnly = func(string) bool { return false }
	}
	return m
}

// toolRailRows is how many source rows the TOOLS block draws before it folds
// the rest into a count. Four, the same bound the backlog's block uses: the
// block answers "is what I configured up", and past four rows that is a
// listing, which is what /mcp is for.
const toolRailRows = 4

// inspectorTools is the TOOLS block: the built-in toolset first, then every
// server the session was told to reach, and what the recall budget left out
// of the prompt. It is present only when something could have gone missing —
// an external source, or a memory that did not fit — because a session with
// nothing but its own tools has no way to have lost any, and the block would
// be a row saying the obvious.
func (m Model) inspectorTools() *components.InspectorTools {
	if len(m.mcp.Sources) == 0 && m.memory.Omitted == 0 {
		return nil
	}
	t := &components.InspectorTools{MemoryOmitted: m.memory.Omitted}
	if n := m.builtinToolCount(); n > 0 {
		t.Up++
		t.Sources = append(t.Sources, components.InspectorToolSource{
			Name: "built-in", State: components.ToolSourceUp, Note: plural(n, "tool"),
		})
	}
	// The block exists so a source that did not answer leaves a trace, which
	// decides what the fold is allowed to take: a source that is up is the one
	// the reader can afford not to see, so the healthy rows go first and every
	// other kind keeps its row for as long as there is one.
	keep := make([]bool, len(m.mcp.Sources))
	room := max(toolRailRows-len(t.Sources), 0)
	for _, healthy := range []bool{false, true} {
		for i, s := range m.mcp.Sources {
			if room == 0 {
				break
			}
			if keep[i] || (s.State == components.ToolSourceUp) != healthy {
				continue
			}
			keep[i], room = true, room-1
		}
	}
	for i, s := range m.mcp.Sources {
		// The heading counts what answered over every source, so a server the
		// fold took still counts towards it.
		if s.State == components.ToolSourceUp {
			t.Up++
		}
		if !keep[i] {
			t.More++
			continue
		}
		t.Sources = append(t.Sources, s)
	}
	return t
}

// builtinToolCount is every registered tool that did not come from a server.
// The session already knows both halves — the definitions it was built with
// and which names a server owns — so the count is a walk rather than a number
// anything has to keep. Without the half that says which names are a server's
// it is zero rather than a total that silently counts every server's tools as
// shhh's own: a count nobody can vouch for is worse than no row.
func (m Model) builtinToolCount() int {
	if m.mcp.Has == nil {
		return 0
	}
	n := 0
	for _, d := range m.toolDefs {
		if m.mcp.Has(d.Name) {
			continue
		}
		n++
	}
	return n
}

// The prompts a server publishes are commands of this session, not rows of
// the package's own table: the table is built once for the process and a
// server's catalog belongs to one session, and can change inside it. So the
// dispatch, the completion menu and the argument specs all reach for the
// session's own list, and a session with no servers has none of it.

// mcpPrompts is the session's prompt catalog, or nothing.
func (m Model) mcpPrompts() []mcp.Prompt {
	if m.mcp.Prompts == nil {
		return nil
	}
	return m.mcp.Prompts()
}

// mcpPrompt finds the prompt a typed command names. The name arrives with
// its slash, the way every other command name does.
func (m Model) mcpPrompt(name string) (mcp.Prompt, bool) {
	if !strings.HasPrefix(name, "/") {
		return mcp.Prompt{}, false
	}
	for _, p := range m.mcpPrompts() {
		if p.Name == name[1:] {
			return p, true
		}
	}
	return mcp.Prompt{}, false
}

// mcpCommandMatches are the prompt rows the completion menu offers for a
// typed token. They come after the registry's own rows because a real
// command must never be outranked by a server's: the registry is what the
// session promises to answer, and a server's catalog is what somebody else
// happens to publish today.
func (m *Model) mcpCommandMatches(token string) []completionItem {
	var out []completionItem
	for _, p := range m.mcpPrompts() {
		name := "/" + p.Name
		if !strings.HasPrefix(name, token) {
			continue
		}
		desc := p.Description
		if desc == "" {
			desc = "a prompt from " + p.Server
		}
		out = append(out, completionItem{
			name: name, args: p.Usage(), desc: desc, space: len(p.Arguments) > 0,
		})
	}
	return out
}

// mcpPromptCommand is the registry row a prompt stands in as, so argument
// completion reaches it through the one lookup every command goes through.
// Every position offers the same key list: the protocol gives arguments no
// order, so `name=value` in any order is what the command takes, and a menu
// that pretended otherwise would be inventing one.
func mcpPromptCommand(p mcp.Prompt) slashCommand {
	c := slashCommand{name: "/" + p.Name, args: p.Usage(), desc: p.Description}
	if len(p.Arguments) == 0 {
		return c
	}
	opts := make([]argOption, 0, len(p.Arguments))
	for _, a := range p.Arguments {
		desc := a.Description
		if a.Required {
			desc = strings.TrimSpace("required · " + desc)
		}
		opts = append(opts, argOption{value: a.Name + "=", desc: desc})
	}
	for range p.Arguments {
		c.argSpecs = append(c.argSpecs, argSpec{options: opts})
	}
	return c
}

// mcpPromptTimeout bounds one prompts/get. It is short because the request
// is one round trip to a server that has already answered a handshake and a
// tool listing, and because the person is sitting on the command: a prompt
// that has not come back in this long is one they should be told about
// rather than left waiting for.
const mcpPromptTimeout = 30 * time.Second

// mcpPromptMsg is a rendered prompt on its way back to the session. It
// carries the command line it came from so the transcript shows what was
// typed rather than the page the server wrote.
type mcpPromptMsg struct {
	shown string
	text  string
	err   error
}

// runMCPPrompt asks the server for the prompt's messages. The request goes
// off the UI goroutine like every other request in this surface: a server
// that is slow to answer must not be able to stop the screen redrawing.
func (m Model) runMCPPrompt(p mcp.Prompt, words []string) (tea.Model, tea.Cmd) {
	if m.mcp.Render == nil {
		return m.surfaceNotice("/" + p.Name + " cannot be rendered in this session.")
	}
	args, err := mcpPromptValues(p, words)
	if err != nil {
		return m.surfaceNotice(err.Error())
	}
	shown := strings.TrimSpace("/" + p.Name + " " + strings.Join(words, " "))
	render := m.mcp.Render
	name := p.Name
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), mcpPromptTimeout)
		defer cancel()
		text, err := render(ctx, name, args)
		return mcpPromptMsg{shown: shown, text: text, err: err}
	}
}

// mcpPromptArgs reads the `name=value` words a prompt was typed with. An
// unknown name and a missing required one are both refused here rather than
// sent: the server would refuse them a round later, in its own words, and
// the person would have paid a turn to find out what the menu already knew.
func mcpPromptValues(p mcp.Prompt, words []string) (map[string]string, error) {
	known := map[string]bool{}
	for _, a := range p.Arguments {
		known[a.Name] = true
	}
	args := map[string]string{}
	for _, w := range words {
		name, value, ok := strings.Cut(w, "=")
		if !ok {
			return nil, fmt.Errorf("/%s takes its arguments as name=value; %q is not one. %s", p.Name, w, mcpPromptUsageLine(p))
		}
		if !known[name] {
			return nil, fmt.Errorf("/%s takes no argument called %s. %s", p.Name, name, mcpPromptUsageLine(p))
		}
		args[name] = value
	}
	var missing []string
	for _, a := range p.Arguments {
		if a.Required && args[a.Name] == "" {
			missing = append(missing, a.Name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("/%s needs %s. %s", p.Name, strings.Join(missing, " and "), mcpPromptUsageLine(p))
	}
	return args, nil
}

func mcpPromptUsageLine(p mcp.Prompt) string {
	if usage := p.Usage(); usage != "" {
		return "Usage: /" + p.Name + " " + usage
	}
	return "Usage: /" + p.Name
}

// applyMCPPrompt lands a rendered prompt. It is the person's turn — they
// typed the command — so it starts one, and it queues as steering while the
// agent works, exactly as an activated skill's text does: the alternative
// is a turn started on top of the running one.
func (m Model) applyMCPPrompt(msg mcpPromptMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.surfaceNotice(msg.shown + " did not render: " + msg.err.Error())
	}
	text := strings.TrimSpace(msg.text)
	if text == "" {
		return m.surfaceNotice(msg.shown + " came back empty; the server rendered no messages.")
	}
	if m.working() || m.decisionUngated() {
		m.steering = append(m.steering, text)
		m.syncViewport()
		return m.surfaceNotice(msg.shown + " queued for the next round.")
	}
	return m.sendUserMessageAs(text, msg.shown)
}

// refreshMCP takes whatever the servers have re-listed. What it buys this
// surface is the commands: a prompt a server published mid-turn is typable
// from the next line on. What the model was told stays as it was — its
// tools and the block naming the resources are the session's
// (docs/capabilities/mcp.md#a-server-may-change-what-it-offers).
//
// The call site is a submitted line rather than a chosen boundary, and the
// toolset is what makes it safe: it applies nothing while a round's calls
// are out, which matters because a line submitted mid-turn is steering.
func (m Model) refreshMCP() {
	if m.mcp.Refresh != nil {
		m.mcp.Refresh()
	}
}
