package config

// One declaration per setting, and every surface reads from it. A key used to
// exist in three places that had to be edited together — the struct field, a
// case in a switch that parsed it, and a row somewhere that showed it — and
// the third was skipped often enough that most of the file was reachable only
// by opening the file. Here a key is a row in the table below, its parser
// comes from the type of the field it names, and the screen, the listing and
// the reference section in the documentation are all renderings of the same
// rows (docs/capabilities/configuration.md#every-setting).
//
// What is deliberately not here: the vocabularies. A permission mode, a
// reasoning level and a containment profile are words the packages that own
// them define, and this package imports none of them. The words are carried
// as strings so a reader and a picker can see them; the judge that turns one
// into a mode stays with its owner, and the surface that writes a value asks
// it before calling Set.

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Kind is the shape a setting's value takes, for the surfaces that have to
// decide how to show it and what to offer instead of a field to type into.
// The parse itself comes from the Go type of the field the key names, which
// cannot disagree with the file; Kind is the reader-facing half of the same
// fact, and a test holds the two together.
type Kind int

const (
	// KindString is free text — a model name, a shell, a colour.
	KindString Kind = iota
	// KindInt is a whole number. See Setting.Signed for what a negative
	// means.
	KindInt
	// KindBool is on or off.
	KindBool
	// KindList is a comma-separated list at the command line and an array in
	// the file.
	KindList
	// KindEnum is one of a closed set of words, which Setting.Values names.
	KindEnum
	// KindPath names a file on disk.
	KindPath
)

// String is the shape in the words a reader reads it in, which is what the
// reference section prints and what the listing carries in its JSON.
func (k Kind) String() string {
	switch k {
	case KindInt:
		return "number"
	case KindBool:
		return "true/false"
	case KindList:
		return "list"
	case KindEnum:
		return "word"
	case KindPath:
		return "path"
	}
	return "text"
}

// Setting is one key: where it lives, what it takes, what stands when nothing
// sets it, and the sentence that says why anyone would touch it.
type Setting struct {
	// Key is the key as the file spells it and as `config set` takes it,
	// dotted from the table down. `agents.profiles.*.model` is the one key
	// with a wildcard: the segment is a role name the person chooses.
	Key  string
	Kind Kind
	// Values are the words a KindEnum takes, and the words a KindList's
	// entries take where those are a closed set too. A key with values is a
	// key some other package owns the vocabulary of, so a surface that
	// writes one must judge it against that owner rather than against this
	// list — the list is what a reader and a picker are shown.
	Values []string
	// Default is the built-in answer in the words a reader reads, not a
	// literal: `90 days`, `on`, `(the provider's own)`. It is what the config
	// screen shows where nothing is set and what the reference section
	// prints, so the two cannot disagree about what this machine does.
	Default string
	// Desc is one sentence saying what the key decides. The struct field's
	// own comment says why the setting is shaped the way it is, which is a
	// different question and belongs beside the field.
	Desc string
	// Signed says a negative is an answer this key has a meaning for — no
	// round cap, no command timeout, an interval that never widens. Without
	// it a negative is refused rather than stored, because everywhere else it
	// is a ceiling nothing can satisfy: a setting that looks present and
	// turns its feature off.
	Signed bool
	// Secret marks a value that must never be echoed. The screen masks it and
	// the listing prints whether it is set rather than what it is.
	Secret bool
	// Env is the environment variable that outranks the file for this key,
	// and Flag the command-line flag that outranks both. Five keys have a
	// rank above the file and the rest are the file or the default
	// (docs/capabilities/configuration.md#one-file-one-format-one-resolution-order).
	Env  string
	Flag string
}

// Group is the file table the key sits in — `provider`, `behavior`,
// `sandbox`. It is the key's first segment rather than a field of its own,
// because a group that could disagree with the key would be a second place to
// be wrong.
func (s Setting) Group() string {
	group, _, _ := strings.Cut(s.Key, ".")
	return group
}

