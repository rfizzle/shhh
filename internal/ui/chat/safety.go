package chat

// The safety reading: `/safety`, the session's whole boundary on one screen.
//
// Every fact on it already had an owner — the mode and the grants are
// /permissions', the working scope /add-dir's, containment /sandbox's, the
// checkout's standing /trust's, the servers /mcp's, the vault /secret's — and
// a person who wanted the whole of it had to ask eight commands and put the
// answers together themselves. This asks them, in the words each already
// answers in, and says under each section which of them changes it. It
// computes nothing of its own: a section that worked its answer out again
// would be a second answer to one question, and the day the two disagreed the
// reading would be the one that was wrong.
// See docs/capabilities/approvals-and-safety.md#one-reading-of-the-boundary.

import (
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
	"github.com/rfizzle/shhh/internal/web"
)

// Safety is what the reading needs that the chat cannot reach itself: the
// readings the command package owns. The zero value is a session that handed
// over none of them, and each section then says its capability is absent
// rather than leaving the section out.
type Safety struct {
	// Trust is the checkout's standing, the value the start screen is
	// handed. It is here too because a conversation draws no start screen and
	// its checkout was answered for all the same.
	Trust Trust
	// Servers are the MCP servers the session was told to reach, as the
	// connect left them. It is a call because a server that dies mid-session
	// is a different answer from the one it gave at the start.
	Servers func() []SafetyServer
	// Secrets are the names the vault holds now — never a value.
	Secrets func() []string
	// EnvMask reports secrets.env_mask: whether an inherited credential-shaped
	// variable is dropped from a command's environment.
	EnvMask bool
}

// SafetyServer is one MCP server as the reading states it.
type SafetyServer struct {
	Name string
	// ReadOnly is the person's own word in their config, which is the only
	// thing that lets a server's calls run without asking
	// (docs/capabilities/mcp.md#a-server-cannot-vouch-for-itself).
	ReadOnly bool
	// Status is what the connect came to, in /mcp's words.
	Status string
}

// WithSafety hands the reading what only the command package can read.
func (m Model) WithSafety(s Safety) Model {
	m.safety = s
	return m
}

// openSafety puts the screen up. It is built once per opening from the live
// session — the scope, the grants and the mode as they stand now — so
// reopening it after one of them moved is how the reader sees the move.
func (m Model) openSafety() (tea.Model, tea.Cmd) {
	m.screens = m.screens.with(stateSafety, &components.SafetyScreen{
		Sections: m.safetySections(),
		Subject:  m.safetySubject(),
	})
	m.enterSurface(stateSafety)
	return m, nil
}

// updateSafety routes keys while the screen is up. Nothing it answers
// changes the session, so the only answer that reaches the host is leaving.
func (m Model) updateSafety(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	screen := m.screens.safety()
	if screen == nil {
		return m.closeSafety()
	}
	if done, _ := screen.Update(msg); !done {
		return m, nil
	}
	return m.closeSafety()
}

// closeSafety hands the screen back to the turn.
func (m Model) closeSafety() (tea.Model, tea.Cmd) {
	m.screens = m.screens.without(stateSafety)
	m.leaveSurface()
	m.syncViewport()
	return m, nil
}

// safetyLines renders the screen, one row per line.
func (m Model) safetyLines(width, height int) []string {
	screen := m.screens.safety()
	if screen == nil {
		return nil
	}
	screen.SetSize(width, height)
	return strings.Split(screen.View(width), "\n")
}

// renderSafetyHint is the one line the screen leaves where the draft box
// was: the way out, and nothing else, because the screen holds the keyboard.
func (m Model) renderSafetyHint() string {
	return sty.SystemMsg.Render("safety · ") + segAs(keys.Screen.Quit, "back to the prompt").render()
}

// safetySubject is the header's field: the mode the session runs under and
// what contains its commands, the two facts the rest of the page qualifies.
func (m Model) safetySubject() string {
	if m.conversation {
		return "conversation · " + conversationModeWord
	}
	contained := m.containment.Mechanism
	if contained == "" {
		contained = "unconfined"
	}
	return m.policy.mode.String() + " · " + contained
}

// safetySections is the boundary in the order a person checks it: what runs
// without asking, where it may write, what holds its commands, what it may
// reach, what the checkout was let bring, and what it carries.
func (m Model) safetySections() []components.SafetySection {
	return []components.SafetySection{
		m.safetyPolicy(),
		m.safetyScope(),
		m.safetyContainment(),
		m.safetyWeb(),
		m.safetyTrust(),
		m.safetyServers(),
		m.safetySecrets(),
		m.safetyTools(),
	}
}

