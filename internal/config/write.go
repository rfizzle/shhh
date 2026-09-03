package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
)

// Edit is one change to the file: Key as `config set` spells it, Value as a
// person would type it, or empty to take the key out.
type Edit struct {
	Key   string
	Value string
}

// Write applies edits to the file at path by editing its text: the line that
// holds each key is replaced, added under its table, or removed, and every
// other byte — the order, the blank lines, the comments — stays as the person
// wrote it. There is no encoder of the whole struct behind this, on purpose:
// one re-encode turned a three-line file with a comment saying why into
// eighty lines of zeroes with the comment gone, and after it unset and zero
// read the same in a file whose own comments say they differ
// (docs/capabilities/configuration.md#a-write-changes-one-line).
//
// A key set to its zero value is removed rather than written as zero, and a
// negative — the form a few keys use to mean "no bound" — is kept. A file that
// does not exist is created holding only what was written. A file that will
// not parse is refused untouched: editing text the parser cannot read would
// land the key somewhere nobody meant.
func Write(path string, edits ...Edit) error {
	return rewrite(path, func(doc *document) error {
		for _, e := range edits {
			key, literal, err := literalFor(e.Key, e.Value)
			if err != nil {
				return err
			}
			if literal == "" {
				err = doc.removeKey(key)
			} else {
				err = doc.setKey(key, literal)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// WriteServer writes one `[mcp.servers.<name>]` table, replacing the
// definition of that name if the file already holds one and adding it beside
// the other MCP tables otherwise. Its fields are written the way the file
// takes them: scalars and lists on the table's own lines, env and headers as
// sub-tables, nothing for a field at its zero value.
func WriteServer(path, name string, s MCPServer) error {
	return rewrite(path, func(doc *document) error {
		key := []string{"mcp", "servers", name}
		doc.removeTable(key)
		doc.addTable(key, tableBody(key, reflect.ValueOf(s), doc.nl))
		return nil
	})
}

// RemoveServer deletes the `[mcp.servers.<name>]` table and every table
// beneath it, and nothing beside them.
func RemoveServer(path, name string) error {
	return rewrite(path, func(doc *document) error {
		doc.removeTable([]string{"mcp", "servers", name})
		return nil
	})
}

// rewrite reads the file, hands its parsed text to change, and writes the
// result back through a temporary file in the same directory, so a failure
// part-way leaves the person's file whole. The result is decoded before it is
// written: the edit is textual, and a text the parser cannot read must never
// replace one it could.
func rewrite(path string, change func(*document) error) error {
	// A config file is often a symlink into a dotfiles checkout. The rename
	// below would replace the link with a plain file and leave the real one
	// holding the old value, so the write goes to what the link names.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	text := string(raw)
	if _, err := toml.Decode(text, &Config{}); err != nil {
		return fmt.Errorf("config %s: not written, the file does not parse: %w", path, err)
	}
	// The decoder accepts a byte-order mark and the span parser does not;
	// it is set aside and put back, so an editor that writes one does not
	// make the file unwritable.
	bom := ""
	if rest, ok := strings.CutPrefix(text, "\uFEFF"); ok {
		bom, text = "\uFEFF", rest
	}
	doc, err := parseDocument(text)
	if err != nil {
		return fmt.Errorf("config %s: %w", path, err)
	}
	if err := change(doc); err != nil {
		return fmt.Errorf("config %s: %w", path, err)
	}
	if _, err := toml.Decode(doc.text, &Config{}); err != nil {
		return fmt.Errorf("config %s: not written, the edit did not produce a readable file: %w", path, err)
	}
	return replaceFile(path, bom+doc.text)
}

// replaceFile writes text over path atomically. A new file is 0600 because
// the provider key lives in this one; an existing file keeps its mode.
func replaceFile(path, text string) error {
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.WriteString(text)
	if err := tmp.Close(); werr == nil {
		werr = err
	}
	if werr == nil {
		werr = os.Chmod(name, mode)
	}
	if werr == nil {
		werr = os.Rename(name, path)
	}
	if werr != nil {
		_ = os.Remove(name) // the write is the failure to report
	}
	return werr
}

// literalFor turns a key and the value a person typed into the path the file
// spells the key by and the TOML literal to write there, empty when the value
// is the key's zero and the line should go. It applies Set to a fresh Config
// and reads the field back, so the literal is whatever the setting holds
// after the same parse `config set` does — a write cannot disagree with a
// load about a key's type.
func literalFor(key, value string) ([]string, string, error) {
	var scratch Config
	if err := Set(&scratch, key, value); err != nil {
		return nil, "", err
	}
	path := fileKey(key)
	v, ok := fieldAt(reflect.ValueOf(scratch), path)
	if !ok {
		return nil, "", fmt.Errorf("unknown config key: %s", key)
	}
	// A pointer is a tri-state key: nil is unset and goes, and a pointer
	// to false is a value — `mouse = false` is the whole point of the key.
	// Everything else is unset at its zero.
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return path, "", nil
		}
		v = v.Elem()
	} else if v.IsZero() {
		return path, "", nil
	}
	lit, err := literal(v)
	if err != nil {
		return nil, "", fmt.Errorf("config key %s: %w", key, err)
	}
	return path, lit, nil
}

// fileKey is the path the file spells a key by. Two keys are spelled
// differently at the command line than in the file — the role models are
// `agents.researcher_model` to `config set` and a `[agents.profiles.<role>]`
// table on disk — and this is the one place that difference is known.
func fileKey(key string) []string {
	if role, ok := strings.CutPrefix(key, "agents."); ok {
		if role, ok := strings.CutSuffix(role, "_model"); ok && role != "" && !strings.Contains(role, ".") {
			return []string{"agents", "profiles", role, "model"}
		}
	}
	return strings.Split(key, ".")
}

// fieldAt walks a struct value along a file path, by the names the toml
// tags give the fields, indexing maps by the segment as written.
func fieldAt(v reflect.Value, path []string) (reflect.Value, bool) {
	for _, seg := range path {
		for v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return reflect.Value{}, false
			}
			v = v.Elem()
		}
		switch v.Kind() {
		case reflect.Map:
			v = v.MapIndex(reflect.ValueOf(seg))
			if !v.IsValid() {
				return reflect.Value{}, false
			}
		case reflect.Struct:
			i, ok := fieldIndex(v.Type(), seg)
			if !ok {
				return reflect.Value{}, false
			}
			v = v.Field(i)
		default:
			return reflect.Value{}, false
		}
	}
	return v, true
}

// fieldIndex is the struct field a file key names, matched the way the
// decoder matches it — by tag, then by name, case-insensitively.
func fieldIndex(t reflect.Type, name string) (int, bool) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
		switch tag {
		case "-":
			continue
		case "":
			tag = f.Name
		}
		if strings.EqualFold(tag, name) {
			return i, true
		}
	}
	return 0, false
}

