package config

// A checkout's own settings, layered over the user's key by key. What is
// true of a repository — which commands run without asking, which mode a
// session starts in, which model reviews here — travels with the
// repository; what is true of the person stays in their file, and nobody
// re-declares a provider per checkout because the merge is per key rather
// than per file.
// See docs/capabilities/configuration.md#two-files-one-resolution-order.
//
// Two gates stand in front of it. The checkout has to be trusted, which is
// the caller's to establish — this package does not read the store — and a
// short set of keys is refused whatever the answer to that was, because each
// of them is a key whose value in a checkout is a value in every clone, or
// one that reaches the machine rather than the tree.

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/rfizzle/shhh/internal/project"
)

// ProjectPath is where a checkout keeps its settings: the shhh directory at
// the root this project's state is keyed on — the repository root, or the
// nearest ancestor already holding one. It is stated whether or not the file
// is there, because it is also the path a `--project` write creates.
func ProjectPath(dir string) string {
	return filepath.Join(project.Root(dir), filepath.FromSlash(project.ConfigFile))
}

// ProjectFileAt is ProjectPath when the file is actually there, and "" when
// it is not. Callers use it to decide whether the checkout's standing is
// worth reading at all: establishing trust walks and fingerprints the
// checkout, and the ordinary repository has no settings file to gate.
func ProjectFileAt(dir string) string {
	path := ProjectPath(dir)
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return ""
	}
	return path
}

// Project is what a checkout's file contributed to the settings in force.
// The zero value is a session running on the user's file alone, which is
// what an untrusted checkout, a checkout with no file, and a directory that
// is not a project all come to.
type Project struct {
	// Path is the file that was read, and Display the same path as a
	// surface prints it — stated from the root, so every session in one
	// checkout names it the same way.
	Path    string
	Display string
	// Keys are the keys the file set, in the order it wrote them. It is the
	// file's order rather than the settings table's because this list is
	// read as a description of that file.
	Keys []string
}

// Loaded reports whether a checkout's file is in force.
func (p Project) Loaded() bool { return p.Path != "" }

// Sets reports whether the checkout's file decided a key.
func (p Project) Sets(key string) bool { return slices.Contains(p.Keys, key) }

// unionKeys are the three keys a project extends rather than replaces. A
// checkout that adds a command to the allowlist cannot know what is already
// on the person's, so replacing would silently take away commands that have
// nothing to do with this repository — and the failure is a session that
// starts asking about `ls` again with nothing on screen to say why. Every
// other list overrides like a scalar, because every other list is a complete
// answer rather than a set of additions.
var unionKeys = []string{
	"behavior.command_allowlist",
	"behavior.read_only_commands",
	"behavior.scope_dirs",
}

// UnionsInProject reports whether a checkout extends this key rather than
// replacing it. Surfaces that say where a value came from ask, because
// `project` on one of these three means the checkout added to the person's
// list rather than took it away.
func UnionsInProject(key string) bool { return slices.Contains(unionKeys, key) }

// projectRefusal is one key a checkout may not set and the reason, phrased
// to follow "is not read from a checkout's file — ".
type projectRefusal struct {
	Key    string
	Reason string
}

// projectRefusals is the whole of what a checkout may not decide. It is
// short on purpose: trust is the second gate rather than the only one, and
// what is listed here is the set of keys whose value in a checkout is a
// value in every clone of it, or one that reaches past the tree onto the
// machine. A key matches by name or as the table above it, so `[sandbox]`
// covers every key under it and `provider.api_key` does not cover
// `provider.api_key_env` beside it.
var projectRefusals = []projectRefusal{
	{"provider.api_key", "a credential in a checkout is a credential in every clone of it"},
	{"provider.api_key_env", "it would let the checkout choose which of your variables is sent as the key"},
	{"web.search_api_key", "a credential in a checkout is a credential in every clone of it"},
	{"web.search_api_key_env", "it would let the checkout choose which of your variables is sent as the search key"},
	{"secrets.env", "it declares which of your environment variables a session may spend, which is about the machine rather than the tree"},
	{"sandbox", "it decides what a contained command may reach, which is the containment itself"},
	{"mcp.servers", "a server is a program to start, and a checkout names its servers in " + project.StateDir + "/mcp.json instead"},
	{"prompts", "it points at a file anywhere on the machine and replaces what a session is told"},
}

// RefusedInProject is the reason a checkout may not set a key, and "" for
// every key it may. It is what a `--project` write asks before it writes, so
// the refusal a write gets and the refusal a load gives are the same
// sentence about the same set.
func RefusedInProject(key string) string {
	_, reason := refusalFor(key)
	return reason
}

// refusalFor is the reason with the entry that matched beside it. The entry
// is what a listing counts by, so a table refused with three keys under it
// is one refusal rather than three.
func refusalFor(key string) (string, string) {
	for _, r := range projectRefusals {
		if key == r.Key || strings.HasPrefix(key, r.Key+".") {
			return r.Key, r.Reason
		}
	}
	return "", ""
}

// ProjectKeyError is a checkout's file naming a key it may not set. It
// refuses the load the way an unknown key does, rather than dropping the key
// and starting: a person reading their checkout's file and a session running
// without what it says is the failure the whole arrangement exists to
// prevent.
type ProjectKeyError struct {
	Path string
	Keys []ProjectKey
	// User is the file the refused keys belong in, named so the reader has
	// somewhere to put them rather than only somewhere not to.
	User string
}

// ProjectKey is one refused key and why it is refused.
type ProjectKey struct {
	Key    string
	Reason string
}

