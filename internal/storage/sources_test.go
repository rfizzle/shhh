package storage

import (
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/web"
)

func TestSources_RoundTripUnderTheSlot(t *testing.T) {
	db := openTestDB(t)
	slot, err := db.ClaimChatSlot("2026-09-07")
	if err != nil {
		t.Fatalf("claim slot: %v", err)
	}

	rows := []web.Source{
		{Turn: 1, Agent: "orchestrator", Kind: web.KindSearch, Query: "tokio", Results: 3, At: time.Now()},
		{Turn: 2, Agent: "web-researcher", Kind: web.KindFetch,
			Requested: "https://docs.rs/tokio", FinalURL: "https://docs.rs/tokio/latest/",
			Title: "tokio", Status: 200, Bytes: 4096, Cached: true, Evidence: "ev-1", At: time.Now()},
	}
	for _, r := range rows {
		if _, err := db.SaveSource(slot, r); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	got, err := db.LoadSources(slot)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("loaded %d rows", len(got))
	}
	if got[0].Kind != web.KindSearch || got[0].Query != "tokio" || got[0].Results != 3 {
		t.Errorf("search row = %+v", got[0])
	}
	fetch := got[1]
	if fetch.Agent != "web-researcher" || fetch.FinalURL != "https://docs.rs/tokio/latest/" ||
		fetch.Requested != "https://docs.rs/tokio" || fetch.Status != 200 ||
		fetch.Bytes != 4096 || !fetch.Cached || fetch.Evidence != "ev-1" || fetch.Turn != 2 {
		t.Errorf("fetch row = %+v", fetch)
	}
	if fetch.At.IsZero() {
		t.Error("the row came back with no time on it")
	}
	if fetch.ID == 0 || fetch.ID == got[0].ID {
		t.Errorf("ids = %d, %d", got[0].ID, fetch.ID)
	}
}

// A slot nothing ever claimed — a headless run — is not an error: the ledger
// keeps the row for the session and only the resume loses it.
func TestSources_AnUnclaimedSlotIsNotAnError(t *testing.T) {
	db := openTestDB(t)
	id, err := db.SaveSource("never-claimed", web.Source{Kind: web.KindFetch, FinalURL: "https://go.dev/"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if id != 0 {
		t.Errorf("id = %d, want 0 for a slot with no row", id)
	}
	rows, err := db.LoadSources("never-claimed")
	if err != nil || len(rows) != 0 {
		t.Errorf("load = %v, %v", rows, err)
	}
}

// The rows hang off the slot's row id, so deleting the conversation takes
// its ledger with it rather than leaving a list of URLs nothing explains.
func TestSources_DeletingTheChatTakesItsLedger(t *testing.T) {
	db := openTestDB(t)
	slot, err := db.ClaimChatSlot("2026-09-07")
	if err != nil {
		t.Fatalf("claim slot: %v", err)
	}
	if _, err := db.SaveSource(slot, web.Source{Kind: web.KindFetch, FinalURL: "https://go.dev/"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := db.DeleteChat(slot); err != nil {
		t.Fatalf("delete chat: %v", err)
	}
	var left int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM sources`).Scan(&left); err != nil {
		t.Fatalf("count: %v", err)
	}
	if left != 0 {
		t.Errorf("%d rows outlived the conversation", left)
	}
}
