package chat

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
)

// toolchainFixture is a declaration as the host hands it: two tools the
// contained PATH lacks, their two install lines, and the registries those
// lines may reach where the mechanism holds a list. Install is recorded
// rather than run.
func toolchainFixture(ran *[]string) Toolchain {
	return Toolchain{
		Declared: []string{"golangci-lint", "gosec", "shellcheck"},
		Missing:  []string{"golangci-lint", "gosec"},
		Lines: []string{
			"go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.5.0",
			"go install github.com/securego/gosec/v2/cmd/gosec@v2.21.4",
		},
		Bin:     "~/.cache/shhh/toolchain/bin",
		Hosts:   []string{"proxy.golang.org", "sum.golang.org"},
		Network: true,
		Install: func(_ context.Context, line string) tools.ExecResult {
			*ran = append(*ran, line)
			return tools.ExecResult{Outcome: tools.ExecSucceeded}
		},
		Recheck: func() []string { return nil },
	}
}

// toolchainModel is a first-contact session whose commands run under
// bubblewrap in a checkout that declared a toolchain.
func toolchainModel(t *testing.T, tc Toolchain) Model {
	t.Helper()
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream, Wiring{
		Start:       new(startFixture()),
		Containment: Containment{Status: "contained: bwrap (workspace profile)", Mechanism: "bwrap", Profile: "workspace", Network: true, Toolchain: tc},
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	return updated.(Model)
}

// pressFor is press, keeping the command the key started.
func pressFor(t *testing.T, m Model, s string) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(tea.KeyPressMsg{Code: []rune(s)[0], Text: s})
	return updated.(Model), cmd
}

// runSetupCmd runs what the card's yes started until the install's answer
// comes back, and hands it to the session: the lines run off the UI
// goroutine, and the batch around them carries the transcript's repaint.
func runSetupCmd(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	var pending []tea.Cmd
	if cmd != nil {
		pending = append(pending, cmd)
	}
	for len(pending) > 0 {
		next := pending[0]
		pending = pending[1:]
		switch msg := next().(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				if c != nil {
					pending = append(pending, c)
				}
			}
		case setupDoneMsg:
			updated, _ := m.Update(msg)
			return updated.(Model)
		}
	}
	t.Fatal("the yes started no install")
	return m
}

// What is missing is named before the first turn, and the last offer is the
// install rather than a check run that would fail for want of it.
func TestToolchain_TheStartScreenNamesWhatIsMissingAndOffersTheInstall(t *testing.T) {
	var ran []string
	m := toolchainModel(t, toolchainFixture(&ran))
	view := ansi.Strip(m.renderHistory())
	for _, want := range []string{"golangci-lint · gosec", "declared, not on PATH", setupCommandName + " installs them", "install the tools this checkout declares"} {
		if !strings.Contains(view, want) {
			t.Errorf("the start screen never says %q:\n%s", want, view)
		}
	}
	m.startFocus = 2
	if action := m.startAction(); action != setupCommandName {
		t.Fatalf("the last offer types %q, want %q", action, setupCommandName)
	}
	if len(ran) != 0 {
		t.Fatal("the start screen ran a line just by drawing the offer")
	}
}

// A checkout whose declared tools are all there is told nothing on the start
// screen, and the offer that costs an approval is the check run again.
func TestToolchain_NothingMissingSaysNothing(t *testing.T) {
	var ran []string
	tc := toolchainFixture(&ran)
	tc.Missing = nil
	m := toolchainModel(t, tc)
	view := ansi.Strip(m.renderHistory())
	if strings.Contains(view, "not on PATH") || strings.Contains(view, "install the tools") {
		t.Fatalf("a checkout with every tool present was told about the install:\n%s", view)
	}
	if status, _ := m.statusCommand(); !strings.Contains(status, "all on PATH") {
		t.Fatalf("/status does not say the declared tools are there:\n%s", status)
	}
}

