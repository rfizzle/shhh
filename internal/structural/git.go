package structural

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

// The verbs. This is the whole vocabulary: a git subcommand that is not one
// of these five cannot be reached through this tool, because there is no
// field a caller can put one in. That is what lets the tool auto-run like any
// other read while every mutating git stays a command that asks.
// See docs/capabilities/approvals-and-safety.md#a-closed-verb-set-is-what-makes-a-read-a-read.
const (
	gitStatus = "status"
	gitLog    = "log"
	gitShow   = "show"
	gitDiff   = "diff"
	gitBlame  = "blame"
)

// Per-verb output bounds, and the one rule behind which verbs have one.
//
// A verb is bounded here only when it has a narrower question to offer:
// status takes paths, log takes a limit, blame takes a line window. For those
// three the output is one record per line, a head-and-tail cut through it is
// not an answer, and the notice can say exactly which argument to change.
//
// show and diff are deliberately unbounded here, because they have no such
// argument — the content is however large the commit is. Bounding them would
// drop the tail somewhere no one can reach it, so they are left to the byte
// cap on the spawn and to the reduction pipeline, which keeps a head, a tail
// and the flagged lines, stores the whole original as evidence, and hands
// back the id to retrieve it.
const (
	// MaxGitLogCommits caps the log verb's limit, and with one line per
	// commit it is also the verb's line bound.
	MaxGitLogCommits = 100

	// defaultGitLogCommits is what log returns when no limit is given: enough
	// to see the shape of recent work without spending a screen on it.
	defaultGitLogCommits = 20

	// MaxGitStatusLines caps the status verb — one line per changed path.
	MaxGitStatusLines = 300

	// MaxGitBlameLines caps the blame verb; past it the answer is a window,
	// which is what start_line/end_line are for.
	MaxGitBlameLines = 400
)

var gitTool = provider.Tool{
	Name: GitToolName,
	Description: "Read this repository's history: status, log, show, diff, blame. " +
		"Ask history questions here rather than running git through execute_command — this tool is read-only, so it answers without an approval. " +
		"log takes search for git's pickaxe: the commits that added or removed a given string. blame says who last touched each line and when. " +
		"Only these five verbs exist; anything that changes the repository (commit, checkout, reset, push, clean) is not reachable here and stays with execute_command.",
	Parameters: json.RawMessage(`{
		"type": "object",
		"properties": {
			"verb": {"type": "string", "enum": ["status", "log", "show", "diff", "blame"], "description": "Which history question to ask"},
			"ref": {"type": "string", "description": "A branch, tag or commit: log starts there, show displays it, diff compares against it, blame reads the file as of it"},
			"to_ref": {"type": "string", "description": "diff only: the second side of the comparison"},
			"paths": {"type": "array", "items": {"type": "string"}, "description": "Limit to these paths, relative to the workspace root; blame needs exactly one"},
			"limit": {"type": "integer", "description": "log only: how many commits (default 20, max 100)"},
			"search": {"type": "string", "description": "log only: only commits that changed the number of occurrences of this string"},
			"stat": {"type": "boolean", "description": "show/diff only: a per-file summary instead of the patch"},
			"staged": {"type": "boolean", "description": "diff only: compare the index rather than the working tree; not together with to_ref"},
			"start_line": {"type": "integer", "description": "blame only: first line to attribute"},
			"end_line": {"type": "integer", "description": "blame only: last line to attribute"}
		},
		"required": ["verb"]
	}`),
}

type gitArgs struct {
	Verb      string   `json:"verb"`
	Ref       string   `json:"ref"`
	ToRef     string   `json:"to_ref"`
	Paths     []string `json:"paths"`
	Limit     int      `json:"limit"`
	Search    string   `json:"search"`
	Stat      bool     `json:"stat"`
	Staged    bool     `json:"staged"`
	StartLine int      `json:"start_line"`
	EndLine   int      `json:"end_line"`
}

