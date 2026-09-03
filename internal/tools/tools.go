package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
)

type Definition struct {
	Tool    provider.Tool
	Execute func(args json.RawMessage) (string, error)
}

// The read-only tool names. They are constants because two other decisions
// are made by name: the reduction pipeline asks which results are already
// bounded (SelfBounding), and the mutation rail asks which are not.
const (
	ReadFileName      = "read_file"
	ListDirectoryName = "list_directory"
	SearchName        = "search"
	GlobName          = "glob"
)

func ReadOnly() []Definition {
	return []Definition{readFile, listDirectory, search, globFiles}
}

// SelfBounding reports whether a tool already bounds its own output.
//
// Every tool named here has a cap in limits.go chosen for the shape of what
// it returns, and a truncation notice that says how to continue past it. A
// second, shape-blind reduction on top of that is not a saving: it takes a
// result the tool sized deliberately and cuts a head and a tail out of it,
// dropping the middle of a file the model was told to read in one call. The
// reduction pipeline exists for output nothing else bounds — a command's, a
// server's, a page's — and that is the only place it earns its cost.
// See docs/capabilities/evidence.md#reduction-is-for-unbounded-output.
func SelfBounding(name string) bool {
	switch name {
	case ReadFileName, ListDirectoryName, SearchName, GlobName:
		return true
	}
	return false
}

// skipWalk reports whether a directory is one no tool walks into. glob,
// search and list_directory all skip the same three, so they say so in one
// place: a directory missing from one of the three lists is a tool that
// floods its own cap with objects nobody asked for.
func skipWalk(name string) bool {
	return name == ".git" || name == "node_modules" || name == "vendor"
}

// walkIgnore carries the .gitignore rules down a filepath.WalkDir.
//
// Every reader that walks a tree honours them, because a build directory git
// refuses to look at is not an answer to "where does this live" — and because
// search already behaves this way wherever ripgrep is installed, which made
// the same question return different trees on two machines. The rules are
// resolved per directory rather than once at the root: a nested .gitignore
// applies inside its own directory and nowhere else.
//
// WalkDir hands a directory over before anything inside it, so a directory's
// rules can always be built from its parent's, which is what the map holds.
// Rules above the walk's root are not read: a root the caller named is one
// they have already decided to look in.
// See docs/capabilities/coding-agent.md#what-git-will-not-look-at-the-agent-does-not-offer.
type walkIgnore struct {
	root  string
	rules map[string]project.Ignore
}

func newWalkIgnore(root string) *walkIgnore {
	root = filepath.Clean(root)
	return &walkIgnore{
		root:  root,
		rules: map[string]project.Ignore{root: project.LoadIgnore(root)},
	}
}

// dir records the rules inside p and reports whether p itself is ignored. The
// walk's own root never is.
func (w *walkIgnore) dir(p string) bool {
	if p == w.root {
		return false
	}
	parent := w.rules[filepath.Dir(p)]
	if parent.Ignored(p, true) {
		return true
	}
	w.rules[p] = parent.Descend(p)
	return false
}

// file reports whether the file at p is ignored.
func (w *walkIgnore) file(p string) bool {
	return w.rules[filepath.Dir(p)].Ignored(p, false)
}

func Definitions() []provider.Tool {
	defs := ReadOnly()
	tools := make([]provider.Tool, len(defs))
	for i, d := range defs {
		tools[i] = d.Tool
	}
	return tools
}

// ExecCommandName is the tool the chat UI intercepts for user approval; it is
// deliberately not executable through Execute.
const ExecCommandName = "execute_command"