// literal is one value as the file writes it. Tables are not values: a map
// or a struct here is a caller writing a table through the key path, which
// tableBody exists for.
func literal(v reflect.Value) (string, error) {
	for v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.String:
		return quoteString(v.String()), nil
	case reflect.Bool:
		return strconv.FormatBool(v.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'g', -1, 64), nil
	case reflect.Slice, reflect.Array:
		parts := make([]string, v.Len())
		for i := range parts {
			p, err := literal(v.Index(i))
			if err != nil {
				return "", err
			}
			parts[i] = p
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	}
	return "", fmt.Errorf("a %s is a table, not a value", v.Kind())
}

// quoteString is a TOML basic string. The escapes are the six TOML names
// and \u for the rest of the control range; Go's own quoting would write
// \x and \a, which TOML does not read.
func quoteString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// quoteKey is one key segment: bare where TOML allows it, a basic string
// otherwise — a server named with a space or a dot still lands in a header
// the decoder reads back as that name.
func quoteKey(seg string) string {
	if seg != "" && strings.IndexFunc(seg, func(r rune) bool { return !isBareKeyRune(r) }) < 0 {
		return seg
	}
	return quoteString(seg)
}

func isBareKeyRune(r rune) bool {
	return r == '_' || r == '-' ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func joinKey(path []string) string {
	parts := make([]string, len(path))
	for i, seg := range path {
		parts[i] = quoteKey(seg)
	}
	return strings.Join(parts, ".")
}

// tableBody is a struct written as the lines under a table header: one line
// per field that is not at its zero value, in the order the struct declares
// them, then one sub-table per map field with its keys sorted, a blank line
// above each the way a person writes one. The zero
// values are left out because the file's own rule is that an absent key is
// the default, and a `timeout_seconds = 0` beside a comment saying so reads
// as a choice nobody made.
func tableBody(path []string, v reflect.Value, nl string) string {
	var b strings.Builder
	var subs []string
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
		if tag == "-" {
			continue
		}
		if tag == "" {
			tag = f.Name
		}
		fv := v.Field(i)
		for fv.Kind() == reflect.Pointer && !fv.IsNil() {
			fv = fv.Elem()
		}
		if fv.Kind() == reflect.Pointer || fv.IsZero() {
			continue
		}
		switch fv.Kind() {
		case reflect.Map:
			var lines strings.Builder
			keys := fv.MapKeys()
			slices.SortFunc(keys, func(a, b reflect.Value) int { return strings.Compare(a.String(), b.String()) })
			for _, k := range keys {
				lit, err := literal(fv.MapIndex(k))
				if err != nil {
					continue
				}
				fmt.Fprintf(&lines, "%s = %s%s", quoteKey(k.String()), lit, nl)
			}
			sub := append(slices.Clone(path), tag)
			subs = append(subs, nl+"["+joinKey(sub)+"]"+nl+lines.String())
		case reflect.Struct:
			sub := append(slices.Clone(path), tag)
			subs = append(subs, nl+"["+joinKey(sub)+"]"+nl+tableBody(sub, fv, nl))
		default:
			lit, err := literal(fv)
			if err != nil {
				continue
			}
			fmt.Fprintf(&b, "%s = %s%s", quoteKey(tag), lit, nl)
		}
	}
	for _, s := range subs {
		b.WriteString(s)
	}
	return b.String()
}

