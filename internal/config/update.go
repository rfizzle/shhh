package config

// An older settings file, read against the table and brought up to date.
//
// The settings table is the file's version: there is no number in the file,
// so "behind" is read off the file's own text — the keys the table has that
// the file neither sets nor lists as a commented row, and the keys it holds
// under a spelling that has since moved (renames.go). The update is the same
// edit-the-text writer `config set` uses rather than a scaffold printed over
// the file: every byte the person wrote stays where they wrote it, the rows
// that arrived since are added as the scaffold writes them, and a renamed key
// is the one line that moves. A re-printed scaffold would have kept the
// values and lost everything else a file holds — the comments, the MCP
// servers, the hooks — and the file is the one thing here that cannot be got
// back (docs/capabilities/configuration.md#an-older-file-is-brought-up-to-date).

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// The command that brings each file up to date, as a refusal or a doctor row
// names it. The person's own file needs --global because the bare command,
// run in a checkout, updates the checkout's.
const (
	UpdateUser    = "shhh config init --global --update"
	UpdateProject = "shhh config init --update"
)

// Behind is what a file is missing against the table.
type Behind struct {
	// New is every key the table has that the file neither sets nor lists
	// as a commented row, as the reference spells it, in the table's order.
	New []string
	// Renamed is every key the file holds under a spelling that moved.
	Renamed []Rename
	// Listed says the file lists at least one key as a commented row — it
	// was written from a scaffold, or in its shape.
	Listed bool
}

// KeysBehind is how many new keys count against the file. A file that lists
// no key as a commented row was never a list of every key — it is three
// lines somebody wrote on purpose — so no key is new to it, and a doctor that
// warned about a hundred absent rows would be asking for a file the person
// chose not to have. `--update` still adds them when it is asked to.
func (b Behind) KeysBehind() int {
	if !b.Listed {
		return 0
	}
	return len(b.New)
}

// Due says the file is behind in a way worth saying.
func (b Behind) Due() bool { return b.KeysBehind() > 0 || len(b.Renamed) > 0 }

// Outdated reads the file at path against the table. project leaves out the
// keys a checkout's file may not decide, which are not missing from one. A
// file that does not exist is behind by nothing: there is nothing to update,
// and `config init` is what writes one.
func Outdated(path string, project bool) (Behind, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Behind{}, nil
	}
	if err != nil {
		return Behind{}, err
	}
	doc, err := parseDocument(strings.TrimPrefix(string(raw), "\uFEFF"))
	if err != nil {
		return Behind{}, fmt.Errorf("config %s: %w", path, err)
	}
	var b Behind
	rs := doc.rows()
	for _, r := range rs {
		if r.commented && r.setting >= 0 {
			b.Listed = true
		}
		if !r.commented {
			if rn, ok := renameOf(strings.Join(r.path, ".")); ok {
				b.Renamed = append(b.Renamed, rn)
			}
		}
	}
	for i, s := range settings {
		if project && RefusedInProject(s.Key) != "" {
			continue
		}
		if !doc.mentions(rs, i) && !b.renamedTo(s) {
			b.New = append(b.New, s.shown())
		}
	}
	return b, nil
}

// renamedTo says a rename the file holds lands on this setting, which is
// then not new: the update writes it under the new name rather than adding a
// commented row for it.
func (b Behind) renamedTo(s Setting) bool {
	for _, r := range b.Renamed {
		if matchPath(strings.Split(s.Key, "."), strings.Split(r.To, ".")) {
			return true
		}
	}
	return false
}