func ExecCommandTool() provider.Tool {
	return provider.Tool{
		Name:        ExecCommandName,
		Description: "Execute a shell command on the user's machine. The user is shown the command and must approve it before it runs; a declined call returns an error result. Returns combined stdout/stderr and the exit code.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"command": {"type": "string", "description": "The shell command to run"}
			},
			"required": ["command"]
		}`),
	}
}

// DefinitionsWithExec returns the read-only tool definitions plus the
// user-approved execute_command tool.
func DefinitionsWithExec() []provider.Tool {
	return append(Definitions(), ExecCommandTool())
}

// Execute dispatches an auto-run tool call. Only read-only tools are
// reachable here: mutating tools (Mutating) and execute_command must go
// through user approval and are deliberately unknown to this path.
func Execute(name string, args json.RawMessage) (string, error) {
	for _, d := range ReadOnly() {
		if d.Tool.Name == name {
			return d.Execute(args)
		}
	}
	return "", fmt.Errorf("unknown tool: %s", name)
}

// What a description tells a model to do.
//
// This one used to lead with "Large files are truncated with a notice; use
// start_line/end_line to page through the rest", and paging was what it got:
// sessions reading twenty and forty line windows out of files well under the
// cap, a round apiece. Nothing was wrong with the tool — MaxReadFileLines is
// two thousand — but the salient sentence of a description is the
// instruction, and that sentence described the exception. The common case
// leads now, and paging is what it actually is: how to continue through a
// file the tool has already told you it could not finish.
var readFile = Definition{
	Tool: provider.Tool{
		Name: ReadFileName,
		Description: "Read the contents of a file. Each line is returned as `<line number>\t<text>`; the number is a reading aid, not part of the file. " +
			"Read the whole file by default — it is one call, and reading it in small windows is not cheaper. " +
			"start_line/end_line are for continuing through a file too large to return at once, which the result says explicitly when it happens. " +
			"A file over the size ceiling is refused whatever range is asked for — search it instead, or take a part of it with a command — and a binary file comes back as a one-line notice of what it is, images attached where the model can see one.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Absolute or relative file path to read"},
				"start_line": {"type": "integer", "description": "Optional 1-based start line"},
				"end_line": {"type": "integer", "description": "Optional 1-based end line (inclusive)"}
			},
			"required": ["path"]
		}`),
	},
	Execute: executeReadFile,
}

type readFileArgs struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

func executeReadFile(raw json.RawMessage) (string, error) {
	var args readFileArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	data, notice, err := readForModel(args.Path)
	if err != nil {
		return "", err
	}
	if notice != "" {
		// Nothing was shown, so nothing is recorded as seen: a mutation
		// built on a notice is a mutation built on nothing.
		return notice, nil
	}
	windowed := args.StartLine > 0 || args.EndLine > 0

	lines := strings.Split(string(data), "\n")
	total := len(lines)
	start := 1
	end := total
	if args.StartLine > 0 || args.EndLine > 0 {
		if args.StartLine > 1 {
			start = args.StartLine
		}
		if args.EndLine > 0 && args.EndLine < end {
			end = args.EndLine
		}
		if start > total {
			return "", fmt.Errorf("start_line %d exceeds file length (%d lines)", start, total)
		}
		if end < start {
			return "", fmt.Errorf("end_line %d is before start_line %d", end, start)
		}
	}
	selected := lines[start-1 : end]

	truncated := false
	if len(selected) > MaxReadFileLines {
		selected = selected[:MaxReadFileLines]
		truncated = true
	}
	content := strings.Join(selected, "\n")
	if cut, wasCut := TruncateOutput(content, MaxReadFileBytes); wasCut {
		// Drop the trailing partial line so the paging notice counts whole
		// lines — unless the cut landed inside the only line we have.
		if i := strings.LastIndexByte(cut, '\n'); i > 0 {
			cut = cut[:i]
		}
		content = cut
		truncated = true
	}
	shown := strings.Count(content, "\n") + 1
	// Number after the caps, never before: MaxReadFileBytes is a budget for
	// the file, and spending part of it on the numbering would mean a reader
	// asking for a whole file got less of one than limits.go promises.
	content = numberLines(content, start)
	if truncated {
		lastLine := start + shown - 1
		content += fmt.Sprintf("\n… (truncated: showing lines %d-%d of %d; call read_file again with start_line=%d to continue)", start, lastLine, total, lastLine+1)
	}

	// What the model has now been shown, so a later mutation can tell a
	// change from a clobber (seen.go). The fingerprint is of the whole file
	// even for a windowed read — the question a mutation asks is whether the
	// file moved, and a window is still a reading of the file it came from.
	noteShown(args.Path, data, !windowed && !truncated)

	return content, nil
}

