package config

// Agent profiles: one TOML file per agent under the config directory's
// agents/ folder. A profile is a named sub-agent the orchestrator can spawn
// by that name, carrying its own model, reasoning level, permission set,
// tool allowlist, permission mode, prompt and budgets. The two built-in
// roles are the profiles everyone has; a file with the same name overrides
// one. See docs/capabilities/subagents.md#a-profile-is-a-file.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// AgentDefinition is one agent profile as written in its TOML file.
type AgentDefinition struct {
	// Name is what spawn_agent's role argument names. It defaults to the
	// file's stem, and a value that disagrees with the stem is an error
	// rather than a second name — the file is the profile, so the file's
	// name is the profile's name.
	Name string `toml:"name"`
	// Description is the one-line account of what this agent is for. It is
	// shown to the orchestrating model in the spawn tool's description, so
	// write it for the model that has to choose between profiles.
	Description string `toml:"description"`
	// Model is the model this agent runs on. Empty or "inherit" defers to
	// the [agents] section of config.toml and then to the session model; a
	// spawn call naming a model outranks all of them.
	Model string `toml:"model"`
	// Reasoning is the thinking level: "off", "low", "medium", "high",
	// "xhigh", "max", or "inherit" (the default) for the session's live
	// level.
	Reasoning string `toml:"reasoning"`
	// Permissions is the set of tool tiers the agent gets: "read" (files and
	// search — always on, listed or not), "write" (write_file, edit_file),
	// "execute" (execute_command) and "web" (web_fetch, web_search when the
	// session has them). An agent with write or execute works in an isolated
	// worktree and its changes come back as a patch.
	Permissions []string `toml:"permissions"`
	// Tools narrows the toolset to these names within the granted
	// permissions. Empty means every tool the permissions allow.
	Tools []string `toml:"tools"`
	// Mode is the permission mode the agent starts in ("manual",
	// "accept-edits", "auto", "plan"); empty inherits the parent's. It is
	// clamped to the parent's mode either way — a profile can make a child
	// stricter than its parent, never looser.
	Mode string `toml:"mode"`
	// Prompt is the agent's instructions. By default it is appended to the
	// base prompt built from the permissions (environment, tools, working
	// style, final report); prompt_mode = "replace" sends it alone.
	Prompt string `toml:"prompt"`
	// PromptFile is a path (absolute, or relative to the profile's
	// directory) whose contents are used as Prompt. Setting both is an error.
	PromptFile string `toml:"prompt_file"`
	// PromptMode is "append" (the default) or "replace".
	PromptMode string `toml:"prompt_mode"`
	// MaxTokens is the default token budget for a spawn that names none;
	// zero means the built-in default.
	MaxTokens int64 `toml:"max_tokens"`
	// MaxRounds is the default check-in interval in tool rounds; zero means
	// none, which is the built-in default.
	MaxRounds int `toml:"max_rounds"`

	// Path is the file this definition was read from; empty for one built
	// in code.
	Path string `toml:"-"`
}

// Permission tiers a profile may grant.
const (
	PermissionRead    = "read"
	PermissionWrite   = "write"
	PermissionExecute = "execute"
	PermissionWeb     = "web"
)

// Prompt modes.
const (
	PromptAppend  = "append"
	PromptReplace = "replace"
)

// knownAgentTools is every tool name a profile may list under tools. A
// profile is validated against this at load time, so a typo is reported by
// path and field rather than turning into a child with fewer tools than its
// author meant.
var knownAgentTools = map[string]string{
	"read_file":       PermissionRead,
	"list_directory":  PermissionRead,
	"search":          PermissionRead,
	"glob":            PermissionRead,
	"write_file":      PermissionWrite,
	"edit_file":       PermissionWrite,
	"execute_command": PermissionExecute,
	"web_fetch":       PermissionWeb,
	"web_search":      PermissionWeb,
}

