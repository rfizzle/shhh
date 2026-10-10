package chat

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// sandboxWriterCommands and sandboxWriterDetail are what the host says a
// writer's commands run under in a sandbox session, on the spawn card's row
// and on /status: contained by the container sandbox — the session
// indicator's word — in a container of the writer's own over its copy.
const (
	sandboxWriterCommands = "contained · container sandbox"
	sandboxWriterDetail   = "required · its own container over its worktree · workspace-netless · network closed"
)

// sandboxContainment is a sandbox session's containment as the host fills
// it: the container is the mechanism, the profile its own, the network the
// profile's switch with no host list, a writer's commands in a container of
// their own, and the declared tools read in the container rather than off this machine's PATH.
func sandboxContainment() Containment {
	return Containment{
		Status:    "contained: container sandbox (workspace-netless profile)",
		Mechanism: "container sandbox",
		Profile:   "workspace-netless",
		GitStore:  "read-only git hooks and config",
		Engine:    "docker",
		Image:     "sha256:ab12cd34ef56…",
		Detail:    "docker (rootless) at /usr/bin/docker · image ghcr.io/rfizzle/shhh-sandbox@sha256:0123456789ab…",
		Writers:   "a writer's commands: " + sandboxWriterCommands + " — " + sandboxWriterDetail,
		Toolchain: Toolchain{
			Declared: []string{"go", "golangci-lint"},
			Missing:  []string{"golangci-lint"},
			Where:    "in the sandbox image",
			Refusal:  "this session's commands run in its sandbox container, whose tools come from the image the declaration prepares",
		},
	}
}

// TestGolden_SandboxSession captures a session whose commands run in a
// sandbox container on the surfaces that say so today: the start screen,
// whose declared-tools line reads the container and offers no install, and
// /status, whose containment block names the container, its profile and what
// a writer's commands get; and /setup, which says the image is where a
// sandbox's tools come from rather than offering an install here.
func TestGolden_SandboxSession(t *testing.T) {
	captureGolden(t, "sandbox-session", "a session whose commands run in a sandbox container", goldenWidths, func(width int) []golden.Panel {
		start := frameModelWith(t, width, 40, Wiring{Containment: sandboxContainment()})
		start.start = new(startFixture())
		status := frameModelWith(t, width, 40, Wiring{Containment: sandboxContainment()})
		status = submitLine(t, status, "/status")
		setup := frameModelWith(t, width, 40, Wiring{Containment: sandboxContainment()})
		setup = submitLine(t, setup, "/setup")
		return []golden.Panel{
			{Label: "the start screen · the container lacks a declared tool, and nothing here installs it", View: start.renderHistory()},
			{Label: "/status · the container, its profile, and a writer's commands", View: status.renderHistory()},
			{Label: "/setup · the image is where the tools come from", View: setup.renderHistory()},
		}
	})
}

// TestGolden_SandboxWriterSpawnCard captures the card a writer is approved on
// in a sandbox session: its commands row says, before the answer, that they
// run contained by the container sandbox, in a container of the writer's own
// over its worktree, under the session's profile with the network closed.
func TestGolden_SandboxWriterSpawnCard(t *testing.T) {
	preview := func(raw json.RawMessage) (GatedPreview, error) {
		p, err := spawnPreview(raw)
		p.Fields = append(p.Fields, GatedField{Label: "commands", Value: sandboxWriterCommands, Detail: sandboxWriterDetail})
		return p, err
	}
	captureGolden(t, "sandbox-writer-spawn-card", "a writer's spawn card in a sandbox session", goldenWidths, func(width int) []golden.Panel {
		m := gatedModelWith(t, func(string, json.RawMessage) (string, error) { return "started", nil },
			map[string]GatedPreviewFunc{subagent.SpawnToolName: preview}, Wiring{Containment: sandboxContainment()})
		updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
			spawnCall("s1", `{"role":"writer","task":"make the build pass","name":"writer-1","paths":["internal/agent/loop.go"]}`),
		}})
		m = handover(t, updated.(Model))
		updated, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 48})
		m = updated.(Model)
		view := strings.Join(m.confirmLines(), "\n")
		if !strings.Contains(view, sandboxWriterCommands) {
			t.Fatalf("the card does not say what contains the writer's commands:\n%s", view)
		}
		return []golden.Panel{
			{Label: "one writer · its commands in a container of its own", View: view},
		}
	})
}

