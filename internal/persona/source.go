package persona

// A profile opened from its file. The drafter's surface edits a file that
// already exists the way it edits a draft, and saving it is not writing a
// new file: only the prompt and the fields revised on the surface change,
// and every other key and comment is kept byte for byte, so a hand-written
// `mode = "read-only"` or a comment header survives the save. The file is
// never rendered again through Render.
// See docs/capabilities/subagents.md#an-older-profile-is-moved-into-sections-not-rewritten.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/diff"
)

// Source is a profile file as it stood when it was opened: the definition
// the loader read, and the bytes it was read from, so a save can tell that
// the file changed under it and write against exactly what was opened.
type Source struct {
	// Path is the profile's TOML file.
	Path string
	// Def is the profile as the loader read it, its prompt resolved.
	Def config.AgentDefinition
	// Raw is the file's bytes when it was opened.
	Raw []byte
	// PromptPath is the prompt_file the profile points at, resolved against
	// the profile's directory, and PromptRaw its bytes; both empty for a
	// profile whose prompt is inline.
	PromptPath string
	PromptRaw  []byte
}

// Open reads a profile file for editing on the drafter's surface. A file the
// loader refuses is refused here in the loader's words.
func Open(path string) (*Source, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	def, err := config.ReadAgentFile(path, raw, os.ReadFile)
	if err != nil {
		return nil, err
	}
	src := &Source{Path: path, Def: def, Raw: raw}
	var keys struct {
		PromptFile string `toml:"prompt_file"`
	}
	// The loader resolved the prompt and forgot where it came from; the save
	// needs the file it lives in, so that one key is read again.
	if _, err := toml.Decode(string(raw), &keys); err == nil && keys.PromptFile != "" {
		src.PromptPath = promptPath(path, keys.PromptFile)
		if src.PromptRaw, err = os.ReadFile(src.PromptPath); err != nil {
			return nil, err
		}
	}
	return src, nil
}

// promptPath is a prompt_file resolved the way the loader resolves it.
func promptPath(profile, file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(filepath.Dir(profile), file)
}

// Older reports a profile in the older shape (config's Current).
func (s *Source) Older() bool { return !s.Def.Current() }

// Rewrite is the profile's files as saving d would leave them: the TOML with
// only the keys d changed rewritten in place, and the prompt file's new
// text, or nil where its prompt is inline or unchanged.
func (s *Source) Rewrite(d Draft) (file, prompt []byte, err error) {
	entries, err := scanTOML(string(s.Raw))
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", s.Path, err)
	}
	ed := tomlEditor{text: string(s.Raw), entries: entries}
	def := s.Def
	if d.Prompt != def.Prompt {
		if s.PromptPath != "" {
			prompt = []byte(strings.TrimSpace(d.Prompt) + "\n")
		} else {
			ed.set("prompt", tomlPrompt(d.Prompt))
		}
	}
	if !sameTiers(d.Permissions, def.Permissions) {
		ed.set("permissions", tomlArray(d.Permissions))
	}
	if !slices.Equal(d.Tools, def.Tools) {
		ed.setOrRemove("tools", tomlArray(d.Tools), len(d.Tools) == 0)
	}
	if d.Intent != def.Intent {
		ed.setOrRemove("intent", tomlString(d.Intent), d.Intent == "")
	}
	if !slices.Equal(d.Deny, def.Deny) {
		ed.setOrRemove("deny", tomlArray(d.Deny), len(d.Deny) == 0)
	}
	return []byte(ed.apply()), prompt, nil
}

// Diff is the change saving d would make, as unified-diff lines: the file as
// it stands against the file as it would be written. For a profile whose
// prompt lives in a prompt_file the prompt's change is that file's.
func (s *Source) Diff(d Draft) ([]string, error) {
	file, prompt, err := s.Rewrite(d)
	if err != nil {
		return nil, err
	}
	var out []string
	add := func(name string, before, after []byte) {
		hunks := diff.Compute(string(before), string(after))
		if len(hunks) == 0 {
			return
		}
		text := diff.Unified(name+" as it stands", name+" as it would be written", hunks)
		out = append(out, strings.Split(strings.TrimRight(text, "\n"), "\n")...)
	}
	add(filepath.Base(s.Path), s.Raw, file)
	if prompt != nil {
		add(filepath.Base(s.PromptPath), s.PromptRaw, prompt)
	}
	return out, nil
}

// SaveOpened writes d back over the file it was opened from. A file changed
// on disk since it was opened is not overwritten: the change is someone's
// and the draft was made against what came before it. What would be written
// is put to the loader first, so a profile it would refuse is refused in its
// words with the file untouched.
func SaveOpened(s *Source, d Draft) (string, error) {
	if changed(s.Path, s.Raw) || (s.PromptPath != "" && changed(s.PromptPath, s.PromptRaw)) {
		return s.Path, fmt.Errorf("%s changed on disk since it was opened; nothing was written", s.Path)
	}
	file, prompt, err := s.Rewrite(d)
	if err != nil {
		return s.Path, err
	}
	read := func(p string) ([]byte, error) {
		if prompt != nil && p == s.PromptPath {
			return prompt, nil
		}
		return os.ReadFile(p)
	}
	if _, err := config.ReadAgentFile(s.Path, file, read); err != nil {
		return s.Path, err
	}
	if prompt != nil {
		if err := writeKeepingMode(s.PromptPath, prompt); err != nil {
			return s.Path, err
		}
	}
	if !bytes.Equal(file, s.Raw) {
		if err := writeKeepingMode(s.Path, file); err != nil {
			return s.Path, err
		}
	}
	return s.Path, nil
}

// changed reports a file whose bytes are no longer the ones opened, or that
// can no longer be read.
func changed(path string, opened []byte) bool {
	now, err := os.ReadFile(path)
	return err != nil || !bytes.Equal(now, opened)
}

// writeKeepingMode replaces a file's contents and keeps its permissions.
func writeKeepingMode(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(path, data, mode)
}

// sameTiers compares two permission lists as the loader reads them: a set,
// with read implied whether it is listed or not.
func sameTiers(a, b []string) bool {
	norm := func(list []string) []string {
		var out []string
		for _, p := range list {
			p = strings.ToLower(strings.TrimSpace(p))
			if p != "" && p != config.PermissionRead && !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
		slices.Sort(out)
		return out
	}
	return slices.Equal(norm(a), norm(b))
}

// tomlPrompt is a prompt as Render writes one: a multi-line basic string.
func tomlPrompt(prompt string) string {
	escaped := strings.ReplaceAll(strings.TrimSpace(prompt), `\`, `\\`)
	return "\"\"\"\n" + strings.ReplaceAll(escaped, `"""`, `""\"`) + "\n\"\"\""
}

// tomlArray is a list of strings as one TOML line.
func tomlArray(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = tomlString(s)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
