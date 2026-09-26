package structural

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/provider"
)

// The preview is a diff, computed here rather than printed by sd: with no
// terminal, sd's --preview prints every named file whole as it would read
// after the replacement, matched or not, so a five-occurrence rename across
// five files came back as the five files and was cut at MaxOutputBytes. Each
// file is previewed on its own and diffed against the file as read, so the
// result follows the change and a file with no match adds nothing. Output
// past MaxOutputBytes is still cut off and kept nowhere, because this tool is
// exempt from the evidence pipeline.
// See docs/capabilities/evidence.md#reduction-is-for-unbounded-output.
var sdTool = provider.Tool{
	Name: SdToolName,
	Description: "PREVIEW a find-and-replace across files with sd. This tool never modifies files: it always runs sd with --preview " +
		"and returns a unified diff of the lines that would change, under a \"--- a/<path>\" header per file; a named file with no match adds nothing. Use it to check a transform across several files, then apply the changes you want with edit_file. " +
		"The pattern is a regular expression unless fixed_strings is set; the replacement may use capture groups like $1. " +
		"Output past 64 KiB is cut off and lost, so a result that says it was truncated is not every file: preview the rest in smaller batches rather than acting on the part you saw.",
	Parameters: json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "Regex (or fixed string) to find"},
			"replacement": {"type": "string", "description": "Replacement text; may reference capture groups ($1, $name). Empty deletes the match"},
			"paths": {"type": "array", "items": {"type": "string"}, "description": "Files to preview the replacement in, relative to the workspace root (at least one); a file with no match adds nothing to the diff"},
			"fixed_strings": {"type": "boolean", "description": "Treat pattern and replacement as literal strings"},
			"ignore_case": {"type": "boolean", "description": "Match the pattern without regard to letter case"},
			"multiline": {"type": "boolean", "description": "Let ^ and $ match line boundaries"},
			"dot_all": {"type": "boolean", "description": "Let . match newlines, so a pattern can span lines"},
			"word_boundary": {"type": "boolean", "description": "Match the pattern only where it is a whole word"},
			"max_replacements": {"type": "integer", "description": "Stop after this many replacements in each file"}
		},
		"required": ["pattern", "replacement", "paths"]
	}`),
}

// MaxSdFileBytes bounds one file's preview. The preview of a file is the
// whole file as sd would write it, held only to be diffed, so this is a bound
// on memory rather than on what the model reads: a file larger than this, or
// one the replacement grows past it, is refused by name.
const MaxSdFileBytes = 4 << 20

type sdArgs struct {
	Pattern         string   `json:"pattern"`
	Replacement     string   `json:"replacement"`
	Paths           []string `json:"paths"`
	FixedStrings    bool     `json:"fixed_strings"`
	IgnoreCase      bool     `json:"ignore_case"`
	Multiline       bool     `json:"multiline"`
	DotAll          bool     `json:"dot_all"`
	WordBoundary    bool     `json:"word_boundary"`
	MaxReplacements int      `json:"max_replacements"`
}

// buildSdArgv constructs sd's argv. Invariants: --preview is always present —
// sd writes files in place by default, so this is load-bearing, not
// defense-in-depth — and pattern, replacement, and every resolved path follow
// a literal "--" delimiter: a pattern colliding with a value-taking flag name
// (like "-f") is otherwise silently consumed as that flag's value, shifting
// sd into blocking on stdin it never receives. Option values ride attached as
// --flag=value.
func buildSdArgv(a sdArgs, resolvedPaths []string) []string {
	argv := []string{"--preview"}
	if a.FixedStrings {
		argv = append(argv, "--fixed-strings")
	}
	var flags strings.Builder
	if a.IgnoreCase {
		flags.WriteByte('i')
	}
	if a.Multiline {
		flags.WriteByte('m')
	}
	if a.DotAll {
		flags.WriteByte('s')
	}
	if a.WordBoundary {
		flags.WriteByte('w')
	}
	if flags.Len() > 0 {
		argv = append(argv, "--flags="+flags.String())
	}
	if a.MaxReplacements > 0 {
		argv = append(argv, "--max-replacements="+strconv.Itoa(a.MaxReplacements))
	}
	argv = append(argv, "--", a.Pattern, a.Replacement)
	return append(argv, resolvedPaths...)
}

func (t *Toolset) executeSd(raw json.RawMessage) (string, error) {
	var args sdArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	resolved, err := t.resolvePaths(args.Paths)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, path := range resolved {
		fileDiff, err := t.sdFileDiff(args, path)
		if err != nil {
			return "", err
		}
		b.WriteString(fileDiff)
		if b.Len() > MaxOutputBytes {
			break
		}
	}
	out := strings.TrimRight(b.String(), "\n")
	if out == "" {
		return "No replacements: the pattern did not match.", nil
	}
	if len(out) > MaxOutputBytes {
		out = truncated(out[:MaxOutputBytes], MaxOutputBytes)
	}
	return "Preview only — no file was changed. Apply wanted changes with edit_file.\n\n" + out, nil
}

// sdFileDiff previews the replacement in one resolved file and returns the
// unified diff of the file as read against sd's preview of it, or "" when
// nothing in it changes. sd is handed one path at a time because, given one,
// it prints the file's new content bare, with no header to parse out of text
// that could itself contain one.
func (t *Toolset) sdFileDiff(args sdArgs, path string) (string, error) {
	rel, err := filepath.Rel(t.root, path)
	if err != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", rel, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory: name the files to preview", rel)
	}
	if info.Size() > MaxSdFileBytes {
		return "", fmt.Errorf("%s is %d bytes, past the %d-byte preview bound", rel, info.Size(), MaxSdFileBytes)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", rel, err)
	}
	after, overflowed, err := t.spawn(SdToolName, buildSdArgv(args, []string{path}), MaxSdFileBytes)
	if err != nil {
		return "", err
	}
	if overflowed {
		return "", fmt.Errorf("the replacement grows %s past the %d-byte preview bound", rel, MaxSdFileBytes)
	}
	if after == string(before) {
		return "", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", rel, rel)
	hunks := diff.Compute(string(before), after)
	if len(hunks) == 0 {
		// The lines are the same and the texts are not: only the file's
		// final newline moved, which a line diff cannot show.
		b.WriteString("(only the final newline changes)\n")
	}
	for _, h := range hunks {
		b.WriteString(h.Header() + "\n")
		for _, l := range h.Lines {
			switch l.Kind {
			case diff.Add:
				b.WriteByte('+')
			case diff.Del:
				b.WriteByte('-')
			default:
				b.WriteByte(' ')
			}
			b.WriteString(l.Text + "\n")
		}
	}
	return b.String(), nil
}
