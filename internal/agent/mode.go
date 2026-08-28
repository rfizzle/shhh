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
	// ModeAuto defers to policy: edits apply and allowlisted commands run;
	// anything else is judged by the LLM classifier (S-060) when configured,
	// else asks. Classifier failures fall back to asking, never allowing.
	ModeAuto
	// ModePlan is read-only research (S-061): file edits and non-inspection
	// commands are refused with a result telling the model it is in plan
	// mode; shell access is restricted to the inspection allowlist.
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

// Describe is the one-line explanation of a mode shown by /permissions and /help.
func (m Mode) Describe() string {
	switch m {
	case ModeAcceptEdits:
		return "file edits apply without prompts; commands and other actions ask"
	case ModeAuto:
		return "edits apply; allowlisted commands run; the classifier judges the rest (or asks)"
	case ModePlan:
		return "read-only research — edits and non-inspection commands are refused"
	default:
		return "every consequential tool call asks"
	}
}

// ParseMode maps a config or /permissions name to its Mode.
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

// permissiveness ranks modes for ClampMode: plan is the most restrictive,
// auto the most permissive.
func permissiveness(m Mode) int {
	switch m {
	case ModePlan:
		return 0
	case ModeAcceptEdits:
		return 2
	case ModeAuto:
		return 3
	default: // ModeManual
		return 1
	}
}

