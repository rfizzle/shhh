package structural

// The writing half of git, built the way the reading half is: a closed set of
// verbs with no field a fifth could arrive in, so what it cannot do is a fact
// about the argv builder rather than a promise about intent.
//
// It exists because a commit is the one act of a coding turn that the
// allowlist can never pre-approve — the allowlist refuses a line carrying
// shell punctuation, and a commit message is quoted text — so every commit
// was a classifier round or a card, however many times the person had said
// yes. Carrying the message as a field costs neither.
// See docs/capabilities/approvals-and-safety.md#the-writing-half-of-git-is-a-tool-too.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

// The write verbs. Four, and no more: staging, committing, making a branch
// and standing on one are what a coding turn ends with. Everything else git
// can do to a repository — push, reset, clean, checkout of paths, rebase,
// merge, stash, tag — has no field here to arrive in, so it stays a command
// and still asks.
const (
	gitAdd    = "add"
	gitCommit = "commit"
	gitBranch = "branch"
	gitSwitch = "switch"
)

var gitWriteTool = provider.Tool{
	Name: GitWriteToolName,
	Description: "Write to this repository: stage files, commit them, create a branch, switch to one. " +
		"Use this rather than running git through execute_command — the message is a field here, so no quoting is involved and no approval is spent on punctuation. " +
		"add stages only files this session changed: anything else is refused by name, and there is no way to stage everything. " +
		"commit needs a staged index and writes the message verbatim. branch creates and never deletes; switch moves to an existing branch, or to a new one off the current commit with create. " +
		"Nothing else is reachable here: push, reset, clean, checkout, rebase, merge, stash, tag, amend and force have no field, and pushing stays with execute_command.",
	Parameters: json.RawMessage(`{
		"type": "object",
		"properties": {
			"verb": {"type": "string", "enum": ["add", "commit", "branch", "switch"], "description": "Which write to make"},
			"paths": {"type": "array", "items": {"type": "string"}, "description": "add only: the files to stage, named one by one, relative to the workspace root"},
			"message": {"type": "string", "description": "commit only: the whole commit message, written verbatim"},
			"branch": {"type": "string", "description": "branch/switch only: the branch name"},
			"create": {"type": "boolean", "description": "switch only: create the branch off the current commit instead of expecting it to exist"}
		},
		"required": ["verb"]
	}`),
}

type gitWriteArgs struct {
	Verb    string   `json:"verb"`
	Paths   []string `json:"paths"`
	Message string   `json:"message"`
	Branch  string   `json:"branch"`
	Create  bool     `json:"create"`
}

// gitWriteFields is each verb's vocabulary, read the way the reader's is: a
// field the verb does not name is refused rather than dropped, because a
// commit that silently ignored a paths list is a commit whose caller believes
// it staged something.
var gitWriteFields = map[string][]string{
	gitAdd:    {"paths"},
	gitCommit: {"message"},
	gitBranch: {"branch"},
	gitSwitch: {"branch", "create"},
}

// setFields names the optional fields this call actually filled in.
func (a gitWriteArgs) setFields() []string {
	var f []string
	if len(a.Paths) > 0 {
		f = append(f, "paths")
	}
	if a.Message != "" {
		f = append(f, "message")
	}
	if a.Branch != "" {
		f = append(f, "branch")
	}
	if a.Create {
		f = append(f, "create")
	}
	return f
}

// Writes is what a session hands the writing half of git, and both halves of
// it are things only the session knows.
//
// Files is the record of what this session changed, which is the one thing a
// shell cannot see: `git add -A` stages the person's uncommitted morning
// beside the agent's afternoon, and a stager that can only name paths this
// record holds cannot. Hooks is the checkout's trust answer, because a commit
// hook is a program the checkout can point git at.
type Writes struct {
	// Files answers with the paths this session changed, in whatever form
	// the front-end holds them; they are resolved against the workspace root
	// here, so a caller need not.
	Files func() []string
	// Hooks says the checkout's own programs may run on a commit. False
	// commits with --no-verify and says so.
	Hooks bool
}

// AllowWrites registers the write tool on a toolset that already found git.
// It is a separate call rather than part of NewToolset because the write
// tier is a decision about the surface, not about the machine: a session with
// somebody to ask gets it, and a sub-agent never does — a child's work comes
// back to its parent as a patch, and a child that could commit would be
// writing history nobody approved.
func (t *Toolset) AllowWrites(w Writes) {
	if t == nil {
		return
	}
	bin, ok := t.bins[GitToolName]
	if !ok {
		return
	}
	t.writes = &w
	t.bins[GitWriteToolName] = bin
}

