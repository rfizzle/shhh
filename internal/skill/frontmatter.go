package skill

import (
	"fmt"
	"strings"
)

// The frontmatter reader.
//
// A SKILL.md frontmatter is a flat mapping of a few known keys, one of which
// (metadata) is itself a flat mapping of strings. That is the whole grammar,
// and a general YAML parser would be the wrong tool for it: skills written
// for other harnesses routinely carry values that are not valid YAML — an
// unquoted "Use when: the user asks…" with a colon in it is the common one —
// and their parsers accept them. This reader takes everything after the
// first colon as the value, which is what those authors meant, and reads
// quoted strings, block scalars and one level of nesting, which is all the
// specification's fields use.

// frontmatter is the parsed key/value view: scalars by key, plus nested
// mappings by key.
type frontmatter struct {
	scalars  map[string]string
	mappings map[string]map[string]string
}

func (f frontmatter) scalar(key string) string { return f.scalars[key] }

func (f frontmatter) mapping(key string) map[string]string {
	m := f.mappings[key]
	if len(m) == 0 {
		return nil
	}
	return m
}

// splitFrontmatter separates the YAML block between the leading --- lines
// from the Markdown body after them. A file with no frontmatter is an error
// rather than a skill with no name: the specification requires it, and a
// file that is missing it is far more often a README than a skill.
func splitFrontmatter(content string) (fm, body string, err error) {
	content = strings.TrimPrefix(content, "\uFEFF")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != "---" {
		return "", "", fmt.Errorf("no frontmatter: the file must start with ---")
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == "---" {
			return strings.Join(lines[1:i], "\n"), strings.TrimSpace(strings.Join(lines[i+1:], "\n")), nil
		}
	}
	return "", "", fmt.Errorf("frontmatter is not closed: no second --- line")
}

// parseFrontmatter reads the block between the --- lines.
func parseFrontmatter(fm string) (frontmatter, error) {
	out := frontmatter{scalars: map[string]string{}, mappings: map[string]map[string]string{}}
	lines := strings.Split(fm, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if indentOf(line) > 0 {
			return out, fmt.Errorf("frontmatter line %d: unexpected indentation", i+1)
		}
		key, rest, ok := splitKey(line)
		if !ok {
			return out, fmt.Errorf("frontmatter line %d: expected key: value", i+1)
		}
		switch rest {
		case "|", ">", "|-", ">-":
			// Block scalar: the indented lines that follow, joined with
			// newlines for | and spaces for >.
			block, next := indentedBlock(lines, i+1)
			if strings.HasPrefix(rest, ">") {
				out.scalars[key] = strings.Join(block, " ")
			} else {
				out.scalars[key] = strings.Join(block, "\n")
			}
			i = next - 1
		case "":
			// A nested mapping, or an empty value if nothing is indented
			// below.
			block, next := indentedBlock(lines, i+1)
			if len(block) == 0 {
				out.scalars[key] = ""
				continue
			}
			m := map[string]string{}
			for _, b := range block {
				if strings.TrimSpace(b) == "" || strings.HasPrefix(strings.TrimSpace(b), "#") {
					continue
				}
				k, v, ok := splitKey(b)
				if !ok {
					return out, fmt.Errorf("frontmatter %s: expected key: value, got %q", key, strings.TrimSpace(b))
				}
				m[k] = unquote(v)
			}
			out.mappings[key] = m
			i = next - 1
		default:
			out.scalars[key] = unquote(rest)
		}
	}
	return out, nil
}

// splitKey splits "key: rest" at the first colon that ends the key. A colon
// inside the rest is part of the value.
func splitKey(line string) (key, rest string, ok bool) {
	line = strings.TrimSpace(line)
	idx := strings.Index(line, ":")
	if idx <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	rest = line[idx+1:]
	if rest != "" && !strings.HasPrefix(rest, " ") && !strings.HasPrefix(rest, "\t") {
		// "a:b" with no space is not a key/value pair in YAML; treat the
		// whole line as malformed rather than guessing.
		return "", "", false
	}
	return key, strings.TrimSpace(rest), true
}

// indentedBlock collects the run of lines from start that are indented (or
// blank), stripping the common indentation, and returns the index of the
// first line that is not.
func indentedBlock(lines []string, start int) (block []string, next int) {
	indent := -1
	next = start
	for next < len(lines) {
		line := lines[next]
		if strings.TrimSpace(line) == "" {
			block = append(block, "")
			next++
			continue
		}
		in := indentOf(line)
		if in == 0 {
			break
		}
		if indent < 0 || in < indent {
			indent = in
		}
		block = append(block, line)
		next++
	}
	// Trailing blank lines belong to whatever comes after, not the block.
	for len(block) > 0 && block[len(block)-1] == "" {
		block = block[:len(block)-1]
	}
	for i, b := range block {
		if b != "" && indent > 0 && len(b) >= indent {
			block[i] = b[indent:]
		}
	}
	return block, next
}

func indentOf(line string) int {
	n := 0
	for _, r := range line {
		if r == ' ' || r == '\t' {
			n++
			continue
		}
		break
	}
	return n
}

// unquote strips one layer of matching quotes and drops a trailing comment
// from an unquoted value.
func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 {
		switch {
		case v[0] == '"' && v[len(v)-1] == '"':
			inner := v[1 : len(v)-1]
			inner = strings.ReplaceAll(inner, `\"`, `"`)
			inner = strings.ReplaceAll(inner, `\n`, "\n")
			return inner
		case v[0] == '\'' && v[len(v)-1] == '\'':
			return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
		}
	}
	if idx := strings.Index(v, " #"); idx >= 0 {
		v = strings.TrimSpace(v[:idx])
	}
	return v
}
