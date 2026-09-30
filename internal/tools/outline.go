package tools

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rfizzle/shhh/internal/provider"
)

// DocumentSymbolName is the outline tool. It is named here rather than in the
// language-server package because every session has it: a Markdown file is
// outlined by this package, and a language server, where one answers for a
// file's language, takes the same call for the files it knows.
const DocumentSymbolName = "document_symbol"

// The outline is registered wherever read_file is, and answers for Markdown
// with no language server behind it, because a document's heading outline was
// the read a session reached for a shell to take most often — `grep -n '^#'`,
// five times over in one session — and it needs nothing a server knows.
// Where a server answers for the file's language, its WrapExecutor takes the
// call first; what reaches this definition is a file no server answers for.
// See docs/capabilities/coding-agent.md#finding-things.
var documentSymbol = Definition{
	Tool: provider.Tool{
		Name: DocumentSymbolName,
		Description: "Outline one file: every declaration or heading in it with its line, nested as it is nested. " +
			"A Markdown file is outlined by its headings in every session; source files are outlined where a language server answers for their language. " +
			"Prefer this over read_file when the question is what is in a file — the outline is a fraction of the file and usually settles which part to read.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "File to outline (absolute or workspace-relative)"}
			},
			"required": ["path"]
		}`),
	},
	Execute: executeDocumentSymbol,
}

// IsMarkdown reports whether path names a file this package outlines by its
// headings. It goes by the extension, the way a language server is chosen.
func IsMarkdown(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".mdx":
		return true
	}
	return false
}

func executeDocumentSymbol(raw json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if a.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if !IsMarkdown(a.Path) {
		ext := filepath.Ext(a.Path)
		if ext == "" {
			ext = "extension-less"
		}
		return "", fmt.Errorf("no language server answers for %s files in this session, and without one %s outlines Markdown headings only; search the file for its declarations instead",
			ext, DocumentSymbolName)
	}
	info, err := os.Stat(a.Path)
	if err != nil {
		return "", fmt.Errorf("cannot read file: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory; list_directory is the tool for one", a.Path)
	}
	if err := notRegular(a.Path, info); err != nil {
		return "", err
	}
	f, err := os.Open(a.Path)
	if err != nil {
		return "", fmt.Errorf("cannot read file: %w", err)
	}
	defer f.Close()
	o, err := scanFile(f, true)
	if err != nil {
		return "", fmt.Errorf("cannot read file: %w", err)
	}
	if len(o.headings) == 0 {
		return fmt.Sprintf("No headings in %s (%s).", a.Path, o.lineWord()), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Outline of %s (%d %s, %s):", a.Path, len(o.headings), noun(len(o.headings), "heading", "headings"), o.lineWord())
	o.writeHeadings(&b)
	return b.String(), nil
}

// heading is one ATX heading: its 1-based line, its level and its text.
type heading struct {
	line  int
	level int
	text  string
}

// scanned is what one pass over a file found: how many lines it has, the
// Markdown headings when they were asked for, and whether the pass stopped
// short of the end.
type scanned struct {
	lines    int
	headings []heading
	// dropped counts headings past MaxOutlineHeadings.
	dropped int
	// partial is true when the pass stopped at MaxScanBytes, so lines is a
	// floor and the headings are the ones above it.
	partial bool
}

func (o scanned) lineWord() string {
	if o.partial {
		return fmt.Sprintf("at least %s lines", commas(o.lines))
	}
	return fmt.Sprintf("%s %s", commas(o.lines), noun(o.lines, "line", "lines"))
}

// writeHeadings writes one row per heading under a header the caller wrote,
// each carrying its line number and its hashes, so the level reads as it is
// written and the number is what read_file's start_line takes.
func (o scanned) writeHeadings(b *strings.Builder) {
	for _, h := range o.headings {
		fmt.Fprintf(b, "\n%d %s %s", h.line, strings.Repeat("#", h.level), h.text)
	}
	if o.dropped > 0 {
		fmt.Fprintf(b, "\n… and %d more (truncated at %d)", o.dropped, MaxOutlineHeadings)
	}
	if o.partial {
		fmt.Fprintf(b, "\n… (stopped reading at %s; headings below that are not listed)", provider.HumanSize(MaxScanBytes))
	}
}

// scanFile reads r once, streaming, counting its lines and — when markdown is
// set — collecting its ATX headings. It holds one line at a time, however
// large the file, which is what lets a file past read_file's ceiling still be
// sized and outlined.
//
// A line counts the way a reader counts one: the text up to a newline, and
// the text after the last newline when there is any. So a file ending in a
// newline and the same file without it both have the same count, and an empty
// file has none.
//
// What is not a heading is as much the point as what is. A `#` inside a
// fenced code block is a shell comment or a C preprocessor line; one indented
// four spaces is an indented code block; one in the front matter at the top
// of the file is YAML. Counting any of those is the reason `grep '^#'` is the
// wrong outline, and a wrong outline sends the next read to the wrong line.
func scanFile(r io.Reader, markdown bool) (scanned, error) {
	var o scanned
	br := bufio.NewReaderSize(r, 64<<10)
	var read int64
	var fence byte
	fenceLen := 0
	frontMatter := ""
	continued := false
	for {
		chunk, isPrefix, err := br.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return o, nil
			}
			return o, err
		}
		read += int64(len(chunk)) + 1
		if continued {
			// The rest of a line longer than the reader's buffer: its first
			// chunk has already been counted and judged.
			continued = isPrefix
			continue
		}
		continued = isPrefix
		o.lines++
		if read > MaxScanBytes {
			o.partial = true
			return o, nil
		}
		if !markdown {
			continue
		}
		switch {
		case o.lines == 1 && (string(chunk) == "---" || string(chunk) == "+++"):
			frontMatter = string(chunk)
			continue
		case frontMatter != "":
			if strings.TrimRight(string(chunk), " \t") == frontMatter {
				frontMatter = ""
			}
			continue
		}
		body, indent := trimIndent(chunk)
		if indent > 3 {
			continue
		}
		if fence != 0 {
			if n := leading(body, fence); n >= fenceLen && len(bytes.TrimSpace(body[n:])) == 0 {
				fence, fenceLen = 0, 0
			}
			continue
		}
		if n := leading(body, '`'); n >= 3 {
			fence, fenceLen = '`', n
			continue
		}
		if n := leading(body, '~'); n >= 3 {
			fence, fenceLen = '~', n
			continue
		}
		if h, ok := atxHeading(body); ok {
			h.line = o.lines
			if len(o.headings) < MaxOutlineHeadings {
				o.headings = append(o.headings, h)
			} else {
				o.dropped++
			}
		}
	}
}

