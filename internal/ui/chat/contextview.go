package chat

// The context surface (docs/interface/surfaces.md#the-context-surface).
//
// `/stats` answers "where did the window go" as a paragraph of aligned text,
// which is a table pretending to be a sentence, and it answers it only down
// to the category. This is the same accounting drawn as a meter and itemised
// below the category: which tool definition, which tool's output.
//
// It reads contextAccounting like every other occupancy surface, so the
// rails, the pressure card and this cannot quote three different totals. The
// itemisation is derived from the same message list the accounting walks and
// scaled by the same factor, so an opened group sums to the category above it
// whether the total is the provider's or ours.

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// contextItemsShown is how many parts of a group are named before the rest
// are counted together. Ten is what fits under a fold without the surface
// becoming the list it is a summary of, and the tail is still stated rather
// than dropped (invariant 4).
const contextItemsShown = 10

// turnLabelRunes is how much of a turn's opening message names it. One short
// line: long enough to recognise the exchange, short enough that the column
// stays a column.
const turnLabelRunes = 34

// ToolTokens is one registered tool definition and what its schema costs the
// window. The host counts them because it is the one that built the toolset:
// which tools a session has depends on what the machine turned out to have.
type ToolTokens struct {
	Name   string
	Tokens int64
}

// WithToolDefinitions records the registered tool definitions and their
// estimated cost. The total is the occupancy breakdown's tool category, and
// the rows are what the context surface itemises it into.
func (m Model) WithToolDefinitions(defs []ToolTokens) Model {
	m.toolDefs = defs
	m.toolDefTokens = 0
	for _, d := range defs {
		m.toolDefTokens += d.Tokens
	}
	return m
}

// openContext puts the surface up. It is built once per opening rather than
// per render: the accounting it draws describes the conversation as it stood
// when the reader asked, and a surface that moved under them while they read
// it would be answering a question they had stopped asking.
func (m Model) openContext() (tea.Model, tea.Cmd) {
	screen := m.contextScreenData()
	m.context = &screen
	m.enterSurface(stateContext)
	return m, nil
}

// updateContext routes keys while the surface is up.
func (m *Model) answerContext(msg tea.KeyPressMsg) (bool, overlayAction) {
	if m.context == nil {
		return true, m.closeContext()
	}
	done, _ := m.context.Update(msg)
	if !done {
		return false, overlayAction{}
	}
	return true, m.closeContext()
}

// closeContext hands the screen back to the turn, which may have moved on
// while the surface was up. The folds the reader opened are remembered on the
// way out, because the surface itself is rebuilt from the accounting the next
// time it is asked for.
func (m *Model) closeContext() overlayAction {
	if m.context != nil {
		m.contextOpen = map[string]bool{}
		for _, g := range m.context.Groups {
			m.contextOpen[g.Label] = g.Open
		}
	}
	m.context = nil
	return overlayAction{close: true}
}

// contextLines renders the surface, one row per line.
func (m Model) contextLines() []string {
	if m.context == nil {
		return nil
	}
	return strings.Split(m.context.View(m.contentWidth()), "\n")
}

// contextScreenData builds the surface from the session's own accounting.
func (m Model) contextScreenData() components.ContextScreen {
	b := m.contextAccounting()
	window := m.contextWindow()
	total := b.total()
	screen := components.ContextScreen{
		Model:      m.modelName,
		Provider:   m.providerName,
		Window:     formatWindowSize(window),
		Tokens:     "~" + formatTokenCount(total),
		Pct:        percentOf(total, window),
		Warn:       warnThresholdPercent,
		Alert:      trimThresholdPercent,
		Source:     b.source(),
		Categories: m.contextCategories(b, window),
		Groups:     m.contextGroups(b),
	}
	// What the last request read from cache is already in the vitals; this
	// is where it is worth stating, because it is the price of the window
	// this screen is otherwise a picture of. A session whose prefix keeps
	// matching pays a fraction for the same occupancy, and a run of low
	// figures is what a conversation being rewritten under the provider
	// looks like from the outside.
	if in := m.vitals.lastIn; in > 0 {
		screen.CacheRead = formatTokenCount(m.vitals.lastCached)
		screen.CacheInput = formatTokenCount(in)
		screen.CachePct = percentOf(m.vitals.lastCached, in)
	}
	return screen
}