// TestGolden_SandboxIndicator captures the indicator a sandbox session wears
// where the reader always looks: the chip that ends the title rail, which
// keeps its word after the rail has shed the model's, and the start screen's
// row between the gate and the tools. Both are read from the containment in
// force.
func TestGolden_SandboxIndicator(t *testing.T) {
	captureGolden(t, "sandbox-indicator", "the title rail and the start screen in a sandbox session", goldenWidths, func(width int) []golden.Panel {
		m := frameModelWith(t, width, 40, Wiring{Containment: sandboxContainment(), Title: "shhh code"})
		m.modelName = "scripted-model"
		m.start = new(startFixture())
		return []golden.Panel{
			{Label: "the title rail · the chip is a word with a glyph, and the last to go", View: m.headerRow(width)},
			{Label: "the start screen · the sandbox row between the gate and the tools", View: m.renderHistory()},
		}
	})
}

// TestSandboxIndicator_ReadsTheContainmentInForce holds that the chip and the
// row are drawn from the containment the host filled in and from nothing
// else: an unconfined session, and a session whose container was never made,
// draw neither; the network reads as it is; and the rail sheds the model's
// words before the chip.
func TestSandboxIndicator_ReadsTheContainmentInForce(t *testing.T) {
	m := frameModelWith(t, 120, 40, Wiring{Containment: sandboxContainment(), Title: "shhh code"})
	m.modelName = "scripted-model"
	m.start = new(startFixture())
	if row := stripANSI(m.headerRow(120)); !strings.HasSuffix(row, "· "+sandboxChip) {
		t.Errorf("rail = %q, want it to end in the chip", row)
	}
	note, ok := m.sandboxNote()
	if want := "container · workspace-netless · no network · docker, image sha256:ab12cd34ef56…"; !ok || note.Value != want {
		t.Errorf("row = %q (%v), want %q", note.Value, ok, want)
	}
	for _, width := range []int{80, 60, 40} {
		row := stripANSI(m.headerRow(width))
		if !strings.Contains(row, sandboxChip) {
			t.Errorf("width %d: rail %q lost the chip", width, row)
		}
	}
	if row := stripANSI(m.headerRow(30)); strings.Contains(row, "scripted-model") {
		t.Errorf("rail %q kept the model ahead of the chip", row)
	}

	open := sandboxContainment()
	open.Network = true
	opened := frameModelWith(t, 120, 40, Wiring{Containment: open})
	if note, _ := opened.sandboxNote(); !strings.Contains(note.Value, "network open") {
		t.Errorf("row = %q, want the open network said", note.Value)
	}

	for name, c := range map[string]Containment{
		"unconfined":     {Status: "unconfined — no engine", Detail: "no engine"},
		"never made":     {Status: "contained: container sandbox (workspace-netless profile)", Profile: "workspace-netless"},
		"another wall":   {Status: "contained: landlock", Mechanism: "landlock", Profile: "workspace-netless"},
		"nothing wired":  {},
		"engine, no box": {Engine: "docker", Image: "sha256:ab12cd34ef56…"},
	} {
		other := frameModelWith(t, 120, 40, Wiring{Containment: c, Title: "shhh code"})
		other.modelName = "scripted-model"
		other.start = new(startFixture())
		if _, ok := other.sandboxNote(); ok {
			t.Errorf("%s: drew the sandbox row", name)
		}
		if strings.Contains(stripANSI(other.headerRow(120)), "sandbox") {
			t.Errorf("%s: drew the chip", name)
		}
	}
}
