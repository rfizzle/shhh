package hook

// Where hooks come from, and what a file that cannot be read costs.
//
// Reading is lenient the way the servers catalog is: a hook that will not
// load is a diagnostic naming it, never a reason the session does not start.
// A project file arrived with a clone, and the person opening the session may
// not have written it — so the answer to a broken entry is to say which one,
// and start without it.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
)

// Set is every hook a session loaded, with the entries it could not.
type Set struct {
	hooks []Hook
	// Diagnostics are the entries that did not load and why, one line each.
	Diagnostics []string
}

// Len is how many hooks loaded. Safe on a nil Set, which is a session with
// none.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.hooks)
}

// Notes are the entries that did not load and why. Safe on a nil Set, which
// is a session that loaded none and had nothing to complain about.
func (s *Set) Notes() []string {
	if s == nil {
		return nil
	}
	return s.Diagnostics
}

// All is every hook that loaded, in the order they fire.
func (s *Set) All() []Hook {
	if s == nil {
		return nil
	}
	return append([]Hook(nil), s.hooks...)
}

// For is the hooks one seam fires, in order: the ones on this event whose
// matcher takes this tool. tool is empty for the events that carry none.
func (s *Set) For(event, tool string) []Hook {
	if s == nil {
		return nil
	}
	var out []Hook
	for _, h := range s.hooks {
		if h.Event == event && h.matches(tool) {
			out = append(out, h)
		}
	}
	return out
}

// Events names the events this set has a hook for, in listing order. It is
// what the doctor row and `/status` print: the question a reader has is which
// seams are live, not which entries exist.
func (s *Set) Events() []string {
	if s == nil {
		return nil
	}
	var out []string
	for _, e := range Events() {
		for _, h := range s.hooks {
			if h.Event == e {
				out = append(out, e)
				break
			}
		}
	}
	return out
}

// Load assembles the set from the entries the user's own config file holds
// and the checkout's file, in that order.
//
// projectFile is the checkout's hooks file, or "" where there is no checkout
// or the person has not trusted it — the trust answer belongs to whoever
// reads the store, and this package never reads one. A checkout's hook is a
// command line that runs as whoever cloned it, which is exactly what trust is
// for (docs/capabilities/approvals-and-safety.md#a-checkout-declares-what-it-runs).
//
// A project hook shadows a user hook of the same name, the precedence a
// person means when they copy a shared entry into a checkout to change it.
func Load(user map[string]Entry, userSource, projectFile string) *Set {
	s := &Set{}
	byName := map[string]Hook{}
	add := func(name string, e Entry, source string) {
		h, err := build(name, e, source)
		if err != nil {
			s.Diagnostics = append(s.Diagnostics, fmt.Sprintf("%s: hook %s: %v", source, name, err))
			return
		}
		byName[name] = h
	}
	for _, name := range sortedNames(user) {
		add(name, user[name], userSource)
	}
	if projectFile != "" {
		entries, diags := ReadJSON(projectFile)
		s.Diagnostics = append(s.Diagnostics, diags...)
		for _, name := range sortedNames(entries) {
			add(name, entries[name], projectFile)
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		s.hooks = append(s.hooks, byName[name])
	}
	return s
}

func sortedNames(m map[string]Entry) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// jsonFile is the checkout's file: a `hooks` object keyed by name, the same
// shape as the `[hooks.entries]` tables in the config file, so an entry moved
// from one to the other keeps its four fields and its name.
type jsonFile struct {
	Hooks map[string]Entry `json:"hooks"`
}

// ReadJSON reads one hooks file. A missing file is nothing; a file that will
// not parse is one diagnostic and no hooks.
func ReadJSON(path string) (map[string]Entry, []string) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("%s: %v", path, err)}
	}
	var f jsonFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, []string{fmt.Sprintf("%s: not a valid hooks file: %v", path, err)}
	}
	return f.Hooks, nil
}