// RoleWildcard is the segment a per-role key leaves to the person: any role
// name may take it, so the table declares the shape once instead of naming
// the built-in roles and going stale when a fourth one lands.
const RoleWildcard = "*"

// settings is every key, in the order the file's own tables run. The order is
// the reference section's order and the config screen's order, so a reader
// who has scrolled one has scrolled the other.
var settings = []Setting{
	{
		Key: "provider.default", Kind: KindString, Default: "openai",
		Env: "SHHH_PROVIDER", Flag: "--provider",
		Desc: "Which provider a request goes to: a built-in one, or a gateway profile from `shhh providers`.",
	}, {
		Key: "provider.model", Kind: KindString, Default: "(the provider's own default)",
		Env: "SHHH_MODEL", Flag: "--model",
		Desc: "The model a session runs on.",
	}, {
		Key: "provider.api_key", Kind: KindString, Default: "(from the environment)", Secret: true,
		Env: "SHHH_API_KEY", Flag: "--api-key",
		Desc: "The provider key, for a machine where it cannot come from the environment.",
	}, {
		Key: "provider.base_url", Kind: KindString, Default: "(the provider's own)",
		Env:  "SHHH_BASE_URL",
		Desc: "Where the provider's API is, for a gateway or a self-hosted endpoint.",
	}, {
		Key: "provider.name", Kind: KindString, Default: "(the provider's own)",
		Desc: "What the provider is called on screen, for a gateway that fronts several.",
	}, {
		Key: "provider.reasoning", Kind: KindEnum, Default: "medium",
		Values: []string{"off", "low", "medium", "high", "xhigh", "max"},
		Env:    "SHHH_REASONING", Flag: "--reasoning",
		Desc: "How hard the model thinks before it answers; the level is fitted to each model, so a rung it lacks lowers to the one it has.",
	}, {
		Key: "provider.cache_ttl", Kind: KindEnum, Default: "1h",
		Values: []string{"5m", "1h"},
		Desc:   "How long the opening a session repeats every round stays cached between rounds.",
	},

	{
		Key: "behavior.silent_mode", Kind: KindBool, Default: "off",
		Desc: "Print the generated command and nothing else, for a shell that pipes it somewhere.",
	}, {
		Key: "behavior.shell", Kind: KindString, Default: "(your login shell)",
		Desc: "The shell commands are run through.",
	}, {
		Key: "behavior.context_max_tokens", Kind: KindInt, Default: "8000 tokens",
		Desc: "The token budget for the shell context a generated command is written against.",
	}, {
		Key: "behavior.max_tool_rounds", Kind: KindInt, Signed: true, Default: "150",
		Desc: "How many consecutive tool rounds one turn may take; a negative removes the cap for every run in scope.",
	}, {
		Key: "behavior.tree_check", Kind: KindBool, Default: "on",
		Desc: "Tell a turn when the working tree moved in a way its own edits do not explain.",
	}, {
		Key: "behavior.command_timeout_seconds", Kind: KindInt, Signed: true, Default: "600",
		Desc: "How long one command the assistant runs may take before it is cancelled; a negative removes the ceiling.",
	}, {
		Key: "behavior.safety_warnings", Kind: KindBool, Default: "on",
		Desc: "Say what a destructive command will do before it is approved.",
	}, {
		Key: "behavior.system_prompt_extra", Kind: KindString, Default: "(nothing)",
		Desc: "Text appended to every system prompt.",
	}, {
		Key: "behavior.command_allowlist", Kind: KindList, Default: "(empty — every command asks)",
		Desc: "Command prefixes that auto-approve in a session; a safety-flagged command always asks anyway.",
	}, {
		Key: "behavior.read_only_commands", Kind: KindList, Default: "(the built-in inspection list alone)",
		Desc: "Commands added to the built-in inspection list that runs without asking; entries skip the built-in flag guards.",
	}, {
		Key: "behavior.read_only_auto", Kind: KindBool, Default: "on",
		Desc: "Run the built-in inspection list without asking; off makes a read prompt like anything else.",
	}, {
		Key: "behavior.scope_dirs", Kind: KindList, Default: "(the directory the session opened in)",
		Desc: "Directories added to a session's working scope at start, beside the one it was opened in.",
	}, {
		Key: "behavior.default_mode", Kind: KindEnum, Default: "manual",
		Values: []string{"manual", "accept-edits", "auto", "plan"},
		Desc:   "The permission mode a session starts in.",
	}, {
		Key: "behavior.mode_cycle", Kind: KindList, Default: "manual, accept-edits, auto, plan",
		Values: []string{"manual", "accept-edits", "auto", "plan"},
		Desc:   "The order the mode key walks the permission modes in.",
	}, {
		Key: "behavior.classifier_model", Kind: KindString, Default: "(the provider's small model, or the session's own)",
		Desc: "The model auto mode's permission classifier runs on.",
	}, {
		Key: "behavior.classifier_timeout_seconds", Kind: KindInt, Default: "30",
		Desc: "How long one classifier request may take.",
	}, {
		Key: "behavior.classifier_max_tokens", Kind: KindInt, Default: "8192",
		Desc: "The ceiling on a classifier response, the reasoning it does before answering included.",
	}, {
		Key: "behavior.classifier_retries", Kind: KindInt, Default: "1",
		Desc: "How many extra attempts an invalid or failed classifier response gets before it fails closed.",
	}, {
		Key: "behavior.memory_disabled", Kind: KindBool, Default: "off",
		Desc: "Turn durable memory off: nothing is injected and the remember tool is not registered.",
	}, {
		Key: "behavior.memory_max_entries", Kind: KindInt, Default: "20",
		Desc: "How many memories are injected into one session's system prompt.",
	}, {
		Key: "behavior.memory_max_tokens", Kind: KindInt, Default: "1200",
		Desc: "The token budget for the injected memory block.",
	}, {
		Key: "behavior.check_in_interval_rounds", Kind: KindInt, Signed: true, Default: "40 rounds",
		Desc: "How many tool rounds pass before a turn is asked to take stock.",
	}, {
		Key: "behavior.check_in_max_doublings", Kind: KindInt, Signed: true, Default: "2 doublings",
		Desc: "How far that interval widens over one turn; a negative fixes it, so a long turn is asked at the same rate throughout.",
	}, {
		Key: "behavior.provider_retries", Kind: KindInt, Default: "3 attempts",
		Desc: "How many times one stall — a rate limit, an overloaded provider, a connection that died before a token — is asked again before the failure stands; zero is a machine that would rather see the failure than sit out a wait.",
	},

	{
		Key: "sandbox.profile", Kind: KindEnum, Default: "workspace",
		Values: []string{"workspace", "workspace-netless"},
		Desc:   "What a contained command may reach: the workspace with the network untouched, or the same with the network closed.",
	}, {
		Key: "sandbox.deny_extra", Kind: KindList, Default: "(the built-in deny mask alone)",
		Desc: "Paths added to the built-in deny mask; a contained command sees them as empty.",
	}, {
		Key: "sandbox.write_extra", Kind: KindList, Default: "(the workspace, scratch and the toolchain caches)",
		Desc: "Paths writable inside containment, beside the workspace.",
	}, {
		Key: "sandbox.container_engine", Kind: KindEnum, Default: "(auto-detected, a rootless engine first)",
		Values: []string{"podman", "docker"},
		Desc:   "Which engine runs a container sandbox.",
	}, {
		Key: "sandbox.container_image", Kind: KindString, Default: "(unset — container sandboxes are unavailable)",
		Desc: "The digest-pinned image (name@sha256:…) a sandbox container runs.",
	}, {
		Key: "sandbox.image_allowlist", Kind: KindList, Default: "(any digest-pinned image)",
		Desc: "The only sandbox images that may run, as digest-pinned references.",
	}, {
		Key: "sandbox.container_memory", Kind: KindString, Default: "2g",
		Desc: "The memory ceiling on a sandbox container.",
	}, {
		Key: "sandbox.container_cpus", Kind: KindString, Default: "2",
		Desc: "The CPU ceiling on a sandbox container.",
	}, {
		Key: "sandbox.container_pids", Kind: KindInt, Default: "256",
		Desc: "The process ceiling inside a sandbox container.",
	}, {
		Key: "sandbox.container_ttl_hours", Kind: KindInt, Default: "24",
		Desc: "How long a sandbox container may live before startup reconciliation reaps it.",
	}, {
		Key: "sandbox.require_isolation", Kind: KindEnum, Default: "(none required)",
		Values: []string{"process", "container", "vm"},
		Desc:   "Refuse to create a sandbox below this verified level; a requirement that cannot be verified fails rather than downgrading.",
	},

	{
		Key: "web.allow_private", Kind: KindBool, Default: "off",
		Desc: "Let a fetch reach private, loopback, link-local and CGNAT addresses, and lift the 80/443 port list; cloud metadata stays blocked either way.",
	}, {
		Key: "web.fetch_max_bytes", Kind: KindInt, Default: "2 MiB",
		Desc: "The download ceiling on one fetch.",
	}, {
		Key: "web.fetch_timeout_seconds", Kind: KindInt, Default: "30",
		Desc: "How long one fetch may take, redirects and the body read included.",
	}, {
		Key: "web.cache_ttl_minutes", Kind: KindInt, Default: "60",
		Desc: "How long a cached response stays fresh.",
	}, {
		Key: "web.search_provider", Kind: KindEnum, Default: "brave",
		Values: []string{"brave"},
		Desc:   "Which backend the web_search tool asks.",
	}, {
		Key: "web.search_api_key", Kind: KindString, Default: "(unset — web_search is not registered)", Secret: true,
		Desc: "The search backend's key; without it the tool does not exist.",
	},

	{
		Key: "lsp.disabled", Kind: KindBool, Default: "off",
		Desc: "Turn the language-server integration off: no servers, no navigation tools, no diagnostics.",
	}, {
		Key: "lsp.request_timeout_seconds", Kind: KindInt, Default: "15",
		Desc: "How long one language-server request may take, the initialize handshake included.",
	}, {
		Key: "lsp.diagnostics_timeout_seconds", Kind: KindInt, Default: "3",
		Desc: "How long an applied edit waits for the server to re-check the file; a check that lands later rides with the next tool result rather than being dropped.",
	},

	{
		Key: "appearance.accent_color", Kind: KindString, Default: "(the palette's own)",
		Desc: "The accent the surfaces are painted with.",
	}, {
		Key: "appearance.mouse", Kind: KindBool, Default: "on",
		Desc: "Terminal mouse reporting: the wheel scrolls the transcript and shhh selects text itself. Off leaves the terminal its native click-drag selection.",
	}, {
		Key: "appearance.notify", Kind: KindBool, Default: "on",
		Desc: "Raise a desktop notification when a turn stops while the window is not the one in front.",
	}, {
		Key: "appearance.window_title", Kind: KindBool, Default: "on",
		Desc: "Name the terminal's own tab after the session.",
	}, {
		Key: "appearance.paste_lines", Kind: KindInt, Signed: true, Default: "10 lines",
		Desc: "The height past which a paste is staged as an attachment instead of typed into the draft; a negative turns that half of the test off.",
	}, {
		Key: "appearance.paste_columns", Kind: KindInt, Signed: true, Default: "1000 columns",
		Desc: "The width past which a paste is staged as an attachment; a negative turns that half of the test off.",
	}, {
		Key: "appearance.rail_width", Kind: KindString, Default: "auto",
		Desc: "How many columns the inspector rail takes: `auto`, which widens the rail with the terminal, or a column count for a pane you chose the size of.",
	},

	{
		Key: "history.retention_days", Kind: KindInt, Default: "90 days",
		Desc: "How long a recorded session is kept before startup prunes it.",
	},

	{
		Key: "reports.retention_days", Kind: KindInt, Default: "90 days",
		Desc: "How long a generated report page is kept.",
	},

	{
		Key: "agents.model", Kind: KindString, Default: "inherit",
		Desc: "The model every sub-agent runs, unless its role says otherwise; `inherit` is the session's own.",
	}, {
		Key: "agents.profiles." + RoleWildcard + ".model", Kind: KindString, Default: "(the sub-agent model)",
		Desc: "The model one role runs — the role is the key's own segment, so any role a spawn names can have one.",
	}, {
		Key: "agents.max_concurrent", Kind: KindInt, Default: "3",
		Desc: "How many children may run at once; further spawns queue.",
	},

	{
		Key: "summary.model", Kind: KindString, Default: "(the provider's small model, or the session's own)",
		Desc: "The model that takes the periodic reading of the session.",
	}, {
		Key: "summary.interval_rounds", Kind: KindInt, Default: "10",
		Desc: "How many tool rounds pass between two readings; higher is cheaper and staler.",
	}, {
		Key: "summary.min_gap_seconds", Kind: KindInt, Default: "20",
		Desc: "The floor on wall-clock time between two readings, so a burst of fast rounds cannot rewrite the block repeatedly.",
	}, {
		Key: "summary.timeout_seconds", Kind: KindInt, Default: "20",
		Desc: "How long one reading may take.",
	}, {
		Key: "summary.max_tokens", Kind: KindInt, Default: "8192",
		Desc: "The ceiling on a reading's response, the reasoning it does before answering included.",
	}, {
		Key: "summary.disabled", Kind: KindBool, Default: "off",
		Desc: "Turn the reading off entirely: no requests are made and the rail draws no summary block.",
	}, {
		Key: "summary.headless", Kind: KindBool, Default: "on",
		Desc: "Take readings in a non-interactive run, which is the surface with nobody in front of it.",
	}, {
		Key: "summary.subagents", Kind: KindBool, Default: "off",
		Desc: "Take readings in each spawned child; a fan-out of six is six more readings per interval.",
	}, {
		Key: "summary.intervene_cooldown_intervals", Kind: KindInt, Signed: true, Default: "2 readings",
		Desc: "How many reading intervals pass between two verdict-driven interventions.",
	}, {
		Key: "summary.steer_target_chars", Kind: KindInt, Signed: true, Default: "400 characters",
		Desc: "How much of the instruction a steer quotes back to a drifting turn; a negative quotes it whole.",
	}, {
		Key: "summary.title", Kind: KindBool, Default: "on when a summary model is set, off otherwise",
		Desc: "Ask the summary model to name an unnamed session after its first turn, for the saved-chat listings.",
	},

	{
		Key: "secrets.env", Kind: KindList, Default: "(nothing declared)",
		Desc: "Environment variables to declare as secrets in every session: the model may use the value and never sees it.",
	},

	{
		Key: "mcp.disabled", Kind: KindBool, Default: "off",
		Desc: "Start no MCP server and register no MCP tool, whatever the file defines.",
	}, {
		Key: "mcp.startup_timeout_seconds", Kind: KindInt, Default: "20",
		Desc: "How long each MCP server has to connect and list its tools; one that has not answered is reported and left out.",
	},

	{
		Key: "prompts.steer", Kind: KindPath, Default: "(the built-in wording)",
		Desc: "A file whose contents replace the message a drifting turn is given.",
	}, {
		Key: "prompts.check_in", Kind: KindPath, Default: "(the built-in wording)",
		Desc: "A file whose contents replace the message a turn that has reached its interval is given.",
	}, {
		Key: "prompts.summary", Kind: KindPath, Default: "(the built-in wording)",
		Desc: "A file whose contents replace the reading instruction the summarizing model is sent.",
	}, {
		Key: "prompts.classifier", Kind: KindPath, Default: "(the built-in wording)",
		Desc: "A file whose contents replace the instruction auto mode's permission classifier is sent.",
	},

	{
		Key: "todo.commit", Kind: KindBool, Default: "on",
		Desc: "End a backlog run in a commit; off leaves the change in the working tree, which is the answer for a directory that is not a repository.",
	},
}

