// Package persona drafts agent profiles from a sentence. A profile is a
// file the person could write by hand; what this adds is the conversation
// that gets a person from "I want something that checks my claims" to a
// file that does, with the model doing the drafting and the person doing
// the deciding. The same mechanism serves both sessions, and what it says
// leans one way in a chat and the other in a coding session.
// See docs/capabilities/subagents.md#a-profile-is-drafted-in-conversation.
package persona

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rfizzle/shhh/internal/config"
)

// Kind is the session the profile is drafted in. It decides what the
// drafter is told to value: a chat persona is a colleague with a
// standpoint and a voice that only reads; a code role is an engineer with
// a job, a way of verifying it, and a permission set to match.
type Kind string

const (
	KindChat Kind = "chat"
	KindCode Kind = "code"
)

// Draft is a profile as proposed: every field of the file, plus one line
// on why it was drawn this way, for the person deciding.
type Draft struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Model       string   `json:"model,omitempty"`
	Reasoning   string   `json:"reasoning,omitempty"`
	Permissions []string `json:"permissions"`
	Prompt      string   `json:"prompt"`
	MaxTokens   int64    `json:"max_tokens,omitempty"`
	Why         string   `json:"why,omitempty"`
}

// Definition is the draft as the loader would read it.
func (d Draft) Definition() config.AgentDefinition {
	return config.AgentDefinition{
		Name:        d.Name,
		Description: d.Description,
		Model:       d.Model,
		Reasoning:   d.Reasoning,
		Permissions: d.Permissions,
		Prompt:      d.Prompt,
		MaxTokens:   d.MaxTokens,
	}
}

// Writes reports a draft that could change something.
func (d Draft) Writes() bool { return d.Definition().Writes() }