// The card lists every line, where they land and what they may reach before
// anything runs; [n] and esc run nothing; only the yes runs, and it runs every
// line in order and reads what is missing again.
func TestToolchain_TheCardRunsTheLinesOnlyOnYes(t *testing.T) {
	var ran []string
	m := toolchainModel(t, toolchainFixture(&ran))
	m = submitLine(t, m, setupCommandName)
	if m.state != stateSetup {
		t.Fatalf("state = %v, want the toolchain card", m.state)
	}
	card := ansi.Strip(strings.Join(m.setupLines(), "\n"))
	for _, want := range []string{"golangci-lint@v2.5.0", "gosec@v2.21.4", "~/.cache/shhh/toolchain/bin", "2 hosts", "proxy.golang.org", "read-only", "bwrap"} {
		if !strings.Contains(card, want) {
			t.Errorf("the card never says %q:\n%s", want, card)
		}
	}
	for _, key := range []string{"n", "esc"} {
		m = press(t, m, key)
		if m.state == stateSetup || len(ran) != 0 {
			t.Fatalf("%s: the card stayed up or ran %v", key, ran)
		}
		if !strings.Contains(lastNote(m), "nothing installed") {
			t.Fatalf("%s: the answer does not say nothing ran: %q", key, lastNote(m))
		}
		m = submitLine(t, m, setupCommandName)
	}
	m, cmd := pressFor(t, m, "y")
	if m.state == stateSetup {
		t.Fatal("the card stayed up after the yes")
	}
	m = runSetupCmd(t, m, cmd)
	if len(ran) != 2 {
		t.Fatalf("ran %v, want both lines", ran)
	}
	if len(m.toolchain().Missing) != 0 || !strings.Contains(lastNote(m), "every declared tool is on PATH") {
		t.Fatalf("the reading after the install was not taken: %v · %q", m.toolchain().Missing, lastNote(m))
	}
}

// A line that fails stops the run there, because the lines after it may need
// what it was installing, and the row says which line and how it ended.
func TestToolchain_TheRunStopsAtTheFirstLineThatFails(t *testing.T) {
	var ran []string
	tc := toolchainFixture(&ran)
	tc.Install = func(_ context.Context, line string) tools.ExecResult {
		ran = append(ran, line)
		return tools.ExecResult{Outcome: tools.ExecExited, ExitCode: 1, Output: "go: downloading\nverifying module: checksum mismatch"}
	}
	tc.Recheck = func() []string { return []string{"golangci-lint", "gosec"} }
	m := toolchainModel(t, tc)
	m = submitLine(t, m, setupCommandName)
	m, cmd := pressFor(t, m, "y")
	m = runSetupCmd(t, m, cmd)
	if len(ran) != 1 {
		t.Fatalf("ran %v; the run should stop at the first failure", ran)
	}
	note := lastNote(m)
	for _, want := range []string{"install stopped at", "golangci-lint@v2.5.0", "exit 1", "checksum mismatch", "still not on PATH: golangci-lint, gosec"} {
		if !strings.Contains(note, want) {
			t.Errorf("the row never says %q: %q", want, note)
		}
	}
}

// A session that requires containment on a host with none cannot run the
// lines, so it draws no card whose yes could not be answered and offers
// nothing on the start screen — it names what is missing and says why.
func TestToolchain_ARequiredContainmentWithNoneDrawsNoCard(t *testing.T) {
	var ran []string
	tc := toolchainFixture(&ran)
	tc.Refusal = "this session requires containment and nothing here provides it"
	m := toolchainModel(t, tc)
	m.containment.Mechanism = ""
	view := ansi.Strip(m.renderHistory())
	if strings.Contains(view, "install the tools") || strings.Contains(view, setupCommandName+" installs") {
		t.Fatalf("the start screen offered an install that cannot run:\n%s", view)
	}
	if !strings.Contains(view, "golangci-lint · gosec") {
		t.Fatalf("what is missing was not named:\n%s", view)
	}
	m = submitLine(t, m, setupCommandName)
	if m.state == stateSetup || len(ran) != 0 {
		t.Fatalf("a refused install drew its card or ran: state %v, ran %v", m.state, ran)
	}
	if !strings.Contains(lastNote(m), "requires containment") {
		t.Fatalf("the refusal was not said: %q", lastNote(m))
	}
}

// /status says what is missing for the reader who typed past the start
// screen, and nothing at all where the checkout declared nothing.
func TestToolchain_StatusNamesWhatIsMissing(t *testing.T) {
	var ran []string
	m := toolchainModel(t, toolchainFixture(&ran))
	status, _ := m.statusCommand()
	if !strings.Contains(status, "Toolchain\ngolangci-lint · gosec — declared, not on PATH · "+setupCommandName+" installs them") {
		t.Fatalf("/status does not name what is missing:\n%s", status)
	}
	m = toolchainModel(t, Toolchain{})
	if status, _ := m.statusCommand(); strings.Contains(status, "Toolchain") {
		t.Fatalf("a checkout that declared nothing has a toolchain paragraph:\n%s", status)
	}
	if next, _ := m.setupCommand(); next.(Model).state == stateSetup {
		t.Fatal("/setup opened a card with nothing to install")
	}
}

