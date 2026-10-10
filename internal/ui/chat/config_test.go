package chat

// `/config` against the surface it borrows and the host behind it.
//
// The screen itself is tested where it lives (internal/ui/components) and its
// config semantics where those live (internal/cli). What is this package's is
// the seam: that a session hosts the screen off one register row, that
// nothing it stages reaches the host until the write, and that the two ways
// out cost what the CLI host's cost.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// fakeConfigHost is the config semantics this package does not own: it
// stages what a change asked for, counts what is standing against the file,
// and writes on the one result that says to.
type fakeConfigHost struct {
	screen components.ConfigScreen
	staged map[string]string
	wrote  map[string]string
	writes int
	// failWith is a write that cannot land: the reason it gives.
	failWith string
	// take is what the host hands the session after each answer, nil for a
	// host that hands it nothing.
	take func(w *Wiring) bool
}

func newFakeConfigHost() *fakeConfigHost {
	h := &fakeConfigHost{staged: map[string]string{}, wrote: map[string]string{}}
	h.screen = components.ConfigScreen{
		Path:      "~/.config/shhh/config.toml",
		InSession: true,
		Rows: []components.ConfigRow{
			{Group: "BEHAVIOR", Key: "behavior.check_in_rounds", Label: "Check in every",
				Value: "8", Source: "default"},
			{Group: "BEHAVIOR", Key: "behavior.default_mode", Label: "Permission mode",
				Value: "auto", Source: "user",
				Options: []components.SelectOption{{Label: "manual"}, {Label: "auto"}}},
		},
	}
	return h
}

func (h *fakeConfigHost) session() ConfigSession {
	return ConfigSession{Screen: &h.screen, Answer: h.answer, Take: h.take}
}

func (h *fakeConfigHost) answer(done bool, result components.ConfigResult) string {
	if c := result.Change; c != nil {
		h.staged[c.Key] = c.Value
		for i := range h.screen.Rows {
			if h.screen.Rows[i].Key == c.Key {
				h.screen.Rows[i].Value = c.Value
			}
		}
		h.screen.Changed = len(h.staged)
	}
	if result.Write {
		if h.failWith != "" {
			h.screen.Notice = "could not write " + h.screen.Path + ": " + h.failWith
			return h.screen.Notice
		}
		h.writes++
		var changed []string
		for k, v := range h.staged {
			h.wrote[k] = v
			changed = append(changed, k)
		}
		receipt := components.WriteReceipt(components.Changes(len(changed)), h.screen.Path, changed...)
		h.staged, h.screen.Changed, h.screen.Notice = map[string]string{}, 0, receipt
		return receipt + "\nThis session keeps the settings it started on; the next one starts on these."
	}
	return ""
}

// configModelWith is a session whose /config opens the given host.
func configModelWith(t *testing.T, h *fakeConfigHost) Model {
	t.Helper()
	m := readyModel(t)
	m.wiring.ConfigScreen = func([]string) (ConfigSession, error) {
		return h.session(), nil
	}
	return m
}

