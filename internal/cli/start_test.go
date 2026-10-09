package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// testChatMessages is one saved exchange: one user turn, one reply.
func testChatMessages() []provider.Message {
	return []provider.Message{
		{Role: provider.RoleUser, Content: "where were we"},
		{Role: provider.RoleAssistant, Content: "right here"},
	}
}

// writeQualityConfig lays down a workspace quality config for the gate line.
func writeQualityConfig(t *testing.T, dir, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(quality.ConfigRelPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestStartGate_NamesTheDefaultSuiteAndItsChecks(t *testing.T) {
	dir := t.TempDir()
	writeQualityConfig(t, dir, `{"suites":{
		"default":{"checks":[{"name":"vet","exe":"go","args":["vet","./..."]},
		                     {"name":"test","exe":"go","args":["test","./..."]}]},
		"quick":{"checks":[{"name":"build","exe":"go","args":["build","./..."]}]}}}`)

	g := startGate(dir)
	if !g.Configured() || g.Suite != quality.DefaultSuite {
		t.Fatalf("suite = %q configured = %v", g.Suite, g.Configured())
	}
	if len(g.Checks) != 2 || g.Checks[0] != "vet" || g.Checks[1] != "test" {
		t.Fatalf("checks = %v, want [vet test]", g.Checks)
	}
	if g.Suites != 2 {
		t.Fatalf("suites = %d, want 2", g.Suites)
	}
	if g.Err != "" {
		t.Fatalf("unexpected error: %s", g.Err)
	}
}

func TestStartGate_FallsBackToTheOnlySuiteWhenThereIsNoDefault(t *testing.T) {
	dir := t.TempDir()
	writeQualityConfig(t, dir, `{"suites":{"ci":{"checks":[{"name":"test","exe":"go","args":["test"]}]}}}`)

	g := startGate(dir)
	if g.Suite != "ci" {
		t.Fatalf("suite = %q, want ci", g.Suite)
	}
}

func TestStartGate_AbsentConfigNamesTheFileItLookedFor(t *testing.T) {
	g := startGate(t.TempDir())
	if g.Configured() {
		t.Fatal("an empty workspace has no gate")
	}
	if g.Path != quality.ConfigRelPath {
		t.Fatalf("path = %q, want %q", g.Path, quality.ConfigRelPath)
	}
	if g.Err != "" {
		t.Fatalf("a missing file is not a broken one: %q", g.Err)
	}
}

func TestStartGate_BrokenConfigIsReportedRatherThanSwallowed(t *testing.T) {
	dir := t.TempDir()
	writeQualityConfig(t, dir, `{"suites":{"default":{"checks":[]}}}`)

	g := startGate(dir)
	if g.Configured() {
		t.Fatal("a config that does not load is not a configured gate")
	}
	if g.Err == "" {
		t.Fatal("a broken gate should say so")
	}
}

func TestBuildStartInfo_SurveysWithoutAGateOrADatabase(t *testing.T) {
	// Neither source is required: the screen still states the project.
	info := buildStartInfo(project.Survey(""), nil, false, chat.Trust{}, config.Project{}, nil)
	if info.Project.Dir == "" {
		t.Fatal("the survey should always name the directory it ran in")
	}
	if info.Gate.Configured() || info.Gate.Path != "" {
		t.Fatalf("a session without a gate should not claim one: %+v", info.Gate)
	}
	if info.Recent.Present {
		t.Fatal("no database means nothing to pick up")
	}
}

func TestBuildStartInfo_CarriesTheMostRecentSavedSession(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.SaveChat("loop refactor", testChatMessages()); err != nil {
		t.Fatalf("save: %v", err)
	}

	info := buildStartInfo(project.Survey(""), db, false, chat.Trust{}, config.Project{}, nil)
	if !info.Recent.Present || info.Recent.Name != "loop refactor" {
		t.Fatalf("recent = %+v", info.Recent)
	}
	if info.Recent.Turns != 1 {
		t.Fatalf("turns = %d, want 1", info.Recent.Turns)
	}
	if info.Recent.Handoff != "" {
		t.Fatalf("a slot with no handoff names none, got %q", info.Recent.Handoff)
	}

	// A handoff kept on it is named by its first line.
	if err := db.SetChatHandoff("loop refactor", "Loop split in two\nopen: the retry path"); err != nil {
		t.Fatalf("handoff: %v", err)
	}
	info = buildStartInfo(project.Survey(""), db, false, chat.Trust{}, config.Project{}, nil)
	if info.Recent.Handoff != "Loop split in two" {
		t.Fatalf("handoff = %q, want its first line", info.Recent.Handoff)
	}
}

// The offer is the newest conversation nobody else is writing into. Putting
// the slot a second session is autosaving into on the screen would offer a
// conversation that its owner's next save takes straight back.
func TestBuildStartInfo_OffersTheNewestSlotNobodyElseHolds(t *testing.T) {
	db := resumeStore(t)
	if err := db.SaveChat("loop refactor", testChatMessages()); err != nil {
		t.Fatalf("save: %v", err)
	}
	olderChat(t, db, "loop refactor")
	heldChat(t, db, "someone else's")

	info := buildStartInfo(project.Survey(""), db, false, chat.Trust{}, config.Project{}, nil)
	if !info.Recent.Present || info.Recent.Name != "loop refactor" {
		t.Fatalf("recent = %+v, want the newest slot nobody else holds", info.Recent)
	}
	// And the screen is told it is offering an older conversation, so the
	// row can say why rather than swapping the answer silently.
	if !info.Recent.Held {
		t.Fatal("the offer stepped past a slot and the screen was not told")
	}
}

// TestBuildScaffold_OffersOnceAndRemembersTheRefusal is the offer's whole
// life: made in a checkout with no state directory of its own, gone from the
// next session in that checkout once it has been refused, and never made
// where the file is already there.
func TestBuildScaffold_OffersOnceAndRemembersTheRefusal(t *testing.T) {
	dir := outsideAnyCheckout(t)
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	s := buildScaffold(db, dir)
	if !s.Offer {
		t.Fatal("a checkout with no state directory should be offered the scaffold")
	}
	if s.Write == nil || s.Decline == nil {
		t.Fatal("the offer has no write or no way to refuse it")
	}

	if err := s.Decline(); err != nil {
		t.Fatalf("decline: %v", err)
	}
	if buildScaffold(db, dir).Offer {
		t.Fatal("the next session offered a scaffold that was already refused")
	}

	// A different checkout is a different answer.
	other := outsideAnyCheckout(t)
	if !buildScaffold(db, other).Offer {
		t.Fatal("one checkout's refusal answered for another")
	}
	if _, err := buildScaffold(db, other).Write(); err != nil {
		t.Fatalf("write: %v", err)
	}
	if buildScaffold(db, other).Offer {
		t.Fatal("a checkout that already has the file was offered it")
	}
}

// The offer is the project's, so it is decided, written and remembered at
// the repository root — a session started two directories down must not
// scaffold where it stands.
func TestBuildScaffold_ActsAtTheRepositoryRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "internal", "cli")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	path, err := buildScaffold(db, sub).Write()
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if want := filepath.Join(root, filepath.FromSlash(project.ContextFile)); path != want {
		t.Fatalf("wrote %q, want the repository's own %q", path, want)
	}
	if _, err := os.Stat(filepath.Join(sub, project.StateDir)); err == nil {
		t.Fatal("a second state directory was written in the subdirectory")
	}
	// And the offer is now answered for the whole checkout, from anywhere
	// in it.
	if buildScaffold(db, sub).Offer {
		t.Fatal("the subdirectory was offered a scaffold the repository already has")
	}
	if buildScaffold(db, root).Offer {
		t.Fatal("the root was offered a scaffold it already has")
	}
}

