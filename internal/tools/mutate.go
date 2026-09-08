package tools

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rfizzle/shhh/internal/provider"
)

// WriteFileName and EditFileName are the file-modification tools the chat UI
// intercepts for user approval, mirroring ExecCommandName.
const (
	WriteFileName = "write_file"
	EditFileName  = "edit_file"
)

// Mutating returns the file-modification tool definitions. They are
// deliberately kept out of ReadOnly(), so Execute — the auto-run path — can
// never dispatch them; approved calls run through ExecuteMutating instead.
func Mutating() []Definition { return shared.Mutating() }

// Mutating with r as the record the two tools check what they are about to
// overwrite against, and file what they wrote in.
func (r *Recorder) Mutating() []Definition {
	write, edit := writeFile, editFile
	write.Execute = r.executeWriteFile
	edit.Execute = r.executeEditFile
	return []Definition{write, edit}
}

// DefinitionsFull returns the complete agent toolset: read-only tools, the
// user-approved execute_command, and the approval-gated file-modification
// tools. This is what `shhh code` registers; `shhh chat` stays on
// DefinitionsWithExec.
func DefinitionsFull() []provider.Tool {
	defs := DefinitionsWithExec()
	for _, d := range Mutating() {
		defs = append(defs, d.Tool)
	}
	return defs
}

// newFileMode is what a file one of these tools brings into existence is
// created with. An existing file keeps the mode it already has: os.WriteFile
// applies a mode only when it creates the file, which is what stops a rewrite
// of a 0755 script from leaving it unexecutable. Don't swap either write for
// a create-truncate that sets the mode unconditionally — nothing here has a
// reason to change permissions, and putting them back costs the model a
// chmod, which is a gated command.
const newFileMode os.FileMode = 0o644

// IsMutating reports whether name is a file-modification tool that must go
// through the user approval queue before it runs.
func IsMutating(name string) bool {
	for _, d := range Mutating() {
		if d.Tool.Name == name {
			return true
		}
	}
	return false
}

// WrittenPath is the file a mutating call names, and "" for any other call,
// for arguments that do not parse, and for a call that named no path.
//
// The surfaces that keep no changeset read what a run has written off the
// calls that wrote it — an unattended run for its tree reading and its git
// stager, a sub-agent for the digest its readings are made of — and they
// share this reading rather than each parsing the arguments again. A copy
// agrees with the original on the day it is written and stops agreeing the
// day a third mutating tool is registered.
func WrittenPath(name, rawArgs string) string {
	if !IsMutating(name) {
		return ""
	}
	var args struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(rawArgs), &args) != nil {
		return ""
	}
	return strings.TrimSpace(args.Path)
}

// ExecuteMutating dispatches a user-approved file-modification tool call. It
// refuses every other tool name so read-only tools cannot be routed here by
// mistake.
func ExecuteMutating(name string, args json.RawMessage) (string, error) {
	return shared.ExecuteMutating(name, args)
}

// ExecuteMutating against r's record, which is what an owner that does not
// share the process's record hands its approver.
func (r *Recorder) ExecuteMutating(name string, args json.RawMessage) (string, error) {
	for _, d := range r.Mutating() {
		if d.Tool.Name == name {
			return d.Execute(args)
		}
	}
	return "", fmt.Errorf("unknown mutating tool: %s", name)
}

// Mutation describes what a mutating tool call would change, for the approval
// prompt's diff preview: the file's current content against the content it
// would have after the call.
type Mutation struct {
	Action  string // short verb for the title, e.g. "write", "edit"
	Path    string
	OldText string
	NewText string
}

// PreviewMutation computes the before/after content for a mutating tool call
// without touching the file. Validation matches execution, so a call that
// previews cleanly only fails later if the file changes underneath it.
func PreviewMutation(name string, raw json.RawMessage) (Mutation, error) {
	return shared.PreviewMutation(name, raw)
}