// checkGitBranch reads a branch name under the same charset a ref is read
// under. It is the reader's rule and not a second one: a name that cannot be
// a ref cannot be a branch either, and the exclusions that matter — no
// leading "-", no ":" — are the same exclusions for the same reasons.
func checkGitBranch(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("branch is required: name the branch")
	}
	return checkGitRef("branch", name)
}

// buildGitWriteArgv constructs git's argv for one write verb. The invariants
// the reader's builder holds hold here too — the verb comes from a closed
// set, every path follows a literal "--", --no-pager and --no-optional-locks
// are on every spawn, and the flags that write or execute a file have no
// field — with three more that are this builder's own:
//
//   - The message rides in a file, never in the argv. A message is prose
//     somebody wrote, and prose in an argv is the one place shell punctuation
//     would become a shell fact again, which is the whole reason this tool
//     exists.
//   - No flag that discards work is in the vocabulary: --amend, --force,
//     --discard-changes, --merge, --hard, --author, --allow-empty and -A have
//     no field to arrive in, so a call cannot rewrite a commit, throw away a
//     dirty tree, invent an author or stage what it was not given.
//   - --no-verify is the one flag decided here rather than by the caller, and
//     it is decided by the checkout's trust answer.
func buildGitWriteArgv(a gitWriteArgs, paths []string, messageFile string, hooks bool) ([]string, error) {
	allowed, known := gitWriteFields[a.Verb]
	if !known {
		return nil, fmt.Errorf("unknown verb %q: use add, commit, branch, or switch", a.Verb)
	}
	for _, f := range a.setFields() {
		if !slices.Contains(allowed, f) {
			return nil, fmt.Errorf("%s does not take %s; it takes %s", a.Verb, f, strings.Join(allowed, ", "))
		}
	}

	argv := []string{"--no-pager", "--no-optional-locks", a.Verb}
	switch a.Verb {
	case gitAdd:
		if len(paths) == 0 {
			return nil, fmt.Errorf("add needs paths: name the files to stage, one by one")
		}

	case gitCommit:
		// The message is checked by the caller, which is also what put it in
		// a file: by the time an argv is being built the message is a path,
		// and a path is the only thing this function can answer for.
		if messageFile == "" {
			return nil, fmt.Errorf("commit needs somewhere to put the message")
		}
		if !hooks {
			argv = append(argv, "--no-verify")
		}
		// Attached, so a message file whose name somehow began with a dash
		// could not be read as the next option.
		argv = append(argv, "--file="+messageFile)

	case gitBranch:
		if err := checkGitBranch(a.Branch); err != nil {
			return nil, err
		}
		paths = []string{a.Branch}

	case gitSwitch:
		if err := checkGitBranch(a.Branch); err != nil {
			return nil, err
		}
		// --no-guess turns off the shorthand that creates a local branch
		// from a remote-tracking one of the same name. The tool offers two
		// things — an existing branch, or a new one off the current commit —
		// and a third that happens only when a name is spelled a certain way
		// is not one of them.
		argv = append(argv, "--no-guess")
		if a.Create {
			// The new branch's name rides attached to --create, because the
			// name after the delimiter is git's start-point in that form and
			// a switch that read the branch as a start-point would fail on
			// its own argument. Nothing is left after the delimiter, which
			// is where the current commit is the default start point.
			argv = append(argv, "--create="+a.Branch)
		} else {
			paths = []string{a.Branch}
		}
	}

	argv = append(argv, "--")
	return append(argv, paths...), nil
}

// executeGitWrite runs one write verb, after the checks that cannot be
// expressed in an argv: what may be staged, and whether there is anything to
// commit.
func (t *Toolset) executeGitWrite(raw json.RawMessage) (string, error) {
	var args gitWriteArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Verb == "" {
		return "", fmt.Errorf("verb is required: add, commit, branch, or switch")
	}
	paths, err := t.resolveGitPaths(args.Paths)
	if err != nil {
		return "", err
	}
	switch args.Verb {
	case gitAdd:
		if err := t.stageable(args.Paths, paths); err != nil {
			return "", err
		}
	case gitCommit:
		return t.commit(args)
	}
	argv, err := buildGitWriteArgv(args, paths, "", t.hooksRun())
	if err != nil {
		return "", err
	}
	if _, err := t.run(GitWriteToolName, argv); err != nil {
		return "", err
	}
	switch args.Verb {
	case gitAdd:
		return fmt.Sprintf("staged %s", plural(len(paths), "file")), nil
	case gitBranch:
		return "created branch " + args.Branch, nil
	}
	// Only switch is left: commit answered above, and the builder refused
	// every verb outside the four before anything spawned.
	return "switched to " + args.Branch, nil
}