// gitFields is each verb's vocabulary. A field the verb does not name is
// refused rather than dropped: a filter that was silently ignored comes back
// as an answer the reader believes was filtered, which is worse than an error.
var gitFields = map[string][]string{
	gitStatus: {"paths"},
	gitLog:    {"ref", "paths", "limit", "search"},
	gitShow:   {"ref", "paths", "stat"},
	gitDiff:   {"ref", "to_ref", "paths", "stat", "staged"},
	gitBlame:  {"ref", "paths", "start_line", "end_line"},
}

// setFields names the optional fields this call actually filled in.
func (a gitArgs) setFields() []string {
	var f []string
	if a.Ref != "" {
		f = append(f, "ref")
	}
	if a.ToRef != "" {
		f = append(f, "to_ref")
	}
	if len(a.Paths) > 0 {
		f = append(f, "paths")
	}
	if a.Limit != 0 {
		f = append(f, "limit")
	}
	if a.Search != "" {
		f = append(f, "search")
	}
	if a.Stat {
		f = append(f, "stat")
	}
	if a.Staged {
		f = append(f, "staged")
	}
	if a.StartLine != 0 {
		f = append(f, "start_line")
	}
	if a.EndLine != 0 {
		f = append(f, "end_line")
	}
	return f
}

// gitRefRe is the charset a ref may use. It is deliberately narrower than
// what git will accept, and two exclusions carry the weight.
//
// A ref may not begin with "-", because a flag-shaped ref is the failure this
// restriction exists for: git forwards a revision into machinery that parses
// it again, so a value that survives one round of argument parsing as data
// can arrive at the second as an option. The delimiter that protects paths
// cannot protect a ref, since a revision has to sit before it.
//
// A ref may not contain ":", which costs the `ref:path` form. That form
// reads a blob by a path git resolves inside the repository, and the
// repository is not the workspace: where a session is opened in a
// subdirectory, `HEAD:../elsewhere` would read a file the containment check
// exists to keep out. The commit form of show answers the same question.
var gitRefRe = regexp.MustCompile(`^[A-Za-z0-9@_][A-Za-z0-9@_./^~{}-]*$`)

// maxGitRefLen bounds a ref; git's own limit is far higher, and nothing this
// tool is for needs more.
const maxGitRefLen = 200

func checkGitRef(field, ref string) error {
	if ref == "" {
		return nil
	}
	if len(ref) > maxGitRefLen || !gitRefRe.MatchString(ref) {
		return fmt.Errorf("%s %q is not a plain branch, tag or commit", field, ref)
	}
	return nil
}

// resolveGitPaths resolves pathspecs against the workspace root and confirms
// they stay inside it. It differs from resolvePath in one way, and the
// difference is what the tool is for: history names paths that are no longer
// on disk — a file deleted three commits ago is exactly what log and show get
// asked about — so containment is decided lexically, and the symlink check
// runs only when there is something there to resolve.
func (t *Toolset) resolveGitPaths(paths []string) ([]string, error) {
	resolved := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			return nil, fmt.Errorf("paths entries must not be empty")
		}
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(t.root, abs)
		}
		abs = filepath.Clean(abs)
		if err := t.containedInRoot(abs, p); err != nil {
			return nil, err
		}
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			if err := t.containedInRoot(real, p); err != nil {
				return nil, err
			}
			abs = real
		}
		resolved = append(resolved, abs)
	}
	return resolved, nil
}

// containedInRoot reports an already-cleaned absolute path that leaves the
// workspace, naming the path the caller asked for rather than the one it
// resolved to.
func (t *Toolset) containedInRoot(abs, asked string) error {
	if abs != t.root && !strings.HasPrefix(abs, t.root+string(os.PathSeparator)) {
		return fmt.Errorf("path %q is outside the workspace", asked)
	}
	return nil
}