// safetyPolicy is /permissions bare and /permissions grants, and the two
// command lists as the configuration layered them — the checkout's own file
// can add to either and take from neither (internal/config/project.go).
func (m Model) safetyPolicy() components.SafetySection {
	sec := components.SafetySection{Title: "mode and policy", ChangedBy: "/permissions"}
	// A conversation has no mode and no cards, so there is no grant to list:
	// saying that is the whole of this section there, and a mode drawn over
	// it would be one the session does not run under
	// (docs/capabilities/chat.md#a-conversation-has-one-mode).
	if m.conversation {
		sec.Lines = []string{conversationModeNote, "No card is drawn, so nothing is granted."}
		return sec
	}
	sec.Lines = append(lines(m.modeStatus()), "")
	sec.Lines = append(sec.Lines, lines(m.grantStatus())...)
	if len(m.policy.allowlist) > 0 {
		sec.Lines = append(sec.Lines, "  allowlist  "+strings.Join(m.policy.allowlist, ", "))
	}
	if len(m.policy.denylist) > 0 {
		sec.Lines = append(sec.Lines, "  denylist   "+strings.Join(m.policy.denylist, ", "))
	}
	return sec
}

// safetyScope is bare /add-dir, and the directories the scope counts as
// sensitive: the ones nothing but a person's own /add-dir will grant.
func (m Model) safetyScope() components.SafetySection {
	sec := components.SafetySection{Title: "where it may write", ChangedBy: "/add-dir"}
	if m.conversation {
		sec.Lines, sec.Absent = []string{"a conversation writes nothing, so it has no working scope"}, true
		return sec
	}
	sec.Lines = lines(m.scopeStatus())
	if m.scope == nil {
		sec.Absent = true
		return sec
	}
	sec.Lines = append(sec.Lines, "Sensitive — asks whatever the mode, and only /add-dir grants it:")
	// Written whole rather than abbreviated from the left the way a card
	// writes a directory — a card has one line to spend, and this screen
	// wraps — with the home directory as the one prefix shortened, since
	// every one of these lives under it.
	home, _ := os.UserHomeDir()
	home = filepath.Clean(home)
	for _, dir := range append(sandbox.CredentialPaths(), sandbox.ShhhPaths()...) {
		if rest, ok := strings.CutPrefix(dir, home+string(filepath.Separator)); ok && home != "." {
			dir = filepath.Join("~", rest)
		}
		sec.Lines = append(sec.Lines, "  "+dir)
	}
	return sec
}

// safetyContainment is the containment line /status prints and the report
// /sandbox doctor prints, the latter against the working scope as it stands
// now rather than as the session started.
func (m Model) safetyContainment() components.SafetySection {
	sec := components.SafetySection{Title: "containment", ChangedBy: "/sandbox"}
	if m.conversation {
		sec.Lines, sec.Absent = []string{"a conversation runs no commands, so there is nothing to contain"}, true
		return sec
	}
	status := m.containmentStatus()
	if status == "" {
		sec.Lines, sec.Absent = []string{"command containment is not configured in this session"}, true
		return sec
	}
	sec.Lines = lines(status)
	required := "off"
	if m.containment.Required || m.containment.Refusal != "" {
		required = "on"
	}
	sec.Lines = append(sec.Lines, "sandbox.require is "+required)
	sec.Absent = m.containment.Mechanism == ""
	if m.containment.Now != nil {
		sec.Lines = append(append(sec.Lines, ""), lines(m.containment.Now())...)
	} else if m.containment.Report != "" {
		sec.Lines = append(append(sec.Lines, ""), lines(m.containment.Report)...)
	}
	return sec
}

// safetyWeb is the two host lists and this session's grants, as
// hostAllowlist merges them for the policy, each grant with when it ends in
// the words the card that made it used.
func (m Model) safetyWeb() components.SafetySection {
	sec := components.SafetySection{Title: "web", ChangedBy: "/permissions revoke hosts"}
	if !m.hasTool(web.FetchToolName) {
		sec.Lines, sec.Absent = []string{"this session has no web tools, so it reaches no host"}, true
		return sec
	}
	if m.conversation {
		sec.Lines = append(sec.Lines, "a conversation fetches without a card, so no host is granted here")
	}
	allowed := m.hostAllowlist()
	for i, h := range allowed {
		if i < len(m.policy.allowHosts) {
			sec.Lines = append(sec.Lines, "  allowed    "+h+" from web.allow_hosts — not this session's to revoke")
			continue
		}
		sec.Lines = append(sec.Lines, "  granted    "+h+", that host alone — "+endsWithSession)
	}
	for _, h := range m.policy.turn.Hosts {
		sec.Lines = append(sec.Lines, "  granted    "+h+", that host alone — "+endsWithTurn)
	}
	for _, h := range m.policy.denyHosts {
		sec.Lines = append(sec.Lines, "  refused    "+h+" from web.deny_hosts — before anything can allow it")
	}
	if len(allowed)+len(m.policy.turn.Hosts) == 0 && !m.conversation {
		sec.Lines = append(sec.Lines, "no host is fetched without asking — every fetch draws a card")
	}
	return sec
}

