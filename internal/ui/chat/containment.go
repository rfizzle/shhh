package chat

import (
	"strings"

	"github.com/rfizzle/shhh/internal/ui/components"
)

// ContainerSandbox is the Mechanism of a sandbox session, whose commands run
// in a disposable container. The host that fills a Containment and the model
// that reads one agree on this word, because it is also what decides whose
// commands go through Run.
const ContainerSandbox = "container sandbox"

// Containment is the process-containment setup for assistant commands.
// When Run is set, approved and waved-through execute_command calls run
// through it (the sandbox-wrapped runner) instead of the plain runner. The
// user's own command — /run, ! and !! — stays on the plain runner and is
// never refused, with one exception: in a sandbox session (Mechanism is
// ContainerSandbox) it runs through TailRun too, because the container is
// the machine the work is on, and a check made on the host would be a check
// of a machine the work is not on. That is the person's decision of
// 2026-10-01, and it moves nothing about the ceiling or the refusal: the
// command is still theirs.
// See docs/capabilities/containment.md#containment-can-be-required.
// Status is the one-line state shown on the exec confirm prompt, and Report
// is the full doctor text behind /sandbox.
type Containment struct {
	// Run answers with the typed result rather than an output and a status,
	// because a wrap that could not be built is a command that never started
	// and the category of that is a fact the row has to be handed, not read
	// back out of text.
	// See docs/capabilities/containment.md#a-command-that-never-started-names-what-it-needed.
	Run RunFunc
	// TailRun is Run with live per-line output reporting for the activity
	// feed's running row; nil runs contained commands with no tail.
	TailRun TailFunc
	Status  string
	Report  string
	// Now is Report resolved again against the working scope as it stands,
	// on the mechanism the session already found — no probe — which is what
	// /safety reads, so a directory granted mid-session is on the page.
	// Nil leaves the reading on Report.
	Now func() string
	// Mechanism, Profile and Network are the same state in the pieces the
	// approval card's blast-radius block needs: the chip on the title
	// rail, and the honest answer to "is the network open". An empty
	// Mechanism means nothing is containing assistant commands, and Detail is
	// then why — the text /sandbox doctor expands on.
	Mechanism string
	Profile   string
	Network   bool
	// Hosts narrows an open network to these hosts, the list the mechanism
	// holds; empty is the network the profile gives. It is set only where
	// the mechanism in force holds the list, so a card never names hosts
	// that nothing is confining the command to.
	Hosts  []string
	Detail string
	// GitStore is sandbox.GitStoreWords: that the workspace repository's
	// hooks and config are read-only to a contained command, in the words
	// the doctor row uses, or "" where nothing is masked. It rides the
	// sandbox field and /status because git runs those programs on the host
	// and a reader deciding on a command is owed whether it can plant one.
	// See docs/capabilities/containment.md#the-repositorys-own-programs-are-read-only.
	GitStore string
	// Engine and Image are what the sandbox container runs on and from: the
	// engine's name and the image's short digest, as the host read them from
	// the container it made. They are set only where a container is in
	// force, so the indicator on the title rail, the start screen's row and
	// /status can never name an engine or an image nothing is running.
	// See docs/capabilities/containment.md#what-is-reported-is-what-is-in-force.
	Engine string
	Image  string
	// Required says the session was told to contain the assistant's
	// commands rather than to prefer it, which is what the chip reports: a
	// mechanism that is in force and a mechanism that had to be are
	// different facts about the same session.
	Required bool
	// Refusal is why no assistant command may run at all — a session that
	// requires containment on a host with none. Non-empty is answered
	// before the card is drawn: there is nothing to decide, so the model
	// gets this as the call's result and the reader is not asked to approve
	// something that cannot happen.
	// See docs/capabilities/containment.md#containment-can-be-required.
	Refusal string
	// Writers is what a writer's commands run under on this host, as one
	// line: contained, refused because a writer must be contained and
	// nothing here can, or uncontained because the person turned that off.
	// It is its own line because a writer's rule is not the session's —
	// a session running its own commands unconfined still refuses a
	// writer's by default. Empty says nothing.
	// See docs/capabilities/containment.md#containment-can-be-required.
	Writers string
	// Manage handles the /sandbox subcommands (doctor, list, status,
	// destroy, prune) for container sandboxes and returns the text to
	// show. Nil means container sandbox management is not wired up.
	Manage func(args []string) string
	// Wrap is the argv one command line runs as under this session's
	// containment, for the callers that have to build the process
	// themselves — a hook, which is a command with a payload on its stdin
	// and no way through the runner that captures one. Nil is a session
	// running its commands bare, and such a caller then runs bare too: it is
	// contained exactly as much as the assistant's own commands are, which
	// is the whole rule.
	// See docs/capabilities/hooks.md#a-hook-is-a-command-like-any-other.
	Wrap func(command string) ([]string, error)
	// Toolchain is what the checkout's toolchain declaration names and this
	// session's PATH lacks, and the install the person can be offered for it
	// (toolchain.go). It rides here because both halves are a reading of the
	// containment: missing is judged against the PATH a contained command is
	// handed, and the install runs under the same wall the assistant's
	// commands do. The zero value is a checkout that declares nothing.
	Toolchain Toolchain
}