// Tier is the permission set in words, for a card.
func (d Draft) Tier() string {
	def := d.Definition()
	var parts []string
	parts = append(parts, "read")
	for _, p := range []string{config.PermissionWeb, config.PermissionWrite, config.PermissionExecute} {
		if def.Has(p) {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " + ")
}

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,23}$`)

// Normalise tidies a draft into what the loader accepts: a lowercase
// dashed name, deduplicated permissions in tier order, the read tier
// implied rather than listed, and — for a chat persona — nothing that
// writes, whatever the model proposed. It returns an error only for what
// tidying cannot fix.
func (d *Draft) Normalise(kind Kind) error {
	d.Name = slug(d.Name)
	if !validName.MatchString(d.Name) {
		return fmt.Errorf("name %q: lowercase letters, digits and dashes, up to 24 characters", d.Name)
	}
	d.Description = strings.TrimSpace(strings.Join(strings.Fields(d.Description), " "))
	if d.Description == "" {
		return fmt.Errorf("description is empty")
	}
	d.Prompt = strings.TrimSpace(d.Prompt)
	if d.Prompt == "" {
		return fmt.Errorf("prompt is empty")
	}
	seen := map[string]bool{}
	var perms []string
	for _, tier := range []string{config.PermissionWeb, config.PermissionWrite, config.PermissionExecute} {
		for _, p := range d.Permissions {
			p = strings.ToLower(strings.TrimSpace(p))
			if p == tier && !seen[p] {
				seen[p] = true
				perms = append(perms, p)
			}
		}
	}
	if kind == KindChat {
		// A chat persona reads. The drafter is told so; this is the
		// guarantee (docs/capabilities/chat.md#colleagues-not-workers).
		var ro []string
		for _, p := range perms {
			if p == config.PermissionWeb {
				ro = append(ro, p)
			}
		}
		perms = ro
	}
	d.Permissions = perms
	d.Model = strings.TrimSpace(d.Model)
	if strings.EqualFold(d.Model, "inherit") {
		d.Model = ""
	}
	d.Reasoning = strings.ToLower(strings.TrimSpace(d.Reasoning))
	switch d.Reasoning {
	case "", "inherit":
		d.Reasoning = ""
	case "off", "low", "medium", "high", "xhigh", "max":
	default:
		return fmt.Errorf("reasoning %q: off, low, medium, high, xhigh, max or inherit", d.Reasoning)
	}
	if d.MaxTokens < 0 {
		d.MaxTokens = 0
	}
	return d.Definition().Validate()
}

// slug is a name as the loader wants it: whatever was proposed, lowered,
// with runs of anything else collapsed to one dash.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 24 {
		out = strings.Trim(out[:24], "-")
	}
	return out
}

// Scope is where a profile file lives.
type Scope string

const (
	// ScopeProject is .shhh/agents under the repository root: the persona
	// travels with the work, committed or not.
	ScopeProject Scope = "project"
	// ScopeGlobal is the config directory's agents/: every session has it.
	ScopeGlobal Scope = "global"
)

// Dir is the directory a scope resolves to from cwd.
func Dir(scope Scope, cwd string) string {
	if scope == ScopeProject {
		return config.ProjectAgentDir(cwd)
	}
	dirs := config.AgentDirs()
	if len(dirs) == 0 {
		return filepath.Join(cwd, ".shhh", "agents")
	}
	return dirs[0]
}

// Write renders the draft and writes it as <dir>/<name>.toml. An existing
// file is not overwritten unless overwrite is set: a profile is something
// the person may have edited by hand since, and a draft that clobbers it is
// a loss they did not choose.
func Write(dir string, d Draft, kind Kind, overwrite bool) (string, error) {
	if err := d.Normalise(kind); err != nil {
		return "", err
	}
	path := filepath.Join(dir, d.Name+".toml")
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return path, fmt.Errorf("%s already exists; pick another name or say to replace it", path)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(Render(d, kind)), 0o644); err != nil {
		return "", err
	}
	// The file the loader reads is the one that counts: read it back so a
	// draft the renderer could not express is caught here, not at the next
	// session's start.
	if _, err := config.LoadAgentFile(path); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// Render is the profile as a TOML file, in the field order the reference
// documents, with a comment on the one thing a reader needs: what the
// file is and how it got here.
func Render(d Draft, kind Kind) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — an agent profile for shhh %s. Spawned by role name; edit freely.\n", d.Name, kind)
	if d.Why != "" {
		fmt.Fprintf(&b, "# %s\n", strings.Join(strings.Fields(d.Why), " "))
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "description = %s\n", tomlString(d.Description))
	if d.Model != "" {
		fmt.Fprintf(&b, "model = %s\n", tomlString(d.Model))
	}
	if d.Reasoning != "" {
		fmt.Fprintf(&b, "reasoning = %s\n", tomlString(d.Reasoning))
	}
	if len(d.Permissions) > 0 {
		quoted := make([]string, len(d.Permissions))
		for i, p := range d.Permissions {
			quoted[i] = tomlString(p)
		}
		fmt.Fprintf(&b, "permissions = [%s]\n", strings.Join(quoted, ", "))
	} else {
		b.WriteString("permissions = [] # read only\n")
	}
	if d.MaxTokens > 0 {
		fmt.Fprintf(&b, "max_tokens = %d\n", d.MaxTokens)
	}
	b.WriteString("prompt = \"\"\"\n")
	b.WriteString(strings.ReplaceAll(d.Prompt, `"""`, `""\"`))
	b.WriteString("\n\"\"\"\n")
	return b.String()
}

func tomlString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return `"` + s + `"`
}

// Suggestions are starting points for a person with no brief in mind,
// worded for the session they are in. Each is a complete brief: picking
// one is the same as typing it.
func Suggestions(kind Kind) []string {
	if kind == KindChat {
		return []string{
			"a skeptic who checks each claim against a primary source and says how sure to be",
			"a devil's advocate who argues the strongest case against whatever I'm leaning toward",
			"a summariser who reads long pages and returns the five things that matter, with quotes",
			"a domain expert in a field I name, who answers in that field's own terms",
		}
	}
	return []string{
		"a test writer who adds table-driven tests for a package and runs them",
		"a reviewer who reads a diff for security problems and reports by severity",
		"a docs keeper who updates the documentation a change made stale, and nothing else",
		"a migrator who applies one mechanical refactor across the tree and verifies the build",
	}
}

// Existing is the roles a session already has, sorted, for the drafter to
// avoid and the person to see.
func Existing(defs map[string]config.AgentDefinition, builtins ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, b := range builtins {
		if !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	for name := range defs {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
