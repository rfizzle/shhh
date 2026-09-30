package chat

// The toolchain offer. A checkout's `.shhh/toolchain.toml` names the binaries
// its work expects on PATH, and a session that starts without them is one
// whose model spends its first rounds discovering that `golangci-lint` is not
// there and then trying to install it itself. So the session says what is
// missing before the first turn — on the start screen, in /status — and puts
// the declaration's install lines to the person on one card. The card is the
// only way the lines run: nothing offers them to the classifier or to a
// mode, because they are command text a checkout wrote
// (docs/capabilities/containment.md#a-checkout-declares-the-toolchain-its-work-needs).
//
// It is the scaffold card's shape and for the scaffold card's reason: a
// suggestion that ran something the moment it was chosen would be the one
// row on the start screen worth more than it says
// (docs/interface/surfaces.md#the-start-screen).

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// setupCommandName is the command the offer stands for, named once so the
// start screen's row and the register cannot drift apart.
const setupCommandName = "/setup"

// Toolchain is the checkout's toolchain declaration as this session read it,
// and the install behind its card. Every field is answered by the host at
// session start; nothing here looks at the machine.
type Toolchain struct {
	// Declared is the declaration's check list: what the work expects on
	// PATH. Empty is a checkout that declares nothing, and every surface
	// below then says nothing.
	Declared []string
	// Missing is the part of Declared the contained PATH does not have.
	Missing []string
	// Lines are the install lines the card lists and runs, in order.
	Lines []string
	// Bin is where the lines put what they install, as the card names it.
	Bin string
	// Hosts are the only hosts the lines may reach — the declaration's, set
	// only where the mechanism in force holds a list — and Network is
	// whether the profile gives the lines a network at all.
	Hosts   []string
	Network bool
	// Refusal is why the lines cannot run in this session at all — a
	// session that requires containment on a host with none. No card is
	// drawn for it: a card whose yes cannot be answered is not a decision,
	// so /setup says why instead and the start screen offers nothing.
	Refusal string
	// Install runs one line as the card's yes runs it. Nil is a session
	// with nobody to put the card to, which offers nothing.
	Install func(ctx context.Context, line string) tools.ExecResult
	// Recheck reads Missing again once the lines have run.
	Recheck func() []string
	// installing is a run under way, so a second /setup does not start a
	// second run of the same lines into the same directory.
	installing bool
}

// toolchain is the declaration as this session holds it.
func (m Model) toolchain() Toolchain { return m.containment.Toolchain }

// setupWired reports a session that has lines to offer and somebody to
// offer them to — which is what /setup exists in.
func (m Model) setupWired() bool {
	tc := m.toolchain()
	return tc.Install != nil && len(tc.Lines) > 0
}

// Runnable reports that the card could run the lines in this session, which
// a session that requires containment on a host with none cannot. It is
// what the model is told the person was offered, so the two cannot differ.
func (t Toolchain) Runnable() bool {
	return t.Install != nil && len(t.Lines) > 0 && t.Refusal == ""
}

// setupRunnable is Runnable for the session's own declaration.
func (m Model) setupRunnable() bool { return m.toolchain().Runnable() }

// setupOffered reports whether the start screen spends its last offer on the
// install: something is missing and the card could put it right.
func (m Model) setupOffered() bool {
	return m.setupRunnable() && len(m.toolchain().Missing) > 0
}

// toolchainNote is the start screen's line naming what is missing, or false
// where nothing is.
func (m Model) toolchainNote() (components.StartNote, bool) {
	tc := m.toolchain()
	if len(tc.Missing) == 0 {
		return components.StartNote{}, false
	}
	detail := "declared, not on PATH"
	if m.setupRunnable() {
		detail += " · " + setupCommandName + " installs them"
	}
	return components.StartNote{Label: "tools", Value: strings.Join(tc.Missing, " · "), Detail: detail}, true
}

// toolchainStatus is the `/status` paragraph for the declaration: what is
// missing, or that nothing is, and nothing at all where nothing was declared.
func (m Model) toolchainStatus() string {
	tc := m.toolchain()
	if len(tc.Declared) == 0 {
		return ""
	}
	if len(tc.Missing) == 0 {
		return "Toolchain\n" + strings.Join(tc.Declared, " · ") + " — all on PATH"
	}
	line := "Toolchain\n" + strings.Join(tc.Missing, " · ") + " — declared, not on PATH"
	if m.setupRunnable() {
		line += " · " + setupCommandName + " installs them"
	}
	return line
}

// setupCommand is /setup: the card, or the reason there is none.
func (m Model) setupCommand() (tea.Model, tea.Cmd) {
	if !m.setupWired() {
		return m.systemNotice("this checkout declares nothing to install here")
	}
	if refusal := m.toolchain().Refusal; refusal != "" {
		return m.systemNotice("nothing can be installed here: " + refusal)
	}
	if m.toolchain().installing {
		return m.systemNotice("the declared tools are being installed already")
	}
	m.enterSurface(stateSetup)
	m.syncViewport()
	return m, nil
}