// inContainer says the session's commands run in a sandbox container, so the
// reader's own run in the same place the assistant's do.
func (c Containment) inContainer() bool {
	return c.Mechanism == ContainerSandbox && (c.TailRun != nil || c.Run != nil)
}

// ranInContainer is the word a reader's own command row carries in its outcome
// when it ran in the sandbox container rather than on this machine.
const ranInContainer = "container"

// containmentRefusal is the refusal an action gets before it is drawn, or ""
// when this session runs it. It is asked of the actions that run a command —
// execute_command and a process start, which is the whole of what the
// requirement is about; /run carries no request here and is never refused.
//
// It asks by the tool rather than by whether the request carries a command
// line: a git write carries one only for the deny list to match, and it is
// not a command the assistant wrote. What it runs besides git is the
// checkout's own hooks, which the checkout's trust answer decides, so it is
// put to the card at the write tier like any other write. The unattended
// surfaces ask the same two names (unattendedHooks in internal/cli).
// See docs/capabilities/containment.md#a-git-write-is-not-a-command.
func (m Model) containmentRefusal(req *approvalRequest) string {
	return m.rules().Contained(ruleCall(req))
}

// containmentStatus is the containment line `/status` prints: what is
// containing the assistant's commands, in the words the card's chip uses, or
// that nothing is and why. A session with no containment wiring says nothing
// rather than claiming either state.
func (m Model) containmentStatus() string {
	if m.containment.Status == "" {
		return ""
	}
	line := m.sessionContainmentStatus()
	if m.containment.Writers != "" {
		line += "\n" + m.containment.Writers
	}
	return line
}

// sessionContainmentStatus is the session's own half of containmentStatus.
func (m Model) sessionContainmentStatus() string {
	if m.containment.Mechanism == "" {
		if m.containment.Refusal != "" {
			// The session was told to require one, so "unconfined" is not
			// the whole answer: nothing of the assistant's is going to run.
			return "Containment\nrequired, and none is in force — the assistant's commands are refused\n" +
				uncontainedDetail(m.containment.Detail)
		}
		return "Containment\nunconfined — " + uncontainedDetail(m.containment.Detail)
	}
	words := m.containmentWords(m.containment.Mechanism)
	if m.inSandbox() {
		// The container and the image it was made from, after the words a
		// card's chip shares: a reader asking what is containing the work is
		// owed which container.
		if w := m.sandboxEngineWords(); w != "" {
			words += " · " + w
		}
	}
	return "Containment\n" + words
}

// sandboxChip is the word the title rail ends in while the session's commands
// run in a container: a glyph and a word, so a terminal with no colour and a
// reader who cannot tell the colours apart read the same fact.
// See docs/interface/principles.md#colour-never-carries-meaning-alone.
const sandboxChip = "⛨ sandbox"

// inSandbox says the containment in force is a container sandbox. It reads
// the mechanism the host filled in once a container was made, never the flag
// that asked for one, so a session whose container could not be made cannot
// draw the indicator.
// See docs/capabilities/containment.md#what-is-reported-is-what-is-in-force.
func (m Model) inSandbox() bool {
	return m.containment.Mechanism == ContainerSandbox
}

// sandboxEngineWords is the engine and the image's short digest in one clause,
// leaving out whichever the host did not name.
func (m Model) sandboxEngineWords() string {
	var parts []string
	if m.containment.Engine != "" {
		parts = append(parts, m.containment.Engine)
	}
	if m.containment.Image != "" {
		parts = append(parts, "image "+m.containment.Image)
	}
	return strings.Join(parts, ", ")
}

// sandboxNote is the start screen's row for a session in a container: the
// mechanism, the profile, whether the network is open, then the engine and
// the image. It is false for a session not in one, which draws no row.
func (m Model) sandboxNote() (components.StartNote, bool) {
	if !m.inSandbox() {
		return components.StartNote{}, false
	}
	net := "no network"
	if m.containment.Network {
		net = "network open"
	}
	parts := []string{"container"}
	if m.containment.Profile != "" {
		parts = append(parts, m.containment.Profile)
	}
	parts = append(parts, net)
	if w := m.sandboxEngineWords(); w != "" {
		parts = append(parts, w)
	}
	return components.StartNote{Label: "sandbox", Value: strings.Join(parts, " · ")}, true
}

// containmentWords is the mechanism and the profile in one clause, with the
// requirement in front of it where there is one. It is one function so the
// chip on a card and the line `/status` prints cannot come to disagree — and
// it takes the mechanism rather than reading it, because a card names the
// path that will run its own action rather than the one beside it.
func (m Model) containmentWords(mechanism string) string {
	words := mechanism
	if m.containment.Required {
		words = "required · " + words
	}
	if m.containment.Profile != "" {
		words += " · " + m.containment.Profile
	}
	if m.containment.GitStore != "" {
		words += " · " + m.containment.GitStore
	}
	return words
}
