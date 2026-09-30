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
	// Tools narrows the toolset within the tiers granted, empty meaning
	// every tool they allow. It is how a role that only verifies through
	// the project's own checks says so: quality_gate sits under read
	// (docs/capabilities/subagents.md#a-profile-that-changes-nothing-can-still-run-the-checks).
	Tools []string `json:"tools,omitempty"`
	// Intent and Deny are the Commands section: what the agent's commands
	// are for, and command prefixes it must never run. Both only narrow —
	// the deny list is added to the person's, and the intent is put to the
	// classifier as something that may refuse and never allow — so the
	// drafter may propose them and a profile carries no allowlist beside
	// them (docs/capabilities/subagents.md#a-profile-is-a-file).
	Intent string   `json:"intent,omitempty"`
	Deny   []string `json:"deny,omitempty"`
	// Sections is the prompt as the drafter answers it: the five prose
	// sections by name, so one it left empty is an empty field rather than
	// a paragraph nobody can see is missing. Normalise writes Prompt from
	// it, which makes it the source of the prompt wherever both are set; a
	// draft carrying only Prompt — an answer in the older shape — has it
	// read into Sections instead.
	Sections  *Sections `json:"sections,omitempty"`
	Prompt    string    `json:"prompt,omitempty"`
	MaxTokens int64     `json:"max_tokens,omitempty"`
	Why       string    `json:"why,omitempty"`
	// Dropped is the tools Normalise took off the list because the session
	// cannot grant their tier, for the card to name. It is not part of the
	// file, nor of the draft the drafter is shown again.
	Dropped []string `json:"-"`
}

// Sections is a profile's five prose sections as the drafter's answer names
// them. The stored form is the prompt with a `##` heading per section; this
// is the same text held apart, so a later revision can change one section
// and hand the other four back as they were
// (docs/capabilities/subagents.md#a-profile-is-drafted-in-conversation).
type Sections struct {
	Purpose      string `json:"purpose"`
	Scope        string `json:"scope"`
	Restrictions string `json:"restrictions"`
	Method       string `json:"method"`
	Report       string `json:"report"`
}

// List is the sections in the loader's order and spelling.
func (s Sections) List() []config.PromptSection {
	return []config.PromptSection{
		{Name: config.SectionPurpose, Body: s.Purpose},
		{Name: config.SectionScope, Body: s.Scope},
		{Name: config.SectionRestrictions, Body: s.Restrictions},
		{Name: config.SectionMethod, Body: s.Method},
		{Name: config.SectionReport, Body: s.Report},
	}
}

// sectionsOf is the loader's reading of a prompt as a Sections.
func sectionsOf(list []config.PromptSection) *Sections {
	s := &Sections{}
	for _, sec := range list {
		body := strings.TrimSpace(sec.Body)
		switch sec.Name {
		case config.SectionPurpose:
			s.Purpose = body
		case config.SectionScope:
			s.Scope = body
		case config.SectionRestrictions:
			s.Restrictions = body
		case config.SectionMethod:
			s.Method = body
		case config.SectionReport:
			s.Report = body
		}
	}
	return s
}

// Empty is the names of the sections with nothing in them, in order.
func (s Sections) Empty() []string {
	var out []string
	for _, sec := range s.List() {
		if strings.TrimSpace(sec.Body) == "" {
			out = append(out, sec.Name)
		}
	}
	return out
}

// Section is one section's text by its loader name, or "" for a name that
// is not one of the five.
func (s Sections) Section(name string) string {
	for _, sec := range s.List() {
		if sec.Name == name {
			return sec.Body
		}
	}
	return ""
}

// SectionList is the draft's prompt as its five sections: the ones the
// drafter answered, or the prompt read into them for a draft that carries
// only a prompt.
func (d Draft) SectionList() []config.PromptSection {
	if d.Sections != nil {
		return d.Sections.List()
	}
	return sectionsOf(config.ReadPromptSections(d.Prompt)).List()
}

// SetSection replaces one section's text and writes the prompt again from
// the five. A revision changes the section and never the prompt: Normalise
// rebuilds the prompt from the sections wherever any is filled, so a prompt
// edited on its own would be overwritten by the next Normalise, and the
// sections are the one place a revision can live
// (docs/capabilities/subagents.md#a-profile-is-drafted-in-conversation).
// It reports false for a name that is not one of the five.
func (d *Draft) SetSection(name, body string) bool {
	list := d.SectionList()
	found := false
	for i := range list {
		if list[i].Name == name {
			list[i].Body = strings.TrimSpace(body)
			found = true
		}
	}
	if !found {
		return false
	}
	d.Sections = sectionsOf(list)
	d.Prompt = config.WritePromptSections(list)
	return true
}

// Definition is the draft as the loader would read it.
func (d Draft) Definition() config.AgentDefinition {
	return config.AgentDefinition{
		Name:        d.Name,
		Description: d.Description,
		Model:       d.Model,
		Reasoning:   d.Reasoning,
		Permissions: d.Permissions,
		Tools:       d.Tools,
		Intent:      d.Intent,
		Deny:        d.Deny,
		Prompt:      d.Prompt,
		MaxTokens:   d.MaxTokens,
	}
}

