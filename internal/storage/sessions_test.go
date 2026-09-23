package storage

import (
	"os"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

func TestLiveSessions_ListsSessionsAPersonCanOpenWithTheirChildrenUnderThem(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()
	liveOnly(t, os.Getpid(), 4242, 4343)

	// Another process's coding session, writing a slot that says where it is.
	if err := db.SaveChat("elsewhere", []provider.Message{{Role: provider.RoleUser, Content: "hi"}}); err != nil {
		t.Fatalf("save chat: %v", err)
	}
	if err := db.SetChatResume("elsewhere", ChatResume{Root: "/work/b"}); err != nil {
		t.Fatalf("set resume: %v", err)
	}
	other := openSessionIn(t, db, "checkout-b", 4242, now.Add(-time.Hour))
	if _, err := db.LinkAgentSession(other, "elsewhere"); err != nil {
		t.Fatalf("link: %v", err)
	}
	// Its sub-agent is working, so the session it hangs under is too; a
	// grandchild is listed under the same row.
	child, err := db.StartChildAgentSession(other, "code", "openai", "gpt-test", "writer-1")
	if err != nil {
		t.Fatalf("start child: %v", err)
	}
	grand, err := db.StartChildAgentSession(child, "code", "openai", "gpt-test", "reader-1a")
	if err != nil {
		t.Fatalf("start grandchild: %v", err)
	}
	if _, err := db.SQL().Exec(`UPDATE agent_sessions SET pid = 4242 WHERE id IN (?, ?)`, child, grand); err != nil {
		t.Fatalf("place children: %v", err)
	}
	time.Sleep(2 * time.Millisecond) // a beat a clock tick after the start
	if err := db.UpdateAgentSession(child, 1, 100, 10, 0); err != nil {
		t.Fatalf("beat child: %v", err)
	}

	// This process's own conversation, not saved yet.
	own, err := db.StartAgentSession("chat", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start own: %v", err)
	}

	// None of these is a session a person can open: an unattended run with
	// no session around it, a session whose process is gone, and one that
	// ended.
	if _, err := db.StartAgentSession("print", "openai", "gpt-test"); err != nil {
		t.Fatalf("start print: %v", err)
	}
	openSessionIn(t, db, "checkout-a", 9999, now)
	ended := openSessionIn(t, db, "checkout-a", 4343, now)
	if err := db.EndAgentSession(ended, ""); err != nil {
		t.Fatalf("end: %v", err)
	}

	got, err := db.LiveSessions(now)
	if err != nil {
		t.Fatalf("LiveSessions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("LiveSessions = %+v, want the other process's session and this one's", got)
	}
	first, second := got[0], got[1]
	if first.ID != other || first.Slot != "elsewhere" || first.Root != "/work/b" || first.Own || first.PID != 4242 {
		t.Fatalf("first row = %+v, want the other process's session in /work/b", first)
	}
	if len(first.Children) != 2 || first.Children[0].Name != "writer-1" || first.Children[1].Name != "reader-1a" {
		t.Fatalf("children = %+v, want writer-1 then reader-1a under their session", first.Children)
	}
	if !first.Working {
		t.Fatal("a session whose children are working read as idle")
	}
	if second.ID != own || !second.Own || second.Slot != "" || second.Root != "" {
		t.Fatalf("second row = %+v, want this process's unsaved session, marked", second)
	}
	if second.Working {
		t.Fatal("a session nothing has answered yet read as working")
	}

	if pid, ok, err := db.LiveSessionPID("elsewhere", now); err != nil || !ok || pid != 4242 {
		t.Fatalf("LiveSessionPID(elsewhere) = %d, %v, %v, want 4242", pid, ok, err)
	}
	if _, ok, _ := db.LiveSessionPID("nobody", now); ok {
		t.Fatal("LiveSessionPID found a process for a slot no session is writing")
	}
}

func TestLiveSessions_AnAnsweredRequestIsWorkAndSilenceIsIdle(t *testing.T) {
	db := openTestDB(t)
	liveOnly(t, 4242)

	id := openSessionIn(t, db, "checkout-a", 4242, time.Now().Add(-time.Hour))
	got, err := db.LiveSessions(time.Now())
	if err != nil || len(got) != 1 || got[0].Working {
		t.Fatalf("a session last heard from an hour ago = %+v, %v, want one idle row", got, err)
	}
	// Totals arrive with every answered request, and they beat the row — a
	// clock tick after the start, which is what tells it from the first beat.
	time.Sleep(2 * time.Millisecond)
	if err := db.UpdateAgentSession(id, 1, 100, 10, 0); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err = db.LiveSessions(time.Now())
	if err != nil || len(got) != 1 || !got[0].Working {
		t.Fatalf("a session that just had a request answered = %+v, %v, want working", got, err)
	}
}
