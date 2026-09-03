package config

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// UnknownKeyError is a config file naming a key no setting reads. It refuses
// the load rather than warning, because a warning on the alternate screen is
// painted over and one on stderr before a headless run lands in a log nobody
// tails — and meanwhile the person reads `[behaviour]` in their own file and
// cannot see why it is not in effect. That is the one failure the whole
// arrangement exists to prevent
// (docs/capabilities/configuration.md#one-file-one-format-one-resolution-order).
type UnknownKeyError struct {
	Path string
	Keys []UnknownKey
}

// UnknownKey is one refused key and, when a known key is within an edit or
// two of it, the one it was probably meant to be.
type UnknownKey struct {
	Key     string
	Nearest string
}

func (e *UnknownKeyError) Error() string {
	parts := make([]string, len(e.Keys))
	for i, k := range e.Keys {
		parts[i] = fmt.Sprintf("%q", k.Key)
		if k.Nearest != "" {
			parts[i] += fmt.Sprintf(" (did you mean %q?)", k.Nearest)
		}
	}
	noun := "unknown key "
	if len(e.Keys) > 1 {
		noun = "unknown keys "
	}
	return "config " + e.Path + ": " + noun + strings.Join(parts, ", ")
}

// keyDistance is how far a misspelling may be from a known key and still be
// offered it. Two edits covers a dropped letter, a doubled one and a
// transposed pair, which is what a hand-typed key is wrong by; three starts
// matching keys that were never meant. A key of four letters or fewer gets
// one edit, because at that length two edits reaches most of the alphabet —
// `top` is two from `lsp`, and offering it would be a second wrong answer.
func keyDistance(seg string) int {
	if len([]rune(seg)) <= 4 {
		return 1
	}
	return 2
}

// unknownKeys turns the decoder's undecoded list into the error, or nil when
// everything decoded. The decoder reports a misspelled table and every key
// beneath it, in the order the file wrote them, so the list is put in depth
// order first and a key under a prefix already refused is dropped: naming
// `behaviour.silent_mode` beside `behaviour` would suggest two mistakes where
// there is one, and a `[behaviour.deep]` written above its `[behaviour]`
// would otherwise be named before the table that explains it.
func unknownKeys(path string, undecoded []toml.Key) error {
	sorted := slices.Clone(undecoded)
	slices.SortStableFunc(sorted, func(a, b toml.Key) int {
		if d := len(a) - len(b); d != 0 {
			return d
		}
		return strings.Compare(a.String(), b.String())
	})
	var keys []UnknownKey
	for _, k := range sorted {
		if underRefused(keys, k) {
			continue
		}
		keys = append(keys, UnknownKey{Key: k.String(), Nearest: nearestKey(k)})
	}
	if len(keys) == 0 {
		return nil
	}
	return &UnknownKeyError{Path: path, Keys: keys}
}

func underRefused(refused []UnknownKey, k toml.Key) bool {
	name := k.String()
	for _, r := range refused {
		if strings.HasPrefix(name, r.Key+".") {
			return true
		}
	}
	return false
}

// nearestKey walks the config struct along the key's path and, at the first
// segment no field matches, offers the sibling within keyDistance of it with
// the rest of the path kept — so `behaviour` finds `behavior`,
// `mcp.servers.foo.commnd` finds `mcp.servers.foo.command`, and
// `agents.profles.writer` finds `agents.profiles.writer` rather than a table
// with the role cut off. A segment under a map (a server's name, a profile's
// role) matches anything, because those names are the user's. Segments are
// matched in lower case, as the decoder matches them. The empty string is no
// offer.
func nearestKey(k toml.Key) string {
	t := reflect.TypeOf(Config{})
	for i, seg := range k {
		seg = strings.ToLower(seg)
		switch t.Kind() {
		case reflect.Map:
			t = t.Elem()
			continue
		case reflect.Pointer:
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return ""
		}
		names := tomlFields(t)
		if next, ok := names[seg]; ok {
			t = next
			continue
		}
		best, bestDist := "", keyDistance(seg)+1
		for name := range names {
			if d := editDistance(seg, name); d < bestDist || (d == bestDist && name < best) {
				best, bestDist = name, d
			}
		}
		if best == "" {
			return ""
		}
		fixed := append(append(append([]string{}, k[:i]...), best), k[i+1:]...)
		return strings.Join(fixed, ".")
	}
	return ""
}

// tomlFields is a struct's key names as the file spells them, each with the
// type beneath it.
func tomlFields(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
		switch name {
		case "-":
			continue
		case "":
			name = f.Name
		}
		out[name] = f.Type
	}
	return out
}

// editDistance is the optimal string alignment distance: insertions,
// deletions, substitutions and one transposition of adjacent letters each
// count one, so `modle` is one edit from `model` rather than two.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev2 := make([]int, len(rb)+1)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(rb)]
}