// PreviewMutation against r's record. It has to be the record the call will
// be executed against, or a person is shown a diff for a change the write
// then refuses.
func (r *Recorder) PreviewMutation(name string, raw json.RawMessage) (Mutation, error) {
	switch name {
	case WriteFileName:
		args, err := parseWriteFileArgs(raw)
		if err != nil {
			return Mutation{}, err
		}
		old, existed, err := readIfExists(args.Path)
		if err != nil {
			return Mutation{}, err
		}
		if err := r.checkSeen(args.Path, []byte(old), existed, true); err != nil {
			return Mutation{}, err
		}
		return Mutation{Action: "write", Path: args.Path, OldText: old, NewText: args.Content}, nil
	case EditFileName:
		args, err := parseEditFileArgs(raw)
		if err != nil {
			return Mutation{}, err
		}
		content, err := os.ReadFile(args.Path)
		if err != nil {
			return Mutation{}, fmt.Errorf("cannot read file: %w", err)
		}
		if err := r.checkSeen(args.Path, content, true, false); err != nil {
			return Mutation{}, err
		}
		updated, _, err := applyEdits(string(content), args.Path, args.Edits)
		if err != nil {
			return Mutation{}, err
		}
		return Mutation{Action: "edit", Path: args.Path, OldText: string(content), NewText: updated}, nil
	}
	return Mutation{}, fmt.Errorf("unknown mutating tool: %s", name)
}

var writeFile = Definition{
	Tool: provider.Tool{
		Name: WriteFileName,
		Description: "Create or overwrite a file with the given content. content is written verbatim — never include read_file's line-number prefixes. " +
			"Overwriting an existing file requires having read it in full first, and fails if it has changed since: prefer edit_file for changing part of a file you have already read. " +
			"Missing parent directories are created automatically. The user reviews a diff and must approve the change before it is applied; a declined call returns an error result.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "File path to create or overwrite"},
				"content": {"type": "string", "description": "Full content the file will have"}
			},
			"required": ["path", "content"]
		}`),
	},
	// Execute is filled in by Mutating, which is where the record the call is
	// checked against is known.
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func parseWriteFileArgs(raw json.RawMessage) (writeFileArgs, error) {
	var args writeFileArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Path == "" {
		return args, fmt.Errorf("path is required")
	}
	return args, nil
}

func (r *Recorder) executeWriteFile(raw json.RawMessage) (string, error) {
	args, err := parseWriteFileArgs(raw)
	if err != nil {
		return "", err
	}
	old, existed, err := readIfExists(args.Path)
	if err != nil {
		return "", err
	}
	// A full overwrite carries no evidence about what it is overwriting, so
	// it has to have been read, in full, and not changed since (seen.go).
	if err := r.checkSeen(args.Path, []byte(old), existed, true); err != nil {
		return "", err
	}
	if dir := filepath.Dir(args.Path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("cannot create parent directories: %w", err)
		}
	}
	if err := os.WriteFile(args.Path, []byte(args.Content), newFileMode); err != nil {
		r.forget(args.Path)
		return "", fmt.Errorf("cannot write file: %w", err)
	}
	r.noteShown(args.Path, []byte(args.Content), true)
	if existed {
		return fmt.Sprintf("Overwrote %s: wrote %d bytes (%d lines), was %d bytes", args.Path, len(args.Content), countLines(args.Content), len(old)), nil
	}
	return fmt.Sprintf("Created %s: wrote %d bytes (%d lines)", args.Path, len(args.Content), countLines(args.Content)), nil
}

var editFile = Definition{
	Tool: provider.Tool{
		Name: EditFileName,
		Description: "Replace exact text snippets in an existing file. Give one replacement as old_text/new_text, or several as edits — one entry per place — and never both in the same call. " +
			"Batch when one file needs changing in several places: that is one round, one diff and one approval instead of one of each per pair. A second file is a second call. " +
			"Every old_text must match the file content exactly (including whitespace) and match exactly once, unless replace_all is set. " +
			"Strip read_file's `<line number>\t` prefix before quoting a line here — the numbers are a reading aid and are not in the file. " +
			"Every quote is matched against the file as it stands, not against the result of the edit before it, so the order does not matter and two edits that would touch the same text are refused. Nothing is written unless all of them apply. " +
			"A file that has changed since you last read it is refused: read it again and rebase the edits on what it says now. " +
			"The user reviews a diff and must approve the change before it is applied; a declined call returns an error result.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "File path to edit"},
				"old_text": {"type": "string", "description": "Exact existing text to replace, for a call that changes one place"},
				"new_text": {"type": "string", "description": "Replacement text, for a call that changes one place"},
				"replace_all": {"type": "boolean", "description": "Replace every occurrence instead of requiring a unique match"},
				"edits": {
					"type": "array",
					"description": "Several places in this one file, applied together against the file as it stands",
					"items": {
						"type": "object",
						"properties": {
							"old_text": {"type": "string", "description": "Exact existing text to replace"},
							"new_text": {"type": "string", "description": "Replacement text"},
							"replace_all": {"type": "boolean", "description": "Replace every occurrence instead of requiring a unique match"}
						},
						"required": ["old_text", "new_text"]
					}
				}
			},
			"required": ["path"]
		}`),
	},
	// Execute is filled in by Mutating, for the same reason write_file's is.
}

