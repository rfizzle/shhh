package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/storage"
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
	info := buildStartInfo(project.Survey(""), nil, false)
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

	info := buildStartInfo(project.Survey(""), db, false)
	if !info.Recent.Present || info.Recent.Name != "loop refactor" {
		t.Fatalf("recent = %+v", info.Recent)
	}
	if info.Recent.Turns != 1 {
		t.Fatalf("turns = %d, want 1", info.Recent.Turns)
	}
}

// TestBuildScaffold_OffersOnceAndRemembersTheRefusal is the offer's whole
// life: made in a checkout with no state directory of its own, gone from the
// next session in that checkout once it has been refused, and never made
// where the file is already there.
func TestBuildScaffold_OffersOnceAndRemembersTheRefusal(t *testing.T) {
	dir := t.TempDir()
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
	other := t.TempDir()
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
