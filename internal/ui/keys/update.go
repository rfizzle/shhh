package keys

// An older keymap file, read against the register and brought up to date.
//
// The register is the file's version the way the settings table is the
// settings file's: there is no number in the file, so "behind" is read off
// the file's own text — the keys a file may move that it neither sets nor
// lists as a commented row. The update adds those rows as the scaffold
// writes them, under the table they belong to where the file has it and as
// a table of their own at the end where it does not, and every byte already
// there stays where it was: the person's own bindings, their comments, their
// order (docs/capabilities/configuration.md#an-older-file-is-brought-up-to-date).

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// KeymapBehind is what a keymap file lacks against the register.
type KeymapBehind struct {
	// Missing is every key a file may move that the file neither sets nor
	// lists as a commented row, by the name a file writes it as, in the
	// order the register declares them.
	Missing []string
	// Listed says the file lists at least one key as a commented row — it
	// was written from the scaffold, or in its shape.
	Listed bool
}

// KeysBehind is how many missing keys count against the file. A file that
// lists no key as a commented row was never a list of every key — it is the
// three lines somebody wrote to move three keys — so no key is new to it.
// The update still adds them when it is asked to.
func (b KeymapBehind) KeysBehind() int {
	if !b.Listed {
		return 0
	}
	return len(b.Missing)
}

// KeymapOutdated reads the keymap file at path against the register. A file
// that is not there is behind by nothing: `config init` is what writes one.
func KeymapOutdated(path string) (KeymapBehind, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return KeymapBehind{}, nil
	}
	if err != nil {
		return KeymapBehind{}, err
	}
	r, err := readKeymapText(string(raw))
	if err != nil {
		return KeymapBehind{}, fmt.Errorf("%s: %w", path, err)
	}
	return r.behind(), nil
}

// KeymapUpdated is the file at path with the keys it lacks added as
// commented rows, and how many were added; the text comes back as it is,
// with zero, when nothing is missing. A file that does not decode is refused
// rather than guessed at, and so is a result that would bind a single key
// differently from the file it was made from — the rows added are comments,
// so anything else is a fault here.
func KeymapUpdated(path string) (string, int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	text := string(raw)
	r, err := readKeymapText(text)
	if err != nil {
		return "", 0, fmt.Errorf("%s: not updated, the file does not read: %w", path, err)
	}
	b := r.behind()
	if len(b.Missing) == 0 {
		return text, 0, nil
	}
	out := r.withRows(b.Missing)
	after, err := readKeymapText(out)
	if err != nil || !reflect.DeepEqual(after.set, r.set) {
		return "", 0, fmt.Errorf("%s: not updated, the result would not bind every key as the file does", path)
	}
	return out, len(b.Missing), nil
}

// keymapText is a keymap file as the update reads it: its lines, the keys it
// sets, the keys it lists as commented rows, and where each table's header is.
type keymapText struct {
	lines []string
	// set is what the file binds, decoded; listed is every key a commented
	// row names under the table it sits in.
	set    map[string][]string
	listed map[string]bool
	// headers is the line each table's first header is on.
	headers map[string]int
	// eol is the line break the file uses, so an added row matches it.
	eol string
}

// commentedRow is a commented line shaped like a binding: `# copy = "c"`.
var commentedRow = regexp.MustCompile(`^#+\s*([A-Za-z0-9_-]+)\s*=`)

func readKeymapText(text string) (keymapText, error) {
	var raw map[string]any
	if _, err := toml.Decode(strings.TrimPrefix(text, "\uFEFF"), &raw); err != nil {
		return keymapText{}, err
	}
	r := keymapText{
		lines:   strings.SplitAfter(text, "\n"),
		set:     map[string][]string{},
		listed:  map[string]bool{},
		headers: map[string]int{},
		eol:     "\n",
	}
	if strings.Contains(text, "\r\n") {
		r.eol = "\r\n"
	}
	if err := flatten("", raw, r.set); err != nil {
		return keymapText{}, err
	}
	table := ""
	for i, line := range r.lines {
		trimmed := strings.TrimSpace(strings.TrimPrefix(line, "\uFEFF"))
		if t, ok := headerOf(trimmed); ok {
			table = t
			if _, seen := r.headers[t]; !seen {
				r.headers[t] = i
			}
			continue
		}
		if m := commentedRow.FindStringSubmatch(trimmed); m != nil && table != "" {
			r.listed[table+"."+m[1]] = true
		}
	}
	return r, nil
}

// headerOf is the table a `[table]` line opens, with its parts unquoted. An
// array of tables is not a keymap's shape and is not read as a header.
func headerOf(line string) (string, bool) {
	if !strings.HasPrefix(line, "[") || strings.HasPrefix(line, "[[") {
		return "", false
	}
	end := strings.Index(line, "]")
	if end < 0 {
		return "", false
	}
	parts := strings.Split(line[1:end], ".")
	for i, p := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(p), `"'`)
	}
	return strings.Join(parts, "."), true
}

// behind names the movable keys the file neither sets nor lists.
func (r keymapText) behind() KeymapBehind {
	var b KeymapBehind
	for _, g := range keyboard(true) {
		if !g.Movable {
			continue
		}
		for _, a := range g.Acts {
			if r.listed[a.Name] {
				b.Listed = true
			}
			if _, set := r.set[a.Name]; !set && !r.listed[a.Name] {
				b.Missing = append(b.Missing, a.Name)
			}
		}
	}
	return b
}

// withRows is the file with a commented row for each named key: under its
// table's last line where the file has the table — before the blank lines
// that separate it from the next — and in a table of its own at the end
// where it does not, in the register's order either way.
func (r keymapText) withRows(names []string) string {
	acts := map[string]Act{}
	for _, g := range keyboard(true) {
		for _, a := range g.Acts {
			acts[a.Name] = a
		}
	}
	lines := slices.Clone(r.lines)
	if last := lines[len(lines)-1]; last == "" {
		lines = lines[:len(lines)-1]
	} else if !strings.HasSuffix(last, "\n") {
		// The rows go after the file's last line, which has to end first.
		lines[len(lines)-1] = last + r.eol
	}
	at := map[int]string{}
	var tail strings.Builder
	var newTables []string
	fresh := map[string]string{}
	for _, name := range names {
		table := tableOf(name)
		row := strings.ReplaceAll(scaffoldRow(table, acts[name]), "\n", r.eol)
		h, ok := r.headers[table]
		if !ok {
			if _, seen := fresh[table]; !seen {
				newTables = append(newTables, table)
			}
			fresh[table] += row
			continue
		}
		end := h + 1
		for end < len(lines) {
			if _, header := headerOf(strings.TrimSpace(lines[end])); header {
				break
			}
			end++
		}
		for end > h+1 && strings.TrimSpace(lines[end-1]) == "" {
			end--
		}
		at[end] += row
	}
	for _, table := range newTables {
		tail.WriteString(r.eol + "[" + table + "]" + r.eol + fresh[table])
	}
	var b strings.Builder
	for i, line := range lines {
		b.WriteString(at[i])
		b.WriteString(line)
	}
	b.WriteString(at[len(lines)])
	b.WriteString(tail.String())
	return b.String()
}