// Settings is every key the file holds, in the order the file's tables run.
func Settings() []Setting { return settings }

// Lookup is the setting a key names. A per-role key resolves to the wildcard
// entry with the role filled in, so the caller gets the key it asked about
// rather than the pattern.
func Lookup(key string) (Setting, bool) {
	if strings.Contains(key, RoleWildcard) {
		return Setting{}, false
	}
	for _, s := range settings {
		if s.Key == key {
			return s, true
		}
		if matchWild(s.Key, key) {
			s.Key = key
			return s, true
		}
	}
	return Setting{}, false
}

// matchWild reports whether a concrete key is the pattern with its wildcard
// segment filled in by a name. An empty name is not a name: it would put a
// profile under the empty role, which nothing could ever spawn.
func matchWild(pattern, key string) bool {
	p, k := strings.Split(pattern, "."), strings.Split(key, ".")
	if len(p) != len(k) || !strings.Contains(pattern, RoleWildcard) {
		return false
	}
	for i := range p {
		switch {
		case p[i] == RoleWildcard:
			if k[i] == "" {
				return false
			}
		case p[i] != k[i]:
			return false
		}
	}
	return true
}

// RenamedKey is the key a key that moved is now spelled as, or "" when it did
// not move. The role models were `agents.researcher_model` and are the
// `[agents.profiles.<role>]` table the file has always written them to, now
// that any role can have one; a person who types the old spelling is told the
// new one rather than told it is unknown.
func RenamedKey(key string) string {
	role, ok := strings.CutPrefix(key, "agents.")
	if !ok {
		return ""
	}
	role, ok = strings.CutSuffix(role, "_model")
	if !ok || role == "" || strings.Contains(role, ".") {
		return ""
	}
	return "agents.profiles." + role + ".model"
}