// hooksRun reports whether the checkout's own programs may run on a commit.
// A session that never said anything about writes is not one that trusted
// the checkout.
func (t *Toolset) hooksRun() bool { return t.writes != nil && t.writes.Hooks }

// stageable refuses a path this session did not change, by name.
//
// This is the one thing the tool does that a shell cannot: uncommitted work
// that was in the tree when the session opened is not the agent's, and the
// rule that it is never staged becomes a fact about the arguments here rather
// than a sentence in the prompt.
// See docs/capabilities/coding-agent.md#the-agent-knows-where-and-when-it-is-standing.
func (t *Toolset) stageable(asked, resolved []string) error {
	if t.writes == nil || t.writes.Files == nil {
		return fmt.Errorf("this session keeps no record of what it changed, so nothing can be staged from it")
	}
	// Both spellings of every changed path go into the set. The record holds
	// the path the write was made through, which may run through a symlink;
	// the pathspec has already been resolved through one. Comparing only one
	// of the two would refuse a file the session really did change, on a
	// checkout whose parent happens to be a link.
	changed := map[string]bool{}
	for _, p := range t.writes.Files() {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(t.root, abs)
		}
		abs = filepath.Clean(abs)
		changed[abs] = true
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			changed[real] = true
		}
	}
	for i, abs := range resolved {
		if changed[abs] {
			continue
		}
		// Named as the caller wrote it: a refusal that answers with an
		// absolute path the model never typed reads as a different file.
		return fmt.Errorf("%s is not this session's work; commit it yourself", asked[i])
	}
	return nil
}