// A document is the file's text and the spans that structure it: a table
// header, a key with its value, or a line that is only a comment or blank.
// Every byte belongs to exactly one span, so an edit is a splice between two
// offsets and the rest of the text is never re-serialized.
type document struct {
	text  string
	spans []span
	// nl is the line terminator the file uses, so a line added to a file
	// written on Windows ends the way its neighbours do.
	nl string
}

type spanKind int

const (
	spanOther spanKind = iota
	spanHeader
	spanKey
)

type span struct {
	kind spanKind
	// path is the table for a header and the full dotted key for a key —
	// the table it sits under joined to the key as written, so `[appearance]`
	// followed by `mouse = true` and a root-level `appearance.mouse = true`
	// are the same path.
	path []string
	// start and end bound the span; end is past its newline.
	start, end int
	// valStart and valEnd bound a key's value, so the key, its spacing and a
	// comment trailing the value are kept when the value changes.
	valStart, valEnd int
	// indent is what preceded the key on its line.
	indent string
}

func parseDocument(text string) (*document, error) {
	doc := &document{text: text, nl: "\n"}
	if strings.Contains(text, "\r\n") {
		doc.nl = "\r\n"
	}
	var table []string
	line := 1
	for i := 0; i < len(text); {
		start := i
		j := i
		for j < len(text) && (text[j] == ' ' || text[j] == '\t') {
			j++
		}
		switch {
		case j == len(text) || text[j] == '\n' || text[j] == '#' || text[j] == '\r':
			end := lineEnd(text, j)
			doc.spans = append(doc.spans, span{kind: spanOther, start: start, end: end})
			i = end
		case text[j] == '[':
			k := j + 1
			array := k < len(text) && text[k] == '['
			if array {
				k++
			}
			path, k, err := parseKeyPath(text, k)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", line, err)
			}
			for k < len(text) && text[k] == ']' {
				k++
			}
			end := lineEnd(text, k)
			table = path
			doc.spans = append(doc.spans, span{kind: spanHeader, path: path, start: start, end: end})
			i = end
		default:
			key, k, err := parseKeyPath(text, j)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", line, err)
			}
			if k >= len(text) || text[k] != '=' {
				return nil, fmt.Errorf("line %d: expected = after the key", line)
			}
			k++
			for k < len(text) && (text[k] == ' ' || text[k] == '\t') {
				k++
			}
			valEnd := scanValue(text, k)
			end := lineEnd(text, valEnd)
			full := append(slices.Clone(table), key...)
			doc.spans = append(doc.spans, span{
				kind: spanKey, path: full, start: start, end: end,
				valStart: k, valEnd: valEnd, indent: text[start:j],
			})
			i = end
		}
		line += strings.Count(text[start:i], "\n")
	}
	return doc, nil
}

