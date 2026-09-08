// Package secret holds the values a session may use but the model may never
// see: API keys, tokens, passphrases. A secret is declared by name, handed
// to every command the model runs as an environment variable of that name,
// and scrubbed from everything that comes back — tool results, command
// output, the user's own typing — before any of it reaches a provider or a
// file that outlives the session.
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

// maskedSuffixes are the endings a variable name has when it holds a
// credential by convention — AWS_SECRET_ACCESS_KEY, GITHUB_TOKEN,
// STRIPE_SECRET. It is three suffixes and not a cleverer rule because the
// mask has to be explainable in one sentence to the person turning it off
// and to the model finding a variable unset.
var maskedSuffixes = [...]string{"_KEY", "_SECRET", "_TOKEN"}

// MaskedEnvName reports whether a variable of this name is kept out of an
// assistant command's environment while the mask is on. Only the name
// decides: the mask exists for the credentials nobody declared, so there is
// no value to compare against. A secret the user did declare is exempt,
// because it is put back by name after the mask has run — the user asked
// for that one to be reachable.
// See docs/capabilities/secrets.md#the-names-that-do-not-travel.
func MaskedEnvName(name string) bool {
	up := strings.ToUpper(name)
	for _, suffix := range maskedSuffixes {
		if strings.HasSuffix(up, suffix) {
			return true
		}
	}
	return false
}

// entry is one secret with the forms it is scrubbed in, computed once at
// Add so a scrub costs no allocation per secret.
type entry struct {
	name  string
	value string
	// ph is Placeholder(name), which every match of this entry writes.
	ph string
	// encoded are the whole-value transformations a command might print
	// instead of the value itself: base64 in each alphabet, hex, URL
	// escaping. A model that asks for `echo $KEY | base64` has not seen the
	// key, and this is what keeps that true.
	encoded []string
}

func newEntry(name, value string) entry {
	e := entry{name: name, value: value, ph: Placeholder(name)}
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
	return e
}

// gateWords is the size of the prefilter bitset: one bit per two-byte
// prefix, so 65536 bits in 8 KB. Two bytes and not one, because one does
// not filter. Measured over 2.7 MB of credential-free log text against five
// declared secrets: their 133 windows have 55 distinct first bytes, which
// 76% of the text's positions hit — a token is alphanumeric and so is most
// of a log — against 111 distinct two-byte prefixes, which 9.9% do. The map
// lookup is what costs, and this is what the text has to get past to reach
// one.
const gateWords = (1 << 16) / 64

// index is the shared window map every fragment scrub reads: one map over
// all entries rather than one per entry, so the text is walked once however
// many secrets are declared. It is built whole by Add and Remove and then
// never written, so Scrub can take a snapshot pointer under the read lock
// and let go of it.
type index struct {
	entries []entry
	// windows maps every minFragment-byte run of every declared value to
	// the entry it came from. A run declared by two secrets belongs to the
	// first that declared it, which is the one whose placeholder the
	// per-entry loop used to write.
	windows map[string]int
	// gate has a bit set for the first two bytes of every window. A
	// position whose bit is clear cannot start one, which is what makes
	// text with no secret in it near-free — the same trick, and the same
	// reason, as the shape prefilter's marker search.
	gate [gateWords]uint64
}

// gateBit is the bit index for a two-byte prefix.
func gateBit(a, b byte) uint32 { return uint32(a)<<8 | uint32(b) }

// newIndex builds the shared window map. Entries shorter than minFragment
// contribute nothing: they are scrubbed whole or not at all, which is what
// the whole-value pass does.
func newIndex(entries []entry) *index {
	ix := &index{entries: entries, windows: make(map[string]int)}
	for n := range entries {
		value := entries[n].value
		for i := 0; i+minFragment <= len(value); i++ {
			w := value[i : i+minFragment]
			if _, dup := ix.windows[w]; dup {
				continue
			}
			ix.windows[w] = n
			k := gateBit(value[i], value[i+1])
			ix.gate[k>>6] |= 1 << (k & 63)
		}
	}
	return ix
}