// Without a store the refusal has nowhere to live, so nothing is offered:
// an offer that cannot be refused for good is a nag.
func TestBuildScaffold_MakesNoOfferItCannotRemember(t *testing.T) {
	s := buildScaffold(nil, t.TempDir())
	if s.Offer {
		t.Fatal("an offer was made with nowhere to record the answer")
	}
	if s.Write == nil {
		t.Fatal("/init should still work without a store")
	}
}

// One reading, in the two shapes the surfaces need it, and neither of them
// stops a session from starting when there is no store to ask.
func TestReadSibling_WithoutAStoreNobodyElseIsHere(t *testing.T) {
	sib := readSibling(nil)
	if !sib.since().IsZero() || sib.live() {
		t.Fatal("no store should answer with nothing")
	}
	if withSibling(nil, sib) != nil {
		t.Fatal("a tree reading that is off stays off")
	}
	c := withSibling(&agent.TreeCheck{Dir: "."}, sib)
	if c.Sibling != nil {
		t.Fatal("a reading with no store should hand the tree no answer to ask for")
	}
}

func TestReadSibling_HandsTheTreeReadingTheLiveHalf(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	c := withSibling(&agent.TreeCheck{Dir: "."}, readSibling(db))
	if c.Sibling == nil {
		t.Fatal("the tree reading was handed no way to ask")
	}
	if c.Sibling() {
		t.Fatal("an empty store answered that somebody else is here")
	}
}

