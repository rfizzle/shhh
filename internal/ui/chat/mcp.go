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
	"github.com/rfizzle/shhh/internal/provider"
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
	// than a value because a server may say its prompt list changed, or
	// join after the session opened, and Refresh and Join take either at
	// the next boundary
	// (docs/capabilities/mcp.md#a-server-may-change-what-it-offers).
	Prompts func() []mcp.Prompt
	// Render asks a server to fill one of its prompts in. It reaches the
	// server, so it is never called on the UI goroutine.
	Render func(ctx context.Context, name string, args map[string]string) (string, error)
	// Refresh applies whatever the servers have re-listed and takes note of
	// any server that stopped answering, and reports whether anything
	// moved. The session calls it where a boundary is: a catalog that moved
	// mid-round would change what a result answers.
	Refresh func() bool
	// Restate is read only when Refresh says something moved: the servers
	// as the rail names them now, and the lines the session owes the reader
	// about one that died. Both halves come from one read of the reports,
	// so a row drawn as failed and a note saying so cannot disagree
	// (docs/capabilities/mcp.md#a-server-that-dies-is-noticed).
	Restate func() ([]components.InspectorToolSource, []string)
	// Abandon gives up every server call in flight. It is the turn's cancel
	// reaching the one request a cancelled turn cannot otherwise stop: a
	// tool executor is handed a name and arguments and no context, so a
	// call to a hung server runs on the toolset's own clock until Abandon
	// or the call timeout ends it
	// (docs/capabilities/mcp.md#a-call-that-hangs-can-be-given-up).
	Abandon func()
	// Sources is one entry per server the session was told to reach, as the
	// rail last read it, in the rail's own vocabulary — a second enum in
	// between would only be this one restated, and a mapping to get it wrong
	// in. It is a value for a settled server, which only a boundary moves:
	// trusting one takes effect in the next session
	// (docs/capabilities/mcp.md#a-checkout-cannot-start-a-process).
	Sources []components.InspectorToolSource
	// Live is the servers as their connects stand this moment, read for a
	// row Sources still holds as starting: a connect ends on its own
	// goroutine, and its row says up or error from the next frame rather
	// than from the next boundary
	// (docs/capabilities/mcp.md#a-server-that-did-not-answer-is-a-row).
	Live func() []components.InspectorToolSource
	// Join takes the connects that ended since the last turn boundary,
	// handed the system prompt the conversation carries now, and says what
	// moved; false is nothing. It is called only between turns, so the
	// model's tools never change inside a round
	// (docs/capabilities/mcp.md#a-server-may-change-what-it-offers).
	Join func(system string) (MCPJoin, bool)
}

