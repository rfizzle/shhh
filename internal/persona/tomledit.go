package persona

// Editing a profile's TOML in place. A profile is a flat file of top-level
// keys, and a save changes a handful of them; the rest of the file — every
// other key, every comment, the order the author chose — is kept as the
// bytes it was. So this finds where each top-level key's value starts and
// ends and replaces only those spans, rather than decoding the file and
// writing a new one.

import (
	"fmt"
	"sort"
	"strings"
)

// tomlEntry is one top-level key: the span of its line, and of its value
// within it.
type tomlEntry struct {
	key              string
	line, lineEnd    int
	valStart, valEnd int
}

// scanTOML finds the top-level keys of a TOML document. It stops at the
// first table header: a profile has none, and the keys under one are not
// the profile's.
func scanTOML(text string) ([]tomlEntry, error) {
	var out []tomlEntry
	i := 0
	// A byte-order mark is the decoder's to skip, so it is this scan's too;
	// the spans still index the text with it, so the save keeps it.
	if strings.HasPrefix(text, "\ufeff") {
		i = len("\ufeff")
	}
	for i < len(text) {
		line := i
		for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
			i++
		}
		switch {
		case i >= len(text):
			return out, nil
		case text[i] == '\n' || text[i] == '\r' || text[i] == '#':
			i = endOfLine(text, i)
			continue
		case text[i] == '[':
			return out, nil
		}
		keyEnd, err := scanKey(text, i)
		if err != nil {
			return nil, err
		}
		key := strings.Trim(strings.TrimSpace(text[i:keyEnd]), `"'`)
		j := keyEnd
		for j < len(text) && (text[j] == ' ' || text[j] == '\t') {
			j++
		}
		if j >= len(text) || text[j] != '=' {
			return nil, fmt.Errorf("line %d: expected = after %q", lineNumber(text, line), key)
		}
		j++
		for j < len(text) && (text[j] == ' ' || text[j] == '\t') {
			j++
		}
		end, err := scanValue(text, j)
		if err != nil {
			return nil, fmt.Errorf("line %d: %s: %w", lineNumber(text, line), key, err)
		}
		next := endOfLine(text, end)
		out = append(out, tomlEntry{key: key, line: line, lineEnd: next, valStart: j, valEnd: end})
		i = next
	}
	return out, nil
}

// scanKey is the end of a bare or quoted key starting at i.
func scanKey(text string, i int) (int, error) {
	if text[i] == '"' || text[i] == '\'' {
		end := strings.IndexByte(text[i+1:], text[i])
		if end < 0 {
			return 0, fmt.Errorf("line %d: unterminated key", lineNumber(text, i))
		}
		return i + 1 + end + 1, nil
	}
	j := i
	for j < len(text) && (isBare(text[j]) || text[j] == '.') {
		j++
	}
	if j == i {
		return 0, fmt.Errorf("line %d: expected a key", lineNumber(text, i))
	}
	return j, nil
}

func isBare(c byte) bool {
	return c == '_' || c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// scanValue is the end of the value starting at i: a string of any of the
// four kinds, an array or inline table however many lines it spans, or a
// bare scalar up to a comment or the line's end.
func scanValue(text string, i int) (int, error) {
	if i >= len(text) {
		return 0, fmt.Errorf("no value")
	}
	switch {
	case strings.HasPrefix(text[i:], `"""`), strings.HasPrefix(text[i:], `'''`):
		quote := text[i : i+3]
		j := i + 3
		for j < len(text) {
			if quote[0] == '"' && text[j] == '\\' {
				j += 2
				continue
			}
			if strings.HasPrefix(text[j:], quote) {
				end := j + 3
				// Up to two quotes of the content may sit against the
				// closing three.
				for k := 0; k < 2 && end < len(text) && text[end] == quote[0]; k++ {
					end++
				}
				return end, nil
			}
			j++
		}
		return 0, fmt.Errorf("unterminated multi-line string")
	case text[i] == '"' || text[i] == '\'':
		q := text[i]
		for j := i + 1; j < len(text); j++ {
			switch {
			case q == '"' && text[j] == '\\':
				j++
			case text[j] == q:
				return j + 1, nil
			case text[j] == '\n':
				return 0, fmt.Errorf("unterminated string")
			}
		}
		return 0, fmt.Errorf("unterminated string")
	case text[i] == '[' || text[i] == '{':
		depth := 0
		for j := i; j < len(text); {
			switch text[j] {
			case '[', '{':
				depth++
				j++
			case ']', '}':
				depth--
				j++
				if depth == 0 {
					return j, nil
				}
			case '"', '\'':
				end, err := scanValue(text, j)
				if err != nil {
					return 0, err
				}
				j = end
			case '#':
				j = endOfLine(text, j)
			default:
				j++
			}
		}
		return 0, fmt.Errorf("unterminated array")
	}
	j := i
	for j < len(text) && text[j] != '\n' && text[j] != '#' {
		j++
	}
	return i + len(strings.TrimRight(text[i:j], " \t\r")), nil
}

// endOfLine is the index after the newline that ends the line i is on.
func endOfLine(text string, i int) int {
	if n := strings.IndexByte(text[i:], '\n'); n >= 0 {
		return i + n + 1
	}
	return len(text)
}

func lineNumber(text string, i int) int { return strings.Count(text[:i], "\n") + 1 }

// tomlEditor gathers replacements over the scanned document and applies
// them at once, so the spans it scanned stay true while it gathers.
type tomlEditor struct {
	text    string
	entries []tomlEntry
	edits   []tomlEdit
}

type tomlEdit struct {
	start, end int
	with       string
}

func (e *tomlEditor) find(key string) (tomlEntry, bool) {
	for _, en := range e.entries {
		if en.key == key {
			return en, true
		}
	}
	return tomlEntry{}, false
}

// set replaces a key's value where the file has it, and otherwise adds the
// key on a line of its own: before the prompt, which a profile keeps last,
// or at the end.
func (e *tomlEditor) set(key, value string) {
	if en, ok := e.find(key); ok {
		e.edits = append(e.edits, tomlEdit{start: en.valStart, end: en.valEnd, with: value})
		return
	}
	line := key + " = " + value + "\n"
	if en, ok := e.find("prompt"); ok && key != "prompt" {
		e.edits = append(e.edits, tomlEdit{start: en.line, end: en.line, with: line})
		return
	}
	at := len(e.text)
	if at > 0 && !strings.HasSuffix(e.text, "\n") {
		line = "\n" + line
	}
	e.edits = append(e.edits, tomlEdit{start: at, end: at, with: line})
}

// setOrRemove sets a key, or takes its line out where the value would say
// nothing.
func (e *tomlEditor) setOrRemove(key, value string, remove bool) {
	if !remove {
		e.set(key, value)
		return
	}
	if en, ok := e.find(key); ok {
		e.edits = append(e.edits, tomlEdit{start: en.line, end: en.lineEnd})
	}
}

// apply is the text with every edit made, latest offset first so the earlier
// offsets still hold; an insertion sorts after a replacement at the same
// offset, so a key added before the prompt lands above it.
func (e *tomlEditor) apply() string {
	edits := append([]tomlEdit(nil), e.edits...)
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].start != edits[j].start {
			return edits[i].start > edits[j].start
		}
		return edits[i].end > edits[j].end
	})
	text := e.text
	for _, ed := range edits {
		text = text[:ed.start] + ed.with + text[ed.end:]
	}
	return text
}
