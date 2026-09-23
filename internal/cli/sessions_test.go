package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
)

// goldenSessions is a machine with three sessions on it: another process's,
// working with a sub-agent and an unattended run under it; this one's, not
// saved yet but in this checkout; and a third that has not saved and is
// somewhere else, so nothing can say where.
func goldenSessions() []runningSession {
	return []runningSession{
		{
			Slot: "2026-08-31 09:12:40", Kind: "code", PID: 4242, Root: "/work/shhh-b", Branch: "rebase-onto-main",
			Started: goldenNow.Add(-3 * time.Hour), State: sessionWorking,
			Children: []runningChild{
				{Name: "writer-1", Kind: "code", Started: goldenNow.Add(-20 * time.Minute), State: sessionWorking},
				{Kind: "print", Started: goldenNow.Add(-5 * time.Minute), State: sessionIdle},
			},
		},
		{
			Kind: "chat", PID: 4343, Root: "/work/shhh", Branch: "main",
			Started: goldenNow.Add(-2 * time.Minute), State: sessionIdle, Own: true,
			Children: []runningChild{},
		},
		{
			Kind: "code", PID: 4444, Started: goldenNow.Add(-time.Minute), State: sessionIdle,
			Children: []runningChild{},
		},
	}
}

func TestSessionsReportGoldens(t *testing.T) {
	assertReportGolden(t, "sessions", sessionsReport(goldenSessions(), goldenNow).Render(80))
	assertReportGolden(t, "sessions.empty", sessionsReport(nil, goldenNow).Render(80))
}

// The listing is the store's live reading with the checkout read off the
// slot: a session that saved says where it is, this process's own row is
// marked, and a child is under its session rather than a row of its own.
func TestRunningSessions_ReadsTheSlotAndMarksThisProcess(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	root := t.TempDir()
	if err := db.SaveChat("elsewhere", []provider.Message{{Role: provider.RoleUser, Content: "hi"}}); err != nil {
		t.Fatalf("save chat: %v", err)
	}
	if err := db.SetChatResume("elsewhere", storage.ChatResume{Root: root}); err != nil {
		t.Fatalf("set resume: %v", err)
	}
	// The parent is the one running process that is not this one a test can
	// name portably.
	other, err := db.StartAgentSession("code", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := db.SQL().Exec(`UPDATE agent_sessions SET pid = ? WHERE id = ?`, os.Getppid(), other); err != nil {
		t.Fatalf("place: %v", err)
	}
	if _, err := db.LinkAgentSession(other, "elsewhere"); err != nil {
		t.Fatalf("link: %v", err)
	}
	child, err := db.StartChildAgentSession(other, "code", "openai", "gpt-test", "writer-1")
	if err != nil {
		t.Fatalf("start child: %v", err)
	}
	if _, err := db.SQL().Exec(`UPDATE agent_sessions SET pid = ? WHERE id = ?`, os.Getppid(), child); err != nil {
		t.Fatalf("place child: %v", err)
	}
	own := startObserveRecorder(db, "chat", "openai", "gpt-test", nil)
	own.stamp("prompt", 0, projectFingerprintRoot(), storage.AgentSettings{})

	got, err := runningSessions(db, time.Now())
	if err != nil {
		t.Fatalf("runningSessions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("runningSessions = %+v, want two sessions", got)
	}
	if got[0].Slot != "elsewhere" || got[0].Root != root || got[0].Own {
		t.Fatalf("first = %+v, want the other process's session in %s", got[0], root)
	}
	if len(got[0].Children) != 1 || got[0].Children[0].Name != "writer-1" {
		t.Fatalf("children = %+v, want writer-1 under its session", got[0].Children)
	}
	if !got[1].Own || got[1].Slot != "" || got[1].Root == "" {
		t.Fatalf("second = %+v, want this process's unsaved session, marked, in this checkout", got[1])
	}

	// /sessions is the same report as the command's.
	listing := sessionsFor(db)()
	for _, want := range []string{"shhh sessions", "elsewhere", "writer-1", "this session"} {
		if !strings.Contains(listing, want) {
			t.Errorf("/sessions listing lacks %q:\n%s", want, listing)
		}
	}
	if sessionsFor(nil) != nil {
		t.Error("a session with no store was handed a listing to call")
	}

	// --json is the same list.
	cmd := newSessionsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := writeJSON(cmd, got); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	var back []runningSession
	if err := json.Unmarshal(out.Bytes(), &back); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if len(back) != 2 || back[0].Slot != "elsewhere" || !back[1].Own {
		t.Fatalf("json = %+v, want the same two sessions", back)
	}
}
