// Package secret holds the values a session may use but the model may never
// see: API keys, tokens, passphrases. A secret is declared by name, handed
// to every command the model runs as an environment variable of that name,
// and scrubbed from everything that comes back — tool results, command
// output, the user's own typing — before any of it reaches a provider.
// See docs/capabilities/secrets.md.
package secret

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// nameRe is the shape of a secret's name, which is the shape of an
// environment variable name: that is how the model reaches it.
var nameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reserved are the variables a secret may not shadow. A command whose PATH
// or HOME is a token is not going to run, and the mistake would be reported
// as something else.
var reserved = map[string]bool{"PATH": true, "HOME": true, "SHELL": true, "PWD": true}

// minFragment is the shortest run of a secret that is scrubbed on its own.
// A whole-value match is not enough: `cut -c1-20` prints a prefix, a wrapped
// terminal splits a token over two lines, and a partial key is still a
// leak. Eight bytes is long enough that a real token's windows are effectively
// unique, and short enough that a fifth of a key does not get through.
const minFragment = 8

// Placeholder is what a secret's value becomes in anything the model
// reads. The name is kept so the model can tell which secret it was.
func Placeholder(name string) string { return "[secret:" + name + "]" }

// ValidName reports whether name can be a secret, or why not.
func ValidName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid secret name %q (letters, digits and _, not starting with a digit)", name)
	}
	if reserved[strings.ToUpper(name)] {
		return fmt.Errorf("%s cannot be a secret", name)
	}
	return nil
}

// entry is one secret with the forms it is scrubbed in, computed once at
// Add so a scrub costs no allocation per secret.
type entry struct {
	name  string
	value string
	// encoded are the whole-value transformations a command might print
	// instead of the value itself: base64 in each alphabet, hex, URL
	// escaping. A model that asks for `echo $KEY | base64` has not seen the
	// key, and this is what keeps that true.
	encoded []string
	// windows are every minFragment-byte run of the value, for the
	// fragment scrub.
	windows map[string]struct{}
}

func newEntry(name, value string) entry {
	e := entry{name: name, value: value, windows: map[string]struct{}{}}
	raw := []byte(value)
	seen := map[string]bool{value: true}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			e.encoded = append(e.encoded, s)
		}
	}
	add(base64.StdEncoding.EncodeToString(raw))
	add(base64.RawStdEncoding.EncodeToString(raw))
	add(base64.URLEncoding.EncodeToString(raw))
	add(base64.RawURLEncoding.EncodeToString(raw))
	add(hex.EncodeToString(raw))
	add(strings.ToUpper(hex.EncodeToString(raw)))
	add(url.QueryEscape(value))
	add(url.PathEscape(value))
	if len(value) >= minFragment {
		for i := 0; i+minFragment <= len(value); i++ {
			e.windows[value[i:i+minFragment]] = struct{}{}
		}
	}
	return e
}

// Vault is the session's secrets. It is safe for concurrent use: the
// executor scrubs on a background goroutine while /secret adds on the UI's.
type Vault struct {
	mu      sync.RWMutex
	entries []entry
}

// New returns an empty vault.
func New() *Vault { return &Vault{} }

// Add declares a secret, replacing one of the same name. An empty value is
// refused: a secret that is nothing masks nothing, and an unset variable
// silently becoming an empty one is the kind of mistake that gets debugged
// for an hour.
func (v *Vault) Add(name, value string) error {
	if v == nil {
		return fmt.Errorf("secrets are unavailable in this session")
	}
	if err := ValidName(name); err != nil {
		return err
	}
	if value == "" {
		return fmt.Errorf("secret %s has an empty value", name)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	e := newEntry(name, value)
	for i := range v.entries {
		if v.entries[i].name == name {
			v.entries[i] = e
			return nil
		}
	}
	v.entries = append(v.entries, e)
	return nil
}

// Remove forgets a secret, reporting whether there was one. What was already
// scrubbed stays scrubbed — the placeholders in the conversation are text.
func (v *Vault) Remove(name string) bool {
	if v == nil {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for i := range v.entries {
		if v.entries[i].name == name {
			v.entries = append(v.entries[:i], v.entries[i+1:]...)
			return true
		}
	}
	return false
}

// Len is how many secrets are declared. A nil vault holds none.
func (v *Vault) Len() int {
	if v == nil {
		return 0
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.entries)
}

// Names lists the declared secrets, sorted.
func (v *Vault) Names() []string {
	if v == nil {
		return nil
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	names := make([]string, 0, len(v.entries))
	for _, e := range v.entries {
		names = append(names, e.name)
	}
	sort.Strings(names)
	return names
}

// Environ is the secrets as NAME=value pairs, for a command's environment.
func (v *Vault) Environ() []string {
	if v == nil {
		return nil
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	env := make([]string, 0, len(v.entries))
	for _, e := range v.entries {
		env = append(env, e.name+"="+e.value)
	}
	return env
}

// Scrub replaces every occurrence of every secret in s — the value, its
// common encodings, and any run of it at least minFragment bytes long —
// with the secret's placeholder. It is the whole guarantee, so it is the
// one function every path to the model goes through; a nil vault scrubs
// nothing.
func (v *Vault) Scrub(s string) string {
	if v == nil || s == "" {
		return s
	}
	v.mu.RLock()
	entries := v.entries
	v.mu.RUnlock()
	if len(entries) == 0 {
		return s
	}
	for _, e := range entries {
		ph := Placeholder(e.name)
		// Whole values and encodings first: a base64 form shares no
		// window with the raw value, and a raw value would otherwise be
		// scrubbed one fragment at a time into several placeholders.
		s = strings.ReplaceAll(s, e.value, ph)
		for _, enc := range e.encoded {
			s = strings.ReplaceAll(s, enc, ph)
		}
		if len(e.windows) > 0 {
			s = scrubFragments(s, e, ph)
		}
	}
	return s
}

// scrubFragments replaces every run of e.value in s at least minFragment
// bytes long. A run is found by its first window and extended as far as
// the value still contains it.
func scrubFragments(s string, e entry, ph string) string {
	var b strings.Builder
	last := 0
	for i := 0; i+minFragment <= len(s); {
		if _, ok := e.windows[s[i:i+minFragment]]; !ok {
			i++
			continue
		}
		end := i + minFragment
		for end < len(s) && strings.Contains(e.value, s[i:end+1]) {
			end++
		}
		b.WriteString(s[last:i])
		b.WriteString(ph)
		last, i = end, end
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// WrapExecutor scrubs every tool result on its way back to the agent. It
// sits at the executor rather than at one tool because any tool can surface
// a value: a command prints it, read_file opens the .env it lives in,
// web_fetch returns the page it was posted to.
func (v *Vault) WrapExecutor(next func(name string, args json.RawMessage) (string, error)) func(string, json.RawMessage) (string, error) {
	if v == nil {
		return next
	}
	return func(name string, args json.RawMessage) (string, error) {
		out, err := next(name, args)
		if err != nil {
			return v.Scrub(out), fmt.Errorf("%s", v.Scrub(err.Error()))
		}
		return v.Scrub(out), nil
	}
}