// Vault is the session's secrets. It is safe for concurrent use: the
// executor scrubs on a background goroutine while /secret adds on the UI's.
type Vault struct {
	mu      sync.RWMutex
	entries []entry
	// idx is the fragment scrub's index over entries, rebuilt whenever
	// entries change and never mutated after. nil is a vault nothing has
	// been declared in.
	idx *index
	// envMask records that this session's commands run with
	// credential-shaped variables stripped from their environment. The
	// vault does not do the stripping — the runner does — but it is what
	// describes the session to the model, and a model that finds
	// $GITHUB_TOKEN unset needs to be told which of the two things
	// happened.
	envMask bool
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
	replaced := false
	for i := range v.entries {
		if v.entries[i].name == name {
			v.entries[i] = e
			replaced = true
			break
		}
	}
	if !replaced {
		v.entries = append(v.entries, e)
	}
	v.reindex()
	return nil
}

// reindex rebuilds the shared window map from entries. It is called with
// the write lock held, and it copies the slice rather than aliasing it:
// Scrub reads the index without the lock, and an Add that appends in place
// would otherwise rewrite an entry a scrub was already walking.
func (v *Vault) reindex() {
	if len(v.entries) == 0 {
		v.idx = nil
		return
	}
	v.idx = newIndex(append([]entry(nil), v.entries...))
}

// SetEnvMask records whether the session masks credential-shaped variables
// out of what a command inherits, so the prompt block can say so. It is set
// once, when the session opens its secrets, before anything reads it.
func (v *Vault) SetEnvMask(on bool) {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.envMask = on
}

// EnvMask reports what SetEnvMask was told. A nil vault masks nothing.
func (v *Vault) EnvMask() bool {
	if v == nil {
		return false
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.envMask
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
			v.reindex()
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
// with the secret's placeholder, and then replaces anything left that
// carries a known credential's shape with the shape's placeholder. It is
// the whole guarantee, so it is the one function every path to the model
// goes through; a nil vault scrubs nothing.
//
// The two passes are in that order and not the other, and a session with no
// secrets at all still runs the second. A declared value that also looks
// like a GitHub token comes out as [secret:NAME] rather than
// [redacted:github-token], because the name is what lets the reader tell
// which of their secrets was used; running the shapes first would throw
// that away for every credential the user bothered to declare.
//
// It is also what the components that write a copy to disk are handed —
// the evidence store's reducer and the process supervisor take this method
// as a plain func(string) string, so neither of them imports this package
// and neither can be given a vault to read values out of. A caller holding
// one of those hands over the method rather than the vault.
func (v *Vault) Scrub(s string) string {
	if v == nil || s == "" {
		return s
	}
	v.mu.RLock()
	ix := v.idx
	v.mu.RUnlock()
	if ix == nil {
		return redact(s)
	}
	// Whole values and encodings first: a base64 form shares no window with
	// the raw value, and a raw value would otherwise be scrubbed one
	// fragment at a time into several placeholders.
	for i := range ix.entries {
		e := &ix.entries[i]
		s = strings.ReplaceAll(s, e.value, e.ph)
		for _, enc := range e.encoded {
			s = strings.ReplaceAll(s, enc, e.ph)
		}
	}
	if len(ix.windows) > 0 {
		s = ix.scrubFragments(s)
	}
	return redact(s)
}

// scrubFragments replaces every run of any declared value in s at least
// minFragment bytes long. A run is found by its first window and extended
// as far as that window's own value still contains it.
//
// It is one walk of s for every secret in the vault, not one each. A
// session with five secrets and a dev server printing a megabyte a second
// pays for this on every byte that comes back, and the per-entry version
// spent five map lookups a byte to answer no five times.
func (ix *index) scrubFragments(s string) string {
	var b strings.Builder
	last := 0
	for i := 0; i+minFragment <= len(s); {
		k := gateBit(s[i], s[i+1])
		if ix.gate[k>>6]&(1<<(k&63)) == 0 {
			i++
			continue
		}
		n, ok := ix.windows[s[i:i+minFragment]]
		if !ok {
			i++
			continue
		}
		value := ix.entries[n].value
		end := i + minFragment
		for end < len(s) && strings.Contains(value, s[i:end+1]) {
			end++
		}
		if last == 0 {
			b.Grow(len(s))
		}
		b.WriteString(s[last:i])
		b.WriteString(ix.entries[n].ph)
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