// lineEnd is the offset past the newline that ends the line at i, or the end
// of the text.
func lineEnd(text string, i int) int {
	if n := strings.IndexByte(text[i:], '\n'); n >= 0 {
		return i + n + 1
	}
	return len(text)
}

// parseKeyPath reads a dotted key — bare, quoted or a mix — and returns its
// segments and the offset past the trailing whitespace.
func parseKeyPath(text string, i int) ([]string, int, error) {
	var path []string
	for {
		for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
			i++
		}
		if i >= len(text) {
			return nil, i, errors.New("unterminated key")
		}
		var seg string
		switch text[i] {
		case '"':
			end := scanValue(text, i)
			s, err := unquoteBasic(text[i:end])
			if err != nil {
				return nil, i, err
			}
			seg, i = s, end
		case '\'':
			end := scanValue(text, i)
			if end-i < 2 {
				return nil, i, errors.New("unterminated key")
			}
			seg, i = text[i+1:end-1], end
		default:
			j := i
			for j < len(text) && isBareKeyRune(rune(text[j])) {
				j++
			}
			if j == i {
				return nil, i, fmt.Errorf("cannot read a key at %q", clip(text[i:]))
			}
			seg, i = text[i:j], j
		}
		path = append(path, seg)
		for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
			i++
		}
		if i < len(text) && text[i] == '.' {
			i++
			continue
		}
		return path, i, nil
	}
}

func clip(s string) string {
	if n := strings.IndexByte(s, '\n'); n >= 0 {
		s = s[:n]
	}
	if len(s) > 20 {
		s = s[:20] + "…"
	}
	return s
}

// unquoteBasic reads a basic string's text back, for a quoted key.
func unquoteBasic(s string) (string, error) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", errors.New("unterminated quoted key")
	}
	s = s[1 : len(s)-1]
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(s) {
			return "", errors.New("bad escape in quoted key")
		}
		switch s[i] {
		case 'b':
			b.WriteByte('\b')
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'f':
			b.WriteByte('\f')
		case 'r':
			b.WriteByte('\r')
		case '"':
			b.WriteByte('"')
		case '\\':
			b.WriteByte('\\')
		case 'u', 'U':
			width := 4
			if s[i] == 'U' {
				width = 8
			}
			if i+width >= len(s) {
				return "", errors.New("bad escape in quoted key")
			}
			n, err := strconv.ParseUint(s[i+1:i+1+width], 16, 32)
			if err != nil || !utf8.ValidRune(rune(n)) {
				return "", errors.New("bad escape in quoted key")
			}
			b.WriteRune(rune(n))
			i += width
		default:
			return "", errors.New("bad escape in quoted key")
		}
	}
	return b.String(), nil
}