// fileEdit is one replacement: the text to find, and what to put in its
// place. A call carries either one of these inline or a list of them.
type fileEdit struct {
	OldText    string `json:"old_text"`
	NewText    string `json:"new_text"`
	ReplaceAll bool   `json:"replace_all"`
}

type editFileArgs struct {
	Path       string     `json:"path"`
	OldText    string     `json:"old_text,omitempty"`
	NewText    string     `json:"new_text,omitempty"`
	ReplaceAll bool       `json:"replace_all,omitempty"`
	Edits      []fileEdit `json:"edits,omitempty"`
}

// parseEditFileArgs reads the call and normalises both spellings into Edits,
// so everything downstream sees a list and there is one code path to be right
// about. The inline pair stays because a model that has been calling this
// tool for a whole conversation should not have its next call refused for
// spelling a single replacement the way it always has.
//
// Carrying both spellings at once is refused rather than merged: the two
// orders that merge could produce are different files, and picking one on the
// model's behalf is picking silently.
func parseEditFileArgs(raw json.RawMessage) (editFileArgs, error) {
	var args editFileArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Path == "" {
		return args, fmt.Errorf("path is required")
	}
	inline := args.OldText != "" || args.NewText != ""
	switch {
	case inline && len(args.Edits) > 0:
		return args, fmt.Errorf("give either old_text/new_text or edits, not both: put every replacement in edits")
	case inline:
		args.Edits = []fileEdit{{OldText: args.OldText, NewText: args.NewText, ReplaceAll: args.ReplaceAll}}
	case len(args.Edits) == 0:
		return args, fmt.Errorf("old_text is required, or edits with one entry per replacement")
	}
	for i, e := range args.Edits {
		switch e.OldText {
		case "":
			return args, fmt.Errorf("%sold_text is required", editLabel(args.Edits, i))
		case e.NewText:
			return args, fmt.Errorf("%sold_text and new_text are identical", editLabel(args.Edits, i))
		}
	}
	return args, nil
}

func (r *Recorder) executeEditFile(raw json.RawMessage) (string, error) {
	args, err := parseEditFileArgs(raw)
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(args.Path)
	if err != nil {
		return "", fmt.Errorf("cannot read file: %w", err)
	}
	// An edit needs no prior read — old_text is its own evidence — but a file
	// that moved since it was read is one this edit was not written against.
	// One question about the file answers it for every edit in the call,
	// because every one of them is matched against this content.
	if err := r.checkSeen(args.Path, content, true, false); err != nil {
		return "", err
	}
	// Every edit is planned before any of them is written, so a call with one
	// bad quote leaves the file exactly as it was rather than half changed.
	updated, count, err := applyEdits(string(content), args.Path, args.Edits)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(args.Path, []byte(updated), newFileMode); err != nil {
		// The file is now of unknown content, so nothing may be claimed about
		// it until something reads it again.
		r.forget(args.Path)
		return "", fmt.Errorf("cannot write file: %w", err)
	}
	// The model knows exactly what it just wrote, so the next edit is not
	// asked to read it again.
	r.noteShown(args.Path, []byte(updated), true)
	made := fmt.Sprintf("%d replacement(s)", count)
	if len(args.Edits) > 1 {
		made += fmt.Sprintf(" from %d edits", len(args.Edits))
	}
	return fmt.Sprintf("Edited %s: %s, file now %d bytes (%d lines)", args.Path, made, len(updated), countLines(updated)), nil
}

