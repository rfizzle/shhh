package chat

// The Tools & permissions selector on the profile drafter
// (docs/capabilities/subagents.md#a-profile-is-drafted-in-conversation):
// enter on the Tools block opens a multi-select of the tiers and of the tools
// this session registered, so a profile's grants are chosen from what exists
// rather than read and corrected off a line. The loader's rules are asked on
// every keystroke through config.CheckGrant — the one function the loader's
// own Validate asks — so a pick the file would be refused for is refused on
// the card, before anything is saved.

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/persona"
	"github.com/rfizzle/shhh/internal/receipt"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// personaPick is the open selector's reading of its own rows: which tier or
// tool each one stands for, so the boxes can be read back as a grant.
type personaPick struct {
	rows []personaPickRow
	// unoffered is the tools the draft named that this session did not
	// register. The selector lists what exists, so a pick taken from it
	// cannot keep them, and the card says they went.
	unoffered []string
	// kept is the draft's own list where the session offered no tool at
	// all, carried through the pick as it was.
	kept []string
	// tiers is the tiers ticked when the boxes were last read, so a tier
	// ticked or unticked since takes its tools with it.
	tiers []string
}

// personaPickRow is one row: a tier, a tool with the tier it sits in, or
// neither for a row that only labels. desc is what the row says when nothing
// refuses it.
type personaPickRow struct {
	tier, tool, desc string
	// writer marks the note that says what write or execute make of the
	// agent; it is worded again on every tick.
	writer bool
}

// personaTierOrder is the tiers in the order the selector offers them.
var personaTierOrder = []string{config.PermissionRead, config.PermissionWrite, config.PermissionExecute, config.PermissionWeb}

// personaTierWords is what each tier lets the agent do, in plain words.
var personaTierWords = map[string]string{
	config.PermissionRead:    "files, searches, listings, the notebook — every agent has this",
	config.PermissionWrite:   "edit files · it works in its own copy of the tree and hands back a patch",
	config.PermissionExecute: "run commands · in the same copy",
	config.PermissionWeb:     "fetch pages and search",
}

// personaAlwaysOn is the row naming what every child gets whatever its
// profile says. It is listed so the selector reads as the whole of the grant,
// and it has no box because none of it is on offer
// (docs/capabilities/subagents.md#a-profile-is-a-file).
const personaAlwaysOn = "navigation · notebook · skills — every child has these"

// pickPersonaSection is enter on the Tools block: it opens the selector with
// the draft's own grant ticked. Every other field block offers no enter.
func (m Model) pickPersonaSection(index int) (tea.Model, tea.Cmd) {
	f := m.persona
	if f.draft == nil || index < 0 || index >= len(personaBlocks) || personaBlocks[index] != personaToolsBlock {
		m.syncViewport()
		return m, nil
	}
	pick, picker := m.personaPicker(*f.draft)
	f.pick = pick
	m.personaScreen.OpenPicker(picker)
	f.refreshPick(picker, f.draft.Name)
	m.syncViewport()
	return m, nil
}