// scanValue is the offset past the value that starts at i. Strings in their
// four forms end where they close; an array or inline table ends at its
// matching bracket, strings and comments inside it skipped; anything else
// ends where the line, or a comment on it, does. Only the extent is read —
// the value's meaning is the decoder's business, and the decoder has already
// accepted the file.
func scanValue(text string, i int) int {
	switch {
	case strings.HasPrefix(text[i:], `"""`):
		return scanMultiline(text, i+3, `"""`, true)
	case strings.HasPrefix(text[i:], "'''"):
		return scanMultiline(text, i+3, "'''", false)
	case i < len(text) && text[i] == '"':
		for j := i + 1; j < len(text) && text[j] != '\n'; j++ {
			switch text[j] {
			case '\\':
				j++
			case '"':
				return j + 1
			}
		}
		return lineTrim(text, i)
	case i < len(text) && text[i] == '\'':
		if n := strings.IndexAny(text[i+1:], "'\n"); n >= 0 && text[i+1+n] == '\'' {
			return i + 1 + n + 1
		}
		return lineTrim(text, i)
	case i < len(text) && (text[i] == '[' || text[i] == '{'):
		depth := 0
		for j := i; j < len(text); j++ {
			switch text[j] {
			case '"', '\'':
				j = scanValue(text, j) - 1
			case '#':
				j = lineEnd(text, j) - 1
			case '[', '{':
				depth++
			case ']', '}':
				depth--
				if depth == 0 {
					return j + 1
				}
			}
		}
		return len(text)
	}
	return lineTrim(text, i)
}

// scanMultiline ends past the closing delimiter, and past the one or two
// extra quotes TOML lets a multi-line string end with.
func scanMultiline(text string, i int, delim string, escapes bool) int {
	for j := i; j < len(text); j++ {
		if escapes && text[j] == '\\' {
			j++
			continue
		}
		if strings.HasPrefix(text[j:], delim) {
			j += len(delim)
			for extra := 0; extra < 2 && j < len(text) && text[j] == delim[0]; extra++ {
				j++
			}
			return j
		}
	}
	return len(text)
}

// lineTrim is the end of a bare value: the line, a comment on it, or the
// text, less trailing whitespace.
func lineTrim(text string, i int) int {
	end := len(text)
	for j := i; j < len(text); j++ {
		if text[j] == '\n' || text[j] == '#' {
			end = j
			break
		}
	}
	for end > i && (text[end-1] == ' ' || text[end-1] == '\t' || text[end-1] == '\r') {
		end--
	}
	return end
}

func (d *document) reparse() {
	nd, err := parseDocument(d.text)
	if err != nil {
		// The text was parsed once already and every edit splices whole
		// lines or a value, so a second parse cannot fail; if it ever did,
		// the decode before the write is what refuses the result.
		d.spans = nil
		return
	}
	d.spans = nd.spans
}

func (d *document) splice(start, end int, with string) {
	d.text = d.text[:start] + with + d.text[end:]
	d.reparse()
}

func (d *document) findKey(path []string) (span, bool) {
	for _, s := range d.spans {
		if s.kind == spanKey && slices.Equal(s.path, path) {
			return s, true
		}
	}
	return span{}, false
}