// KnownAgentTools lists the tool names a profile may name, sorted.
func KnownAgentTools() []string {
	out := make([]string, 0, len(knownAgentTools))
	for name := range knownAgentTools {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

var validAgentName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,23}$`)

// Has reports whether the profile grants a permission tier. Read is always
// granted: an agent that cannot look at the workspace has nothing to report.
func (d AgentDefinition) Has(permission string) bool {
	if permission == PermissionRead {
		return true
	}
	for _, p := range d.Permissions {
		if strings.EqualFold(strings.TrimSpace(p), permission) {
			return true
		}
	}
	return false
}

// Writes reports whether the agent can change anything — write or execute —
// which is what decides that it works in an isolated copy of the workspace
// and hands back a patch.
func (d AgentDefinition) Writes() bool {
	return d.Has(PermissionWrite) || d.Has(PermissionExecute)
}

// Allows reports whether a tool name survives the profile's allowlist. An
// empty allowlist admits every tool the permissions grant.
func (d AgentDefinition) Allows(tool string) bool {
	if len(d.Tools) == 0 {
		return true
	}
	for _, t := range d.Tools {
		if strings.TrimSpace(t) == tool {
			return true
		}
	}
	return false
}

// InheritsReasoning reports whether the profile leaves the reasoning level
// to the session.
func (d AgentDefinition) InheritsReasoning() bool {
	r := strings.ToLower(strings.TrimSpace(d.Reasoning))
	return r == "" || r == InheritModel
}

// ProfileModel is the model the profile itself asks for, or "" when it
// defers to the session and config.
func (d AgentDefinition) ProfileModel() string {
	m := strings.TrimSpace(d.Model)
	if strings.EqualFold(m, InheritModel) {
		return ""
	}
	return m
}

// Validate checks what can be checked without the runtime: names, tiers,
// tool names against the tiers that grant them, and the prompt fields.
func (d AgentDefinition) Validate() error {
	if !validAgentName.MatchString(d.Name) {
		return fmt.Errorf("name %q: lowercase letters, digits and dashes, max 24 characters", d.Name)
	}
	for _, p := range d.Permissions {
		switch strings.ToLower(strings.TrimSpace(p)) {
		case PermissionRead, PermissionWrite, PermissionExecute, PermissionWeb:
		default:
			return fmt.Errorf("permissions: unknown tier %q (valid: read, write, execute, web)", p)
		}
	}
	for _, t := range d.Tools {
		name := strings.TrimSpace(t)
		tier, ok := knownAgentTools[name]
		if !ok {
			return fmt.Errorf("tools: unknown tool %q (valid: %s)", t, strings.Join(KnownAgentTools(), ", "))
		}
		if !d.Has(tier) {
			return fmt.Errorf("tools: %q needs the %q permission, which this profile does not grant", name, tier)
		}
	}
	switch strings.ToLower(strings.TrimSpace(d.PromptMode)) {
	case "", PromptAppend, PromptReplace:
	default:
		return fmt.Errorf("prompt_mode: %q (valid: append, replace)", d.PromptMode)
	}
	if d.Prompt != "" && d.PromptFile != "" {
		return fmt.Errorf("prompt and prompt_file are both set; use one")
	}
	if strings.EqualFold(strings.TrimSpace(d.PromptMode), PromptReplace) && strings.TrimSpace(d.Prompt) == "" {
		return fmt.Errorf("prompt_mode = \"replace\" needs a prompt to replace the base with")
	}
	if d.MaxTokens < 0 {
		return fmt.Errorf("max_tokens: must not be negative")
	}
	if d.MaxRounds < 0 {
		return fmt.Errorf("max_rounds: must not be negative")
	}
	return nil
}

// AgentDirs returns the profile directories in search order, one per config
// path: the first directory holding a given name wins, so an XDG directory
// shadows ~/.config the way config.toml does.
func AgentDirs() []string {
	var out []string
	for _, p := range Paths() {
		out = append(out, filepath.Join(filepath.Dir(p), "agents"))
	}
	return out
}

// ProjectAgentDir is the project's own profile directory: .shhh/agents
// under the repository root — the nearest ancestor of dir holding .git, or
// dir itself outside a repository. It is searched before the config
// directories, so a project can carry the personas its work needs and
// shadow a global one of the same name. Nothing assumes the directory is
// committed. See docs/capabilities/subagents.md#a-profile-is-a-file.
func ProjectAgentDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	root := abs
	for probe := abs; ; {
		if _, err := os.Stat(filepath.Join(probe, ".git")); err == nil {
			root = probe
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	return filepath.Join(root, ".shhh", "agents")
}

// LoadAgents reads every profile under the config agent directories.
func LoadAgents() (map[string]AgentDefinition, error) {
	return LoadAgentsFrom(AgentDirs()...)
}

// LoadAgentsFor reads the project's profiles and then the config
// directories', the project shadowing.
func LoadAgentsFor(dir string) (map[string]AgentDefinition, error) {
	return LoadAgentsFrom(append([]string{ProjectAgentDir(dir)}, AgentDirs()...)...)
}

// LoadAgentsFrom reads every *.toml under dirs, earlier directories
// shadowing later ones. A directory that does not exist is fine; a file
// that does not parse or validate is an error naming the file, because a
// profile that silently failed to load is a spawn that quietly gets the
// wrong tools.
func LoadAgentsFrom(dirs ...string) (map[string]AgentDefinition, error) {
	out := map[string]AgentDefinition{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			def, err := LoadAgentFile(path)
			if err != nil {
				return nil, err
			}
			if _, shadowed := out[def.Name]; shadowed {
				continue
			}
			out[def.Name] = def
		}
	}
	return out, nil
}

// LoadAgentFile reads one profile. The name defaults to the file's stem and
// a prompt_file is resolved relative to the profile's directory.
func LoadAgentFile(path string) (AgentDefinition, error) {
	var def AgentDefinition
	meta, err := toml.DecodeFile(path, &def)
	if err != nil {
		return AgentDefinition{}, fmt.Errorf("agent profile %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return AgentDefinition{}, fmt.Errorf("agent profile %s: unknown key %s", path, strings.Join(keys, ", "))
	}
	def.Path = path
	stem := strings.TrimSuffix(filepath.Base(path), ".toml")
	switch {
	case def.Name == "":
		def.Name = stem
	case def.Name != stem:
		return AgentDefinition{}, fmt.Errorf("agent profile %s: name %q does not match the file name; rename one of them", path, def.Name)
	}
	if def.PromptFile != "" {
		if def.Prompt != "" {
			return AgentDefinition{}, fmt.Errorf("agent profile %s: prompt and prompt_file are both set; use one", path)
		}
		p := def.PromptFile
		if !filepath.IsAbs(p) {
			p = filepath.Join(filepath.Dir(path), p)
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return AgentDefinition{}, fmt.Errorf("agent profile %s: prompt_file: %w", path, err)
		}
		def.Prompt = string(body)
		def.PromptFile = ""
	}
	if err := def.Validate(); err != nil {
		return AgentDefinition{}, fmt.Errorf("agent profile %s: %w", path, err)
	}
	return def, nil
}

// SkillDirs returns shhh's own user-scope skill directories in search
// order, one per config path, beside the agents directory: the layout that
// holds config.toml holds everything the user wrote for shhh.
// See docs/capabilities/skills.md#where-skills-live.
func SkillDirs() []string {
	var out []string
	for _, p := range Paths() {
		out = append(out, filepath.Join(filepath.Dir(p), "skills"))
	}
	return out
}