// Updated is the file at path brought up to date, and whether that differs
// from what is there. A file that does not exist comes back as the scaffold,
// which is what an update of nothing is.
//
// Nothing is returned for a file the update cannot be sure of: one that does
// not parse, one naming a key that is neither a setting nor a rename — the
// person has a typo to settle first, and a value moved past it would be a
// guess — and one where a key and its old spelling are both set. The result
// is checked before it is returned: decoded, it must hold every value the
// file held, under the new name where a key moved, and nothing else. The
// rows the update adds are comments, so anything else is a fault here, and
// it is refused rather than written.
func Updated(path string, project bool) (string, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Scaffold(Config{}, project), true, nil
	}
	if err != nil {
		return "", false, err
	}
	update := UpdateUser
	if project {
		update = UpdateProject
	}
	original := string(raw)
	meta, err := toml.Decode(original, &Config{})
	if err != nil {
		return "", false, fmt.Errorf("config %s: not updated, the file does not parse: %w", path, err)
	}
	var unknown []toml.Key
	for _, k := range meta.Undecoded() {
		if _, ok := renameOf(k.String()); !ok {
			unknown = append(unknown, k)
		}
	}
	if err := unknownKeys(path, update, unknown); err != nil {
		return "", false, err
	}
	bom, text := "", original
	if rest, ok := strings.CutPrefix(text, "\uFEFF"); ok {
		bom, text = "\uFEFF", rest
	}
	doc, err := parseDocument(text)
	if err != nil {
		return "", false, fmt.Errorf("config %s: %w", path, err)
	}
	moves, err := doc.moveRenamed()
	if err != nil {
		return "", false, fmt.Errorf("config %s: not updated: %w", path, err)
	}
	if err := doc.addMissing(project); err != nil {
		return "", false, fmt.Errorf("config %s: not updated: %w", path, err)
	}
	if doc.text == text {
		return original, false, nil
	}
	if err := sameValues(text, doc.text, moves); err != nil {
		return "", false, fmt.Errorf("config %s: not updated: %w", path, err)
	}
	return bom + doc.text, true, nil
}

// UpdateFile writes Updated over the file, through a temporary file in the
// same directory and a rename, so a failure part-way leaves the person's
// file as it was. A file already up to date is not written at all, which is
// what keeps it byte for byte what it was. It reports whether it wrote.
func UpdateFile(path string, project bool) (bool, error) {
	// A settings file is often a link into a dotfiles checkout, and the
	// rename would replace the link with a plain file.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	text, changed, err := Updated(path, project)
	if err != nil || !changed {
		return false, err
	}
	return true, replaceFile(path, text)
}

// row is a line that names a key: one the file sets, or one it lists as a
// commented row the way the scaffold writes every key it does not set.
type row struct {
	// path is the key in full, the table it sits under joined to the key as
	// written; table is that table, the header above the line.
	path  []string
	table []string
	// setting is the index of the setting the key is, or -1 for a key the
	// table does not read — an MCP server's field, a hook's.
	setting   int
	commented bool
	start     int
	end       int
}

// rows is every line of the document that names a key, in file order.
func (d *document) rows() []row {
	var out []row
	var table []string
	for _, s := range d.spans {
		switch s.kind {
		case spanHeader:
			table = s.path
		case spanKey:
			out = append(out, row{path: s.path, table: table, setting: settingOf(s.path), start: s.start, end: s.end})
		case spanOther:
			key, ok := commentedKey(d.text[s.start:s.end])
			if !ok {
				continue
			}
			full := append(slices.Clone(table), key...)
			out = append(out, row{path: full, table: table, setting: settingOf(full), commented: true, start: s.start, end: s.end})
		}
	}
	return out
}

// commentedKey reads a comment line that is a key written out and commented,
// `#name = value` as the scaffold writes it or `# name = value` as a person
// might, and answers the key. A line of prose is not one: a sentence has a
// space where a key would need its `=`.
func commentedKey(line string) ([]string, bool) {
	rest := strings.TrimLeft(line, " \t")
	rest, ok := strings.CutPrefix(rest, "#")
	if !ok {
		return nil, false
	}
	rest = strings.TrimLeft(rest, " \t")
	if rest == "" {
		return nil, false
	}
	key, i, err := parseKeyPath(rest, 0)
	if err != nil || i >= len(rest) || rest[i] != '=' {
		return nil, false
	}
	return key, true
}

// settingOf is the index of the setting a key path is, matched the way the
// decoder matches a file's keys — without regard to case — and with a
// wildcard segment taking any name, the scaffold's `<role>` included.
func settingOf(path []string) int {
	for i, s := range settings {
		if matchPath(strings.Split(s.Key, "."), path) {
			return i
		}
	}
	return -1
}

func matchPath(pattern, path []string) bool {
	if len(pattern) != len(path) {
		return false
	}
	for i := range pattern {
		if pattern[i] == RoleWildcard {
			if path[i] == "" {
				return false
			}
			continue
		}
		if !strings.EqualFold(pattern[i], path[i]) {
			return false
		}
	}
	return true
}

// covers says a key path is a strict prefix of the pattern: a value written
// over the whole table the setting sits in, `appearance = { mouse = true }`,
// which says whatever it says about every key inside it.
func covers(path, pattern []string) bool {
	if len(path) >= len(pattern) {
		return false
	}
	return matchPath(pattern[:len(path)], path)
}