// The words the Commands section is written in when it is handed to the
// person's editor: one field per line, each deny prefix a line of its own.
const (
	commandsIntent = "intent:"
	commandsDeny   = "deny:"
	commandsAllow  = "allow:"
)

// CommandsText is the Commands section as text: the intent on a line, then
// one line per deny prefix, in the words ParseCommands reads back. It is
// empty for a draft that states neither.
func (d Draft) CommandsText() string {
	var lines []string
	if d.Intent != "" {
		lines = append(lines, commandsIntent+" "+d.Intent)
	}
	for _, p := range d.Deny {
		lines = append(lines, commandsDeny+" "+p)
	}
	return strings.Join(lines, "\n")
}

// SetCommands replaces the Commands section with what text says, read the
// way CommandsText writes it: an `intent:` line, `deny:` lines, blank lines
// and `#` comments ignored. Anything else is refused with its line, and so
// is an `allow:` line, in the loader's own words — the file would not load,
// so the card must not keep it. On a refusal the draft is left as it was.
func (d *Draft) SetCommands(text string) error {
	var intent []string
	var deny []string
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(lower, commandsIntent):
			if v := strings.TrimSpace(line[len(commandsIntent):]); v != "" {
				intent = append(intent, v)
			}
		case strings.HasPrefix(lower, commandsDeny):
			if v := strings.TrimSpace(line[len(commandsDeny):]); v != "" {
				deny = append(deny, v)
			}
		case strings.HasPrefix(lower, commandsAllow):
			return config.CheckCommands(nil, "", true)
		default:
			return fmt.Errorf("line %d: start it with %q or %q", i+1, commandsIntent, commandsDeny)
		}
	}
	next := Draft{Intent: strings.Join(intent, " "), Deny: deny}
	next.tidyCommands()
	if err := config.CheckCommands(next.Deny, next.Intent, false); err != nil {
		return err
	}
	d.Intent, d.Deny = next.Intent, next.Deny
	return nil
}

// tidyCommands puts the intent on one line and drops blank and repeated
// deny prefixes, keeping the order they were written in.
func (d *Draft) tidyCommands() {
	d.Intent = strings.Join(strings.Fields(d.Intent), " ")
	seen := map[string]bool{}
	var deny []string
	for _, p := range d.Deny {
		p = strings.Join(strings.Fields(p), " ")
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		deny = append(deny, p)
	}
	d.Deny = deny
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
// implied rather than listed, a deduplicated tool allowlist, and — for a
// chat persona — nothing that writes, whatever the model proposed: the
// write and execute tiers are taken away, and so are the tools that need
// them, named in Dropped. It returns an error only for what tidying cannot
// fix: a tool the tiers do not grant, or the quality gate named beside
// write or execute, are
// refusals of the loader's that a draft hears here instead — while it is
// still a card the person can revise rather than a file that will not
// load (docs/capabilities/subagents.md#a-profile-is-drafted-in-conversation).
func (d *Draft) Normalise(kind Kind) error {
	d.Name = slug(d.Name)
	if !validName.MatchString(d.Name) {
		return fmt.Errorf("name %q: lowercase letters, digits and dashes, up to 24 characters", d.Name)
	}
	d.Description = strings.TrimSpace(strings.Join(strings.Fields(d.Description), " "))
	if d.Description == "" {
		return fmt.Errorf("description is empty")
	}
	// The sections are the prompt whenever they say anything; a draft with
	// none is read from its prompt, which keeps a prompt with no headings
	// as its Purpose and as the text it was.
	if d.Sections != nil && len(d.Sections.Empty()) < len(config.PromptSectionNames()) {
		d.Sections = sectionsOf(d.Sections.List())
		d.Prompt = config.WritePromptSections(d.Sections.List())
	} else {
		d.Prompt = strings.TrimSpace(d.Prompt)
		d.Sections = sectionsOf(config.ReadPromptSections(d.Prompt))
	}
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
	var tools, dropped []string
	listed := map[string]bool{}
	for _, t := range d.Tools {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || listed[t] {
			continue
		}
		listed[t] = true
		// A tool that needs a tier a chat persona cannot hold goes the way
		// the tier did: the read-only guarantee is kept by taking it off,
		// not by refusing a draft the person could not fix from the card.
		if tier, _ := config.ToolTier(t); kind == KindChat &&
			(tier == config.PermissionWrite || tier == config.PermissionExecute) {
			dropped = append(dropped, t)
			continue
		}
		tools = append(tools, t)
	}
	d.Tools, d.Dropped = tools, dropped
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
	d.tidyCommands()
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
	if len(d.Tools) > 0 {
		quoted := make([]string, len(d.Tools))
		for i, t := range d.Tools {
			quoted[i] = tomlString(t)
		}
		fmt.Fprintf(&b, "tools = [%s]\n", strings.Join(quoted, ", "))
	}
	if d.MaxTokens > 0 {
		fmt.Fprintf(&b, "max_tokens = %d\n", d.MaxTokens)
	}
	if d.Intent != "" {
		fmt.Fprintf(&b, "intent = %s\n", tomlString(d.Intent))
	}
	if len(d.Deny) > 0 {
		quoted := make([]string, len(d.Deny))
		for i, p := range d.Deny {
			quoted[i] = tomlString(p)
		}
		fmt.Fprintf(&b, "deny = [%s]\n", strings.Join(quoted, ", "))
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
