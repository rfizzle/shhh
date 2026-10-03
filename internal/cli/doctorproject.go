package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/hook"
	"github.com/rfizzle/shhh/internal/hostgit"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/todo/run"
	"github.com/rfizzle/shhh/internal/ui/components"
)

func probeGit(ctx context.Context, _ config.Config) doctorFinding {
	dir, err := os.Getwd()
	if err != nil {
		return doctorGit(doctorGitState{Err: err})
	}
	return doctorGit(readGitState(ctx, dir))
}

// doctorGitState is what git was able to say about the working directory.
type doctorGitState struct {
	// Repo says the directory is inside a work tree at all.
	Repo bool
	// Root is the work tree's own directory, which is what the row names —
	// the working directory may be several levels inside it.
	Root string
	// Changed is how many paths `git status --porcelain` listed, and
	// Untracked how many of those git has never seen.
	Changed   int
	Untracked int
	// Dir is where the walk started, for the row to name when there is no
	// repository to name instead.
	Dir string
	// Err is a git that could not be run at all, which is a different answer
	// from a directory that is not a repository.
	Err error
}

// readGitState runs the three cheap commands that answer the question.
func readGitState(ctx context.Context, dir string) doctorGitState {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, doctorGitTimeout)
	defer cancel()

	state := doctorGitState{Dir: dir}
	git := func(args ...string) (string, error) {
		out, err := hostgit.Command(ctx, dir, args...).Output()
		return strings.TrimSpace(string(out)), err
	}
	if _, err := exec.LookPath("git"); err != nil {
		state.Err = err
		return state
	}
	inside, err := git("rev-parse", "--is-inside-work-tree")
	if err != nil || inside != "true" {
		return state
	}
	state.Repo = true
	if root, rootErr := git("rev-parse", "--show-toplevel"); rootErr == nil {
		state.Root = root
	}
	status, statusErr := git("status", "--porcelain")
	if statusErr != nil {
		state.Err = statusErr
		return state
	}
	for _, line := range strings.Split(status, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		state.Changed++
		if strings.HasPrefix(line, "??") {
			state.Untracked++
		}
	}
	return state
}

// doctorGit reads the workspace. What hangs on it is undo: `[u] undo turn`
// restores from what the changeset recorded, and outside a repository there
// is nothing to compare an edit against — so a directory that is not a work
// tree is a warning rather than a pass, and it says which key is affected.
func doctorGit(state doctorGitState) doctorFinding {
	switch {
	case state.Err != nil && !state.Repo:
		return doctorFinding{
			Subject: "git did not answer", Detail: state.Err.Error(),
			Outcome: "unknown", State: components.DoctorWarned,
			Consequence: "shhh cannot tell a tracked file from an untracked one, so every edit reads as unrecoverable",
			FixLabel:    "show what is missing",
			Fix:         []string{"install git and re-run: shhh doctor"},
		}
	case !state.Repo:
		return doctorFinding{
			Subject: shortPath(state.Dir), Detail: "not a git work tree",
			Outcome: "no repo", State: components.DoctorWarned,
			Consequence: "every edit here will say it cannot be undone, because there is nothing to restore from",
			FixLabel:    "show the one command",
			Fix:         []string{"git init                 or start the agent inside a repository"},
		}
	}
	root := state.Root
	if root == "" {
		root = state.Dir
	}
	f := doctorFinding{Subject: shortPath(root), Outcome: "ok"}
	switch {
	case state.Changed == 0:
		f.Detail = "clean"
	case state.Untracked == 0:
		f.Detail = countOf(state.Changed, "file changed", "files changed") + ", all tracked"
	default:
		f.Detail = fmt.Sprintf("%s, %d untracked",
			countOf(state.Changed, "file changed", "files changed"), state.Untracked)
	}
	return f
}

// probeHooks is the doctor's reading of the person's own commands at the
// session's seams. It reads the same two files a session reads and through
// the same loader, so a hook the doctor lists is a hook a session fires.
func probeHooks(_ context.Context, cfg config.Config) doctorFinding {
	if cfg.Hooks.Disabled {
		return doctorFinding{
			Subject: "hooks are off", Detail: "hooks.disabled",
			Outcome: "off", State: components.DoctorSkipped,
		}
	}
	return doctorHooks(hookSet(cfg), cfg.HookCeiling())
}