// safetyTrust is the checkout's standing: trusted or not, what was withheld,
// and what changed since a session here last read it — /status's reading of
// it, with the answer itself on the line above.
func (m Model) safetyTrust() components.SafetySection {
	sec := components.SafetySection{Title: "what the checkout was let load", ChangedBy: "/trust"}
	t := m.safety.Trust
	if m.start != nil {
		t = m.trust()
	}
	switch {
	case t.Granted:
		sec.Lines = []string{"trusted — its skills, agent profiles, suites, hooks and servers load"}
	case len(t.Withheld) > 0:
		sec.Lines, sec.Absent = []string{"not trusted"}, true
	default:
		sec.Lines = []string{"not trusted, and it declares nothing that would load"}
	}
	sec.Lines = append(sec.Lines, lines(t.status())...)
	return sec
}

// safetyServers is each MCP server, whether its calls run or ask, and what
// its connect came to.
func (m Model) safetyServers() components.SafetySection {
	sec := components.SafetySection{Title: "mcp", ChangedBy: "/mcp"}
	var servers []SafetyServer
	if m.safety.Servers != nil {
		servers = m.safety.Servers()
	}
	if len(servers) == 0 {
		sec.Lines, sec.Absent = []string{"no MCP servers in this session"}, true
		return sec
	}
	for _, s := range servers {
		reach := "asks before acting"
		if s.ReadOnly {
			reach = "read-only, runs without asking"
		}
		line := "  " + s.Name + " — " + reach
		if s.Status != "" {
			line += " · " + s.Status
		}
		sec.Lines = append(sec.Lines, line)
	}
	return sec
}

// safetySecrets is the vault's names and whether the environment mask is on.
// The values are never on this screen: the vault is the one thing in the
// session that holds them, and nothing that draws reaches it.
func (m Model) safetySecrets() components.SafetySection {
	sec := components.SafetySection{Title: "secrets", ChangedBy: "/secret"}
	var names []string
	if m.safety.Secrets != nil {
		names = m.safety.Secrets()
	}
	if len(names) == 0 {
		sec.Lines = []string{"no secrets declared"}
	}
	for _, n := range names {
		sec.Lines = append(sec.Lines, "  $"+n+" — in every command, masked everywhere else")
	}
	if m.safety.EnvMask {
		sec.Lines = append(sec.Lines, "secrets.env_mask is on — an inherited *_KEY, *_SECRET or *_TOKEN never reaches a command")
	} else {
		sec.Lines = append(sec.Lines, "secrets.env_mask is off — inherited variables reach commands as they are")
	}
	return sec
}

// safetyTools is the registered toolset in its tiers. A server's tools are
// counted rather than listed, because the server's own section says whether
// they ask and a list of ninety names says nothing more.
func (m Model) safetyTools() components.SafetySection {
	sec := components.SafetySection{Title: "tools", ChangedBy: "/permissions, which decides which tiers ask"}
	var read, exec, write, asks []string
	served := 0
	for _, d := range m.toolDefs {
		switch name := d.Name; {
		case m.mcp.Has != nil && m.mcp.Has(name):
			served++
		case tools.IsMutating(name) || name == structural.GitWriteToolName:
			write = append(write, name)
		case name == tools.ExecCommandName || name == process.ToolName:
			exec = append(exec, name)
		// A conversation answers its own fetches without a card, so the one
		// tool that asks everywhere else is a read here.
		case m.conversation && name == web.FetchToolName:
			read = append(read, name)
		case m.requiresApproval(provider.ToolCall{Name: name}):
			asks = append(asks, name)
		default:
			read = append(read, name)
		}
	}
	if len(read)+len(exec)+len(write)+len(asks)+served == 0 {
		sec.Lines, sec.Absent = []string{"no tools registered"}, true
		return sec
	}
	for _, tier := range []struct {
		name  string
		names []string
	}{
		{"read-only", read}, {"execute", exec}, {"mutating", write}, {"asks", asks},
	} {
		if len(tier.names) > 0 {
			sec.Lines = append(sec.Lines, "  "+padRight(tier.name, 10)+" "+strings.Join(tier.names, ", "))
		}
	}
	if served > 0 {
		sec.Lines = append(sec.Lines, "  "+padRight("servers", 10)+" "+plural(served, "tool")+" — see mcp above")
	}
	return sec
}

// hasTool reports whether the session registered a tool by that name.
func (m Model) hasTool(name string) bool {
	for _, d := range m.toolDefs {
		if d.Name == name {
			return true
		}
	}
	return false
}

// lines splits an owner's answer into the lines the screen lays out.
func lines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// padRight pads a column label to its width.
func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
