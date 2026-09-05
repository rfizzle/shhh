package keys

// The reserved-keys document's tables, written from the table in reserved.go
// rather than by hand, for the reason the settings reference is
// (internal/config/docgen.go): a chord refused in code and listed in prose
// are two places to be wrong, and the one that goes stale is the prose.
// Only the region between the markers is generated; the prose around it —
// what the encoding can carry, the sources — is a person's.

import (
	"fmt"
	"os"
	"strings"
)

const (
	reservedBegin = "<!-- BEGIN generated reserved keys — written by `make docs` from the table in internal/ui/keys/reserved.go; edit the table, not this. -->"
	reservedEnd   = "<!-- END generated reserved keys -->"
)

// ReservedReference is the inventory as the document prints it: one table
// per tier, rows grouped by who takes the chords and where, in the order the
// table declares them; then the chords the shipped keyboard keeps on purpose,
// each with its reason.
func ReservedReference() string {
	var b strings.Builder
	b.WriteString(reservedBegin + "\n")
	for tier := TierDesktop; tier <= TierByte; tier++ {
		fmt.Fprintf(&b, "\n### Tier %c — %s\n\n", 'A'+rune(tier), tier)
		b.WriteString("| Chord | Taken by | On |\n|---|---|---|\n")
		var keys []string
		var taker, on string
		flush := func() {
			if len(keys) == 0 {
				return
			}
			fmt.Fprintf(&b, "| %s | %s | %s |\n", strings.Join(keys, ", "), cell(taker), cell(on))
			keys = nil
		}
		for _, r := range reserved {
			if r.Tier != tier {
				continue
			}
			if r.Taker != taker || r.On != on {
				flush()
				taker, on = r.Taker, r.On
			}
			keys = append(keys, "`"+r.Key+"`")
		}
		flush()
	}
	b.WriteString("\n### Kept on purpose\n\n")
	b.WriteString("The chords the keyboard shhh ships spends although the list names them. Each is a code change beside a sentence, never a keymap file's decision.\n\n")
	b.WriteString("| Chord | Why |\n|---|---|\n")
	for _, k := range sorted(kept) {
		fmt.Fprintf(&b, "| `%s` | %s |\n", k, cell(kept[k]))
	}
	b.WriteString("\n" + reservedEnd)
	return b.String()
}

// cell escapes what would end a column early.
func cell(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

// ReservedReferenceIn is the document with the generated region replaced,
// and whether that changed anything. A document with no markers is an error
// rather than an append.
func ReservedReferenceIn(doc string) (string, bool, error) {
	i := strings.Index(doc, reservedBegin)
	j := strings.Index(doc, reservedEnd)
	if i < 0 || j < i {
		return "", false, fmt.Errorf("the reserved keys markers are not in the document")
	}
	out := doc[:i] + ReservedReference() + doc[j+len(reservedEnd):]
	return out, out != doc, nil
}

// WriteReservedReference rewrites the generated region of the document at
// path and reports whether it had drifted; `make docs` writes, the test
// checks.
func WriteReservedReference(path string, write bool) (stale bool, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	out, changed, err := ReservedReferenceIn(string(raw))
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	if !changed {
		return false, nil
	}
	if !write {
		return true, nil
	}
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	return true, os.WriteFile(path, []byte(out), mode)
}
