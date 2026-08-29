package components

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The config screen's own rules (S-127,
// docs/interface/surfaces.md#the-supporting-screens). What it borrows from
// the selector is covered where the selector is covered; what is tested here
// is what this screen decides: that a picker opens under the row rather than
// over the screen, that nothing reaches the host until it is asked for, and
// that the keys mean what the hint line says they mean.

func configFixture() *ConfigScreen {
	return &ConfigScreen{
		Path:     "~/.config/shhh/config.toml",
		MaxLines: 24,
		Rows: []ConfigRow{
			{Group: "SESSION", Key: "behavior.default_mode", Label: "permission mode",
				Value: "⏵⏵ auto", ValueTone: ToneSafe, Detail: "edits apply", Source: "user",
				Options: []SelectOption{{Label: "manual"}, {Label: "auto"}, {Label: "plan"}}},
			{Group: "SESSION", Key: "behavior.max_tool_rounds", Label: "round limit",
				Value: "25", Source: "default"},
			{Group: "MODEL", Key: "provider.api_key", Label: "api key",
				Value: "···4f9c", Source: "user", Secret: true},
			{Group: "MODEL", Key: "provider.model", Label: "model",
				Value: "gpt-5.2", Source: "user",
				Options: []SelectOption{{Label: "gpt-5.2"}, {Label: "claude-sonnet-4.6"}}},
		},
	}
}