// stageOne types a value into the first row: enter opens the field over what
// is already there, backspace clears it, the runes go in, enter takes it. That
// is the path a reader walks, which is what makes the count the header
// carries afterwards mean anything.
func stageOne(t *testing.T, m Model, value string) Model {
	t.Helper()
	m = pressKeys(t, m, keyEnter)
	for range 12 {
		m = pressKeys(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	m = typeChars(t, m, value)
	return pressKeys(t, m, keyEnter)
}

func TestConfig_TheCommandOpensTheScreen(t *testing.T) {
	h := newFakeConfigHost()
	m := sendText(t, configModelWith(t, h), "/config")
	if m.state != stateConfig || m.screens.config() == nil {
		t.Fatalf("/config left the session in state %v", m.state)
	}
	view := strings.Join(m.configScreenLines(m.contentWidth(), 30), "\n")
	for _, want := range []string{
		"/config",                    // the surface names itself as it was reached
		"~/.config/shhh/config.toml", // and the file it would write
		"Check in every",             // the rows the host handed it
		"back",                       // the way out, in the header
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen is missing %q:\n%s", want, view)
		}
	}
	if hint := m.renderConfigHint(); !strings.Contains(hint, "config") {
		t.Errorf("the panel does not say which surface has the keyboard: %q", hint)
	}
}

// /help lists it, and only where the session can reach it: the help, the
// completion menu and the answer to a typed command all read the one table.
func TestConfig_HelpListsItWhereTheSessionHasIt(t *testing.T) {
	h := newFakeConfigHost()
	m := configModelWith(t, h)
	help := helpText(&m)
	if !strings.Contains(help, "/config") {
		t.Error("/help never names /config")
	}
	if !strings.Contains(help, "reaches your config file until [ctrl+s]") {
		t.Errorf("/help does not say when an edit reaches the file:\n%s", help)
	}
	bare := readyModel(t)
	if strings.Contains(helpText(&bare), "/config") {
		t.Error("/help offers /config to a session that cannot open it")
	}
}

// One register row and no second list: the state borrows the screen, so the
// turn underneath goes on running and the pane draws the screen's own rows.
func TestConfig_IsOneRegisterRow(t *testing.T) {
	o := overlayFor(stateConfig)
	if o == nil {
		t.Fatal("stateConfig has no row in the register")
	}
	if o.Placement() != placePane {
		t.Fatalf("the settings screen places %d, want the pane", o.Placement())
	}
	if !o.borrows || !stateConfig.isSurface() {
		t.Error("the screen must borrow the turn's screen rather than be a stage of it")
	}
	h := newFakeConfigHost()
	m := configModelWith(t, h)
	m.setTurnState(stateStreaming)
	m = sendText(t, m, "/config")
	if m.turnState() != stateStreaming {
		t.Fatalf("the turn under the screen is %v, want it still streaming", m.turnState())
	}
	if len(o.Lines(m, 100, 30)) == 0 {
		t.Error("the row draws nothing")
	}
	// The two readers of the state that are not the register: a takeover
	// spans both panes, and a surface drawing its own rows is not one the
	// transcript's drag-selection may anchor in.
	if !m.inspectorHidden() {
		t.Error("the rail is still drawn under a surface that takes both panes")
	}
	m.pointer.mouseOn, m.ready = true, true
	if m.selectableSurface() {
		t.Error("the transcript's selection is live over a surface that replaced it")
	}
}

// Nothing reaches the host until the write, and the write does not ask.
func TestConfig_NothingIsWrittenUntilTheWriteKey(t *testing.T) {
	h := newFakeConfigHost()
	m := sendText(t, configModelWith(t, h), "/config")
	m = stageOne(t, m, "3")

	if h.screen.Changed != 1 {
		t.Fatalf("%d changes standing after one edit, want 1", h.screen.Changed)
	}
	if len(h.wrote) != 0 {
		t.Fatalf("a staged edit reached the file: %v", h.wrote)
	}

	m = pressKeys(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if m.state != stateConfig {
		t.Fatal("the write closed the screen: it stays up to show its receipt")
	}
	if h.writes != 1 || h.wrote["behavior.check_in_rounds"] != "3" {
		t.Fatalf("%d writes, file holds %v", h.writes, h.wrote)
	}
	last := m.transcript[len(m.transcript)-1].text
	if !strings.Contains(last, "wrote 1 change to ~/.config/shhh/config.toml · behavior.check_in_rounds") {
		t.Errorf("the transcript row does not say what was written: %q", last)
	}
	// The write is about the next session, and a row that said only "wrote"
	// would let the reader believe this turn had changed under them.
	if !strings.Contains(last, "keeps the settings it started on") {
		t.Errorf("the transcript row does not say what the write did not do: %q", last)
	}
}

// Escape is the safe answer here as it is on the CLI host: with work staged
// it asks, and declining leaves both the screen and the edits alone.
func TestConfig_EscapeAsksBeforeDiscarding(t *testing.T) {
	h := newFakeConfigHost()
	m := sendText(t, configModelWith(t, h), "/config")
	m = stageOne(t, m, "3")

	m = pressKeys(t, m, keyEsc)
	if m.state != stateConfig {
		t.Fatal("escape discarded the staged edit without asking")
	}
	view := strings.Join(m.configScreenLines(m.contentWidth(), 30), "\n")
	if !strings.Contains(view, "Discard") {
		t.Errorf("the question the discard asks is not on the screen:\n%s", view)
	}
	m = pressKeys(t, m, tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.state != stateConfig || h.screen.Changed != 1 {
		t.Fatalf("declining the question cost something: state %v, %d changes", m.state, h.screen.Changed)
	}
	m = pressKeys(t, m, keyEsc, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if m.state == stateConfig {
		t.Fatal("the screen is still up after the discard")
	}
	if len(h.wrote) != 0 {
		t.Fatalf("the way out wrote something: %v", h.wrote)
	}
}

// With nothing staged there is nothing to lose, so the way out is one press
// and leaves no row behind.
func TestConfig_LeavingWithNothingStagedIsOnePress(t *testing.T) {
	h := newFakeConfigHost()
	m := sendText(t, configModelWith(t, h), "/config")
	before := len(m.transcript)
	m = pressKeys(t, m, keyEsc)
	if m.state == stateConfig || m.screens.config() != nil {
		t.Fatal("escape over an untouched screen did not close it")
	}
	if len(m.transcript) != before {
		t.Errorf("leaving without writing left a row: %q", m.transcript[len(m.transcript)-1].text)
	}
}

// A session the CLI never gave an opener to says so rather than drawing a
// screen with nothing behind it.
func TestConfig_ASessionWithoutAHostSaysSo(t *testing.T) {
	m := sendText(t, readyModel(t), "/config")
	if m.state == stateConfig {
		t.Fatal("a session with no config host opened the screen anyway")
	}
	last := m.transcript[len(m.transcript)-1].text
	if !strings.Contains(last, "shhh config") {
		t.Errorf("the answer does not name the other door: %q", last)
	}
}

// A mode and a model the settings screen has the session take are the
// session's in every way /permissions and /model would make them: the status
// line draws them the same, and the mode cycle walks on from the taken mode.
func TestConfigScreen_ATakenModeReadsLikeTheModeKey(t *testing.T) {
	var switched []string
	switchModel := func(name string) { switched = append(switched, name) }

	h := newFakeConfigHost()
	h.take = func(w *Wiring) bool {
		if len(h.staged) == 0 {
			return false
		}
		w.Mode, w.ModelName = agent.ModeAuto, "model-b"
		return true
	}
	m := configModelWith(t, h)
	m.wiring.SwitchModel = switchModel
	m = sendText(t, m, "/config")
	m = stageOne(t, m, "auto")
	if m.policy.mode != agent.ModeAuto || m.modelName != "model-b" {
		t.Fatalf("the session runs in %s on %q after the staging", m.policy.mode, m.modelName)
	}
	if len(switched) != 1 || switched[0] != "model-b" {
		t.Errorf("the session's stream was switched to %v", switched)
	}
	m = pressKeys(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}, keyEsc)

	ref := readyModel(t)
	ref.wiring.SwitchModel = switchModel
	ref = sendText(t, ref, "/permissions auto")
	ref = sendText(t, ref, "/model model-b")
	got, header := m.renderStatusBar(120), m.headerRow(120)
	if !strings.Contains(got, "auto") || !strings.Contains(header, "model-b") {
		t.Errorf("the frame does not draw the taken mode and model:\n%s\n%s", header, got)
	}
	if want := ref.renderStatusBar(120); got != want {
		t.Errorf("the status line after the screen's take:\n%s\nafter the commands:\n%s", got, want)
	}
	if want := ref.headerRow(120); header != want {
		t.Errorf("the header after the screen's take:\n%s\nafter the commands:\n%s", header, want)
	}
	shiftTab := tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	m, ref = pressKeys(t, m, shiftTab), pressKeys(t, ref, shiftTab)
	if m.policy.mode != ref.policy.mode || m.policy.mode == agent.ModeAuto {
		t.Errorf("the cycle walked from the taken mode to %s, from the command's to %s", m.policy.mode, ref.policy.mode)
	}
}

// While a turn runs, what the screen staged waits for the next turn to open,
// so the model and the round limit never move under the turn using them.
func TestConfigScreen_ATakeWaitsForTheTurnBoundary(t *testing.T) {
	m := readyModel(t)
	m.turnOpen = true
	m.takeStaged(func(w *Wiring) bool { w.Mode, w.MaxToolRounds = agent.ModeAuto, 7; return true })
	if m.policy.mode == agent.ModeAuto || m.agent.MaxRounds() == 7 {
		t.Fatal("the take moved the session under a running turn")
	}
	m.turnOpen = false
	m.openTurn("next")
	if m.policy.mode != agent.ModeAuto || m.agent.MaxRounds() != 7 {
		t.Errorf("the next turn opened in %s on %d rounds, want the staged auto and 7", m.policy.mode, m.agent.MaxRounds())
	}

	// What the screen did not move is not put back: a mode cycled while the
	// take waited is the mode the next turn opens in.
	m.turnOpen = true
	m.takeStaged(func(w *Wiring) bool { w.MaxToolRounds = 9; return true })
	m.applyMode(agent.ModePlan)
	m.turnOpen = false
	m.openTurn("after")
	if m.policy.mode != agent.ModePlan || m.agent.MaxRounds() != 9 {
		t.Errorf("the turn opened in %s on %d rounds, want the cycled plan and the staged 9", m.policy.mode, m.agent.MaxRounds())
	}
}