// ClampMode caps mode at ceiling: a sub-agent can never run in a more
// permissive mode than its parent (S-068).
func ClampMode(mode, ceiling Mode) Mode {
	if permissiveness(mode) > permissiveness(ceiling) {
		return ceiling
	}
	return mode
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
	// Path is the file for ActionEdit, which is what a directory-scoped edit
	// grant is matched against (S-054, GrantPrefix's counterpart).
	Path string
	// SafetyFlagged marks commands flagged by safety.Check; they always ask
	// the human, in every mode except plan (which refuses them outright).
	SafetyFlagged bool
	// OutOfScope names the directories this action reaches that are outside
	// the session's working scope (S-141). It is resolved by the front-end,
	// which holds the scope; here it is one more thing that stops a
	// permissive mode answering on the user's behalf, because a mode that
	// says "edits apply" was granted over the work, not over the whole disk.
	OutOfScope []string
	// ScopeSensitive marks an out-of-scope directory that only a person may
	// grant — a home directory, a system root, another tool's credential
	// store. Like SafetyFlagged it always asks, in every mode.
	ScopeSensitive bool
	// ScopeRefused marks a path behind the containment deny mask, which no
	// grant can reach. The call is refused rather than asked about: the
	// answer would not have been honoured.
	ScopeRefused bool
	// ScopeReason is why the scope fields say what they do, in the words the
	// card and the tool result print after a dash.
	ScopeReason string
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

// scopeRefusedReason states why a call was refused for what it reaches, in
// the words the transcript and the tool result both use.
func scopeRefusedReason(a Action) string {
	if a.ScopeReason != "" {
		return "outside the working scope — " + a.ScopeReason
	}
	return "outside the working scope, behind the containment deny mask"
}

// ScopeRefusedResult is the tool result recorded for a call refused for the
// paths it reaches, so the model learns the boundary instead of retrying it.
func ScopeRefusedResult(reason string) string {
	return "error: this path is outside the session's working scope and cannot be granted (" + reason +
		"). Work inside the session's directories, or ask the user to run /add-dir for a directory that can be granted."
}

// PlanModeResult is the tool result recorded for a gated call refused in
// plan mode, so the model learns why nothing ran instead of the call being
// silently dropped.
const PlanModeResult = "error: this session is in plan mode (read-only); the call was not executed. Present your plan as a message, or ask the user to switch modes (Shift+Tab or /permissions)."

// ModePolicy is the session approval-policy state: the active mode plus the
// S-054 internals (per-category session grants and the config command
// allowlist) that manual and accept-edits build on.
type ModePolicy struct {
	Mode Mode
	// AllowEdits and AllowCommands are the blanket session grants, which
	// `/permissions allow` sets: every edit, every command, until they are revoked.
	AllowEdits    bool
	AllowCommands bool
	// EditDirs are the scoped edit grants [a] records on an edit card: edits
	// under these directories run without asking, and nothing else does.
	EditDirs []string
	// CommandAllowlist entries pre-approve matching commands — the config
	// list, plus whatever [a] has recorded on a command card this session.
	CommandAllowlist []string
	// ReadOnlyExtra extends the built-in read-only command allowlist
	// (behavior.read_only_commands).
	ReadOnlyExtra []string
	// ReadOnlyDisabled turns off the built-in read-only allowlist, so
	// inspection commands prompt like anything else
	// (behavior.read_only_auto = false).
	ReadOnlyDisabled bool
}

// readOnly reports whether a command auto-runs as pure inspection.
func (p ModePolicy) readOnly(a Action) bool {
	if p.ReadOnlyDisabled || a.Kind != ActionCommand || a.SafetyFlagged {
		return false
	}
	return ReadOnlyAllowed(a.Command, p.ReadOnlyExtra)
}

// ReadOnlyCommands is the built-in allowlist of inspection commands that run
// without a prompt in every mode: pure reads, nothing that can write, delete,
// or execute further commands or code. Matching goes through AllowlistMatches,
// so chained or redirected commands never qualify, and a safety-flagged
// command is never matched against it at all.
//
// Entries are deliberately conservative. Anything that compiles or runs
// project code (go build, go test, go vet, make, npm run) stays out: it
// executes the repository's own code, which is not a read.
func ReadOnlyCommands() []string {
	return []string{
		// Filesystem inspection.
		"ls", "pwd", "cat", "head", "tail", "wc", "file", "stat", "tree", "du",
		"realpath", "basename", "dirname", "readlink",
		// Search.
		"grep", "rg", "find", "fd", "which", "type",
		// Text shaping over piped-in content is out (it needs a pipe, which
		// AllowlistMatches rejects anyway); these are read-only on their own.
		"diff", "cmp", "sort", "uniq", "cut", "column",
		// Git inspection.
		"git status", "git log", "git diff", "git show", "git blame",
		"git ls-files", "git branch", "git remote -v", "git describe",
		"git rev-parse", "git shortlog", "git tag -l", "git stash list",
		// Toolchain inspection (no compilation, no execution).
		"go version", "go env", "go list", "go doc", "go mod graph", "go mod why",
		"node --version", "npm ls", "python --version", "python3 --version",
		"cargo --version", "rustc --version",
		// Environment.
		"whoami", "hostname", "uname", "date", "env", "printenv", "id",
	}
}

// PlanInspectionCommands is the read-only allowlist under its plan-mode name
// (S-061); plan mode grants exactly the same set.
func PlanInspectionCommands() []string { return ReadOnlyCommands() }

// readOnlyGuards names the flags that turn an otherwise read-only command
// into one that writes, deletes, or executes something else. A command whose
// prefix matches a key and that carries any of its flags is not read-only,
// however innocent the rest of it looks ("find . -delete").
var readOnlyGuards = map[string][]string{
	"find":       {"-delete", "-exec", "-execdir", "-ok", "-okdir", "-fls", "-fprint", "-fprint0", "-fprintf"},
	"fd":         {"-x", "-X", "--exec", "--exec-batch"},
	"sort":       {"-o", "--output"},
	"tree":       {"-o"},
	"git branch": {"-d", "-D", "-m", "-M", "-c", "-C", "--delete", "--move", "--copy", "--set-upstream-to", "-u", "--unset-upstream", "--edit-description"},
	// env with operands runs a command; bare env just prints the environment.
	"env": {},
}

// guardedReadOnly applies readOnlyGuards to a command already known to match
// the built-in allowlist.
func guardedReadOnly(command string) bool {
	words := strings.Fields(command)
	for prefix, banned := range readOnlyGuards {
		pattern := strings.Fields(prefix)
		if len(pattern) > len(words) {
			continue
		}
		match := true
		for i, w := range pattern {
			if words[i] != w {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		rest := words[len(pattern):]
		// A guard with no banned flags allows only the bare command.
		if len(banned) == 0 && len(rest) > 0 {
			return false
		}
		for _, w := range rest {
			for _, b := range banned {
				if w == b || strings.HasPrefix(w, b+"=") {
					return false
				}
			}
		}
	}
	return true
}

// ReadOnlyAllowed reports whether a command is a built-in read-only
// inspection command, or matches one of the caller's extra entries. Extra
// entries are the user's own call and skip the built-in flag guards.
func ReadOnlyAllowed(command string, extra []string) bool {
	if AllowlistMatches(ReadOnlyCommands(), command) && guardedReadOnly(command) {
		return true
	}
	return len(extra) > 0 && AllowlistMatches(extra, command)
}

// PlanInspectionAllowed reports whether a command is on plan mode's
// inspection allowlist.
func PlanInspectionAllowed(command string) bool {
	return ReadOnlyAllowed(command, nil)
}

// Decide returns the verdict for one gated action and, for Allow, the reason
// shown in the transcript ("session policy", "allowlist", "auto mode", …).
func (p ModePolicy) Decide(a Action) (Decision, string) {
	if p.Mode == ModePlan {
		// Plan mode grants inspection even with the read-only allowlist
		// disabled: read-only is the whole point of the mode.
		if a.Kind == ActionCommand && !a.SafetyFlagged && ReadOnlyAllowed(a.Command, p.ReadOnlyExtra) {
			return Allow, "plan mode inspection"
		}
		return Deny, "plan mode"
	}
	if a.SafetyFlagged {
		return Ask, ""
	}
	// The working scope (S-141) is checked before the mode is: a path behind
	// the deny mask is refused whatever the mode says, and a directory
	// outside the scope is a decision the session has not been given — the
	// permissive modes were granted over the work, and this is the question
	// of what the work is.
	if a.ScopeRefused {
		return Deny, scopeRefusedReason(a)
	}
	if len(a.OutOfScope) > 0 {
		return Ask, ""
	}
	// Inspection commands never prompt, in any mode: they cannot change
	// anything, and prompting for them is the bulk of the noise.
	if p.readOnly(a) {
		return Allow, "read-only"
	}
	switch a.Kind {
	case ActionEdit:
		switch {
		case p.Mode == ModeAcceptEdits || p.Mode == ModeAuto:
			return Allow, p.Mode.String() + " mode"
		case p.AllowEdits:
			return Allow, "session policy"
		case PathUnder(p.EditDirs, a.Path):
			return Allow, "session grant"
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