// MCPJoin is what a boundary's join moved.
type MCPJoin struct {
	// Notes are the transcript's lines: one per server that came up or did
	// not start.
	Notes []string
	// Sources are the rows as they stand after the join.
	Sources []components.InspectorToolSource
	// System is the system prompt with the servers' block and the toolbox
	// said again over the new set; empty leaves the prompt as it was, which
	// is a join where nothing came up.
	System string
	// ServerTools are every server tool the request now carries, which
	// replace the servers' share of what the session counts as its tools.
	ServerTools []ToolTokens
	// Gated are the approval cards of the server tools that ask, which a
	// tool that joined has to carry before its first call can be made.
	Gated map[string]GatedPreviewFunc
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
	sources := m.mcpSources()
	if len(sources) == 0 && m.memory.Omitted == 0 {
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
	// other kind — a server still starting as much as one that failed — keeps
	// its row for as long as there is one.
	keep := make([]bool, len(sources))
	room := max(toolRailRows-len(t.Sources), 0)
	for _, healthy := range []bool{false, true} {
		for i, s := range sources {
			if room == 0 {
				break
			}
			if keep[i] || (s.State == components.ToolSourceUp) != healthy {
				continue
			}
			keep[i], room = true, room-1
		}
	}
	for i, s := range sources {
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
	return len(m.builtinToolNames())
}

// builtinToolNames is every registered tool that did not come from a server,
// by name. A session with no servers has nothing to tell apart, so every name
// is its own — the tools screen lists them where the rail, which is not up in
// such a session, would have drawn no row.
func (m Model) builtinToolNames() []string {
	var out []string
	for _, d := range m.toolDefs {
		if m.mcp.Has != nil && m.mcp.Has(d.Name) {
			continue
		}
		out = append(out, d.Name)
	}
	return out
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
		return m.surfaceNotice("/" + p.Name + " cannot be rendered in this session")
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
		return "usage: /" + p.Name + " " + usage
	}
	return "usage: /" + p.Name
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
		return m.surfaceNotice(msg.shown + " came back empty; the server rendered no messages")
	}
	if m.working() || m.decisionUngated() {
		m.steering = append(m.steering, steeringItem{text: text, id: m.queue.next(), kind: queuedPrompt})
		m.syncViewport()
		return m.surfaceNotice(msg.shown + " queued for the next round")
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
//
// It is also where a server that stopped answering reaches the screen. The
// death happened inside a round, where the rail cannot be redrawn from and
// a transcript line would land between a call and its result; here it is a
// row that greys and a line saying what the session lost
// (docs/capabilities/mcp.md#a-server-that-dies-is-noticed).
//
// And it is where a server that answered after the session opened joins,
// when no turn is in flight: its row turned when its connect ended, and
// here its tools, the block naming it and the toolbox reach the model, from
// the turn this line starts (joinMCP).
func (m *Model) refreshMCP() {
	m.joinMCP()
	if m.mcp.Refresh == nil || !m.mcp.Refresh() || m.mcp.Restate == nil {
		return
	}
	sources, notes := m.mcp.Restate()
	m.mcp.Sources = sources
	for _, note := range notes {
		m.appendEntry(entry{kind: entrySystem, text: note})
	}
}

// joinMCP takes the connects that ended since the last boundary, between
// turns only: a line typed while a turn runs is steering, and a tool list
// that moved under it would change the model's tools inside the turn it is
// steering. What it takes moves together — the system prompt, the tools the
// session counts, the approval cards a server's tools ask on, the rows — and
// the transcript says what joined, at the line, before the message that
// starts the turn (docs/capabilities/mcp.md#a-server-may-change-what-it-offers).
func (m *Model) joinMCP() {
	if m.mcp.Join == nil || m.turnInFlight() {
		return
	}
	system := ""
	msgs := m.agent.Messages()
	if len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		system = msgs[0].Content
	}
	j, ok := m.mcp.Join(system)
	if !ok {
		return
	}
	if j.System != "" && system != "" && j.System != system {
		next := append([]provider.Message(nil), msgs...)
		next[0].Content = j.System
		m.agent.SetMessages(next)
	}
	if j.ServerTools != nil {
		defs := make([]ToolTokens, 0, len(m.toolDefs)+len(j.ServerTools))
		for _, d := range m.toolDefs {
			if m.mcp.Has == nil || !m.mcp.Has(d.Name) {
				defs = append(defs, d)
			}
		}
		*m = m.WithToolDefinitions(append(defs, j.ServerTools...))
	}
	if len(j.Gated) > 0 {
		// A copy, not the map the session was built with: the model is a
		// value, and an earlier copy of it must not gain a card it never had.
		gated := make(map[string]GatedPreviewFunc, len(m.gatedTools)+len(j.Gated))
		for name, f := range m.gatedTools {
			gated[name] = f
		}
		for name, f := range j.Gated {
			gated[name] = f
		}
		m.gatedTools = gated
	}
	if j.Sources != nil {
		m.mcp.Sources = j.Sources
	}
	for _, note := range j.Notes {
		m.appendEntry(entry{kind: entrySystem, text: note})
	}
}

// mcpSources is the servers as the rail draws them this frame: the rows
// Sources holds, with each one Sources still holds as starting read again
// from the connects as they stand, so a row turns the moment its connect
// ends rather than at the next boundary — and a row still starting carries
// its seconds, counted on the session's clock.
func (m Model) mcpSources() []components.InspectorToolSource {
	srcs := m.mcp.Sources
	starting := false
	for _, s := range srcs {
		starting = starting || s.State == components.ToolSourceStarting
	}
	if !starting {
		return srcs
	}
	var live []components.InspectorToolSource
	if m.mcp.Live != nil {
		live = m.mcp.Live()
	}
	out := make([]components.InspectorToolSource, len(srcs))
	for i, s := range srcs {
		if s.State == components.ToolSourceStarting && len(live) == len(srcs) {
			s = live[i]
		}
		out[i] = startingNote(s)
	}
	return out
}

// mcpStarting reports whether a server is still connecting, which keeps the
// tick up: a starting row's note is its seconds, read off the clock at each
// paint, and the tick is what repaints it.
func (m Model) mcpStarting() bool {
	for _, s := range m.mcpSources() {
		if s.State == components.ToolSourceStarting {
			return true
		}
	}
	return false
}

// startingNote is a starting source with its note: the seconds since its
// connect began, in the rail's one clock format.
func startingNote(s components.InspectorToolSource) components.InspectorToolSource {
	if s.State == components.ToolSourceStarting && !s.Since.IsZero() {
		s.Note = components.FormatElapsed(max(clock().Sub(s.Since), 0))
	}
	return s
}

// abandonMCPCalls gives up every server call in flight, which is what the
// turn's cancel means for a request the turn's own context never reached.
func (m Model) abandonMCPCalls() {
	if m.mcp.Abandon != nil {
		m.mcp.Abandon()
	}
}