// Nearest is the known key within an edit or two of an unknown one, or "" when
// nothing is close. It is the same offer a misspelled key in the file gets, so
// `config get behaviour.shell` and a file holding it read the same.
func Nearest(key string) string {
	best, bestDist := "", keyDistance(key)+1
	for _, s := range settings {
		name := strings.ReplaceAll(s.Key, RoleWildcard, "<role>")
		if d := editDistance(key, name); d < bestDist || (d == bestDist && name < best) {
			best, bestDist = name, d
		}
	}
	return best
}

// UnknownKeyMessage is the sentence a key no setting reads gets, wherever it
// was typed: the key, and the one it was probably meant to be. A key that was
// renamed says so instead, because "unknown" would be a lie about a setting
// the person still has.
func UnknownKeyMessage(key string) string {
	if to := RenamedKey(key); to != "" {
		return fmt.Sprintf("config key %s: renamed, set %s instead", key, to)
	}
	msg := "unknown config key: " + key
	if near := Nearest(key); near != "" {
		msg += fmt.Sprintf(" (did you mean %q?)", near)
	}
	return msg
}

// Set applies one value, as a person typed it, to the setting a key names.
// The value is parsed for the type the setting has and nothing is written
// when the parse fails: the alternative is a key that reports success and
// holds a number nobody chose, which for a retention key means the next
// startup prunes everything
// (docs/capabilities/configuration.md#a-value-is-refused-before-it-is-written).
//
// The words a value may be — a permission mode, a reasoning level, a
// containment profile — are not judged here. Those vocabularies belong to the
// packages that own them, and this one imports none of them; the surface that
// writes a config value checks them before it calls this.
func Set(cfg *Config, key, value string) error {
	s, ok := Lookup(key)
	if !ok {
		return fmt.Errorf("%s", UnknownKeyMessage(key))
	}
	return atField(reflect.ValueOf(cfg).Elem(), strings.Split(key, "."), func(f reflect.Value) error {
		return s.store(f, key, value)
	})
}

