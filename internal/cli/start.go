package cli

// First-contact facts. Everything the empty session's start screen
// states is gathered here, once, while the session is being built: the
// project survey, the quality-gate configuration in effect, and the most
// recent saved session. Nothing on that screen is computed again while it is
// on the terminal.
//
// One thing gathered here is not only the screen's. Whether another session
// already has this checkout open is stated on the screen, in the system
// prompt and in the tree notice, so the question is pointed at the store
// once and handed to all three. The screen and the prompt then share a
// single answer, read while the survey they are both written from is taken;
// the tree notice asks again each time it has something to attribute,
// because it speaks turn after turn and the answer changes underneath it.

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/hostgit"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// sessionSibling is the session's way of asking whether another one already
// has this checkout open. It is a question rather than an answer, because
// the answer keeps changing: the second session is usually opened after the
// first, and both the prompt and the tree notice are written after that.
//
// The zero value answers no, which is what a session with no store to ask
// can honestly say, and what stops nothing from starting.
type sessionSibling struct {
	read func() (time.Time, bool)
}

// readSibling points the question at the session's own store.
func readSibling(db *storage.DB) sessionSibling {
	if db == nil {
		return sessionSibling{}
	}
	return sessionSibling{read: func() (time.Time, bool) { return liveSibling(db) }}
}

// since is when the other session started, and the zero time when there is
// none. It is asked again every time the system prompt is built, launch and
// session boundary alike: a new conversation opens on the checkout as it
// stands now, and somebody may have arrived while the last one was running.
func (s sessionSibling) since() time.Time {
	if s.read == nil {
		return time.Time{}
	}
	at, _ := s.read()
	return at
}

// live is the same question where only the yes or no is wanted.
func (s sessionSibling) live() bool {
	if s.read == nil {
		return false
	}
	_, ok := s.read()
	return ok
}

// withSibling hands the tree reading the question, so the block that says
// the tree moved can name the likeliest author. Neither a reading that is
// off nor a session with nothing to ask gains a clause.
func withSibling(c *agent.TreeCheck, sib sessionSibling) *agent.TreeCheck {
	if c == nil || sib.read == nil {
		return c
	}
	c.Sibling = sib.live
	return c
}

// buildStartInfo assembles the start screen from the survey the session
// already took. Every source is optional: a missing gate config or an
// unavailable database costs its own clause and nothing else.
//
// The survey is handed in rather than taken here because the model's prompt
// block is built from the same answer, and the tree walk behind it is not
// worth doing twice.
func buildStartInfo(survey project.Info, db *storage.DB, gateEnabled bool, trust chat.Trust, proj config.Project, wordings []string) chat.StartInfo {
	wd := survey.Dir
	// The checkout's own settings file rides on the survey, the way the
	// sibling reading does: the screen states what this session is running
	// on, and a session running on settings the reader never wrote is one
	// they cannot account for from their own file alone.
	survey.ConfigFile = proj.Display
	info := chat.StartInfo{Project: survey, Trust: trust, Wordings: wordings}
	// The profile is named only where it is not the one shhh ships: a line
	// saying `code` on every session in every Go checkout is a line nobody
	// reads by the third one (todoprofile.go).
	if p := backlogProfileIs(); p.name() != defaultProfileName {
		info.Profile = chat.StartProfile{Name: p.name(), From: p.from}
	}
	// And where the backlog is, where it is not this directory's project:
	// the root `todo.root` named, or the global list a session outside every
	// project falls back to (todoroot.go).
	if root := todo.Root(wd); root != survey.Root {
		info.Backlog = project.Abbreviate(root)
	}
	if gateEnabled {
		info.Gate = startGate(wd)
	}
	info.Ready = startReady(todo.Root(wd))
	if survey.Repo && survey.Branch != "" {
		info.Branch = startBranch(wd, survey.Branch)
	}
	if db != nil {
		if recent, ok, err := db.MostRecentChat(); err == nil && ok {
			info.Recent = chat.StartRecent{
				Present: true,
				Name:    recent.Name,
				Title:   recent.Title,
				Turns:   recent.Turns,
				Updated: recent.UpdatedAt,
				Cost:    recent.Cost,
				Priced:  recent.Priced,
				// The store steps past a slot another running session is
				// autosaving into, so the offer can be a conversation older
				// than the last one. The screen says so on the row rather
				// than swapping the answer silently.
				Held: recent.Held != "",
			}
		}
	}
	return info
}

// startReady is the backlog's next item that can be started, in the words of
// the profile the backlog is written in. A root with no backlog, or none
// ready, is the zero value and the slot is as it was.
func startReady(root string) chat.StartReady {
	p := todoProfile()
	ready := todo.Load(p, root).Ready()
	if len(ready) == 0 {
		return chat.StartReady{}
	}
	it := ready[0]
	return chat.StartReady{Present: true, Slug: it.Slug, Title: it.Title, Noun: p.Noun}
}

// startBranch reads how far the checkout's branch is ahead of its default
// branch, and whether those commits are on its upstream. Any failure — no
// default branch to compare with, the branch being the default — is the zero
// value: an offer the code cannot verify is not made.
func startBranch(dir, branch string) chat.StartBranch {
	base := defaultBranch(dir)
	if base == "" || base == branch {
		return chat.StartBranch{}
	}
	ahead := countCommits(dir, base+"..HEAD")
	if ahead <= 0 {
		return chat.StartBranch{}
	}
	// A branch with no upstream has nothing pushed; one with an upstream is
	// pushed when it holds nothing the upstream lacks.
	unpushed := countCommits(dir, "@{upstream}..HEAD")
	return chat.StartBranch{Ahead: ahead, Pushed: unpushed == 0}
}