// trimIndent strips a line's leading spaces and reports how many columns they
// took. A tab is taken as reaching the next multiple of four, which is enough
// to put it past the three a heading or a fence may be indented by.
func trimIndent(line []byte) ([]byte, int) {
	col := 0
	for i, c := range line {
		switch c {
		case ' ':
			col++
		case '\t':
			col += 4 - col%4
		default:
			return line[i:], col
		}
	}
	return nil, col
}

// leading counts how many times c repeats at the start of b.
func leading(b []byte, c byte) int {
	n := 0
	for n < len(b) && b[n] == c {
		n++
	}
	return n
}

// atxHeading reads an ATX heading — one to six hashes, then a space, a tab or
// the end of the line — from a line whose indent is already removed, and
// drops the optional closing run of hashes.
func atxHeading(body []byte) (heading, bool) {
	level := leading(body, '#')
	if level < 1 || level > 6 {
		return heading{}, false
	}
	rest := body[level:]
	if len(rest) > 0 && rest[0] != ' ' && rest[0] != '\t' {
		return heading{}, false
	}
	text := strings.TrimSpace(string(rest))
	if trimmed := strings.TrimRight(text, "#"); trimmed != text {
		if trimmed == "" {
			text = ""
		} else if strings.HasSuffix(trimmed, " ") || strings.HasSuffix(trimmed, "\t") {
			text = strings.TrimSpace(trimmed)
		}
	}
	if utf8.RuneCountInString(text) > MaxOutlineTextRunes {
		runes := []rune(text)
		text = string(runes[:MaxOutlineTextRunes]) + "…"
	}
	return heading{level: level, text: text}, true
}

// lineCount is how many lines data holds, counted as scanFile counts them.
func lineCount(data []byte) int {
	n := bytes.Count(data, []byte{'\n'})
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n
}

// sizeLine is the first line of a read that shows part of a file: its path,
// how many lines and bytes the whole file has, and which lines follow. It is
// what `wc` was being run for — the size of a file the model is reading in
// part, stated where the part is.
// See docs/capabilities/coding-agent.md#finding-things.
func sizeLine(path string, data []byte, first, last int) string {
	lines := lineCount(data)
	shown := fmt.Sprintf("showing lines %d-%d", first, last)
	if last < first {
		shown = "showing no lines"
	}
	return fmt.Sprintf("%s: %s %s, %s bytes; %s", path, commas(lines), noun(lines, "line", "lines"), commas(len(data)), shown)
}

// IsSizeLine reports whether line is the size line a partial read opens with
// rather than a line of the file, for anything counting a read's lines: the
// file's own lines all open with their number and a tab, and this one names
// the file instead.
func IsSizeLine(line string) bool {
	if i := strings.IndexByte(line, '\t'); i > 0 {
		if _, err := strconv.Atoi(line[:i]); err == nil {
			return false
		}
	}
	return strings.Contains(line, " bytes; showing ")
}

// overCeiling is read_file's answer for a file past MaxReadFileSize: what the
// file is, in one streaming pass, rather than a refusal that leaves the model
// running `wc` and `grep '^#'` to learn the same thing.
func overCeiling(path string, f io.Reader, size int64) (string, error) {
	o, err := scanFile(f, IsMarkdown(path))
	if err != nil {
		return "", fmt.Errorf("cannot read file: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s is %s (%s bytes, %s) and read_file returns no file over %s, whatever line range is asked for. Search it for what you need.",
		path, provider.HumanSize(int(size)), commas64(size), o.lineWord(), provider.HumanSize(MaxReadFileSize))
	if IsMarkdown(path) {
		if len(o.headings) == 0 {
			b.WriteString(" It has no headings.")
		} else {
			fmt.Fprintf(&b, " Its %d %s:", len(o.headings), noun(len(o.headings), "heading", "headings"))
			o.writeHeadings(&b)
		}
	}
	return b.String(), nil
}

func noun(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// commas writes n with thousands separators, which is how a count is read at
// a glance — 48512 is a number to count the digits of, 48,512 is not.
func commas(n int) string { return commas64(int64(n)) }

func commas64(n int64) string {
	s := strconv.FormatInt(n, 10)
	if n < 0 || len(s) <= 3 {
		return s
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}