// store parses the value for the type of the field the key names and puts it
// there. The switch is over Go types rather than over keys, which is the
// whole point of the table: a new key of a type already here is one row and
// no new parse, and a key whose type nothing here handles fails loudly at the
// one place that would otherwise have silently ignored it.
//
// The two pointer types keep unset apart from the zero value, which is what
// the keys whose default is not their zero need: a pointer to false is
// `mouse = false`, and a nil is mouse reporting doing whatever it does when
// nobody said.
func (s Setting) store(f reflect.Value, key, value string) error {
	switch p := f.Addr().Interface().(type) {
	case *string:
		*p = value
	case *[]string:
		*p = splitList(value)
	case *bool:
		b, err := boolValue(key, value)
		if err != nil {
			return err
		}
		*p = b
	case **bool:
		b, err := triState(key, value)
		if err != nil {
			return err
		}
		*p = b
	case *int:
		n, err := intValue(key, value, s.Signed)
		if err != nil {
			return err
		}
		*p = n
	case *int64:
		n, err := intValue(key, value, s.Signed)
		if err != nil {
			return err
		}
		*p = int64(n)
	case **int:
		n, err := optionalCount(key, value, s.Signed)
		if err != nil {
			return err
		}
		*p = n
	default:
		return fmt.Errorf("config key %s: no parser for a %s", key, f.Type())
	}
	return nil
}