// mentions says the file sets the setting, lists it as a commented row, or
// holds a value over the whole table it sits in.
func (d *document) mentions(rs []row, i int) bool {
	pattern := strings.Split(settings[i].Key, ".")
	for _, r := range rs {
		if r.setting == i || (!r.commented && covers(r.path, pattern)) {
			return true
		}
	}
	return false
}

// moved is one key the update wrote under its new name, as the file spelled
// it and as it is spelled now, for the check that nothing else changed.
type moved struct {
	from, to []string
}

// moveRenamed writes every renamed key under its new name, with its value as
// the file wrote it — the literal and any comment trailing it on the line,
// byte for byte — and a comment above it saying where it came from. The line
// under the old name goes. A comment on the lines above the old key stays
// where it is, because nothing says whether it was about that key or about
// the section it opened.
func (d *document) moveRenamed() ([]moved, error) {
	var out []moved
	for {
		done := true
		for _, s := range d.spans {
			if s.kind != spanKey {
				continue
			}
			r, ok := renameOf(strings.Join(s.path, "."))
			if !ok {
				continue
			}
			to := strings.Split(r.To, ".")
			if _, dup := d.findKeyFold(to); dup {
				return nil, fmt.Errorf("%s and %s are both set; keep the one you mean and remove the other",
					joinKey(s.path), r.To)
			}
			if err := d.inlineTableOver(to); err != nil {
				return nil, err
			}
			value := strings.TrimRight(d.text[s.valStart:s.end], "\r\n")
			from := slices.Clone(s.path)
			d.splice(s.start, s.end, "")
			d.placeMoved(to, "# "+strings.Join(from, ".")+" moved here in "+r.Moved+d.nl, value)
			out = append(out, moved{from: from, to: to})
			done = false
			break
		}
		if done {
			return out, nil
		}
	}
}

func (d *document) findKeyFold(path []string) (span, bool) {
	for _, s := range d.spans {
		if s.kind == spanKey && matchPath(path, s.path) {
			return s, true
		}
	}
	return span{}, false
}

// placeMoved writes a moved key after the last key of its table where the
// file has that table, and adds the table beside its relatives where it does
// not — the same places `config set` would put the key.
func (d *document) placeMoved(path []string, note, value string) {
	name := quoteKey(path[len(path)-1])
	table := path[:len(path)-1]
	for i, s := range d.spans {
		if s.kind != spanHeader || !matchPath(table, s.path) {
			continue
		}
		at, indent := s.end, ""
		for _, b := range d.spans[i+1:] {
			if b.kind == spanHeader {
				break
			}
			if b.kind == spanKey {
				at, indent = b.end, b.indent
			}
		}
		d.splice(at, at, d.lineBreakAt(at)+indent+note+indent+name+" = "+value+d.nl)
		return
	}
	d.addTable(table, note+name+" = "+value+d.nl)
}

// lineBreakAt is the newline a text inserted at offset needs in front of it:
// none where the offset starts a line, one where the file's last line has no
// newline of its own.
func (d *document) lineBreakAt(at int) string {
	if at > 0 && d.text[at-1] != '\n' {
		return d.nl
	}
	return ""
}

// addMissing adds every key the file does not mention as the scaffold writes
// it — the sentence that says what it decides, then the key commented out at
// its default — in the table's order: after the nearest key before it that
// the file has, in its own table, and a table the file does not have at all
// after the nearest table before it that it does.
func (d *document) addMissing(project bool) error {
	groups := groupOrder(project)
	for gi, g := range groups {
		var missing []int
		rs := d.rows()
		for i, s := range settings {
			if s.Group() != g || (project && RefusedInProject(s.Key) != "") {
				continue
			}
			if !d.mentions(rs, i) {
				missing = append(missing, i)
			}
		}
		if len(missing) == 0 {
			continue
		}
		if h, ok := d.header([]string{g}); ok {
			for _, i := range missing {
				at := d.anchorIn(h, g, i)
				d.splice(at, at, d.lineBreakAt(at)+d.keyBlock(i))
			}
			continue
		}
		var body strings.Builder
		body.WriteString("[" + quoteKey(g) + "]" + d.nl)
		for _, i := range missing {
			body.WriteString(d.keyBlock(i))
		}
		d.insertTable(groups[:gi], body.String())
	}
	return nil
}

// groupOrder is the tables in the order the settings run, in this scope.
func groupOrder(project bool) []string {
	var out []string
	for _, s := range settings {
		if project && RefusedInProject(s.Key) != "" {
			continue
		}
		if g := s.Group(); len(out) == 0 || out[len(out)-1] != g {
			out = append(out, g)
		}
	}
	return out
}