// buildGitArgv constructs git's argv for one verb. The invariants:
//
//   - The verb comes from the closed set above, so no argv this function can
//     produce runs a git subcommand that writes. That is what makes the
//     read-only classification a fact about this builder rather than a
//     promise about intent.
//   - Every path follows a literal "--", so a dash-prefixed path is a path;
//     the delimiter is written even when there are none, so a ref that has
//     gone missing cannot let a later token be read as one.
//   - --no-pager on every spawn, so no core.pager program is ever launched,
//     and colour is forced off per verb — status has no --no-color and takes
//     the machine format instead, blame has two colour knobs and no unambiguous
//     --no-color at all.
//   - --no-optional-locks on every spawn. Without it `status` refreshes and
//     rewrites .git/index, which is a tool documented as never writing a file
//     writing one, and it takes index.lock while doing so — contention against
//     the editor and the other sessions sharing the checkout.
//   - --no-ext-diff and --no-textconv wherever a diff is rendered: both
//     configuration keys name a program to run, and a read-only tool that
//     runs a configured program is not one.
//   - The flags that write or execute a file are absent from the vocabulary
//     entirely: --output, -c/--config-env, --exec-path, --upload-pack and
//     --receive-pack have no field here to arrive in.
func buildGitArgv(a gitArgs, paths []string) ([]string, error) {
	allowed, known := gitFields[a.Verb]
	if !known {
		return nil, fmt.Errorf("unknown verb %q: use status, log, show, diff, or blame", a.Verb)
	}
	for _, f := range a.setFields() {
		if !slices.Contains(allowed, f) {
			return nil, fmt.Errorf("%s does not take %s; it takes %s", a.Verb, f, strings.Join(allowed, ", "))
		}
	}
	if err := checkGitRef("ref", a.Ref); err != nil {
		return nil, err
	}
	if err := checkGitRef("to_ref", a.ToRef); err != nil {
		return nil, err
	}

	argv := []string{"--no-pager", "--no-optional-locks", a.Verb}
	switch a.Verb {
	case gitStatus:
		// The porcelain format is stable across versions and across the
		// user's configuration, and it is colour-free by construction: git
		// status is the one verb with no --no-color to force.
		argv = append(argv, "--porcelain=v1", "--branch")

	case gitLog:
		limit := a.Limit
		if limit <= 0 {
			limit = defaultGitLogCommits
		}
		if limit > MaxGitLogCommits {
			limit = MaxGitLogCommits
		}
		argv = append(argv,
			"--no-color", "--no-ext-diff", "--no-textconv", "--no-show-signature",
			"--date=short", "--pretty=format:%h %ad %an %s",
			"--max-count="+strconv.Itoa(limit))
		if a.Search != "" {
			// The pickaxe value rides attached to -S: separated, a
			// dash-prefixed search string is read as the next option.
			argv = append(argv, "-S"+a.Search)
		}
		if a.Ref != "" {
			argv = append(argv, a.Ref)
		}

	case gitShow:
		if a.Ref == "" {
			return nil, fmt.Errorf("show needs a ref: the commit, tag or branch to display")
		}
		argv = append(argv, "--no-color", "--no-ext-diff", "--no-textconv",
			"--no-show-signature", "--unified=3", "--date=short")
		if a.Stat {
			argv = append(argv, "--stat")
		}
		argv = append(argv, a.Ref)

	case gitDiff:
		if a.ToRef != "" && a.Ref == "" {
			return nil, fmt.Errorf("to_ref needs a ref to compare against")
		}
		// git takes at most one commit beside --staged, and refuses the pair
		// with a usage dump rather than an answer.
		if a.ToRef != "" && a.Staged {
			return nil, fmt.Errorf("staged compares the index against one ref; drop to_ref or drop staged")
		}
		argv = append(argv, "--no-color", "--no-ext-diff", "--no-textconv", "--unified=3")
		if a.Staged {
			argv = append(argv, "--staged")
		}
		if a.Stat {
			argv = append(argv, "--stat")
		}
		if a.Ref != "" {
			argv = append(argv, a.Ref)
		}
		if a.ToRef != "" {
			argv = append(argv, a.ToRef)
		}

	case gitBlame:
		if len(paths) != 1 {
			return nil, fmt.Errorf("blame takes exactly one path")
		}
		// blame names its two colour settings individually; --no-color is
		// ambiguous between them and git refuses it.
		argv = append(argv, "--no-color-lines", "--no-color-by-age", "--no-textconv", "--date=short")
		window, err := blameWindow(a)
		if err != nil {
			return nil, err
		}
		if window != "" {
			argv = append(argv, window)
		}
		if a.Ref != "" {
			argv = append(argv, a.Ref)
		}
	}

	argv = append(argv, "--")
	return append(argv, paths...), nil
}