// The paragraph naming the tools as missing was written into the system
// prompt at session start, so an install that put them on PATH is told to
// the model as a machine message naming what it put there — and only what
// it put there: a run that stopped short names the part that landed, and
// one that landed nothing says nothing.
func TestToolchain_AnInstallTellsTheModelWhatIsNowOnPath(t *testing.T) {
	for _, tt := range []struct {
		name  string
		after []string
		want  string
	}{
		{"every tool", nil, "The user installed golangci-lint and gosec from this checkout's toolchain declaration, and they are now on the PATH"},
		{"one of two", []string{"gosec"}, "The user installed golangci-lint from this checkout's toolchain declaration, and it is now on the PATH"},
		{"none", []string{"golangci-lint", "gosec"}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var ran []string
			tc := toolchainFixture(&ran)
			tc.Recheck = func() []string { return tt.after }
			m := toolchainModel(t, tc)
			before := len(m.agent.Messages())
			m = submitLine(t, m, setupCommandName)
			m, cmd := pressFor(t, m, "y")
			m = runSetupCmd(t, m, cmd)
			var told []provider.Message
			for _, msg := range m.agent.Messages()[before:] {
				if msg.Machine {
					told = append(told, msg)
				}
			}
			if tt.want == "" {
				if len(told) != 0 {
					t.Fatalf("an install that landed nothing told the model %q", told[0].Content)
				}
				return
			}
			if len(told) != 1 || !strings.Contains(told[0].Content, tt.want) {
				t.Fatalf("the model was told %v, want one machine message holding %q", told, tt.want)
			}
		})
	}
}

// runUntilToolchainRead runs what a transition started until the
// declaration's second reading comes back, and hands it to the session.
func runUntilToolchainRead(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	pending := []tea.Cmd{cmd}
	for len(pending) > 0 {
		next := pending[0]
		pending = pending[1:]
		if next == nil {
			continue
		}
		switch msg := next().(type) {
		case tea.BatchMsg:
			pending = append(pending, msg...)
		case toolchainReadMsg:
			updated, _ := m.Update(msg)
			return updated.(Model)
		}
	}
	t.Fatal("the transition started no reading of the declaration")
	return m
}

// A checkout trusted mid-session, or a declaration the draft card just wrote,
// is read again: the start screen's line, /status and the draft card's
// "loads once trusted" say what is so now, while what the session holds of
// its own — the draft and its write — stays. Nothing moved reads nothing.
func TestToolchain_TrustOrAWrittenDeclarationIsReadAgain(t *testing.T) {
	var ran []string
	moves, reads := 0, 0
	fresh := toolchainFixture(&ran)
	held := Toolchain{
		Untrusted:  true,
		Draft:      func(context.Context, bool) ToolchainDraft { return ToolchainDraft{} },
		WriteDraft: func([]byte) (string, error) { return "", nil },
		Moved:      func() int { return moves },
		Reread: func() Toolchain {
			reads++
			return fresh
		},
	}
	m := toolchainModel(t, held)
	if view := ansi.Strip(m.renderHistory()); strings.Contains(view, "not on PATH") {
		t.Fatalf("an untrusted checkout named a declaration it cannot read:\n%s", view)
	}
	if _, cmd := m.rereadToolchain(); cmd != nil {
		t.Fatal("a reading was started with nothing moved")
	}

	moves++
	updated, cmd := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m = runUntilToolchainRead(t, updated.(Model), cmd)
	if reads != 1 {
		t.Fatalf("the declaration was read %d times, want once", reads)
	}
	if view := ansi.Strip(m.renderHistory()); !strings.Contains(view, "golangci-lint · gosec") {
		t.Fatalf("the start screen still reads the declaration as it was:\n%s", view)
	}
	if status, _ := m.statusCommand(); !strings.Contains(status, "Toolchain\ngolangci-lint · gosec — declared, not on PATH") {
		t.Fatalf("/status still reads the declaration as it was:\n%s", status)
	}
	tc := m.toolchain()
	if tc.Untrusted {
		t.Fatal("the draft card would still say the file loads once trusted")
	}
	if tc.Draft == nil || tc.WriteDraft == nil || tc.Reread == nil || tc.Moved == nil {
		t.Fatal("the reading dropped what the session holds of its own")
	}
	if _, cmd := m.rereadToolchain(); cmd != nil {
		t.Fatal("a second reading was started for the same move")
	}
}
