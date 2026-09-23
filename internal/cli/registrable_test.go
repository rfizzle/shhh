package cli

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/subagent"
)

// Every tool a surface can register has a note in the toolbox. A tool with
// none reaches the model as a bare schema — the tool you reach for last, if
// at all. The list is the registrations themselves rather than a list of
// names written beside them, so a tool registered without a note fails here
// whichever package it came from.
//
// MCP server tools are the one exclusion: their names and descriptions are
// the server's, and registrableDefinitions does not hold them.
func TestToolboxHasANoteForEveryToolThatCanBeRegistered(t *testing.T) {
	defs := registrable(t)
	box := prompt.Toolbox(defs, false)
	for _, d := range defs {
		if !strings.Contains(box, "- "+d.Name+" — ") {
			t.Errorf("%s can be registered and has no note saying what it is for", d.Name)
		}
	}
}

// Every argument of every registrable tool says what it is for. A property
// with no description is one the model fills in by guessing from its name,
// and thirty characters is the length that admits "The failed agent to run
// again" and refuses a bare noun. The walk goes into nested objects and
// array items, which is where a bare field hides.
func TestEveryRegistrableArgumentSaysWhatItIsFor(t *testing.T) {
	const least = 30
	for _, d := range registrable(t) {
		var schema any
		if err := json.Unmarshal(d.Parameters, &schema); err != nil {
			t.Errorf("%s: parameters do not parse: %v", d.Name, err)
			continue
		}
		walkProperties(schema, "", func(path string, prop map[string]any) {
			desc, _ := prop["description"].(string)
			switch {
			case strings.TrimSpace(desc) == "":
				t.Errorf("%s: %s has no description", d.Name, path)
			case len([]rune(strings.TrimSpace(desc))) < least:
				t.Errorf("%s: %s is described in under %d characters: %q", d.Name, path, least, desc)
			}
		})
	}
}

// registrable is the set both tests read, built from the fixtures the two
// runtime-shaped definitions need: a catalog of one skill for the skill
// tool's name enum, and the built-in roles for the spawn tool's role enum.
func registrable(t *testing.T) []provider.Tool {
	t.Helper()
	catalog := &skill.Catalog{Skills: []skill.Skill{{Name: "fixture", Description: "A fixture skill."}}}
	defs := registrableDefinitions(catalog, subagent.BuiltinProfiles())
	if len(defs) == 0 {
		t.Fatal("no registrable definitions")
	}
	return defs
}

// walkProperties calls visit for every property of every object in a JSON
// schema, at any depth, with its dotted path; an array's items add "[]".
func walkProperties(node any, path string, visit func(string, map[string]any)) {
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	if props, ok := m["properties"].(map[string]any); ok {
		names := make([]string, 0, len(props))
		for name := range props {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			sub, ok := props[name].(map[string]any)
			if !ok {
				continue
			}
			p := name
			if path != "" {
				p = path + "." + name
			}
			visit(p, sub)
			walkProperties(sub, p, visit)
		}
	}
	if items, ok := m["items"]; ok {
		walkProperties(items, path+"[]", visit)
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		if alts, ok := m[key].([]any); ok {
			for _, alt := range alts {
				walkProperties(alt, path, visit)
			}
		}
	}
}