// blameWindow renders blame's line range as one attached -L token, or "" when
// the whole file was asked for. Both ends are integers, so nothing here can
// be read as a flag.
func blameWindow(a gitArgs) (string, error) {
	if a.StartLine == 0 && a.EndLine == 0 {
		return "", nil
	}
	start, end := a.StartLine, a.EndLine
	if start <= 0 {
		start = 1
	}
	if end == 0 {
		end = start + MaxGitBlameLines - 1
	}
	if end < start {
		return "", fmt.Errorf("end_line %d is before start_line %d", end, start)
	}
	return "-L" + strconv.Itoa(start) + "," + strconv.Itoa(end), nil
}

// spawnEnv is the environment one tool's spawn runs with; nil inherits the
// session's unchanged, which is what every tool but git wants.
//
// git needs one because two of its configuration keys name a program git then
// runs, and neither is reachable by a flag. --no-pager and --no-ext-diff shut
// the two that are; core.fsmonitor is the one that is left, and git execs it
// on status, diff and blame — so a repository someone else wrote could turn a
// read that runs in every mode, with no approval, into arbitrary execution.
// Blanking it takes a config override, and an override on the command line
// would mean putting -c into the vocabulary this tool exists to keep closed.
// The environment form does the same job and stays out of the argv.
//
// Whatever GIT_CONFIG_* the session inherited is dropped first: those
// variables are numbered, so appending ours to an existing set would either
// renumber theirs or be renumbered by it, and either way the override this
// function exists for is the one that goes missing.
func spawnEnv(name string) []string {
	if name != GitToolName {
		return nil
	}
	env := os.Environ()
	kept := make([]string, 0, len(env)+3)
	for _, v := range env {
		if strings.HasPrefix(v, "GIT_CONFIG_COUNT=") ||
			strings.HasPrefix(v, "GIT_CONFIG_KEY_") ||
			strings.HasPrefix(v, "GIT_CONFIG_VALUE_") {
			continue
		}
		kept = append(kept, v)
	}
	return append(kept,
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.fsmonitor",
		"GIT_CONFIG_VALUE_0=")
}

func (t *Toolset) executeGit(raw json.RawMessage) (string, error) {
	var args gitArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Verb == "" {
		return "", fmt.Errorf("verb is required: status, log, show, diff, or blame")
	}
	paths, err := t.resolveGitPaths(args.Paths)
	if err != nil {
		return "", err
	}
	argv, err := buildGitArgv(args, paths)
	if err != nil {
		return "", err
	}
	out, err := t.run(GitToolName, argv)
	if err != nil {
		return "", err
	}
	return shapeGitOutput(args.Verb, out), nil
}

// shapeGitOutput bounds one verb's output where a bound has a narrower
// question to point at, and turns the several ways git says "nothing" into one
// sentence per verb — an empty result otherwise reads as a failure.
func shapeGitOutput(verb, out string) string {
	out = strings.TrimRight(out, "\n")
	if out == "" {
		switch verb {
		case gitLog:
			return "No commits matched."
		case gitDiff:
			return "No changes."
		}
		return "(no output)"
	}
	max, hint := gitBounds(verb)
	if max == 0 {
		return out
	}
	lines := strings.Split(out, "\n")
	if len(lines) <= max {
		return out
	}
	return strings.Join(lines[:max], "\n") +
		fmt.Sprintf("\n… (truncated at %d lines; %s)", max, hint)
}

// gitBounds is the line bound for a verb and the sentence that says how to get
// under it. A zero bound means the verb has no narrower question to offer, so
// the reduction pipeline handles its size instead.
func gitBounds(verb string) (int, string) {
	switch verb {
	case gitStatus:
		return MaxGitStatusLines, "name paths to narrow it"
	case gitLog:
		return MaxGitLogCommits, "lower limit or name paths to see fewer commits"
	case gitBlame:
		return MaxGitBlameLines, "use start_line and end_line to attribute a window"
	}
	return 0, ""
}