// defaultBranch is the branch the checkout's work goes up against: the
// remote's own HEAD where there is one, else main or master, whichever
// exists. It is a ref to count against: the local branch, or origin/<name>
// where the remote's default has no local copy.
func defaultBranch(dir string) string {
	ctx := context.Background()
	if out, err := hostgit.Output(ctx, dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if name := strings.TrimPrefix(strings.TrimSpace(out), "origin/"); name != "" {
			for _, ref := range []string{"refs/heads/" + name, "refs/remotes/origin/" + name} {
				if _, err := hostgit.Output(ctx, dir, "rev-parse", "--verify", "--quiet", ref); err == nil {
					return strings.TrimPrefix(ref, "refs/heads/")
				}
			}
		}
	}
	for _, name := range []string{"main", "master"} {
		if _, err := hostgit.Output(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+name); err == nil {
			return name
		}
	}
	return ""
}

// countCommits is how many commits a range holds, and -1 where git has no
// answer for it.
func countCommits(dir, rng string) int {
	out, err := hostgit.Output(context.Background(), dir, "rev-list", "--count", rng)
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return -1
	}
	return n
}

// scaffoldOffer names the offer in the store. It is a value in a table, so
// it is a constant rather than a phrase: the wording of the row on screen is
// free to change without forgetting who already said no.
const scaffoldOffer = "scaffold"

// buildScaffold wires the start screen's scaffolding offer for a session in
// wd. The offer is answered here, once: a checkout that can take it, and no
// refusal already on record for it
// (docs/interface/surfaces.md#the-start-screen). Without a store the refusal
// has nowhere to live, so nothing is offered — an offer that cannot be
// refused for good is a nag.
func buildScaffold(db *storage.DB, wd string) chat.Scaffold {
	if wd == "" {
		return chat.Scaffold{}
	}
	// The repository root, not the working directory: the offer is the
	// project's and so is the answer, and a session started two directories
	// down that scaffolded where it stood would put a second context file
	// nearer than the project's own — which the nearest-first read would
	// then prefer. `shhh init --project` still writes where it is told,
	// because a flag that says "the current directory" has to mean it.
	root := project.Root(wd)
	s := chat.Scaffold{
		Paths: project.ScaffoldPaths(root, wd),
		Write: func() (string, error) { return project.Scaffold(root) },
	}
	if db == nil {
		return s
	}
	s.Decline = func() error { return db.DeclineOffer(root, scaffoldOffer) }
	s.Offer = project.NeedsScaffold(root) && !db.OfferDeclined(root, scaffoldOffer)
	return s
}

// startGate reads the workspace's quality config for the screen's gate line.
// A config that exists but does not load is reported as unreadable rather
// than as absent — a broken gate is the more urgent of the two.
func startGate(workspace string) chat.StartGate {
	g := chat.StartGate{Path: quality.ConfigRelPath}
	cfg, err := quality.LoadConfig(workspace)
	if err != nil {
		if !os.IsNotExist(err) {
			g.Err = err.Error()
		}
		return g
	}
	names := cfg.SuiteNames()
	g.Suites = len(names)
	suite := quality.DefaultSuite
	if _, ok := cfg.Suites[suite]; !ok && len(names) > 0 {
		suite = names[0]
	}
	s, ok := cfg.Suites[suite]
	if !ok {
		return g
	}
	g.Suite = suite
	for _, c := range s.Checks {
		g.Checks = append(g.Checks, c.Name)
	}
	return g
}

// startOffersEvidence is what the start screen's reading reads beyond what the
// screen already holds: the names of the files the tree has changed, the
// last ten commit subjects and the backlog's ready items, each as "id:
// title". It is a function rather than a value because it runs git and reads
// the backlog, and that is paid on the reading's own goroutine after the
// screen has drawn, never before
// (docs/capabilities/chat.md#the-start-screen-is-read-for-this-checkout).
// Names and subjects only: no file's contents reach the reading from here.
func startOffersEvidence(survey project.Info) func(context.Context) agent.StartOffersRequest {
	dir := survey.Dir
	return func(ctx context.Context) agent.StartOffersRequest {
		var req agent.StartOffersRequest
		if survey.Repo {
			if out, err := hostgit.Output(ctx, dir, "status", "--porcelain"); err == nil {
				for _, line := range strings.Split(out, "\n") {
					if len(line) > 3 {
						req.Dirty = append(req.Dirty, strings.TrimSpace(line[3:]))
					}
				}
			}
			if out, err := hostgit.Output(ctx, dir, "log", "-10", "--format=%s"); err == nil {
				for _, line := range strings.Split(out, "\n") {
					if line = strings.TrimSpace(line); line != "" {
						req.Commits = append(req.Commits, line)
					}
				}
			}
		}
		for _, it := range todo.Load(todoProfile(), todo.Root(dir)).Ready() {
			req.Ready = append(req.Ready, it.Slug+": "+it.Title)
		}
		return req
	}
}