func (e *ProjectKeyError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "config %s:", e.Path)
	for i, k := range e.Keys {
		if i > 0 {
			b.WriteString(";")
		}
		fmt.Fprintf(&b, " %s is not read from a checkout's file — %s", k.Key, k.Reason)
	}
	if e.User != "" {
		verb := " set it in "
		if len(e.Keys) > 1 {
			verb = " set them in "
		}
		b.WriteString(";" + verb + e.User)
	}
	return b.String()
}

// LayerProject reads the checkout's file at path over cfg, key by key, and
// reports what it set. A key the file names replaces the user's; a key it
// does not name is untouched, which is what lets a repository state the two
// things that are true of it without restating the provider and the key for
// everyone who clones it.
//
// The merge is driven by the keys the file actually wrote rather than by
// which of them are non-zero, so a checkout that turns something off — a
// `silent_mode = false` over a user's true — is honoured. Reading zero as
// unset would make "off" the one answer a project could not give.
func LayerProject(cfg Config, path string) (Config, Project, error) {
	var over Config
	meta, err := toml.DecodeFile(path, &over)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, Project{}, nil
		}
		return cfg, Project{}, err
	}
	if err := unknownKeys(path, meta.Undecoded()); err != nil {
		return cfg, Project{}, err
	}
	if err := refusedKeys(path, meta); err != nil {
		return cfg, Project{}, err
	}
	root := filepath.Dir(filepath.Dir(path))
	proj := Project{Path: path, Display: relativeToRoot(root, path)}
	for _, k := range meta.Keys() {
		key := k.String()
		// A table is the header over the keys beneath it and decides
		// nothing by itself; the keys under it each arrive here in turn.
		switch meta.Type(k...) {
		case "Hash", "ArrayHash":
			continue
		}
		if err := layerKey(&cfg, over, key, root); err != nil {
			return cfg, Project{}, fmt.Errorf("config %s: %w", path, err)
		}
		proj.Keys = append(proj.Keys, key)
	}
	return cfg, proj, nil
}

// refusedKeys collects what the file may not set, one entry per refusal
// rather than per line: a `[sandbox]` with three keys under it is one
// mistake, and naming all four would say there were four. The key named is
// the first thing the file wrote under that refusal — the table's own header
// where it wrote one — because that is the line the person goes to delete. A
// header counts by itself, so an empty `[sandbox]` is refused for the reason
// a full one is.
func refusedKeys(path string, meta toml.MetaData) error {
	var keys []ProjectKey
	var seen []string
	for _, k := range meta.Keys() {
		key := k.String()
		under, reason := refusalFor(key)
		if reason == "" || slices.Contains(seen, under) {
			continue
		}
		seen = append(seen, under)
		keys = append(keys, ProjectKey{Key: key, Reason: reason})
	}
	if len(keys) == 0 {
		return nil
	}
	return &ProjectKeyError{Path: path, Keys: keys, User: WritePath()}
}

// layerKey copies one key's value out of the checkout's config and into the
// user's, unioning where the key is one of the three that extend. root is
// the checkout's, so a relative scope directory the file names is the
// directory beside it rather than one beside whoever ran the command.
func layerKey(dst *Config, src Config, key, root string) error {
	from, ok := fieldAt(reflect.ValueOf(src), strings.Split(key, "."))
	if !ok {
		return fmt.Errorf("%s", UnknownKeyMessage(key))
	}
	return atField(reflect.ValueOf(dst).Elem(), strings.Split(key, "."), func(f reflect.Value) error {
		if !from.Type().AssignableTo(f.Type()) {
			return fmt.Errorf("config key %s: a %s cannot be layered over a %s", key, from.Type(), f.Type())
		}
		if slices.Contains(unionKeys, key) {
			add, _ := from.Interface().([]string)
			held, _ := f.Interface().([]string)
			if key == "behavior.scope_dirs" {
				add = rootedDirs(root, add)
			}
			f.Set(reflect.ValueOf(union(held, add)))
			return nil
		}
		f.Set(from)
		return nil
	})
}

// union is the user's entries with the checkout's added, keeping the order
// each was written in and dropping a repeat. The user's come first because
// theirs is the list that was there before this checkout was cloned.
func union(held, add []string) []string {
	out := slices.Clone(held)
	for _, entry := range add {
		if !slices.Contains(out, entry) {
			out = append(out, entry)
		}
	}
	return out
}

// rootedDirs resolves the checkout's scope directories against the checkout.
// A project that names `../shared` means the directory beside the repository
// and not one beside wherever the session happened to be opened, which is
// the only reading that is the same in every clone.
func rootedDirs(root string, dirs []string) []string {
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if dir != "" && !filepath.IsAbs(dir) {
			dir = filepath.Clean(filepath.Join(root, dir))
		}
		out = append(out, dir)
	}
	return out
}

// relativeToRoot states the file from the checkout's root, so two sessions
// opened at two depths of one repository name it the same way.
func relativeToRoot(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// OverriddenNote is what a write to the user's file has to say when the
// checkout's file sets the same key: the value was written and is not what
// this directory will use. It is empty when nothing the write touched is
// overridden.
//
// The write still happens, the way it does under a flag or an environment
// variable that outranks the file. The user's file is read in every other
// checkout, so refusing the write would punish them for where they were
// standing; what must not happen is a confirmation that reads as though the
// value took effect here.
func (p Project) OverriddenNote(keys ...string) string {
	var hit []string
	for _, key := range keys {
		if p.Sets(key) && !slices.Contains(hit, key) {
			hit = append(hit, key)
		}
	}
	if len(hit) == 0 {
		return ""
	}
	verb := " is set by "
	if len(hit) > 1 {
		verb = " are set by "
	}
	return strings.Join(hit, ", ") + verb + p.Display +
		" in this checkout, which outranks your file here — what you wrote stands wherever that file does not"
}