// match is one place an edit claims: a half-open byte range of the file as it
// was read, and which edit claimed it.
type match struct {
	start, end int
	edit       int
}

// applyEdits is the one validator, shared by execution and the approval
// preview — a card that offered a change the write then refused would be an
// approval given for something that never happened.
//
// Every quote is matched against content as it was read rather than against
// the result of the edit before it. That is what makes the order of the list
// irrelevant: a model listing three changes to one file is describing places,
// not steps, and an incremental apply would make the second edit's meaning
// depend on the first's. The cost is that two edits claiming the same bytes
// have no order to be resolved by, so they are refused naming both rather
// than silently resolved by position in the array.
// See docs/capabilities/coding-agent.md#several-places-in-one-file-are-one-call.
func applyEdits(content, path string, edits []fileEdit) (string, int, error) {
	var matches []match
	for i, e := range edits {
		// Occurrences are counted by walking them, because they are what the
		// splice needs anyway: they are non-overlapping in the same sense
		// strings.Count and strings.ReplaceAll are, so "aa" in "aaa" is one.
		var starts []int
		for off := 0; ; {
			at := strings.Index(content[off:], e.OldText)
			if at < 0 {
				break
			}
			starts = append(starts, off+at)
			off += at + len(e.OldText)
		}
		label := editLabel(edits, i)
		switch {
		case len(starts) == 0:
			if looksLineNumbered(e.OldText) {
				return "", 0, fmt.Errorf("%sold_text not found in %s; it looks like it still carries read_file's line-number prefixes — strip the leading digits and tab from each line and try again", label, path)
			}
			if miss, ok := nearMiss(content, e.OldText); ok {
				return "", 0, fmt.Errorf("%sold_text not found in %s; %s from it only in whitespace: %s — re-quote it from the file, indentation and all", label, path, miss.where(), snippet(miss.actual))
			}
			return "", 0, fmt.Errorf("%sold_text not found in %s; it must match the file content exactly, including whitespace", label, path)
		case len(starts) > 1 && !e.ReplaceAll:
			return "", 0, fmt.Errorf("%sold_text matches %d locations in %s; provide a longer unique snippet or set replace_all", label, len(starts), path)
		}
		if !e.ReplaceAll {
			starts = starts[:1]
		}
		for _, start := range starts {
			matches = append(matches, match{start: start, end: start + len(e.OldText), edit: i})
		}
	}
	slices.SortFunc(matches, func(a, b match) int { return cmp.Compare(a.start, b.start) })
	for i := 1; i < len(matches); i++ {
		prev, m := matches[i-1], matches[i]
		if m.start >= prev.end {
			continue
		}
		// The matches are in file order and the model wrote a list, so the
		// two are named in the order it listed them: a refusal that says
		// "edits 2 and 1" makes the reader check whether it means something.
		a, b := min(prev.edit, m.edit), max(prev.edit, m.edit)
		return "", 0, fmt.Errorf("edits %d and %d overlap in %s: %s and %s claim the same text; quote text that does not overlap, or combine them into one edit",
			a+1, b+1, path, snippet(edits[a].OldText), snippet(edits[b].OldText))
	}

	var b strings.Builder
	b.Grow(len(content))
	at := 0
	for _, m := range matches {
		b.WriteString(content[at:m.start])
		b.WriteString(edits[m.edit].NewText)
		at = m.end
	}
	b.WriteString(content[at:])
	return b.String(), len(matches), nil
}

// whitespaceMiss is the one place a failed quote nearly matched: the 1-based
// line range it covers, and its first line as the file actually writes it.
type whitespaceMiss struct {
	first, last int
	actual      string
}