// readForModel opens a file on read_file's behalf: the size ceiling and the
// binary sniff both happen here, before the file is read whole, and either
// can end the call without its contents.
//
// The two answers are deliberately different kinds. Over the ceiling is an
// error, because the model asked for something it cannot have and has to ask
// differently. A binary file is not an error: "this is a PNG" is a complete
// answer to what was asked, and an error there invites the retry with a
// smaller range — the same call again, for a file that has no lines.
func readForModel(path string) (data []byte, notice string, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", fmt.Errorf("cannot read file: %w", err)
	}
	if info.IsDir() {
		return nil, "", fmt.Errorf("%s is a directory; list_directory is the tool for one", path)
	}
	if info.Size() > MaxReadFileSize {
		return nil, "", fmt.Errorf("%s is %s and read_file stops at %s, whatever line range is asked for; search it for what you need, or read a part of it with a command",
			path, attachment.HumanSize(int(info.Size())), attachment.HumanSize(MaxReadFileSize))
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, "", fmt.Errorf("cannot read file: %w", err)
	}
	defer f.Close()

	head := make([]byte, SniffBytes)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, "", fmt.Errorf("cannot read file: %w", err)
	}
	head = head[:n]
	if mediaType, text := sniffText(head); !text {
		return nil, binaryNotice(path, mediaType, head, f, info.Size()), nil
	}

	rest, err := io.ReadAll(f)
	if err != nil {
		return nil, "", fmt.Errorf("cannot read file: %w", err)
	}
	return append(head, rest...), "", nil
}

// sniffText reports what a file's opening bytes are, and whether they are
// text a reader can return as lines.
//
// Both halves of the test earn their place. The content sniffer names the
// file — "image/png" tells the model to stop reaching for this tool, where
// "binary" tells it nothing — but it reads only the first 512 bytes, so a
// file that opens with a page of text and continues as bytes passes it. The
// NUL scan over the whole sniffed window is what catches that one, and it is
// git's own rule, so a file this calls binary is one git, search and the
// diffs all call binary too.
func sniffText(head []byte) (mediaType string, text bool) {
	mediaType = http.DetectContentType(head)
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = strings.TrimSpace(mediaType[:i])
	}
	return mediaType, strings.HasPrefix(mediaType, "text/") && !bytes.ContainsRune(head, 0)
}

// binaryNotice is the whole result for a file that has no text to return: one
// line naming what it is and how big it is. An image is also left for the
// dispatcher to carry as an attachment, so a model that can see one is shown
// the file instead of being told about it.
//
// The notice is the key the bytes are collected by, so it is built after the
// attempt to attach and says which of the two happened. Claiming an image was
// attached when the read failed would leave the model waiting for a picture
// that is not coming.
func binaryNotice(path, mediaType string, head []byte, rest io.Reader, size int64) string {
	name := filepath.Base(path)
	if att, ok := imageAttachment(name, mediaType, head, rest, size); ok {
		notice := fmt.Sprintf("%s is an image (%s, %s), attached to this result for models that can see one; it has no text to return.",
			path, mediaType, attachment.HumanSize(int(size)))
		attachment.NoteResult(notice, att)
		return notice
	}
	return fmt.Sprintf("%s is a binary file (%s, %s); read_file returns text and there is none in it. Search it, or read part of it with a command, if you need what is inside.",
		path, mediaType, attachment.HumanSize(int(size)))
}

