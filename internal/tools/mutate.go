package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/receipt/describe"
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

// WrittenPaths is every file a mutating call names — its path, or each entry
// of an edit's list of files — and nothing for any other call, for arguments
// that do not parse, and for a call that named no path.
//
// The surfaces that keep no changeset read what a run has written off the
// calls that wrote it — an unattended run for its tree reading and its git
// stager, a sub-agent for the digest its readings are made of — and the
// gates read a write's targets off it too, so they share this reading rather
// than each parsing the arguments again. It was one path once, on the
// reading that a write is one of two tools and names one file; an edit that
// names several broke the second half, and a copy that kept reading `path`
// would have answered for the first file of a list and let the rest through
// unread.
func WrittenPaths(name, rawArgs string) []string {
	if !IsMutating(name) {
		return nil
	}
	return callPaths(rawArgs)
}

// callPaths is WrittenPaths without asking whether the call writes: the path
// and the list of files a file tool's arguments name. It is apart so the
// edit tool's own describer can read it without the definition referring to
// the list of definitions it is in.
func callPaths(rawArgs string) []string {
	var args struct {
		Path  string   `json:"path"`
		Paths []string `json:"paths"`
	}
	if json.Unmarshal([]byte(rawArgs), &args) != nil {
		return nil
	}
	var out []string
	if p := strings.TrimSpace(args.Path); p != "" {
		out = append(out, p)
	}
	for _, p := range args.Paths {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// refuseCallPaths refuses a write or an edit of shhh's own files, whatever
// the mode: the diff and the near-miss text an edit answers with are the file
// read back, and a write over the sign-in or sandbox record is shhh's state
// rewritten behind one approval card.
func refuseCallPaths(raw json.RawMessage) error {
	for _, p := range callPaths(string(raw)) {
		if err := refuseSecret(p); err != nil {
			return err
		}
	}
	return nil
}

// ExecuteMutating dispatches a user-approved file-modification tool call. It
// refuses every other tool name so read-only tools cannot be routed here by
// mistake.
func ExecuteMutating(ctx context.Context, name string, args json.RawMessage) (string, error) {
	return shared.ExecuteMutating(ctx, name, args)
}

// ExecuteMutating against r's record, which is what an owner that does not
// share the process's record hands its approver.
func (r *Recorder) ExecuteMutating(ctx context.Context, name string, args json.RawMessage) (string, error) {
	for _, d := range r.Mutating() {
		if d.Tool.Name == name {
			return d.Execute(ctx, args)
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
	// Executable is a write that also adds the execute bits, so the card can
	// say so beside the diff.
	Executable bool
	// Files is an edit across a list of files, one entry per file listed in
	// the order the call listed them, and Path is then empty: there is no
	// one file the card is about.
	Files []FileMutation
	// Pattern and Replacement are the edit's old_text and new_text as the
	// call wrote them, and Regex whether the first is a pattern. They are
	// set for an edit across files and for a pattern edit of one, which are
	// the two cards where the change is not read off its diff alone.
	Pattern, Replacement string
	Regex                bool
}

// FileMutation is one file of an edit across a list: its content now, what
// the edit would leave, and how many places it replaces — zero for a file the
// pattern does not match, which the edit leaves alone.
type FileMutation struct {
	Path             string
	OldText, NewText string
	Replacements     int
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
	if err := refuseCallPaths(raw); err != nil {
		return Mutation{}, err
	}
	switch name {
	case WriteFileName:
		args, err := parseWriteFileArgs(raw, false)
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
		return Mutation{Action: "write", Path: args.Path, OldText: old, NewText: args.Content, Executable: args.Executable}, nil
	case EditFileName:
		args, err := parseEditFileArgs(raw)
		if err != nil {
			return Mutation{}, err
		}
		if len(args.Paths) > 0 {
			plans, err := r.planPaths(args)
			if err != nil {
				return Mutation{}, err
			}
			m := Mutation{Action: "edit", Pattern: args.Edits[0].OldText, Replacement: args.Edits[0].NewText, Regex: args.Regex}
			for _, p := range plans {
				m.Files = append(m.Files, FileMutation{Path: p.path, OldText: string(p.before), NewText: p.after, Replacements: len(p.repls)})
			}
			return m, nil
		}
		content, err := os.ReadFile(args.Path)
		if err != nil {
			return Mutation{}, fmt.Errorf("cannot read file: %w", err)
		}
		updated, _, err := r.planEdit(content, args)
		if err != nil {
			return Mutation{}, err
		}
		action := "edit"
		if args.Append != "" {
			action = "append"
		}
		m := Mutation{Action: action, Path: args.Path, OldText: string(content), NewText: updated}
		m.Regex = args.Regex
		if args.Regex && len(args.Edits) == 1 {
			m.Pattern, m.Replacement = args.Edits[0].OldText, args.Edits[0].NewText
		}
		return m, nil
	}
	return Mutation{}, fmt.Errorf("unknown mutating tool: %s", name)
}

// The two descriptions below say a change is gated and leave the rest to the
// permission mode, because promising that the user sees a diff and answers for
// it is true in one of the five modes: --yes, accept-edits and auto each apply
// writes without asking, read-only and plan refuse the write outright, and a
// model told otherwise plans around a pause that never comes and reports its
// own edits as pending.
// See docs/capabilities/approvals-and-safety.md#the-five-modes.
var writeFile = Definition{
	Receipt: describe.Describer{Kind: describe.KindWrite, Verb: "write",
		Does: "a new file, or one written whole"},
	Tool: provider.Tool{
		Name: WriteFileName,
		Description: "Create or overwrite a file with the given content. content is written verbatim — never include read_file's line-number prefixes. " +
			"Overwriting an existing file requires having read it in full first, and fails if it has changed since: prefer edit_file for changing part of a file you have already read. " +
			"Missing parent directories are created automatically. " +
			"A path git runs or reads as configuration — a hook, .git/config, .git/info, a worktree's .git file — is refused in every mode. " +
			"Whether the change is applied straight away or is shown to the user as a diff for approval first is the session's permission mode's to decide; a declined call returns an error result.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "File path to create or overwrite"},
				"content": {"type": "string", "description": "Full content the file will have"},
				"executable": {"type": "boolean", "description": "Set true to make the file executable (adds the execute bits to its mode), for a script you are writing. False or absent leaves the mode as it is: a new file is not executable, and an overwritten script stays executable. Do not run chmod +x for this."}
			},
			"required": ["path", "content"]
		}`),
	},
	// Execute is filled in by Mutating, which is where the record the call is
	// checked against is known.
}

type writeFileArgs struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Executable bool   `json:"executable"`
}

func parseWriteFileArgs(raw json.RawMessage, byPerson bool) (writeFileArgs, error) {
	var args writeFileArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Path == "" {
		return args, fmt.Errorf("path is required")
	}
	if !byPerson && GitProgramPath("", args.Path) {
		return args, ErrGitProgramPath
	}
	return args, nil
}

func (r *Recorder) executeWriteFile(_ context.Context, raw json.RawMessage) (string, error) {
	if err := refuseCallPaths(raw); err != nil {
		return "", err
	}
	return r.writeFile(raw, false)
}

// WriteByPerson is the write tool's write for a file the person saves
// themselves, in the edit pane. It is the same write with the same checks
// against what it overwrites, minus the refusal of a path git runs: that
// rule is about what the model may write, and a person who opens
// .git/hooks/pre-commit and saves it has meant to, as they mean a command
// typed after `!`. No model-facing path reaches it.
func (r *Recorder) WriteByPerson(raw json.RawMessage) (string, error) {
	return r.writeFile(raw, true)
}

func (r *Recorder) writeFile(raw json.RawMessage, byPerson bool) (string, error) {
	args, err := parseWriteFileArgs(raw, byPerson)
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
	exec := ""
	if args.Executable {
		if err := addExecuteBits(args.Path); err != nil {
			r.forget(args.Path)
			return "", fmt.Errorf("wrote the file but cannot make it executable: %w", err)
		}
		exec = ", executable"
	}
	r.noteShown(args.Path, []byte(args.Content), true)
	if existed {
		return fmt.Sprintf("Overwrote %s: wrote %d bytes (%d lines), was %d bytes%s", args.Path, len(args.Content), countLines(args.Content), len(old), exec), nil
	}
	return fmt.Sprintf("Created %s: wrote %d bytes (%d lines)%s", args.Path, len(args.Content), countLines(args.Content), exec), nil
}

// addExecuteBits adds an execute bit wherever the file already has a read
// bit, so the umask that made a new file 0644 or 0640 is honoured (0755, 0750)
// and an existing mode is only ever added to, never stripped. A script's mode
// is the write's and not a command's.
// See docs/capabilities/coding-agent.md#the-built-in-tools-come-before-the-shell.
func addExecuteBits(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := info.Mode().Perm()
	return os.Chmod(path, mode|(mode&0o444)>>2)
}

var editFile = Definition{
	Receipt: describe.Describer{Kind: describe.KindWrite, Verb: "edit",
		Subject: editSubject, Count: countEdited,
		Does: "a change to part of a file"},
	Tool: provider.Tool{
		Name: EditFileName,
		Description: "Change an existing file: replace exact text snippets in it, or add text to its end. Give one replacement as old_text/new_text, several as edits — one entry per place — or the text to add as append, and never two of these in the same call. " +
			"Batch changes to one file into one call. One old_text/new_text applied to several files is one call naming them in paths instead of path; different changes to different files are separate calls. " +
			"Every old_text must match the file content exactly (including whitespace) and match exactly once, unless replace_all is set. " +
			"Set regex when the replacement depends on what matched: old_text is then a pattern and new_text may use its groups. " +
			"Strip read_file's `<line number>\t` prefix before quoting a line here; the numbers are not in the file. " +
			"Every quote is matched against the file as it stands, not against the edit before it, so the order does not matter and two edits that would touch the same text are refused. Nothing is written unless all of them apply. " +
			"A file that has changed since you last read it is refused: read it again and rebase the edits. " +
			"append is for adding to the end of a file without reading it first — a new entry in a log, a changelog, a backlog or a list: it quotes nothing, so a file that moved is not refused; a newline goes before the text when the file does not end with one. Use it rather than a shell redirect. " +
			"Whether the change is applied straight away or shown for approval is the session's permission mode's to decide; a declined call returns an error result.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "File path to edit"},
				"paths": {"type": "array", "items": {"type": "string"}, "description": "Several files to apply one old_text/new_text to, in place of path — a rename or an added argument across a package. Each is read, changed and recorded as its own file, and can be undone on its own. List each file by name, at most 50: a glob is not taken, so get the list from search or fd first. Every file has to be one you have read, or one a search in this turn found a match in; a file the pattern does not match is reported and left alone, unless none matches. Not with edits or append"},
				"old_text": {"type": "string", "description": "Exact existing text to replace, for a call that changes one place"},
				"new_text": {"type": "string", "description": "Replacement text, for a call that changes one place"},
				"replace_all": {"type": "boolean", "description": "Replace every occurrence instead of requiring a unique match"},
				"regex": {"type": "boolean", "description": "Treat each old_text as a regular expression in the search tool's dialect (RE2), case-sensitive, with ^ and $ matching at line ends; prefix (?i) to ignore case. new_text may then use $1 or ${name} for what a group matched; write ${1}x when a letter, digit or underscore follows the group, and $$ for a literal $. A pattern that can match empty text is refused. Leave it off for literal text, where nothing needs escaping"},
				"append": {"type": "string", "description": "Text to add at the end of the existing file, for a call that only adds — a log line, a changelog entry, an item on a list. It needs no read_file first. Give it without old_text/new_text or edits"},
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
			}
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
	// Append is the third form: text added at the end of the file. It is
	// kept apart from Edits rather than folded into them because it quotes
	// nothing, and a quote is what every rule applied to Edits is about.
	Append string `json:"append,omitempty"`
	// Paths is the call's list of files, in place of Path, for one
	// replacement applied to each (editacross.go).
	Paths []string `json:"paths,omitempty"`
	// Regex reads every OldText as a pattern and every NewText as a
	// template over its groups.
	Regex bool `json:"regex,omitempty"`
}

// parseEditFileArgs reads the call and normalises both spellings into Edits,
// so everything downstream sees a list and there is one code path to be right
// about. The inline pair stays because a model that has been calling this
// tool for a whole conversation should not have its next call refused for
// spelling a single replacement the way it always has.
//
// Carrying both spellings at once is refused rather than merged: the two
// orders that merge could produce are different files, and picking one on the
// model's behalf is picking silently. An append beside either is refused for
// the same reason: whether it lands at the end of the file before the
// replacements or after them is a choice nobody made.
func parseEditFileArgs(raw json.RawMessage) (editFileArgs, error) {
	var args editFileArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, fmt.Errorf("invalid arguments: %w", err)
	}
	switch {
	case args.Path != "" && len(args.Paths) > 0:
		return args, fmt.Errorf("give either path or paths, not both: put every file in paths")
	case len(args.Paths) > 0:
		if err := checkPathList(args); err != nil {
			return args, err
		}
	case args.Path == "":
		return args, fmt.Errorf("path is required, or paths for one replacement across several files")
	case GitProgramPath("", args.Path):
		return args, ErrGitProgramPath
	}
	inline := args.OldText != "" || args.NewText != ""
	switch {
	case args.Append != "" && (inline || len(args.Edits) > 0):
		return args, fmt.Errorf("give either append or a replacement (old_text/new_text or edits), not both: make the other change a second call")
	case args.Append != "":
		return args, nil
	case inline && len(args.Edits) > 0:
		return args, fmt.Errorf("give either old_text/new_text or edits, not both: put every replacement in edits")
	case inline:
		args.Edits = []fileEdit{{OldText: args.OldText, NewText: args.NewText, ReplaceAll: args.ReplaceAll}}
	case len(args.Edits) == 0:
		return args, fmt.Errorf("old_text is required, or edits with one entry per replacement, or append to add text at the end")
	}
	for i, e := range args.Edits {
		switch {
		case e.OldText == "":
			return args, fmt.Errorf("%sold_text is required", editLabel(args.Edits, i))
		case args.Regex:
			// A pattern and a template that read the same can still change
			// the file — `\.` replaced by `\.` writes a backslash — so the
			// identical-text refusal is a literal edit's alone. What a
			// pattern is refused for is asked once here, before any file
			// is read, so a bad pattern is one error and not one per file.
			if _, err := compileEditPattern(e.OldText, e.NewText); err != nil {
				return args, fmt.Errorf("%s%w", editLabel(args.Edits, i), err)
			}
		case e.OldText == e.NewText:
			return args, fmt.Errorf("%sold_text and new_text are identical", editLabel(args.Edits, i))
		}
	}
	return args, nil
}

func (r *Recorder) executeEditFile(_ context.Context, raw json.RawMessage) (string, error) {
	if err := refuseCallPaths(raw); err != nil {
		return "", err
	}
	args, err := parseEditFileArgs(raw)
	if err != nil {
		return "", err
	}
	if len(args.Paths) > 0 {
		return r.editPaths(args)
	}
	content, err := os.ReadFile(args.Path)
	if err != nil {
		return "", fmt.Errorf("cannot read file: %w", err)
	}
	updated, repls, err := r.planEdit(content, args)
	if err != nil {
		return "", err
	}
	count := len(repls)
	if err := os.WriteFile(args.Path, []byte(updated), newFileMode); err != nil {
		// The file is now of unknown content, so nothing may be claimed about
		// it until something reads it again.
		r.forget(args.Path)
		return "", fmt.Errorf("cannot write file: %w", err)
	}
	if args.Append != "" {
		r.noteAppended(args.Path, content, []byte(updated))
		return fmt.Sprintf("Appended %d bytes (%d lines) to %s, file now %d bytes (%d lines)",
			len(args.Append), countLines(args.Append), args.Path, len(updated), countLines(updated)), nil
	}
	// The model knows exactly what it just wrote, so the next edit is not
	// asked to read it again.
	_, had := r.lookupSeen(args.Path)
	r.noteShown(args.Path, []byte(updated), true)
	if !had {
		// Recorded so the next edit is not refused as stale, but not as a
		// read: a later list of files does not admit it on this alone.
		r.markUnread(args.Path)
	}
	made := fmt.Sprintf("%d replacement(s)", count)
	if len(args.Edits) > 1 {
		made += fmt.Sprintf(" from %d edits", len(args.Edits))
	}
	out := fmt.Sprintf("Edited %s: %s, file now %d bytes (%d lines)", args.Path, made, len(updated), countLines(updated))
	if args.Regex {
		out += replacedList([]plannedFile{{path: args.Path, repls: repls}}, false)
	}
	return out, nil
}

// planEdit is the file a call would leave behind, asked by the preview and by
// the write alike so a card never shows a change the write then refuses.
//
// An append takes no staleness check: it quotes nothing, so there is no
// picture of the file for a move to have made wrong, and the end of the file
// is the end of whatever the file says now. That is the reason the form
// exists — a log or a changelog is exactly the file something else keeps
// writing to, and a model sent to re-read one before adding a line to it
// reaches for a shell redirect instead.
// See docs/capabilities/coding-agent.md#adding-to-the-end-of-a-file-needs-no-read.
func (r *Recorder) planEdit(content []byte, args editFileArgs) (string, []replacement, error) {
	if args.Append != "" {
		return appendText(string(content), args.Append), nil, nil
	}
	// An edit needs no prior read — old_text is its own evidence — but a file
	// that moved since it was read is one this edit was not written against.
	// One question about the file answers it for every edit in the call,
	// because every one of them is matched against this content.
	if err := r.checkSeen(args.Path, content, true, false); err != nil {
		return "", nil, err
	}
	// Every edit is planned before any of them is written, so a call with one
	// bad quote leaves the file exactly as it was rather than half changed.
	return applyEdits(string(content), args.Path, args.Edits, args.Regex, false)
}

// appendText puts text after content, starting it on a line of its own. The
// model appending has not seen whether the file ends in a newline, and an
// entry run onto the end of the previous line is a failure it cannot see. An
// empty file has no line to separate from.
func appendText(content, text string) string {
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content + text
}

// match is one place an edit claims: a half-open byte range of the file as it
// was read, and which edit claimed it.
type match struct {
	start, end int
	edit       int
	// repl is what goes in its place: the edit's new_text, or for a pattern
	// that template expanded against this match's groups.
	repl string
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
//
// A pattern edit takes the same road: its matches are places like a quote's,
// with their own replacement each, so the uniqueness rule, the overlap rule
// and the all-or-nothing splice are the literal edit's and not a second set.
// missOK is an edit across a list of files, where a file the pattern does not
// match is left alone rather than refused: the caller counts the misses.
func applyEdits(content, path string, edits []fileEdit, regex, missOK bool) (string, []replacement, error) {
	var matches []match
	for i, e := range edits {
		label := editLabel(edits, i)
		found, err := findEdit(content, path, e, regex)
		if err != nil {
			return "", nil, fmt.Errorf("%s%w", label, err)
		}
		switch {
		case len(found) == 0 && missOK:
			continue
		case len(found) == 0 && regex:
			return "", nil, fmt.Errorf("%sold_text pattern matches nothing in %s", label, path)
		case len(found) == 0:
			if looksLineNumbered(e.OldText) {
				return "", nil, fmt.Errorf("%sold_text not found in %s; it looks like it still carries read_file's line-number prefixes — strip the leading digits and tab from each line and try again", label, path)
			}
			if miss, ok := nearMiss(content, e.OldText); ok {
				return "", nil, fmt.Errorf("%sold_text not found in %s; %s from it only in whitespace%s: %s — re-quote it from the file, indentation and all", label, path, miss.where(), miss.indentGap(), snippet(miss.actual))
			}
			return "", nil, fmt.Errorf("%sold_text not found in %s; it must match the file content exactly, including whitespace", label, path)
		case len(found) > 1 && !e.ReplaceAll:
			return "", nil, fmt.Errorf("%sold_text matches %d locations in %s; provide a longer unique snippet or set replace_all", label, len(found), path)
		}
		if !e.ReplaceAll {
			found = found[:1]
		}
		for _, m := range found {
			m.edit = i
			matches = append(matches, m)
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
		return "", nil, fmt.Errorf("edits %d and %d overlap in %s: %s and %s claim the same text; quote text that does not overlap, or combine them into one edit",
			a+1, b+1, path, snippet(edits[a].OldText), snippet(edits[b].OldText))
	}

	var b strings.Builder
	b.Grow(len(content))
	repls := make([]replacement, 0, len(matches))
	at := 0
	for _, m := range matches {
		b.WriteString(content[at:m.start])
		b.WriteString(m.repl)
		repls = append(repls, replacement{line: 1 + strings.Count(content[:m.start], "\n"), before: content[m.start:m.end], after: m.repl})
		at = m.end
	}
	b.WriteString(content[at:])
	return b.String(), repls, nil
}

// findEdit is every place one edit matches in content, in file order and not
// overlapping, each with its replacement. A literal is walked with
// strings.Index — non-overlapping in the sense strings.Count and
// strings.ReplaceAll are, so "aa" in "aaa" is one — and a pattern is matched
// by the expression the search tool compiles, with new_text expanded against
// each match's own groups.
func findEdit(content, path string, e fileEdit, regex bool) ([]match, error) {
	var found []match
	if !regex {
		for off := 0; ; {
			at := strings.Index(content[off:], e.OldText)
			if at < 0 {
				return found, nil
			}
			found = append(found, match{start: off + at, end: off + at + len(e.OldText), repl: e.NewText})
			off += at + len(e.OldText)
		}
	}
	re, err := compileEditPattern(e.OldText, e.NewText)
	if err != nil {
		return nil, err
	}
	for _, loc := range re.FindAllStringSubmatchIndex(content, -1) {
		if loc[0] == loc[1] {
			// compileEditPattern refuses a pattern that matches the empty
			// string, and this is the rest of that rule: `\b` or `(?m)$`
			// matches nothing at the start of an empty text and empty text
			// all through a real one. A replacement there inserts at a
			// position rather than replacing anything, which is not the edit
			// the call describes.
			return nil, fmt.Errorf("old_text pattern matches empty text at line %d of %s; anchor it to text it must find", 1+strings.Count(content[:loc[0]], "\n"), path)
		}
		found = append(found, match{start: loc[0], end: loc[1], repl: string(re.ExpandString(nil, e.NewText, content, loc))})
	}
	return found, nil
}

// whitespaceMiss is the one place a failed quote nearly matched: the 1-based
// line range it covers, its first line as the file actually writes it, and
// the first line as the quote wrote it, so the refusal can spell out the
// whitespace difference instead of asking the reader to count tabs.
type whitespaceMiss struct {
	first, last int
	actual      string
	quoted      string
}

// where names the range and agrees its verb with it, for a sentence that
// reads the same whether one line was quoted or ten.
func (w whitespaceMiss) where() string {
	if w.first == w.last {
		return fmt.Sprintf("line %d differs", w.first)
	}
	return fmt.Sprintf("lines %d-%d differ", w.first, w.last)
}

// indentGap names the leading whitespace mismatch on the first line when it
// is only a difference of tabs versus spaces or depth. It keeps the refusal
// actionable without guessing what the model meant.
func (w whitespaceMiss) indentGap() string {
	actualIndent := leadingSpace(w.actual)
	quotedIndent := leadingSpace(w.quoted)
	if actualIndent == quotedIndent {
		return ""
	}
	return fmt.Sprintf(" (file begins with %s, your quote begins with %s)", reprWhitespace(actualIndent), reprWhitespace(quotedIndent))
}

// leadingSpace returns the leading tabs and spaces of s.
func leadingSpace(s string) string {
	for i, r := range s {
		if r != '\t' && r != ' ' {
			return s[:i]
		}
	}
	return s
}

// reprWhitespace spells leading whitespace so a reader can see tabs and count
// spaces without parsing escape sequences.
func reprWhitespace(s string) string {
	if s == "" {
		return "no indentation"
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\t':
			b.WriteString("\\t")
		case ' ':
			b.WriteByte('·')
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
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
	return whitespaceMiss{first: found + 1, last: found + len(trimmed), actual: lines[found], quoted: wantLines[0]}, true
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
