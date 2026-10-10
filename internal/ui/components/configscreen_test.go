package components

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The config screen's own rules (
// docs/interface/surfaces.md#the-supporting-screens). What it borrows from
// the selector is covered where the selector is covered; what is tested here
// is what this screen decides: that a picker opens under the row rather than
// over the screen, that nothing reaches the host until it is asked for, and
// that the keys mean what the hint line says they mean.

func configFixture() *ConfigScreen {
	return &ConfigScreen{
		Path:     "~/.config/shhh/config.toml",
		maxLines: 24,
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

// The picker opens beneath the row being changed, indented one level,
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
// is the one key the screen guarantees changes nothing.
func TestConfigScreen_EscKeepsTheCurrentValue(t *testing.T) {
	c := configFixture()
	c.Update(key("enter"))
	c.Update(key("down"))
	done, result := c.Update(key("esc"))
	if done || result != (ConfigResult{}) {
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
	change := result.Change
	if change == nil {
		t.Fatalf("enter resolves a change, got %#v", result)
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
	c.Update(key("backspace"))
	c.Update(key("backspace"))
	typeInto(c, "40")
	if view := c.View(110); !strings.Contains(view, "▸ 40") {
		t.Fatalf("the field echoes what is typed in the query row's grammar:\n%s", view)
	}
	_, result := c.Update(key("enter"))
	change := result.Change
	if change == nil || change.Key != "behavior.max_tool_rounds" || change.Value != "40" {
		t.Fatalf("enter resolves the typed value: %#v", result)
	}
}

// A secret is never echoed — not while it is being typed and not on the row
// it came from (the last four characters and nothing else).
func TestConfigScreen_SecretIsNeverEchoed(t *testing.T) {
	c := configFixture()
	c.Focus = 2
	c.Update(key("enter"))
	typeInto(c, "sk-live-secret")
	view := c.View(110)
	if strings.Contains(view, "sk-live-secret") {
		t.Fatalf("the key is never rendered:\n%s", view)
	}
	if !strings.Contains(view, "●●●●") {
		t.Fatalf("the entry masks what was typed:\n%s", view)
	}
	_, result := c.Update(key("enter"))
	change := result.Change
	if change == nil || change.Value != "sk-live-secret" {
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

// Nothing is written until [ctrl+s], and the write is not offered while there
// is nothing to write — a key that cannot act is not offered (invariant 5).
func TestConfigScreen_WriteIsOfferedOnlyWhenSomethingIsStaged(t *testing.T) {
	c := configFixture()
	if strings.Contains(c.View(110), "[ctrl+s]") {
		t.Fatalf("a clean screen offers no write:\n%s", c.View(110))
	}

	c.Changed = 2
	view := ansi.Strip(c.View(110))
	if !strings.Contains(view, "[ctrl+s] write 2 changes") {
		t.Fatalf("a staged change offers the write:\n%s", view)
	}
	if !strings.Contains(view, "2 changes unwritten") {
		t.Fatalf("the header counts what is standing against the file:\n%s", view)
	}
}

// The scope key is offered only where there are two files to write, and
// pressing it hands the switch to the host rather than moving anything
// itself: the screen owns no idea of which file is which.
func TestConfigScreen_TheScopeKeyIsOfferedOnlyInACheckout(t *testing.T) {
	c := configFixture()
	if strings.Contains(c.View(110), "[shift+tab]") {
		t.Fatalf("a screen with one file to write offers a switch:\n%s", c.View(110))
	}
	if _, result := c.Update(key("shift+tab")); result.Scope {
		t.Fatal("shift+tab switched the write on a screen with one file")
	}

	c.Scoped = true
	view := c.View(110)
	if !strings.Contains(view, "the checkout's file") || !strings.Contains(view, "[shift+tab] write yours") {
		t.Fatalf("a screen in a checkout names whose file it writes and offers the other:\n%s", view)
	}
	if _, result := c.Update(key("shift+tab")); !result.Scope {
		t.Fatal("shift+tab did not ask the host to switch the write")
	}
}

// ctrl+s writes at once: no question stands in front of it, the screen stays
// up for the host's receipt, and the old bare letter is only a letter. With
// nothing staged the key says so on the foot row and writes nothing.
func TestSettings_CtrlSWritesAtOnce(t *testing.T) {
	c := configFixture()
	c.Changed = 1
	done, result := c.Update(key("ctrl+s"))
	if done || !result.Write || result.Canceled {
		t.Fatalf("ctrl+s asks the host to write and keeps the screen: done=%v result=%#v", done, result)
	}
	if strings.Contains(ansi.Strip(c.View(110)), "[y/N]") {
		t.Fatalf("a write is not asked about:\n%s", c.View(110))
	}
	if _, result := c.Update(key("w")); result.Write {
		t.Fatal("w no longer writes")
	}

	empty := configFixture()
	done, result = empty.Update(key("ctrl+s"))
	if done || result != (ConfigResult{}) {
		t.Fatalf("nothing staged writes nothing: done=%v result=%#v", done, result)
	}
	if !strings.Contains(ansi.Strip(empty.View(110)), "nothing staged to write") {
		t.Fatalf("the foot row says why nothing happened:\n%s", empty.View(110))
	}
	empty.Update(key("down"))
	if strings.Contains(ansi.Strip(empty.View(110)), "nothing staged to write") {
		t.Fatal("the next key clears the notice")
	}
}

// The hint and the annotation are drawn at every width the golden widths
// cover: the annotation does not drop when it will not fit beside the keys.
func TestSettings_TheHintAndAnnotationAreDrawnAtEveryWidth(t *testing.T) {
	for _, width := range []int{60, 80, 110, 130} {
		c := configFixture()
		c.Changed = 3
		view := ansi.Strip(c.View(width))
		for _, want := range []string{"[ctrl+s] write 3 changes", "nothing is written until [ctrl+s]"} {
			if !strings.Contains(view, want) {
				t.Errorf("width %d lacks %q:\n%s", width, want, view)
			}
		}
	}
}

// esc leaves without writing, and with nothing staged it leaves on the
// press: there is no typed work to put back. q is a plain letter.
func TestConfigScreen_LeavingWritesNothing(t *testing.T) {
	c := configFixture()
	done, result := c.Update(key("esc"))
	if !done || result.Write || !result.Canceled {
		t.Fatalf("esc leaves writing nothing: done=%v result=%#v", done, result)
	}
	if done, _ := configFixture().Update(key("q")); done {
		t.Fatal("q left the screen")
	}
}

// Staged edits are typed work, so the way out asks before it drops them
// (docs/interface/principles.md#esc-is-always-the-safe-answer), over the same
// count the header is carrying.
func TestConfigScreen_LeavingAsksBeforeItDiscards(t *testing.T) {
	for _, k := range []string{"esc"} {
		c := configFixture()
		c.Changed = 2
		done, result := c.Update(key(k))
		if done || result != (ConfigResult{}) {
			t.Fatalf("%s with edits staged asks first: done=%v result=%#v", k, done, result)
		}
		view := stripANSI(c.View(110))
		if !strings.Contains(view, "Discard 2 changes?") || !strings.Contains(view, "[y/N]") {
			t.Fatalf("%s asks in the inline confirm:\n%s", k, view)
		}
		if !strings.Contains(view, "2 changes unwritten") {
			t.Fatalf("the question reads the count the header carries:\n%s", view)
		}
		done, result = c.Update(key("y"))
		if !done || result.Write || !result.Canceled {
			t.Fatalf("y discards and leaves: done=%v result=%#v", done, result)
		}
	}
}

// Declining the discard keeps the screen and everything staged on it — n,
// enter and esc alike, because esc on a question has never been an answer of
// its own.
func TestConfigScreen_DecliningTheDiscardKeepsTheEdits(t *testing.T) {
	for _, k := range []string{"n", "enter", "esc"} {
		c := configFixture()
		c.Changed = 2
		c.Update(key("esc"))
		done, result := c.Update(key(k))
		if done || result != (ConfigResult{}) {
			t.Fatalf("%s on the question keeps the screen: done=%v result=%#v", k, done, result)
		}
		view := stripANSI(c.View(110))
		if strings.Contains(view, "[y/N]") {
			t.Fatalf("%s takes the question down:\n%s", k, view)
		}
		if c.Changed != 2 || !strings.Contains(view, "2 changes unwritten") {
			t.Fatalf("%s keeps the staged edits:\n%s", k, view)
		}
		// The screen is still the screen: the next key is the settings list's.
		done, _ = c.Update(key("y"))
		if done {
			t.Fatalf("%s left the answered question armed", k)
		}
	}
}

// The footer says what the way out does, which is not the same sentence with
// something staged as without it.
func TestConfigScreen_TheFooterNamesWhatTheWayOutDoes(t *testing.T) {
	c := configFixture()
	if view := stripANSI(c.View(110)); !strings.Contains(view, "[esc] leave") {
		t.Fatalf("with nothing staged esc leaves:\n%s", view)
	}
	c.Changed = 2
	if view := stripANSI(c.View(110)); !strings.Contains(view, "[esc] discard, after asking") {
		t.Fatalf("with edits staged esc discards, after asking:\n%s", view)
	}
}

// The register behind [?] reads the same state the compact row does: it would
// be describing a screen the reader is not on if it promised a question with
// nothing staged.
func TestConfigScreen_TheRegisterReadsWhatIsStaged(t *testing.T) {
	c := configFixture()
	c.Update(key("?"))
	if view := stripANSI(c.View(110)); strings.Contains(view, "asking") ||
		!strings.Contains(view, "[esc] leave the screen writing nothing") {
		t.Fatalf("with nothing staged the register promises no question:\n%s", view)
	}
	c.Changed = 2
	view := stripANSI(c.View(110))
	if !strings.Contains(view, "leave the picker, or ask before discarding the lot") {
		t.Fatalf("with edits staged the register says esc asks:\n%s", view)
	}
	if !strings.Contains(view, "[esc] ask before discarding the lot and leaving") {
		t.Fatalf("with edits staged the register says the way out asks too:\n%s", view)
	}
}

// The settings list is the selector card, so it filters like one: / opens the
// row, the row carries both counts, and the letters that are keys elsewhere
// are text while it is open.
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

// [ctrl+r] resets one row rather than the screen, and says so.
func TestConfigScreen_ResetIsOneRow(t *testing.T) {
	c := configFixture()
	_, result := c.Update(key("ctrl+r"))
	change := result.Change
	if change == nil || !change.Reset || change.Key != "behavior.default_mode" {
		t.Fatalf("ctrl+r resets the row under the pointer: %#v", result)
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
	if strings.Contains(view, "╭─") || strings.Contains(view, "╰─") {
		t.Fatalf("a takeover surface draws no card frame:\n%s", view)
	}
	lines := strings.Split(view, "\n")
	if !strings.HasPrefix(lines[0], "shhh config") {
		t.Fatalf("the header names the command and its subject: %q", lines[0])
	}
	if !strings.Contains(lines[0], "[?] keys · [esc] back") {
		t.Fatalf("the header carries the two keys every one of these screens has: %q", lines[0])
	}
}

// The screen's keyboard is seven keys: move, enter, esc, /, ctrl+s, ctrl+r
// and shift+tab, with ? for the list every screen has. Every other key is a
// letter, in the list and in a picker, and nothing in the register offers it.
func TestConfigScreen_TheRegisterIsSevenKeys(t *testing.T) {
	c := configFixture()
	c.Scoped = true
	c.Update(key("?"))
	view := ansi.Strip(c.View(130))
	for _, want := range []string{"[↑↓/jk]", "[enter]", "[/]", "[ctrl+s]", "[ctrl+r]", "[shift+tab]", "[esc]"} {
		if !strings.Contains(view, want) {
			t.Errorf("the register lacks %s:\n%s", want, view)
		}
	}
	for _, gone := range []string{"[r]", "[g]", "[d]", "[m]", "[q]", "ctrl+u", "editor"} {
		if strings.Contains(view, gone) {
			t.Errorf("the register still offers %s:\n%s", gone, view)
		}
	}
	for _, letter := range []string{"r", "g", "d", "m", "q", "ctrl+u"} {
		c := configFixture()
		c.Scoped = true
		if done, result := c.Update(key(letter)); done || result != (ConfigResult{}) {
			t.Errorf("%s did something on the list: done=%v result=%#v", letter, done, result)
		}
	}
	// A picker is move, enter, /, esc and the write chord: m and g are letters
	// there, and nothing but enter takes the option under the pointer.
	for _, letter := range []string{"m", "g", "d", "r"} {
		c := configFixture()
		c.Scoped = true
		c.Update(key("down"))
		c.Update(key("down"))
		c.Update(key("down"))
		c.Update(key("enter"))
		if c.picker == nil {
			t.Fatal("enter on the model row did not open its picker")
		}
		if _, result := c.Update(key(letter)); result.Change != nil || result.Scope {
			t.Errorf("%s answered the picker: %#v", letter, result)
		}
	}
}

// ctrl+s with a field or a picker open takes what is in it as the staged
// value and then writes, instead of being swallowed by the field; esc inside
// a field still closes it without staging.
func TestSettings_CtrlSInsideAFieldStagesThenWrites(t *testing.T) {
	c := configFixture()
	c.Update(key("down"))
	c.Update(key("enter"))
	c.Update(key("backspace"))
	c.Update(key("backspace"))
	typeInto(c, "40")
	done, result := c.Update(key("ctrl+s"))
	if done || !result.Write || result.Change == nil ||
		result.Change.Key != "behavior.max_tool_rounds" || result.Change.Value != "40" {
		t.Fatalf("ctrl+s in a field stages what was typed and writes: done=%v result=%#v", done, result)
	}

	p := configFixture()
	p.Update(key("enter"))
	p.Update(key("down"))
	_, result = p.Update(key("ctrl+s"))
	if !result.Write || result.Change == nil || result.Change.Value != "auto" {
		t.Fatalf("ctrl+s in a picker stages the option under the pointer and writes: %#v", result)
	}

	e := configFixture()
	e.Update(key("down"))
	e.Update(key("enter"))
	typeInto(e, "9")
	if _, result := e.Update(key("esc")); result.Change != nil || result.Write {
		t.Fatalf("esc in a field stages nothing: %#v", result)
	}
}