// where names the range and agrees its verb with it, for a sentence that
// reads the same whether one line was quoted or ten.
func (w whitespaceMiss) where() string {
	if w.first == w.last {
		return fmt.Sprintf("line %d differs", w.first)
	}
	return fmt.Sprintf("lines %d-%d differ", w.first, w.last)
}

// nearMiss finds the single place in content whose lines differ from want
// only in leading and trailing whitespace, for a quote that matched nothing.
//
// A changed indent is the commonest way a quote from a search result, a diff
// or a wrapped paste stops matching, and it is the one miss the model cannot
// diagnose from the refusal: the text it sent and the text in the file look
// identical on screen. Named, it is one re-quote; unnamed, it is a re-read of
// a file the model already has, and the read is what the reduction and the
// window were spent avoiding.
//
// It is never applied. Whitespace is not decoration in every language, and
// this is a guess about what the model meant — the model can see the line it
// is being shown and quote it, which is the same round either way, without
// anything being written on a machine's guess.
//
// Exactly one place, or nothing: two candidates make the message ambiguous,
// and a quote of nothing but whitespace matches every blank run in the file,
// so it is not a near miss but a different mistake.
func nearMiss(content, want string) (whitespaceMiss, bool) {
	wantLines := strings.Split(strings.TrimSuffix(want, "\n"), "\n")
	trimmed := make([]string, len(wantLines))
	blank := true
	for i, line := range wantLines {
		trimmed[i] = strings.TrimSpace(line)
		if trimmed[i] != "" {
			blank = false
		}
	}
	if blank {
		return whitespaceMiss{}, false
	}

	lines := strings.Split(content, "\n")
	// Trimmed once rather than inside the window: a fifty-line quote against a
	// ten-thousand-line file is half a million comparisons, and every line
	// would otherwise be trimmed once per window it appears in.
	bare := make([]string, len(lines))
	for i, line := range lines {
		bare[i] = strings.TrimSpace(line)
	}
	found := -1
	for i := 0; i+len(trimmed) <= len(lines); i++ {
		match := true
		for j, wantLine := range trimmed {
			if bare[i+j] != wantLine {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if found >= 0 {
			return whitespaceMiss{}, false
		}
		found = i
	}
	if found < 0 {
		return whitespaceMiss{}, false
	}
	return whitespaceMiss{first: found + 1, last: found + len(trimmed), actual: lines[found]}, true
}

// editLabel names which edit a message is about, and says nothing at all when
// the call carries one: there is only one thing that sentence could be about,
// and every message here reads as it always did for the single-pair call.
func editLabel(edits []fileEdit, i int) string {
	if len(edits) < 2 {
		return ""
	}
	return fmt.Sprintf("edit %d (%s): ", i+1, snippet(edits[i].OldText))
}

// snippet is an old_text short enough to name in a sentence: its first line,
// quoted, cut at a width that still says which edit is meant. Quoting is what
// makes leading whitespace visible, which is the difference the reader is
// most often looking for.
func snippet(s string) string {
	line, cut := s, false
	if at := strings.IndexByte(line, '\n'); at >= 0 {
		line, cut = line[:at], true
	}
	// Wide enough for a signature or a struct field, short enough that two of
	// them and the sentence around them still fit a terminal line.
	const width = 40
	if utf8.RuneCountInString(line) > width {
		line, cut = string([]rune(line)[:width]), true
	}
	if cut {
		return strconv.Quote(line) + "…"
	}
	return strconv.Quote(line)
}

// readIfExists returns the file's content, or existed=false for a missing
// file; any other read failure is an error.
func readIfExists(path string) (content string, existed bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("cannot read file: %w", err)
	}
	return string(data), true, nil
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// looksLineNumbered reports whether every line of s opens with digits and a
// tab — the shape read_file returns. It is only ever asked after a match has
// already failed, so a file that genuinely looks like this loses nothing but
// a more specific error message.
func looksLineNumbered(s string) bool {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	if len(lines) == 0 {
		return false
	}
	for _, line := range lines {
		tab := strings.IndexByte(line, '\t')
		if tab <= 0 {
			return false
		}
		for _, r := range line[:tab] {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}