// setupCard is the approval card for the install: every line it would run,
// where they land, what they may reach and what contains them.
func (m Model) setupCard() *components.ApprovalCard {
	tc := m.toolchain()
	// The install runs under the session's own mechanism, so the card names
	// it the way a command card does.
	mechanism := m.containment.Mechanism
	act := "install what .shhh/toolchain.toml declares"
	if len(tc.Missing) > 0 {
		act = "install " + strings.Join(tc.Missing, ", ")
	}
	card := &components.ApprovalCard{
		Variant:     components.ApprovalGeneric,
		Title:       "Approve toolchain install",
		ActGlyph:    "$",
		Act:         act,
		Summary:     "the lines run one at a time and stop at the first that fails",
		Answer:      "install them",
		Decline:     "no — nothing installed",
		Return:      "leave — nothing installed",
		Uncontained: mechanism == "",
		MaxLines:    m.planPanelBound(),
		KeyList:     true,
	}
	for _, line := range tc.Lines {
		card.Fields = append(card.Fields, components.CardField{Label: "runs", Value: line})
	}
	card.Fields = append(card.Fields,
		components.CardField{Label: "lands in", Value: tc.Bin, Detail: "shhh's own, on every command's PATH"},
		setupNetwork(tc, mechanism))
	// What the wall is, where there is one: the checkout itself is not
	// written. Where there is none the UNCONTAINED chip says so instead, as
	// it does on a command card.
	if mechanism != "" {
		card.Fields = append(card.Fields,
			components.CardField{Label: "workspace", Value: "read-only", Tone: components.ToneSafe},
			components.CardField{Label: "sandbox", Value: m.containmentWords(mechanism), Tone: components.ToneChrome})
	}
	return card
}

// setupNetwork is the network row, in the words the command card's own uses
// (radius.go), about the network the install is given rather than the one
// the session's commands are.
func setupNetwork(tc Toolchain, mechanism string) components.CardField {
	f := components.CardField{Label: "network"}
	switch {
	case mechanism == "":
		f.Value, f.Detail, f.Tone = "open", "nothing contains the install, so nothing limits what it reaches", components.ToneOpen
	case len(tc.Hosts) > 0:
		f.Value = fmt.Sprintf("%d hosts", len(tc.Hosts))
		if len(tc.Hosts) == 1 {
			f.Value = "1 host"
		}
		f.Detail, f.Tone = "only "+strings.Join(tc.Hosts, ", ")+"; every other host is refused", components.ToneNeutral
	case tc.Network:
		f.Value, f.Detail, f.Tone = "open", "the declaration names no hosts, so the profile's network is the install's", components.ToneOpen
	default:
		f.Value, f.Detail, f.Tone = "closed", "the containment profile removes it, so a line that downloads will fail", components.ToneSafe
	}
	return f
}

// answerSetup routes the card's keys the way the scaffold card's are routed
// (scaffold.go): esc and [n] both leave with nothing run, and only the yes
// runs anything.
func (m *Model) answerSetup(msg tea.KeyPressMsg) (bool, overlayAction) {
	switch {
	case keys.Match(msg, keys.Select.Cancel), keys.Match(msg, keys.Decision.Refuse):
		return true, overlayAction{close: true, note: "nothing installed; " + setupCommandName + " offers it again"}
	case keys.Match(msg, keys.Decision.Accept):
		tc := m.toolchain()
		m.containment.Toolchain.installing = true
		return true, overlayAction{
			close: true,
			note:  "installing " + plural(len(tc.Lines), "line") + " from .shhh/toolchain.toml",
			run:   runSetup(tc),
		}
	}
	return false, overlayAction{}
}

// setupDoneMsg is the install finished: how many lines ran, the one that
// failed if one did, and what is still missing afterwards.
type setupDoneMsg struct {
	ran     int
	failed  string
	result  tools.ExecResult
	missing []string
}

// runSetup runs the lines in order, off the UI goroutine, and stops at the
// first that fails: the lines after it may need what it was installing, and
// a run that went on would report a second failure that is the first one's.
func runSetup(tc Toolchain) tea.Cmd {
	return func() tea.Msg {
		var done setupDoneMsg
		for _, line := range tc.Lines {
			res := tc.Install(context.Background(), line)
			if res.Failed() {
				done.failed, done.result = line, res
				break
			}
			done.ran++
		}
		done.missing = tc.Missing
		if tc.Recheck != nil {
			done.missing = tc.Recheck()
		}
		return done
	}
}

// finishSetup takes the run's answer onto the session: what is missing now,
// and one row saying what the run came to.
func (m Model) finishSetup(msg setupDoneMsg) (tea.Model, tea.Cmd) {
	m.containment.Toolchain.installing = false
	m.containment.Toolchain.Missing = msg.missing
	var b strings.Builder
	if msg.failed != "" {
		fmt.Fprintf(&b, "install stopped at `%s` (%s)", msg.failed, setupEnding(msg.result))
		if tail := setupTail(msg.result.Output); tail != "" {
			b.WriteString("\n" + tail)
		}
	} else {
		fmt.Fprintf(&b, "installed: %s ran", plural(msg.ran, "line"))
	}
	if len(msg.missing) > 0 {
		b.WriteString("\nstill not on PATH: " + strings.Join(msg.missing, ", "))
	} else if len(m.toolchain().Declared) > 0 {
		b.WriteString("\nevery declared tool is on PATH")
	}
	return m.systemNotice(b.String())
}

// setupEnding is how a failed line ended, in the words a command row uses.
func setupEnding(r tools.ExecResult) string {
	switch r.Outcome {
	case tools.ExecExited:
		return fmt.Sprintf("exit %d", r.ExitCode)
	case tools.ExecDidNotStart:
		return "did not start"
	}
	return "did not finish"
}

// setupTailLines bounds what a failed line's output leaves on the transcript:
// the end is where an installer says why, and the rest is a download log.
const setupTailLines = 8

func setupTail(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) > setupTailLines {
		lines = lines[len(lines)-setupTailLines:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// setupLines renders the card, one row per line.
func (m Model) setupLines() []string {
	return strings.Split(m.setupCard().View(m.contentWidth()), "\n")
}