func typeInto(c *ConfigScreen, text string) {
	for _, r := range text {
		c.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// §19a: the picker opens beneath the row being changed, indented one level,
// so the setting stays visible above its own options. A modal over the screen
// would hide the thing the reader is deciding about.
func TestConfigScreen_PickerOpensUnderTheRow(t *testing.T) {
	c := configFixture()
	c.Update(key("enter"))
	lines := strings.Split(c.View(110), "\n")

	row, option := -1, -1
	for i, line := range lines {
		if strings.Contains(line, "permission mode") {
			row = i
		}
		if strings.Contains(line, "manual") {
			option = i
		}
	}
	if row < 0 || option < 0 {
		t.Fatalf("the row and its options are both on screen:\n%s", c.View(110))
	}
	if option <= row {
		t.Fatalf("the picker opens under the row it changes, not above it:\n%s", c.View(110))
	}
	indent := func(i int) int { return len(lines[i]) - len(strings.TrimLeft(lines[i], " ")) }
	if indent(option) <= indent(row) {
		t.Fatalf("the picker is indented one level in from the row:\n%s", c.View(110))
	}
}

// esc on an open picker keeps the current value and leaves the screen up. It
// is the one key §19a guarantees changes nothing.
func TestConfigScreen_EscKeepsTheCurrentValue(t *testing.T) {
	c := configFixture()
	c.Update(key("enter"))
	c.Update(key("down"))
	done, result := c.Update(key("esc"))
	if done || result != nil {
		t.Fatalf("esc on a picker closes the picker, not the screen: done=%v result=%#v", done, result)
	}
	if strings.Contains(c.View(110), "manual") {
		t.Fatal("the picker is gone after esc")
	}
}

// Taking an option resolves the edit to the host, which owns what a value
// means. The screen never writes a config of its own.
func TestConfigScreen_TakingAnOptionResolvesTheChange(t *testing.T) {
	c := configFixture()
	c.Update(key("enter"))
	c.Update(key("down"))
	_, result := c.Update(key("enter"))
	change, ok := result.(ConfigChange)
	if !ok {
		t.Fatalf("enter resolves a ConfigChange, got %#v", result)
	}
	if change.Key != "behavior.default_mode" || change.Value != "auto" {
		t.Fatalf("the change names the key and the chosen option: %#v", change)
	}
}

// A setting with no answers opens a field in the filter row's own grammar,
// and enter resolves what was typed.
func TestConfigScreen_FieldEditsResolveWhatWasTyped(t *testing.T) {
	c := configFixture()
	c.Focus = 1
	c.Update(key("enter"))
	c.Update(key("ctrl+u"))
	typeInto(c, "40")
	if view := c.View(110); !strings.Contains(view, "▸ 40") {
		t.Fatalf("the field echoes what is typed in the query row's grammar:\n%s", view)
	}
	_, result := c.Update(key("enter"))
	change, ok := result.(ConfigChange)
	if !ok || change.Key != "behavior.max_tool_rounds" || change.Value != "40" {
		t.Fatalf("enter resolves the typed value: %#v", result)
	}
}

// A secret is never echoed — not while it is being typed and not on the row
// it came from (§19a: the last four characters and nothing else).
func TestConfigScreen_SecretIsNeverEchoed(t *testing.T) {
	c := configFixture()
	c.Focus = 2
	c.Update(key("enter"))
	typeInto(c, "sk-live-secret")
	view := c.View(110)
	if strings.Contains(view, "sk-live-secret") {
		t.Fatalf("the key is never rendered:\n%s", view)
	}
	if !strings.Contains(view, "••••") {
		t.Fatalf("the entry masks what was typed:\n%s", view)
	}
	_, result := c.Update(key("enter"))
	change, ok := result.(ConfigChange)
	if !ok || change.Value != "sk-live-secret" {
		t.Fatalf("the key still reaches the host: %#v", result)
	}
}

func TestConfigScreen_MaskSecretIsTheLastFour(t *testing.T) {
	if got := MaskSecret("sk-live-0000-4f9c"); got != "···4f9c" {
		t.Fatalf("MaskSecret = %q", got)
	}
	if got := MaskSecret("abc"); strings.Contains(got, "a") {
		t.Fatalf("a key too short to mask is all dots, got %q", got)
	}
	if got := MaskSecret(""); got != "" {
		t.Fatalf("nothing set masks to nothing, got %q", got)
	}
}

// Nothing is written until [w], and [w] is not offered while there is
// nothing to write — a key that cannot act is not offered (invariant 5).
func TestConfigScreen_WriteIsOfferedOnlyWhenSomethingIsStaged(t *testing.T) {
	c := configFixture()
	if strings.Contains(c.View(110), "[w]") {
		t.Fatalf("a clean screen offers no write:\n%s", c.View(110))
	}
	done, _ := c.Update(key("w"))
	if done {
		t.Fatal("w does nothing while nothing is staged")
	}

	c.Changed = 2
	view := c.View(110)
	if !strings.Contains(view, "[w]") {
		t.Fatalf("a staged change offers the write:\n%s", view)
	}
	if !strings.Contains(view, "2 changes unwritten") {
		t.Fatalf("the header counts what is standing against the file:\n%s", view)
	}
}

// [w] asks before it writes, in the shared inline confirm, and the confirm
// defaults to no.
func TestConfigScreen_WriteConfirms(t *testing.T) {
	c := configFixture()
	c.Changed = 1
	c.Update(key("w"))
	view := c.View(110)
	if !strings.Contains(view, "[y/N]") || !strings.Contains(view, "~/.config/shhh/config.toml") {
		t.Fatalf("the write-back asks in the inline confirm:\n%s", view)
	}
	done, result := c.Update(key("n"))
	if done {
		t.Fatalf("declining the confirm keeps the screen up: %#v", result)
	}

	c.Update(key("w"))
	done, result = c.Update(key("y"))
	out, ok := result.(ConfigResult)
	if !done || !ok || !out.Write {
		t.Fatalf("y writes: done=%v result=%#v", done, result)
	}
}

// esc and q both leave without writing. §19a: esc discards the lot, and on
// this screen "changes nothing" is literal because nothing has reached the
// file yet.
func TestConfigScreen_LeavingWritesNothing(t *testing.T) {
	for _, k := range []string{"esc", "q"} {
		c := configFixture()
		c.Changed = 3
		done, result := c.Update(key(k))
		out, ok := result.(ConfigResult)
		if !done || !ok || out.Write || !out.Canceled {
			t.Fatalf("%s leaves writing nothing: done=%v result=%#v", k, done, result)
		}
	}
}

// The settings list is the §4a card, so it filters like one: / opens the row,
// the row carries both counts, and the letters that are keys elsewhere are
// text while it is open.
func TestConfigScreen_SettingsFilter(t *testing.T) {
	c := configFixture()
	c.Update(key("/"))
	typeInto(c, "mo")
	view := stripANSI(c.View(110))
	if !strings.Contains(view, "▸ mo") || !strings.Contains(view, "of 4 match") {
		t.Fatalf("the query row states what it typed and both counts:\n%s", view)
	}
	if strings.Contains(view, "round limit") {
		t.Fatalf("the filter hid the rows it did not match:\n%s", view)
	}
	if !strings.Contains(view, "permission mode") || !strings.Contains(view, "model") {
		t.Fatalf("the filter keeps the rows it matched:\n%s", view)
	}

	// w is a letter here, not the write key.
	c.Changed = 1
	done, _ := c.Update(key("w"))
	if done {
		t.Fatal("with the query line open, w types rather than writing")
	}
	if !strings.Contains(c.View(110), "▸ mow") {
		t.Fatalf("w reached the query line:\n%s", c.View(110))
	}
}

// A row's source is its own field, right-aligned, and a setting the host
// cannot honour says so there rather than being dropped (invariant 4).
func TestConfigScreen_SourceStatesWhereTheValueCameFrom(t *testing.T) {
	c := configFixture()
	c.Rows = append(c.Rows, ConfigRow{
		Group: "WORKSPACE", Key: "sandbox.profile", Label: "sandbox",
		Value: "⛨ workspace", Source: "unavailable on this host", SourceTone: ToneRisk,
	})
	view := c.View(110)
	for _, want := range []string{"user", "default", "unavailable on this host"} {
		if !strings.Contains(view, want) {
			t.Fatalf("every row states where its value came from — missing %q:\n%s", want, view)
		}
	}
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "sandbox") && !strings.HasSuffix(line, "unavailable on this host") {
			t.Fatalf("the source is right-aligned at the end of the row: %q", line)
		}
	}
}