// setKey replaces the key's value where the file has the key, adds the line
// after the table's last key where it has the table, and adds the table at
// the end otherwise.
func (d *document) setKey(path []string, literal string) error {
	if s, ok := d.findKey(path); ok {
		d.splice(s.valStart, s.valEnd, literal)
		return nil
	}
	if err := d.inlineTableOver(path); err != nil {
		return err
	}
	name := quoteKey(path[len(path)-1])
	table := path[:len(path)-1]
	for i, s := range d.spans {
		if s.kind != spanHeader || !slices.Equal(s.path, table) {
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
		d.splice(at, at, indent+name+" = "+literal+d.nl)
		return nil
	}
	line := name + " = " + literal + d.nl
	if len(table) > 0 {
		line = "[" + joinKey(table) + "]" + d.nl + line
	}
	d.appendBlock(line)
	return nil
}

func (d *document) removeKey(path []string) error {
	if s, ok := d.findKey(path); ok {
		d.splice(s.start, s.end, "")
		return nil
	}
	return d.inlineTableOver(path)
}

// inlineTableOver refuses a key whose table the file wrote as an inline
// table — `appearance = { mouse = true }` — because a line cannot be added
// to or taken out of one, and appending a `[appearance]` header would make
// the file define the table twice. The refusal names the line to edit; the
// loader takes the form, so a person who wrote it may keep it.
func (d *document) inlineTableOver(path []string) error {
	for _, s := range d.spans {
		if s.kind != spanKey || len(s.path) >= len(path) || !underPath(path, s.path) {
			continue
		}
		return fmt.Errorf("%s is written as an inline table, so %s is not written here; edit that line by hand",
			joinKey(s.path), joinKey(path))
	}
	return nil
}

// appendBlock adds a table at the end of the file, one blank line after
// whatever is there.
func (d *document) appendBlock(block string) {
	sep := ""
	if d.text != "" {
		sep = d.nl
		if !strings.HasSuffix(d.text, "\n") {
			sep = d.nl + d.nl
		}
	}
	d.splice(len(d.text), len(d.text), sep+block)
}

// addTable writes a table with the given body beside its nearest relatives —
// after the last key of the last table that shares its first segment — or
// at the end of the file when there is none. Placing a server's table under
// the other `[mcp…]` tables is what a person adding one by hand would do,
// and a file that reads in the order it was written is the point of editing
// it in place. The anchor is the last key rather than the last line because
// a comment after it most often introduces the section that follows, and a
// table put between the two would read as what the comment describes.
func (d *document) addTable(path []string, body string) {
	block := "[" + joinKey(path) + "]" + d.nl + body
	at := -1
	for i, s := range d.spans {
		if s.kind != spanHeader || len(s.path) == 0 || s.path[0] != path[0] {
			continue
		}
		at = s.end
		for _, b := range d.spans[i+1:] {
			if b.kind == spanHeader {
				break
			}
			if b.kind == spanKey {
				at = b.end
			}
		}
	}
	if at < 0 {
		d.appendBlock(block)
		return
	}
	d.splice(at, at, d.nl+block)
}

// removeTable deletes every table at or under path with its body, and every
// key whose path is under it, however the file spelled them. A table and the
// sub-tables written directly beneath it go as one region, which ends at its
// last key: a comment below that is left where it is, because it more often
// introduces the section that follows than describes the one removed, and a
// stray comment costs less than a destroyed one. The blank lines after the
// last key go with the region, and a blank line that separated the region
// from what came before goes with it when the region did not end with one,
// so adding a table and removing it again leaves the text as it was.
func (d *document) removeTable(path []string) {
	for {
		removed := false
		for i, s := range d.spans {
			if !underPath(s.path, path) {
				continue
			}
			start, end := s.start, s.end
			if s.kind == spanHeader {
				last := i
				for j, b := range d.spans[i+1:] {
					if b.kind == spanHeader && !underPath(b.path, path) {
						break
					}
					if b.kind != spanOther {
						last = i + 1 + j
					}
				}
				end = d.spans[last].end
				for _, b := range d.spans[last+1:] {
					if strings.TrimSpace(d.text[b.start:b.end]) != "" {
						break
					}
					end = b.end
				}
			}
			bodyBlank := strings.HasSuffix(d.text[start:end], "\n\n") || strings.HasSuffix(d.text[start:end], "\n\r\n")
			if !bodyBlank && (strings.HasSuffix(d.text[:start], "\n\n") || strings.HasSuffix(d.text[:start], "\n\r\n")) {
				start = strings.LastIndexByte(d.text[:start-1], '\n') + 1
			}
			d.splice(start, end, "")
			removed = true
			break
		}
		if !removed {
			return
		}
	}
}

func underPath(p, prefix []string) bool {
	return len(p) >= len(prefix) && slices.Equal(p[:len(prefix)], prefix)
}