// personaPicker builds the selector over a draft: the tiers, then the tools
// the session registered grouped by tier, then what every child always has.
// A chat profile is offered no tier that writes and no tool that needs one.
func (m Model) personaPicker(d persona.Draft) (*personaPick, *components.MultiSelect) {
	chat := m.wiring.Personas.Kind == persona.KindChat
	def := d.Definition()
	pick := &personaPick{}
	var opts []components.SelectOption
	var checked, fixed []bool
	add := func(row personaPickRow, opt components.SelectOption, on, stays bool) {
		pick.rows = append(pick.rows, row)
		opts = append(opts, opt)
		checked = append(checked, on)
		fixed = append(fixed, stays)
	}
	header := func(label, desc string, row personaPickRow) {
		add(row, components.SelectOption{Label: label, Desc: desc, Header: true}, false, false)
	}
	header("tiers", "", personaPickRow{})
	for _, tier := range personaTierOrder {
		if chat && writingTier(tier) {
			continue
		}
		words := personaTierWords[tier]
		add(personaPickRow{tier: tier, desc: words},
			components.SelectOption{Label: tier, Desc: words},
			def.Has(tier), tier == config.PermissionRead)
	}
	if chat {
		header("", "write, execute — not on offer: a chat profile never acts", personaPickRow{})
	} else {
		header("", "", personaPickRow{writer: true})
	}
	var tools []string
	for _, tier := range personaTierOrder {
		for _, name := range config.KnownAgentTools() {
			if t, _ := config.ToolTier(name); t == tier && m.hasTool(name) && (!chat || !writingTier(tier)) {
				tools = append(tools, name)
			}
		}
	}
	if len(tools) > 0 {
		header("tools this session registered, by tier — it gets only what is ticked", "", personaPickRow{})
	}
	for _, name := range tools {
		tier, _ := config.ToolTier(name)
		// An empty allowlist is every tool the loader would give the tiers
		// granted — the gate never beside write or execute — so that is what
		// is ticked for it: the boxes are the grant as the loader reads it.
		on := slices.Contains(d.Tools, name)
		if len(d.Tools) == 0 {
			on = config.CheckGrant(d.Permissions, []string{name}) == nil
		}
		// What each tool does is the clause it declared beside its
		// definition; a tool that declared none is still offered, under its
		// tier and without one.
		does := receipt.Does(name)
		add(personaPickRow{tool: name, tier: tier, desc: does},
			components.SelectOption{Label: name, Value: tier, Desc: does}, on, false)
	}
	for _, name := range d.Tools {
		switch {
		case len(tools) == 0:
			// Nothing to pick from is no reason to change the list: an
			// emptied one would be read as every tool the tiers grant.
			pick.kept = append(pick.kept, name)
		case !slices.Contains(tools, name):
			pick.unoffered = append(pick.unoffered, name)
		}
	}
	header("always", personaAlwaysOn, personaPickRow{})

	pick.tiers, _ = pick.grant(checked)
	ms := components.NewMultiSelect("What may "+d.Name+" do?", opts)
	ms.Checked, ms.Fixed = checked, fixed
	ms.Columns, ms.AllowNone = true, true
	ms.Tone = components.CardDecision
	ms.TakeVerb, ms.CancelLabel = "select", "cancel"
	for i, opt := range opts {
		if !opt.Header {
			ms.Focus = i
			break
		}
	}
	return pick, ms
}

// writingTier reports a tier that makes the agent a writer.
func writingTier(tier string) bool {
	return tier == config.PermissionWrite || tier == config.PermissionExecute
}

// grant is the boxes as the loader would read them: the tiers ticked, in the
// order the drafter's Normalise keeps them, and the tools ticked, in the
// selector's order.
func (p *personaPick) grant(checked []bool) (tiers, tools []string) {
	for _, tier := range []string{config.PermissionWeb, config.PermissionWrite, config.PermissionExecute} {
		for i, row := range p.rows {
			if row.tool == "" && row.tier == tier && checked[i] {
				tiers = append(tiers, tier)
			}
		}
	}
	for i, row := range p.rows {
		if row.tool != "" && checked[i] {
			tools = append(tools, row.tool)
		}
	}
	return tiers, append(tools, p.kept...)
}

// offered is how many tool rows the selector has.
func (p *personaPick) offered() int {
	n := 0
	for _, row := range p.rows {
		if row.tool != "" {
			n++
		}
	}
	return n
}