// contextCategories is the legend: the accounting's categories in the
// surface's own words, then what is left. A category the session has nothing
// in is left out rather than drawn as a zero, except the three that are
// always there — a session with no tool results has not had any yet, and a
// row saying so is worth more than a row missing.
func (m Model) contextCategories(b contextBreakdown, window int64) []components.ContextCategory {
	rows := []components.ContextCategory{}
	for _, row := range []struct {
		label  string
		tokens int64
		tone   components.ContextTone
		always bool
	}{
		{"system prompt", b.System, components.ContextPrompt, true},
		{"project context", b.Project, components.ContextProject, false},
		{"tool definitions", b.Tools, components.ContextTools, true},
		{"messages", b.Messages, components.ContextMessages, true},
		{"tool results", b.ToolResults, components.ContextOutput, true},
	} {
		if row.tokens == 0 && !row.always {
			continue
		}
		rows = append(rows, components.ContextCategory{
			Label:  row.label,
			Tokens: formatTokenCount(row.tokens),
			Pct:    formatSharePct(row.tokens, window),
			Share:  shareOf(row.tokens, window),
			Tone:   row.tone,
		})
	}
	free := max(window-b.total(), 0)
	return append(rows, components.ContextCategory{
		Label:  "free space",
		Tokens: formatTokenCount(free),
		Pct:    formatSharePct(free, window),
		Share:  shareOf(free, window),
		Tone:   components.ContextFree,
	})
}

// contextGroups are the categories made of many things. Both are about tools,
// and they are separate groups because they answer separate questions: what
// the session pays to have a tool available, and what it paid for having used
// one.
func (m Model) contextGroups(b contextBreakdown) []components.ContextGroup {
	var groups []components.ContextGroup
	if g, ok := m.toolDefGroup(b.Tools); ok {
		groups = append(groups, g)
	}
	if g, ok := m.toolResultGroup(b.ToolResults); ok {
		groups = append(groups, g)
	}
	if g, ok := m.messageTurnGroup(b.Messages); ok {
		groups = append(groups, g)
	}
	return m.carryOpen(groups)
}

// carryOpen restores the folds the reader had opened. The surface is rebuilt
// on every opening, so without this a reader who opened a group, left, and
// came back would find it shut again.
func (m Model) carryOpen(groups []components.ContextGroup) []components.ContextGroup {
	for i := range groups {
		groups[i].Open = m.contextOpen[groups[i].Label]
	}
	return groups
}

// toolDefGroup itemises the tool definitions by tool. scaled is the category
// total the accounting reports, which is the estimate rescaled when the
// provider reported a size; the rows are scaled with it so they sum to it.
func (m Model) toolDefGroup(scaled int64) (components.ContextGroup, bool) {
	if len(m.toolDefs) == 0 {
		return components.ContextGroup{}, false
	}
	rows := make([]contextRow, 0, len(m.toolDefs))
	for _, d := range m.toolDefs {
		rows = append(rows, contextRow{name: d.Name, tokens: d.Tokens})
	}
	return contextGroupFrom("tool definitions", "tool", rows, m.toolDefTokens, scaled), true
}

// toolResultGroup itemises what tool output is occupying the window, by the
// tool that produced it. A result carries only the id of the call it answers,
// so the names come from the assistant messages that made the calls; output
// from a call that is no longer in the conversation is counted under the one
// name the surface can honestly give it.
func (m Model) toolResultGroup(scaled int64) (components.ContextGroup, bool) {
	byTool := map[string]int64{}
	names := map[string]string{}
	var raw int64
	for _, msg := range m.agent.Messages() {
		for _, tc := range msg.ToolCalls {
			names[tc.ID] = tc.Name
		}
		if msg.Role != provider.RoleTool {
			continue
		}
		tokens := agent.EstimateTokens(msg.Content)
		name := names[msg.ToolCallID]
		if name == "" {
			name = "(call since compacted)"
		}
		byTool[name] += tokens
		raw += tokens
	}
	if len(byTool) == 0 {
		return components.ContextGroup{}, false
	}
	rows := make([]contextRow, 0, len(byTool))
	for name, tokens := range byTool {
		rows = append(rows, contextRow{name: name, tokens: tokens})
	}
	return contextGroupFrom("tool results", "tool", rows, raw, scaled), true
}

// messageTurnGroup itemises the conversation by turn, because "the messages
// are most of the window" is a fact you can only act on once you know which
// exchange it was. A turn runs from a user message to the next one, which is
// the same boundary compaction keeps whole messages at.
//
// Its label is the opening of the message that started the turn. Three of
// those messages are not lines anybody typed — the compaction summary, a
// command's output, the continue prompt — and quoting one back as if it were
// a question the reader asked would be a lie about their own session, so each
// is named for what it is instead.
func (m Model) messageTurnGroup(scaled int64) (components.ContextGroup, bool) {
	var rows []contextRow
	add := func(label string, tokens int64) {
		if label == "" {
			return
		}
		rows = append(rows, contextRow{name: label, tokens: tokens})
	}
	label, tokens := "", int64(0)
	for i, msg := range m.agent.Messages() {
		if i == 0 && msg.Role == provider.RoleSystem {
			continue
		}
		if msg.Role == provider.RoleTool {
			continue
		}
		if msg.Role == provider.RoleUser {
			add(label, tokens)
			label, tokens = turnLabel(msg.Content), 0
		} else if label == "" {
			// An assistant message before any user message: a resumed
			// session opening mid-turn. It is a turn whose opening is gone.
			label = "(the turn this session resumed into)"
		}
		tokens += agent.EstimateTokens(msg.Content)
		for _, tc := range msg.ToolCalls {
			tokens += agent.EstimateTokens(tc.Arguments)
		}
	}
	add(label, tokens)
	if len(rows) == 0 {
		return components.ContextGroup{}, false
	}
	var raw int64
	for _, r := range rows {
		raw += r.tokens
	}
	return contextGroupFrom("messages", "turn", rows, raw, scaled), true
}

