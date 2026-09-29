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
	"slices"
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

const (
	platformBegin = "<!-- BEGIN generated platform keys — written by `make docs` from the Mac's table in internal/ui/keys/platform.go; edit the table, not this. -->"
	platformEnd   = "<!-- END generated platform keys -->"
)

// PlatformReference is every key a Mac ships differently from Linux and
// Windows, as the document prints it: the name a file writes it by, what it
// does, and the keystrokes on each, in the order the register declares them.
func PlatformReference() string {
	var b strings.Builder
	b.WriteString(platformBegin + "\n\n")
	b.WriteString("| Key | Does | Linux and Windows | A Mac |\n|---|---|---|---|\n")
	for _, g := range keyboard(true) {
		for _, a := range g.Acts {
			linux, _ := ShippedOn("linux", a.Name)
			darwin, _ := ShippedOn("darwin", a.Name)
			if slices.Equal(linux, darwin) {
				continue
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", a.Name, cell(a.Words), keystrokes(linux), keystrokes(darwin))
		}
	}
	b.WriteString("\n" + platformEnd)
	return b.String()
}

// PlatformReferenceIn is the document with the platform region replaced.
func PlatformReferenceIn(doc string) (string, bool, error) {
	return regionIn(doc, platformBegin, platformEnd, PlatformReference(), "platform keys")
}

// WritePlatformReference rewrites the platform region of the document at
// path and reports whether it had drifted.
func WritePlatformReference(path string, write bool) (stale bool, err error) {
	return writeRegion(path, write, PlatformReferenceIn)
}

// cell escapes what would end a column early.
func cell(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

// ReservedReferenceIn is the document with the generated region replaced,
// and whether that changed anything. A document with no markers is an error
// rather than an append.
func ReservedReferenceIn(doc string) (string, bool, error) {
	return regionIn(doc, reservedBegin, reservedEnd, ReservedReference(), "reserved keys")
}

// WriteReservedReference rewrites the generated region of the document at
// path and reports whether it had drifted; `make docs` writes, the test
// checks.
func WriteReservedReference(path string, write bool) (stale bool, err error) {
	return writeRegion(path, write, ReservedReferenceIn)
}

// regionIn replaces the text between two markers with body.
func regionIn(doc, begin, end, body, what string) (string, bool, error) {
	i := strings.Index(doc, begin)
	j := strings.Index(doc, end)
	if i < 0 || j < i {
		return "", false, fmt.Errorf("the %s markers are not in the document", what)
	}
	out := doc[:i] + body + doc[j+len(end):]
	return out, out != doc, nil
}

// writeRegion rewrites a document through one of the In functions.
func writeRegion(path string, write bool, in func(string) (string, bool, error)) (stale bool, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	out, changed, err := in(string(raw))
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

// The keymap's reference and its scaffold, written from the register for the
// reason the reserved inventory is: the names a keymap file uses are the
// register's field names, and a list of them typed out by hand is a list that
// is wrong the first time a key is added
// (docs/capabilities/configuration.md#the-keymap-file).

const (
	keymapBegin = "<!-- BEGIN generated keymap reference — written by `make docs` from the register in internal/ui/keys; edit the register, not this. -->"
	keymapEnd   = "<!-- END generated keymap reference -->"
)

// KeymapReference is every key the register declares as a table: the name a
// file writes it by, the keystrokes it ships with on Linux and Windows and on
// a Mac, what it does, and whether a file may move it. It is written from the
// platforms' shipped keyboards rather than from this process's, so neither a
// keymap nor the platform of the machine that ran `make docs` can reach the
// document, and a test that has moved a key does not find it stale.
func KeymapReference() string {
	var b strings.Builder
	b.WriteString(keymapBegin + "\n\n")
	b.WriteString("| Key | Ships as | On a Mac | Does | A file moves it |\n|---|---|---|---|---|\n")
	for _, g := range keyboard(true) {
		for _, a := range g.Acts {
			moves := "yes"
			if !a.Movable {
				moves = "no"
			}
			linux, _ := ShippedOn("linux", a.Name)
			mac := "the same"
			if darwin, _ := ShippedOn("darwin", a.Name); !slices.Equal(darwin, linux) {
				mac = keystrokes(darwin)
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n", a.Name, keystrokes(linux), mac, cell(a.Words), moves)
		}
	}
	b.WriteString("\n" + keymapEnd)
	return b.String()
}

// keystrokes is a list of keys as the reference prints them. A space is
// quoted, because a code span holding one space draws as nothing.
func keystrokes(keys []string) string {
	shown := make([]string, len(keys))
	for i, k := range keys {
		if k == " " {
			k = `" "`
		}
		shown[i] = "`" + cell(k) + "`"
	}
	return strings.Join(shown, ", ")
}

// KeymapReferenceIn is the document with the keymap region replaced.
func KeymapReferenceIn(doc string) (string, bool, error) {
	return regionIn(doc, keymapBegin, keymapEnd, KeymapReference(), "keymap reference")
}

// WriteKeymapReference rewrites the keymap region of the document at path
// and reports whether it had drifted.
func WriteKeymapReference(path string, write bool) (stale bool, err error) {
	return writeRegion(path, write, KeymapReferenceIn)
}

// Scaffold is a keymap file holding every key shhh ships, at the keystrokes
// it ships with, each line commented out — so the file as written changes
// nothing, and uncommenting a line is how a key moves. It is the register's
// own shape, one table per group, and the groups a file cannot reach are
// named in the opening comment rather than written as lines that would be
// refused.
func Scaffold() string {
	var b strings.Builder
	b.WriteString(`# shhh keybindings.
#
# Every key shhh ships, one table per group of keys, at the keystrokes it
# ships with. Each line is commented out, so this file changes nothing until
# you uncomment one. A value is one keystroke or a list of them; the words
# after a line are what the key does, and they stay the program's.
#
# The file is applied whole or refused whole, and the keyboard shhh ships runs
# instead of a refused one. It is refused if it would:
#   - leave one surface answering a keystroke with two acts;
#   - put a destructive act on a movement key;
#   - put a bare key where the draft can take text;
#   - give a key that moves both ways one half of its pair (back first, then on);
#   - move a key onto a chord the desktop or the terminal takes.
# A line naming a key shhh has since given up is read and does nothing, and
# shhh doctor names it.
# ` + "`shhh keys check`" + ` reads this file the way a session will, without starting one.
# See docs/capabilities/configuration.md#the-keymap-file.
`)
	var fixedNames []string
	for _, g := range keyboard(true) {
		if !g.Movable {
			for _, a := range g.Acts {
				fixedNames = append(fixedNames, a.Name)
			}
			continue
		}
		table := ""
		for _, a := range g.Acts {
			if t := tableOf(a.Name); t != table {
				table = t
				fmt.Fprintf(&b, "\n[%s]\n", table)
			}
			b.WriteString(scaffoldRow(table, a))
		}
	}
	if len(fixedNames) > 0 {
		b.WriteString("\n# These are declared and are not a file's to move: a line naming one is\n# refused as naming no key.\n")
		b.WriteString(wrapComment(strings.Join(fixedNames, ", "), 78))
	}
	return b.String()
}

// tableOf is the table a key's line sits under: its name less the last part.
func tableOf(name string) string { return name[:strings.LastIndex(name, ".")] }

// scaffoldRow is one key as the scaffold writes it under its table: commented
// out, at the keystrokes it ships with, with what it does beside it. The
// update writes the keys a file lacks through this too, so an added row reads
// as the scaffold's own.
func scaffoldRow(table string, a Act) string {
	return fmt.Sprintf("# %s = %s  # %s\n", a.Name[len(table)+1:], tomlKeys(a.Shipped), a.Words)
}

// tomlKeys is a binding's keystrokes as a TOML value: one as a string, several
// as a list.
func tomlKeys(keys []string) string {
	quoted := make([]string, len(keys))
	for i, k := range keys {
		quoted[i] = fmt.Sprintf("%q", k)
	}
	if len(quoted) == 1 {
		return quoted[0]
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// wrapComment breaks a line of names into indented comment lines no wider
// than width.
func wrapComment(text string, width int) string {
	var b strings.Builder
	line := "#  "
	for _, word := range strings.Fields(text) {
		if len(line)+1+len(word) > width && line != "#  " {
			b.WriteString(line + "\n")
			line = "#  "
		}
		line += " " + word
	}
	b.WriteString(line + "\n")
	return b.String()
}