// refreshPick asks the loader's rules of the boxes as they stand and puts the
// answers on the card: a tool the rules refuse beside the tiers ticked —
// whatever else were ticked — cannot be ticked and says why, the note says
// what write or execute make of the agent, and a pick the loader would refuse
// is refused with its sentence until it changes.
//
// A tier ticked or unticked since the last reading takes its tools with it:
// a tier is every tool it grants until a tool is unticked, and a tool left
// ticked where the new tiers cannot grant it — its own tier unticked, or the
// gate once write or execute is ticked — would be a refusal the reader did
// not make. Such a tool is unticked, and its row then says why.
func (f *personaFlow) refreshPick(ms *components.MultiSelect, name string) {
	p := f.pick
	tiers, _ := p.grant(ms.Checked)
	changed := !slices.Equal(tiers, p.tiers)
	for i, row := range p.rows {
		if row.tool == "" {
			continue
		}
		grantable := config.CheckGrant(tiers, []string{row.tool}) == nil
		now, was := slices.Contains(tiers, row.tier), slices.Contains(p.tiers, row.tier)
		switch {
		case row.tier != config.PermissionRead && now && !was:
			ms.Checked[i] = grantable
		case changed && ms.Checked[i] && !grantable:
			ms.Checked[i] = false
		}
	}
	p.tiers = tiers
	tiers, tools := p.grant(ms.Checked)
	writes := slices.ContainsFunc(tiers, writingTier)
	for i, row := range p.rows {
		opt := &ms.Options[i]
		switch {
		case row.writer:
			opt.Desc = "▸ with read and web only, " + name + " changes nothing and reads your tree as it stands"
			if writes {
				opt.Desc = "▸ with write or execute, " + name + " is a writer: it works in its own copy, its patch comes to a card"
			}
		case row.tool != "":
			// Asked with the tool's own tier granted, so a tool whose tier
			// is merely unticked stays on offer — ticking the tier is the
			// fix — and only a refusal no tier can answer takes the box.
			err := config.CheckGrant(append(slices.Clone(tiers), row.tier), []string{row.tool})
			opt.Dim = err != nil && !ms.Checked[i]
			opt.Desc = row.desc
			if opt.Dim {
				opt.Desc = strings.TrimPrefix(err.Error(), "tools: ")
			}
		}
	}
	ms.Warning = ""
	if err := config.CheckGrant(tiers, tools); err != nil {
		ms.Warning = err.Error()
	} else if len(tools) == 0 && p.offered() > 0 {
		// The loader reads an empty list as every tool the tiers grant, so
		// an empty pick would widen the grant it looks like it narrows.
		ms.Warning = "tick at least one tool: a profile that lists none is given every tool its tiers allow"
	}
}

// takePersonaPick writes the selector's answer into the draft's tiers and
// tools and nothing else: the prompt and its sections are not the selector's.
// A pick with every offered tool the loader would grant ticked is written as
// no list at all, which is the loader's word for every tool the tiers grant.
func (m Model) takePersonaPick() (tea.Model, tea.Cmd) {
	f := m.persona
	ms := m.personaScreen.Picker
	if f.draft == nil || f.pick == nil || ms == nil {
		return m.dropPersonaPick()
	}
	tiers, tools := f.pick.grant(ms.Checked)
	if config.CheckGrant(tiers, tools) != nil {
		// The card refuses enter while its warning stands; this is the same
		// rule held at the one place the draft is written.
		m.syncViewport()
		return m, nil
	}
	whole := len(f.pick.kept) == 0
	for i, row := range f.pick.rows {
		if row.tool != "" && !ms.Checked[i] && config.CheckGrant(tiers, []string{row.tool}) == nil {
			whole = false
		}
	}
	if whole {
		tools = nil
	}
	f.draft.Permissions, f.draft.Tools, f.draft.Dropped = tiers, tools, nil
	var warning string
	if len(f.pick.unoffered) > 0 {
		warning = "Taken off the tools: " + strings.Join(f.pick.unoffered, ", ") + " — this session has not registered them."
	}
	f.pick = nil
	m.openPersonaCard()
	m.personaScreen.Warn(warning)
	m.syncViewport()
	return m, nil
}

// dropPersonaPick closes the selector and leaves the draft as it was.
func (m Model) dropPersonaPick() (tea.Model, tea.Cmd) {
	m.persona.pick = nil
	m.openPersonaCard()
	m.syncViewport()
	return m, nil
}