// turnLabel is the opening of the message a turn started with, on one line.
// A message the session wrote on the reader's behalf is named rather than
// quoted (typedByHand, recall.go).
func turnLabel(text string) string {
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return "(an empty message)"
	case !typedByHand(text):
		return synthesisedTurnLabel(text)
	}
	line := text
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	if r := []rune(line); len(r) > turnLabelRunes {
		return strings.TrimSpace(string(r[:turnLabelRunes])) + "…"
	}
	return line
}

// synthesisedTurnLabel names one of the three user-role messages nobody
// typed. The openings are the constants the code that writes them declares,
// so a reworded message cannot quietly start being quoted as if it were the
// reader's.
func synthesisedTurnLabel(text string) string {
	switch {
	case strings.HasPrefix(text, compactContextPrefix):
		return "the compaction summary"
	case strings.HasPrefix(text, commandContextPrefix):
		return "a command's output"
	default:
		return "carrying on from the round limit"
	}
}

// contextRow is one itemised part before it is formatted.
type contextRow struct {
	name   string
	tokens int64
}

// contextGroupFrom orders the rows, scales them onto the category total the
// legend states, and formats the ones worth naming. Ties break on the name so
// two runs of the same session list the parts in the same order — a list that
// reshuffles between openings reads as a measurement that changed.
func contextGroupFrom(label, unit string, rows []contextRow, raw, scaled int64) components.ContextGroup {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].tokens != rows[j].tokens {
			return rows[i].tokens > rows[j].tokens
		}
		return rows[i].name < rows[j].name
	})
	group := components.ContextGroup{
		Label:   label,
		Summary: fmt.Sprintf("%s · %s", plural(len(rows), unit), formatTokenCount(scaled)),
	}
	shown := min(len(rows), contextItemsShown)
	var named int64
	for _, r := range rows[:shown] {
		tokens := scaleTokens(r.tokens, raw, scaled)
		named += tokens
		group.Items = append(group.Items, components.ContextItem{
			Label:  r.name,
			Share:  percentOf(tokens, scaled),
			Tokens: formatTokenCount(tokens),
			Pct:    formatSharePct(tokens, scaled),
		})
	}
	if rest := len(rows) - shown; rest > 0 {
		group.More = fmt.Sprintf("↓ %s · %s together",
			plural(rest, "more"), formatTokenCount(max(scaled-named, 0)))
	}
	return group
}

// scaleTokens puts an estimated part onto the total its category was scaled
// to. An unscaled category (nothing reported yet) passes through unchanged.
func scaleTokens(part, raw, scaled int64) int64 {
	if raw <= 0 || scaled <= 0 || raw == scaled {
		return part
	}
	return part * scaled / raw
}

// shareOf is a share as an exact percentage, which is what the grid measures
// its runs with. The legend states the same number rounded; a run measured
// from the rounded figure would drift a cell for every category above it.
func shareOf(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) * 100 / float64(whole)
}

// percentOf is a share as whole percent, clamped to the range a meter draws.
func percentOf(part, whole int64) int {
	if whole <= 0 {
		return 0
	}
	return min(max(int(part*100/whole), 0), 100)
}

// formatSharePct is a share as the legend states it. One decimal, because
// the categories that matter for a large window are all under one percent of
// it and a column of `0%` would say none of them cost anything.
func formatSharePct(part, whole int64) string {
	if whole <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", float64(part)*100/float64(whole))
}

// formatWindowSize is a context window in the words a model is sold in — a
// window is a round number somebody chose, so `1m` is what it is called and
// `1000.0k` is what arithmetic makes of it.
func formatWindowSize(n int64) string {
	switch {
	case n >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1_000_000), ".0") + "m"
	case n >= 1000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1000), ".0") + "k"
	default:
		return fmt.Sprintf("%d", n)
	}
}

// contextKeyHint is what the status bar offers while the surface is up.
func contextKeyHint() string {
	return keys.Bracket(keys.Context.Back) + " " + keys.Words(keys.Context.Back)
}