// The checkout's own settings file rides on the survey, so the screen can
// name what this session is running on: a session running on settings the
// reader never wrote is one they cannot account for from their own file.
func TestBuildStartInfo_CarriesTheCheckoutsSettingsFile(t *testing.T) {
	proj := config.Project{Path: "/repo/.shhh/config.toml", Display: ".shhh/config.toml",
		Keys: []string{"behavior.default_mode"}}
	info := buildStartInfo(project.Survey(""), nil, false, chat.Trust{}, proj, nil)
	if info.Project.ConfigFile != ".shhh/config.toml" {
		t.Fatalf("the checkout's settings file is not on the survey: %q", info.Project.ConfigFile)
	}
	// Nothing to say where the checkout has no file of its own, which is
	// almost every repository.
	bare := buildStartInfo(project.Survey(""), nil, false, chat.Trust{}, config.Project{}, nil)
	if bare.Project.ConfigFile != "" {
		t.Fatalf("a checkout with no settings file named one: %q", bare.Project.ConfigFile)
	}
}

func TestStartBranch_CountsWhatIsAheadOfTheDefaultAndWhetherItWentUp(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "seed")
	if got := startBranch(dir, "main"); got != (chat.StartBranch{}) {
		t.Fatalf("the default branch is ahead of itself: %+v", got)
	}
	run("checkout", "-q", "-b", "work")
	run("commit", "-q", "--allow-empty", "-m", "one")
	run("commit", "-q", "--allow-empty", "-m", "two")
	if got := startBranch(dir, "work"); got.Ahead != 2 || got.Pushed {
		t.Fatalf("branch = %+v, want 2 ahead and not pushed", got)
	}
	// The branch is its own upstream, which is what pushed means: nothing
	// the upstream lacks.
	run("config", "branch.work.remote", ".")
	run("config", "branch.work.merge", "refs/heads/work")
	if got := startBranch(dir, "work"); got.Ahead != 2 || !got.Pushed {
		t.Fatalf("branch = %+v, want 2 ahead and pushed", got)
	}
}

// The start screen's reading is handed names and subjects only: the changed
// files' names and the last ten commit subjects, never a file's contents.
func TestStartOffersEvidence_NamesAndSubjectsOnly(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for i := range 12 {
		run("commit", "-q", "--allow-empty", "-m", "subject "+strconv.Itoa(i))
	}
	if err := os.WriteFile(filepath.Join(dir, "cache.go"), []byte("package cache // the secret body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := startOffersEvidence(project.Info{Dir: dir, Repo: true})(context.Background())
	if len(req.Dirty) != 1 || req.Dirty[0] != "cache.go" {
		t.Fatalf("dirty = %q, want the one name", req.Dirty)
	}
	if len(req.Commits) != 10 || req.Commits[0] != "subject 11" {
		t.Fatalf("commits = %q, want the last ten, newest first", req.Commits)
	}
	if strings.Contains(strings.Join(append(req.Dirty, req.Commits...), " "), "secret body") {
		t.Fatal("a file's contents reached the reading")
	}
}

func TestStartBranch_CountsAgainstTheRemoteDefaultWhenThereIsNoLocalOne(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "seed")
	// The remote's default exists only as origin/main; there is no local main.
	run("update-ref", "refs/remotes/origin/main", "HEAD")
	run("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	run("checkout", "-q", "-b", "work")
	run("branch", "-q", "-D", "main")
	run("commit", "-q", "--allow-empty", "-m", "one")
	if got := startBranch(dir, "work"); got.Ahead != 1 || got.Pushed {
		t.Fatalf("branch = %+v, want 1 ahead of origin/main and not pushed", got)
	}
	// Neither a local nor a remote copy: nothing to verify, no offer.
	run("update-ref", "-d", "refs/remotes/origin/main")
	if got := startBranch(dir, "work"); got != (chat.StartBranch{}) {
		t.Fatalf("branch = %+v, want no offer without any default", got)
	}
}
