package chat

// /help and the key list.
//
// Neither list is written out here. A command is declared once in the
// registry (complete.go) and a key once in the register
// (internal/ui/keys), and a help text that spelled either out again was a
// second place for it to be wrong — which is how a conversation came to
// offer a command it would then refuse. So the rows below carry the prose
// and nothing else: which commands this session has comes from the registry,
// and how a key is spelled comes from the register, so a command that is not
// wired and a rebind both move the help with the handler.
//
// The paragraph beside a key is longer than the phrase a one-line hint gives
// it, because this list is where a reader who cannot find something comes to
// read a paragraph. It is declared with the key among the input's offers
// (internal/ui/keys), which the input's row of the register is read off, so
// the input cannot answer a key that has no row here. A command's paragraph
// is declared with the command, in the command table.
//
// The prose is written unwrapped and laid out at the width it is drawn at
// (helpsheet.go): a list authored at one width was a list that ran off the
// pane at every narrower one (docs/interface/principles.md#one-grid).

import (
	"cmp"
	"slices"
	"strings"

	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// helpText is /help as text: the sheet laid out at the width a copy of it is
// read at. The transcript draws the sheet itself (helpSheet), at the pane's
// width; this is what the row carries for a copy and a search.
func helpText(m *Model) string { return m.helpSheet().text() }

// helpSheet is /help: the command list this session actually has, one row per
// command the registry offers here, in the registry's order, with the
// paragraph this file keeps beside it — then what happens mid-turn, the keys,
// and the approval policy.
//
// The rows used to be one static string. That is how a conversation came to
// print a `/todo` row and then answer that `/todo` was not part of the
// session: the completion menu and the answer to a typed command were both
// asked what this session had wired, and the help was the only one of the
// three that was not. A reader who cannot type what the help offers has been
// told the wrong thing about the session they are in.
//
// A command that needs the turn to be finished is still listed. It drops out
// of the completion menu for the duration, because the menu is a thing you
// press; the help is what the session can do, not what it can do this second.
func (m Model) helpSheet() helpSheet {
	commands := helpSection{title: "commands", head: helpHeadWidth}
	for _, c := range slashCommands() {
		if c.enabled != nil && !c.enabled(&m) {
			continue
		}
		commands.rows = append(commands.rows, helpRow{
			head:  []string{helpHead(c)},
			paras: strings.Split(commandHelp(c), "\n"),
		})
	}
	mid := helpSection{title: "mid-turn", rows: []helpRow{{paras: []string{helpMidTurn(&m)}}}}
	return helpSheet{commands, mid, m.helpKeySection(), m.policySection()}
}

// helpHeadWidth is the command column, and helpHead the row's entry in it:
// the name with its argument hint where the two fit, and the name alone where
// they do not — a command with more forms than fit in a column has them in
// its paragraph, where there is room to say what each one is for.
const helpHeadWidth = 15

func helpHead(c slashCommand) string {
	if c.args != "" && len(c.name)+1+len(c.args) < helpHeadWidth {
		return c.name + " " + c.args
	}
	return c.name
}

// helpMidTurn is the paragraph under the list. It is here rather than on any
// one row because it is about the list as a whole: what happens if you type a
// command while the agent is still working.
//
// The exceptions are named off the registry rather than written out, because
// a list of them that had to be kept in step by hand would be the same defect
// this whole file is fixing one level down.
func helpMidTurn(m *Model) string {
	var names []string
	for _, c := range slashCommands() {
		if c.idleOnly == "" || (c.enabled != nil && !c.enabled(m)) {
			continue
		}
		names = append(names, c.name)
	}
	return "commands run while the agent is working — including while sub-agents are in " +
		"flight, which is the only time they exist. The exceptions are the ones that " +
		"rewrite the running conversation, or write into the tree the turn is working " +
		"in (" + strings.Join(names, ", ") + "); they say so and wait for the turn. " +
		"/clear asks instead: ending the session over a turn that is not over cancels " +
		"it, which is a question rather than a wait"
}

// commandHelp is a command's /help paragraph, declared with the command on
// its row of the command table (command.go).
func commandHelp(c slashCommand) string { return c.help }

// helpKeysText is the key list on its own, as text: what the chord prints,
// so the door Claude Code taught opens the same list /help holds. Every key
// the input offers has a row below and every spelling in the column is the
// register's, so a rebind moves the list with the handler and a key added to
// the register with no row here fails the test rather than quietly not being
// in the help.
func helpKeysText() string { return helpSheet{helpKeyList(helpKeyRows())}.text() }

// helpKeyList is the given rows as the key section.
func helpKeyList(rows []helpKeyRow) helpSection {
	s := helpSection{title: "keys", head: helpKeyWidth, keys: true}
	for _, r := range rows {
		s.rows = append(s.rows, helpRow{head: r.column(), paras: strings.Split(r.text, "\n")})
	}
	return s
}

// helpKeySection is the key list as this moment has it: the input's keys,
// and — while a child's routed command card waits beside the draft offering
// its grant — a row for that card's [a] in the card's own words. The
// register's words for the key promise a choice of how long, which that card
// does not draw: it makes one grant, for this command, every agent, this turn
// (docs/interface/surfaces.md#the-agent-manager).
//
// A conversation's list has no row for the mode chord: it has one mode, and
// the chord answers with a sentence saying so, so a row offering to cycle it
// would offer a key the session refuses
// (docs/capabilities/chat.md#a-conversation-has-one-mode).
func (m Model) helpKeySection() helpSection {
	rows := helpKeyRows()
	if m.conversation {
		rows = slices.DeleteFunc(slices.Clone(rows), func(r helpKeyRow) bool {
			return len(r.binds) == 1 && keys.Shown(r.binds[0]) == keys.Shown(keys.Draft.Mode)
		})
	}
	s := helpKeyList(rows)
	ask := m.activeChildAsk()
	if ask == nil || ask.Kind != subagent.AskCommand || !m.childAskCard(ask).AllowAlways {
		return s
	}
	s.rows = append(s.rows, helpRow{
		head: []string{keys.Bracket(keys.Decision.Always)},
		paras: []string{keys.AlwaysRouted + ", on the agent's card waiting now — the one grant " +
			"that card makes; it has no list of lengths to choose from"},
	})
	return s
}

// helpKeys is the key list the chord prints, as text.
func (m Model) helpKeys() string { return helpSheet{m.helpKeySection()}.text() }

// helpKeyWidth is the key column. It is wide enough for the longest spelling
// a row shows as one line (`[ctrl+a ctrl+e]`) plus the two spaces that
// separate a column from its prose.
const helpKeyWidth = 17

// helpKeyRow is one row of the key list, as the offer that declares it
// (keys.Offer) spells it: which keys it is about, how their spellings join,
// the column it writes out where the register does not spell it, and the
// paragraph beside them.
type helpKeyRow struct {
	// binds are the register bindings the row is about. The column is their
	// spellings, so a rebind moves the list with the handler.
	binds []keys.Binding
	sep   string
	key   string
	text  string
}

// column is the row's key column, one entry per line of it.
func (r helpKeyRow) column() []string {
	if r.key != "" {
		return strings.Split(r.key, "\n")
	}
	shown := make([]string, 0, len(r.binds))
	for _, b := range r.binds {
		shown = append(shown, keys.Bracket(b))
	}
	switch r.sep {
	case "":
		return shown[:1]
	case "\n":
		// A row about one binding that answers to more than one chord —
		// the handover, whose spelling names the alias the desktop cannot
		// take — gives each chord a line, because the two together are
		// wider than the column and a key in the column is never cut.
		if len(r.binds) == 1 {
			shown = shown[:0]
			for _, k := range r.binds[0].Keys() {
				shown = append(shown, keys.Bracketed(k))
			}
		}
		return shown
	default:
		return []string{strings.Join(shown, r.sep)}
	}
}

// helpKeyRows is the key list, in the order a reader meets the keys rather
// than the order the register declares them: what sends a message first, then
// what the draft does, then what takes the screen, then the ways out. Each
// row and its paragraph are declared once, with the key, among the input's
// offers (internal/ui/keys), and the order is each offer's weight.
func helpKeyRows() []helpKeyRow {
	offers := keys.InputOffers()
	slices.SortStableFunc(offers, func(a, b keys.Offer) int { return cmp.Compare(a.Weight, b.Weight) })
	rows := make([]helpKeyRow, 0, len(offers))
	for _, o := range offers {
		text := strings.Replace(o.Help, keys.RailDoors, railDoorList(), 1)
		rows = append(rows, helpKeyRow{binds: o.Binds, sep: o.Sep, key: o.Key, text: text})
	}
	return rows
}

// railDoorOrder is the order the rail click's paragraph names the doors in.
// Which command each door runs is not said here: it is read off the door's
// register row (railDoorCommands), so the paragraph names what a click does.
// A door missing from the order fails a test rather than the paragraph.
var railDoorOrder = []string{
	components.RailSummary, components.RailTurn, components.RailAlerts,
	components.RailChanges, components.RailAgents, components.RailSteps,
	components.RailTodo, components.RailContext, components.RailSpend,
	components.RailTools,
}

// railDoorList is the rail click's door list: each block beside the command
// its heading runs.
func railDoorList() string {
	commands := railDoorCommands()
	doors := make([]string, 0, len(railDoorOrder))
	for _, block := range railDoorOrder {
		doors = append(doors, block+" is "+commands[block])
	}
	return strings.Join(doors, ", ")
}

// railDoorCommands is the command a click on each door runs, by the door's
// block: the register row's own command, or the one its door names where a
// click runs another form. A further block sharing a row's door is left out,
// as it is never on the rail beside the door it shares.
func railDoorCommands() map[string]string {
	rows := []*mode{agentListMode()}
	for _, o := range overlays() {
		rows = append(rows, o)
	}
	commands := map[string]string{}
	for _, o := range rows {
		if o.door != nil {
			commands[o.door.block] = cmp.Or(o.doorCommand, o.command)
		}
	}
	return commands
}
