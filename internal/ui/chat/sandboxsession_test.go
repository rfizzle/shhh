package chat

import (
	"testing"

	"github.com/rfizzle/shhh/internal/ui/golden"
)

// sandboxContainment is a sandbox session's containment as the host fills
// it: the container is the mechanism, the profile its own, the network the
// profile's switch with no host list, a writer's commands refused, and the
// declared tools read in the container rather than off this machine's PATH.
func sandboxContainment() Containment {
	return Containment{
		Status:    "contained: container sandbox (workspace-netless profile)",
		Mechanism: "container sandbox",
		Profile:   "workspace-netless",
		GitStore:  "read-only git hooks and config",
		Detail:    "docker (rootless) at /usr/bin/docker · image ghcr.io/rfizzle/shhh-sandbox@sha256:0123456789ab…",
		Writers:   "a writer's commands: refused — a writer's worktree is outside this session's container",
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