// doctorHooks is that reading. Hooks are the person's own commands, so
// nothing here is a fault: a checkout with none is not missing anything, and
// an entry that would not load is a warning naming it rather than a failure,
// because the session started and started without it.
func doctorHooks(set *hook.Set, ceiling time.Duration) doctorFinding {
	notes := set.Notes()
	if set.Len() == 0 && len(notes) == 0 {
		return doctorFinding{
			Subject: "no hooks", Detail: strings.Join(hook.Events(), " · "),
			Outcome: "empty", State: components.DoctorSkipped,
		}
	}
	f := doctorFinding{
		Subject: countOf(set.Len(), "hook", "hooks"),
		Detail:  strings.Join(set.Events(), " · "),
		Outcome: "ok",
	}
	if ceiling > 0 {
		f.Detail += " · " + ceiling.String() + " each"
	}
	if len(notes) > 0 {
		f.Outcome = "unreadable"
		f.State = components.DoctorSkipped
		f.Consequence = countOf(len(notes), "entry", "entries") + " did not load and will not fire"
		f.Fix = notes
		f.FixLabel = fmt.Sprintf("show the %s", countOf(len(f.Fix), "line", "lines"))
	}
	return f
}

// probePrompts reads every wording a file replaced, from the settings and
// from the checkout's own prompts directory.
//
// It is a check of its own because an unreadable wording is the one config
// failure that stops a session from starting at all, and a reader who has
// just written the path is exactly who is looking here.
func probePrompts(_ context.Context, cfg config.Config) doctorFinding {
	return doctorPrompts(readWordings(cfg.Prompts, projectPrompts()), backlogProfileIs())
}

// doctorPrompts is that reading. Replacing nothing is the ordinary case and
// not a fault, so a machine running the built-in prose reads as empty rather
// than as missing something.
//
// Every wording gets a line naming the file it came from, whether or not it
// read: three directories can hold a `steer.md` and only one of them is in
// force, so "which file am I actually running" is the question this row is
// opened with — and a wording that has gone missing is found here rather
// than at the next session that refuses to start.
func doctorPrompts(rows []wordingRow, profile backlogProfile) doctorFinding {
	under := profileLines(profile)
	if len(rows) == 0 {
		return doctorFinding{
			Subject: "no wordings replaced", Detail: "the built-in prose",
			Outcome: "empty", State: components.DoctorSkipped,
			Fix: under, FixLabel: "show the profile this backlog runs under",
		}
	}
	names := make([]string, 0, len(rows))
	lines := append([]string{}, under...)
	unreadable := 0
	for _, r := range rows {
		names = append(names, r.key)
		if r.err != nil {
			unreadable++
			lines = append(lines, r.err.Error())
			continue
		}
		lines = append(lines, r.key+" — "+r.from)
	}
	f := doctorFinding{
		Subject:  countOf(len(rows), "wording", "wordings"),
		Detail:   strings.Join(names, " · "),
		Outcome:  "ok",
		Fix:      lines,
		FixLabel: "show which file each came from",
	}
	if unreadable > 0 {
		f.Outcome = "unreadable"
		f.State = components.DoctorFailed
		f.Consequence = countOf(unreadable, "wording", "wordings") + " cannot be read, and no session starts until that is settled"
		f.FixLabel = fmt.Sprintf("show the %s", countOf(len(f.Fix), "line", "lines"))
	}
	return f
}

// profileLines say which profile the wordings belong to: a wording key is a
// step of a run, so "which file am I running" is only half the question — the
// other half is which run has a step by that name at all. A profile that will
// not load is said here rather than as a list of keys nothing could read,
// because there is no step to name a wording for until it does.
func profileLines(p backlogProfile) []string {
	if p.err != nil {
		return []string{p.err.Error()}
	}
	lines := []string{"profile " + p.name() + " — " + p.from}
	steps := p.pipeline.Strip()
	if len(steps) == 0 {
		return append(lines, "no run: this profile's items are worked by hand")
	}
	names := make([]string, 0, len(steps))
	for _, s := range steps {
		names = append(names, string(s))
	}
	wordings := "wordings: " + strings.Join(p.pipeline.WordingKeys(), ", ")
	if p.dir != "" {
		// A profile read from a directory has its wordings in files the
		// reader can open, and the row's next use is opening one. The
		// directory is named the way the profile above it is, so the two
		// lines read as one place.
		wordings += " · under " + p.from + run.ProfileWordings + "/"
	}
	return append(lines, "steps: "+strings.Join(names, " · "), wordings)
}

