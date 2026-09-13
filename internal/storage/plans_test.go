package storage

import (
	"bytes"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

func TestPlanRecord_RoundTripsByOpaqueHandle(t *testing.T) {
	db := openTestDB(t)
	want := []byte(`{"task":"split the modes","steps":[{"number":1,"title":"add it"}]}`)
	handle, err := db.SavePlanRecord("2026-09-13 10:00:00", want)
	if err != nil {
		t.Fatal(err)
	}
	if handle == "" || handle == string(want) {
		t.Fatalf("handle must be opaque: %q", handle)
	}
	got, err := db.LoadPlanRecord(handle)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if _, err := db.LoadPlanRecord("plan-missing"); err == nil {
		t.Fatal("a handle the store does not have should be refused")
	}
	if _, err := db.SavePlanRecord("", nil); err == nil {
		t.Fatal("an empty record should not be given a handle")
	}
}

// The record outlives the conversation it was made in, which is the whole
// point of it: a fresh session opened from a plan is opened after the
// transcript behind the plan is gone.
func TestPlanRecord_SurvivesItsChatSession(t *testing.T) {
	db := openTestDB(t)
	const slot = "2026-09-13 10:00:00"
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "what would you do"},
		{Role: provider.RoleAssistant, Content: "1. do the thing"},
	}
	if err := db.SaveChat(slot, msgs); err != nil {
		t.Fatal(err)
	}
	handle, err := db.SavePlanRecord(slot, []byte(`{"task":"q","text":"do the thing"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteChat(slot); err != nil {
		t.Fatal(err)
	}
	if _, err := db.LoadPlanRecord(handle); err != nil {
		t.Fatalf("the plan went with the transcript it was made to replace: %v", err)
	}
}