// commit is the verb with a before and an after: nothing may be committed
// out of an empty index, and what landed has to be named afterwards, because
// the sha is the only part of a commit nobody can reconstruct from the
// arguments.
func (t *Toolset) commit(args gitWriteArgs) (string, error) {
	if strings.TrimSpace(args.Message) == "" {
		return "", fmt.Errorf("commit needs a message")
	}
	staged, err := t.stagedFiles()
	if err != nil {
		return "", err
	}
	if len(staged) == 0 {
		// Named as the empty index rather than made into an empty commit:
		// --allow-empty has no field here, and a turn that thinks it
		// committed its work when it staged nothing is the failure this
		// refusal exists for.
		return "", fmt.Errorf("nothing is staged, so there is nothing to commit; stage the files first")
	}
	f, err := os.CreateTemp("", "shhh-commit-*.txt")
	if err != nil {
		return "", fmt.Errorf("cannot write the commit message: %w", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.WriteString(strings.TrimRight(args.Message, "\n") + "\n"); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("cannot write the commit message: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("cannot write the commit message: %w", err)
	}
	hooks := t.hooksRun()
	argv, err := buildGitWriteArgv(args, nil, f.Name(), hooks)
	if err != nil {
		return "", err
	}
	// git speaks for itself here. An identity nobody configured is the
	// commit's own refusal — "Please tell me who you are" — and passing
	// --author to get past it would be inventing one, which is why there is
	// no field for it.
	if _, err := t.run(GitWriteToolName, argv); err != nil {
		return "", err
	}
	return CommitReceipt(len(staged), t.head(), t.branch(), hooks), nil
}

// CommitReceipt is what a commit answers with, and the one wording for it:
// the transcript row, the turn's close and anything else that has to say a
// commit landed all say it the same way, because they are saying the same
// thing.
//
// The skipped hooks are a second line rather than a longer first one. The
// first line is the receipt itself and is what a row states in one field, and
// at eighty columns a field carrying both would be wider than the row: the
// fact still has to be stated, so it is stated under the receipt instead of
// pushed off the end of it.
func CommitReceipt(files int, head, branch string, hooks bool) string {
	r := fmt.Sprintf("committed %s as %s", plural(files, "file"), head)
	if branch != "" {
		r += " on " + branch
	}
	if !hooks {
		r += "\nhooks skipped · checkout not trusted — /trust to run them"
	}
	return r
}

// The three readings a commit needs around itself: what is staged, what sha
// landed, and where. Their argv is written out here rather than built,
// because none of it comes from the model — a fixed argv with no field in it
// is a stronger statement than a closed vocabulary, not a weaker one. All
// three are reads, and the write verbs stay the four the builder knows.

// stagedFiles is what the index holds, read through the reader's own flags.
// The name list rather than an exit code, because the count is the other
// half of the receipt.
func (t *Toolset) stagedFiles() ([]string, error) {
	out, err := t.run(GitWriteToolName, []string{"--no-pager", "--no-optional-locks", "diff", "--cached", "--name-only", "--no-ext-diff", "--no-textconv"})
	if err != nil {
		return nil, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// head is the short sha of the commit just made, and branch is where it
// landed. Both are asked for rather than parsed out of git's own commit
// output, whose shape is a user-facing sentence and not a contract; both are
// empty where git could not answer, so the receipt states what it knows.
func (t *Toolset) head() string {
	out, err := t.run(GitWriteToolName, []string{"--no-pager", "--no-optional-locks", "rev-parse", "--short", "HEAD"})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (t *Toolset) branch() string {
	out, err := t.run(GitWriteToolName, []string{"--no-pager", "--no-optional-locks", "branch", "--show-current"})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// plural is the package's own count-and-noun, for the sentences this file
// builds.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// CommitVerb is the write verb whose result is a receipt, named because a
// surface that draws the turn's close has to tell a commit from a staging.
const CommitVerb = gitCommit

// AddArgv and CommitArgv are the two writes a caller outside a session's
// toolset makes: the commit the unattended backlog runner ends a run with.
// They exist so that there is one place that spells a git write — the runner
// used to keep its own `git add --` and `git commit -F`, and a commit is the
// one act of a run that cannot be taken back, so two spellings were two
// places the rule about what a commit may carry could quietly disagree.
//
// The argv carries no repository: a caller that runs git somewhere other
// than its own working directory prepends its own -C, which is what the
// runner does.
func AddArgv(paths []string) ([]string, error) {
	return buildGitWriteArgv(gitWriteArgs{Verb: gitAdd}, paths, "", false)
}

// CommitArgv builds the commit, reading the message out of messageFile.
// hooks is the checkout's trust answer: an untrusted checkout commits with
// --no-verify, because a commit hook is a program the checkout can point git
// at and it runs as whoever cloned it.
func CommitArgv(messageFile string, hooks bool) ([]string, error) {
	return buildGitWriteArgv(gitWriteArgs{Verb: gitCommit}, nil, messageFile, hooks)
}

// WriteLine is the command line one call to this tool stands for, for the
// deny list to answer before anything can allow it. A person who wrote `git
// commit` on the list meant the act; a tool that let the act through under
// another name would be the way around the list.
func WriteLine(raw json.RawMessage) string {
	var a gitWriteArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "git"
	}
	if _, known := gitWriteFields[a.Verb]; !known {
		// An unreadable call still stands for git, so a list that refuses
		// git as a whole refuses it. The call itself is refused a moment
		// later for the verb it does not have.
		return "git"
	}
	return "git " + a.Verb
}

// Write is one call to the write tool as a card reads it: what it is about
// to do, said in the words the confirm prompt uses.
type Write struct {
	// Verb is the write verb, one of the four.
	Verb string
	// Title is the card's headline — the act, not the tool's name.
	Title string
	// Summary is the one-line description the transcript keeps.
	Summary string
	// Hooks says the checkout's own commit hooks will run on this commit.
	Hooks bool
}

// WritePlan reads one call for the card that asks about it. It is a method
// because the hooks answer is the session's, not the call's.
func (t *Toolset) WritePlan(raw json.RawMessage) (Write, error) {
	var a gitWriteArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return Write{}, fmt.Errorf("invalid arguments: %w", err)
	}
	if _, known := gitWriteFields[a.Verb]; !known {
		return Write{}, fmt.Errorf("unknown verb %q: use add, commit, branch, or switch", a.Verb)
	}
	w := Write{Verb: a.Verb, Hooks: t.hooksRun()}
	switch a.Verb {
	case gitAdd:
		w.Title = "stage " + plural(len(a.Paths), "file")
		w.Summary = strings.Join(a.Paths, ", ")
	case gitCommit:
		w.Title = "commit"
		w.Summary = firstMessageLine(a.Message)
	case gitBranch:
		w.Title = "branch " + a.Branch
		w.Summary = "create the branch " + a.Branch
	case gitSwitch:
		w.Title = "switch to " + a.Branch
		w.Summary = "switch to the branch " + a.Branch
		if a.Create {
			w.Summary = "create " + a.Branch + " off the current commit and switch to it"
		}
	}
	return w, nil
}

// firstMessageLine is a commit message's subject, which is all a one-line
// summary has room for.
func firstMessageLine(message string) string {
	if at := strings.IndexByte(message, '\n'); at >= 0 {
		return strings.TrimSpace(message[:at])
	}
	return strings.TrimSpace(message)
}