// keyBlock is one key as the scaffold writes it: a blank line, the comment,
// and the commented row.
func (d *document) keyBlock(i int) string {
	s := settings[i]
	var b strings.Builder
	b.WriteString(d.nl)
	for _, line := range scaffoldNotes(s) {
		b.WriteString(strings.TrimRight("# "+line, " ") + d.nl)
	}
	for _, line := range scaffoldLines(Config{}, s) {
		b.WriteString(line + d.nl)
	}
	return b.String()
}

// header is the span index of the table's own header.
func (d *document) header(path []string) (int, bool) {
	for i, s := range d.spans {
		if s.kind == spanHeader && matchPath(path, s.path) {
			return i, true
		}
	}
	return 0, false
}

// anchorIn is where setting i goes in the table whose header is span h: past
// the last line of the nearest setting before it that the table already
// lists, or straight under the header when it lists none of them.
func (d *document) anchorIn(h int, group string, i int) int {
	at, best := d.spans[h].end, -1
	for _, r := range d.rows() {
		if !matchPath([]string{group}, r.table) || r.setting < 0 || r.setting >= i {
			continue
		}
		switch {
		case r.setting > best:
			best, at = r.setting, r.end
		case r.setting == best && r.end > at:
			at = r.end
		}
	}
	return at
}

// insertTable puts a table the file does not have after the last line of
// the nearest earlier table it does have — that table and every table under
// it — or in front of the file's first table when it has none of them, or at
// the end of a file with no tables at all.
func (d *document) insertTable(before []string, block string) {
	for j := len(before) - 1; j >= 0; j-- {
		at, found := 0, false
		var table []string
		for _, s := range d.spans {
			if s.kind == spanHeader {
				table = s.path
			}
			if len(table) == 0 || !strings.EqualFold(table[0], before[j]) {
				continue
			}
			switch s.kind {
			case spanHeader, spanKey:
				at, found = s.end, true
			default:
				if _, ok := commentedKey(d.text[s.start:s.end]); ok {
					at, found = s.end, true
				}
			}
		}
		if found {
			d.splice(at, at, d.lineBreakAt(at)+d.nl+block)
			return
		}
	}
	for _, s := range d.spans {
		if s.kind == spanHeader {
			d.splice(s.start, s.start, block+d.nl)
			return
		}
	}
	d.appendBlock(block)
}

// sameValues decodes the text before and after an update and refuses a
// result that holds anything but the same values, with each moved key under
// its new name. It is the check that makes "values are never changed" a
// property of the write rather than a promise about the code above it.
func sameValues(before, after string, moves []moved) error {
	var was, now map[string]any
	if _, err := toml.Decode(before, &was); err != nil {
		return err
	}
	if _, err := toml.Decode(after, &now); err != nil {
		return fmt.Errorf("the update did not produce a readable file: %w", err)
	}
	var cfg Config
	meta, err := toml.Decode(after, &cfg)
	if err != nil {
		return fmt.Errorf("the update did not produce a readable file: %w", err)
	}
	if left := meta.Undecoded(); len(left) > 0 {
		return fmt.Errorf("%s is still not a key this version reads; move it by hand", left[0])
	}
	for _, m := range moves {
		v, ok := takeValue(was, m.from)
		if !ok {
			return fmt.Errorf("%s could not be read back before the move", joinKey(m.from))
		}
		putValue(was, m.to, v)
	}
	if !reflect.DeepEqual(pruneEmpty(was), pruneEmpty(now)) {
		return errors.New("the update would have changed a value the file set, so nothing was written")
	}
	return nil
}

// pruneEmpty drops every table that holds no value, at any depth: a table the
// update added holds only commented rows, and a table a move emptied holds
// nothing, and neither is a value either side of the comparison set.
func pruneEmpty(m map[string]any) map[string]any {
	for k, v := range m {
		if sub, ok := v.(map[string]any); ok {
			if len(pruneEmpty(sub)) == 0 {
				delete(m, k)
			}
		}
	}
	return m
}

func takeValue(m map[string]any, path []string) (any, bool) {
	for i, seg := range path {
		v, ok := m[seg]
		if !ok {
			return nil, false
		}
		if i == len(path)-1 {
			delete(m, seg)
			return v, true
		}
		next, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		m = next
	}
	return nil, false
}

func putValue(m map[string]any, path []string, v any) {
	for _, seg := range path[:len(path)-1] {
		next, ok := m[seg].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[seg] = next
		}
		m = next
	}
	m[path[len(path)-1]] = v
}
