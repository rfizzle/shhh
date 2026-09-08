package chat

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/observe"
)

// The reading — what moved, and what to say — is the agent's and is tested
// there. What a session adds is delivery, and the changeset as the
// subtrahend.

func treeRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	ws := t.TempDir()
	git := func(args ...string) { gitIn(t, ws, args...) }
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "init")
	return ws
}

// gitIn runs one git command in ws, with a developer's own configuration and
// identity kept out of it.
func gitIn(t *testing.T, ws string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", ws}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestTree_ANoticeReachesTheConversationAndTheTranscript(t *testing.T) {
	ws := treeRepo(t)
	var signals []string
	m := gatedModel(t, nil, nil).
		WithChangeset(changeset.New(changeset.DefaultMaxBytes), nil).
		WithTreeCheck(&agent.TreeCheck{Dir: ws})
	m = m.WithObserver(observe.Observer{Signal: func(_ observe.Pos, code, reason string) { signals = append(signals, code+":"+reason) }})
	m = advanceRounds(m, 2)
	before := m.agent.Rounds()

	if err := os.WriteFile(filepath.Join(ws, "theirs.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.injectTreeNotice(false)

	if got := lastUserMessage(m); !strings.Contains(got, "[tree: 1 path changed outside this session: theirs.txt]") {
		t.Errorf("the notice should be the last user message, got:\n%s", got)
	}
	if m.agent.Rounds() != before {
		t.Errorf("a tree notice must not move the round counter: %d → %d", before, m.agent.Rounds())
	}
	last := m.transcript[len(m.transcript)-1]
	if last.kind != entrySystem || !strings.Contains(last.text, "Tree moved") {
		t.Errorf("the notice should be visible in the transcript, got %q", last.text)
	}
	if len(signals) != 1 || signals[0] != observe.SignalTree+":paths" {
		t.Errorf("the notice is recorded once, got %v", signals)
	}
}

func TestTree_TheChangesetIsTheSubtrahend(t *testing.T) {
	ws := treeRepo(t)
	store := changeset.New(changeset.DefaultMaxBytes)
	m := gatedModel(t, nil, nil).
		WithChangeset(store, nil).
		WithTreeCheck(&agent.TreeCheck{Dir: ws})

	mine := filepath.Join(ws, "mine.txt")
	if err := os.WriteFile(mine, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store.Add(1, changeset.Record{Path: mine, After: "x\n", AfterExists: true})
	m.injectTreeNotice(false)
	if got := lastUserMessage(m); strings.Contains(got, "[tree:") {
		t.Errorf("a path the changeset recorded is the session's own, got:\n%s", got)
	}
}

// The instruction files are not the caller's to name: the session finds them
// the way the prompt found them, so what the notice calls the older reading
// is the block the model is actually holding.
func TestTree_TheInstructionFilesAreFoundWithoutBeingNamed(t *testing.T) {
	ws := treeRepo(t)
	agents := filepath.Join(ws, "AGENTS.md")
	if err := os.WriteFile(agents, []byte("the old rule\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Committed, so the file the notice names moved in content rather than
	// into the status for the first time.
	gitIn(t, ws, "add", "AGENTS.md")
	gitIn(t, ws, "commit", "-q", "-m", "instructions")

	m := gatedModel(t, nil, nil).
		WithChangeset(changeset.New(changeset.DefaultMaxBytes), nil).
		WithTreeCheck(&agent.TreeCheck{Dir: ws})

	if err := os.WriteFile(agents, []byte("the new rule\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.injectTreeNotice(false)

	if got := lastUserMessage(m); !strings.Contains(got, "The project instructions changed (AGENTS.md)") {
		t.Errorf("the notice should say the prompt block is the older reading, got:\n%s", got)
	}
}

func TestTree_NilLeavesTheReadingOff(t *testing.T) {
	m := gatedModel(t, nil, nil).WithTreeCheck(nil)
	if m.agent.TreeChecking() {
		t.Fatal("nil is off")
	}
}
