package agent

// Permission modes (S-059): a session-level state machine that decides how
// approval-gated tool calls are handled. Read-only tools never reach this
// layer — they auto-run in every mode — and safety-flagged commands always
// ask the human regardless of mode.

import (
	"fmt"
	"strings"
)

// Mode is the session's permission mode.
type Mode int

const (
	// ModeManual prompts for every consequential tool call (the maximally
	// safe default). Session grants ([a] on a prompt) and the config command
	// allowlist can loosen it per category (S-054).
	ModeManual Mode = iota
	// ModeAcceptEdits auto-allows file edits; commands and other external
	// actions still prompt.
	ModeAcceptEdits
	// ModeAuto defers to policy: the LLM classifier when enabled (S-060),
	// else allowlist rules — edits apply, allowlisted commands run, anything
	// else asks.
	ModeAuto
	// ModePlan is read-only: gated calls are refused with a result telling
	// the model it is in plan mode (the full planning flow is S-061).
	ModePlan
)

func (m Mode) String() string {
	switch m {
	case ModeAcceptEdits:
		return "accept-edits"
	case ModeAuto:
		return "auto"
	case ModePlan:
		return "plan"
	default:
		return "manual"
	}
}

// Describe is the one-line explanation of a mode shown by /mode and /help.
func (m Mode) Describe() string {
	switch m {
	case ModeAcceptEdits:
		return "file edits apply without prompts; commands and other actions ask"
	case ModeAuto:
		return "edits apply; allowlisted commands run; anything else asks"
	case ModePlan:
		return "read-only — file edits and commands are refused"
	default:
		return "every consequential tool call asks"
	}
}

// ParseMode maps a config or /mode name to its Mode.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "manual":
		return ModeManual, nil
	case "accept-edits", "accept_edits", "accept edits":
		return ModeAcceptEdits, nil
	case "auto":
		return ModeAuto, nil
	case "plan":
		return ModePlan, nil
	}
	return ModeManual, fmt.Errorf("unknown mode %q (valid: manual, accept-edits, auto, plan)", s)
}

// DefaultCycle is the Shift+Tab mode order when the config does not override
// it (behavior.mode_cycle).
func DefaultCycle() []Mode {
	return []Mode{ModeManual, ModeAcceptEdits, ModeAuto, ModePlan}
}

// ParseCycle parses behavior.mode_cycle entries; an empty list means the
// default cycle.
func ParseCycle(names []string) ([]Mode, error) {
	if len(names) == 0 {
		return nil, nil
	}
	cycle := make([]Mode, 0, len(names))
	for _, name := range names {
		mode, err := ParseMode(name)
		if err != nil {
			return nil, err
		}
		cycle = append(cycle, mode)
	}
	return cycle, nil
}

// NextMode returns the mode after current in cycle, wrapping around; a nil
// cycle uses DefaultCycle, and a current mode outside the cycle enters it at
// the start.
func NextMode(cycle []Mode, current Mode) Mode {
	if len(cycle) == 0 {
		cycle = DefaultCycle()
	}
	for i, m := range cycle {
		if m == current {
			return cycle[(i+1)%len(cycle)]
		}
	}
	return cycle[0]
}

// ActionKind classifies an approval-gated tool call for mode decisions.
type ActionKind int

const (
	// ActionEdit is a file write/edit.
	ActionEdit ActionKind = iota
	// ActionCommand is a shell command.
	ActionCommand
	// ActionOther is any other gated tool call (registered gated tools).
	ActionOther
)

// Action is one approval-gated tool call as the mode policy sees it.
type Action struct {
	Kind ActionKind
	// Command is the command text for ActionCommand (allowlist matching).
	Command string
	// SafetyFlagged marks commands flagged by safety.Check; they always ask
	// the human, in every mode except plan (which refuses them outright).
	SafetyFlagged bool
}

// Decision is a mode policy verdict for one gated tool call.
type Decision int

const (
	// Ask prompts the user (the fallback whenever nothing allows or denies).
	Ask Decision = iota
	// Allow runs the call without a prompt.
	Allow
	// Deny refuses the call with an error tool result, without prompting.
	Deny
)

// PlanModeResult is the tool result recorded for a gated call refused in
// plan mode, so the model learns why nothing ran instead of the call being
// silently dropped.
const PlanModeResult = "error: this session is in plan mode (read-only); the call was not executed. Present your plan as a message, or ask the user to switch modes (Shift+Tab or /mode)."

// ModePolicy is the session approval-policy state: the active mode plus the
// S-054 internals (per-category session grants and the config command
// allowlist) that manual and accept-edits build on.
type ModePolicy struct {
	Mode Mode
	// AllowEdits and AllowCommands are the [a] session grants.
	AllowEdits    bool
	AllowCommands bool
	// CommandAllowlist entries pre-approve matching commands (config).
	CommandAllowlist []string
}

// Decide returns the verdict for one gated action and, for Allow, the reason
// shown in the transcript ("session policy", "allowlist", "auto mode", …).
func (p ModePolicy) Decide(a Action) (Decision, string) {
	if p.Mode == ModePlan {
		return Deny, "plan mode"
	}
	if a.SafetyFlagged {
		return Ask, ""
	}
	switch a.Kind {
	case ActionEdit:
		switch {
		case p.Mode == ModeAcceptEdits || p.Mode == ModeAuto:
			return Allow, p.Mode.String() + " mode"
		case p.AllowEdits:
			return Allow, "session policy"
		}
	case ActionCommand:
		switch {
		case p.AllowCommands:
			return Allow, "session policy"
		case AllowlistMatches(p.CommandAllowlist, a.Command):
			return Allow, "allowlist"
		}
	}
	return Ask, ""
}