// imageAttachment reads the rest of an image and classifies it the way a
// pasted or dragged-in file is classified, so there is one answer to what
// shhh can put in front of a model. A format no provider takes, or a file
// past the attachment ceiling, is not an image as far as this is concerned:
// it is bytes, and the notice says so.
func imageAttachment(name, mediaType string, head []byte, rest io.Reader, size int64) (provider.Attachment, bool) {
	if !strings.HasPrefix(mediaType, "image/") || size > attachment.MaxBytes {
		return provider.Attachment{}, false
	}
	tail, err := io.ReadAll(rest)
	if err != nil {
		return provider.Attachment{}, false
	}
	att, err := attachment.FromBytes(name, append(head, tail...))
	if err != nil || att.Kind != provider.AttachmentImage {
		return provider.Attachment{}, false
	}
	return att, true
}

// numberLines prefixes each line with its 1-based number in the file and a
// tab, so a reader can cite file:line without counting and can point an edit
// at the right place. The separator is a tab because the prefix has to be
// unambiguously strippable from a line of source that may start with
// anything — including spaces.
func numberLines(content string, start int) string {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	b.Grow(len(content) + len(lines)*6)
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strconv.Itoa(start + i))
		b.WriteByte('\t')
		b.WriteString(line)
	}
	return b.String()
}

var listDirectory = Definition{
	Tool: provider.Tool{
		Name: ListDirectoryName,
		Description: "List files and directories at a given path. Returns one entry per line with type prefix (file: or dir:). " +
			"Reports .git, node_modules and vendor without descending into them, and leaves out anything .gitignore names — " +
			"list an ignored directory by naming it directly if you need to see inside it.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Directory path to list"},
				"depth": {"type": "integer", "description": "Optional max recursion depth (default 1, max 3)"}
			},
			"required": ["path"]
		}`),
	},
	Execute: executeListDirectory,
}

type listDirectoryArgs struct {
	Path  string `json:"path"`
	Depth int    `json:"depth"`
}

func executeListDirectory(raw json.RawMessage) (string, error) {
	var args listDirectoryArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if args.Depth < 1 {
		args.Depth = 1
	}
	if args.Depth > 3 {
		args.Depth = 3
	}

	var lines []string
	err := walkDir(args.Path, "", args.Depth, project.LoadIgnore(args.Path), &lines)
	if err != nil {
		return "", err
	}
	if len(lines) > MaxListEntries {
		lines = lines[:MaxListEntries]
		return strings.Join(lines, "\n") + fmt.Sprintf("\n… (truncated at %d entries; list a subdirectory or use a smaller depth to see more)", MaxListEntries), nil
	}
	return strings.Join(lines, "\n"), nil
}

// walkDir lists one directory, carrying the ignore rules gathered on the way
// down. An ignored entry is left out entirely rather than named and not
// entered: .git is skipped because descending into it is expensive, and the
// reader still wants to know it is there, but a path git refuses to see is
// not part of the answer at all.
func walkDir(base, prefix string, depth int, rules project.Ignore, lines *[]string) error {
	if depth < 1 {
		return nil
	}
	dir := filepath.Join(base, prefix)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("cannot list directory: %w", err)
	}
	for _, e := range entries {
		rel := filepath.Join(prefix, e.Name())
		full := filepath.Join(base, rel)
		if rules.Ignored(full, e.IsDir()) {
			continue
		}
		if e.IsDir() {
			*lines = append(*lines, "dir: "+rel)
			// The directory is named but not entered. Descending into .git
			// spent most of a 500-entry budget on object shards — a listing
			// the reader then had to narrow by hand, one wasted round for a
			// question nobody asked.
			if depth > 1 && !skipWalk(e.Name()) {
				_ = walkDir(base, rel, depth-1, rules.Descend(full), lines)
			}
		} else {
			*lines = append(*lines, "file: "+rel)
		}
	}
	return nil
}
