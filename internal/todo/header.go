package todo

import (
	"fmt"
	"strings"
)

// The header reader and writer.
//
// An item's header is a flat block of `key: value` lines between two ---
// lines, where a value is a scalar or a bracketed list. That is the whole
// grammar. It is read here rather than by a YAML parser for the same reason
// the skill frontmatter is: a person typing a title with a colon in it has
// not made a syntax error, and a comment after a value is a comment. The
// reader keeps line order and line text, because a write must put back
// every line it did not change exactly as it found it.
// See docs/capabilities/todo.md#an-item-is-a-file-you-can-edit.

// header is the parsed block, in file order.
type header struct {
	lines []headerLine
}

// headerLine is one line of the block: a field, or a blank or comment line
// kept for the rewrite.
type headerLine struct {
	key, value string
	raw        string
	// comment is the ` # …` after the value, kept so an edit to the value
	// puts it back.
	comment string
	// field is false for a blank or comment line, which has no key.
	field bool
}

// splitHeader separates the header block from the body. A file with no
// header is an error rather than an item with no fields: the fields are
// what makes it an item at all, and a Markdown file without them is far
// more often a note that landed in the wrong directory.
func splitHeader(content string) (block []string, body string, err error) {
	content = strings.TrimPrefix(content, "\uFEFF")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != "---" {
		return nil, "", fmt.Errorf("no header: the file must start with ---")
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == "---" {
			return lines[1:i], strings.Join(lines[i+1:], "\n"), nil
		}
	}
	return nil, "", fmt.Errorf("header is not closed: no second --- line")
}

// parseHeader reads the block. Line numbers in errors count from the file's
// first line, so they are the number an editor shows.
func parseHeader(block []string) (header, error) {
	var h header
	for i, raw := range block {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			h.lines = append(h.lines, headerLine{raw: raw})
			continue
		}
		idx := strings.Index(trimmed, ":")
		if idx <= 0 {
			return h, fmt.Errorf("line %d: expected key: value, got %q", i+2, trimmed)
		}
		key := strings.TrimSpace(trimmed[:idx])
		rest := trimmed[idx+1:]
		if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
			return h, fmt.Errorf("line %d: %q needs a space after its colon", i+2, key)
		}
		h.lines = append(h.lines, headerLine{key: key, value: unquote(rest), comment: trailingComment(rest), raw: raw, field: true})
	}
	return h, nil
}

// set changes the first field with the key, or appends one, and reports
// whether anything changed. Only that line's text is rewritten, and a
// comment on it survives.
func (h *header) set(key, value string) bool {
	for i, l := range h.lines {
		if l.field && l.key == key {
			if l.value == value {
				return false
			}
			h.lines[i].value = value
			h.lines[i].raw = key + ": " + quote(value) + l.comment
			return true
		}
	}
	h.lines = append(h.lines, headerLine{key: key, value: value, raw: key + ": " + quote(value), field: true})
	return true
}

// quote wraps a value that would otherwise read back differently: one that
// contains what looks like a comment, starts with a quote or a bracket, or
// has space at an end. A title like "Fix #12 in the parser" is the common
// case, and writing it bare would lose everything after the hash.
func quote(v string) string {
	if v == "" {
		return v
	}
	needs := strings.Contains(v, " #") || strings.Contains(v, "\t#") ||
		v[0] == '"' || v[0] == '\'' || v[0] == '[' || v[0] == '#' ||
		v != strings.TrimSpace(v)
	if !needs {
		return v
	}
	return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
}

// render writes the block back, between its --- lines.
func (h header) render() string {
	var b strings.Builder
	b.WriteString("---\n")
	for _, l := range h.lines {
		b.WriteString(l.raw)
		b.WriteByte('\n')
	}
	b.WriteString("---\n")
	return b.String()
}

// unquote reads a value: a quoted string up to its closing quote, whatever
// follows it, or a bare value up to a trailing comment.
func unquote(v string) string {
	v = strings.TrimSpace(v)
	if inner, _, ok := quoted(v); ok {
		return inner
	}
	if idx := commentAt(v); idx >= 0 {
		v = strings.TrimSpace(v[:idx])
	}
	return v
}

// trailingComment is the ` # …` after a value, with its leading space, or
// "" when there is none.
func trailingComment(v string) string {
	v = strings.TrimSpace(v)
	if _, rest, ok := quoted(v); ok {
		if idx := commentAt(rest); idx >= 0 {
			return " " + strings.TrimSpace(rest[idx:])
		}
		return ""
	}
	if idx := commentAt(v); idx >= 0 {
		return " " + strings.TrimSpace(v[idx:])
	}
	return ""
}

// quoted reads a leading quoted string and returns what it held and what
// followed the closing quote.
func quoted(v string) (inner, rest string, ok bool) {
	if v == "" || (v[0] != '"' && v[0] != '\'') {
		return "", "", false
	}
	q := v[0]
	for i := 1; i < len(v); i++ {
		switch {
		case q == '"' && v[i] == '\\' && i+1 < len(v):
			i++
		case v[i] == q:
			if q == '\'' && i+1 < len(v) && v[i+1] == '\'' {
				i++
				continue
			}
			inner = v[1:i]
			if q == '"' {
				inner = strings.ReplaceAll(inner, `\"`, `"`)
			} else {
				inner = strings.ReplaceAll(inner, "''", "'")
			}
			return inner, v[i+1:], true
		}
	}
	return "", "", false
}

// commentAt is where a bare value's trailing comment starts, or -1.
func commentAt(v string) int {
	if strings.HasPrefix(v, "#") {
		return 0
	}
	for _, sep := range []string{" #", "\t#"} {
		if idx := strings.Index(v, sep); idx >= 0 {
			return idx
		}
	}
	return -1
}

// parseList reads a bracketed or comma-separated list: `[a, b]`, `a, b`,
// or a single value. Empty brackets and an empty value are an empty list.
func parseList(v string) []string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "[")
	v = strings.TrimSuffix(v, "]")
	var out []string
	for _, part := range strings.Split(v, ",") {
		part = unquote(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// formatList writes a list the way parseList reads it.
func formatList(items []string) string {
	return "[" + strings.Join(items, ", ") + "]"
}