func probeTools(context.Context, config.Config) doctorFinding {
	var found, missing []string
	for _, tool := range structural.ToolBinaries() {
		if _, err := exec.LookPath(tool); err != nil {
			missing = append(missing, tool)
			continue
		}
		found = append(found, tool)
	}
	servers := make([]string, 0, 4)
	for _, spec := range lsp.DetectServers() {
		servers = append(servers, spec.Name)
	}
	return doctorTools(found, missing, servers)
}

// doctorTools reads what the agent will find on PATH. None of it is required
// — every structural tool has a built-in fallback and the LSP integration is
// a clean no-op without a server — so this check never fails. It states what
// is there and what is not, because "why did it not use ast-grep" is a
// question with an answer.
func doctorTools(found, missing, servers []string) doctorFinding {
	f := doctorFinding{Outcome: "ok"}
	switch {
	case len(found) == 0:
		f.Subject = "no structural tools"
		f.Outcome = "built-ins only"
		f.State = components.DoctorSkipped
	default:
		f.Subject = strings.Join(found, ", ")
	}
	switch {
	case len(servers) == 0 && len(missing) == 0:
		f.Detail = "no language server on PATH"
	case len(servers) == 0:
		f.Detail = "no " + strings.Join(missing, "/") + ", no language server"
	case len(missing) == 0:
		f.Detail = strings.Join(servers, ", ")
	default:
		f.Detail = "no " + strings.Join(missing, "/") + " · " + strings.Join(servers, ", ")
	}
	return f
}

func probeProject(context.Context, config.Config) doctorFinding {
	dir, err := os.Getwd()
	if err != nil {
		dir = ""
	}
	return doctorProject(project.Instructions(dir, userInstructionsPath()))
}

// doctorProject lists the instruction files a session started here would put
// in its system prompt, in the order it states them. A checkout that has
// told the model nothing is the ordinary state of a new one rather than a
// fault, so it is `⊘` with the names it would have read, which is also the
// only place those names are written down for someone who has not read the
// manual.
func doctorProject(files []project.Instruction) doctorFinding {
	if len(files) == 0 {
		return doctorFinding{
			Subject: "nothing read", Detail: "no " + project.InstructionNames(),
			Outcome: "empty", State: components.DoctorSkipped,
		}
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, shortPath(f.Path))
	}
	return doctorFinding{
		Subject: countOf(len(files), "instruction file", "instruction files"),
		Detail:  strings.Join(paths, " · "), Outcome: "ok",
	}
}

func probeMemory(context.Context, config.Config) doctorFinding {
	dir, err := os.Getwd()
	if err != nil {
		return doctorMemory("", 0, 0, err)
	}
	db, err := openStore()
	if err != nil {
		// The store's own row already says this, and saying it twice would be
		// the report blaming one fault on two checks.
		return doctorMemory(memory.ProjectScope(dir), 0, 0, nil)
	}
	defer db.Close()
	entries, listErr := memory.NewStore(db, memory.ProjectScope(dir)).List()
	scoped := 0
	for _, e := range entries {
		if e.Scope != memory.GlobalScope {
			scoped++
		}
	}
	return doctorMemory(memory.ProjectScope(dir), scoped, len(entries)-scoped, listErr)
}

// doctorMemory reads durable memory for this project. An empty store
// is the ordinary state of a new project rather than a fault, so it is `⊘`
// with the words for it, not a warning.
func doctorMemory(scope string, forProject, global int, err error) doctorFinding {
	if err != nil {
		return doctorFinding{
			Subject: "memory did not load", Detail: err.Error(),
			Outcome: "unreadable", State: components.DoctorWarned,
			Consequence: "sessions in this project will start with nothing remembered",
		}
	}
	if forProject+global == 0 {
		return doctorFinding{
			Subject: "nothing remembered yet", Detail: shortPath(scope),
			Outcome: "empty", State: components.DoctorSkipped,
		}
	}
	detail := countOf(forProject, "entry", "entries") + " for this project"
	if global > 0 {
		detail += " · " + strconv.Itoa(global) + " global"
	}
	return doctorFinding{Subject: shortPath(scope), Detail: detail, Outcome: "ok"}
}