// [r] resets one row rather than the screen, and says so.
func TestConfigScreen_ResetIsOneRow(t *testing.T) {
	c := configFixture()
	_, result := c.Update(key("r"))
	change, ok := result.(ConfigChange)
	if !ok || !change.Reset || change.Key != "behavior.default_mode" {
		t.Fatalf("r resets the row under the pointer: %#v", result)
	}
}

// The pointer steps over the group rails, which are labels rather than
// options, and stops at either end rather than wrapping.
func TestConfigScreen_PointerStepsOverRails(t *testing.T) {
	c := configFixture()
	for i := 0; i < 10; i++ {
		c.Update(key("down"))
	}
	if c.Focus != len(c.Rows)-1 {
		t.Fatalf("the pointer stops at the last setting, got %d", c.Focus)
	}
	for i := 0; i < 10; i++ {
		c.Update(key("up"))
	}
	if c.Focus != 0 {
		t.Fatalf("the pointer stops at the first setting, got %d", c.Focus)
	}
}

// The screen is a takeover: full width, no frame, one header and one hint
// line.
func TestConfigScreen_IsATakeoverNotACard(t *testing.T) {
	view := stripANSI(configFixture().View(110))
	if strings.Contains(view, "┌─") || strings.Contains(view, "└─") {
		t.Fatalf("a takeover surface draws no card frame:\n%s", view)
	}
	lines := strings.Split(view, "\n")
	if !strings.HasPrefix(lines[0], "shhh config") {
		t.Fatalf("the header names the command and its subject: %q", lines[0])
	}
	if !strings.Contains(lines[0], "[?] keys · [q] quit") {
		t.Fatalf("the header carries the two keys every one of these screens has: %q", lines[0])
	}
}
