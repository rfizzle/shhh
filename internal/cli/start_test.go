package cli

import (
	"os"
	"path/filepath"
	"testing"

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
	info := buildStartInfo(nil, false)
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

	info := buildStartInfo(db, false)
	if !info.Recent.Present || info.Recent.Name != "loop refactor" {
		t.Fatalf("recent = %+v", info.Recent)
	}
	if info.Recent.Turns != 1 {
		t.Fatalf("turns = %d, want 1", info.Recent.Turns)
	}
}