// ValueError is a value that is not the shape the key it was given to takes.
// It names both, because the two questions a refused write raises are which
// key was refused and what it wanted instead, and an error that answers
// neither leaves the person guessing at a file they cannot see the effect of
// (docs/capabilities/configuration.md#a-value-is-refused-before-it-is-written).
type ValueError struct {
	Key   string
	Value string
	// Want is the shape, phrased to follow "is not": "a whole number",
	// "true or false".
	Want string
}

func (e *ValueError) Error() string {
	return fmt.Sprintf("config key %s: %q is not %s", e.Key, e.Value, e.Want)
}

// intValue parses a number as a person types it; empty is unset. signed says
// the key has a meaning for a negative, and without it one is refused rather
// than stored.
func intValue(key, value string, signed bool) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, &ValueError{Key: key, Value: value, Want: "a whole number"}
	}
	if n < 0 && !signed {
		return 0, &ValueError{Key: key, Value: value, Want: "a whole number, zero or above"}
	}
	return n, nil
}

// boolValue parses a boolean the way Go reads one — true/false, t/f, 1/0 and
// their capitalisations — and refuses everything else. `yes` and `on` are
// among the everything else on purpose: they are words a person reasonably
// types, and a parser that quietly answered false for them is how a key
// nobody could see turned itself off.
func boolValue(key, value string) (bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		return false, &ValueError{Key: key, Value: value, Want: "true or false"}
	}
	return b, nil
}

