package chat

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/persona"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// pickerTools is a session's registered toolset as the host hands it over:
// the tools a profile may name, the navigation and notebook tools every child
// gets, and a tool nothing may name.
var pickerTools = []ToolTokens{
	{Name: "read_file"}, {Name: "search"}, {Name: "glob"}, {Name: "quality_gate"},
	{Name: "write_file"}, {Name: "edit_file"}, {Name: "execute_command"}, {Name: "web_fetch"},
	{Name: "git"}, {Name: "definition"}, {Name: "notebook_read"}, {Name: "process"},
}

// openPicker opens the selector on the Tools block of a sectioned draft.
func openPicker(t *testing.T, kind persona.Kind) Model {
	t.Helper()
	m, _, _ := sectionedModel(t, kind)
	m = m.WithToolDefinitions(pickerTools)
	for range 5 {
		m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.personaScreen.Picker == nil {
		t.Fatalf("enter on the Tools block should open the selector:\n%s", personaView(m))
	}
	return m
}

// pickerRow finds a row by its label.
func pickerRow(t *testing.T, m Model, label string) int {
	t.Helper()
	for i, o := range m.personaScreen.Picker.Options {
		if o.Label == label && !o.Header {
			return i
		}
	}
	t.Fatalf("no row %q on the selector", label)
	return -1
}

// togglePickerRow moves the pointer onto a row and presses space on it.
func togglePickerRow(t *testing.T, m Model, label string) Model {
	t.Helper()
	m.personaScreen.Picker.Focus = pickerRow(t, m, label)
	return pressOn(t, m, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
}

// The selector lists the four tiers and then the tools this session
// registered that a profile may name, under their tiers; what every child
// always has is one fixed row and never a box.
func TestPersona_TheToolsSelectorOffersWhatTheSessionRegistered(t *testing.T) {
	m := openPicker(t, persona.KindCode)
	ms := m.personaScreen.Picker
	var boxes []string
	for _, o := range ms.Options {
		if !o.Header {
			boxes = append(boxes, o.Label)
		}
	}
	want := []string{"read", "write", "execute", "web",
		"glob", "quality_gate", "read_file", "search", "edit_file", "write_file", "execute_command", "web_fetch"}
	if strings.Join(boxes, ",") != strings.Join(want, ",") {
		t.Fatalf("rows = %v, want %v", boxes, want)
	}
	read := pickerRow(t, m, "read")
	if !ms.Checked[read] || !ms.Fixed[read] {
		t.Fatalf("read should be ticked and fixed")
	}
	view := personaView(m)
	for _, s := range []string{"What may test-writer do?", "navigation · notebook · skills — every child has these",
		"is a writer: it works in its own copy", "[space] toggle"} {
		if !strings.Contains(view, s) {
			t.Fatalf("the selector should say %q:\n%s", s, view)
		}
	}
	// The draft grants write and execute with no list, so every tool the
	// loader would give it is ticked — and the gate, which it would not, is
	// on the card as refused, with the loader's reason.
	gate := pickerRow(t, m, "quality_gate")
	if ms.Checked[gate] || !ms.Options[gate].Dim || !strings.Contains(ms.Options[gate].Desc, "changes nothing") {
		t.Fatalf("the gate beside write should be refused with the reason: %+v", ms.Options[gate])
	}
	for _, name := range []string{"read_file", "write_file", "execute_command"} {
		if !ms.Checked[pickerRow(t, m, name)] {
			t.Fatalf("%s should arrive ticked", name)
		}
	}
	if ms.Checked[pickerRow(t, m, "web_fetch")] {
		t.Fatalf("web_fetch should not be ticked: the draft does not grant web")
	}
}

// The loader's rules hold on the selector before anything is saved: the gate
// cannot be ticked beside write or execute, a gate ticked before either was
// is refused with the loader's sentence, and enter does not take a refused
// pick.
func TestPersona_TheSelectorHoldsTheLoadersRules(t *testing.T) {
	m := openPicker(t, persona.KindCode)
	m = togglePickerRow(t, m, "quality_gate")
	if m.personaScreen.Picker.Checked[pickerRow(t, m, "quality_gate")] {
		t.Fatalf("the gate was ticked beside write")
	}
	if view := personaView(m); !strings.Contains(view, "quality_gate cannot be taken here") {
		t.Fatalf("space on the gate should say why:\n%s", view)
	}
	m = togglePickerRow(t, m, "write")
	m = togglePickerRow(t, m, "execute")
	if view := personaView(m); !strings.Contains(view, "changes nothing and reads your tree") {
		t.Fatalf("with write and execute off the note should say so:\n%s", view)
	}
	// A gate ticked on a profile that changes nothing goes when write is
	// ticked, and its row says why: one tick never lands on a refusal.
	m = togglePickerRow(t, m, "quality_gate")
	m = togglePickerRow(t, m, "write")
	ms := m.personaScreen.Picker
	gate := pickerRow(t, m, "quality_gate")
	if ms.Checked[gate] || !ms.Options[gate].Dim || ms.Warning != "" {
		t.Fatalf("ticking write should take the gate off with its reason: %+v, warning %q", ms.Options[gate], ms.Warning)
	}
	// A tool ticked without its tier is refused with the loader's own
	// sentence, and enter does not take the pick.
	m = togglePickerRow(t, m, "web_fetch")
	if w := m.personaScreen.Picker.Warning; !strings.Contains(w, `"web_fetch" needs the "web" permission`) {
		t.Fatalf("a tool without its tier should be refused, warning = %q", w)
	}
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.personaScreen.Picker == nil || strings.Join(m.persona.draft.Permissions, ",") != "write,execute" {
		t.Fatalf("a refused pick was taken: %+v", m.persona.draft.Permissions)
	}
}

// A session that registered no tool a profile may name has nothing to pick
// from, and the draft's own list is kept rather than emptied into "every
// tool".
func TestPersona_NoToolsToPickKeepsTheDraftsList(t *testing.T) {
	m, _, _ := sectionedModel(t, persona.KindCode)
	m.persona.draft.Tools = []string{"search"}
	for range 5 {
		m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := strings.Join(m.persona.draft.Tools, ","); got != "search" {
		t.Fatalf("tools = %q, want the draft's own list", got)
	}
}

// The answer reaches the draft's tiers and tools and nothing else: the prompt
// and its sections are not the selector's to change.
func TestPersona_ThePickIsTheDraftsTiersAndTools(t *testing.T) {
	m := openPicker(t, persona.KindCode)
	prompt := m.persona.draft.Prompt
	m = togglePickerRow(t, m, "execute")
	m = togglePickerRow(t, m, "glob")
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	d := m.persona.draft
	if m.personaScreen.Picker != nil || m.persona.pick != nil {
		t.Fatalf("the selector should close on enter")
	}
	if strings.Join(d.Permissions, ",") != "write" {
		t.Fatalf("permissions = %v", d.Permissions)
	}
	if want := "read_file,search,edit_file,write_file"; strings.Join(d.Tools, ",") != want {
		t.Fatalf("tools = %v, want %s", d.Tools, want)
	}
	if d.Prompt != prompt {
		t.Fatalf("the pick changed the prompt")
	}
	if err := d.Definition().Validate(); err != nil {
		t.Fatalf("the pick does not load: %v", err)
	}
	if view := personaView(m); !strings.Contains(view, "read + write") {
		t.Fatalf("the Tools block should state the pick:\n%s", view)
	}
	// A tier ticked again brings its tools with it, and every tool ticked is
	// no list at all, the loader's word for everything the tiers grant; and
	// esc leaves the draft as it was.
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = togglePickerRow(t, m, "glob")
	m = togglePickerRow(t, m, "execute")
	if !m.personaScreen.Picker.Checked[pickerRow(t, m, "execute_command")] {
		t.Fatalf("ticking execute should tick the tool it grants")
	}
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.persona.draft.Tools != nil {
		t.Fatalf("a whole pick should be no list, tools = %v", m.persona.draft.Tools)
	}
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = togglePickerRow(t, m, "web")
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.personaScreen.Picker != nil || slices.Contains(m.persona.draft.Permissions, "web") {
		t.Fatalf("esc should close the selector and change nothing: %v", m.persona.draft.Permissions)
	}
}

// An empty list would be read as every tool, so a pick that unticks them all
// is refused rather than widening what it looks like it narrows.
func TestPersona_AnEmptyToolListIsRefused(t *testing.T) {
	m := openPicker(t, persona.KindCode)
	for _, name := range []string{"glob", "read_file", "search", "edit_file", "write_file", "execute_command"} {
		m = togglePickerRow(t, m, name)
	}
	if w := m.personaScreen.Picker.Warning; !strings.Contains(w, "tick at least one tool") {
		t.Fatalf("an empty pick should be refused, warning = %q", w)
	}
}

// A chat profile is offered no tier that writes and no tool that needs one.
func TestPersona_AChatProfileHasNoWritingTierToPick(t *testing.T) {
	m := openPicker(t, persona.KindChat)
	for _, o := range m.personaScreen.Picker.Options {
		if o.Header {
			continue
		}
		switch o.Label {
		case "write", "execute", "write_file", "edit_file", "execute_command":
			t.Fatalf("a chat profile was offered %s", o.Label)
		}
	}
	if view := personaView(m); !strings.Contains(view, "write, execute — not on offer: a chat profile never acts") {
		t.Fatalf("the chat selector should say why write is missing:\n%s", view)
	}
	m = togglePickerRow(t, m, "web")
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.persona.draft.Permissions) != 0 || m.persona.draft.Writes() {
		t.Fatalf("permissions = %v", m.persona.draft.Permissions)
	}
	if m.personaScreen.Step != components.ProfileDraft {
		t.Fatalf("the draft should be back on screen")
	}
}