// triState is a tri-state key's value: nil for an empty value, so that a
// reset puts the key back to unset — the default, whatever it is — rather
// than to false, which for a key that is on when unset would be the opposite
// of what a reset means.
func triState(key, value string) (*bool, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	b, err := boolValue(key, value)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// optionalCount parses a count whose unset is not its zero: empty is a key
// the file does not hold, and a written zero is the number nought. It is
// triState's integer, for a setting where turning the feature off and
// leaving it alone are two different requests.
//
// It takes signed for the reason the plain integers do rather than assuming
// it: a key that says in the table that a negative means something and then
// had it refused here would be a table that lies about what it accepts.
func optionalCount(key, value string, signed bool) (*int, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	n, err := intValue(key, value, signed)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// splitList parses a comma-separated config value into its non-empty,
// trimmed entries; an empty value clears the list.
func splitList(value string) []string {
	var list []string
	for _, part := range strings.Split(value, ",") {
		if p := strings.TrimSpace(part); p != "" {
			list = append(list, p)
		}
	}
	return list
}

// Value is what a config holds for a key, in the words the file would write,
// and whether anything set it. It is what every surface reads a setting with:
// the screen compares it against the loaded file to say where a value came
// from, and the listing prints it.
//
// A list is joined with commas because that is how `config set` takes one
// back, so what the listing prints round-trips.
func Value(cfg Config, key string) (text string, set bool) {
	f, ok := fieldAt(reflect.ValueOf(cfg), strings.Split(key, "."))
	if !ok {
		return "", false
	}
	return readField(f)
}

// readField is one field as text, and whether anything set it. Unset is the
// zero value for the plain types and a nil for the three pointers, which is
// the distinction those pointers exist to keep: `mouse = false` is a person's
// answer and an absent key is not.
func readField(f reflect.Value) (string, bool) {
	switch v := f.Interface().(type) {
	case string:
		return v, v != ""
	case []string:
		return strings.Join(v, ", "), len(v) > 0
	case bool:
		if !v {
			return "", false
		}
		return "true", true
	case *bool:
		if v == nil {
			return "", false
		}
		return strconv.FormatBool(*v), true
	case int:
		if v == 0 {
			return "", false
		}
		return strconv.Itoa(v), true
	case int64:
		if v == 0 {
			return "", false
		}
		return strconv.FormatInt(v, 10), true
	case *int:
		if v == nil {
			return "", false
		}
		return strconv.Itoa(*v), true
	}
	return "", false
}

// errNoField is a table entry whose key names no field of Config, which a
// test forbids and nothing else can produce: Set has already found the key in
// the table by the time it walks for the field. It is an error rather than a
// panic because the alternative to failing here is writing nowhere and
// reporting success.
var errNoField = errors.New("no such field")

// atField hands the field a key names to use, as a value that can be
// assigned. It is the write walk; reading one takes fieldAt, which touches
// nothing.
//
// A map on the way — the per-role agent profiles — is copied out, edited and
// put back, because a value inside a Go map has no address and the
// alternative would be a second way of writing a setting, for one key. The
// map itself is replaced by a copy of itself first, and that is not
// housekeeping: a Config is copied by value and its maps are not, so writing
// straight into one would change every other copy along with this one. The
// config screen holds two — what it loaded and what it has staged — and
// compares them to say whether a row is in the file yet, so a shared map
// would make a staged role model read as already written and then not write
// it.
func atField(v reflect.Value, path []string, use func(reflect.Value) error) error {
	if len(path) == 0 {
		return use(v)
	}
	seg := path[0]
	switch v.Kind() {
	case reflect.Map:
		if !v.CanSet() {
			return errNoField
		}
		next := reflect.MakeMap(v.Type())
		for _, k := range v.MapKeys() {
			next.SetMapIndex(k, v.MapIndex(k))
		}
		held := reflect.New(v.Type().Elem()).Elem()
		if at := next.MapIndex(reflect.ValueOf(seg)); at.IsValid() {
			held.Set(at)
		}
		if err := atField(held, path[1:], use); err != nil {
			return err
		}
		next.SetMapIndex(reflect.ValueOf(seg), held)
		v.Set(next)
		return nil
	case reflect.Struct:
		i, ok := fieldIndex(v.Type(), seg)
		if !ok {
			return errNoField
		}
		return atField(v.Field(i), path[1:], use)
	}
	return errNoField
}
